import SwiftUI

/// A poster: plays a film or an episode, opens a show.
struct PosterCard: View {
    let item: Item
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model

    var body: some View {
        Group {
            if item.isVideo {
                Button { model.playVideo(item) } label: { poster }
            } else if item.kind == "audiobook" {
                Button { Task { await model.playBook(item) } } label: { poster }
            } else {
                NavigationLink(value: item) { poster }
            }
        }
        .buttonStyle(CardButton())
        .overlay(alignment: .bottom) { CardTitle(title: item.title, subtitle: item.cardLine, width: 240) }
        .padding(.bottom, CardTitle.room)
    }

    private var poster: some View {
        Cover(url: api.artURL(source: item.sourceId, artId: item.artId, size: 500), kind: item.kind)
            .frame(width: 240, height: 360)
    }
}

/// A show's episodes, season by season, each marked with how far this person
/// got, and one Play for what to watch now - the page's rule: carry on the
/// one part-watched, else the one after the last finished, else the first.
struct ShowView: View {
    let series: Item
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model
    @State private var episodes: [Item] = []
    @State private var progress: [String: Double] = [:]
    @State private var season: String?

    /// The page's show page (`showShow`): "← TV", the poster with the name,
    /// the seasons and what it is about, one button for what to watch now,
    /// the seasons as pills, then that season's episodes.
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                BackLink(label: "TV")
                HStack(alignment: .top, spacing: 36) {
                    Cover(url: api.artURL(source: series.sourceId, artId: series.artId, size: 700))
                        .frame(width: 360, height: 540)
                    VStack(alignment: .leading, spacing: 16) {
                        Text(series.title)
                            .font(.system(size: 48, weight: .bold))
                            .foregroundStyle(Theme.text)
                            .lineLimit(2)
                        Text([series.extra?["year"], seasons.isEmpty ? nil : "\(seasons.count) season\(seasons.count == 1 ? "" : "s")"]
                                .compactMap { $0 }.joined(separator: " \u{00B7} "))
                            .font(.system(size: 30))
                            .foregroundStyle(Theme.muted)
                        if let about = series.extra?["overview"], !about.isEmpty {
                            Text(about)
                                .font(.system(size: 28))
                                .foregroundStyle(Theme.muted)
                                .lineSpacing(6)
                                .lineLimit(4)
                                .frame(maxWidth: 1100, alignment: .leading)
                        }
                        if let next = nextToWatch() {
                            Button("\u{25B6}  \(label(for: next))") { model.playVideo(next) }
                                .buttonStyle(RoundButton(primary: true))
                                .pageStartsHere()
                                .padding(.top, 8)
                        }
                    }
                }
                .padding(.top, 20)
                .padding(.bottom, 28)
                if seasons.count > 1 {
                    HStack(spacing: 16) {
                        ForEach(seasons, id: \.self) { s in
                            let on = s == shownSeason
                            Button { season = s } label: {
                                Text(s == "0" ? "Specials" : "Season \(s)")
                                    .font(.system(size: 28, weight: .semibold))
                                    .foregroundStyle(on ? Color.black : Theme.muted)
                                    .padding(.horizontal, 28)
                                    .frame(height: 70)
                                    .background(Capsule().fill(on ? Theme.text : Theme.surface))
                                    .overlay(Capsule().strokeBorder(on ? Theme.text : Theme.border, lineWidth: 2))
                                    .padding(6)
                                    .ring(radius: 41, width: 6)
                            }
                            .buttonStyle(FlatButton())
                        }
                    }
                    .focusSection()
                    .padding(.bottom, 16)
                }
                LazyVStack(alignment: .leading, spacing: 8) {
                    ForEach(episodes.filter { $0.season == shownSeason }, id: \.key) { episode in
                        Button { model.playVideo(episode) } label: { row(episode) }
                            .buttonStyle(FlatButton())
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
        .task {
            guard let show = try? await api.show(series) else { return }
            episodes = show.episodes
            progress = show.progress ?? [:]
        }
    }

    /// The season chosen, else the one holding what to watch now.
    private var shownSeason: String {
        season ?? nextToWatch()?.season ?? seasons.first ?? "0"
    }

    private var seasons: [String] {
        var seen: [String] = []
        for e in episodes where !seen.contains(e.season) { seen.append(e.season) }
        return seen
    }

    private func nextToWatch() -> Item? {
        if let part = episodes.first(where: { (progress[$0.id] ?? 0) > 0.005 && (progress[$0.id] ?? 0) < 0.93 }) {
            return part
        }
        let lastDone = episodes.lastIndex { (progress[$0.id] ?? 0) >= 0.93 } ?? -1
        return episodes.indices.contains(lastDone + 1) ? episodes[lastDone + 1] : episodes.first
    }

    /// "Resume S1 E2" part way through it, else "Play S1 E2", as the page says.
    private func label(for next: Item) -> String {
        let part = (progress[next.id] ?? 0) > 0.005 && (progress[next.id] ?? 0) < 0.93
        let name: String
        if let n = next.extra?["number"], next.season != "0" { name = "S\(next.season) E\(n)" } else { name = next.title }
        return (part ? "Resume " : "Play ") + name
    }

    /// The page's episode row: a still with the progress along its foot, the
    /// number and name, its length (and Watched), what it is about, and a
    /// green tick once seen.
    private func row(_ episode: Item) -> some View {
        let done = progress[episode.id] ?? 0
        return HStack(alignment: .top, spacing: 24) {
            ZStack(alignment: .bottom) {
                Theme.surface2
                SafeImage(url: api.artURL(source: episode.sourceId, artId: episode.artId, size: 500), maxPixels: 500) {
                    $0.resizable().scaledToFill()
                } placeholder: { Color.clear }
                if done > 0.005 && done < 0.93 {
                    GeometryReader { g in
                        ZStack(alignment: .leading) {
                            Color(red: 6 / 255, green: 9 / 255, blue: 14 / 255).opacity(0.7)
                            Theme.accent.frame(width: g.size.width * done)
                        }
                    }
                    .frame(height: 10)
                }
            }
            .frame(width: 400, height: 225)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            VStack(alignment: .leading, spacing: 6) {
                Text((episode.extra?["number"].map { "\($0). " } ?? "") + episode.title)
                    .font(.system(size: 30, weight: .bold))
                    .foregroundStyle(Theme.text)
                    .lineLimit(1)
                let minutes = Int(((episode.durationSeconds ?? 0) / 60).rounded())
                let meta = [minutes > 0 ? "\(minutes) min" : nil, done >= 0.93 ? "Watched" : nil].compactMap { $0 }
                if !meta.isEmpty {
                    Text(meta.joined(separator: " \u{00B7} ")).font(.system(size: 26)).foregroundStyle(Theme.muted)
                }
                if let about = episode.extra?["overview"], !about.isEmpty {
                    Text(about)
                        .font(.system(size: 26))
                        .foregroundStyle(Theme.muted)
                        .lineSpacing(4)
                        .lineLimit(2)
                        .padding(.top, 4)
                }
            }
            Spacer(minLength: 0)
            if done >= 0.93 {
                Image("Icons/check")
                    .resizable()
                    .frame(width: 36, height: 36)
                    .foregroundStyle(Color(red: 34 / 255, green: 197 / 255, blue: 94 / 255))
                    .padding(.top, 8)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .ring(radius: 24, width: 6, inset: -2)
    }
}
