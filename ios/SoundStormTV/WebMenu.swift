import SwiftUI

/// The page's menu (`.item-menu`), in place of tvOS's grey context menu: a
/// dark panel with a head naming what it is for, rows of an icon and a name,
/// a tick on the one chosen, and pages of their own for a longer choice
/// (Look, Speed, Sleep timer) with a way back. Opened by holding OK, as the
/// page's TV mode opens its menu.
struct MenuPage {
    var title: String
    var subtitle: String? = nil
    var entries: [MenuEntry]
}

struct MenuEntry: Identifiable {
    let id = UUID()
    var icon: String
    var label: String
    var detail: String? = nil
    var checked = false
    /// The page's pink heart for a favorite.
    var favorite = false
    var action: Action

    enum Action {
        case run(() -> Void)
        case page(() -> MenuPage)
    }
}

private struct WebMenuPanel: View {
    let root: MenuPage
    let close: () -> Void
    @State private var stack: [MenuPage] = []
    @FocusState private var lit: UUID?
    private static let backID = UUID()

    var body: some View {
        let page = stack.last ?? root
        VStack(alignment: .leading, spacing: 4) {
            VStack(alignment: .leading, spacing: 2) {
                Text(page.title)
                    .font(.system(size: 28, weight: .semibold))
                    .foregroundStyle(Theme.text)
                    .lineLimit(1)
                if let s = page.subtitle, !s.isEmpty {
                    Text(s).font(.system(size: 25)).foregroundStyle(Theme.muted).lineLimit(1)
                }
            }
            .padding(.horizontal, 20)
            .padding(.top, 16)
            .padding(.bottom, 20)
            Rectangle().fill(.white.opacity(0.07)).frame(height: 2).padding(.bottom, 4)
            ScrollView {
                VStack(spacing: 4) {
                    if !stack.isEmpty {
                        row(id: Self.backID, icon: "back", label: "Back", muted: true) { stack.removeLast() }
                    }
                    ForEach(page.entries) { e in
                        row(id: e.id, icon: e.icon, label: e.label, detail: e.detail, checked: e.checked, favorite: e.favorite) {
                            switch e.action {
                            case .run(let go): close(); go()
                            case .page(let make): stack.append(make())
                            }
                        }
                    }
                }
                .padding(4)
            }
            .scrollClipDisabled()
            .frame(maxHeight: 700)
            .fixedSize(horizontal: false, vertical: true)
        }
        .padding(12)
        .frame(width: 560)
        .background(RoundedRectangle(cornerRadius: 24).fill(Color(red: 26 / 255, green: 31 / 255, blue: 40 / 255)))
        .overlay(RoundedRectangle(cornerRadius: 24).strokeBorder(.white.opacity(0.08), lineWidth: 2))
        .shadow(color: .black.opacity(0.55), radius: 40, y: 28)
        .focusSection()
        // Back on the remote steps out of a page, then closes.
        .onExitCommand { if stack.isEmpty { close() } else { stack.removeLast() } }
        .onAppear { DispatchQueue.main.async { lightFirst() } }
        .onChange(of: stack.count) { DispatchQueue.main.async { lightFirst() } }
    }

    private func row(id: UUID, icon: String, label: String, detail: String? = nil, checked: Bool = false,
                     favorite: Bool = false, muted: Bool = false, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 24) {
                Group {
                    if favorite {
                        Image(systemName: "heart.fill").resizable().scaledToFit()
                            .foregroundStyle(Color(red: 1, green: 107 / 255, blue: 142 / 255))
                    } else {
                        Image("Icons/" + icon).resizable()
                            .foregroundStyle(muted ? Theme.muted : Theme.text)
                    }
                }
                .frame(width: 34, height: 34)
                Text(label)
                    .font(.system(size: muted ? 26 : 28, weight: muted ? .semibold : .medium))
                    .foregroundStyle(muted ? Theme.muted : Theme.text)
                    .lineLimit(1)
                Spacer(minLength: 8)
                if let detail {
                    Text(detail).font(.system(size: 25)).monospacedDigit().foregroundStyle(Theme.muted)
                }
                if checked {
                    Image("Icons/check").resizable().frame(width: 30, height: 30).foregroundStyle(Theme.accent)
                }
            }
            .padding(.horizontal, 20)
            .frame(minHeight: 80)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(MenuRowLight())
            .contentShape(Rectangle())
            .ring(radius: 16, width: 6, inset: -6)
        }
        .buttonStyle(FlatButton())
        .focused($lit, equals: id)
    }

    /// The remote starts on the first choice of each page (on a page's own
    /// first choice, not its Back).
    private func lightFirst() {
        let page = stack.last ?? root
        lit = page.entries.first(where: \.checked)?.id ?? page.entries.first?.id
    }
}

/// The page's faint light behind the row the remote is on.
private struct MenuRowLight: View {
    @Environment(\.isFocused) private var focused
    var body: some View {
        RoundedRectangle(cornerRadius: 16).fill(.white.opacity(focused ? 0.06 : 0))
    }
}

private struct WebMenuHost: ViewModifier {
    @Binding var page: MenuPage?

    func body(content: Content) -> some View {
        ZStack {
            // What is underneath takes no presses while the menu is up.
            content.disabled(page != nil)
            if let root = page {
                Color.black.opacity(0.45).ignoresSafeArea()
                    .transition(.opacity)
                WebMenuPanel(root: root) { page = nil }
                    .transition(.opacity.combined(with: .scale(scale: 0.98)))
            }
        }
        .animation(.easeOut(duration: 0.12), value: page != nil)
    }
}

extension View {
    /// Shows the page's menu over this view while `page` is set.
    func webMenu(_ page: Binding<MenuPage?>) -> some View { modifier(WebMenuHost(page: page)) }
}
