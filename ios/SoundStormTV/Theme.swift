import SwiftUI

/// The web page's look in TV mode (style.css, `html.tv`), so the Apple TV
/// app is a copy of the Google TV one - the owner's asking, 2026-10-03. The
/// numbers are the page's, measured in Chrome at a Google TV's 960 x 540,
/// doubled: an Apple TV screen is 1920 points across.
enum Theme {
    static let bg = Color.black
    static let surface = Color(red: 23 / 255, green: 27 / 255, blue: 34 / 255)      // #171b22
    static let surface2 = Color(red: 31 / 255, green: 36 / 255, blue: 45 / 255)     // #1f242d
    static let border = Color(red: 42 / 255, green: 48 / 255, blue: 57 / 255)       // #2a3039
    static let text = Color(red: 230 / 255, green: 233 / 255, blue: 238 / 255)      // #e6e9ee
    static let muted = Color(red: 148 / 255, green: 155 / 255, blue: 167 / 255)     // #949ba7
    static let accent = Color(red: 106 / 255, green: 168 / 255, blue: 255 / 255)    // #6aa8ff
    static let quick = Color(red: 21 / 255, green: 21 / 255, blue: 21 / 255)       // the quick buttons' #151515
    static let sideBar = Color(red: 14 / 255, green: 17 / 255, blue: 22 / 255)      // rgba(14,17,22,.96)

    // The side bar: 84px wide, entries 64px tall with 6px between.
    static let sideBarWidth: CGFloat = 168
    static let tabHeight: CGFloat = 128
    // The header: 18px above, 28px either side; the search box 43px tall.
    static let headerTop: CGFloat = 36
    static let gutter: CGFloat = 56
    // Cards: 180px square covers, 10px corners.
    static let card: CGFloat = 360
    static let cardRadius: CGFloat = 20
}

/// The page's focus mark on a TV: a white ring (4px round a cover, 3px inset
/// on a side bar entry) and nothing else - no lift, no tilt, no shadow.
struct Ring: ViewModifier {
    var radius: CGFloat
    var width: CGFloat = 6
    var inset: CGFloat = 0
    @Environment(\.isFocused) private var focused

    func body(content: Content) -> some View {
        content.overlay {
            RoundedRectangle(cornerRadius: radius)
                .strokeBorder(.white, lineWidth: width)
                .padding(inset)
                .opacity(focused ? 1 : 0)
        }
    }
}

/// A button that draws its own focus (Ring) instead of tvOS's lift.
struct FlatButton: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .opacity(configuration.isPressed ? 0.85 : 1)
    }
}

extension View {
    func ring(radius: CGFloat, width: CGFloat = 6, inset: CGFloat = 0) -> some View {
        modifier(Ring(radius: radius, width: width, inset: inset))
    }
}

/// A card's cover as the page draws it on a TV: no lift and no shadow, but a
/// white outline round the cover and the card a little larger (1.05) while
/// it has the focus.
struct CardButton: ButtonStyle {
    var radius: CGFloat = Theme.cardRadius

    func makeBody(configuration: Configuration) -> some View {
        CardFocus(radius: radius, pressed: configuration.isPressed) { configuration.label }
    }
}

private struct CardFocus<Label: View>: View {
    let radius: CGFloat
    let pressed: Bool
    @ViewBuilder let label: Label
    @Environment(\.isFocused) private var focused

    var body: some View {
        label
            .overlay {
                RoundedRectangle(cornerRadius: radius + 8)
                    .strokeBorder(.white, lineWidth: 8)
                    .padding(-8)
                    .opacity(focused ? 1 : 0)
            }
            .scaleEffect(focused ? (pressed ? 1.0 : 1.05) : 1)
            .animation(.easeOut(duration: 0.15), value: focused)
            .animation(.easeOut(duration: 0.1), value: pressed)
    }
}

extension Theme {
    /// The page's grid of covers on a TV: four across, 20px apart.
    static let grid = Array(repeating: GridItem(.fixed(card), spacing: 40), count: 4)
    /// Posters (films, shows): taller than wide, six across.
    static let posterGrid = Array(repeating: GridItem(.fixed(240), spacing: 40), count: 6)
    /// The page's margin between the side bar and what is on it.
    static let page: CGFloat = 40
}

/// The page's buttons: the main one filled with the accent and dark text,
/// the rest dark with a thin border and grey text; a white ring on focus.
struct WebButton: ButtonStyle {
    var primary = false
    var wide = true

    func makeBody(configuration: Configuration) -> some View {
        WebButtonBody(primary: primary, wide: wide, pressed: configuration.isPressed) { configuration.label }
    }
}

private struct WebButtonBody<Label: View>: View {
    let primary: Bool
    let wide: Bool
    let pressed: Bool
    @ViewBuilder let label: Label
    @Environment(\.isEnabled) private var enabled

    var body: some View {
        label
            .font(.system(size: 32, weight: .semibold))
            .foregroundStyle(primary ? Color(red: 11 / 255, green: 13 / 255, blue: 16 / 255) : Theme.text.opacity(0.85))
            .lineLimit(1)
            .padding(.horizontal, 34)
            .frame(maxWidth: wide ? .infinity : nil, minHeight: 84)
            .background(RoundedRectangle(cornerRadius: 16).fill(primary ? Theme.accent : Theme.surface2))
            .overlay {
                if !primary { RoundedRectangle(cornerRadius: 16).strokeBorder(Theme.border, lineWidth: 2) }
            }
            .ring(radius: 22, width: 6, inset: -6)
            .opacity(enabled ? (pressed ? 0.85 : 1) : 0.5)
    }
}

/// A text box with its name above, as the page has it. The box itself is
/// tvOS's own (grey, white while the remote is on it): a TextField on tvOS
/// always draws its own platter, and one drawn inside a second box looked
/// broken.
struct WebField: View {
    let label: String
    @Binding var text: String
    var secure = false
    var onSubmit: () -> Void = {}

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(label)
                .font(.system(size: 28, weight: .medium))
                .foregroundStyle(Theme.muted)
            Group {
                if secure { SecureField("", text: $text) } else { TextField("", text: $text) }
            }
            .onSubmit(onSubmit)
        }
    }
}

/// The page's sign-in card: a dark panel with a thin border, centred on black.
struct Panel: ViewModifier {
    var width: CGFloat = 760
    var centred = false

    func body(content: Content) -> some View {
        content
            .multilineTextAlignment(centred ? .center : .leading)
            .frame(width: width - 100, alignment: centred ? .center : .leading)
            .padding(50)
            .background(RoundedRectangle(cornerRadius: 20).fill(Theme.surface))
            .overlay(RoundedRectangle(cornerRadius: 20).strokeBorder(Theme.border, lineWidth: 2))
            .buttonStyle(WebButton())
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Theme.bg.ignoresSafeArea())
    }
}

extension View {
    func panel(width: CGFloat = 760, centred: Bool = false) -> some View { modifier(Panel(width: width, centred: centred)) }
    /// Grey explaining text, the page's `.muted`.
    func muted() -> some View { font(.system(size: 30)).foregroundStyle(Theme.muted) }
}

/// The page's message at the foot of the screen (`.toast`): a dark rounded
/// box, its text in the page's white.
struct Toast: View {
    let text: String

    var body: some View {
        Text(text)
            .font(.system(size: 28, weight: .medium))
            .foregroundStyle(Theme.text)
            .lineLimit(1)
            .padding(.horizontal, 36)
            .padding(.vertical, 20)
            .background(RoundedRectangle(cornerRadius: 24).fill(Color(red: 36 / 255, green: 42 / 255, blue: 53 / 255)))
            .shadow(color: .black.opacity(0.5), radius: 30, y: 24)
            .transition(.opacity)
    }
}
