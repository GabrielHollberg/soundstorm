import UIKit

/// First launch, or "Change server": the servers this app knows, to pick one
/// (held for Rename and Remove), and the address of another to add - checked
/// to be a SoundStorm before it is kept.
final class ConnectViewController: UIViewController, UITextFieldDelegate {
    var onConnected: ((URL) -> Void)?

    /// The server in use, marked in the list.
    private let prefill: URL?
    private let field = UITextField()
    private let serversTitle = UILabel()
    private let servers = UIStackView()
    /// Servers found on the phone's network that it does not know yet.
    private let nearbyTitle = UILabel()
    private let nearby = UIStackView()
    private var found: [ServerDiscovery.Found] = []
    private var searched = false
    private var searching: Task<Void, Never>?
    private let scanner = CodeScanner()
    private let button = UIButton(configuration: .filled())
    private let hint = UILabel()
    private let message = UILabel()
    private var checking: Task<Void, Never>?

    // The web app's own colors (style.css: --bg, --surface, --accent).
    private static let surface = UIColor(red: 0x17 / 255, green: 0x1b / 255, blue: 0x22 / 255, alpha: 1)
    private static let accent = UIColor(red: 0x6a / 255, green: 0xa8 / 255, blue: 0xff / 255, alpha: 1)

    init(prefill: URL?) {
        self.prefill = prefill
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    override var preferredStatusBarStyle: UIStatusBarStyle { .lightContent }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black

        let logo = UIImageView(image: UIImage(named: "Logo"))
        logo.layer.cornerRadius = 18
        logo.layer.cornerCurve = .continuous
        logo.clipsToBounds = true
        logo.widthAnchor.constraint(equalToConstant: 80).isActive = true
        logo.heightAnchor.constraint(equalToConstant: 80).isActive = true

        let title = UILabel()
        title.text = "SoundStorm"
        title.font = .systemFont(ofSize: 32, weight: .heavy).italic()
        title.textColor = .white

        serversTitle.text = "Your servers"
        serversTitle.font = .preferredFont(forTextStyle: .headline)
        serversTitle.textColor = .white
        servers.axis = .vertical
        servers.spacing = 8

        hint.font = .preferredFont(forTextStyle: .subheadline)
        hint.textColor = .secondaryLabel
        hint.numberOfLines = 0
        hint.textAlignment = .center

        field.placeholder = "abc123.home.soundstorm.dev"
        field.keyboardType = .URL
        field.textContentType = .URL
        field.autocapitalizationType = .none
        field.autocorrectionType = .no
        field.spellCheckingType = .no
        field.returnKeyType = .go
        field.clearButtonMode = .whileEditing
        field.textColor = .white
        field.backgroundColor = Self.surface
        field.layer.cornerRadius = 12
        field.leftView = UIView(frame: CGRect(x: 0, y: 0, width: 14, height: 1))
        field.leftViewMode = .always
        field.heightAnchor.constraint(equalToConstant: 50).isActive = true
        field.delegate = self

        button.configuration?.title = "Connect"
        button.configuration?.baseBackgroundColor = Self.accent
        button.configuration?.baseForegroundColor = .black
        button.configuration?.cornerStyle = .large
        button.configuration?.buttonSize = .large
        button.addAction(UIAction { [weak self] _ in self?.connect() }, for: .primaryActionTriggered)

        message.font = .preferredFont(forTextStyle: .footnote)
        message.textColor = .systemRed
        message.numberOfLines = 0
        message.textAlignment = .center

        nearbyTitle.font = .preferredFont(forTextStyle: .headline)
        nearbyTitle.textColor = .white
        nearbyTitle.numberOfLines = 0
        nearbyTitle.textAlignment = .center
        nearbyTitle.isHidden = true
        nearby.axis = .vertical
        nearby.spacing = 8
        nearby.isHidden = true

        let stack = UIStackView(arrangedSubviews: [logo, title, nearbyTitle, nearby, serversTitle, servers, hint, field, button, message])
        stack.axis = .vertical
        stack.alignment = .center
        stack.spacing = 16
        stack.setCustomSpacing(28, after: title)
        stack.setCustomSpacing(28, after: servers)
        stack.setCustomSpacing(16, after: hint)
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        stack.setCustomSpacing(28, after: nearby)
        for wide in [field, button, hint, message, servers, nearby, nearbyTitle] {
            wide.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        }
        // Centered in whatever the keyboard leaves, and no wider than 420
        // points so it stays a column on an iPad.
        let space = UILayoutGuide()
        view.addLayoutGuide(space)
        showServers()
        let fill = stack.widthAnchor.constraint(equalTo: view.layoutMarginsGuide.widthAnchor, constant: -16)
        fill.priority = .defaultHigh
        NSLayoutConstraint.activate([
            space.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
            space.bottomAnchor.constraint(equalTo: view.keyboardLayoutGuide.topAnchor),
            stack.centerYAnchor.constraint(equalTo: space.centerYAnchor),
            stack.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: 420),
            fill,
        ])
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        // A centered stack measures a label as one line unless told its
        // width, which it only has once laid out.
        for label in [hint, message, nearbyTitle] where label.preferredMaxLayoutWidth != label.bounds.width {
            label.preferredMaxLayoutWidth = label.bounds.width
            view.setNeedsLayout()
        }
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        // Looked for on the network at once, and again while nothing is
        // found (the server may still be starting; iOS asks permission to
        // look on the local network the first time).
        searching?.cancel()
        searching = Task { [weak self] in
            while !Task.isCancelled {
                let all = await ServerDiscovery.search()
                guard let self, !Task.isCancelled else { return }
                let known = Set(ServerAddress.all.map { $0.url.host() ?? "" })
                self.found = all.filter { !known.contains($0.url.host() ?? "") }
                self.searched = true
                self.showNearby()
                // Not straight to typing when nothing is found: the keyboard
                // would cover why, and a new box is usually just still starting.
                try? await Task.sleep(for: .seconds(self.found.isEmpty ? 10 : 30))
            }
        }
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        searching?.cancel()
    }

    /// What the network search found: one tap, nothing to type.
    private func showNearby() {
        nearby.arrangedSubviews.forEach { $0.removeFromSuperview() }
        // Nothing found, and no server known: why, and that it keeps looking.
        let explain = found.isEmpty && searched && ServerAddress.all.isEmpty
        nearbyTitle.isHidden = found.isEmpty && !explain
        nearbyTitle.textColor = explain ? .secondaryLabel : .white
        nearbyTitle.font = .preferredFont(forTextStyle: explain ? .subheadline : .headline)
        nearby.isHidden = found.isEmpty
        let fresh = found.count == 1 && !found[0].setUp
        nearbyTitle.text = explain ? ServerDiscovery.nothingFound(tv: false)
            : fresh ? "We found your new SoundStorm"
            : found.count == 1 ? "We found \(found[0].label) on your network"
            : "We found SoundStorm on your network - which one?"
        for server in found {
            var config = UIButton.Configuration.filled()
            config.baseBackgroundColor = Self.accent
            config.baseForegroundColor = .black
            config.cornerStyle = .large
            if found.count == 1 {
                config.title = server.setUp ? "Use it" : "Set it up"
            } else {
                config.title = server.label
                config.subtitle = server.setUp ? nil : "Not set up yet"
            }
            let row = UIButton(configuration: config)
            row.addAction(UIAction { [weak self] _ in
                if server.setUp { self?.use(server) } else { self?.setUp(server) }
            }, for: .primaryActionTriggered)
            nearby.addArrangedSubview(row)
        }
        if ServerAddress.all.isEmpty {
            hint.text = found.isEmpty
                ? Self.firstHint
                : "Or type its address, or just the code at its start."
        }
    }

    /// A new box: its setup code, scanned off the sticker or typed, then the
    /// page's sign-up for the owner's name and password (with the code
    /// already in, from the address).
    private func setUp(_ server: ServerDiscovery.Found) {
        let ask = UIAlertController(title: "Set up your new SoundStorm",
                                    message: "The setup code is on the sticker on the bottom of the box.",
                                    preferredStyle: .alert)
        ask.addAction(UIAlertAction(title: "Scan the code", style: .default) { [weak self] _ in
            guard let self else { return }
            self.scanner.scan(over: self, failed: { [weak self] in self?.typeCode(server, note: "The camera couldn't be used. Type the code instead.") }) { [weak self] text in
                guard let text else { return }
                self?.begin(server, code: Self.setupCode(in: text))
            }
        })
        ask.addAction(UIAlertAction(title: "Type the code", style: .default) { [weak self] _ in self?.typeCode(server, note: nil) })
        ask.addAction(UIAlertAction(title: "Cancel", style: .cancel))
        present(ask, animated: true)
    }

    private func typeCode(_ server: ServerDiscovery.Found, note: String?) {
        let ask = UIAlertController(title: "Setup code", message: note ?? "As printed on the sticker. Capitals and dashes do not matter.",
                                    preferredStyle: .alert)
        ask.addTextField { field in
            field.autocapitalizationType = .allCharacters
            field.autocorrectionType = .no
            field.placeholder = "ABCD-EFGH-..."
        }
        ask.addAction(UIAlertAction(title: "Cancel", style: .cancel))
        ask.addAction(UIAlertAction(title: "Continue", style: .default) { [weak self, weak ask] _ in
            self?.begin(server, code: Self.setupCode(in: ask?.textFields?.first?.text ?? ""))
        })
        present(ask, animated: true)
    }

    private func begin(_ server: ServerDiscovery.Found, code: String) {
        guard !code.isEmpty else { return }
        ServerAddress.pendingSetup = code
        onConnected?(server.url)
    }

    /// The code in what was scanned: an address carrying `?setup=`, or the
    /// code itself.
    static func setupCode(in text: String) -> String {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if let parts = URLComponents(string: text), parts.scheme != nil,
           let code = parts.queryItems?.first(where: { $0.name == "setup" })?.value {
            return code
        }
        return text
    }

    /// A found server: a phone leaves the house, so its away name is kept
    /// when it answers (the page moves home by itself when it can).
    private func use(_ server: ServerDiscovery.Found) {
        guard checking == nil else { return }
        let host = server.url.host() ?? ""
        ServerAddress.serverCalls(server.url, server.name)
        guard host.hasSuffix(".home.soundstorm.dev") else { onConnected?(server.url); return }
        setBusy(true)
        checking = Task { [weak self] in
            let code = String(host.dropLast(".home.soundstorm.dev".count))
            let url = (try? await ServerAddress.find(code, preferAway: true)) ?? server.url
            ServerAddress.serverCalls(url, server.name)
            self?.setBusy(false)
            self?.checking = nil
            self?.onConnected?(url)
        }
    }

    private static let firstHint = "Enter your server's address, like abc123.home.soundstorm.dev at home or abc123.net.soundstorm.dev away - or just the abc123 at its start. It is in SoundStorm's Settings, under Use on your phone or TV."

    /// The list, as it is now: a button each, the one in use ticked; held,
    /// Rename and Remove.
    private func showServers() {
        let list = ServerAddress.all
        servers.arrangedSubviews.forEach { $0.removeFromSuperview() }
        serversTitle.isHidden = list.isEmpty
        servers.isHidden = list.isEmpty
        hint.text = list.isEmpty
            ? (found.isEmpty ? Self.firstHint : "Or type its address, or just the code at its start.")
            : "Or add another - its address, or just the code at its start."
        for server in list {
            var config = UIButton.Configuration.filled()
            config.baseBackgroundColor = Self.surface
            config.baseForegroundColor = .white
            config.cornerStyle = .large
            config.title = server.name
            // The address under the name, unless the name is the address.
            let host = server.url.host() ?? server.url.absoluteString
            config.subtitle = server.name == host ? nil : host
            config.titleAlignment = .leading
            config.image = server.url == prefill ? UIImage(systemName: "checkmark") : nil
            config.imagePlacement = .trailing
            config.contentInsets = NSDirectionalEdgeInsets(top: 12, leading: 14, bottom: 12, trailing: 14)
            let row = UIButton(configuration: config)
            row.contentHorizontalAlignment = .leading
            row.accessibilityHint = "Hold for Rename and Remove"
            row.addAction(UIAction { [weak self] _ in self?.onConnected?(server.url) }, for: .primaryActionTriggered)
            row.menu = UIMenu(children: [
                UIAction(title: "Rename", image: UIImage(systemName: "pencil")) { [weak self] _ in self?.rename(server) },
                UIAction(title: "Remove", image: UIImage(systemName: "trash"), attributes: .destructive) { [weak self] _ in
                    ServerAddress.forget(server.url)
                    self?.showServers()
                },
            ])
            servers.addArrangedSubview(row)
        }
    }

    private func rename(_ server: ServerAddress.Server) {
        let alert = UIAlertController(title: "Rename", message: server.url.host(), preferredStyle: .alert)
        alert.addTextField { $0.text = server.name; $0.clearButtonMode = .whileEditing }
        alert.addAction(UIAlertAction(title: "Cancel", style: .cancel))
        alert.addAction(UIAlertAction(title: "Save", style: .default) { [weak self, weak alert] _ in
            ServerAddress.rename(server.url, to: alert?.textFields?.first?.text ?? "")
            self?.showServers()
        })
        present(alert, animated: true)
    }

    func textFieldShouldReturn(_ textField: UITextField) -> Bool {
        connect()
        return false
    }

    private func connect() {
        guard checking == nil else { return }
        let typed = field.text ?? ""
        message.text = nil
        setBusy(true)
        checking = Task { [weak self] in
            do {
                // A phone leaves the house: its away name first.
                let found = try await ServerAddress.find(typed, preferAway: true)
                self?.onConnected?(found)
            } catch {
                self?.message.text = error.localizedDescription
            }
            self?.setBusy(false)
            self?.checking = nil
        }
    }

    private func setBusy(_ busy: Bool) {
        button.configuration?.showsActivityIndicator = busy
        button.configuration?.title = busy ? "Connecting" : "Connect"
        field.isEnabled = !busy
    }
}

private extension UIFont {
    func italic() -> UIFont {
        guard let italic = fontDescriptor.withSymbolicTraits([.traitItalic, .traitBold]) else { return self }
        return UIFont(descriptor: italic, size: pointSize)
    }
}
