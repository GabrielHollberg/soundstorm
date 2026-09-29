import AVFoundation
import SwiftUI

@main
struct SoundStormApp: App {
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
                // Light status bar text over the app's black background.
                .preferredColorScheme(.dark)
        }
    }
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
        if let server = ServerAddress.saved {
            showWeb(server)
        } else {
            showConnect(prefill: nil)
        }
    }

    private func showConnect(prefill: URL?) {
        let connect = ConnectViewController(prefill: prefill)
        connect.onConnected = { [weak self] url in
            ServerAddress.saved = url
            self?.showWeb(url)
        }
        show(connect)
    }

    private func showWeb(_ server: URL) {
        let web = WebViewController(server: server)
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
