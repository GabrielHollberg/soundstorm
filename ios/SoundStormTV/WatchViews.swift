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
        .buttonStyle(.borderless)
        .overlay(alignment: .bottom) {
            Text(item.title).lineLimit(1).frame(width: 240).offset(y: 50)
        }
        .padding(.bottom, 50)
    }

    private var poster: some View {
        Cover(url: api.artURL(source: item.sourceId, artId: item.artId, size: 500))
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

    var body: some View {
        HStack(alignment: .top, spacing: 80) {
            VStack(alignment: .leading, spacing: 24) {
                Cover(url: api.artURL(source: series.sourceId, artId: series.artId, size: 700))
                    .frame(width: 400, height: 600)
                Text(series.title).font(.title2).lineLimit(2)
                if let next = nextToWatch() {
                    Button { model.playVideo(next) } label: {
                        Label(label(for: next), systemImage: "play.fill")
                    }
                }
            }
            .frame(width: 400)
            List {
                ForEach(seasons, id: \.self) { season in
                    Section(season == "0" ? "Specials" : "Season \(season)") {
                        ForEach(episodes.filter { $0.season == season }, id: \.key) { episode in
                            Button { model.playVideo(episode) } label: { row(episode) }
                        }
                    }
                }
            }
        }
        .padding(60)
        .task {
            guard let show = try? await api.show(series) else { return }
            episodes = show.episodes
            progress = show.progress ?? [:]
        }
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

    private func label(for next: Item) -> String {
        let code = next.episodeCode.map { " \($0)" } ?? ""
        let part = (progress[next.id] ?? 0) > 0.005
        return (part ? "Carry on" : "Play") + code
    }

    private func row(_ episode: Item) -> some View {
        let done = progress[episode.id] ?? 0
        return HStack(spacing: 30) {
            Text(episode.episodeCode ?? "").foregroundStyle(.secondary).frame(width: 140, alignment: .leading)
            VStack(alignment: .leading, spacing: 8) {
                Text(episode.title).lineLimit(1)
                if done > 0.005 && done < 0.93 {
                    ProgressView(value: done).frame(width: 300)
                }
            }
            Spacer()
            if done >= 0.93 { Image(systemName: "checkmark").foregroundStyle(.secondary) }
        }
    }
}
