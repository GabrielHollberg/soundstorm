import AppKit
import SwiftUI

/// EmberStorm's look: the app's black, its blue, the cloud.
enum Look {
    static let background = Color(red: 0.035, green: 0.04, blue: 0.055)
    static let card = Color(red: 0.08, green: 0.09, blue: 0.11)
    static let line = Color.white.opacity(0.08)
    static let accent = Color(red: 0.37, green: 0.65, blue: 1.0)
    static let soft = Color.white.opacity(0.62)
    static let warn = Color(red: 1.0, green: 0.42, blue: 0.38)
}

struct SetupView: View {
    let setup: Setup
    @State private var height: CGFloat = 480

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Group {
                switch setup.page {
                case .looking: Waiting(text: "One moment...")
                case .welcome: Welcome(setup: setup)
                case .questions: Questions(setup: setup)
                case .working: Working(installer: setup.installer, title: "Setting up EmberStorm")
                case .finished(let r): Finished(setup: setup, result: r)
                case .launching: Waiting(text: setup.launchStatus)
                case .running: Running(setup: setup)
                case .removing: Working(installer: setup.installer, title: "Removing EmberStorm")
                case .removed(let library): Removed(library: library)
                case .failed(let message, let retry): Failed(setup: setup, message: message, retry: retry)
                }
            }
            .padding(.horizontal, 36)
            .padding(.bottom, 32)
        }
        // As tall as the page, measured: the window fits each page (and the
        // details opening) rather than one height for all, which left a
        // page's foot empty.
        .fixedSize(horizontal: false, vertical: true)
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = max(320, $0) }
        .frame(height: height, alignment: .top)
        .animation(.easeOut(duration: 0.2), value: height)
        .background(Look.background)
        .foregroundStyle(.white)
        .tint(Look.accent)
        .task { await setup.appeared() }
    }

    private var header: some View {
        HStack(spacing: 14) {
            Image(nsImage: NSApp.applicationIconImage)
                .resizable()
                .frame(width: 48, height: 48)
            Text("EmberStorm")
                .font(.system(size: 26, weight: .heavy).italic())
        }
        .padding(.horizontal, 36)
        .padding(.top, 30)
        .padding(.bottom, 20)
    }
}

// MARK: Pages

private struct Waiting: View {
    let text: String
    var body: some View {
        HStack(spacing: 12) {
            ProgressView().controlSize(.small)
            Text(text).foregroundStyle(Look.soft)
        }
        .padding(.top, 40)
    }
}

private struct Welcome: View {
    let setup: Setup
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Your music, films, TV, audiobooks, ebooks and photos, on this Mac")
                .font(.title2.weight(.semibold))
                .fixedSize(horizontal: false, vertical: true)
            Text("EmberStorm runs here and is reached from every phone, TV and computer at home - one account each, nothing in anybody's cloud.")
                .foregroundStyle(Look.soft)
                .fixedSize(horizontal: false, vertical: true)
            Card {
                Line(icon: "clock", text: "Setting it up takes 20 to 40 minutes, mostly downloading (about 12 GB). Nothing is needed from you once it starts.")
                if setup.dockerNeeded {
                    Line(icon: "shippingbox", text: "It runs inside Docker Desktop, which is installed too - free for personal use, no Docker account needed.")
                }
                Line(icon: "key", text: "You type your Mac's password once, before it starts - nothing asks you anything after that.")
            }
            Spacer(minLength: 0)
            HStack {
                Spacer()
                Button("Continue") { setup.page = .questions }
                    .buttonStyle(Primary())
                    .keyboardShortcut(.defaultAction)
            }
        }
    }
}

private struct Questions: View {
    @Bindable var setup: Setup
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text(setup.needsPassword ? "A few things first" : "Two things first")
                .font(.title2.weight(.semibold))
            Text("Then it needs nothing more from you.")
                .foregroundStyle(Look.soft)
            Card {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Where your media goes").font(.headline)
                    Text(setup.libraryShown.path(percentEncoded: false))
                        .font(.system(.callout, design: .monospaced))
                        .foregroundStyle(Look.soft)
                        .lineLimit(1).truncationMode(.middle)
                    HStack {
                        Button("Choose another folder...") { setup.chooseLibrary() }
                        if setup.library != nil {
                            Button("Use the usual folder") { setup.library = nil }
                        }
                    }
                    .buttonStyle(Quiet())
                    Text("An external drive is fine - keep it plugged in. Folders for music, films, TV, audiobooks, ebooks and photos are made there.")
                        .font(.callout).foregroundStyle(Look.soft)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            if setup.askAwake {
                Card {
                    Toggle(isOn: $setup.keepAwake) {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Keep this Mac awake while plugged in").font(.headline)
                            Text("Asleep, nothing can reach EmberStorm. It also starts up again by itself after a power cut."
                                 + (setup.laptop ? " A MacBook still sleeps with its lid closed unless a screen is plugged in." : ""))
                                .font(.callout).foregroundStyle(Look.soft)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                    .toggleStyle(.switch)
                }
            }
            if setup.needsPassword {
                Card {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Your Mac's password").font(.headline)
                        Text("The one you sign in to this Mac with, needed to \(setup.dockerNeeded ? (setup.askAwake && setup.keepAwake ? "install Docker Desktop and keep this Mac awake" : "install Docker Desktop") : "keep this Mac awake"). It is used for this setup only, and never kept or sent anywhere.")
                            .font(.callout).foregroundStyle(Look.soft)
                            .fixedSize(horizontal: false, vertical: true)
                        SecureField("Password", text: $setup.password)
                            .textFieldStyle(.roundedBorder)
                            .onSubmit { if !setup.password.isEmpty { setup.start() } }
                        if setup.passwordWrong {
                            Text("That password didn't work. It must be this Mac account's password, and the account must be allowed to install apps (an administrator).")
                                .font(.callout).foregroundStyle(Look.warn)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
            if setup.roomShort {
                Text("This Mac has \(gb(setup.roomFree)) free, and EmberStorm's programs need about \(gb(setup.roomNeeded)). Free some space, then come back.")
                    .foregroundStyle(Look.warn)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
            HStack {
                Button("Back") { setup.page = .welcome }.buttonStyle(Quiet())
                Spacer()
                if setup.checking { ProgressView().controlSize(.small) }
                Button("Set up EmberStorm") { setup.start() }
                    .buttonStyle(Primary())
                    .keyboardShortcut(.defaultAction)
                    .disabled(setup.roomShort || setup.checking || (setup.needsPassword && setup.password.isEmpty))
            }
        }
    }
}

private struct Working: View {
    let installer: Installer
    let title: String
    @State private var details = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(title).font(.title2.weight(.semibold))
            if title.hasPrefix("Setting") {
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(Installer.Stage.allCases, id: \.self) { s in
                        HStack(spacing: 12) {
                            marker(s).frame(width: 20)
                            Text(s.title)
                                .foregroundStyle(s.rawValue <= installer.stage.rawValue ? .white : Look.soft)
                        }
                    }
                }
            }
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    ProgressView(value: installer.fraction).progressViewStyle(.linear)
                    Text("\(Int(installer.fraction * 100))%")
                        .font(.callout.monospacedDigit()).foregroundStyle(Look.soft)
                        .frame(width: 44, alignment: .trailing)
                }
                Text(installer.status)
                    .font(.callout).foregroundStyle(Look.soft)
                    .lineLimit(2)
            }
            // A button rather than a disclosure group: a Mac's opens only from
            // its small triangle, and clicking the words did nothing.
            Button {
                details.toggle()
            } label: {
                Label(details ? "Hide details" : "Show details", systemImage: details ? "chevron.down" : "chevron.right")
            }
            .buttonStyle(Quiet())
            .font(.callout)
            if details {
                ScrollView {
                    Text(installer.shown)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(Look.soft)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .defaultScrollAnchor(.bottom)
                .frame(height: 180)
                .padding(10)
                .background(Look.card, in: RoundedRectangle(cornerRadius: 10))
            }
            Text("You can leave this window open and use the Mac for something else.")
                .font(.callout).foregroundStyle(Look.soft)
        }
    }

    @ViewBuilder private func marker(_ s: Stage) -> some View {
        if s.rawValue < installer.stage.rawValue {
            Image(systemName: "checkmark.circle.fill").foregroundStyle(Look.accent)
        } else if s == installer.stage {
            ProgressView().controlSize(.small)
        } else {
            Image(systemName: "circle").foregroundStyle(Look.line)
        }
    }
    typealias Stage = Installer.Stage
}

private struct Finished: View {
    let setup: Setup
    let result: Installer.Result

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(result.upgrade && result.setup.isEmpty ? "EmberStorm is up to date" : "EmberStorm is ready")
                .font(.title2.weight(.semibold))
            if !result.setup.isEmpty {
                Card {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Next: make your account").font(.headline)
                        Text("Open EmberStorm and choose a name and password. If it asks for a setup code, it is:")
                            .foregroundStyle(Look.soft)
                            .fixedSize(horizontal: false, vertical: true)
                        Text(result.code)
                            .font(.system(size: 26, weight: .bold, design: .monospaced))
                            .foregroundStyle(Look.accent)
                            .textSelection(.enabled)
                    }
                }
            }
            let phone = result.secure.isEmpty ? result.lan : result.secure
            if !phone.isEmpty {
                Card {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("On your phone, TV or another computer at home").font(.headline)
                        Text(phone)
                            .font(.system(.body, design: .monospaced))
                            .textSelection(.enabled)
                        Text("Or get the EmberStorm app: it finds this Mac by itself.")
                            .font(.callout).foregroundStyle(Look.soft)
                    }
                }
            }
            Text("Your media goes in \(result.library.isEmpty ? "the library folder" : result.library). To open EmberStorm later, open this app again - keep it in the Dock (right-click its icon, Options, Keep in Dock). It runs while you are signed in to this Mac; to have it come back by itself after a restart, turn on automatic login in System Settings, Users & Groups.")
                .font(.callout).foregroundStyle(Look.soft)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            HStack {
                Spacer()
                Button("Open EmberStorm") { setup.open(result) }
                    .buttonStyle(Primary())
                    .keyboardShortcut(.defaultAction)
            }
        }
    }
}

private struct Running: View {
    let setup: Setup
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("EmberStorm is open in your browser").font(.title2.weight(.semibold))
            Text("It keeps running after you close this window. Open this app again whenever you want EmberStorm - or keep it in the Dock.")
                .foregroundStyle(Look.soft)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            HStack {
                Button("Uninstall...") { setup.uninstall() }.buttonStyle(Quiet())
                Button("Update EmberStorm") { setup.update() }.buttonStyle(Quiet())
                Spacer()
                Button("Close") { NSApp.terminate(nil) }
                    .buttonStyle(Primary())
                    .keyboardShortcut(.defaultAction)
            }
        }
    }
}

private struct Removed: View {
    let library: String
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("EmberStorm is removed").font(.title2.weight(.semibold))
            Text("Your media was left exactly where it was:")
                .foregroundStyle(Look.soft)
            Text(library).font(.system(.body, design: .monospaced)).textSelection(.enabled)
            Text("You can move this app to the Bin. Docker Desktop is still in Applications, if nothing else uses it.")
                .foregroundStyle(Look.soft)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            HStack {
                Spacer()
                Button("Close") { NSApp.terminate(nil) }.buttonStyle(Primary()).keyboardShortcut(.defaultAction)
            }
        }
    }
}

private struct Failed: View {
    let setup: Setup
    let message: String
    let retry: Setup.Retry
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(retry == .launch ? "EmberStorm didn't start" : retry == .uninstall ? "EmberStorm wasn't removed" : "Setting up stopped")
                .font(.title2.weight(.semibold))
            Text(message)
                .foregroundStyle(Look.soft)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
            if !setup.installer.log.isEmpty {
                Button("Copy the details") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(setup.installer.log, forType: .string)
                }
                .buttonStyle(Quiet())
            }
            Spacer(minLength: 0)
            HStack {
                Spacer()
                Button("Try again") { setup.retry(retry) }
                    .buttonStyle(Primary())
                    .keyboardShortcut(.defaultAction)
            }
        }
    }
}

// MARK: Pieces

private struct Card<Content: View>: View {
    @ViewBuilder let content: Content
    var body: some View {
        VStack(alignment: .leading, spacing: 12) { content }
            .padding(18)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Look.card, in: RoundedRectangle(cornerRadius: 14))
            .overlay(RoundedRectangle(cornerRadius: 14).stroke(Look.line))
    }
}

private struct Line: View {
    let icon: String
    let text: String
    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Image(systemName: icon).foregroundStyle(Look.accent).frame(width: 20)
            Text(text).fixedSize(horizontal: false, vertical: true)
        }
    }
}

private struct Primary: ButtonStyle {
    @Environment(\.isEnabled) private var enabled
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.headline)
            .padding(.horizontal, 22).padding(.vertical, 11)
            .background(Look.accent.opacity(enabled ? (configuration.isPressed ? 0.75 : 1) : 0.35), in: Capsule())
            .foregroundStyle(.black)
    }
}

private struct Quiet: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .padding(.horizontal, 14).padding(.vertical, 8)
            .background(Color.white.opacity(configuration.isPressed ? 0.14 : 0.07), in: Capsule())
            .foregroundStyle(.white)
    }
}

private func gb(_ bytes: Int64) -> String { "\(max(0, bytes) >> 30) GB" }
