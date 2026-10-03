import SwiftUI

/// The frame every page sits in, as the web page draws it on a TV: the side
/// bar down the left (Settings at its top, whoever is watching, what is
/// playing, then the tabs), and across the top the wordmark and the search
/// box. tvOS's own side bar was replaced (2026-10-03): it could not be made
/// to look like SoundStorm.

/// Where the remote starts: on the page, never the side bar or the search
/// box (the page's own rule) - the page marks its first choice with
/// `.pageStartsHere()`.
struct PageFocusKey: EnvironmentKey { static let defaultValue: Namespace.ID? = nil }
extension EnvironmentValues {
    var pageFocus: Namespace.ID? {
        get { self[PageFocusKey.self] }
        set { self[PageFocusKey.self] = newValue }
    }
}
private struct StartsHere: ViewModifier {
    let on: Bool
    @Environment(\.pageFocus) private var page
    func body(content: Content) -> some View {
        if let page { content.prefersDefaultFocus(on, in: page) } else { content }
    }
}
extension View {
    func pageStartsHere(_ on: Bool = true) -> some View { modifier(StartsHere(on: on)) }
}

struct SideBar: View {
    @Binding var tab: String
    @Environment(AppModel.self) private var model
    @Environment(API.self) private var api
    @Environment(Player.self) private var player

    var body: some View {
        VStack(spacing: 12) {
            entry("settings", label: "Settings", icon: "gear")
            if let user = api.user {
                // Who is watching: their circle, opening "Who's listening?".
                Button { Task { await model.showProfiles() } } label: {
                    tabLabel(selected: false, lit: true) {
                        Avatar(name: user.name, picture: user.picture, size: 80)
                    } title: {
                        Text(user.name)
                    }
                }
                .buttonStyle(FlatButton())
            }
            if let song = player.current {
                // The mini player's place on a TV: the playing song's cover.
                Button { model.showingNowPlaying = true } label: {
                    tabLabel(selected: false, lit: true) {
                        SafeImage(url: api.artURL(source: song.sourceId, artId: song.artId, size: 160), maxPixels: 160) {
                            $0.resizable().scaledToFill()
                        } placeholder: {
                            Image(systemName: player.isPlaying ? "waveform" : "pause.fill").font(.system(size: 40))
                        }
                        .frame(width: 80, height: 80)
                        .clipShape(RoundedRectangle(cornerRadius: 16))
                    } title: {
                        Text("Playing")
                    }
                }
                .buttonStyle(FlatButton())
            }
            entry("home", label: "Home", icon: "home")
            // A tab with no shelf behind it is left out, as on the page.
            if model.has(tab: "music") { entry("music", label: "Music", icon: "note") }
            if model.has(tab: "watch") { entry("watch", label: "Watch", icon: "film") }
            if model.has(tab: "books") { entry("books", label: "Books", icon: "book") }
            if model.has(tab: "photos") { entry("photos", label: "Photos", icon: "photo") }
            Spacer(minLength: 0)
        }
        .padding(.top, 16)
        .frame(width: Theme.sideBarWidth)
        .frame(maxHeight: .infinity)
        .background(Theme.sideBar.ignoresSafeArea())
        .focusSection()
    }

    private func entry(_ value: String, label: String, icon: String) -> some View {
        let selected = tab == value
        return Button { tab = value } label: {
            tabLabel(selected: selected, lit: selected) {
                Image("Icons/" + icon)
                    .resizable()
                    .frame(width: 48, height: 48)
                    .foregroundStyle(selected ? Theme.accent : Theme.muted)
                    .frame(height: 56)
            } title: {
                Text(label)
            }
        }
        .buttonStyle(FlatButton())
    }

    /// One entry: its picture over its name, the name white when it is the
    /// page showing (or a person, or what plays) and grey otherwise.
    private func tabLabel<Icon: View, Title: View>(selected: Bool, lit: Bool,
                                                    @ViewBuilder icon: () -> Icon,
                                                    @ViewBuilder title: () -> Title) -> some View {
        VStack(spacing: 6) {
            icon()
            title()
                .font(.system(size: 24, weight: .semibold))
                .foregroundStyle(lit ? Theme.text : Theme.muted)
                .lineLimit(1)
        }
        .padding(.vertical, 12)
        .frame(width: Theme.sideBarWidth - 2, height: Theme.tabHeight)
        .contentShape(Rectangle())
        .ring(radius: 28, width: 6, inset: 8)
    }
}

/// The wordmark: the cloud and the name in heavy italic, as the page's
/// header has them.
struct WordMark: View {
    var size: CGFloat = 36

    var body: some View {
        HStack(spacing: size * 0.3) {
            // The page's cloud.svg, 1.3em by 1.1em, in the text's colour.
            Image("Cloud")
                .resizable()
                .scaledToFit()
                .frame(width: size * 1.3, height: size * 1.1)
                .foregroundStyle(Theme.text)
                .offset(y: size * 0.1)
            Text("SoundStorm")
                .font(.system(size: size, weight: .heavy).italic())
                .foregroundStyle(Theme.text)
        }
    }
}

/// Across the top: the wordmark, and the search box - a button here, which
/// opens the search page (typing on a TV is its keyboard's, not a box's).
struct Header: View {
    @Binding var tab: String

    var body: some View {
        HStack(spacing: 32) {
            WordMark()
            Button { tab = "search" } label: {
                Text("Search everything\u{2026}")
                    .font(.system(size: 30))
                    .foregroundStyle(Theme.muted)
                    .padding(.horizontal, 22)
                    .frame(maxWidth: 1240, minHeight: 86, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: 16).fill(Theme.bg))
                    .overlay(RoundedRectangle(cornerRadius: 16).strokeBorder(Theme.border, lineWidth: 2))
                    .ring(radius: 16, width: 6)
            }
            .buttonStyle(FlatButton())
            Spacer(minLength: 0)
        }
        .padding(.top, Theme.headerTop)
        .padding(.horizontal, Theme.gutter)
        .padding(.bottom, 28)
        .focusSection()
    }
}
