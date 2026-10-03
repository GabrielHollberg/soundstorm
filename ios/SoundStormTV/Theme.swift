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
