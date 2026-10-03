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

        field.placeholder = "yourname.home.soundstorm.dev"
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

        let stack = UIStackView(arrangedSubviews: [logo, title, serversTitle, servers, hint, field, button, message])
        stack.axis = .vertical
        stack.alignment = .center
        stack.spacing = 16
        stack.setCustomSpacing(28, after: title)
        stack.setCustomSpacing(28, after: servers)
        stack.setCustomSpacing(16, after: hint)
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        for wide in [field, button, hint, message, servers] {
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
        for label in [hint, message] where label.preferredMaxLayoutWidth != label.bounds.width {
            label.preferredMaxLayoutWidth = label.bounds.width
            view.setNeedsLayout()
        }
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        // Straight to typing only when there is nothing to pick.
        if ServerAddress.all.isEmpty { field.becomeFirstResponder() }
    }

    /// The list, as it is now: a button each, the one in use ticked; held,
    /// Rename and Remove.
    private func showServers() {
        let list = ServerAddress.all
        servers.arrangedSubviews.forEach { $0.removeFromSuperview() }
        serversTitle.isHidden = list.isEmpty
        servers.isHidden = list.isEmpty
        hint.text = list.isEmpty
            ? "Enter your server's address - the one you open in a browser."
            : "Or add another - its address, the one you open in a browser."
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
        guard let server = ServerAddress.parse(field.text ?? "") else {
            message.text = "That doesn't look like a web address."
            return
        }
        message.text = nil
        setBusy(true)
        checking = Task { [weak self] in
            do {
                let found = try await ServerAddress.find(server)
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
