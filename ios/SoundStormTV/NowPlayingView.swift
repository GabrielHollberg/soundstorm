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

    /// The Lyrics look, instead of the cover; kept between songs and launches.
    @AppStorage("lyricsLook") private var lyricsLook = false
    @State private var lyrics: API.Lyrics?
    private enum Sheet: Identifiable { case queue, playlists; var id: Self { self } }
    @State private var sheet: Sheet?

    private enum Spot { case none, timeline, putAway }
    @State private var spot: Spot = .none
    @State private var faded = false
    @State private var idle: Task<Void, Never>?
    @FocusState private var focused: Bool

    var body: some View {
        let song = player.current
        let art = song.flatMap { api.artURL(source: $0.sourceId, artId: $0.artId, size: 1200) }
        ZStack {
            // The cover, blurred and darkened, fills the screen behind.
            Cover(url: art)
                .scaleEffect(1.3)
                .blur(radius: 80)
                .overlay(Color.black.opacity(0.55))
                .ignoresSafeArea()

            VStack(spacing: 0) {
                Image(systemName: "chevron.down")
                    .font(.title2.weight(.semibold))
                    .padding(18)
                    .background(Circle().fill(.white.opacity(spot == .putAway ? 0.3 : 0)))
                    .overlay(Circle().stroke(.white, lineWidth: spot == .putAway ? 4 : 0))
                    .opacity(faded ? 0 : 1)

                Spacer(minLength: 30)
                Group {
                    if lyricsLook, let lyrics, !lyrics.lines.isEmpty {
                        LyricsView(lyrics: lyrics, time: player.time)
                            .frame(width: 1400, height: 560)
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
                VStack(spacing: 10) {
                    Text(song?.title ?? "").font(.title2.weight(.semibold)).lineLimit(1)
                    Text(song?.artist ?? "").foregroundStyle(.secondary).lineLimit(1)
                }
                .padding(.top, 40)
                .frame(maxWidth: 1400)
                Spacer(minLength: 30)

                Timeline(time: player.time, duration: player.duration, lit: spot == .timeline)
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
            if let song { lyrics = await api.lyrics(song) }
        }
        .sheet(item: $sheet) { which in
            switch which {
            case .queue: QueueSheet()
            case .playlists: if let song { PlaylistPicker(song: song) }
            }
        }
    }

    @ViewBuilder
    private func menu(for song: Item?) -> some View {
        if let song {
            let fav = model.favorites.contains(song.key)
            Button { Task { await model.toggleFavorite(song) } } label: {
                Label(fav ? "Remove from favorites" : "Add to favorites", systemImage: fav ? "heart.slash" : "heart")
            }
            Button { sheet = .playlists } label: { Label("Add to playlist", systemImage: "plus") }
            Button { lyricsLook.toggle() } label: {
                Label(lyricsLook ? "Show the cover" : "Show lyrics", systemImage: lyricsLook ? "photo" : "quote.bubble")
            }
            .disabled(lyrics?.lines.isEmpty ?? true && !lyricsLook)
            Button { sheet = .queue } label: { Label("Up next", systemImage: "list.bullet") }
        }
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
