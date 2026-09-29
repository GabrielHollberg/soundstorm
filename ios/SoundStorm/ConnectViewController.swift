import UIKit

/// First launch, or "Change server": asks for the address of somebody's own
/// SoundStorm, checks it really is one, and hands it back.
final class ConnectViewController: UIViewController, UITextFieldDelegate {
    var onConnected: ((URL) -> Void)?

    private let prefill: URL?
    private let field = UITextField()
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

        hint.text = "Enter your server's address - the one you open in a browser."
        hint.font = .preferredFont(forTextStyle: .subheadline)
        hint.textColor = .secondaryLabel
        hint.numberOfLines = 0
        hint.textAlignment = .center

        field.placeholder = "yourname.home.soundstorm.dev"
        field.text = prefill.map { $0.host() ?? $0.absoluteString }
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

        let stack = UIStackView(arrangedSubviews: [logo, title, hint, field, button, message])
        stack.axis = .vertical
        stack.alignment = .center
        stack.spacing = 16
        stack.setCustomSpacing(28, after: hint)
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        for wide in [field, button, hint, message] {
            wide.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        }
        // Centered in whatever the keyboard leaves, and no wider than 420
        // points so it stays a column on an iPad.
        let space = UILayoutGuide()
        view.addLayoutGuide(space)
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
        field.becomeFirstResponder()
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
                try await ServerAddress.check(server)
                self?.onConnected?(server)
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
