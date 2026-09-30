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
#endif
