import AppKit
import SwiftUI

/// EmberStorm for the Mac: the setup the first time it is opened, and the way
/// to start EmberStorm every time after - the Windows setup window and its
/// desktop icon in one app, so nobody types a command into Terminal.
@main
struct EmberStormApp: App {
    @NSApplicationDelegateAdaptor private var delegate: AppDelegate
    @State private var setup = Setup()

    var body: some Scene {
        Window("EmberStorm", id: "main") {
            SetupView(setup: setup)
                .frame(width: 620)
                .onAppear { delegate.setup = setup }
                .preferredColorScheme(.dark)
        }
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)
        .commands {
            CommandGroup(replacing: .newItem) {}
            CommandGroup(after: .appInfo) {
                Button("Update EmberStorm...") { Setup.showWindow(); setup.update() }
                    .disabled(!setup.installed || setup.installer.running)
                Button("Uninstall EmberStorm...") { Setup.showWindow(); setup.uninstall() }
                    .disabled(!setup.installed || setup.installer.running)
            }
        }
        // Once installed, a cloud in the menu bar: opening the app opens the
        // browser and no window covers it, so this is where Update and
        // Uninstall are found, as Docker's and Plex's are.
        MenuBarExtra("EmberStorm", systemImage: "cloud.bolt.fill",
                     isInserted: Binding(get: { setup.installed }, set: { _ in })) {
            Button("Open EmberStorm") { setup.openAgain() }
            Divider()
            Button("Update EmberStorm...") { Setup.showWindow(); setup.update() }
                .disabled(setup.installer.running)
            Button("Uninstall EmberStorm...") { Setup.showWindow(); setup.uninstall() }
                .disabled(setup.installer.running)
            Divider()
            Button("Quit (EmberStorm keeps running)") { NSApp.terminate(nil) }
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    var setup: Setup?

    /// Installed, the app stays for its menu bar cloud when the window goes;
    /// setting up, closing the window ends it as before.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        !(MainActor.assumeIsolated { setup?.installed } ?? false)
    }

    /// The app opened again (the Dock, Applications): the browser, as the
    /// first opening did.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        guard let setup = MainActor.assumeIsolated({ self.setup }), MainActor.assumeIsolated({ setup.installed }) else { return true }
        MainActor.assumeIsolated { setup.openAgain() }
        return false
    }

    #if DEBUG
    /// -snapshot <png> [-snapshotAfter <s>]: the window drawn to a picture,
    /// for checking its pages where nothing may capture the screen.
    func applicationDidFinishLaunching(_ notification: Notification) {
        guard let path = UserDefaults.standard.string(forKey: "snapshot") else { return }
        let after = UserDefaults.standard.double(forKey: "snapshotAfter")
        DispatchQueue.main.asyncAfter(deadline: .now() + (after > 0 ? after : 3)) {
            guard let view = NSApp.windows.first(where: \.isVisible)?.contentView,
                  let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return }
            view.cacheDisplay(in: view.bounds, to: rep)
            try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
        }
    }
    #endif

    /// Quitting part way through asks first, and stops what was started.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let setup = MainActor.assumeIsolated({ self.setup }), MainActor.assumeIsolated({ setup.installer.running }) else {
            return .terminateNow
        }
        let alert = NSAlert()
        alert.messageText = "Stop setting up EmberStorm?"
        alert.informativeText = "What has been downloaded is kept; open EmberStorm again to carry on."
        alert.addButton(withTitle: "Keep going")
        alert.addButton(withTitle: "Stop")
        guard alert.runModal() == .alertSecondButtonReturn else { return .terminateCancel }
        MainActor.assumeIsolated { setup.installer.stop() }
        return .terminateNow
    }
}
