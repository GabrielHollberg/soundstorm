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
    enum Stage { case connect, checking, unreachable(String), signIn(hasAccount: Bool), profiles([API.Profile], fromSettings: Bool), library }

    private(set) var stage: Stage = .checking
    private(set) var api: API?
    private(set) var player: Player?
    /// Hears songs for the visualizers.
    private(set) var listener: SongListener?
    /// Now Playing's look, the account's own (the page's setting).
    private(set) var look = "lyrics"
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
        ServerAddress.remember(server)
        player?.stop()
        let api = API(server: server)
        self.api = api
        player = Player(api: api)
        listener = SongListener(api: api)
        stage = .checking
        Task { await refreshSession() }
    }

    func refreshSession() async {
        guard let api else { return }
        do {
            let s = try await api.session()
            // "Who's listening?" every time the TV opens, when anybody is
            // kept on it - the owner's choice for a shared screen.
            if s.hasAccount {
                let people = await api.profiles()
                if !people.isEmpty {
                    stage = .profiles(people, fromSettings: false)
                    return
                }
            }
            stage = s.signedIn ? .library : .signIn(hasAccount: s.hasAccount)
            lastUser = api.user?.id
        } catch {
            // A request given up on (the screen that asked has gone) says
            // nothing about the server: taken for "unreachable", it put the
            // error back just after the server answered.
            if error is CancellationError || (error as? URLError)?.code == .cancelled { return }
            // Not reachable now - the server restarting, the Wi-Fi: said, and
            // tried again, rather than dropping to the address as if it were
            // wrong.
            stage = .unreachable(api.server.host() ?? api.server.absoluteString)
        }
    }

    /// Whose things are on screen, to tell when somebody else signs in.
    private var lastUser: String?

    func signedIn() {
        // Somebody else: the last person's music and pages go with them. The
        // place in a book was saved before the sign-in (signInSomeoneElse),
        // while the session was still theirs.
        if let api, lastUser != nil, api.user?.id != lastUser {
            reset()
            player?.stop()
        }
        lastUser = api?.user?.id
        stage = .library
    }

    /// What a person was doing, gone with them: shared by switching person,
    /// signing out and changing server.
    private func reset() {
        video = nil
        photos = nil
        reading = nil
        showingNowPlaying = false
        favorites = []
        pills = nil
        look = "lyrics"
    }

    /// Switch person, from Settings.
    func showProfiles() async {
        guard let api else { return }
        let people = await api.profiles()
        stage = people.isEmpty ? .signIn(hasAccount: true) : .profiles(people, fromSettings: true)
    }

    /// Someone else: signing in, to be kept on this TV - the book playing
    /// saved first, as the person still signed in.
    func signInSomeoneElse() async {
        await player?.stopSaving()
        stage = .signIn(hasAccount: true)
    }

    /// Back from the picker opened in Settings, to the person still here.
    func cancelSwitch() { stage = .library }

    /// Switched to somebody: what the last person was doing goes with them.
    /// The player was stopped, and the book's place saved, before the switch
    /// (`beforeSwitch`), so it went to the person who was listening.
    func switched() {
        reset()
        lastUser = api?.user?.id
        stage = .library
    }

    /// Before the account changes: the place in a book saved, as the person
    /// it belongs to, and the music stopped.
    func beforeSwitch() async {
        await player?.stopSaving()
    }

    func signOut() async {
        await player?.stopSaving()
        reset()
        await api?.signOut()
        lastUser = nil
        stage = .signIn(hasAccount: true)
    }

    func changeServer() {
        Task { await player?.stopSaving() }
        reset()
        lastUser = nil
        stage = .connect
    }

    /// The server said this TV is not signed in any more (a session ended
    /// elsewhere): back to signing in.
    func signedOutElsewhere() {
        player?.stop()
        reset()
        lastUser = nil
        stage = .signIn(hasAccount: true)
    }

    /// A film or an episode, full screen. The music pauses for it, as a
    /// film stops the music on the page.
    func playVideo(_ item: Item) {
        guard let api else { return }
        player?.pause()
        video = VideoSession(item: item, api: api)
    }

    func loadLook() async {
        look = Looks.shown(await api?.coverStyle())
    }

    func setLook(_ key: String) {
        look = key
        Task { await api?.setCoverStyle(key) }
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
        case .unreachable(let host):
            UnreachableView(host: host)
        case .connect:
            ConnectView()
        case .signIn(let hasAccount):
            SignInView(hasAccount: hasAccount)
        case .profiles(let people, let fromSettings):
            ProfilesView(people: people, canGoBack: fromSettings)
        case .library:
            if let api = model.api, let player = model.player {
                if api.mustRenew {
                    RenewPasswordView()
                } else if api.sessionEnded {
                    ProgressView().task { model.signedOutElsewhere() }
                } else {
                    LibraryView()
                        .modifier(DeviceRequests())
                        .modifier(RemoteControlled())
                        .environment(api)
                        .environment(player)
                }
            }
        }
    }
}
