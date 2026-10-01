import SwiftUI

/// The side bar, as the web page's TV mode has it: the playing song first
/// (in place of a mini player, which a remote could not reach), then the
/// library. Films, photos and books come in later steps.
struct LibraryView: View {
    @Environment(AppModel.self) private var model
    @Environment(API.self) private var api
    @Environment(Player.self) private var player
    @State private var tab = Self.firstTab

    private static var firstTab: String {
        #if DEBUG
        if UserDefaults.standard.string(forKey: "autovideo") != nil { return "watch" }
        if UserDefaults.standard.bool(forKey: "autophoto") { return "photos" }
        if UserDefaults.standard.bool(forKey: "autobook") { return "books" }
        if let tab = UserDefaults.standard.string(forKey: "tab") { return tab }
        #endif
        return "home"
    }

    var body: some View {
        @Bindable var model = model
        TabView(selection: $tab) {
            if let song = player.current {
                Tab(value: "playing") {
                    // Choosing it opens Now Playing; the side bar keeps the
                    // page that was showing behind it.
                    Color.clear.onAppear {
                        model.showingNowPlaying = true
                        tab = "home"
                    }
                } label: {
                    Label(song.title, systemImage: player.isPlaying ? "waveform" : "pause.fill")
                }
            }
            Tab("Home", systemImage: "house", value: "home") { HomeTab() }
            Tab("Music", systemImage: "music.note", value: "music") { MusicTab() }
            Tab("Watch", systemImage: "film", value: "watch") { WatchTab() }
            Tab("Books", systemImage: "headphones", value: "books") { BooksTab() }
            Tab("Photos", systemImage: "photo.on.rectangle", value: "photos") { PhotosTab() }
            Tab("Search", systemImage: "magnifyingglass", value: "search") { SearchView() }
            Tab("Settings", systemImage: "gearshape", value: "settings") { SettingsView() }
        }
        .tabViewStyle(.sidebarAdaptable)
        .task { await model.loadFavorites() }
        .task { model.pills = await api.pills() }
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
    @State private var mixes: [API.Mix] = []
    @State private var albums: [Album] = []
    @State private var failed: String?

    var body: some View {
            ScrollView {
                VStack(alignment: .leading, spacing: 50) {
                    if let failed { Text(failed).foregroundStyle(.secondary) }
                    if !carryOn.isEmpty {
                        Row(title: "Continue") { ForEach(carryOn, id: \.key) { PosterCard(item: $0) } }
                    }
                    if !favorites.isEmpty {
                        Row(title: "Favorites") {
                            ForEach(Array(favorites.enumerated()), id: \.element.key) { i, song in
                                SongCard(song: song, list: favorites, index: i)
                            }
                        }
                    }
                    if !recent.isEmpty {
                        Row(title: "Recently played") {
                            ForEach(Array(recent.enumerated()), id: \.element.key) { i, song in
                                SongCard(song: song, list: recent, index: i)
                            }
                        }
                    }
                    if !mixes.isEmpty {
                        Row(title: "Mixes") { ForEach(mixes) { MixCard(mix: $0) } }
                    }
                    if !albums.isEmpty {
                        Row(title: "Albums") { ForEach(albums) { AlbumCard(album: $0) } }
                    }
                }
                .padding(.vertical, 40)
            }
        .task {
            do {
                async let home = api.home()
                async let played = api.recentlyPlayed()
                async let favs = api.favorites()
                async let mix = api.mixes()
                async let going = api.continueWatching()
                albums = try await home.albums
                recent = (try? await played) ?? []
                favorites = ((try? await favs) ?? []).filter { $0.kind == "music" }
                mixes = (try? await mix) ?? []
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
        }
    }
}

// MARK: Music

struct ArtistView: View {
    let artist: API.Artist
    @Environment(API.self) private var api
    @State private var albums: [Album] = []

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 40) {
                Text(artist.name).font(.title2).padding(.leading, 20)
                LazyVGrid(columns: Array(repeating: GridItem(.fixed(300), spacing: 50), count: 5), spacing: 60) {
                    ForEach(albums) { AlbumCard(album: $0) }
                }
            }
            .padding(60)
        }
        .task { albums = (try? await api.albums(of: artist)) ?? [] }
    }
}

struct AlbumView: View {
    let album: Album
    @Environment(API.self) private var api
    @State private var songs: [Item] = []

    var body: some View {
        SongList(title: album.title, subtitle: album.artist,
                 art: api.artURL(source: album.sourceId, artId: album.artId, size: 800), songs: songs)
            .task { songs = (try? await api.album(album)) ?? [] }
    }
}

struct PlaylistView: View {
    let playlist: Playlist
    @Environment(API.self) private var api
    @State private var songs: [Item] = []

    var body: some View {
        let cover = playlist.covers?.first
        SongList(title: playlist.name, subtitle: "\(playlist.count) songs",
                 art: cover.flatMap { api.artURL(source: $0.sourceId, artId: $0.artId, size: 800) }, songs: songs)
            .task { songs = (try? await api.playlist(playlist)) ?? [] }
    }
}

/// An album's or a playlist's songs beside its cover: Play, Shuffle, and
/// any song to start from it.
struct SongList: View {
    let title: String
    let subtitle: String
    let art: URL?
    let songs: [Item]
    @Environment(AppModel.self) private var model

    var body: some View {
        HStack(alignment: .top, spacing: 80) {
            VStack(alignment: .leading, spacing: 24) {
                Cover(url: art).frame(width: 500, height: 500)
                Text(title).font(.title2).lineLimit(2)
                Text(subtitle).foregroundStyle(.secondary)
                HStack {
                    Button { model.play(songs) } label: { Label("Play", systemImage: "play.fill") }
                    Button { model.play(songs.shuffled()) } label: { Label("Shuffle", systemImage: "shuffle") }
                }
                .disabled(songs.isEmpty)
            }
            .frame(width: 500)
            List {
                ForEach(Array(songs.enumerated()), id: \.element.key) { i, song in
                    Button { model.play(songs, from: i) } label: {
                        HStack {
                            Text("\(i + 1)").foregroundStyle(.secondary).frame(width: 60, alignment: .leading)
                            VStack(alignment: .leading) {
                                Text(song.title)
                                if !song.artist.isEmpty {
                                    Text(song.artist).font(.caption).foregroundStyle(.secondary)
                                }
                            }
                            Spacer()
                            if let d = song.durationSeconds { Text(clock(d)).foregroundStyle(.secondary) }
                        }
                    }
                }
            }
        }
        .padding(60)
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

    var body: some View {
        VStack(spacing: 40) {
            Text(api.user.map { "Signed in as \($0.name)" } ?? "")
            Text(api.server.host() ?? "").foregroundStyle(.secondary)
            Button("Switch person") { Task { await model.showProfiles() } }
            Button("Change server") { model.changeServer() }
            // Signing out takes this person off the TV; Switch person keeps
            // everybody on it.
            Button("Sign out") { Task { await model.signOut() } }
        }
    }
}

// MARK: Pieces

struct Row<Content: View>: View {
    let title: String
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(title).font(.title3).padding(.leading, 20)
            ScrollView(.horizontal) {
                LazyHStack(spacing: 50) { content }
                    .padding(.horizontal, 20)
                    .padding(.vertical, 30) // room for the focused card to grow
            }
            .scrollClipDisabled()
        }
    }
}

struct Cover: View {
    let url: URL?

    var body: some View {
        SafeImage(url: url) { image in
            image.resizable().scaledToFill()
        } placeholder: {
            Rectangle().fill(.white.opacity(0.06))
                .overlay(Image(systemName: "music.note").font(.largeTitle).foregroundStyle(.secondary))
        }
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}

struct AlbumCard: View {
    let album: Album
    @Environment(API.self) private var api

    var body: some View {
        NavigationLink(value: album) {
            Cover(url: api.artURL(source: album.sourceId, artId: album.artId))
                .frame(width: 300, height: 300)
        }
        .buttonStyle(.borderless)
        .overlay(alignment: .bottom) {
            VStack(spacing: 4) {
                Text(album.title).lineLimit(1)
                Text(album.artist).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            .frame(width: 300)
            .offset(y: 70)
        }
        .padding(.bottom, 70)
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
                .clipShape(RoundedRectangle(cornerRadius: 12))
            }
        }
    }

    private func cell(_ url: URL) -> some View {
        SafeImage(url: url, maxPixels: 300) { $0.resizable().scaledToFill() } placeholder: { Color.white.opacity(0.06) }
            .frame(width: 150, height: 150).clipped()
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
            Collage(source: mix.sourceId, covers: mix.covers ?? []).frame(width: 300, height: 300)
        }
        .buttonStyle(.borderless)
        .overlay(alignment: .bottom) { CardTitle(title: mix.title, subtitle: mix.subtitle) }
        .padding(.bottom, 70)
    }
}

struct StationCard: View {
    let station: API.Station
    @Environment(AppModel.self) private var model

    var body: some View {
        Button { Task { await model.tune(station) } } label: {
            Collage(source: station.sourceId, covers: station.covers ?? []).frame(width: 300, height: 300)
        }
        .buttonStyle(.borderless)
        .overlay(alignment: .bottom) { CardTitle(title: station.title, subtitle: station.subtitle) }
        .padding(.bottom, 70)
    }
}

struct ArtistCard: View {
    let artist: API.Artist
    @Environment(API.self) private var api

    var body: some View {
        NavigationLink(value: artist) {
            Cover(url: api.artURL(source: artist.sourceId, artId: artist.artId))
                .frame(width: 260, height: 260)
                .clipShape(Circle())
        }
        .buttonStyle(.borderless)
        .overlay(alignment: .bottom) { CardTitle(title: artist.name, subtitle: nil) }
        .padding(.bottom, 70)
    }
}

struct CardTitle: View {
    let title: String
    let subtitle: String?

    var body: some View {
        VStack(spacing: 4) {
            Text(title).lineLimit(1)
            if let subtitle, !subtitle.isEmpty {
                Text(subtitle).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .frame(width: 300)
        .offset(y: 70)
    }
}

struct PlaylistCard: View {
    let playlist: Playlist
    @Environment(API.self) private var api

    var body: some View {
        let cover = playlist.covers?.first
        NavigationLink(value: playlist) {
            Cover(url: cover.flatMap { api.artURL(source: $0.sourceId, artId: $0.artId) })
                .frame(width: 300, height: 300)
        }
        .buttonStyle(.borderless)
        .overlay(alignment: .bottom) {
            Text(playlist.name).lineLimit(1).frame(width: 300).offset(y: 50)
        }
        .padding(.bottom, 50)
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
                .frame(width: 300, height: 300)
        }
        .buttonStyle(.borderless)
        .overlay(alignment: .bottom) {
            VStack(spacing: 4) {
                Text(song.title).lineLimit(1)
                Text(song.artist).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            .frame(width: 300)
            .offset(y: 70)
        }
        .padding(.bottom, 70)
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
                Text("No audiobooks yet.").foregroundStyle(.secondary).padding(40)
            }
            LazyVGrid(columns: Array(repeating: GridItem(.fixed(300), spacing: 50), count: 5), spacing: 60) {
                ForEach(books, id: \.key) { book in
                    Button { Task { await model.playBook(book) } } label: {
                        Cover(url: api.artURL(source: book.sourceId, artId: book.artId))
                            .frame(width: 300, height: 300)
                    }
                    .buttonStyle(.borderless)
                    .overlay(alignment: .bottom) { CardTitle(title: book.title, subtitle: book.artist) }
                    .padding(.bottom, 70)
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
