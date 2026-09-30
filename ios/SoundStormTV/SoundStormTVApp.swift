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
