import SwiftUI

/// An album's, a playlist's or an artist's page as the web page draws it on
/// a TV (`showAlbum`, `trackRow`): "← Albums" in the accent, the cover with
/// the kind, name and facts beside it, round Play and Shuffle buttons, then
/// the songs as numbered rows, each ringed when the remote is on it.

/// "← Albums": the page's back link. Back on the remote does the same.
struct BackLink: View {
    let label: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        Button { dismiss() } label: {
            Text("\u{2190} \(label)")
                .font(.system(size: 30, weight: .medium))
                .foregroundStyle(Theme.accent)
                .padding(.vertical, 12)
                .padding(.horizontal, 8)
                .ring(radius: 12, width: 6, inset: -6)
        }
        .buttonStyle(FlatButton())
    }
}

/// The page's round buttons beside a cover: Play filled with the accent, the
/// rest outlined.
struct RoundButton: ButtonStyle {
    var primary = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 30, weight: primary ? .semibold : .medium))
            .foregroundStyle(primary ? Color(red: 8 / 255, green: 19 / 255, blue: 31 / 255) : Theme.muted)
            .lineLimit(1)
            .padding(.horizontal, primary ? 44 : 36)
            .padding(.vertical, 18)
            .background(Capsule().fill(primary ? Theme.accent : Color.clear))
            .overlay { if !primary { Capsule().strokeBorder(Theme.border, lineWidth: 2) } }
            .overlay { RoundRing() }
            .opacity(configuration.isPressed ? 0.65 : 1)
    }
}

private struct RoundRing: View {
    @Environment(\.isFocused) private var focused
    var body: some View {
        Capsule().strokeBorder(.white, lineWidth: 6).padding(-12).opacity(focused ? 1 : 0)
    }
}

/// The head of the page: the cover, and beside it the kind ("ALBUM"), the
/// name, who, the facts and the buttons, all sitting on the cover's foot.
struct DetailHead<Buttons: View>: View {
    let art: URL?
    var round = false
    let kind: String
    let title: String
    let subtitle: String?
    let facts: String
    @ViewBuilder let buttons: Buttons

    var body: some View {
        HStack(alignment: .bottom, spacing: 48) {
            Cover(url: art)
                .frame(width: 440, height: 440)
                .clipShape(round ? AnyShape(Circle()) : AnyShape(RoundedRectangle(cornerRadius: Theme.cardRadius)))
                .shadow(color: .black.opacity(0.45), radius: 30, y: 20)
            VStack(alignment: .leading, spacing: 12) {
                Text(kind.uppercased())
                    .font(.system(size: 24, weight: .bold))
                    .tracking(2)
                    .foregroundStyle(Theme.muted)
                Text(title)
                    .font(.system(size: 60, weight: .bold))
                    .foregroundStyle(Theme.text)
                    .lineLimit(2)
                if let subtitle, !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.system(size: 30, weight: .semibold))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                }
                if !facts.isEmpty {
                    Text(facts).font(.system(size: 28)).foregroundStyle(Theme.muted)
                }
                HStack(spacing: 20) { buttons }
                    .padding(.top, 20)
                    .focusSection()
            }
        }
    }
}

/// The page's "3 songs · 12 min".
func songFacts(_ songs: [Item], year: Int? = nil) -> String {
    let total = songs.compactMap(\.durationSeconds).reduce(0, +)
    var parts: [String] = []
    if let year { parts.append(String(year)) }
    parts.append("\(songs.count) song\(songs.count == 1 ? "" : "s")")
    if total > 0 {
        let minutes = Int((total / 60).rounded())
        parts.append(minutes < 60 ? "\(minutes) min" : "\(minutes / 60) hr \(minutes % 60) min")
    }
    return parts.joined(separator: " \u{00B7} ")
}

/// One song: its number in grey, its name, its length; the whole row ringed
/// in white with the remote on it.
struct TrackRow: View {
    let number: Int
    let song: Item
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 24) {
                Text("\(number)")
                    .monospacedDigit()
                    .foregroundStyle(Theme.muted)
                    .frame(width: 72, alignment: .trailing)
                VStack(alignment: .leading, spacing: 2) {
                    Text(song.title).foregroundStyle(Theme.text).lineLimit(1)
                }
                Spacer(minLength: 20)
                if let d = song.durationSeconds {
                    Text(clock(d)).monospacedDigit().font(.system(size: 27)).foregroundStyle(Theme.muted)
                }
            }
            .font(.system(size: 30))
            .padding(.vertical, 20)
            .padding(.horizontal, 24)
            .contentShape(Rectangle())
            .ring(radius: 16, width: 6, inset: -6)
        }
        .buttonStyle(FlatButton())
    }
}

/// An album's or a playlist's page.
struct SongList: View {
    let back: String
    let kind: String
    let title: String
    let subtitle: String
    let art: URL?
    let songs: [Item]
    var year: Int? = nil
    /// The album's radio, where there is one.
    var radio: API.Station? = nil
    @Environment(AppModel.self) private var model

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                BackLink(label: back)
                DetailHead(art: art, kind: kind, title: title, subtitle: subtitle,
                           facts: songs.isEmpty ? "" : songFacts(songs, year: year)) {
                    Button("\u{25B6}  Play") { model.play(songs) }
                        .buttonStyle(RoundButton(primary: true))
                        .pageStartsHere()
                    Button("Shuffle") { model.play(songs.shuffled()) }
                        .buttonStyle(RoundButton())
                    if let radio {
                        Button("Radio") { Task { await model.tune(radio) } }
                            .buttonStyle(RoundButton())
                    }
                }
                .disabled(songs.isEmpty)
                .padding(.top, 24)
                .padding(.bottom, 44)
                LazyVStack(spacing: 0) {
                    ForEach(Array(songs.enumerated()), id: \.element.key) { i, song in
                        TrackRow(number: i + 1, song: song) { model.play(songs, from: i) }
                    }
                }
                .focusSection()
            }
            .padding(.horizontal, Theme.page)
            .padding(.top, 12)
            .padding(.bottom, 60)
        }
        .scrollClipDisabled()
        .toolbar(.hidden, for: .navigationBar)
    }
}
