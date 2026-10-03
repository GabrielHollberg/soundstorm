import AVFoundation
import SwiftUI

/// Photos: this day in earlier years, then the camera roll, newest first,
/// loading more as it is scrolled. A clip plays as a film.
struct PhotosView: View {
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model
    @State private var days: [API.PhotoDay] = []
    @State private var photos: [Item] = []
    @State private var hasMore = true
    @State private var loading = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 50) {
                ForEach(days) { day in
                    Row(title: "On this day, \(String(day.year))") {
                        ForEach(day.items, id: \.key) { item in
                            Thumb(item: item) { open(item, in: day.items) }
                        }
                    }
                }
                if !photos.isEmpty {
                    Text("All photos").font(.title3).padding(.leading, 20)
                }
                LazyVGrid(columns: Theme.grid, spacing: 40) {
                    ForEach(photos, id: \.key) { item in
                        Thumb(item: item) { open(item, in: photos) }
                            .onAppear { if item.key == photos.last?.key { Task { await more() } } }
                    }
                }
                .padding(.horizontal, 20)
            }
            .padding(.vertical, 40)
        }
        .task {
            days = (try? await api.onThisDay()) ?? []
            await more()
            #if DEBUG
            // For the simulator, which has no remote: -autophoto YES opens the first photo.
            if UserDefaults.standard.bool(forKey: "autophoto"), let first = photos.first(where: \.isPhoto) {
                try? await Task.sleep(for: .seconds(2)) // the music of -autostation, first
                model.showingNowPlaying = false          // as Back would put it away
                try? await Task.sleep(for: .seconds(1))
                open(first, in: photos)
            }
            #endif
        }
    }

    private func more() async {
        guard hasMore, !loading else { return }
        loading = true
        defer { loading = false }
        guard let page = try? await api.photos(offset: photos.count) else { return }
        photos += page.items
        hasMore = page.hasMore && !page.items.isEmpty
    }

    private func open(_ item: Item, in list: [Item]) {
        if item.isClip {
            model.playVideo(item)
        } else {
            // The viewer steps through photos only; clips are skipped over.
            let shown = list.filter(\.isPhoto)
            model.photos = PhotoViewing(photos: shown, index: shown.firstIndex(of: item) ?? 0)
        }
    }
}

private struct Thumb: View {
    let item: Item
    let action: () -> Void
    @Environment(API.self) private var api

    var body: some View {
        Button(action: action) {
            Cover(url: api.artURL(source: item.sourceId, artId: item.artId, size: 400))
                .frame(width: Theme.card, height: Theme.card)
                .overlay(alignment: .bottomTrailing) {
                    if item.isClip {
                        Image(systemName: "play.circle.fill").font(.title).padding(12).shadow(radius: 6)
                    }
                }
        }
        .buttonStyle(CardButton())
    }
}

/// Which photos the viewer walks, and where it is.
struct PhotoViewing: Identifiable {
    let id = UUID()
    let photos: [Item]
    var index: Int
}

/// One photo, the whole screen. Left and right step; down pauses or plays
/// the music, which otherwise plays on (the owner's asking, as over a book);
/// OK plays a Live Photo's moving part over it (LIVE on the page), quiet
/// while music plays; Back closes. The caption shows on opening and on each
/// step, then fades.
struct PhotoViewer: View {
    @State var viewing: PhotoViewing
    @Environment(API.self) private var api
    @Environment(Player.self) private var player
    @Environment(\.dismiss) private var dismiss
    @State private var images: [String: UIImage] = [:]
    @State private var captionShown = true
    @State private var toast: String?
    @State private var fade: Task<Void, Never>?
    @FocusState private var focused: Bool
    /// A Live Photo's moving part, while it plays.
    @State private var live: AVPlayer?

    var body: some View {
        let photo = viewing.photos[viewing.index]
        ZStack {
            Color.black.ignoresSafeArea()
            if let image = images[photo.key] {
                Image(uiImage: image).resizable().scaledToFit().ignoresSafeArea()
            } else {
                ProgressView()
            }
            if let live {
                LiveLayer(player: live).ignoresSafeArea()
            }
            VStack {
                if let toast {
                    Label(toast, systemImage: player.isPlaying ? "play.fill" : "pause.fill")
                        .padding(.horizontal, 30).padding(.vertical, 16)
                        .background(.ultraThinMaterial, in: Capsule())
                        .transition(.opacity)
                }
                Spacer()
                HStack(alignment: .bottom) {
                    if photo.extra?["live"] != nil {
                        Text("LIVE").font(.caption.bold())
                            .padding(.horizontal, 10).padding(.vertical, 4)
                            .background(.ultraThinMaterial, in: Capsule())
                    }
                    Text(caption(photo)).font(.headline).shadow(radius: 8)
                    Spacer()
                    Text("\(viewing.index + 1) of \(viewing.photos.count)")
                        .foregroundStyle(.secondary).shadow(radius: 8)
                }
                .opacity(captionShown ? 1 : 0)
            }
            .padding(60)
        }
        .animation(.easeInOut(duration: 0.5), value: captionShown)
        .animation(.easeInOut(duration: 0.3), value: toast)
        .focusable()
        .focused($focused)
        .focusEffectDisabled()
        .onAppear { focused = true; showCaption() }
        .onMoveCommand { direction in
            switch direction {
            case .left: step(-1)
            case .right: step(1)
            case .down: toggleMusic()
            default: showCaption()
            }
        }
        .onExitCommand { dismiss() }
        .onPlayPauseCommand { toggleMusic() }
        // OK on a Live Photo plays its moving part.
        .onTapGesture { playLive(photo) }
        .onChange(of: viewing.index) { live = nil }
        .task(id: viewing.index) { await load(around: viewing.index) }
        #if DEBUG
        // -autostep YES: a step right after three seconds, then down.
        .task {
            guard UserDefaults.standard.bool(forKey: "autostep") else { return }
            try? await Task.sleep(for: .seconds(3))
            step(1)
            try? await Task.sleep(for: .seconds(3))
            toggleMusic()
        }
        #endif
    }

    private func step(_ by: Int) {
        let next = viewing.index + by
        guard viewing.photos.indices.contains(next) else { return }
        viewing.index = next
        showCaption()
    }

    private func playLive(_ photo: Item) {
        guard photo.extra?["live"] != nil, live == nil else { return }
        let p = AVPlayer(playerItem: AVPlayerItem(asset: AVURLAsset(url: api.liveURL(photo),
                                                                     options: [AVURLAssetHTTPCookiesKey: api.cookies])))
        p.isMuted = player.isPlaying // quiet while the music plays
        live = p
        p.play()
        Task {
            // Gone when it ends, back to the still.
            for await _ in NotificationCenter.default.notifications(named: AVPlayerItem.didPlayToEndTimeNotification, object: p.currentItem) {
                if live === p { live = nil }
                return
            }
        }
    }

    private func toggleMusic() {
        guard player.current != nil else { return }
        player.togglePlay()
        say(player.isPlaying ? "Music playing" : "Music paused")
    }

    private func say(_ text: String) {
        toast = text
        Task {
            try? await Task.sleep(for: .seconds(2))
            if toast == text { toast = nil }
        }
    }

    private func showCaption() {
        captionShown = true
        fade?.cancel()
        fade = Task {
            try? await Task.sleep(for: .seconds(4))
            if !Task.isCancelled { captionShown = false }
        }
    }

    /// The photo and the ones either side, fetched now, so a step lands on a
    /// picture that is already there rather than on a spinner.
    private func load(around i: Int) async {
        for j in [i, i + 1, i - 1] where viewing.photos.indices.contains(j) {
            let p = viewing.photos[j]
            guard images[p.key] == nil, let url = api.previewURL(p),
                  let (data, _) = try? await SafeLoad.data(from: url, limit: SafeLoad.picture),
                  let image = SafeLoad.image(data, maxPixels: 3840)
            else { continue }
            images[p.key] = image
        }
        // Only a few are kept: a long walk through a camera roll would
        // otherwise hold every picture it passed.
        let keep = Set([i - 1, i, i + 1].filter(viewing.photos.indices.contains).map { viewing.photos[$0].key })
        images = images.filter { keep.contains($0.key) }
    }

    private func caption(_ p: Item) -> String {
        [p.title, p.subtitle, p.extra?["place"]].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " — ")
    }
}

/// A Live Photo's moving part, drawn over the still.
private struct LiveLayer: UIViewRepresentable {
    let player: AVPlayer

    func makeUIView(context: Context) -> LiveView {
        let view = LiveView()
        view.layer.player = player
        view.layer.videoGravity = .resizeAspect
        return view
    }

    func updateUIView(_ view: LiveView, context: Context) { view.layer.player = player }

    final class LiveView: UIView {
        override static var layerClass: AnyClass { AVPlayerLayer.self }
        override var layer: AVPlayerLayer { super.layer as! AVPlayerLayer }
    }
}
