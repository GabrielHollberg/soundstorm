import Foundation

/// The same /api the web page uses. tvOS has no web view, so the Apple TV app
/// talks to the server directly. The session is the server's cookie, kept in
/// the shared cookie store like a browser keeps it; nothing here sends an
/// Origin header, which is the only check the server makes on a POST.
@Observable
final class API {
    let server: URL
    private(set) var user: User?

    init(server: URL) {
        self.server = server
    }

    // MARK: Session

    struct User: Decodable {
        let id: String
        let name: String
        let owner: Bool?
    }

    struct Session: Decodable {
        let hasAccount: Bool
        let signedIn: Bool
        let user: User?
    }

    enum Failure: LocalizedError {
        case status(Int, String?)
        case wrongPassword
        case throttled(Int?)

        var errorDescription: String? {
            switch self {
            case .wrongPassword: "That username and password don't match."
            case .throttled(let seconds):
                "Too many tries. Wait \(seconds.map { "\($0) seconds" } ?? "a minute") and try again."
            case .status(let code, let message): message ?? "The server answered \(code)."
            }
        }
    }

    func session() async throws -> Session {
        let s: Session = try await get("api/session")
        user = s.signedIn ? s.user : nil
        return s
    }

    func signIn(username: String, password: String) async throws {
        struct Body: Encodable { let username: String; let password: String }
        struct Answer: Decodable { let user: User? }
        do {
            let answer: Answer = try await send("POST", "api/login", Body(username: username, password: password))
            user = answer.user
        } catch Failure.status(401, _) {
            throw Failure.wrongPassword
        }
    }

    func signOut() async {
        struct Empty: Encodable {}
        struct Answer: Decodable {}
        _ = try? await send("POST", "api/logout", Empty()) as Answer
        user = nil
    }

    // MARK: Library

    struct Home: Decodable {
        let albums: [Album]
        let shelves: [Shelf]
    }

    struct Shelf: Decodable {
        let kind: String
        let items: [Item]
    }

    func home() async throws -> Home { try await get("api/home") }

    func recentlyPlayed() async throws -> [Item] {
        struct Answer: Decodable { let songs: [Item] }
        let a: Answer = try await get("api/music/mixes/recently-played")
        return a.songs
    }

    func albums(order: String = "recent") async throws -> [Album] {
        struct Answer: Decodable { let albums: [Album] }
        let a: Answer = try await get("api/music/albums", query: ["order": order])
        return a.albums
    }

    func album(_ album: Album) async throws -> [Item] {
        struct Answer: Decodable { let songs: [Item] }
        let a: Answer = try await get("api/music/albums/\(Self.part(album.sourceId))/\(Self.path(album.id))")
        return a.songs
    }

    func playlists() async throws -> [Playlist] {
        struct Answer: Decodable { let playlists: [Playlist] }
        let a: Answer = try await get("api/playlists")
        return a.playlists
    }

    func playlist(_ playlist: Playlist) async throws -> [Item] {
        struct Answer: Decodable { let items: [Item] }
        let a: Answer = try await get("api/playlists/\(Self.part(playlist.id))")
        return a.items
    }

    func search(_ q: String, kind: String = "music", limit: Int = 60) async throws -> [Item] {
        struct Answer: Decodable { let items: [Item] }
        let a: Answer = try await get("api/search", query: ["q": q, "kind": kind, "limit": String(limit)])
        return a.items
    }

    /// Counts a song as heard: history, mixes and scrobbling on the server.
    func recordPlayed(_ item: Item) async {
        struct Body: Encodable { let source: String; let id: String }
        struct Answer: Decodable {}
        _ = try? await send("POST", "api/history", Body(source: item.sourceId, id: item.id)) as Answer
    }

    // MARK: Films and TV

    /// Films, or TV shows (series only: a search with no text lists shows,
    /// and an episode is found by opening its show).
    func browse(kind: String, limit: Int = 200) async throws -> [Item] {
        try await search("", kind: kind, limit: limit)
    }

    struct Show: Decodable {
        let episodes: [Item]
        let progress: [String: Double]?
    }

    func show(_ series: Item) async throws -> Show {
        try await get("api/tv/show", query: ["source": series.sourceId, "id": series.id])
    }

    func nextEpisode(after episode: Item) async -> Item? {
        struct Answer: Decodable { let found: Bool; let episode: Item? }
        let a: Answer? = try? await get("api/tv/next", query: ["source": episode.sourceId, "id": episode.id])
        return a?.found == true ? a?.episode : nil
    }

    struct Playback: Decodable {
        let mode: String
        let url: String
    }

    /// How a film plays: the file itself, or Jellyfin's HLS through the
    /// server when the file is not something the TV can play as it is.
    func playback(_ item: Item) async throws -> Playback {
        try await get("api/playback/\(Self.part(item.sourceId))/\(Self.path(item.id))")
    }

    /// The server gives paths ("/api/hls/..."); the player needs them whole.
    func absolute(_ path: String) -> URL {
        URL(string: path, relativeTo: server)?.absoluteURL ?? server
    }

    /// Where this person stopped in a film or episode: the web page's own
    /// record (/api/book/progress, "t=<seconds>"), so the TV and the page
    /// carry on from the same place.
    func watchedSeconds(_ item: Item) async -> Double? {
        struct Answer: Decodable { let found: Bool; let location: String? }
        guard let a: Answer = try? await get("api/book/progress", query: ["source": item.sourceId, "id": item.id]),
              a.found, let loc = a.location, loc.hasPrefix("t=")
        else { return nil }
        return Double(loc.dropFirst(2))
    }

    func saveWatched(_ item: Item, seconds: Double, length: Double) async {
        struct Body: Encodable { let location: String; let fraction: Double }
        struct Answer: Decodable {}
        guard seconds > 5, length > 0 else { return }
        let body = Body(location: String(format: "t=%.1f", seconds), fraction: min(1, max(0, seconds / length)))
        var request = URLRequest(url: url("api/book/progress", query: ["source": item.sourceId, "id": item.id]))
        request.httpMethod = "PUT"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try? JSONEncoder().encode(body)
        _ = try? await perform(request) as Answer
    }

    // MARK: URLs

    /// The song's bytes, original quality; ranges work.
    func streamURL(_ item: Item) -> URL {
        url("api/stream/\(Self.part(item.sourceId))/\(Self.path(item.id))")
    }

    func artURL(source: String, artId: String?, size: Int = 400) -> URL? {
        guard let artId, !artId.isEmpty else { return nil }
        return url("api/art/\(Self.part(source))/\(Self.path(artId))", query: ["size": String(size)])
    }

    /// The cookies AVPlayer must send: it does not read the shared store by
    /// itself, and every stream and cover is refused without the session.
    var cookies: [HTTPCookie] { HTTPCookieStorage.shared.cookies(for: server) ?? [] }

    // MARK: Plumbing

    private func url(_ path: String, query: [String: String] = [:]) -> URL {
        var parts = URLComponents(url: server, resolvingAgainstBaseURL: false)!
        parts.percentEncodedPath = "/" + path
        if !query.isEmpty {
            parts.queryItems = query.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) }
        }
        return parts.url!
    }

    /// One path segment, fully encoded - a source id never holds a slash.
    static func part(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(CharacterSet(charactersIn: "-._~"))) ?? s
    }

    /// An item id, each part encoded and its slashes kept, as app.js's
    /// escapeId does: Navidrome's ids are plain, a folder source's are paths.
    static func path(_ s: String) -> String {
        s.split(separator: "/", omittingEmptySubsequences: false).map { part(String($0)) }.joined(separator: "/")
    }

    private func get<T: Decodable>(_ path: String, query: [String: String] = [:]) async throws -> T {
        try await perform(URLRequest(url: url(path, query: query)))
    }

    private func send<B: Encodable, T: Decodable>(_ method: String, _ path: String, _ body: B) async throws -> T {
        var request = URLRequest(url: url(path))
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(body)
        return try await perform(request)
    }

    private func perform<T: Decodable>(_ request: URLRequest) async throws -> T {
        var request = request
        request.timeoutInterval = 20
        let (data, response) = try await URLSession.shared.data(for: request)
        let http = response as? HTTPURLResponse
        let code = http?.statusCode ?? 0
        guard (200..<300).contains(code) else {
            if code == 429 {
                throw Failure.throttled(http?.value(forHTTPHeaderField: "Retry-After").flatMap(Int.init))
            }
            throw Failure.status(code, (try? JSONDecoder().decode(Problem.self, from: data))?.error)
        }
        if data.isEmpty, let empty = EmptyAnswer() as? T { return empty }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

private struct EmptyAnswer: Decodable {}
private struct Problem: Decodable { let error: String }

// MARK: Models - media.Item and friends, as the server writes them.

struct Item: Decodable, Identifiable, Hashable {
    let id: String
    let sourceId: String
    let kind: String
    let title: String
    let subtitle: String?
    let creators: [String]?
    let artId: String?
    let durationSeconds: Double?
    let extra: [String: String]?

    /// The same song from two lists is the same song.
    var key: String { sourceId + "/" + id }
    var artist: String { creators?.joined(separator: ", ") ?? subtitle ?? "" }
    var album: String? { extra?["album"] }
    /// "S01E02", on an episode; a show has none.
    var episodeCode: String? { extra?["episode"] }
    var season: String { extra?["season"] ?? "0" }
    var isVideo: Bool { kind == "video" || (kind == "tv" && episodeCode != nil) }
}

struct Album: Decodable, Identifiable, Hashable {
    let id: String
    let sourceId: String
    let title: String
    let artist: String
    let year: Int?
    let songCount: Int?
    let artId: String?
}

struct Playlist: Decodable, Identifiable, Hashable {
    struct Cover: Decodable, Hashable { let sourceId: String; let artId: String }
    let id: String
    let name: String
    let count: Int
    let covers: [Cover]?
}
