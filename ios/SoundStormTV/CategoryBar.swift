import SwiftUI

/// The row of categories along the top of a tab - the web page's pills
/// (TABS in app.js), TV-sized. Moving along it with the remote changes the
/// category at once, as tvOS's own top bars do, so looking through them is
/// only left and right; the chosen one stays marked when the focus moves
/// down into what it shows.
struct CategoryBar<Value: Hashable>: View {
    let items: [(value: Value, label: String)]
    @Binding var selection: Value
    @FocusState private var focused: Value?

    var body: some View {
        ScrollView(.horizontal) {
            HStack(spacing: 16) {
                ForEach(items, id: \.value) { item in
                    Button { selection = item.value } label: {
                        Text(item.label)
                            .font(.callout.weight(.semibold))
                            .padding(.horizontal, 28)
                            .padding(.vertical, 12)
                            .background(
                                Capsule().fill(.white.opacity(selection == item.value && focused == nil ? 0.22 : 0))
                            )
                    }
                    .buttonStyle(.plain)
                    .focused($focused, equals: item.value)
                }
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 20)
        }
        .scrollClipDisabled()
        .onChange(of: focused) { _, value in
            if let value, value != selection { selection = value }
        }
        // Coming up from below lands on the chosen category, not the first.
        .defaultFocus($focused, selection)
    }
}
