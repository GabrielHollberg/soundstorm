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
        rateObservation = player.observe(\.timeControlStatus) { [weak self] p, _ in
            let paused = p.timeControlStatus == .paused
            Task { @MainActor in if paused { self?.save(force: true) } }
        }
    }

    func start() async {
        stage = .loading
        do {
            let playback = try await api.playback(item)
            let asset = AVURLAsset(url: api.absolute(playback.url),
                                   options: [AVURLAssetHTTPCookiesKey: api.cookies])
            let playerItem = AVPlayerItem(asset: asset)
            playerItem.externalMetadata = metadata()
            watchEnd(of: playerItem)
            watchStatus(of: playerItem)
            player.replaceCurrentItem(with: playerItem)
            if let seconds = await api.watchedSeconds(item) {
                await resume(at: seconds, in: playerItem)
            }
            player.play()
            stage = .playing
        } catch {
            stage = .failed(error.localizedDescription)
        }
    }

    /// Not from the very start, and not into the credits - both are a fresh
    /// start rather than a place to carry on from (the page's rule).
    private func resume(at seconds: Double, in playerItem: AVPlayerItem) async {
        let length = (try? await playerItem.asset.load(.duration).seconds).flatMap { $0.isFinite && $0 > 0 ? $0 : nil }
            ?? item.durationSeconds ?? 0
        guard seconds > 10, length == 0 || seconds < length * 0.93 else { return }
        await player.seek(to: CMTime(seconds: seconds, preferredTimescale: 600))
    }

    func stop() {
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
            PlayerController(player: session.player).ignoresSafeArea()
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

private struct PlayerController: UIViewControllerRepresentable {
    let player: AVPlayer

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let controller = AVPlayerViewController()
        controller.player = player
        return controller
    }

    func updateUIViewController(_ controller: AVPlayerViewController, context: Context) {}
}
