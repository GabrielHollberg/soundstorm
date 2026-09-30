import AVFoundation
import MediaPlayer
import UIKit

/// Music, played by tvOS itself. AVQueuePlayer holds the song playing and the
/// next, so the next is already loading when this one ends (gapless, as the
/// web page's preload is). The system's Now Playing - the TV's Control Center
/// and the remote's media keys - is told what plays and answers here.
@Observable
final class Player {
    private(set) var queue: [Item] = []
    private(set) var index = 0
    private(set) var isPlaying = false
    private(set) var time: Double = 0
    private(set) var duration: Double = 0

    var current: Item? { queue.indices.contains(index) ? queue[index] : nil }

    private let api: API
    private let player = AVQueuePlayer()
    private var timeObserver: Any?
    private var endObserver: NSObjectProtocol?
    private var counted = false
    /// The AVPlayerItem of the song playing, and of the one loaded after it.
    private var playing: AVPlayerItem?
    private var upNext: AVPlayerItem?
    private var artwork: MPMediaItemArtwork?

    init(api: API) {
        self.api = api
        player.actionAtItemEnd = .advance
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: 1, timescale: 2), queue: .main) { [weak self] t in
            MainActor.assumeIsolated { self?.tick(t.seconds) }
        }
        endObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.didPlayToEndTimeNotification, object: nil, queue: .main
        ) { [weak self] note in
            let ended = note.object as? AVPlayerItem
            MainActor.assumeIsolated { self?.itemEnded(ended) }
        }
        setUpRemoteCommands()
    }

    // MARK: Control

    func play(_ items: [Item], from start: Int = 0) {
        let songs = items.filter { $0.kind == "music" }
        guard !songs.isEmpty else { return }
        let first = songs.firstIndex(of: items[min(start, items.count - 1)]) ?? 0
        station = nil
        queue = songs
        load(at: first)
    }

    /// A radio station: its first songs now, and more before they run out.
    func play(station: API.Station, first batch: API.Batch) {
        guard !batch.songs.isEmpty else { return }
        queue = batch.songs
        self.station = station
        stationFrom = batch.next
        load(at: 0)
    }

    /// Any song in the queue, from Up next.
    func jump(to i: Int) {
        guard queue.indices.contains(i) else { return }
        load(at: i)
    }

    private(set) var station: API.Station?
    private var stationFrom: Int?
    private var tuning = false

    /// Two songs from the end of a station, its next batch is fetched and
    /// added, leaving out everything already in the queue.
    private func topUpStation() {
        guard let station, !tuning, index + 2 >= queue.count else { return }
        tuning = true
        Task {
            defer { tuning = false }
            guard let batch = try? await api.tune(station, exclude: queue.map(\.id), from: stationFrom),
                  self.station == station
            else { return }
            let known = Set(queue.map(\.key))
            queue += batch.songs.filter { !known.contains($0.key) }
            stationFrom = batch.next ?? stationFrom
            enqueueNext()
        }
    }

    func togglePlay() { isPlaying ? pause() : resume() }

    func resume() {
        guard current != nil else { return }
        player.play()
        isPlaying = true
        publish()
    }

    func pause() {
        player.pause()
        isPlaying = false
        publish()
    }

    func next() {
        guard index + 1 < queue.count else { return }
        load(at: index + 1)
    }

    /// Back to the start of the song, or the one before within its first
    /// three seconds - the rule every player has, the web page's included.
    func previous() {
        if time > 3 || index == 0 {
            seek(to: 0)
        } else {
            load(at: index - 1)
        }
    }

    func seek(to seconds: Double) {
        let t = max(0, min(seconds, duration > 0 ? duration - 0.5 : seconds))
        player.seek(to: CMTime(seconds: t, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
        time = t
        publish()
    }

    func skip(by seconds: Double) { seek(to: time + seconds) }

    func stop() {
        station = nil
        player.removeAllItems()
        playing = nil
        upNext = nil
        queue = []
        index = 0
        isPlaying = false
        time = 0
        duration = 0
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
    }

    // MARK: Queue

    private func load(at i: Int) {
        index = i
        counted = false
        time = 0
        duration = queue[i].durationSeconds ?? 0
        player.removeAllItems()
        upNext = nil
        playing = playerItem(queue[i])
        player.insert(playing!, after: nil)
        enqueueNext()
        try? AVAudioSession.sharedInstance().setActive(true)
        player.play()
        isPlaying = true
        artwork = nil
        publish()
        loadArtwork(for: queue[i])
    }

    /// The song after this one, loading while this one plays.
    private func enqueueNext() {
        topUpStation()
        guard index + 1 < queue.count, let playing, upNext == nil else { return }
        let next = playerItem(queue[index + 1])
        player.insert(next, after: playing)
        upNext = next
    }

    private func playerItem(_ item: Item) -> AVPlayerItem {
        let asset = AVURLAsset(url: api.streamURL(item), options: [AVURLAssetHTTPCookiesKey: api.cookies])
        return AVPlayerItem(asset: asset)
    }

    private func itemEnded(_ ended: AVPlayerItem?) {
        // Only the song that was playing: an item from a queue since replaced
        // can still report its end.
        guard let ended, ended === playing else { return }
        if index + 1 < queue.count, let upNext {
            // AVQueuePlayer has already moved on to the preloaded next.
            playing = upNext
            self.upNext = nil
            index += 1
            counted = false
            time = 0
            duration = queue[index].durationSeconds ?? 0
            enqueueNext()
            artwork = nil
            publish()
            loadArtwork(for: queue[index])
        } else {
            isPlaying = false
            time = 0
            publish()
        }
    }

    private func tick(_ seconds: Double) {
        guard seconds.isFinite else { return }
        time = seconds
        if let d = player.currentItem?.duration.seconds, d.isFinite, d > 0 { duration = d }
        // Counted once per start, as the web page counts it: half the song,
        // or four minutes of a long one.
        if !counted, let item = current, duration > 0, seconds >= min(duration / 2, 240) {
            counted = true
            Task { await api.recordPlayed(item) }
        }
    }

    // MARK: The system's Now Playing

    private func publish() {
        guard let item = current else { return }
        var info: [String: Any] = [
            MPMediaItemPropertyTitle: item.title,
            MPMediaItemPropertyArtist: item.artist,
            MPMediaItemPropertyPlaybackDuration: duration,
            MPNowPlayingInfoPropertyElapsedPlaybackTime: time,
            MPNowPlayingInfoPropertyPlaybackRate: isPlaying ? 1.0 : 0.0,
        ]
        if let album = item.album { info[MPMediaItemPropertyAlbumTitle] = album }
        if let artwork { info[MPMediaItemPropertyArtwork] = artwork }
        MPNowPlayingInfoCenter.default().nowPlayingInfo = info
        MPNowPlayingInfoCenter.default().playbackState = isPlaying ? .playing : .paused
    }

    private func loadArtwork(for item: Item) {
        guard let url = api.artURL(source: item.sourceId, artId: item.artId, size: 800) else { return }
        Task {
            guard let (data, _) = try? await URLSession.shared.data(from: url),
                  let image = UIImage(data: data), current == item
            else { return }
            // Asked for on MediaPlayer's own queue, not the main one: the
            // closure must not inherit the app's main-actor default, or
            // Swift's isolation check stops the app the first time it runs.
            artwork = MPMediaItemArtwork(boundsSize: image.size) { @Sendable _ in image }
            publish()
        }
    }

    private func setUpRemoteCommands() {
        let c = MPRemoteCommandCenter.shared()
        c.playCommand.addTarget { [weak self] _ in self?.resume(); return .success }
        c.pauseCommand.addTarget { [weak self] _ in self?.pause(); return .success }
        c.togglePlayPauseCommand.addTarget { [weak self] _ in self?.togglePlay(); return .success }
        c.nextTrackCommand.addTarget { [weak self] _ in self?.next(); return .success }
        c.previousTrackCommand.addTarget { [weak self] _ in self?.previous(); return .success }
        c.changePlaybackPositionCommand.addTarget { [weak self] event in
            guard let e = event as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
            self?.seek(to: e.positionTime)
            return .success
        }
    }
}
