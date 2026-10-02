import AVKit
import SwiftUI

/// A film or an episode, in tvOS's own player. AVPlayerViewController already
/// is what the web page's TV mode was built to imitate - click pauses, left
/// and right move ten seconds, a swipe down shows the details, Back leaves -
/// so it is used as it is. What SoundStorm adds: the stream with the session
/// cookie, carrying on from where this person stopped (the web page's own
/// record, so the TV and the page agree), and Up next after an episode.
@Observable
final class VideoSession: Identifiable {
    enum Stage: Equatable {
        case loading
        case playing
        case upNext(Item, secondsLeft: Int)
        case failed(String)
    }

    private(set) var item: Item
    private(set) var stage: Stage = .loading
    let player = AVPlayer()

    /// The film's subtitle and audio tracks, and which are chosen. Subtitles
    /// start off, as on the page; a language chosen carries on to the next
    /// episode when it has one.
    private(set) var subtitleTracks: [API.Playback.Subtitle] = []
    private(set) var audioTracks: [API.Playback.Audio] = []
    private(set) var subtitleChoice: Int?
    private(set) var audioChoice: Int?
    /// What is on screen now, drawn over the picture by SubtitleOverlay.
    private(set) var subtitleText = ""
    private var subtitles: Subtitles?
    private var subtitleLanguage: String?
    private var subtitleObserver: Any?

    private let api: API
    private var timeObserver: Any?
    private var endObserver: NSObjectProtocol?
    private var statusObservation: NSKeyValueObservation?
    private var rateObservation: NSKeyValueObservation?
    private var lastSave = Date.distantPast
    private var countdown: Task<Void, Never>?

    init(item: Item, api: API) {
        self.item = item
        self.api = api
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: 5, timescale: 1), queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.save(force: false) }
        }
        // Saved on pause too, as the page saves it, so stopping to talk and
        // then leaving keeps the moment.
        subtitleObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: 1, timescale: 10), queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.showSubtitle() }
        }
        rateObservation = player.observe(\.timeControlStatus) { [weak self] p, _ in
            let paused = p.timeControlStatus == .paused
            Task { @MainActor in if paused { self?.save(force: true) } }
        }
    }

    func start() async {
        stage = .loading
        do {
            let playback = try await api.playback(item)
            subtitleTracks = playback.subtitles ?? []
            audioTracks = playback.audio ?? []
            audioChoice = audioTracks.first(where: { $0.default == true })?.index ?? audioTracks.first?.index
            let playerItem = try load(playback)
            if let seconds = await api.watchedSeconds(item) {
                await resume(at: seconds, in: playerItem)
            }
            // Back pressed while it loaded: nothing plays behind the library.
            guard !stopped else { return }
            player.play()
            stage = .playing
            await carryOnSubtitles()
            #if DEBUG
            // For the simulator, which has no remote to open the menus with.
            if let n = UserDefaults.standard.object(forKey: "autosubtitle") as? Int ?? Int(UserDefaults.standard.string(forKey: "autosubtitle") ?? "") {
                await chooseSubtitle(n)
            }
            if let n = Int(UserDefaults.standard.string(forKey: "autoaudio") ?? "") {
                try? await Task.sleep(for: .seconds(3))
                await chooseAudio(n)
            }
            #endif
        } catch {
            stage = .failed(error.localizedDescription)
        }
    }

    private func load(_ playback: API.Playback) throws -> AVPlayerItem {
        let asset = AVURLAsset(url: try api.absolute(playback.url), options: [AVURLAssetHTTPCookiesKey: api.cookies])
        let playerItem = AVPlayerItem(asset: asset)
        playerItem.externalMetadata = metadata()
        watchEnd(of: playerItem)
        watchStatus(of: playerItem)
        player.replaceCurrentItem(with: playerItem)
        return playerItem
    }

    // MARK: Subtitles and audio

    /// Off, or the track at that place in the list.
    func chooseSubtitle(_ index: Int?) async {
        subtitleChoice = index
        subtitleLanguage = index.flatMap { subtitleTracks.indices.contains($0) ? subtitleTracks[$0].language ?? "" : nil }
        subtitles = nil
        subtitleText = ""
        guard let index, subtitleTracks.indices.contains(index) else { return }
        do {
            let vtt = try await api.text(at: subtitleTracks[index].url)
            guard subtitleChoice == index else { return } // chosen again meanwhile
            subtitles = Subtitles(webVTT: vtt)
            showSubtitle()
        } catch {
            subtitleChoice = nil
        }
    }

    /// The next episode keeps the language chosen for the last, when it has it.
    private func carryOnSubtitles() async {
        guard let language = subtitleLanguage else {
            await chooseSubtitle(nil)
            return
        }
        let wanted = subtitleTracks.firstIndex { ($0.language ?? "") == language && $0.forced != true }
            ?? subtitleTracks.firstIndex { ($0.language ?? "") == language }
        let keep = subtitleLanguage
        await chooseSubtitle(wanted)
        if wanted == nil { subtitleLanguage = keep } // still wanted for a later episode
    }

    /// Another language is another stream (Jellyfin's HLS with that audio),
    /// played on from the same moment - what the page does.
    func chooseAudio(_ index: Int) async {
        guard index != audioChoice else { return }
        let at = player.currentTime()
        let wasPlaying = player.timeControlStatus != .paused
        do {
            let playback = try await api.playback(item, audio: index)
            audioChoice = index
            _ = try load(playback)
            await player.seek(to: at)
            if wasPlaying { player.play() }
        } catch {
            // The film plays on in the language it had; a moment's word says
            // the other could not be had. It used to end the film.
            subtitleNotice = "That language couldn't be played."
            Task {
                try? await Task.sleep(for: .seconds(4))
                subtitleNotice = nil
            }
        }
    }

    /// A short message drawn where subtitles are, over them for a moment.
    private var subtitleNotice: String?

    private func showSubtitle() {
        let text = subtitleNotice ?? subtitles?.text(at: player.currentTime().seconds) ?? ""
        if text != subtitleText { subtitleText = text }
    }

    /// Not from the very start, and not into the credits - both are a fresh
    /// start rather than a place to carry on from (the page's rule).
    private func resume(at seconds: Double, in playerItem: AVPlayerItem) async {
        let length = (try? await playerItem.asset.load(.duration).seconds).flatMap { $0.isFinite && $0 > 0 ? $0 : nil }
            ?? item.durationSeconds ?? 0
        guard seconds > 10, length == 0 || seconds < length * 0.93 else { return }
        await player.seek(to: CMTime(seconds: seconds, preferredTimescale: 600))
    }

    /// Closed: a start still loading must not play after.
    private var stopped = false

    func stop() {
        stopped = true
        save(force: true)
        countdown?.cancel()
        player.pause()
        player.replaceCurrentItem(with: nil)
    }

    // MARK: Up next

    private func ended() {
        save(force: true)
        guard item.kind == "tv" else {
            stage = .failed("") // the film is over: the view closes
            return
        }
        Task {
            guard let next = await api.nextEpisode(after: item) else {
                stage = .failed("")
                return
            }
            countDown(into: next)
        }
    }

    /// Eight seconds into the next episode, as the page counts them, with
    /// Play now and Cancel beside it.
    private func countDown(into next: Item) {
        countdown?.cancel()
        stage = .upNext(next, secondsLeft: 8)
        countdown = Task {
            for left in stride(from: 7, through: 0, by: -1) {
                try? await Task.sleep(for: .seconds(1))
                guard !Task.isCancelled else { return }
                stage = .upNext(next, secondsLeft: left)
            }
            playNow(next)
        }
    }

    func playNow(_ next: Item) {
        countdown?.cancel()
        item = next
        Task { await start() }
    }

    func cancelUpNext() {
        countdown?.cancel()
        stage = .failed("")
    }

    // MARK: Plumbing

    private func save(force: Bool) {
        guard player.currentItem != nil else { return }
        if !force && Date().timeIntervalSince(lastSave) < 30 { return }
        let seconds = player.currentTime().seconds
        let d = player.currentItem?.duration.seconds ?? 0
        // A transcode may not know its length yet; the backend's runtime
        // stands in for it, as on the page.
        let length = d.isFinite && d > 0 ? d : (item.durationSeconds ?? 0)
        guard seconds.isFinite, seconds > 5, length > 0 else { return }
        lastSave = Date()
        let item = item
        Task { await api.saveWatched(item, seconds: seconds, length: length) }
    }

    private func watchEnd(of playerItem: AVPlayerItem) {
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        endObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.didPlayToEndTimeNotification, object: playerItem, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.ended() }
        }
    }

    private func watchStatus(of playerItem: AVPlayerItem) {
        statusObservation = playerItem.observe(\.status) { [weak self] p, _ in
            let failed = p.status == .failed
            let message = p.error?.localizedDescription ?? "This can't be played."
            Task { @MainActor in if failed { self?.stage = .failed(message) } }
        }
    }

    /// The name and episode shown in the player's details panel.
    private func metadata() -> [AVMetadataItem] {
        func entry(_ id: AVMetadataIdentifier, _ value: String) -> AVMetadataItem {
            let m = AVMutableMetadataItem()
            m.identifier = id
            m.value = value as NSString
            m.extendedLanguageTag = "und"
            return m
        }
        var items = [entry(.commonIdentifierTitle, item.title)]
        let about = [item.episodeCode, item.subtitle].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
        if !about.isEmpty { items.append(entry(.iTunesMetadataTrackSubTitle, about)) }
        return items
    }
}

struct VideoView: View {
    let session: VideoSession
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            PlayerController(session: session).ignoresSafeArea()
            switch session.stage {
            case .loading:
                ProgressView()
            case .playing:
                EmptyView()
            case .upNext(let next, let left):
                UpNext(next: next, secondsLeft: left,
                       playNow: { session.playNow(next) }, cancel: { session.cancelUpNext() })
            case .failed(let message):
                if !message.isEmpty {
                    VStack(spacing: 30) {
                        Text("This can't be played.").font(.title3)
                        Text(message).foregroundStyle(.secondary)
                        Button("Back") { dismiss() }
                    }
                }
            }
        }
        .task { await session.start() }
        .onChange(of: session.stage) { _, stage in
            // A film that ended, or Up next cancelled: back to the library.
            if case .failed("") = stage { dismiss() }
        }
        .onDisappear { session.stop() }
    }
}

private struct UpNext: View {
    let next: Item
    let secondsLeft: Int
    let playNow: () -> Void
    let cancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text("Up next in \(secondsLeft)").foregroundStyle(.secondary)
            Text([next.episodeCode, next.title].compactMap { $0 }.joined(separator: " · "))
                .font(.title3).lineLimit(2)
            HStack {
                Button("Play now", action: playNow)
                Button("Cancel", action: cancel)
            }
        }
        .padding(50)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 30))
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
        .padding(80)
    }
}

/// tvOS's player, with SoundStorm's subtitles drawn in its content overlay
/// (over the picture, under its own controls) and Subtitles and Audio menus
/// in its transport bar, where a swipe down finds them like any app's.
private struct PlayerController: UIViewControllerRepresentable {
    let session: VideoSession

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let controller = AVPlayerViewController()
        controller.player = session.player
        if let overlay = controller.contentOverlayView {
            let host = UIHostingController(rootView: SubtitleOverlay(session: session))
            host.view.backgroundColor = .clear
            host.view.isUserInteractionEnabled = false
            // Pinned, not sized from the overlay's bounds: they are zero when
            // the controller is made, and the subtitles sat mid-screen.
            host.view.translatesAutoresizingMaskIntoConstraints = false
            controller.addChild(host)
            overlay.addSubview(host.view)
            NSLayoutConstraint.activate([
                host.view.leadingAnchor.constraint(equalTo: overlay.leadingAnchor),
                host.view.trailingAnchor.constraint(equalTo: overlay.trailingAnchor),
                host.view.topAnchor.constraint(equalTo: overlay.topAnchor),
                host.view.bottomAnchor.constraint(equalTo: overlay.bottomAnchor),
            ])
            host.didMove(toParent: controller)
        }
        return controller
    }

    func updateUIViewController(_ controller: AVPlayerViewController, context: Context) {
        // Read here so SwiftUI calls this again when they change.
        let subtitles = session.subtitleTracks
        let chosenSubtitle = session.subtitleChoice
        let audio = session.audioTracks
        let chosenAudio = session.audioChoice
        var menus: [UIMenuElement] = []
        if !subtitles.isEmpty {
            let off = UIAction(title: "Off", state: chosenSubtitle == nil ? .on : .off) { _ in
                Task { await session.chooseSubtitle(nil) }
            }
            let tracks = subtitles.enumerated().map { i, t in
                UIAction(title: t.label ?? "Track \(i + 1)", state: chosenSubtitle == i ? .on : .off) { _ in
                    Task { await session.chooseSubtitle(i) }
                }
            }
            menus.append(UIMenu(title: "Subtitles", image: UIImage(systemName: "captions.bubble"),
                                options: .singleSelection, children: [off] + tracks))
        }
        if audio.count > 1 {
            let tracks = audio.map { t in
                UIAction(title: t.label ?? t.language ?? "Track \(t.index)", state: chosenAudio == t.index ? .on : .off) { _ in
                    Task { await session.chooseAudio(t.index) }
                }
            }
            menus.append(UIMenu(title: "Audio", image: UIImage(systemName: "speaker.wave.2"),
                                options: .singleSelection, children: tracks))
        }
        controller.transportBarCustomMenuItems = menus
    }
}

/// The subtitle showing, low in the picture, white with a soft shadow.
private struct SubtitleOverlay: View {
    let session: VideoSession

    var body: some View {
        VStack {
            Spacer()
            if !session.subtitleText.isEmpty {
                Text(session.subtitleText)
                    .font(.system(size: 46, weight: .semibold))
                    .foregroundStyle(.white)
                    .multilineTextAlignment(.center)
                    .shadow(color: .black, radius: 3)
                    .shadow(color: .black.opacity(0.8), radius: 12)
                    .padding(.horizontal, 140)
                    .padding(.bottom, 90)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
