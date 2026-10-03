import SwiftUI

/// The side bar, as the web page's TV mode has it: the playing song first
/// (in place of a mini player, which a remote could not reach), then the
/// library. Films, photos and books come in later steps.
struct LibraryView: View {
    @Environment(AppModel.self) private var model
    @Environment(API.self) private var api
    @Environment(Player.self) private var player
    @State private var tab = Self.firstTab
    @Namespace private var pageFocus
    @State private var frameReady = false

    private static var firstTab: String {
        #if DEBUG
        if UserDefaults.standard.string(forKey: "autovideo") != nil { return "watch" }
        if UserDefaults.standard.bool(forKey: "autophoto") { return "photos" }
        if UserDefaults.standard.bool(forKey: "autobook") { return "books" }
        if let tab = UserDefaults.standard.string(forKey: "tab") { return tab }
        #endif
        return "home"
    }

    /// The page the side bar has chosen.
    @ViewBuilder private var page: some View {
        switch tab {
        case "music": MusicTab()
        case "watch": WatchTab()
        case "books": BooksTab()
        case "photos": PhotosTab()
        case "search": NavigationStack { SearchView().libraryDestinations() }
        case "settings": SettingsView()
        default: HomeTab()
        }
    }

    var body: some View {
        @Bindable var model = model
        HStack(spacing: 0) {
            // Not reachable for the first moment, so the remote starts on the
            // page, never on the side bar or the search box (the page's rule).
            SideBar(tab: $tab)
                .disabled(!frameReady)
            VStack(spacing: 0) {
                Header(tab: $tab)
                    .disabled(!frameReady)
                page
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                    .focusSection()
            }
        }
        .background(Theme.bg)
        // The page's own margins, not tvOS's: the Google TV look.
        .ignoresSafeArea()
        .focusScope(pageFocus)
        .environment(\.pageFocus, pageFocus)
        .task {
            try? await Task.sleep(for: .milliseconds(800))
            frameReady = true
        }
        .task { await model.loadFavorites() }
        .task { model.pills = await api.pills() }
        .task { model.shelfFiles = try? await api.shelfFiles() }
        .onChange(of: model.requestedTab) { _, t in
            if let t { tab = t; model.requestedTab = nil }
        }
        .task { await model.loadLook() }
        #if DEBUG
        .modifier(DebugAutostation())
        .modifier(DebugAutoread())
        #endif
        .fullScreenCover(isPresented: $model.showingNowPlaying) { NowPlayingView() }
        .fullScreenCover(item: $model.video) { VideoView(session: $0) }
        .fullScreenCover(item: $model.photos) { PhotoViewer(viewing: $0) }
        .fullScreenCover(item: $model.reading) { reading in
            switch reading {
            case .epub(let r): ReaderView(reader: r)
            case .pdf(let r): PdfView(reader: r)
            }
        }
        // The remote's play/pause works everywhere, not only in Now Playing.
        .onPlayPauseCommand { player.togglePlay() }
    }
}

// MARK: Home

/// The web page's Home, TV-sized: what to carry on watching, favorites, what
/// was played lately, the mixes, and albums.
struct HomeView: View {
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model
    @State private var carryOn: [Item] = []
    @State private var favorites: [Item] = []
    @State private var recent: [Item] = []
    @State private var home: API.Home?
    @State private var loaded = false
    @State private var failed: String?

    static let shelfTitles = ["video": "New films", "tv": "New TV", "audiobook": "New audiobooks",
                              "ebook": "New books", "document": "New documents", "picture": "New photos"]
    static let shelfOpens = ["video": ("watch", "video"), "tv": ("watch", "tv"), "audiobook": ("books", "audiobook"),
                             "ebook": ("books", "ebook"), "document": ("books", "document"), "picture": ("photos", "picture")]
    static let favoriteRows: [(title: String, kinds: [String], tab: String, category: String)] = [
        ("Favorite songs", ["music"], "music", "favorites"),
        ("Favorite films and TV", ["video", "tv"], "watch", "fav-watch"),
        ("Favorite books", ["audiobook", "ebook", "document"], "books", "fav-books"),
        ("Favorite photos", ["picture"], "photos", "fav-photos"),
    ]

    /// The page's Home on a TV: one-press music across the top, then what
    /// you were part way through and just playing, what is new on every
    /// shelf, then your favorites - each row with See all.
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 44) {
                if model.has("music") {
                    QuickPlay(hasFavorites: favorites.contains { $0.kind == "music" })
                }
                if let failed { Text(failed).foregroundStyle(Theme.muted).padding(.horizontal, Theme.page) }
                if !carryOn.isEmpty {
                    Row(title: "Continue") {
                        ForEach(Array(carryOn.enumerated()), id: \.element.key) { i, item in
                            ItemCard(item: item, list: carryOn, index: i, poster: false)
                        }
                    }
                }
                if !recent.isEmpty {
                    Row(title: "Recently played", seeAll: { model.open(tab: "music", category: "mixes") }) {
                        ForEach(Array(recent.prefix(12).enumerated()), id: \.element.key) { i, song in
                            SongCard(song: song, list: recent, index: i)
                        }
                    }
                }
                if let albums = home?.albums, !albums.isEmpty {
                    Row(title: "New music", seeAll: { model.open(tab: "music", category: "albums") }) {
                        ForEach(albums) { AlbumCard(album: $0) }
                    }
                }
                ForEach(home?.shelves ?? [], id: \.kind) { shelf in
                    if !shelf.items.isEmpty {
                        let opens = Self.shelfOpens[shelf.kind]
                        Row(title: Self.shelfTitles[shelf.kind] ?? "New",
                            seeAll: opens.map { o in { model.open(tab: o.0, category: o.1) } }) {
                            ForEach(Array(shelf.items.enumerated()), id: \.element.key) { i, item in
                                ItemCard(item: item, list: shelf.items, index: i, poster: false)
                            }
                        }
                    }
                }
                ForEach(Self.favoriteRows, id: \.title) { row in
                    let list = Array(favorites.filter { row.kinds.contains($0.kind) }.prefix(12))
                    if !list.isEmpty {
                        Row(title: row.title, seeAll: { model.open(tab: row.tab, category: row.category) }) {
                            ForEach(Array(list.enumerated()), id: \.element.key) { i, item in
                                ItemCard(item: item, list: list, index: i, poster: false)
                            }
                        }
                    }
                }
                if loaded && failed == nil && carryOn.isEmpty && recent.isEmpty && favorites.isEmpty
                    && (home?.albums.isEmpty ?? true) && (home?.shelves.allSatisfy { $0.items.isEmpty } ?? true) {
                    Text("Nothing new lately.")
                        .font(.system(size: 30))
                        .foregroundStyle(Theme.muted)
                        .padding(.horizontal, Theme.page)
                }
            }
            .padding(.top, 8)
            .padding(.bottom, 60)
        }
        .scrollClipDisabled()
        .task {
            do {
                async let homeAnswer = api.home()
                async let played = api.recentlyPlayed()
                async let favs = api.favorites()
                async let going = api.continueWatching()
                home = try await homeAnswer
                recent = (try? await played) ?? []
                favorites = (try? await favs) ?? []
                carryOn = ((try? await going) ?? []).filter { $0.isVideo || $0.kind == "audiobook" }
                #if DEBUG
                // For the simulator, which has no remote to press play with:
                // xcrun simctl launch booted dev.soundstorm.app -autoplay YES
                if UserDefaults.standard.bool(forKey: "autoplay"), !recent.isEmpty, model.player?.current == nil {
                    model.play(recent)
                }
                #endif
            } catch {
                failed = error.localizedDescription
            }
            loaded = true
        }
    }
}

/// Home's big buttons, the page's homeQuickPlay: music with one press -
/// Shuffle all music (lit, and where the remote starts), Favorite songs when
/// there are any, Music radio, New music.
struct QuickPlay: View {
    let hasFavorites: Bool
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model

    var body: some View {
        HStack(spacing: 20) {
            button("Shuffle all music", icon: "shuffle", primary: true) {
                await model.tune(API.Station(mode: "shuffle", seed: nil, title: "Shuffle all music",
                                             subtitle: nil, covers: nil, sourceId: ""))
            }
            .pageStartsHere()
            if hasFavorites {
                button("Favorite songs", icon: "heart", primary: false) {
                    if let songs = try? await api.mix("favorites"), !songs.isEmpty { model.play(songs) }
                }
            }
            button("Music radio", icon: "radio", primary: false) {
                await model.tune(API.Station(mode: "library", seed: nil, title: "Library radio",
                                             subtitle: nil, covers: nil, sourceId: ""))
            }
            button("New music", icon: "sparkle", primary: false) {
                if let songs = try? await api.mix("recently-added"), !songs.isEmpty { model.play(songs) }
            }
        }
        .padding(.horizontal, Theme.page)
        .padding(.top, 20)
        .focusSection()
    }

    private func button(_ label: String, icon: String, primary: Bool, run: @escaping () async -> Void) -> some View {
        Button { Task { await run() } } label: {
            HStack(spacing: 24) {
                Image("Icons/" + icon)
                    .resizable()
                    .frame(width: 44, height: 44)
                Text(label)
                    .font(.system(size: 32, weight: .semibold))
                    .lineLimit(1)
            }
            .foregroundStyle(.white)
            .padding(.horizontal, 36)
            .frame(width: 404, height: 102, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 28).fill(primary ? Theme.accent : Theme.quick))
            .ring(radius: 36, width: 8, inset: -8)
        }
        .buttonStyle(FlatButton())
    }
}

// MARK: Music

struct ArtistView: View {
    let artist: API.Artist
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model
    @State private var albums: [Album] = []

    /// The page's artist page: their picture round, "N albums", Play, Shuffle
    /// and Artist radio, then their albums.
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                BackLink(label: "Artists")
                DetailHead(art: api.artURL(source: artist.sourceId, artId: artist.artId, size: 800), round: true,
                           kind: "Artist", title: artist.name, subtitle: nil,
                           facts: "\(albums.count) album\(albums.count == 1 ? "" : "s")") {
                    Button("\u{25B6}  Play") { Task { await playAll(shuffle: false) } }
                        .buttonStyle(RoundButton(primary: true))
                        .pageStartsHere()
                    Button("Shuffle") { Task { await playAll(shuffle: true) } }
                        .buttonStyle(RoundButton())
                    Button("Artist radio") {
                        Task { await model.tune(API.Station(mode: "artist", seed: artist.name, title: artist.name,
                                                            subtitle: nil, covers: nil, sourceId: artist.sourceId)) }
                    }
                    .buttonStyle(RoundButton())
                }
                .padding(.top, 24)
                .padding(.bottom, 44)
                Text("Albums")
                    .font(.system(size: 34, weight: .bold))
                    .foregroundStyle(Theme.text)
                LazyVGrid(columns: Theme.grid, alignment: .leading, spacing: 70) {
                    ForEach(albums) { AlbumCard(album: $0) }
                }
                .padding(.top, 28)
                .focusSection()
            }
            .padding(.horizontal, Theme.page)
            .padding(.top, 12)
            .padding(.bottom, 60)
        }
        .scrollClipDisabled()
        .toolbar(.hidden, for: .navigationBar)
        .task { albums = (try? await api.albums(of: artist)) ?? [] }
    }

    /// Every song of theirs, album by album, as the page's Play does.
    private func playAll(shuffle: Bool) async {
        var songs: [Item] = []
        for album in albums { songs += (try? await api.album(album)) ?? [] }
        guard !songs.isEmpty else { return }
        model.play(shuffle ? songs.shuffled() : songs)
    }
}

struct AlbumView: View {
    let album: Album
    @Environment(API.self) private var api
    @State private var songs: [Item] = []

    var body: some View {
        SongList(back: "Albums", kind: "Album", title: album.title, subtitle: album.artist,
                 art: api.artURL(source: album.sourceId, artId: album.artId, size: 800), songs: songs,
                 year: album.year,
                 radio: songs.first.map { API.Station(mode: "album", seed: $0.id, title: album.title,
                                                       subtitle: nil, covers: nil, sourceId: $0.sourceId) })
            .task { songs = (try? await api.album(album)) ?? [] }
    }
}

struct PlaylistView: View {
    let playlist: Playlist
    @Environment(API.self) private var api
    @State private var songs: [Item] = []

    var body: some View {
        let cover = playlist.covers?.first
        SongList(back: "Playlists", kind: "Playlist", title: playlist.name, subtitle: "",
                 art: cover.flatMap { api.artURL(source: $0.sourceId, artId: $0.artId, size: 800) }, songs: songs)
            .task { songs = (try? await api.playlist(playlist)) ?? [] }
    }
}


// MARK: Search

struct SearchView: View {
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model
    @State private var query = ""
    @State private var songs: [Item] = []

    var body: some View {
        List {
            ForEach(Array(songs.enumerated()), id: \.element.key) { i, song in
                Button { model.play(songs, from: i) } label: {
                    HStack(spacing: 30) {
                        Cover(url: api.artURL(source: song.sourceId, artId: song.artId, size: 200))
                            .frame(width: 100, height: 100)
                        VStack(alignment: .leading) {
                            Text(song.title)
                            Text(song.artist).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
        .searchable(text: $query, prompt: "Songs, albums, artists")
        .task(id: query) {
            let q = query.trimmingCharacters(in: .whitespaces)
            guard !q.isEmpty else { songs = []; return }
            try? await Task.sleep(for: .milliseconds(300)) // typing on a TV is slow; wait for a pause
            guard !Task.isCancelled else { return }
            songs = (try? await api.search(q)) ?? []
        }
    }
}

// MARK: Settings

struct SettingsView: View {
    @Environment(AppModel.self) private var model
    @Environment(API.self) private var api

    /// The page's Settings, as much of it as a TV has: the heading, who is
    /// signed in, and a card for this TV.
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text("Settings")
                    .font(.system(size: 52, weight: .bold))
                    .foregroundStyle(Theme.text)
                if let user = api.user {
                    Text("Signed in as \(user.name)\(user.owner == true ? ", the owner of this server" : "").")
                        .muted()
                }
                VStack(alignment: .leading, spacing: 24) {
                    Text("On this TV")
                        .font(.system(size: 36, weight: .bold))
                        .foregroundStyle(Theme.text)
                    Text(ServerAddress.all.first(where: { $0.url == api.server })?.name ?? (api.server.host() ?? ""))
                        .muted()
                    HStack(spacing: 20) {
                        Button("Switch person") { Task { await model.showProfiles() } }
                            .buttonStyle(WebButton(primary: true, wide: false))
                            .pageStartsHere()
                        Button("Change server") { model.changeServer() }
                            .buttonStyle(WebButton(wide: false))
                        // Signing out takes this person off the TV; Switch
                        // person keeps everybody on it.
                        Button("Sign out") { Task { await model.signOut() } }
                            .buttonStyle(WebButton(wide: false))
                    }
                    Text("Switch person keeps everybody on this TV. Sign out takes you off it.")
                        .font(.system(size: 26))
                        .foregroundStyle(Theme.muted)
                }
                .padding(50)
                .frame(maxWidth: 1440, alignment: .leading)
                .background(RoundedRectangle(cornerRadius: 20).fill(Theme.surface))
                .overlay(RoundedRectangle(cornerRadius: 20).strokeBorder(Theme.border, lineWidth: 2))
                .padding(.top, 24)
                .focusSection()
            }
            .padding(.horizontal, 156)
            .padding(.top, 30)
            .padding(.bottom, 60)
        }
        .scrollClipDisabled()
    }
}

// MARK: Pieces

struct Row<Content: View>: View {
    let title: String
    var seeAll: (() -> Void)? = nil
    @ViewBuilder let content: Content

    init(title: String, seeAll: (() -> Void)? = nil, @ViewBuilder content: () -> Content) {
        self.title = title
        self.seeAll = seeAll
        self.content = content()
    }

    /// The page's Home row: its name in bold on the left, See all in the
    /// accent on the right, and the covers in a strip under them.
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                Text(title)
                    .font(.system(size: 38, weight: .bold))
                    .foregroundStyle(Theme.text)
                Spacer()
                if let seeAll {
                    Button(action: seeAll) {
                        Text("See all")
                            .font(.system(size: 30, weight: .semibold))
                            .foregroundStyle(Theme.accent)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .ring(radius: 12, width: 4)
                    }
                    .buttonStyle(FlatButton())
                }
            }
            .padding(.horizontal, Theme.page)
            ScrollView(.horizontal) {
                LazyHStack(alignment: .top, spacing: 32) { content }
                    .padding(.horizontal, Theme.page)
                    .padding(.vertical, 28) // room for the outline and the focused card's growth
            }
            .scrollClipDisabled()
        }
    }
}

struct Cover: View {
    let url: URL?
    /// Now Playing's covers keep the look they had (the owner: keep the
    /// animations as they are); everywhere else a card is the page's.
    var plain = false

    var body: some View {
        let radius: CGFloat = plain ? 12 : Theme.cardRadius
        SafeImage(url: url) { image in
            image.resizable().scaledToFill()
        } placeholder: {
            Rectangle().fill(plain ? Color.white.opacity(0.06) : Theme.surface2)
                .overlay(Image(systemName: "music.note").font(plain ? .largeTitle : .system(size: 64))
                    .foregroundStyle(plain ? Color.secondary : Theme.muted))
        }
        // Filled to whatever frame it is given and cut there: a picture not of
        // that shape (a poster, a wide still) spilled past it.
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
        .clipped()
        .clipShape(RoundedRectangle(cornerRadius: radius))
        .overlay {
            if !plain {
                RoundedRectangle(cornerRadius: radius).strokeBorder(Theme.border, lineWidth: 2)
            }
        }
    }
}

struct AlbumCard: View {
    let album: Album
    @Environment(API.self) private var api

    var body: some View {
        NavigationLink(value: album) {
            Cover(url: api.artURL(source: album.sourceId, artId: album.artId))
                .frame(width: Theme.card, height: Theme.card)
        }
        .buttonStyle(CardButton())
        .overlay(alignment: .bottom) { CardTitle(title: album.title, subtitle: album.artist) }
        .padding(.bottom, CardTitle.room)
    }
}

/// Up to four covers, as the page's collages are.
struct Collage: View {
    let source: String
    let covers: [String]
    @Environment(API.self) private var api

    var body: some View {
        let urls = covers.prefix(4).compactMap { api.artURL(source: source, artId: $0, size: 300) }
        Group {
            if urls.count < 4 {
                Cover(url: urls.first)
            } else {
                Grid(horizontalSpacing: 0, verticalSpacing: 0) {
                    GridRow { cell(urls[0]); cell(urls[1]) }
                    GridRow { cell(urls[2]); cell(urls[3]) }
                }
                .clipShape(RoundedRectangle(cornerRadius: Theme.cardRadius))
            }
        }
    }

    private func cell(_ url: URL) -> some View {
        SafeImage(url: url, maxPixels: 300) { $0.resizable().scaledToFill() } placeholder: { Theme.surface2 }
            .frame(width: Theme.card / 2, height: Theme.card / 2).clipped()
    }
}

struct MixCard: View {
    let mix: API.Mix
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model

    var body: some View {
        Button {
            Task { if let songs = try? await api.mix(mix.id), !songs.isEmpty { model.play(songs) } }
        } label: {
            Collage(source: mix.sourceId, covers: mix.covers ?? []).frame(width: Theme.card, height: Theme.card)
        }
        .buttonStyle(CardButton())
        .overlay(alignment: .bottom) { CardTitle(title: mix.title, subtitle: mix.subtitle) }
        .padding(.bottom, CardTitle.room)
    }
}

struct StationCard: View {
    let station: API.Station
    @Environment(AppModel.self) private var model

    var body: some View {
        Button { Task { await model.tune(station) } } label: {
            Collage(source: station.sourceId, covers: station.covers ?? []).frame(width: Theme.card, height: Theme.card)
        }
        .buttonStyle(CardButton())
        .overlay(alignment: .bottom) { CardTitle(title: station.title, subtitle: station.subtitle) }
        .padding(.bottom, CardTitle.room)
    }
}

struct ArtistCard: View {
    let artist: API.Artist
    @Environment(API.self) private var api

    var body: some View {
        NavigationLink(value: artist) {
            Cover(url: api.artURL(source: artist.sourceId, artId: artist.artId))
                .frame(width: Theme.card, height: Theme.card)
                .clipShape(Circle())
        }
        .buttonStyle(CardButton(radius: Theme.card / 2))
        .overlay(alignment: .bottom) { CardTitle(title: artist.name, subtitle: nil) }
        .padding(.bottom, CardTitle.room)
    }
}

struct CardTitle: View {
    let title: String
    let subtitle: String?
    var width: CGFloat = Theme.card
    /// The room a card keeps under its cover for these two lines.
    static let room: CGFloat = 84

    /// Under a cover, as the page has it: the name, then who in grey, both
    /// from the cover's left edge.
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(.system(size: 28, weight: .semibold))
                .foregroundStyle(Theme.text)
                .lineLimit(1)
            Text(subtitle ?? " ")
                .font(.system(size: 24, weight: .medium))
                .foregroundStyle(Theme.muted)
                .lineLimit(1)
        }
        .frame(width: width, alignment: .leading)
        .offset(y: CardTitle.room)
    }
}

struct PlaylistCard: View {
    let playlist: Playlist
    @Environment(API.self) private var api

    var body: some View {
        let cover = playlist.covers?.first
        NavigationLink(value: playlist) {
            Cover(url: cover.flatMap { api.artURL(source: $0.sourceId, artId: $0.artId) })
                .frame(width: Theme.card, height: Theme.card)
        }
        .buttonStyle(CardButton())
        .overlay(alignment: .bottom) { CardTitle(title: playlist.name, subtitle: "\(playlist.count) songs") }
        .padding(.bottom, CardTitle.room)
    }
}

struct SongCard: View {
    let song: Item
    let list: [Item]
    let index: Int
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model

    var body: some View {
        Button { model.play(list, from: index) } label: {
            Cover(url: api.artURL(source: song.sourceId, artId: song.artId))
                .frame(width: Theme.card, height: Theme.card)
        }
        .buttonStyle(CardButton())
        .overlay(alignment: .bottom) { CardTitle(title: song.title, subtitle: song.artist) }
        .padding(.bottom, CardTitle.room)
    }
}

func clock(_ seconds: Double) -> String {
    // Under a billion seconds: Int() of anything past Int's range traps.
    guard seconds.isFinite, seconds >= 0, seconds < 1e9 else { return "0:00" }
    let s = Int(seconds)
    return s >= 3600 ? String(format: "%d:%02d:%02d", s / 3600, s / 60 % 60, s % 60)
        : String(format: "%d:%02d", s / 60, s % 60)
}

// MARK: Audiobooks

/// Every audiobook, as covers; one plays from where this person got to.
struct BooksView: View {
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model
    @State private var books: [Item] = []
    @State private var loaded = false

    var body: some View {
        ScrollView {
            if loaded && books.isEmpty {
                Nothing()
            }
            LazyVGrid(columns: Theme.grid, spacing: 70) {
                ForEach(books, id: \.key) { book in
                    Button { Task { await model.playBook(book) } } label: {
                        Cover(url: api.artURL(source: book.sourceId, artId: book.artId))
                            .frame(width: Theme.card, height: Theme.card)
                    }
                    .buttonStyle(CardButton())
                    .overlay(alignment: .bottom) { CardTitle(title: book.title, subtitle: book.artist) }
                    .padding(.bottom, CardTitle.room)
                }
            }
            .padding(.vertical, 40)
        }
        .task {
            books = (try? await api.browse(kind: "audiobook")) ?? []
            loaded = true
            #if DEBUG
            // For the simulator, which has no remote: -autobook YES plays the first book.
            if UserDefaults.standard.bool(forKey: "autobook"), let first = books.first, model.player?.current == nil {
                await model.playBook(first)
            }
            #endif
        }
    }
}
