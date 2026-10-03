import Foundation

/// Finding a SoundStorm server on the network the device is on, so somebody
/// setting up a TV never has to type an address (the owner's asking: "some
/// rando buys it, plugs it in" - the TV should say it found the server).
///
/// Bonjour was the obvious way and does not work here: SoundStorm runs in
/// Docker, which on Windows and macOS keeps a container's announcements off
/// the home network. So the device asks every address on its own network
/// whether SoundStorm answers on its port (`/healthz`, which says so) - 254
/// quick questions at once, a second or two. A server that answers is then
/// asked its secure home name (`/api/session`'s `secureName`), which is kept
/// rather than the bare address when it answers too.
nonisolated enum ServerDiscovery {
    struct Found: Hashable, Identifiable {
        /// The address to keep: the secure home name if it answered, else
        /// the plain address on the network.
        let url: URL
        /// What to call it on screen.
        var label: String {
            let host = url.host() ?? url.absoluteString
            return host.hasSuffix(".home.soundstorm.dev") ? String(host.dropLast(".home.soundstorm.dev".count)) : host
        }
        var id: String { url.absoluteString }
    }

    /// SoundStorm's own port; a server moved to another is typed in.
    static let port = 8099

    /// The servers on this device's network, best address each.
    static func search() async -> [Found] {
        let hosts = neighbours()
        guard !hosts.isEmpty else { return [] }
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 2
        config.httpMaximumConnectionsPerHost = 1
        let session = URLSession(configuration: config)
        defer { session.invalidateAndCancel() }
        let answering = await withTaskGroup(of: URL?.self) { group in
            for host in hosts {
                group.addTask {
                    guard let url = URL(string: "http://\(host):\(port)"),
                          await isSoundStorm(url, session) else { return nil }
                    return url
                }
            }
            var out: [URL] = []
            for await url in group { if let url { out.append(url) } }
            return out
        }
        var found: [Found] = []
        for plain in answering.sorted(by: { $0.absoluteString < $1.absoluteString }) {
            found.append(Found(url: await secureName(of: plain, session) ?? plain))
        }
        return Array(Set(found)).sorted { $0.label < $1.label }
    }

    private static func isSoundStorm(_ url: URL, _ session: URLSession) async -> Bool {
        guard let (data, response) = try? await session.data(from: url.appending(path: "healthz")),
              (response as? HTTPURLResponse)?.statusCode == 200 else { return false }
        struct Health: Decodable { let status: String; let sources: Int }
        return (try? JSONDecoder().decode(Health.self, from: data))?.status == "ok"
    }

    /// The install's secure home name, if it has one and it answers from here.
    private static func secureName(of plain: URL, _ session: URLSession) async -> URL? {
        struct Session: Decodable { let secureName: String? }
        guard let (data, _) = try? await session.data(from: plain.appending(path: "api/session")),
              let name = (try? JSONDecoder().decode(Session.self, from: data))?.secureName,
              name.hasSuffix(".soundstorm.dev"),
              let secure = URL(string: "https://\(name):\(port)"),
              await isSoundStorm(secure, session) else { return nil }
        return secure
    }

    /// Every address on the device's own network (its IPv4 /24 - a
    /// wider network is searched only around the device, to stay quick),
    /// and on 192.168.0.x and 192.168.1.x.
    private static func neighbours() -> [String] {
        var list: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&list) == 0, let first = list else { return [] }
        defer { freeifaddrs(list) }
        var mine: [UInt32] = []
        for p in sequence(first: first, next: { $0.pointee.ifa_next }) {
            let a = p.pointee
            guard let addr = a.ifa_addr, addr.pointee.sa_family == UInt8(AF_INET),
                  (Int32(a.ifa_flags) & IFF_UP) != 0, (Int32(a.ifa_flags) & IFF_LOOPBACK) == 0,
                  let name = String(validatingCString: a.ifa_name), name.hasPrefix("en") else { continue }
            let ip = addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { UInt32(bigEndian: $0.pointee.sin_addr.s_addr) }
            // Private networks only: a public address is not a home network.
            let isPrivate = ip >> 24 == 10 || ip >> 20 == 0xAC1 || ip >> 16 == 0xC0A8
            if isPrivate { mine.append(ip) }
        }
        var hosts: [String] = []
        // And the two networks routers most often make, when the device is on
        // a private network: a mesh or second router inside the first puts the
        // TV on one network and the server on the one outside it (the owner's
        // own house: 192.168.86.x inside 192.168.0.x).
        if !mine.isEmpty { mine += [0xC0A8_0000, 0xC0A8_0100] }
        for ip in Set(mine.map { $0 & 0xFFFF_FF00 }) {
            let base = ip & 0xFFFF_FF00
            for n in UInt32(1)...254 {
                let v = base | n
                hosts.append("\(v >> 24).\((v >> 16) & 255).\((v >> 8) & 255).\(v & 255)")
            }
        }
        return Array(Set(hosts))
    }
}
