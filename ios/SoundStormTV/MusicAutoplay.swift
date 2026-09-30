#if DEBUG
import SwiftUI

/// For the simulator, which has no remote: `-autostation YES` tunes the
/// first radio station once the library is open.
struct DebugAutostation: ViewModifier {
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model

    func body(content: Content) -> some View {
        content.task {
            guard UserDefaults.standard.bool(forKey: "autostation"), model.player?.current == nil,
                  let station = try? await api.radio().stations.first
            else { return }
            await model.tune(station)
        }
    }
}

/// -autoread <id> opens an ebook or document; -autoturn <n> turns n pages on.
struct DebugAutoread: ViewModifier {
    @Environment(API.self) private var api
    @Environment(AppModel.self) private var model

    func body(content: Content) -> some View {
        content.task {
            guard let id = UserDefaults.standard.string(forKey: "autoread"),
                  let item = try? await api.page(kinds: ["ebook", "document"], offset: 0).items.first(where: { $0.id == id })
            else { return }
            model.read(item)
            let turns = UserDefaults.standard.integer(forKey: "autoturn")
            guard turns > 0 else { return }
            try? await Task.sleep(for: .seconds(6))
            switch model.reading {
            case .epub(let reader): for _ in 0..<turns { await reader.next() }
            case .pdf(let reader): for _ in 0..<turns { reader.turn(1) }
            case nil: break
            }
        }
    }
}
#endif
