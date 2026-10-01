import SwiftUI

/// Now Playing, built to the rules settled for the web page's TV mode (see
/// "TV apps" in CLAUDE.md) rather than worked out again:
///
/// - No play, previous or next buttons. With nothing highlighted, OK plays
///   and pauses and left and right change song.
/// - Down highlights the timeline, where left and right move ten seconds;
///   up highlights the arrow that puts Now Playing away (OK on it, or Back,
///   closes it).
/// - The timeline and the arrow fade after four seconds without the remote,
///   leaving the cover and the title. Faded, OK and left and right still act
///   and they stay faded; down or up brings them back.
///
/// The whole screen is one focusable view that takes the remote's presses
/// itself and draws its own highlight, so the focus engine never wanders
/// off to something the rules do not have.
struct NowPlayingView: View {
    @Environment(Player.self) private var player
    @Environment(API.self) private var api
    @Environment(\.dismiss) private var dismiss
    @Environment(AppModel.self) private var model

    @State private var lyrics: API.Lyrics?
    @State private var palette = VizPalette()
    private enum Sheet: Identifiable { case queue, playlists, chapters; var id: Self { self } }
    @State private var sheet: Sheet?

    private enum Spot { case none, timeline, putAway }
    @State private var spot: Spot = .none
    @State private var faded = false
    @State private var idle: Task<Void, Never>?
    @FocusState private var focused: Bool

    var body: some View {
        let song = player.current
        let art = song.flatMap { api.artURL(source: $0.sourceId, artId: $0.artId, size: 1200) }
        // A book has the plain cover, as on the page.
        let look = player.isBook ? "square" : model.look
        let viz = Looks.isVisualizer(look)
        ZStack {
            if viz, let listener = model.listener {
                // A visualizer fills the screen; the title sits above it.
                VisualizerView(look: look, palette: palette, player: player, listener: listener)
                    .ignoresSafeArea()
            } else {
                // The cover, blurred and darkened, fills the screen behind.
                Cover(url: art)
                    .scaleEffect(1.3)
                    .blur(radius: 80)
                    .overlay(Color.black.opacity(0.55))
                    .ignoresSafeArea()
            }

            VStack(spacing: 0) {
                Image(systemName: "chevron.down")
                    .font(.title2.weight(.semibold))
                    .padding(18)
                    .background(Circle().fill(.white.opacity(spot == .putAway ? 0.3 : 0)))
                    .overlay(Circle().stroke(.white, lineWidth: spot == .putAway ? 4 : 0))
                    .opacity(faded ? 0 : 1)

                if viz { titleBlock(song) }
                Spacer(minLength: 30)
                Group {
                    if viz {
                        Color.clear.frame(height: 560)
                    } else if look == "lyrics", let lyrics, !lyrics.lines.isEmpty {
                        LyricsView(lyrics: lyrics, time: player.time)
                            .frame(width: 1400, height: 560)
                    } else if look == "spin" || look == "vinyl" {
                        TurningCover(url: art, record: look == "vinyl", player: player)
                            .frame(width: 560, height: 560)
                            .shadow(color: .black.opacity(0.6), radius: 40, y: 20)
                    } else {
                        Cover(url: art)
                            .frame(width: 560, height: 560)
                            .shadow(color: .black.opacity(0.6), radius: 40, y: 20)
                    }
                }
                .overlay(alignment: .center) {
                    if !player.isPlaying && song != nil {
                        Image(systemName: "pause.fill").font(.system(size: 90)).shadow(radius: 20)
                    }
                }
                if !viz { titleBlock(song) }
                Spacer(minLength: 30)


                Group {
                    if let c = player.chapter {
                        // A book's timeline is its chapter's, with the whole
                        // book beneath - as on the page.
                        VStack(spacing: 14) {
                            Timeline(time: player.time - c.start, duration: c.end - c.start, lit: spot == .timeline)
                            Text("\(clock(player.time)) of \(clock(player.duration))" + (player.speed != 1 ? " · \(speedName(player.speed))" : ""))
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    } else {
                        Timeline(time: player.time, duration: player.duration, lit: spot == .timeline)
                    }
                }
                .frame(width: 1100)
                .opacity(faded ? 0 : 1)
            }
            .padding(.vertical, 60)
        }
        .animation(.easeInOut(duration: 0.6), value: faded)
        .animation(.easeOut(duration: 0.15), value: spot)
        .focusable()
        .focused($focused)
        .focusEffectDisabled()
        .onAppear {
            focused = true
            wake()
        }
        .onDisappear { idle?.cancel() }
        .onMoveCommand(perform: move)
        .onTapGesture(perform: select)
        .onPlayPauseCommand { player.togglePlay() } // a media key acts, and wakes nothing
        .onExitCommand { dismiss() }
        // Holding OK opens the menu, as a held OK does on the page's TV mode.
        .contextMenu { menu(for: song) }
        .task(id: song?.key) {
            lyrics = nil
            guard let song, !player.isBook else { return }
            async let words = api.lyrics(song)
            if let url = api.artURL(source: song.sourceId, artId: song.artId, size: 100),
               let (data, _) = try? await SafeLoad.data(from: url, limit: SafeLoad.picture),
               let image = SafeLoad.image(data, maxPixels: 200) {
                palette = VizPalette(image: image)
            } else {
                palette = VizPalette()
            }
            lyrics = await words
        }
        .task(id: "\(song?.key ?? "")|\(model.look)") {
            // Heard only for a visualizer: hearing is a whole song's decoding.
            if let song, Looks.isVisualizer(model.look), !player.isBook { model.listener?.listen(to: song) }
        }
        .sheet(item: $sheet) { which in
            switch which {
            case .queue: QueueSheet()
            case .playlists: if let song { PlaylistPicker(song: song) }
            case .chapters: ChapterSheet()
            }
        }
    }

    /// The title, the artist or chapter, and the sleep timer.
    private func titleBlock(_ song: Item?) -> some View {
        VStack(spacing: 10) {
            Text(song?.title ?? "").font(.title2.weight(.semibold)).lineLimit(1)
            // A book names its chapter where a song names its artist.
            Text(player.chapter?.title ?? song?.artist ?? "").foregroundStyle(.secondary).lineLimit(1)
            if let sleepAt = player.sleepAt {
                Text("Stops at \(sleepAt.formatted(date: .omitted, time: .shortened))")
                    .font(.caption).foregroundStyle(.secondary)
            } else if player.sleepAtEnd {
                Text(player.isBook ? "Stops at the end of the chapter" : "Stops at the end of the song")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .shadow(color: .black.opacity(0.8), radius: 12)
        .padding(.top, 40)
        .frame(maxWidth: 1400)
    }

    @ViewBuilder
    private func menu(for song: Item?) -> some View {
        if player.isBook {
            if !player.chapters.isEmpty {
                Button { sheet = .chapters } label: { Label("Chapters", systemImage: "list.bullet") }
            }
            Menu {
                ForEach([0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3], id: \.self) { v in
                    Button { player.setSpeed(v) } label: {
                        if v == player.speed { Label(speedName(v), systemImage: "checkmark") } else { Text(speedName(v)) }
                    }
                }
            } label: { Label("Speed: \(speedName(player.speed))", systemImage: "gauge.with.dots.needle.50percent") }
            sleepMenu
        } else if let song {
            let fav = model.favorites.contains(song.key)
            Button { Task { await model.toggleFavorite(song) } } label: {
                Label(fav ? "Remove from favorites" : "Add to favorites", systemImage: fav ? "heart.slash" : "heart")
            }
            Button { sheet = .playlists } label: { Label("Add to playlist", systemImage: "plus") }
            Menu {
                Section("Lyrics and covers") { lookButtons(Looks.covers) }
                Section("Visualizers") { lookButtons(Looks.visualizers) }
            } label: { Label("Look", systemImage: "sparkles") }
            Button { sheet = .queue } label: { Label("Up next", systemImage: "list.bullet") }
            sleepMenu
        }
    }

    private func lookButtons(_ looks: [(key: String, label: String)]) -> some View {
        ForEach(looks, id: \.key) { l in
            Button { model.setLook(l.key) } label: {
                if model.look == l.key { Label(l.label, systemImage: "checkmark") } else { Text(l.label) }
            }
        }
    }

    /// The page's sleep timer: a time, or the end of what is playing.
    private var sleepMenu: some View {
        Menu {
            ForEach([15, 30, 45, 60, 90], id: \.self) { m in
                Button("\(m) minutes") { player.sleep(minutes: m) }
            }
            Button(player.isBook ? "End of chapter" : "End of song") { player.sleepAtEndOfThis() }
            if player.sleepAt != nil || player.sleepAtEnd {
                Button("Off", role: .destructive) { player.sleep(minutes: nil) }
            }
        } label: { Label("Sleep timer", systemImage: "moon.zzz") }
    }

    private func move(_ direction: MoveCommandDirection) {
        switch direction {
        case .left, .right:
            let forward = direction == .right
            if spot == .timeline && !faded {
                // The timeline showing: ten seconds.
                player.skip(by: forward ? 10 : -10)
                wake()
            } else if spot == .none || faded {
                // Nothing highlighted, or faded: the song. Faded stays faded.
                forward ? player.next() : player.previous()
                if !faded { wake() }
            } else {
                wake()
            }
        case .down:
            if faded || spot != .timeline { spot = .timeline }
            wake()
        case .up:
            if faded || spot != .putAway { spot = .putAway }
            wake()
        @unknown default:
            wake()
        }
    }

    private func select() {
        if spot == .putAway && !faded {
            dismiss()
            return
        }
        player.togglePlay()
        if !faded { wake() }
    }

    /// Shows the timeline and the arrow, and fades them again after four
    /// seconds without a press.
    private func wake() {
        faded = false
        idle?.cancel()
        idle = Task {
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled else { return }
            faded = true
            spot = .none
        }
    }
}

private struct Timeline: View {
    let time: Double
    let duration: Double
    let lit: Bool

    var body: some View {
        VStack(spacing: 12) {
            GeometryReader { g in
                let f = duration > 0 ? min(1, max(0, time / duration)) : 0
                ZStack(alignment: .leading) {
                    Capsule().fill(.white.opacity(0.25))
                    Capsule().fill(.white).frame(width: g.size.width * f)
                }
            }
            .frame(height: lit ? 14 : 8)
            .overlay(Capsule().stroke(.white, lineWidth: lit ? 3 : 0).padding(-8))
            HStack {
                Text(clock(time))
                Spacer()
                Text(clock(duration))
            }
            .font(.callout.monospacedDigit())
            .foregroundStyle(.secondary)
        }
    }
}

/// The words, the line being sung lit and the ones round it dimmer - the
/// page's Lyrics look. Unsynced lyrics are shown as they are.
private struct LyricsView: View {
    let lyrics: API.Lyrics
    let time: Double

    var body: some View {
        let lines = lyrics.lines
        let now = lyrics.synced ? (lines.lastIndex { $0.start >= 0 && Double($0.start) / 1000 <= time } ?? -1) : -1
        let first = max(0, min(now - 2, lines.count - 7))
        VStack(spacing: 22) {
            ForEach(first..<min(lines.count, first + 7), id: \.self) { i in
                Text(lines[i].text.isEmpty ? "♪" : lines[i].text)
                    .font(.system(size: i == now ? 52 : 40, weight: i == now ? .bold : .semibold))
                    .foregroundStyle(.white.opacity(!lyrics.synced || i == now ? 1 : 0.4))
                    .multilineTextAlignment(.center)
                    .lineLimit(2)
            }
        }
        .animation(.easeInOut(duration: 0.3), value: now)
    }
}

/// What plays after this: the queue, the playing song marked; OK on one
/// plays it.
private struct QueueSheet: View {
    @Environment(Player.self) private var player
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                ForEach(Array(player.queue.enumerated()), id: \.offset) { i, song in
                    Button {
                        player.jump(to: i)
                        dismiss()
                    } label: {
                        HStack {
                            Image(systemName: "speaker.wave.2.fill").opacity(i == player.index ? 1 : 0)
                            VStack(alignment: .leading) {
                                Text(song.title)
                                Text(song.artist).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            .navigationTitle("Up next")
        }
    }
}

private struct PlaylistPicker: View {
    let song: Item
    @Environment(API.self) private var api
    @Environment(\.dismiss) private var dismiss
    @State private var playlists: [Playlist] = []
    @State private var message: String?

    var body: some View {
        NavigationStack {
            List {
                if let message { Text(message).foregroundStyle(.secondary) }
                ForEach(playlists) { playlist in
                    Button(playlist.name) {
                        Task {
                            do {
                                try await api.addToPlaylist(playlist, song)
                                dismiss()
                            } catch {
                                message = error.localizedDescription
                            }
                        }
                    }
                }
            }
            .navigationTitle("Add to playlist")
            .task { playlists = (try? await api.playlists()) ?? [] }
        }
    }
}

func speedName(_ v: Double) -> String {
    v == 1 ? "1× normal" : String(format: "%g×", v)
}

/// A book's chapters, the one being heard marked; OK on one goes there.
private struct ChapterSheet: View {
    @Environment(Player.self) private var player
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollViewReader { scroller in
                List {
                    ForEach(Array(player.chapters.enumerated()), id: \.offset) { i, c in
                        Button {
                            player.jump(toChapter: i)
                            dismiss()
                        } label: {
                            HStack {
                                Image(systemName: "speaker.wave.2.fill").opacity(i == player.chapter?.index ? 1 : 0)
                                Text(c.title)
                                Spacer()
                                Text(clock(c.startSeconds)).foregroundStyle(.secondary)
                            }
                        }
                        .id(i)
                    }
                }
                .onAppear { scroller.scrollTo(player.chapter?.index ?? 0, anchor: .center) }
            }
            .navigationTitle("Chapters")
        }
    }
}
