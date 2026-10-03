import AVFoundation
import MediaPlayer
import UIKit
import WebKit

/// The page's audio element, played by iOS itself (the Android app's
/// NativeAudio, in Swift). Reported as: paused from the lock screen, the music
/// could not be started again there. The page played the music in its web
/// view, and a few seconds after a pause iOS suspends an app that is playing
/// nothing - the web view with it - so the lock screen's play had nothing
/// awake to answer it. A player of the app's own is woken by iOS for its
/// remote commands, and plays on from where it was.
///
/// The page script gives `<audio id="audio-player">` the Android app's
/// stand-in (`window.soundstormApp.nativeAudio`): a song on the server set
/// as its src, play, pause, seeking, volume and speed arrive here as "audio"
/// messages, and what the player does goes back through
/// `window.__soundstormAudio` as the element's own events. A downloaded song
/// (a blob: address) plays in the page as before. The page hands over the
/// songs ahead (`upcoming`), so the player moves into them by itself, with
/// the page asleep and the lock screen naming each.
@MainActor
final class NativeAudio: NSObject {
    static let shared = NativeAudio()

    weak var webView: WKWebView?
    /// Where the page is: only its own addresses are played.
    var isServer: (URL) -> Bool = { _ in false }

    private let player = AVQueuePlayer()
    private var urls: [ObjectIdentifier: String] = [:]
    private var upcomingMeta: [String: Meta] = [:]
    private var lastItem: AVPlayerItem?
    private var ended = false
    private var seeking = false
    private var rate: Float = 1
    private var failed = false
    private var timeObserver: Any?
    private var observations: [NSKeyValueObservation] = []
    /// Messages are run in order: a load waits for the cookies, and the play
    /// sent straight after it must not overtake it.
    private var chain: Task<Void, Never>?

    /// What the page says is playing, and which lock-screen buttons it has.
    private var meta = Meta()
    private var actions: Set<String> = []
    private var artwork: (url: String, image: MPMediaItemArtwork)?

    struct Meta: Equatable {
        var title = "", artist = "", album = "", art = ""
    }

    private override init() {
        super.init()
        player.actionAtItemEnd = .advance
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: 1, timescale: 2), queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
        observations.append(player.observe(\.timeControlStatus) { [weak self] _, _ in
            DispatchQueue.main.async { self?.changed() }
        })
        observations.append(player.observe(\.currentItem) { [weak self] _, _ in
            DispatchQueue.main.async { self?.itemChanged() }
        })
        NotificationCenter.default.addObserver(self, selector: #selector(playedToEnd(_:)),
                                               name: .AVPlayerItemDidPlayToEndTime, object: nil)
        NotificationCenter.default.addObserver(self, selector: #selector(interrupted(_:)),
                                               name: AVAudioSession.interruptionNotification, object: nil)
        commands()
    }

    // MARK: From the page

    func handle(_ m: [String: Any]) {
        let previous = chain
        chain = Task {
            await previous?.value
            await run(m)
        }
    }

    private func run(_ m: [String: Any]) async {
        switch m["cmd"] as? String ?? "" {
        case "load":
            guard let url = allowed(m["url"]) else { return }
            if let current = player.currentItem, urls[ObjectIdentifier(current)] == url.absoluteString {
                // Already this song: moved into it by itself, or asked for
                // again after it ended, which starts it over.
                if ended { await player.seek(to: .zero); ended = false }
                report()
                return
            }
            let items = player.items()
            if items.count > 1, urls[ObjectIdentifier(items[1])] == url.absoluteString {
                // The one queued next, reached early (a skip).
                ended = false
                player.advanceToNextItem()
            } else {
                let item = await makeItem(url)
                player.removeAllItems()
                ended = false
                failed = false
                player.insert(item, after: nil)
            }
            report()
        case "queue":
            guard let url = allowed(m["url"]), player.currentItem != nil else { return }
            dropAfterCurrent()
            player.insert(await makeItem(url), after: player.items().last)
        case "upcoming":
            guard player.currentItem != nil else { return }
            dropAfterCurrent()
            for o in (m["items"] as? [[String: Any]] ?? []).prefix(50) {
                guard let url = allowed(o["url"]) else { break }
                upcomingMeta[url.absoluteString] = Meta(title: o["title"] as? String ?? "", artist: o["artist"] as? String ?? "",
                                                        album: o["album"] as? String ?? "", art: o["art"] as? String ?? "")
                player.insert(await makeItem(url), after: player.items().last)
            }
        case "unqueue":
            dropAfterCurrent()
        case "play":
            play()
        case "pause":
            player.pause()
        case "state":
            report()
        case "seek":
            let s = max(0, (m["s"] as? NSNumber)?.doubleValue ?? 0)
            seeking = true
            ended = false
            await player.seek(to: CMTime(seconds: s, preferredTimescale: 1000), toleranceBefore: .zero, toleranceAfter: .zero)
            report()
        case "volume":
            player.volume = min(1, max(0, (m["v"] as? NSNumber)?.floatValue ?? 1))
        case "rate":
            rate = min(4, max(0.25, (m["r"] as? NSNumber)?.floatValue ?? 1))
            player.defaultRate = rate
            if player.rate != 0 { player.rate = rate }
        case "stop":
            player.pause()
            player.removeAllItems()
            urls.removeAll()
            ended = false
            MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
            report()
        default:
            break
        }
    }

    /// What the page's media session says: the song, and its buttons.
    func media(_ m: [String: Any]) {
        actions = Set(m["actions"] as? [String] ?? [])
        if let d = m["metadata"] as? [String: Any] {
            meta = Meta(title: d["title"] as? String ?? "", artist: d["artist"] as? String ?? "",
                        album: d["album"] as? String ?? "", art: (d["artwork"] as? [String])?.first ?? "")
        }
        enableCommands()
        nowPlaying()
    }

    private func allowed(_ value: Any?) -> URL? {
        guard let s = value as? String, let url = URL(string: s), url.scheme == "http" || url.scheme == "https",
              isServer(url) else { return nil }
        return url
    }

    private func dropAfterCurrent() {
        for item in player.items().dropFirst() { player.remove(item) }
    }

    private func makeItem(_ url: URL) async -> AVPlayerItem {
        // The player does not read the web view's cookies: the session's are
        // handed to it, as the Apple TV does.
        let cookies = await cookies(for: url)
        let asset = AVURLAsset(url: url, options: [AVURLAssetHTTPCookiesKey: cookies])
        let item = AVPlayerItem(asset: asset)
        urls[ObjectIdentifier(item)] = url.absoluteString
        observations.append(item.observe(\.status) { [weak self] item, _ in
            DispatchQueue.main.async {
                guard let self, item.status == .failed, item === self.player.currentItem else { return }
                self.failed = true
                self.report()
            }
        })
        return item
    }

    private func cookies(for url: URL) async -> [HTTPCookie] {
        guard let host = url.host()?.lowercased() else { return [] }
        let all = await WKWebsiteDataStore.default().httpCookieStore.allCookies()
        return all.filter { c in
            let domain = c.domain.lowercased()
            return domain.hasPrefix(".") ? (host == String(domain.dropFirst()) || host.hasSuffix(domain)) : host == domain
        }
    }

    private func play() {
        try? AVAudioSession.sharedInstance().setActive(true)
        if ended, player.currentItem != nil {
            player.seek(to: .zero)
            ended = false
        }
        player.defaultRate = rate
        player.play()
    }

    // MARK: What the player did

    private func itemChanged() {
        let item = player.currentItem
        defer { lastItem = item }
        guard let last = lastItem, last !== item else { return }
        urls[ObjectIdentifier(last)] = nil
        // Moved into the queued song by itself: the page hears the last one
        // end, and sets this one, which it finds already playing.
        if let item, let next = urls[ObjectIdentifier(item)] {
            if let upcoming = upcomingMeta[next] { meta = upcoming; nowPlaying() }
            send(["ev": "ended", "next": next])
        }
    }

    @objc private func playedToEnd(_ note: Notification) {
        // The last song in the queue: the player stops on it.
        guard let item = note.object as? AVPlayerItem, item === player.currentItem,
              player.items().count <= 1 else { return }
        ended = true
        report()
    }

    @objc private func interrupted(_ note: Notification) {
        // A call or an alarm over: play on if iOS says so.
        guard let raw = note.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt,
              AVAudioSession.InterruptionType(rawValue: raw) == .ended,
              let o = note.userInfo?[AVAudioSessionInterruptionOptionKey] as? UInt,
              AVAudioSession.InterruptionOptions(rawValue: o).contains(.shouldResume),
              player.currentItem != nil else { return }
        play()
    }

    private func changed() {
        report()
        nowPlaying()
    }

    private func tick() {
        if player.timeControlStatus == .playing { report() }
    }

    private func state() -> [String: Any] {
        guard let item = player.currentItem else {
            return ["ev": "state", "state": "idle", "playing": false, "pwr": false]
        }
        let status = player.timeControlStatus
        let duration = item.duration.seconds
        let buffered = item.loadedTimeRanges.last.map { $0.timeRangeValue.end.seconds } ?? 0
        var o: [String: Any] = [
            "ev": "state",
            "state": ended ? "ended" : status == .waitingToPlayAtSpecifiedRate ? "buffering" : "ready",
            "playing": status == .playing,
            "pwr": status != .paused,
            "url": urls[ObjectIdentifier(item)] ?? "",
            "position": max(0, player.currentTime().seconds.isFinite ? player.currentTime().seconds : 0),
            "buffered": buffered.isFinite ? buffered : 0,
            "rate": Double(rate),
            "volume": Double(player.volume),
            "seeked": seeking && status != .waitingToPlayAtSpecifiedRate,
        ]
        if duration.isFinite, duration > 0 { o["duration"] = duration }
        if failed { o["error"] = 4 }
        return o
    }

    private func report() {
        let s = state()
        if s["seeked"] as? Bool == true { seeking = false }
        send(s)
    }

    private func send(_ event: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: event),
              let json = String(data: data, encoding: .utf8) else { return }
        webView?.evaluateJavaScript("window.__soundstormAudio && window.__soundstormAudio(\(json))")
    }

    /// A lock-screen button the page answers (next, previous, a jump).
    private func pageAction(_ action: String, _ details: [String: Any] = [:]) {
        guard let data = try? JSONSerialization.data(withJSONObject: details),
              let json = String(data: data, encoding: .utf8) else { return }
        webView?.evaluateJavaScript("window.__soundstormMediaAction && window.__soundstormMediaAction(\"\(action)\", \(json))")
    }

    #if DEBUG
    /// What the lock screen's play does, for the simulator's check.
    func debugRemotePlay() { play() }
    #endif

    // MARK: Lock screen, Control Centre, headphones, a car

    private func commands() {
        let c = MPRemoteCommandCenter.shared()
        // Play and pause are the player's own, so they work with the page
        // asleep - which is the whole point.
        c.playCommand.addTarget { [weak self] _ in
            guard let self, self.player.currentItem != nil else { return .noActionableNowPlayingItem }
            self.play()
            return .success
        }
        c.pauseCommand.addTarget { [weak self] _ in
            self?.player.pause()
            return .success
        }
        c.togglePlayPauseCommand.addTarget { [weak self] _ in
            guard let self, self.player.currentItem != nil else { return .noActionableNowPlayingItem }
            if self.player.timeControlStatus == .paused { self.play() } else { self.player.pause() }
            return .success
        }
        c.nextTrackCommand.addTarget { [weak self] _ in
            self?.pageAction("nexttrack")
            return .success
        }
        c.previousTrackCommand.addTarget { [weak self] _ in
            guard let self else { return .commandFailed }
            if self.actions.contains("previoustrack") { self.pageAction("previoustrack") } else { self.player.seek(to: .zero) }
            return .success
        }
        c.skipForwardCommand.preferredIntervals = [15]
        c.skipBackwardCommand.preferredIntervals = [15]
        c.skipForwardCommand.addTarget { [weak self] _ in
            self?.pageAction("seekforward")
            return .success
        }
        c.skipBackwardCommand.addTarget { [weak self] _ in
            self?.pageAction("seekbackward")
            return .success
        }
        c.changePlaybackPositionCommand.addTarget { [weak self] event in
            guard let self, let event = event as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
            if self.actions.contains("seekto") {
                self.pageAction("seekto", ["seekTime": event.positionTime])
            } else {
                self.player.seek(to: CMTime(seconds: event.positionTime, preferredTimescale: 1000))
            }
            return .success
        }
        enableCommands()
    }

    private func enableCommands() {
        let c = MPRemoteCommandCenter.shared()
        // An audiobook gets the jumps, not next and previous (as the page has it).
        let next = actions.contains("nexttrack")
        c.nextTrackCommand.isEnabled = next
        c.previousTrackCommand.isEnabled = next || actions.contains("previoustrack")
        c.skipForwardCommand.isEnabled = !next && actions.contains("seekforward")
        c.skipBackwardCommand.isEnabled = !next && actions.contains("seekbackward")
    }

    private func nowPlaying() {
        guard let item = player.currentItem else { return }
        var info: [String: Any] = [
            MPMediaItemPropertyTitle: meta.title,
            MPMediaItemPropertyArtist: meta.artist,
            MPMediaItemPropertyAlbumTitle: meta.album,
            MPNowPlayingInfoPropertyElapsedPlaybackTime: max(0, player.currentTime().seconds.isFinite ? player.currentTime().seconds : 0),
            MPNowPlayingInfoPropertyPlaybackRate: player.timeControlStatus == .playing ? Double(rate) : 0,
            MPNowPlayingInfoPropertyDefaultPlaybackRate: Double(rate),
        ]
        if item.duration.seconds.isFinite { info[MPMediaItemPropertyPlaybackDuration] = item.duration.seconds }
        if let artwork, artwork.url == meta.art { info[MPMediaItemPropertyArtwork] = artwork.image }
        MPNowPlayingInfoCenter.default().nowPlayingInfo = info
        if !meta.art.isEmpty, artwork?.url != meta.art { loadArtwork(meta.art) }
    }

    private func loadArtwork(_ address: String) {
        guard let url = allowed(address) else { return }
        Task {
            var request = URLRequest(url: url)
            for (field, value) in HTTPCookie.requestHeaderFields(with: await cookies(for: url)) {
                request.setValue(value, forHTTPHeaderField: field)
            }
            guard let (data, _) = try? await URLSession.shared.data(for: request), data.count < 8 << 20,
                  let image = UIImage(data: data)?.preparingThumbnail(of: CGSize(width: 600, height: 600)) else { return }
            let art = MPMediaItemArtwork(boundsSize: image.size) { @Sendable _ in image }
            artwork = (address, art)
            nowPlaying()
        }
    }
}
