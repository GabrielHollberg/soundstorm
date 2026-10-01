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
        /// Asked to choose a new password before anything else (the owner
        /// asked everybody, or this one no longer meets the rules).
        let mustRenew: Bool?
    }

    /// Set when the server refuses a request until a new password is chosen.
    private(set) var renewDemanded = false
    /// Whether this person must choose a new password before anything else.
    var mustRenew: Bool { renewDemanded || user?.mustRenew == true }

    /// How a sign-in went: in, or waiting for this new device to be approved.
    enum SignIn {
        case signedIn
        case waiting(id: String, codeAllowed: Bool)
    }

    struct Session: Decodable {
        let hasAccount: Bool
        let signedIn: Bool
        let user: User?
        let approveNewDevices: Bool?
    }

    /// Whether new devices need approval (the owner's setting), known from
    /// the session answer; nil until asked.
    private(set) var approveNewDevices: Bool?

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
        approveNewDevices = s.signedIn ? (s.approveNewDevices ?? false) : nil
        return s
    }

    func signIn(username: String, password: String) async throws -> SignIn {
        struct Body: Encodable { let username: String; let password: String }
        struct Answer: Decodable { let user: User?; let pending: String?; let code: Bool? }
        do {
            let answer: Answer = try await send("POST", "api/login", Body(username: username, password: password))
            // With approval of new devices turned on, the right password on a
            // device the account has not used waits for a yes.
            if let id = answer.pending { return .waiting(id: id, codeAllowed: answer.code ?? false) }
            user = answer.user
            renewDemanded = false
            return .signedIn
        } catch Failure.status(401, _) {
            throw Failure.wrongPassword
        }
    }

    /// A waiting sign-in: true once it is approved (the session cookie then
    /// arrives with the answer), false while it still waits; refused or
    /// expired throws, saying which. With `setupCode`, the server's setup
    /// code approves it.
    func waitingSignIn(_ id: String, setupCode: String? = nil) async throws -> Bool {
        struct Body: Encodable { let setupCode: String }
        struct Answer: Decodable { let signedIn: Bool?; let user: User? }
        let path = "api/login/pending/" + Self.part(id)
        let answer: Answer = if let setupCode {
            try await send("POST", path, Body(setupCode: setupCode))
        } else {
            try await get(path)
        }
        guard answer.signedIn == true else { return false }
        user = answer.user
        renewDemanded = false
        return true
    }

    /// A code for signing this TV in from a phone (the server's tvlink.go).
    struct Link: Decodable {
        let id: String
        let code: String
        let url: String
    }

    func newLink() async throws -> Link {
        struct Empty: Encodable {}
        return try await send("POST", "api/link", Empty())
    }

    /// How a code is going: true once a phone allowed it (the session cookie
    /// arrives with the answer), false while it waits. Run out (404) or not
    /// allowed (403) throws.
    func linkSignedIn(_ id: String) async throws -> Bool {
        struct Answer: Decodable { let signedIn: Bool?; let user: User? }
        let a: Answer = try await get("api/link/" + Self.part(id))
        guard a.signedIn == true else { return false }
        user = a.user
        renewDemanded = false
        return true
    }

    /// The code's address as a QR code, drawn by the server.
    func linkQR(_ id: String) -> URL { url("api/link/" + Self.part(id) + "/qr.png") }

    /// A device asking to sign in, for a device already signed in to approve.
    struct PendingDevice: Decodable, Identifiable {
        let id: String
        let user: String
        let device: String
    }

    /// New devices waiting to sign in to this account (every account, for
    /// the owner). Asked only while approval is turned on.
    func pendingDevices() async -> [PendingDevice] {
        struct Answer: Decodable { let pending: [PendingDevice] }
        if approveNewDevices == nil { _ = try? await session() }
        guard approveNewDevices == true else { return [] }
        let a: Answer? = try? await get("api/devices/pending")
        return a?.pending ?? []
    }

    func answer(_ device: PendingDevice, approve: Bool) async {
        struct Body: Encodable { let approve: Bool }
        struct Answer: Decodable {}
        let _: Answer? = try? await send("POST", "api/devices/pending/" + Self.part(device.id), Body(approve: approve))
    }

    /// Choosing a new password, which needs the current one. Every other
    /// device is signed out; this one stays in.
    func changePassword(current: String, new password: String) async throws {
        struct Body: Encodable { let current: String; let password: String }
        struct Answer: Decodable {}
        let _: Answer = try await send("POST", "api/account/password", Body(current: current, password: password))
        renewDemanded = false
        _ = try? await session()
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

    // MARK: More music

    struct Mix: Decodable, Identifiable, Hashable {
        let id: String
        let title: String
        let subtitle: String?
        let covers: [String]?
        let sourceId: String
    }

    func mixes() async throws -> [Mix] {
        struct Answer: Decodable { let mixes: [Mix] }
        let a: Answer = try await get("api/music/mixes")
        return a.mixes
    }

    func mix(_ id: String) async throws -> [Item] {
        struct Answer: Decodable { let songs: [Item] }
        let a: Answer = try await get("api/music/mixes/\(Self.part(id))")
        return a.songs
    }

    /// A radio station, or a mood: what to ask POST /api/music/radio for.
    struct Station: Decodable, Identifiable, Hashable {
        let mode: String
        let seed: String?
        let title: String
        let subtitle: String?
        let covers: [String]?
        let sourceId: String
        var id: String { mode + "/" + (seed ?? "") }
    }

    struct Radio: Decodable {
        let stations: [Station]
        let moods: [Station]?
    }

    func radio() async throws -> Radio { try await get("api/music/radio") }

    struct Batch: Decodable {
        let title: String?
        let songs: [Item]
        let next: Int?
    }

    /// A station's next songs, leaving out what has played already.
    func tune(_ station: Station, exclude: [String], from: Int?) async throws -> Batch {
        struct Body: Encodable {
            let mode: String
            let seed: String?
            let exclude: [String]
            let from: Int?
            let size: Int
        }
        return try await send("POST", "api/music/radio",
                              Body(mode: station.mode, seed: station.seed, exclude: exclude, from: from, size: 40))
    }

    struct Artist: Decodable, Identifiable, Hashable {
        let id: String
        let sourceId: String
        let name: String
        let albumCount: Int?
        let artId: String?
    }

    func artists() async throws -> [Artist] {
        struct Answer: Decodable { let artists: [Artist] }
        let a: Answer = try await get("api/music/artists")
        return a.artists
    }

    func albums(of artist: Artist) async throws -> [Album] {
        struct Answer: Decodable { let albums: [Album] }
        let a: Answer = try await get("api/music/artists/\(Self.part(artist.sourceId))/\(Self.path(artist.id))")
        return a.albums
    }

    func favorites() async throws -> [Item] {
        struct Answer: Decodable { let items: [Item] }
        let a: Answer = try await get("api/favorites")
        return a.items
    }

    func setFavorite(_ item: Item, _ on: Bool) async throws {
        struct Answer: Decodable {}
        var request = URLRequest(url: url("api/favorites", query: ["source": item.sourceId, "id": item.id]))
        request.httpMethod = on ? "PUT" : "DELETE"
        let _: Answer = try await perform(request)
    }

    func addToPlaylist(_ playlist: Playlist, _ item: Item) async throws {
        struct Body: Encodable { let source: String; let id: String }
        struct Answer: Decodable {}
        let _: Answer = try await send("POST", "api/playlists/\(Self.part(playlist.id))/items",
                                       Body(source: item.sourceId, id: item.id))
    }

    struct Lyrics: Decodable {
        struct Line: Decodable { let start: Int; let text: String }
        let synced: Bool
        let lines: [Line]
    }

    func lyrics(_ item: Item) async -> Lyrics? {
        try? await get("api/music/lyrics/\(Self.part(item.sourceId))/\(Self.path(item.id))")
    }

    /// Part-watched films and episodes (and books, which come later), with
    /// how far through each is.
    func continueWatching() async throws -> [Item] {
        struct Answer: Decodable { let items: [Item] }
        let a: Answer = try await get("api/continue")
        return a.items
    }

    // MARK: Photos

    /// The camera roll, a page at a time, newest first - the page's own
    /// browse of the picture shelf.
    func photos(offset: Int, limit: Int = 60) async throws -> (items: [Item], hasMore: Bool) {
        struct Answer: Decodable { let items: [Item]; let hasMore: Bool? }
        let a: Answer = try await get("api/search", query: ["q": "", "kind": "picture",
                                                             "limit": String(limit), "offset": String(offset)])
        return (a.items, a.hasMore ?? false)
    }

    struct PhotoDay: Decodable, Identifiable {
        let year: Int
        let items: [Item]
        var id: Int { year }
    }

    /// Photos from this day in earlier years, newest year first.
    func onThisDay() async throws -> [PhotoDay] {
        struct Answer: Decodable { let days: [PhotoDay] }
        let a: Answer = try await get("api/photos/on-this-day")
        return a.days
    }

    /// A photo at screen size (Immich's preview), not the original file.
    func previewURL(_ item: Item) -> URL? {
        guard let artId = item.artId else { return nil }
        return url("api/art/\(Self.part(item.sourceId))/\(Self.path(artId + "@preview"))")
    }

    // MARK: Categories (the page's pills)

    /// The account's own order of each tab's categories, and the ones put
    /// away - set on the page (hold and slide, the + button); the TV follows.
    struct Pills: Decodable {
        let pills: [String: [String]]?
        let hiddenPills: [String: [String]]?
    }

    func pills() async -> Pills? { try? await get("api/prefs") }

    /// A page of a shelf, the page's own browse: an empty search of those kinds.
    func page(kinds: [String], offset: Int, limit: Int = 60) async throws -> (items: [Item], hasMore: Bool) {
        struct Answer: Decodable { let items: [Item]; let hasMore: Bool?; let offset: Int? }
        let a: Answer = try await get("api/search", query: ["q": "", "kind": kinds.joined(separator: ","),
                                                             "limit": String(limit), "offset": String(offset)])
        return (a.items, a.hasMore ?? false)
    }

    /// A genre, an author, a series, a person or a place: a name and a cover,
    /// and what it holds.
    struct Group: Decodable, Identifiable, Hashable {
        /// genres, authors, series, people, places
        var kind = ""
        /// A genre's shelves, for opening it.
        var scope: [String] = []
        var id: String
        let name: String
        let subtitle: String?
        let count: Int?
        let coverSource: String?
        let coverArt: String?

        enum Keys: String, CodingKey { case key, id, name, subtitle, count, cover, artId, sourceId, authors }
        init(from d: Decoder) throws {
            let c = try d.container(keyedBy: Keys.self)
            name = try c.decode(String.self, forKey: .name)
            id = (try? c.decode(String.self, forKey: .key)) ?? (try? c.decode(String.self, forKey: .id)) ?? name
            count = try? c.decode(Int.self, forKey: .count)
            let authors = (try? c.decode([String].self, forKey: .authors))?.joined(separator: ", ")
            subtitle = (try? c.decode(String.self, forKey: .subtitle)) ?? authors
            if let cover = try? c.decode(Item.self, forKey: .cover) {
                coverSource = cover.sourceId
                coverArt = cover.artId
            } else {
                coverSource = try? c.decode(String.self, forKey: .sourceId)
                coverArt = try? c.decode(String.self, forKey: .artId)
            }
        }
    }

    func genres(kinds: [String]) async throws -> [Group] {
        struct Answer: Decodable { let genres: [Group] }
        let a: Answer = try await get("api/genres", query: ["kinds": kinds.joined(separator: ",")])
        return a.genres.map { var g = $0; g.kind = "genres"; return g }
    }

    func genre(_ name: String, kinds: [String]) async throws -> [Item] {
        struct Answer: Decodable { let items: [Item] }
        let a: Answer = try await get("api/genres", query: ["kinds": kinds.joined(separator: ","), "name": name])
        return a.items
    }

    func bookGroups(_ which: String) async throws -> [Group] {
        struct Authors: Decodable { let authors: [Group] }
        struct Series: Decodable { let series: [Group] }
        let groups: [Group] = which == "authors"
            ? (try await get("api/books/authors") as Authors).authors
            : (try await get("api/books/series") as Series).series
        return groups.map { var g = $0; g.kind = which; return g }
    }

    /// An author's books (their series' books first, in order) or a series'.
    func bookGroup(_ group: Group) async throws -> [Item] {
        struct Series: Decodable { let books: [Item]? }
        struct Answer: Decodable { let series: [Series]?; let books: [Item]? }
        let a: Answer = try await get("api/books/\(group.kind)", query: ["key": group.id])
        return (a.series ?? []).flatMap { $0.books ?? [] } + (a.books ?? [])
    }

    func photoGroups(_ which: String) async throws -> [Group] {
        struct People: Decodable { let people: [Group]? ; let places: [Group]? }
        let a: People = try await get("api/photos/\(which)")
        return (a.people ?? a.places ?? []).map { var g = $0; g.kind = which; return g }
    }

    func photoGroup(_ group: Group) async throws -> [Item] {
        struct Answer: Decodable { let items: [Item] }
        let a: Answer = try await get("api/photos/\(group.kind)", query: ["id": group.id])
        return a.items
    }

    // MARK: Reading

    /// What an EPUB holds: its files, by path. The server unzips; nothing here
    /// does.
    func bookEntries(_ item: Item) async throws -> [String] {
        struct Entry: Decodable { let name: String }
        struct Answer: Decodable { let entries: [Entry] }
        let a: Answer = try await get("api/book/manifest", query: ["source": item.sourceId, "id": item.id])
        return a.entries.map(\.name)
    }

    /// One file from inside an EPUB.
    func bookResource(_ item: Item, path: String) async throws -> Data {
        var request = URLRequest(url: url("api/book/resource", query: ["source": item.sourceId, "id": item.id, "path": path]))
        request.timeoutInterval = 30
        let (data, response) = try await SafeLoad.data(request, limit: SafeLoad.bookPart)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw Failure.status(0, "Part of the book didn't load.") }
        return data
    }

    /// A whole file - a PDF, which has no inside to ask for.
    func file(_ item: Item) async throws -> Data {
        var request = URLRequest(url: streamURL(item))
        request.timeoutInterval = 120
        let (data, response) = try await SafeLoad.data(request, limit: SafeLoad.wholeFile)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw Failure.status(0, "The file didn't load.") }
        return data
    }

    /// Where this person is in a book: the page's own record, an EPUB CFI and
    /// how far through.
    func readPlace(_ item: Item) async -> (location: String, fraction: Double)? {
        struct Answer: Decodable { let found: Bool; let location: String?; let fraction: Double? }
        guard let a: Answer = try? await get("api/book/progress", query: ["source": item.sourceId, "id": item.id]),
              a.found, let loc = a.location
        else { return nil }
        return (loc, a.fraction ?? 0)
    }

    func saveReadPlace(_ item: Item, location: String, fraction: Double) async {
        struct Body: Encodable { let location: String; let fraction: Double }
        struct Answer: Decodable {}
        var request = URLRequest(url: url("api/book/progress", query: ["source": item.sourceId, "id": item.id]))
        request.httpMethod = "PUT"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try? JSONEncoder().encode(Body(location: location, fraction: min(1, max(0, fraction))))
        _ = try? await perform(request) as Answer
    }

    // MARK: Visualizers

    struct Sound: Decodable { let known: Bool; let tempo: Double; let energy: Double? }

    /// AudioMuse's tempo and energy for a song, where it has heard it.
    func sound(_ item: Item) async -> Sound? {
        let s: Sound? = try? await get("api/music/sound", query: ["id": item.id])
        return s?.known == true ? s : nil
    }

    /// The page's hearing version: an answer of any other is heard again.
    nonisolated static let heardVersion = 8

    /// What the server heard in a song (internal/beats), as the page keeps it.
    struct KeptHearing: Decodable, Sendable {
        let v: Int
        let fps: Double
        let down: Int
        let loud, low, high, beats: String
    }

    func heardOnServer(_ item: Item) async -> KeptHearing? {
        try? await get("api/music/beats", query: ["source": item.sourceId, "id": item.id, "v": String(Self.heardVersion)])
    }

    /// The song at a lower bitrate, to a file, for hearing it: what the page
    /// hears too (stream?kbps=96, listen=1 so it is never paced).
    func download(_ item: Item, kbps: Int) async throws -> URL {
        var parts = URLComponents(url: streamURL(item), resolvingAgainstBaseURL: false)!
        parts.queryItems = [URLQueryItem(name: "kbps", value: String(kbps)), URLQueryItem(name: "listen", value: "1")]
        // Hearing decodes the whole song into memory; past about half an hour
        // at 96 kbps (the server's own limit for hearing) it is not heard, and
        // not downloaded past that either.
        let (temp, response) = try await SafeLoad.download(URLRequest(url: parts.url!), limit: 24 << 20)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else {
            try? FileManager.default.removeItem(at: temp)
            throw Failure.status(0, nil)
        }
        // A name AVFoundation can tell the kind of.
        let file = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString + ".mp3")
        try FileManager.default.moveItem(at: temp, to: file)
        return file
    }

    /// The Now Playing look, the page's own setting.
    func coverStyle() async -> String? {
        struct Prefs: Decodable { let coverStyle: String? }
        let p: Prefs? = try? await get("api/prefs")
        return p?.coverStyle
    }

    func setCoverStyle(_ v: String) async {
        struct Body: Encodable { let coverStyle: String }
        struct Answer: Decodable {}
        let _: Answer? = try? await send("PATCH", "api/prefs", Body(coverStyle: v))
    }

    // MARK: Read Along

    /// A book somebody has both as an ebook and an audiobook.
    struct Pair: Decodable, Identifiable, Hashable {
        struct Sync: Decodable, Hashable { let state: String; let progress: Double? }
        let ebook: Item
        let audiobook: Item
        let sync: Sync?
        var id: String { ebook.key + "|" + audiobook.key }
    }

    func pairs() async throws -> [Pair] {
        struct Answer: Decodable { let pairs: [Pair] }
        let a: Answer = try await get("api/books/pairs")
        return a.pairs
    }

    /// One sentence on the recording's whole timeline, and where it is in the
    /// synced book ("OEBPS/Text/ch01.xhtml#s0012").
    struct Moment: Decodable {
        let t: Double
        let e: Double
        let h: String
    }

    /// Storyteller's synced copy of the book, and its sentences' times.
    func readAlong(_ pair: Pair) async throws -> (book: Item, timeline: [Moment]) {
        struct Answer: Decodable { let item: Item?; let timeline: [Moment]? }
        let a: Answer = try await get("api/readalong", query: [
            "ebookSource": pair.ebook.sourceId, "ebookId": pair.ebook.id,
            "audiobookSource": pair.audiobook.sourceId, "audiobookId": pair.audiobook.id,
        ])
        guard let book = a.item else { throw Failure.status(0, "This book is still syncing.") }
        return (book, a.timeline ?? [])
    }

    /// Whether the sentence being read is lit, the account's setting.
    func readAlongHighlight() async -> Bool {
        struct Prefs: Decodable { let readAlongHighlight: Bool? }
        let p: Prefs? = try? await get("api/prefs")
        return p?.readAlongHighlight ?? true
    }

    func setReadAlongHighlight(_ on: Bool) async {
        struct Body: Encodable { let readAlongHighlight: Bool }
        struct Answer: Decodable {}
        let _: Answer? = try? await send("PATCH", "api/prefs", Body(readAlongHighlight: on))
    }

    // MARK: Audiobooks

    struct Chapter: Decodable, Hashable {
        let title: String
        let startSeconds: Double
    }

    /// What /api/playback says about a book: its file or files on the book's
    /// own clock, its chapters, and where this person got to.
    struct BookPlayback: Decodable {
        struct Track: Decodable {
            let title: String
            let url: String
            let startSeconds: Double
        }
        struct Position: Decodable {
            let seconds: Double
            let duration: Double?
            let finished: Bool?
        }
        let url: String
        let tracks: [Track]?
        let chapters: [Chapter]?
        let position: Position?
    }

    func bookPlayback(_ item: Item) async throws -> BookPlayback {
        try await get("api/playback/\(Self.part(item.sourceId))/\(Self.path(item.id))")
    }

    /// Upstream, to Audiobookshelf, so its own apps carry on from here too.
    func saveBookPosition(_ item: Item, seconds: Double, duration: Double, finished: Bool) async {
        struct Body: Encodable { let seconds: Double; let duration: Double; let finished: Bool }
        struct Answer: Decodable {}
        let _: Answer? = try? await send("PUT", "api/playback/\(Self.part(item.sourceId))/\(Self.path(item.id))",
                                         Body(seconds: seconds, duration: duration, finished: finished))
    }

    /// The account's audiobook speed, the page's own setting.
    func bookSpeed() async -> Double {
        struct Prefs: Decodable { let audiobookSpeed: Double? }
        let p: Prefs? = try? await get("api/prefs")
        let v = p?.audiobookSpeed ?? 1
        return (0.5...3.5).contains(v) ? v : 1
    }

    func setBookSpeed(_ v: Double) async {
        struct Body: Encodable { let audiobookSpeed: Double }
        struct Answer: Decodable {}
        let _: Answer? = try? await send("PATCH", "api/prefs", Body(audiobookSpeed: v))
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
        struct Subtitle: Decodable, Hashable {
            let label: String?
            let language: String?
            let forced: Bool?
            let url: String
        }
        struct Audio: Decodable, Hashable {
            let index: Int
            let label: String?
            let language: String?
            let `default`: Bool?
        }
        let mode: String
        let url: String
        let subtitles: [Subtitle]?
        let audio: [Audio]?
    }

    /// How a film plays: the file itself, or Jellyfin's HLS through the
    /// server when the file is not something the TV can play as it is. Another
    /// audio language is always HLS with that stream (`audio`).
    func playback(_ item: Item, audio: Int? = nil) async throws -> Playback {
        try await get("api/playback/\(Self.part(item.sourceId))/\(Self.path(item.id))",
                      query: audio.map { ["audio": String($0)] } ?? [:])
    }

    /// A subtitle track's text: WebVTT, fetched with the session like the rest.
    func text(at path: String) async throws -> String {
        let (data, response) = try await SafeLoad.data(from: try absolute(path), limit: SafeLoad.bookPart)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw Failure.status(0, "Subtitles didn't load.") }
        return String(decoding: data, as: UTF8.self)
    }

    /// The server gives paths ("/api/hls/..."); the player needs them whole.
    /// Only ever on the server: an answer naming another host (a full URL in a
    /// playback, track or subtitle) is refused rather than followed, with the
    /// session cookie, somewhere else - the security review's finding.
    func absolute(_ path: String) throws -> URL {
        guard let url = URL(string: path, relativeTo: server)?.absoluteURL,
              url.scheme == server.scheme, url.host() == server.host(), url.port == server.port
        else { throw Failure.status(0, "The server pointed somewhere else.") }
        return url
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
    /// A song or file; `kbps` asks for a converted copy (only 96 to 320 are
    /// honoured), as the page does on a slow link.
    func streamURL(_ item: Item, kbps: Int? = nil) -> URL {
        url("api/stream/\(Self.part(item.sourceId))/\(Self.path(item.id))",
            query: kbps.map { ["kbps": String($0)] } ?? [:])
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
            // URLComponents leaves "+", which a server reads as a space:
            // "C++" searched as "C  "; and ";", which Go refuses outright,
            // dropping the whole value.
            parts.percentEncodedQuery = parts.percentEncodedQuery?
                .replacingOccurrences(of: "+", with: "%2B").replacingOccurrences(of: ";", with: "%3B")
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
            let problem = try? JSONDecoder().decode(Problem.self, from: data)
            // Held to choosing a new password: nothing else answers until then.
            if code == 403 && problem?.mustRenew == true { renewDemanded = true }
            throw Failure.status(code, problem?.error)
        }
        if data.isEmpty, let empty = EmptyAnswer() as? T { return empty }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

private struct EmptyAnswer: Decodable {}
private struct Problem: Decodable { let error: String; let mustRenew: Bool? }

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
    /// How far through, in the Continue row.
    let progress: Double?

    /// The same song from two lists is the same song.
    var key: String { sourceId + "/" + id }
    var artist: String { creators?.joined(separator: ", ") ?? subtitle ?? "" }
    var album: String? { extra?["album"] }
    /// "S01E02", on an episode; a show has none.
    var episodeCode: String? { extra?["episode"] }
    var season: String { extra?["season"] ?? "0" }
    /// A clip from a camera roll: a film, not a photo.
    var isClip: Bool { kind == "picture" && extra?["type"] == "video" }
    var isPhoto: Bool { kind == "picture" && !isClip }
    var isVideo: Bool { kind == "video" || (kind == "tv" && episodeCode != nil) || isClip }
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
