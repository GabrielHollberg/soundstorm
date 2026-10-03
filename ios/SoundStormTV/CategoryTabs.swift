import SwiftUI

/// Each tab's categories, as the web page has them (TABS in app.js): in the
/// account's own order and without the ones put away (both set on the page;
/// the TV follows). Genres start put away, as on the page.
enum Categories {
    static let home: [(value: String, label: String)] = [("", "Home"), ("favorites", "Favorites")]
    static let music: [(value: String, label: String)] = [
        ("mixes", "Mixes"), ("radio", "Radio"), ("playlists", "Playlists"), ("songs", "Songs"),
        ("albums", "Albums"), ("artists", "Artists"), ("favorites", "Favorites"), ("genres", "Genres"),
    ]
    static let watch: [(value: String, label: String)] = [
        ("video", "Films"), ("tv", "TV"), ("fav-watch", "Favorites"), ("genres-watch", "Genres"),
    ]
    static let books: [(value: String, label: String)] = [
        ("audiobook", "Audiobooks"), ("ebook", "Ebooks"), ("authors", "Authors"), ("series", "Series"),
        ("pairs", "Read Along"), ("document", "Documents"), ("fav-books", "Favorites"), ("genres-books", "Genres"),
    ]
    static let photos: [(value: String, label: String)] = [
        ("picture", "Photos"), ("people", "People"), ("places", "Places"), ("photo-videos", "Videos"),
        ("photo-live", "Live photos"), ("fav-photos", "Favorites"),
    ]
    static let hiddenAtFirst: [String: [String]] = [
        "music": ["genres"], "watch": ["genres-watch"], "books": ["genres-books"],
    ]

    /// The row as this account has it.
    static func arranged(_ row: String, _ all: [(value: String, label: String)], _ pills: API.Pills?) -> [(value: String, label: String)] {
        // The page once spelled it "favourites"; a saved order may still.
        let order = (pills?.pills?[row] ?? []).map { $0 == "favourites" ? "favorites" : $0 }
        let hidden = pills?.hiddenPills?[row] ?? hiddenAtFirst[row] ?? []
        let rank = { (v: String) in order.firstIndex(of: v) ?? Int.max }
        return all.enumerated()
            .filter { !hidden.contains($0.element.value) }
            .sorted { (rank($0.element.value), $0.offset) < (rank($1.element.value), $1.offset) }
            .map(\.element)
    }
}

/// A tab: its categories along the top, and the chosen one's page beneath.
struct CategoryTab<Content: View>: View {
    let row: String
    let all: [(value: String, label: String)]
    @ViewBuilder let content: (String) -> Content
    @Environment(AppModel.self) private var model
    @State private var chosen: String? = {
        #if DEBUG
        // For the simulator, which has no remote: -category <value>
        return UserDefaults.standard.string(forKey: "category")
        #else
        return nil
        #endif
    }()

    var body: some View {
        let shown = Categories.arranged(row, all, model.pills).filter { row == "home" || model.has(category: $0.value) }
        let current = chosen.flatMap { c in shown.contains { $0.value == c } ? c : nil } ?? shown.first?.value ?? ""
        NavigationStack {
            VStack(alignment: .leading, spacing: 0) {
                if shown.count > 1 {
                    CategoryBar(items: shown, selection: Binding(get: { current }, set: { chosen = $0 }))
                }
                content(current)
                    .id(current) // a fresh page, and a fresh load, per category
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            }
            .libraryDestinations()
        }
        .onAppear {
            if let c = model.openCategory.removeValue(forKey: row) { chosen = c }
        }
        .onChange(of: model.openCategory[row]) { _, c in
            if let c { chosen = c; model.openCategory[row] = nil }
        }
    }
}

extension View {
    /// Where a card leads, in any tab.
    func libraryDestinations() -> some View {
        navigationDestination(for: Album.self) { AlbumView(album: $0) }
            .navigationDestination(for: Playlist.self) { PlaylistView(playlist: $0) }
            .navigationDestination(for: API.Artist.self) { ArtistView(artist: $0) }
            .navigationDestination(for: Item.self) { ShowView(series: $0) }
            .navigationDestination(for: API.Group.self) { GroupView(group: $0) }
    }
}

// MARK: The tabs

struct HomeTab: View {
    @Environment(API.self) private var api

    var body: some View {
        NavigationStack {
            HomeView().libraryDestinations()
        }
    }
}

struct MusicTab: View {
    @Environment(API.self) private var api

    var body: some View {
        CategoryTab(row: "music", all: Categories.music) { c in
            switch c {
            case "mixes": MixesPage()
            case "radio": RadioPage()
            case "playlists": PlaylistsPage()
            case "songs": PagedItems(kinds: ["music"])
            case "albums": AlbumsPage()
            case "artists": ArtistsPage()
            case "favorites": ItemsPage { try await api.favorites().filter { $0.kind == "music" } }
            case "genres": GroupsPage(round: false) { try await api.genres(kinds: ["music"]).scoped(["music"]) }
            default: EmptyView()
            }
        }
    }
}

struct WatchTab: View {
    @Environment(API.self) private var api

    var body: some View {
        CategoryTab(row: "watch", all: Categories.watch) { c in
            switch c {
            case "video": PagedItems(kinds: ["video"])
            case "tv": PagedItems(kinds: ["tv"])
            case "fav-watch": ItemsPage { try await api.favorites().filter { $0.kind == "video" || $0.kind == "tv" } }
            case "genres-watch": GroupsPage(round: false) { try await api.genres(kinds: ["video", "tv"]).scoped(["video", "tv"]) }
            default: EmptyView()
            }
        }
        #if DEBUG
        .modifier(DebugAutovideo())
        #endif
    }
}

struct BooksTab: View {
    @Environment(API.self) private var api

    var body: some View {
        CategoryTab(row: "books", all: Categories.books) { c in
            switch c {
            case "audiobook": BooksView()
            case "ebook": PagedItems(kinds: ["ebook"])
            case "document": PagedItems(kinds: ["document"])
            case "pairs": PairsPage()
            case "authors": GroupsPage(round: true) { try await api.bookGroups("authors") }
            case "series": GroupsPage(round: false) { try await api.bookGroups("series") }
            case "fav-books": ItemsPage { try await api.favorites().filter { ["audiobook", "ebook", "document"].contains($0.kind) } }
            case "genres-books": GroupsPage(round: false) {
                try await api.genres(kinds: ["audiobook", "ebook"]).scoped(["audiobook", "ebook"])
            }
            default: EmptyView()
            }
        }
    }
}

struct PhotosTab: View {
    @Environment(API.self) private var api

    var body: some View {
        CategoryTab(row: "photos", all: Categories.photos) { c in
            switch c {
            case "picture": PhotosView()
            case "people": GroupsPage(round: true) { try await api.photoGroups("people") }
            case "places": GroupsPage(round: false) { try await api.photoGroups("places") }
            case "photo-videos": ItemsPage { try await api.photos(ofType: "video") }
            case "photo-live": ItemsPage { try await api.photos(ofType: "live") }
            case "fav-photos": ItemsPage { try await api.favorites().filter { $0.kind == "picture" } }
            default: EmptyView()
            }
        }
    }
}

// MARK: Pages

/// A shelf a page at a time, as it scrolls - the page's own browse.
struct PagedItems: View {
    let kinds: [String]
    @Environment(API.self) private var api
    @State private var items: [Item] = []
    @State private var hasMore = true
    @State private var loading = false
    @State private var loaded = false
    /// How far into the server's list: not items.count, since repeats are
    /// left out, and asking from fewer fetched the same page again.
    @State private var offset = 0

    var body: some View {
        ScrollView {
            if loaded && items.isEmpty { Nothing() }
            ItemGrid(items: items) { item in
                if item.key == items.last?.key { Task { await more() } }
            }
        }
        .task { await more() }
        #if DEBUG
        .modifier(DebugOpenFirst(items: kinds == ["tv"] ? items : []) { ShowView(series: $0) })
        #endif
    }

    private func more() async {
        guard hasMore, !loading else { return }
        loading = true
        defer { loading = false; loaded = true }
        guard let page = try? await api.page(kinds: kinds, offset: offset) else { return }
        offset += page.items.count
        let known = Set(items.map(\.key))
        items += page.items.filter { !known.contains($0.key) }
        hasMore = page.hasMore && !page.items.isEmpty
    }
}

/// A list fetched whole: favorites, a genre, an author, a person.
struct ItemsPage: View {
    let load: () async throws -> [Item]
    @State private var items: [Item] = []
    @State private var loaded = false

    var body: some View {
        ScrollView {
            if loaded && items.isEmpty { Nothing() }
            ItemGrid(items: items)
        }
        .task {
            items = ((try? await load()) ?? []).filter(\.playsHere)
            loaded = true
        }
    }
}

/// Genres, authors, series, people or places: each opens what it holds.
struct GroupsPage: View {
    let round: Bool
    let load: () async throws -> [API.Group]
    @Environment(API.self) private var api
    @State private var groups: [API.Group] = []
    @State private var loaded = false

    var body: some View {
        ScrollView {
            if loaded && groups.isEmpty { Nothing() }
            LazyVGrid(columns: Theme.grid, spacing: 70) {
                ForEach(groups) { g in
                    NavigationLink(value: g) {
                        Cover(url: g.coverSource.flatMap { api.artURL(source: $0, artId: g.coverArt) })
                            .frame(width: Theme.card, height: Theme.card)
                            .clipShape(round ? AnyShape(Circle()) : AnyShape(RoundedRectangle(cornerRadius: 12)))
                    }
                    .buttonStyle(CardButton())
                    .overlay(alignment: .bottom) {
                        CardTitle(title: g.name.isEmpty ? "Add a name on the web" : g.name,
                                  subtitle: g.subtitle ?? g.count.map { "\($0)" })
                            .frame(width: 270)
                    }
                    .padding(.bottom, CardTitle.room)
                }
            }
            .padding(.vertical, 40)
        }
        .task {
            groups = (try? await load()) ?? []
            loaded = true
        }
    }
}

/// One genre, author, series, person or place.
struct GroupView: View {
    let group: API.Group
    @Environment(API.self) private var api

    var body: some View {
        VStack(alignment: .leading) {
            Text(group.name).font(.title2).padding(.leading, 60)
            ItemsPage {
                switch group.kind {
                case "genres": return try await api.genre(group.id, kinds: group.scope)
                case "authors", "series": return try await api.bookGroup(group)
                default: return try await api.photoGroup(group)
                }
            }
        }
    }
}

struct MixesPage: View {
    @Environment(API.self) private var api
    @State private var mixes: [API.Mix] = []

    var body: some View {
        ScrollView {
            LazyVGrid(columns: Theme.grid, spacing: 70) {
                ForEach(mixes) { MixCard(mix: $0) }
            }
            .padding(.vertical, 40)
        }
        .task { mixes = (try? await api.mixes()) ?? [] }
    }
}

struct RadioPage: View {
    @Environment(API.self) private var api
    @State private var radio: API.Radio?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 50) {
                if let radio, !radio.stations.isEmpty {
                    Row(title: "Stations") { ForEach(radio.stations) { StationCard(station: $0) } }
                }
                if let moods = radio?.moods, !moods.isEmpty {
                    Row(title: "Moods") { ForEach(moods) { StationCard(station: $0) } }
                }
            }
            .padding(.vertical, 40)
        }
        .task { radio = try? await api.radio() }
    }
}

struct PlaylistsPage: View {
    @Environment(API.self) private var api
    @State private var playlists: [Playlist] = []
    @State private var loaded = false

    var body: some View {
        ScrollView {
            if loaded && playlists.isEmpty { Nothing() }
            LazyVGrid(columns: Theme.grid, spacing: 70) {
                ForEach(playlists) { PlaylistCard(playlist: $0) }
            }
            .padding(.vertical, 40)
        }
        .task { playlists = (try? await api.playlists()) ?? []; loaded = true }
    }
}

struct AlbumsPage: View {
    @Environment(API.self) private var api
    @State private var albums: [Album] = []

    var body: some View {
        ScrollView {
            LazyVGrid(columns: Theme.grid, spacing: 70) {
                ForEach(albums) { AlbumCard(album: $0) }
            }
            .padding(.vertical, 40)
        }
        .task { albums = (try? await api.albums(order: "name")) ?? [] }
        #if DEBUG
        .modifier(DebugOpenFirst(items: albums) { AlbumView(album: $0) })
        #endif
    }
}

struct ArtistsPage: View {
    @Environment(API.self) private var api
    @State private var artists: [API.Artist] = []

    var body: some View {
        ScrollView {
            LazyVGrid(columns: Theme.grid, spacing: 70) {
                ForEach(artists) { ArtistCard(artist: $0) }
            }
            .padding(.vertical, 40)
        }
        #if DEBUG
        .modifier(DebugOpenFirst(items: artists) { ArtistView(artist: $0) })
        #endif
        .task { artists = (try? await api.artists()) ?? [] }
    }
}

// MARK: Pieces

/// Items of any kind, as cards that open the right way: a song plays with
/// the ones around it, a book from where it was left, a film in the player,
/// a show its episodes, a photo in the viewer.
struct ItemGrid: View {
    let items: [Item]
    var onShow: (Item) -> Void = { _ in }

    var body: some View {
        // Films and shows are posters; the rest square.
        let posters = !items.isEmpty && items.allSatisfy { $0.kind == "video" || $0.kind == "tv" }
        LazyVGrid(columns: Array(repeating: GridItem(.fixed(posters ? 240 : Theme.card), spacing: 40), count: posters ? 6 : 4), spacing: 70) {
            ForEach(Array(items.enumerated()), id: \.element.key) { i, item in
                ItemCard(item: item, list: items, index: i, poster: posters)
                    .onAppear { onShow(item) }
            }
        }
        .padding(.vertical, 40)
    }
}

struct ItemCard: View {
    let item: Item
    let list: [Item]
    let index: Int
    let poster: Bool
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model

    var body: some View {
        let size = poster ? CGSize(width: 240, height: 360) : CGSize(width: Theme.card, height: Theme.card)
        let art = Cover(url: api.artURL(source: item.sourceId, artId: item.artId, size: 500), kind: item.kind)
            .frame(width: size.width, height: size.height)
        Group {
            if item.kind == "tv" && !item.isVideo {
                NavigationLink(value: item) { art }
            } else {
                Button(action: open) { art }
            }
        }
        .buttonStyle(CardButton())
        .overlay(alignment: .bottom) {
            CardTitle(title: item.title, subtitle: item.kind == "picture" ? nil : item.cardLine).frame(width: size.width)
        }
        .padding(.bottom, CardTitle.room)
    }

    private func open() {
        switch item.kind {
        case "music":
            model.play(list.filter { $0.kind == "music" }, from: list.filter { $0.kind == "music" }.firstIndex(of: item) ?? 0)
        case "audiobook":
            Task { await model.playBook(item) }
        case "ebook", "document":
            model.read(item)
        case "picture" where item.isPhoto:
            let photos = list.filter(\.isPhoto)
            model.photos = PhotoViewing(photos: photos, index: photos.firstIndex(of: item) ?? 0)
        default:
            model.playVideo(item)
        }
    }
}

struct Nothing: View {
    var body: some View {
        Text("Nothing on this shelf yet.")
            .muted()
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, Theme.page)
            .padding(.vertical, 30)
    }
}

extension Item {
    /// What the TV can open - everything, now it has a reader.
    var playsHere: Bool { true }
}

extension Array where Element == API.Group {
    /// Genres remember which shelves they are of, for opening one.
    func scoped(_ kinds: [String]) -> [API.Group] { map { var g = $0; g.scope = kinds; return g } }
}

#if DEBUG
/// For the simulator, which has no remote: -openFirst YES opens the first
/// album or artist of the list showing.
struct DebugOpenFirst<T: Hashable, Page: View>: ViewModifier {
    let items: [T]
    @ViewBuilder let page: (T) -> Page
    @State private var opened: T?

    func body(content: Content) -> some View {
        content
            .navigationDestination(item: $opened) { page($0) }
            .onChange(of: items) { _, all in
                if opened == nil, UserDefaults.standard.bool(forKey: "openFirst") { opened = all.first }
            }
    }
}

/// For the simulator, which has no remote: -autovideo <film or episode id>.
struct DebugAutovideo: ViewModifier {
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model

    func body(content: Content) -> some View {
        content.task {
            guard let id = UserDefaults.standard.string(forKey: "autovideo"), model.video == nil else { return }
            if let film = try? await api.browse(kind: "video").first(where: { $0.id == id }) {
                model.playVideo(film)
            } else if let series = try? await api.browse(kind: "tv").first(where: { $0.episodeCode == nil }),
                      let show = try? await api.show(series),
                      let episode = show.episodes.first(where: { $0.id == id }) {
                model.playVideo(episode)
            }
        }
    }
}
#endif

/// Read Along: books somebody has as both an ebook and an audiobook. One
/// plays the audiobook and opens the book; synced, the page follows it.
struct PairsPage: View {
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model
    @State private var pairs: [API.Pair] = []
    @State private var loaded = false

    var body: some View {
        ScrollView {
            if loaded && pairs.isEmpty { Nothing() }
            LazyVGrid(columns: Theme.grid, spacing: 70) {
                ForEach(pairs) { pair in
                    Button { Task { await model.readAlong(pair) } } label: {
                        Cover(url: api.artURL(source: pair.ebook.sourceId, artId: pair.ebook.artId ?? pair.audiobook.artId, size: 500))
                            .frame(width: Theme.card, height: Theme.card)
                    }
                    .buttonStyle(CardButton())
                    .overlay(alignment: .bottom) {
                        CardTitle(title: pair.ebook.title, subtitle: syncLine(pair)).frame(width: 270)
                    }
                    .padding(.bottom, CardTitle.room)
                }
            }
            .padding(.vertical, 40)
        }
        .task {
            pairs = (try? await api.pairs()) ?? []
            loaded = true
            #if DEBUG
            // For the simulator, which has no remote: -autopair YES opens the first.
            if UserDefaults.standard.bool(forKey: "autopair"), model.reading == nil, let first = pairs.first {
                await model.readAlong(first)
            }
            #endif
        }
    }

    /// Whether the page will follow: synced, syncing, or not yet (set going
    /// from the page's Read Along, where the sync is started).
    /// The server's 0...1 as a whole percent, kept finite and in range before
    /// it becomes an Int (Int() of anything huge traps).
    private func percent(_ p: Double?) -> Int {
        guard let p, p.isFinite else { return 0 }
        return Int((max(0, min(1, p)) * 100).rounded())
    }

    private func syncLine(_ pair: API.Pair) -> String {
        switch pair.sync?.state {
        case "ready": "Follows the audiobook"
        case "queued", "working": "Syncing \(percent(pair.sync?.progress))%"
        case "failed": "Sync failed"
        default: "Not synced"
        }
    }
}
