import Foundation

/// The one SoundStorm server this app talks to. Every install is someone's
/// own, so the address is asked for on first launch rather than built in.
enum ServerAddress {
    private static let key = "serverURL"

    /// Kept as a plain string, so it can also be given at launch for testing:
    /// `xcrun simctl launch booted dev.soundstorm.app -serverURL http://localhost:8080`
    static var saved: URL? {
        get { UserDefaults.standard.string(forKey: key).flatMap(parse) }
        set { UserDefaults.standard.set(newValue?.absoluteString, forKey: key) }
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
