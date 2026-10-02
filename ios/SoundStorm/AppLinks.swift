import AVFoundation
import MediaPlayer
import UIKit
import VisionKit

/// A TV's sign-in code reaching the app: scanned with the camera from inside
/// it (Settings, Sign in a TV), or a `soundstorm://link?server=&code=` link
/// the page offers in Safari - as the Android app's (`MainActivity.tvLink`,
/// `scanTvCode`). The page then asks "Sign in a TV?".
enum TVLink {
    /// The code in what was scanned or opened: the QR's address
    /// (`/link/<code>` or `?link=<code>`), a `soundstorm://link`, or the six
    /// characters alone.
    static func code(in raw: String) -> String? {
        let raw = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return nil }
        var found: String?
        if let url = URL(string: raw), url.scheme != nil {
            let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
            found = query.first(where: { $0.name == "link" || $0.name == "code" })?.value
            let parts = url.pathComponents
            if found == nil, parts.count == 3, parts[1] == "link" { found = parts[2] }
        }
        let code = (found ?? raw).uppercased().filter { $0.isLetter || $0.isNumber }
        return code.count == 6 && (found != nil || raw.count <= 9) ? code : nil
    }

    /// A link opening the app: the server it is for (only one this app
    /// already knows - a link must never point the app somewhere new) and
    /// the code.
    static func parse(_ url: URL) -> (server: URL?, code: String)? {
        let host = url.host()?.lowercased() ?? ""
        if url.scheme == "soundstorm", host == "link",
           let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems,
           let code = items.first(where: { $0.name == "code" })?.value.flatMap(Self.code(in:)) {
            let wanted = items.first(where: { $0.name == "server" })?.value.flatMap(ServerAddress.parse)
            return (known(wanted), code)
        }
        if url.scheme == "https", host.hasSuffix(".home.soundstorm.dev") || host.hasSuffix(".net.soundstorm.dev"),
           let code = code(in: url.absoluteString) {
            return (known(ServerAddress.parse(host)), code)
        }
        return nil
    }

    /// The saved server a link names - by address, or by the install's id
    /// for its home and away names alike - or nil (the app's own is used,
    /// where a stranger's code simply finds no TV).
    private static func known(_ wanted: URL?) -> URL? {
        guard let wanted else { return nil }
        let installID = { (u: URL) -> String? in
            guard let h = u.host(), h.hasSuffix(".soundstorm.dev") else { return nil }
            return String(h.split(separator: ".").first ?? "")
        }
        let list = ServerAddress.all.map(\.url)
        return list.first(where: { $0.host() == wanted.host() && $0.port == wanted.port })
            ?? list.first(where: { installID($0) != nil && installID($0) == installID(wanted) })
    }
}

/// Scanning a TV's QR code with the camera (VisionKit's own scanner).
final class CodeScanner: NSObject, DataScannerViewControllerDelegate {
    private var done: ((String?) -> Void)?
    private weak var scanner: DataScannerViewController?

    /// Shows the scanner over `host`; `done` gets the code, or nil if
    /// nothing was scanned, and is told "failed" through `failed` when no
    /// scanner can start (no camera, or permission refused).
    func scan(over host: UIViewController, failed: @escaping () -> Void, done: @escaping (String?) -> Void) {
        Task { @MainActor in
            guard DataScannerViewController.isSupported, await AVCaptureDevice.requestAccess(for: .video),
                  DataScannerViewController.isAvailable
            else { return failed() }
            let scanner = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])],
                                                    qualityLevel: .balanced, recognizesMultipleItems: false,
                                                    isHighlightingEnabled: true)
            scanner.delegate = self
            self.scanner = scanner
            self.done = done
            scanner.navigationItem.title = "Scan the TV's code"
            scanner.navigationItem.leftBarButtonItem = UIBarButtonItem(systemItem: .cancel, primaryAction: UIAction { [weak self] _ in
                self?.finish(nil)
            })
            let nav = UINavigationController(rootViewController: scanner)
            nav.modalPresentationStyle = .fullScreen
            host.present(nav, animated: true) { try? scanner.startScanning() }
        }
    }

    func dataScanner(_ dataScanner: DataScannerViewController, didAdd addedItems: [RecognizedItem], allItems: [RecognizedItem]) {
        for case .barcode(let code) in addedItems {
            if let text = code.payloadStringValue { return finish(text) }
        }
    }

    private func finish(_ text: String?) {
        scanner?.stopScanning()
        let done = self.done
        self.done = nil
        scanner?.navigationController?.dismiss(animated: true) { done?(text) }
    }
}

/// The phone's volume buttons turning a TV this phone controls (the page's
/// `remoteVolume` and `__soundstormVolumeKey`, as Android's dispatchKeyEvent).
/// iOS gives no way to take the buttons themselves, so the phone's own
/// volume is watched: each press is told to the page, and the volume is put
/// back in the middle, so presses keep counting either way and the phone's
/// level is where it was when this ends. A hidden volume view keeps iOS's
/// own volume display out of the way.
final class VolumeKeys {
    private var observation: NSKeyValueObservation?
    private var view: MPVolumeView?
    private var original: Float = 0.5
    private var resetting = false
    var onPress: ((Int) -> Void)?

    private static let middle: Float = 0.5

    func set(_ on: Bool, in window: UIWindow?) {
        on ? start(window) : stop()
    }

    private func start(_ window: UIWindow?) {
        guard observation == nil, let window else { return }
        let session = AVAudioSession.sharedInstance()
        try? session.setActive(true)
        original = session.outputVolume
        let view = MPVolumeView(frame: CGRect(x: -200, y: -200, width: 10, height: 10))
        view.alpha = 0.01
        window.addSubview(view)
        self.view = view
        setVolume(Self.middle)
        observation = session.observe(\.outputVolume, options: [.new]) { [weak self] _, change in
            guard let value = change.newValue else { return }
            DispatchQueue.main.async { self?.changed(value) }
        }
    }

    private func changed(_ value: Float) {
        guard !resetting, abs(value - Self.middle) > 0.001 else { return }
        onPress?(value > Self.middle ? 1 : -1)
        setVolume(Self.middle)
    }

    private func stop() {
        guard observation != nil else { return }
        observation?.invalidate()
        observation = nil
        setVolume(original)
        view?.removeFromSuperview()
        view = nil
    }

    private func setVolume(_ value: Float) {
        guard let slider = view?.subviews.compactMap({ $0 as? UISlider }).first else { return }
        resetting = true
        slider.value = value
        slider.sendActions(for: .valueChanged)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) { self.resetting = false }
    }
}
