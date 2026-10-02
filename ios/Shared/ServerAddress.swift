import Foundation

/// The SoundStorm servers this app knows, and the one it talks to now. Every
/// install is someone's own, so addresses are asked for rather than built in;
/// somebody may use more than one (their own and their parents', say), so the
/// app keeps a list, each with a name, the latest first.
///
/// Each server's sign-ins stay with it: cookies belong to an address, so
/// moving between servers signs nobody out of either.
enum ServerAddress {
    private static let key = "serverURL"
    private static let listKey = "servers"

    /// The server in use. Kept as a plain string, so it can also be given at
    /// launch for testing:
    /// `xcrun simctl launch booted dev.soundstorm.app -serverURL http://localhost:8080`
    static var saved: URL? {
        get { UserDefaults.standard.string(forKey: key).flatMap(parse) }
        set { UserDefaults.standard.set(newValue?.absoluteString, forKey: key) }
    }

    /// A server in the list: its address, and the name shown for it.
    struct Server: Codable, Hashable, Identifiable {
        var url: URL
        var name: String
        var id: URL { url }
    }

    /// Every server this app knows, the latest used first. An app from before
    /// the list starts it with the server it already had.
    static var all: [Server] {
        get {
            if let data = UserDefaults.standard.data(forKey: listKey),
               let list = try? JSONDecoder().decode([Server].self, from: data) {
                return list
            }
            return saved.map { [Server(url: $0, name: defaultName(for: $0))] } ?? []
        }
        set {
            UserDefaults.standard.set(try? JSONEncoder().encode(newValue), forKey: listKey)
        }
    }

    /// Using a server: it goes to the top of the list (added if new) and is
    /// the one the app opens with.
    static func remember(_ url: URL) {
        var list = all
        let entry = list.first(where: { $0.url == url }) ?? Server(url: url, name: defaultName(for: url))
        list.removeAll { $0.url == url }
        list.insert(entry, at: 0)
        all = list
        saved = url
    }

    /// Taken off the list. If it was the one in use, there is none now.
    static func forget(_ url: URL) {
        all = all.filter { $0.url != url }
        if saved == url { saved = nil }
    }

    static func rename(_ url: URL, to name: String) {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        all = all.map { $0.url == url ? Server(url: $0.url, name: name.isEmpty ? defaultName(for: $0.url) : name) : $0 }
    }

    /// A first name for a server, until somebody gives it one: its address
    /// without the parts every install shares.
    static func defaultName(for url: URL) -> String {
        let host = url.host() ?? url.absoluteString
        for suffix in [".home.soundstorm.dev", ".net.soundstorm.dev"] where host.hasSuffix(suffix) {
            return "SoundStorm " + host.dropLast(suffix.count)
        }
        return host
    }

    /// Turns what somebody typed into the server's root URL. Anything without
    /// a scheme is https: the default install has a real certificate on its
    /// soundstorm.dev name, and iOS refuses plain http to anything but the
    /// local network anyway. A path is dropped - the app lives at the root.
    static func parse(_ text: String) -> URL? {
        var text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }
        if !text.contains("://") { text = "https://" + text }
        guard var parts = URLComponents(string: text),
              let scheme = parts.scheme?.lowercased(), scheme == "https" || scheme == "http",
              let host = parts.host, !host.isEmpty
        else { return nil }
        parts.scheme = scheme
        // Names are not case-sensitive; one spelling keeps one server once in
        // the list and its cookies in one place.
        parts.host = host.lowercased()
        parts.path = ""
        parts.query = nil
        parts.fragment = nil
        return parts.url
    }

    enum CheckError: LocalizedError {
        case notSoundStorm
        case unreachable(String)

        var errorDescription: String? {
            switch self {
            case .notSoundStorm:
                "Something answered at that address, but it isn't SoundStorm."
            case .unreachable(let why):
                why
            }
        }
    }

    /// Asks the server's /healthz, which every SoundStorm answers with
    /// {"status":"ok","sources":n}, so a typo that lands on some other web
    /// server is caught here rather than as a strange page later.
    static func check(_ server: URL) async throws {
        var request = URLRequest(url: server.appending(path: "healthz"))
        request.timeoutInterval = 10
        request.cachePolicy = .reloadIgnoringLocalCacheData
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await URLSession.shared.data(for: request)
        } catch let error as URLError {
            throw CheckError.unreachable(explain(error, server))
        }
        struct Health: Decodable { let status: String; let sources: Int }
        guard (response as? HTTPURLResponse)?.statusCode == 200,
              let health = try? JSONDecoder().decode(Health.self, from: data),
              health.status == "ok"
        else { throw CheckError.notSoundStorm }
    }

    private static func explain(_ error: URLError, _ server: URL) -> String {
        let host = server.host() ?? server.absoluteString
        switch error.code {
        case .serverCertificateUntrusted, .serverCertificateHasBadDate,
             .serverCertificateHasUnknownRoot, .serverCertificateNotYetValid,
             .secureConnectionFailed:
            return "\(host) has a certificate this iPhone doesn't trust. "
                + "Use the soundstorm.dev address SoundStorm gave you, or install its certificate first."
        case .appTransportSecurityRequiresSecureConnection:
            return "iOS only allows plain http:// on your home network. Use the https:// address."
        case .cannotFindHost, .dnsLookupFailed:
            return "Couldn't find \(host). Check the address."
        case .notConnectedToInternet:
            return "This iPhone is offline."
        case .timedOut, .cannotConnectToHost, .networkConnectionLost:
            return "Couldn't reach \(host). Is SoundStorm running, and is this iPhone on a network that can reach it?"
        default:
            return error.localizedDescription
        }
    }
}
