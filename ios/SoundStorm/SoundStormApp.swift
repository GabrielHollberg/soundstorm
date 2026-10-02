import AVFoundation
import SwiftUI

@main
struct SoundStormApp: App {
    @UIApplicationDelegateAdaptor private var delegate: AppDelegate

    init() {
        // The playback category is what lets the web page's audio carry on
        // when the phone locks or the ring switch is on silent. Without it,
        // UIBackgroundModes=audio in Info.plist does nothing: iOS treats the
        // page's audio as ambient and stops it with the screen.
        try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .default)
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .ignoresSafeArea()
                // A TV's sign-in code: soundstorm://link?server=&code= from
                // the page in Safari, or the QR's own address once Universal
                // Links are set up (TVLink).
                .onOpenURL { url in NotificationCenter.default.post(name: .tvLink, object: url) }
                .onContinueUserActivity(NSUserActivityTypeBrowsingWeb) { activity in
                    if let url = activity.webpageURL { NotificationCenter.default.post(name: .tvLink, object: url) }
                }
                .statusBarHidden(AppChrome.shared.statusBarHidden)
                .animation(.easeInOut(duration: 0.25), value: AppChrome.shared.statusBarHidden)
                // Light status bar text over the app's black background.
                .preferredColorScheme(.dark)
        }
    }
}

/// Photo backup's half of the app's life: its background task is registered
/// at launch, as iOS requires, and an upload that finished with the app closed
/// is told to it here.
final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions options: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        PhotoBackup.shared.launched()
        return true
    }

    func application(_ application: UIApplication, handleEventsForBackgroundURLSession identifier: String,
                     completionHandler: @escaping () -> Void) {
        guard identifier == Uploader.identifier || identifier == Uploader.wifiIdentifier else { return completionHandler() }
        nonisolated(unsafe) let done = completionHandler
        Uploader.shared.whenFinished { DispatchQueue.main.async { done() } }
    }
}

/// What the app's frame shows around the page. The status bar is hidden
/// while the page's Now Playing is open (the owner's asking: nothing on the
/// screen but the music) and shown everywhere else, since an iPhone has no
/// swipe that brings a hidden one back for a look at the time.
@Observable
final class AppChrome {
    static let shared = AppChrome()
    var statusBarHidden = false
}

struct RootView: UIViewControllerRepresentable {
    func makeUIViewController(context: Context) -> RootViewController { RootViewController() }
    func updateUIViewController(_ controller: RootViewController, context: Context) {}
}

/// Shows the connect screen until a server is known, then the server's own
/// web app. Changing server goes back to the connect screen.
final class RootViewController: UIViewController {
    private var current: UIViewController?

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        NotificationCenter.default.addObserver(forName: .tvLink, object: nil, queue: .main) { [weak self] note in
            guard let url = note.object as? URL else { return }
            MainActor.assumeIsolated { self?.open(url) }
        }
        if let server = ServerAddress.saved {
            showWeb(server)
        } else {
            showConnect(prefill: nil)
        }
    }

    private func showConnect(prefill: URL?) {
        let connect = ConnectViewController(prefill: prefill)
        connect.onConnected = { [weak self] url in
            ServerAddress.remember(url)
            self?.showWeb(url)
        }
        show(connect)
    }

    /// A TV's code: to the page showing that server if it is open, else that
    /// (known) server opened with the code - the app's own if the link named
    /// one it does not know.
    private func open(_ url: URL) {
        guard let link = TVLink.parse(url), let server = link.server ?? ServerAddress.saved else { return }
        if let web = current as? WebViewController, web.serverURL == server {
            web.handLink(link.code)
        } else {
            ServerAddress.remember(server)
            showWeb(server, link: link.code)
        }
    }

    private func showWeb(_ server: URL, link: String? = nil) {
        let web = WebViewController(server: server, link: link)
        web.onChangeServer = { [weak self] in
            self?.showConnect(prefill: server)
        }
        show(web)
    }

    private func show(_ controller: UIViewController) {
        current?.willMove(toParent: nil)
        current?.view.removeFromSuperview()
        current?.removeFromParent()
        addChild(controller)
        controller.view.frame = view.bounds
        controller.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(controller.view)
        controller.didMove(toParent: self)
        current = controller
        setNeedsStatusBarAppearanceUpdate()
    }

    override var childForStatusBarStyle: UIViewController? { current }
}

extension Notification.Name {
    static let tvLink = Notification.Name("soundstorm.tvLink")
}
