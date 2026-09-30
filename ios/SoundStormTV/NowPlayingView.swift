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
                Cover(url: art)
                    .frame(width: 560, height: 560)
                    .shadow(color: .black.opacity(0.6), radius: 40, y: 20)
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
