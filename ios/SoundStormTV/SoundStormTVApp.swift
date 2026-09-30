import AVFoundation
import SwiftUI

@main
struct SoundStormTVApp: App {
    @State private var model = AppModel()

    init() {
        // Music carries on under the screen saver and in other apps, as on
        // the phones (UIBackgroundModes audio in InfoTV.plist).
        try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .default, policy: .longFormAudio)
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(model)
                .preferredColorScheme(.dark)
        }
    }
}

/// Where the app is: no server yet, a server but nobody signed in, or in.
@Observable
final class AppModel {
    enum Stage { case connect, checking, signIn(hasAccount: Bool), library }

    private(set) var stage: Stage = .checking
    private(set) var api: API?
    private(set) var player: Player?
    var showingNowPlaying = false
    /// The film or episode playing, full screen.
    var video: VideoSession?
    /// The photo viewer, when open.
    var photos: PhotoViewing?
    /// A book being read, when open.
    var reading: Reading?

    enum Reading: Identifiable {
        case epub(BookReader)
        case pdf(PdfReader)
        var id: ObjectIdentifier {
            switch self {
            case .epub(let r): ObjectIdentifier(r)
            case .pdf(let r): ObjectIdentifier(r)
            }
        }
    }

    /// An ebook or a document. A document is always a PDF, as on the page.
    func read(_ item: Item) {
        guard let api else { return }
        let pdf = item.kind == "document" || item.extra?["format"]?.lowercased() == "pdf"
        reading = pdf ? .pdf(PdfReader(item: item, api: api)) : .epub(BookReader(item: item, api: api))
    }
    /// The order of each tab's categories and the ones put away, from the
    /// account (set on the page).
    var pills: API.Pills?
    /// This person's favorites, by item key, for Now Playing's heart.
    private(set) var favorites: Set<String> = []

    init() {
        if let server = ServerAddress.saved {
            use(server)
        } else {
            stage = .connect
        }
    }

    func use(_ server: URL) {
        ServerAddress.saved = server
        player?.stop()
        let api = API(server: server)
        self.api = api
        player = Player(api: api)
        stage = .checking
        Task { await refreshSession() }
    }

    func refreshSession() async {
        guard let api else { return }
        do {
            let s = try await api.session()
            stage = s.signedIn ? .library : .signIn(hasAccount: s.hasAccount)
        } catch {
            // Unreachable: back to the address, which says why when tried.
            stage = .connect
        }
    }

    func signedIn() { stage = .library }

    func signOut() async {
        player?.stop()
        await api?.signOut()
        stage = .signIn(hasAccount: true)
    }

    func changeServer() {
        player?.stop()
        stage = .connect
    }

    /// A film or an episode, full screen. The music pauses for it, as a
    /// film stops the music on the page.
    func playVideo(_ item: Item) {
        guard let api else { return }
        player?.pause()
        video = VideoSession(item: item, api: api)
    }

    func loadFavorites() async {
        guard let items = try? await api?.favorites() else { return }
        favorites = Set(items.map(\.key))
    }

    func toggleFavorite(_ item: Item) async {
        let on = !favorites.contains(item.key)
        do {
            try await api?.setFavorite(item, on)
            if on { favorites.insert(item.key) } else { favorites.remove(item.key) }
        } catch {}
    }

    /// A radio station or a mood: its first songs, and Now Playing.
    func tune(_ station: API.Station) async {
        guard let api, let batch = try? await api.tune(station, exclude: [], from: nil) else { return }
        player?.play(station: station, first: batch)
        showingNowPlaying = true
    }

    /// A book, from where this person got to, at the account's speed.
    func playBook(_ item: Item, show: Bool = true) async {
        guard let api, let playback = try? await api.bookPlayback(item) else { return }
        let speed = await api.bookSpeed()
        player?.play(book: item, playback, speed: speed)
        if show { showingNowPlaying = true }
    }

    /// The audiobook plays and the book opens over it. Synced, the page
    /// follows the recording (Storyteller's copy of the book, whose sentences
    /// the timeline names); not yet synced, both simply open, as on the page.
    func readAlong(_ pair: API.Pair) async {
        guard let api, let player else { return }
        await playBook(pair.audiobook, show: false)
        let synced = pair.sync?.state == "ready" ? try? await api.readAlong(pair) : nil
        // Given up on (the page it came from went away): nothing, rather than
        // taking the failed request for "not synced" and opening the wrong copy.
        guard !Task.isCancelled else { return }
        if let synced, !synced.timeline.isEmpty {
            // Storyteller's title is its own; the shelf's reads better.
            let b = synced.book
            let book = Item(id: b.id, sourceId: b.sourceId, kind: "ebook", title: pair.ebook.title,
                            subtitle: nil, creators: pair.ebook.creators, artId: pair.ebook.artId,
                            durationSeconds: nil, extra: ["format": "epub"], progress: nil)
            let reader = BookReader(item: book, api: api)
            reader.follow(synced.timeline, player: player, highlight: await api.readAlongHighlight())
            reading = .epub(reader)
        } else {
            read(pair.ebook)
        }
    }

    /// Plays and opens Now Playing, as the web page's TV mode does.
    func play(_ items: [Item], from index: Int = 0) {
        player?.play(items, from: index)
        showingNowPlaying = true
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        switch model.stage {
        case .checking:
            ProgressView()
        case .connect:
            ConnectView()
        case .signIn(let hasAccount):
            SignInView(hasAccount: hasAccount)
        case .library:
            if let api = model.api, let player = model.player {
                LibraryView()
                    .environment(api)
                    .environment(player)
            }
        }
    }
}
