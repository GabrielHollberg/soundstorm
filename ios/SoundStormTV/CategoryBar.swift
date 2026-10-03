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

    // The page's pills (#subtabs on a TV): 39px tall, 16px either side of the
    // name, 14px semibold; the chosen one light with dark text, the others the
    // surface with a border and grey text. The chosen one sits in the middle
    // of the row, as the page keeps it.
    var body: some View {
        GeometryReader { geo in
            // Half the row at each end, so the first and last can sit in the
            // middle too.
            let spacer = max(0, geo.size.width / 2 - 140)
            ScrollViewReader { scroller in
                ScrollView(.horizontal) {
                    HStack(spacing: 16) {
                        Color.clear.frame(width: spacer, height: 1)
                        ForEach(items, id: \.value) { item in
                            pill(item)
                        }
                        Color.clear.frame(width: spacer, height: 1)
                    }
                    .padding(.vertical, 8)
                }
                .scrollClipDisabled()
                .onChange(of: selection, initial: true) { _, value in
                    withAnimation(.easeOut(duration: 0.25)) { scroller.scrollTo(value, anchor: .center) }
                }
            }
        }
        .frame(height: 110)
        .onChange(of: focused) { _, value in
            if let value, value != selection { selection = value }
        }
        // Coming up from below lands on the chosen category, not the first.
        .defaultFocus($focused, selection)
        .focusSection()
    }

    private func pill(_ item: (value: Value, label: String)) -> some View {
        let on = selection == item.value
        return Button { selection = item.value } label: {
            Text(item.label)
                .font(.system(size: 28, weight: .semibold))
                .foregroundStyle(on ? Color.black : Theme.muted)
                .padding(.horizontal, 32)
                .frame(height: 78)
                .background(Capsule().fill(on ? Theme.text : Theme.surface))
                .overlay(Capsule().strokeBorder(on ? Theme.text : Theme.border, lineWidth: 2))
                .padding(6)
                .ring(radius: 45, width: 6)
        }
        .buttonStyle(FlatButton())
        .focused($focused, equals: item.value)
        .pageStartsHere(on)
        .id(item.value)
    }
}
