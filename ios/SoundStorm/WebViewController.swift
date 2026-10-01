import UIKit
import WebKit

/// The server's own web app, full screen. The page is the app: this only
/// gives it what a browser tab would (dialogs, links out, recovering from a
/// failed load) plus the one thing a tab cannot - playing on with the phone
/// locked, which comes from the audio session in SoundStormApp.
final class WebViewController: UIViewController {
    var onChangeServer: (() -> Void)?

    private let server: URL
    /// Where the page is now. Usually `server`, but the page moves itself to
    /// the install's home name when it can reach it (see sameInstall).
    private var current: URL
    private var webView: WKWebView!
    private let failure = FailureView()

    init(server: URL) {
        self.server = server
        self.current = server
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    override var preferredStatusBarStyle: UIStatusBarStyle { .lightContent }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black

        let config = WKWebViewConfiguration()
        // What an installed web app gets in Safari: media plays in the page
        // rather than jumping to the system player, and may start without a
        // fresh tap - the queue moving on to the next song is not a tap.
        config.allowsInlineMediaPlayback = true
        config.mediaTypesRequiringUserActionForPlayback = []
        config.allowsAirPlayForMediaPlayback = true
        config.allowsPictureInPictureMediaPlayback = true
        // Keeps Safari's own user agent and adds a name the page can test for.
        config.applicationNameForUserAgent = "Mobile/15E148 Safari/604.1 SoundStormApp/1"

        // window.soundstormApp tells the page it is inside this app, so it
        // can offer what only the app can do (see "Change server" in app.js).
        let content = config.userContentController
        content.addUserScript(WKUserScript(
            source: Self.pageScript,
            injectionTime: .atDocumentStart,
            forMainFrameOnly: true))
        content.add(MessageRelay(self), name: "soundstorm")

        webView = WKWebView(frame: view.bounds, configuration: config)
        webView.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        webView.navigationDelegate = self
        webView.uiDelegate = self
        // The page lays itself out under the status bar and home indicator
        // with env(safe-area-inset-*), as it does installed from Safari.
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        // Black until the page paints, instead of a white flash.
        webView.isOpaque = false
        webView.backgroundColor = .black
        webView.scrollView.backgroundColor = .black
        #if DEBUG
        webView.isInspectable = true // Safari → Develop → this iPhone
        #endif
        view.addSubview(webView)

        failure.frame = view.bounds
        failure.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        failure.isHidden = true
        failure.onRetry = { [weak self] in self?.load() }
        failure.onChangeServer = { [weak self] in self?.onChangeServer?() }
        view.addSubview(failure)

        load()
    }

    private func load() {
        failure.isHidden = true
        current = server
        AppChrome.shared.statusBarHidden = false
        webView.load(URLRequest(url: server))
    }

    /// Runs before the page's own scripts. It tells the page it is inside
    /// the app, and tells the app when Now Playing opens and closes (app.js
    /// marks the body np-open), so the status bar can go - watched from here
    /// rather than sent by app.js, so it needs no change to the web app.
    private static let pageScript = """
        window.soundstormApp = { version: 1 };
        (() => {
          let open = false;
          const report = () => {
            const now = document.body.classList.contains('np-open');
            if (now === open) return;
            open = now;
            window.webkit.messageHandlers.soundstorm.postMessage({ type: 'nowPlaying', open });
          };
          const watch = () => {
            new MutationObserver(report).observe(document.body, { attributes: true, attributeFilter: ['class'] });
            report();
          };
          if (document.body) watch(); else document.addEventListener('DOMContentLoaded', watch);
        })();
        """

    fileprivate func showFailure(_ error: Error) {
        let error = error as NSError
        // A navigation replaced by another, or turned into a download, is
        // not a failure anybody needs to see.
        if error.domain == NSURLErrorDomain && error.code == NSURLErrorCancelled { return }
        if error.domain == WKError.errorDomain && error.code == 102 { return } // frame load interrupted
        AppChrome.shared.statusBarHidden = false
        failure.show(host: server.host() ?? server.absoluteString, detail: error.localizedDescription)
    }

    fileprivate func received(_ message: WKScriptMessage) {
        // Only the server's own pages may ask the app for anything.
        let origin = message.frameInfo.securityOrigin
        // Scheme, host and port, as isServer: a page on another port of the
        // same machine is not the server.
        let defaultPort = current.scheme == "https" ? 443 : 80
        guard message.frameInfo.isMainFrame,
              origin.host.lowercased() == current.host()?.lowercased(),
              origin.protocol == current.scheme,
              (origin.port == 0 ? defaultPort : origin.port) == (current.port ?? defaultPort)
        else { return }
        guard let body = message.body as? [String: Any], let type = body["type"] as? String else { return }
        switch type {
        case "changeServer":
            AppChrome.shared.statusBarHidden = false
            onChangeServer?()
        case "nowPlaying":
            AppChrome.shared.statusBarHidden = body["open"] as? Bool ?? false
        default:
            break
        }
    }

    /// Same scheme, host and port as where the page is now: everything else
    /// leaves the app for Safari, as a link out of an installed web app does.
    private func isServer(_ url: URL) -> Bool {
        url.scheme == current.scheme && url.host()?.lowercased() == current.host()?.lowercased()
            && url.port == current.port
    }

    /// The page moving itself to this install's secure home name, which it
    /// does after checking it can reach it (moveToSecureName in app.js): from
    /// the away-from-home name <id>.net.soundstorm.dev when the phone is at
    /// home, or from a LAN address like http://192.168.0.19:8099. That is the
    /// same server, not a link out, and was being sent to Safari.
    ///
    /// Only this install's names count. Anybody can get a *.net.soundstorm.dev
    /// name for a server of their own, so from a soundstorm.dev address the id
    /// must match. From a LAN address the id is not known, and any home name
    /// on the same port is accepted: home names only ever point at private
    /// addresses, and the page asking to go there is the server's own.
    private func sameInstall(_ url: URL) -> Bool {
        guard url.scheme == "https", url.port == current.port,
              let host = url.host()?.lowercased(),
              let (id, level) = Self.installName(host), level == "home"
        else { return false }
        if let (currentID, _) = current.host().flatMap({ Self.installName($0.lowercased()) }) {
            return id == currentID
        }
        return current.scheme == "http"
    }

    /// "<id>.home.soundstorm.dev" -> (id, "home"); nil for anything else.
    private static func installName(_ host: String) -> (String, String)? {
        let parts = host.split(separator: ".")
        guard parts.count == 4, parts[2] == "soundstorm", parts[3] == "dev",
              parts[1] == "home" || parts[1] == "net"
        else { return nil }
        return (String(parts[0]), String(parts[1]))
    }
}

extension WebViewController: WKNavigationDelegate {
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction) async -> WKNavigationActionPolicy {
        guard let url = action.request.url else { return .cancel }
        let mainFrame = action.targetFrame?.isMainFrame ?? true
        if mainFrame && sameInstall(url) {
            // Followed for now, never saved: the address typed stays the one
            // the app starts from. Saving a name reached from a plain-http
            // page let someone on the same Wi-Fi pin the app to a server of
            // their own for good (the security review; Android the same).
            current = url
            return .allow
        }
        if mainFrame && !isServer(url) && ["http", "https"].contains(url.scheme ?? "") {
            await UIApplication.shared.open(url)
            return .cancel
        }
        if !["http", "https", "blob", "data", "about"].contains(url.scheme ?? "") {
            // mailto:, tel: and the like belong to other apps - only when
            // somebody followed such a link on the page itself, never because
            // a frame or a script asked.
            if mainFrame && action.navigationType == .linkActivated {
                await UIApplication.shared.open(url)
            }
            return .cancel
        }
        return .allow
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        showFailure(error)
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        showFailure(error)
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        // iOS reclaims a background web view's memory by killing its page.
        // Reloading is what Safari does when you come back to such a tab.
        load()
    }
}

extension WebViewController: WKUIDelegate {
    // A web view shows no alert(), confirm() or prompt() unless its app does,
    // and SoundStorm asks before removing downloads and big files.
    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo) async {
        await withCheckedContinuation { done in
            let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
            alert.addAction(UIAlertAction(title: "OK", style: .default) { _ in done.resume() })
            present(alert, animated: true)
        }
    }

    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo) async -> Bool {
        await withCheckedContinuation { done in
            let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
            alert.addAction(UIAlertAction(title: "Cancel", style: .cancel) { _ in done.resume(returning: false) })
            alert.addAction(UIAlertAction(title: "OK", style: .default) { _ in done.resume(returning: true) })
            present(alert, animated: true)
        }
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String,
                 defaultText: String?, initiatedByFrame frame: WKFrameInfo) async -> String? {
        await withCheckedContinuation { done in
            let alert = UIAlertController(title: nil, message: prompt, preferredStyle: .alert)
            alert.addTextField { $0.text = defaultText }
            alert.addAction(UIAlertAction(title: "Cancel", style: .cancel) { _ in done.resume(returning: nil) })
            alert.addAction(UIAlertAction(title: "OK", style: .default) { [weak alert] _ in
                done.resume(returning: alert?.textFields?.first?.text ?? "")
            })
            present(alert, animated: true)
        }
    }

    /// target="_blank" (the ListenBrainz and LRCLIB links in Account): Safari.
    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        // Only a link somebody followed, and only to the web: not a window a
        // script opened, nor any other kind of address.
        if let url = action.request.url, action.navigationType == .linkActivated,
           ["http", "https"].contains(url.scheme ?? "") {
            UIApplication.shared.open(url)
        }
        return nil
    }
}

/// WKUserContentController holds its handlers strongly, and the controller
/// holds the web view, so a direct reference would never be freed.
private final class MessageRelay: NSObject, WKScriptMessageHandler {
    weak var target: WebViewController?

    init(_ target: WebViewController) { self.target = target }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.received(message)
    }
}

/// Shown when the server cannot be reached, with the two ways forward: try
/// again, or point the app at a different address.
private final class FailureView: UIView {
    var onRetry: (() -> Void)?
    var onChangeServer: (() -> Void)?

    private let title = UILabel()
    private let detail = UILabel()

    override init(frame: CGRect) {
        super.init(frame: frame)
        backgroundColor = .black

        title.font = .preferredFont(forTextStyle: .title3)
        title.textColor = .white
        title.numberOfLines = 0
        title.textAlignment = .center

        detail.font = .preferredFont(forTextStyle: .footnote)
        detail.textColor = .secondaryLabel
        detail.numberOfLines = 0
        detail.textAlignment = .center

        var retry = UIButton.Configuration.filled()
        retry.title = "Try again"
        retry.baseBackgroundColor = UIColor(red: 0x6a / 255, green: 0xa8 / 255, blue: 0xff / 255, alpha: 1)
        retry.baseForegroundColor = .black
        retry.cornerStyle = .large
        let retryButton = UIButton(configuration: retry)
        retryButton.addAction(UIAction { [weak self] _ in self?.onRetry?() }, for: .primaryActionTriggered)

        var change = UIButton.Configuration.plain()
        change.title = "Change server"
        change.baseForegroundColor = .secondaryLabel
        let changeButton = UIButton(configuration: change)
        changeButton.addAction(UIAction { [weak self] _ in self?.onChangeServer?() }, for: .primaryActionTriggered)

        let stack = UIStackView(arrangedSubviews: [title, detail, retryButton, changeButton])
        stack.axis = .vertical
        stack.alignment = .center
        stack.spacing = 12
        stack.setCustomSpacing(24, after: detail)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            stack.leadingAnchor.constraint(equalTo: layoutMarginsGuide.leadingAnchor, constant: 16),
            stack.trailingAnchor.constraint(equalTo: layoutMarginsGuide.trailingAnchor, constant: -16),
        ])
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    func show(host: String, detail text: String) {
        title.text = "Can't reach SoundStorm at \(host)"
        detail.text = text
        isHidden = false
    }
}
