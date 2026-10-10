import AppKit
import Foundation

/// What the window shows, and the work behind each page. Not installed: a
/// welcome, the questions (all of them, before anything is done - as the
/// Windows setup asks them, so nobody comes back to a question), then the
/// setup going and its finish. Installed: EmberStorm started and opened, the
/// Windows desktop icon's job, with Update and Uninstall at hand.
@MainActor
@Observable
final class Setup {
    enum Page {
        case looking, welcome, questions, working, finished(Installer.Result)
        case launching, running, removing, removed(String), failed(String, Retry)
    }
    enum Retry { case setup, launch, uninstall }

    var page: Page = .looking
    let installer = Installer()
    var launchStatus = "Starting EmberStorm..."

    /// Where it is installed, or will be.
    private(set) var dir = Install.defaultFolder
    private(set) var installed = false
    /// The answers.
    var library: URL?
    var keepAwake = true
    /// The Mac's password, asked on the questions page and checked there,
    /// so nothing waits on it later.
    var password = ""
    private(set) var passwordWrong = false
    private(set) var checking = false
    /// Whether anything needs it: installing Docker, or the power settings.
    var needsPassword: Bool { dockerNeeded || (askAwake && keepAwake) }
    private(set) var askAwake = false
    private(set) var laptop = false
    /// Updating rather than installing: nothing is asked.
    private var updating = false

    var libraryShown: URL { library ?? dir.appending(path: "library") }
    var dockerNeeded: Bool { !Docker.installed }
    /// About 30GB for EmberStorm's programs (19GB of images, room to unpack), and Docker Desktop itself.
    var roomNeeded: Int64 { (dockerNeeded ? 35 : 30) << 30 }
    var roomFree: Int64 { ThisMac.freeSpace(at: FileManager.default.homeDirectoryForCurrentUser) }
    var roomShort: Bool { roomFree < roomNeeded }

    func appeared() async {
        #if DEBUG
        // -page questions|working|finished|run: straight to a page, to look at it.
        switch UserDefaults.standard.string(forKey: "page") {
        case "questions": askAwake = true; page = .questions; return
        case "working": page = .working; return
        case "run": setUp(); return  // with -script: a pretend setup, to time the window
        case "finished":
            page = .finished(Installer.Result(url: "http://localhost:8099", setup: "/?setup=x", code: "A1B2-C3D4-E5F6-A7B8-C9D0",
                                              secure: "https://k3x9m2p7qa.home.emberstorm.app:8099", library: dir.appending(path: "library").path))
            return
        default: break
        }
        #endif
        if let found = await Install.find() {
            dir = found
            installed = true
            UserDefaults.standard.set(found.path, forKey: "installDir")
            await launch()
        } else {
            askAwake = !(await ThisMac.neverSleeps())
            laptop = await ThisMac.isLaptop
            page = .welcome
        }
    }

    // MARK: Setting up

    /// Set up, from the questions page: the password checked first.
    func start() {
        guard needsPassword else { return setUp() }
        checking = true
        passwordWrong = false
        Task {
            let ok = await Installer.checkPassword(password)
            checking = false
            guard ok else {
                passwordWrong = true
                return
            }
            setUp()
        }
    }

    func setUp() {
        if dockerNeeded { Installer.quietDockerFirstStart() }
        page = .working
        let hiding = hideDockerWhileSettingUp()
        Task {
            defer { hiding.cancel() }
            var args: [String] = []
            if let library, library.standardizedFileURL != dir.appending(path: "library").standardizedFileURL {
                args += ["--library", library.path]
            }
            do {
                let result = try await installer.run(dir: dir, arguments: args, keepAwake: !updating && askAwake && keepAwake,
                                                     password: !updating && needsPassword ? password : nil)
                installed = true
                UserDefaults.standard.set(dir.path, forKey: "installDir")
                moveToApplications()
                page = .finished(result)
            } catch let e as Installer.Failed {
                page = .failed(e.message, .setup)
            } catch is Installer.Stopped {
                page = installed ? .running : .questions
            } catch {
                page = .failed(error.localizedDescription, .setup)
            }
            updating = false
        }
    }

    /// Docker Desktop's own window, put away while the setup runs: its first
    /// start opened it over everything (the first real run, 2026-10-10), and
    /// it then sat waiting for somebody - though nothing in it is needed, its
    /// terms accepted when it was installed and its engine running anyway.
    private func hideDockerWhileSettingUp() -> Task<Void, Never> {
        Task {
            while !Task.isCancelled {
                for app in NSWorkspace.shared.runningApplications
                where app.bundleIdentifier == "com.docker.docker" && !app.isHidden {
                    app.hide()
                }
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }

    func update() {
        updating = true
        setUp()
    }

    func chooseLibrary() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.prompt = "Keep media here"
        panel.message = "Choose where EmberStorm keeps your music, films, books and photos - an external drive is fine."
        if panel.runModal() == .OK, let url = panel.url { library = url }
    }

    /// Opens what the setup ended with: its first screen, with the setup code
    /// in the address, marked as this computer.
    func open(_ r: Installer.Result) {
        let base = (r.url.isEmpty ? "http://localhost:\(Install.port(in: dir))" : r.url) + r.setup
        let address = base + (base.contains("?") ? "&" : "?") + "here=server"
        if let url = URL(string: address) { NSWorkspace.shared.open(url) }
    }

    // MARK: Installed

    func launch() async {
        page = .launching
        launchStatus = "Starting EmberStorm..."
        let hiding = hideDockerWhileSettingUp()
        defer { hiding.cancel() }
        do {
            try await Install.launch(dir) { [weak self] s in self?.launchStatus = s }
            page = .running
        } catch let e as Installer.Failed {
            page = .failed(e.message, .launch)
        } catch {
            page = .failed(error.localizedDescription, .launch)
        }
    }

    func uninstall() {
        let alert = NSAlert()
        alert.messageText = "Remove EmberStorm from this Mac?"
        alert.informativeText = "Its accounts, settings and the media servers' data are removed. Your media - music, films, books and photos - stays where it is, and a copy of the accounts is kept in the install folder. Docker Desktop stays; remove it from Applications if nothing else uses it."
        alert.addButton(withTitle: "Remove")
        alert.addButton(withTitle: "Cancel")
        alert.buttons.first?.hasDestructiveAction = true
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let library = Install.setting("SOUNDSTORM_LIBRARY_PATH", in: dir).map { URL(fileURLWithPath: $0, relativeTo: dir).standardizedFileURL.path }
            ?? dir.appending(path: "library").path
        page = .removing
        Task {
            do {
                _ = try await installer.run(dir: dir, arguments: ["--uninstall"], keepAwake: false)
                installed = false
                UserDefaults.standard.removeObject(forKey: "installDir")
                page = .removed(library)
            } catch let e as Installer.Failed {
                page = .failed(e.message, .uninstall)
            } catch {
                page = .failed(error.localizedDescription, .uninstall)
            }
        }
    }

    func retry(_ what: Retry) {
        switch what {
        case .setup: setUp()
        case .launch: Task { await launch() }
        case .uninstall: uninstall()
        }
    }

    /// Run from Downloads: put in Applications, where it is found again and
    /// can be kept in the Dock. Quietly left where it is when it cannot be.
    private func moveToApplications() {
        let here = Bundle.main.bundleURL
        guard !here.path.hasPrefix("/Applications/"), !here.path.contains("/Applications/"),
              !here.path.contains("/DerivedData/") else { return }
        for folder in [URL(fileURLWithPath: "/Applications"),
                       FileManager.default.homeDirectoryForCurrentUser.appending(path: "Applications")] {
            let dest = folder.appending(path: here.lastPathComponent)
            try? FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
            if FileManager.default.fileExists(atPath: dest.path) { try? FileManager.default.removeItem(at: dest) }
            if (try? FileManager.default.copyItem(at: here, to: dest)) != nil {
                try? FileManager.default.trashItem(at: here, resultingItemURL: nil)
                return
            }
        }
    }
}
