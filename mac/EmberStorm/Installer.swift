import Foundation

/// Runs the Mac and Linux installer (install.sh at the repository's root) and
/// turns what it prints into steps, a share done and a line saying what is
/// happening - the Windows setup window's job, for install.ps1. The script
/// stays the one place that knows how to install; this only asks its
/// questions first, in a window, and shows it going.
///
/// The script is the newest on main, fetched at the commit it is at (so the
/// compose file it downloads matches it), as the Windows setup file does.
/// It runs with no terminal, so it asks nothing: the answers come as
/// settings (EMBERSTORM_KEEP_AWAKE, --library), the Mac's password through
/// SUDO_ASKPASS (a window of macOS's own, `askpass`), and what it ends with
/// is written to EMBERSTORM_RESULT for the finished page.
@MainActor
@Observable
final class Installer {
    enum Stage: Int, CaseIterable {
        case docker, ready, download, start
        var title: String {
            switch self {
            case .docker: "Docker"
            case .ready: "Getting ready"
            case .download: "Downloading EmberStorm"
            case .start: "Starting EmberStorm"
            }
        }
    }

    struct Result {
        var url = "", setup = "", code = "", secure = "", lan = "", library = "", dir = ""
        var upgrade = false
    }

    private(set) var stage: Stage = .docker
    private(set) var status = "Getting the setup..."
    private(set) var fraction = 0.0
    /// What it printed, for Copy the details: the last 5,000 lines.
    var log: String { lines.joined(separator: "\n") }
    /// What Show details shows: the last 300 lines, without Docker's line
    /// for every piece of every download ("402614bd39aa Downloading 5.2MB"),
    /// thousands of which made one text too tall to draw - it showed blank.
    private(set) var shown = ""
    private var lines: [String] = []
    private var shownLines: [String] = []
    private(set) var running = false

    private var process: Process?
    private var resultFile: URL?
    private var pulling: Set<String> = []
    private var pulled: Set<String> = []
    private var installingDocker = false
    private var creep: Task<Void, Never>?

    nonisolated static let repo = "GabrielHollberg/emberstorm"

    /// The steps' shares of the whole, by how long each really takes.
    private func share(_ s: Stage) -> (start: Double, size: Double) {
        let docker = installingDocker ? 0.25 : 0.05
        switch s {
        case .docker: return (0, docker)
        case .ready: return (docker, 0.03)
        case .download: return (docker + 0.03, 0.85 - docker)
        case .start: return (0.88, 0.12)
        }
    }

    /// Runs it: arguments as install.sh takes them. Throws with what the
    /// script said, in its own words, when it stops.
    func run(dir: URL, arguments: [String], keepAwake: Bool, password: String? = nil) async throws -> Result {
        running = true
        defer { running = false; creep?.cancel() }
        lines = []
        shownLines = []
        shown = ""
        pulling = []
        pulled = []
        installingDocker = !Docker.installed
        stage = .docker
        fraction = 0
        status = "Getting the setup..."
        let (script, commit) = try await Self.fetchScript()
        let result = FileManager.default.temporaryDirectory.appending(path: "emberstorm-result-\(UUID().uuidString)")
        resultFile = result
        // The password asked before anything started (the questions page),
        // handed to sudo from a file only this account can read, gone when
        // the run ends. Asked part way through, it waited on whoever had left
        // the Mac (the first real run, 2026-10-10: after Docker's download).
        let askpass = try Self.askpass(password)
        defer { Self.forgetPassword() }

        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = [script.path] + arguments
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = Docker.path
        env["SOUNDSTORM_DIR"] = dir.path
        if let commit { env["SOUNDSTORM_BRANCH"] = commit }
        env["EMBERSTORM_KEEP_AWAKE"] = keepAwake ? "yes" : "no"
        env["EMBERSTORM_RESULT"] = result.path
        env["SUDO_ASKPASS"] = askpass.path
        if let icon = Bundle.main.url(forResource: "AppIcon", withExtension: "icns") { env["EMBERSTORM_ICON"] = icon.path }
        p.environment = env
        p.currentDirectoryURL = FileManager.default.homeDirectoryForCurrentUser
        p.standardInput = FileHandle.nullDevice
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err
        var errorText = ""
        let lines = AsyncStream<(String, Bool)> { continuation in
            for (pipe, isError) in [(out, false), (err, true)] {
                let pending = LineBuffer()
                pipe.fileHandleForReading.readabilityHandler = { handle in
                    let data = handle.availableData
                    if data.isEmpty {
                        handle.readabilityHandler = nil
                        if let rest = pending.rest() { continuation.yield((rest, isError)) }
                        return
                    }
                    for line in pending.add(data) { continuation.yield((line, isError)) }
                }
            }
            p.terminationHandler = { _ in
                // Whatever is still in the pipes comes first.
                DispatchQueue.global().asyncAfter(deadline: .now() + 0.3) { continuation.finish() }
            }
        }
        process = p
        try p.run()
        startCreep()
        for await (line, isError) in lines {
            keep(line)
            // Docker prints its progress on the error stream: both are read.
            if isError { errorText += line + "\n" }
            read(line)
        }
        p.waitUntilExit()
        process = nil
        try? FileManager.default.removeItem(at: script)
        guard p.terminationStatus == 0 else {
            if p.terminationReason == .uncaughtSignal { throw Stopped() }
            // The script's own message (die writes it beside the result);
            // else what it printed, cleaned.
            let said = (try? String(contentsOf: URL(fileURLWithPath: result.path + ".error"), encoding: .utf8))?
                .trimmingCharacters(in: .whitespacesAndNewlines)
            try? FileManager.default.removeItem(atPath: result.path + ".error")
            throw Failed(message: said.map(Self.forTheWindow) ?? Self.cleanError(errorText))
        }
        fraction = 1
        status = "Done."
        return Self.readResult(result)
    }

    private func keep(_ line: String) {
        lines.append(line)
        if lines.count > 5000 { lines.removeFirst(lines.count - 5000) }
        let words = line.split(separator: " ")
        // A download's piece: a short hex id, then what is happening to it.
        if let first = words.first, first.count == 12, first.allSatisfy(\.isHexDigit) { return }
        shownLines.append(line)
        if shownLines.count > 300 { shownLines.removeFirst(shownLines.count - 300) }
        shown = shownLines.joined(separator: "\n")
    }

    struct Failed: Error { let message: String }
    struct Stopped: Error {}

    func stop() {
        guard let p = process, p.isRunning else { return }
        // What the script started (docker compose pull) goes with it.
        let children = Process()
        children.executableURL = URL(fileURLWithPath: "/usr/bin/pkill")
        children.arguments = ["-TERM", "-P", String(p.processIdentifier)]
        try? children.run()
        children.waitUntilExit()
        p.terminate()
    }

    /// One line the script printed: a step, a note, or the download's count.
    private func read(_ raw: String) {
        let line = raw.trimmingCharacters(in: .whitespaces)
        guard !line.isEmpty else { return }
        if line.hasPrefix("==> ") {
            let name = String(line.dropFirst(4))
            switch name {
            case let n where n.hasPrefix("Checking Docker"): enter(.docker)
            case let n where n.hasPrefix("Installing Docker"): installingDocker = true; enter(.docker)
            case let n where n.hasPrefix("Setting up"), let n where n.hasPrefix("Choosing a port"),
                 let n where n.hasPrefix("Copying"), let n where n.hasPrefix("Restoring"): enter(.ready)
            case let n where n.hasPrefix("Checking for newer"), let n where n.hasPrefix("Downloading"): enter(.download)
            case let n where n.hasPrefix("Starting"), let n where n.hasPrefix("Waiting for EmberStorm"),
                 let n where n.hasPrefix("Getting a secure"): enter(.start)
            default: break
            }
            status = name.hasSuffix("...") ? name : name + "..."
            return
        }
        // docker compose pull, without a terminal: "<name> Pulling" as each
        // image starts and "<name> Pulled" as it is in.
        let words = line.split(separator: " ")
        if words.count == 2, stage == .download {
            let name = String(words[0])
            switch words[1] {
            case "Pulling": pulling.insert(name)
            case "Pulled", "Skipped": pulling.insert(name); pulled.insert(name)
            default: break
            }
            if !pulling.isEmpty {
                status = "Downloaded \(pulled.count) of \(max(pulling.count, pulled.count)) parts of EmberStorm"
                let (start, size) = share(.download)
                fraction = max(fraction, start + size * Double(pulled.count) / Double(max(pulling.count, 12)))
            }
            return
        }
        // The script's own sentences (a note, a heartbeat) say what it is doing.
        // Not the script's word about Docker's window: this app puts it away.
        if line.contains("Docker window") || line.contains("Continue without signing in") { return }
        if raw.hasPrefix("    "), !line.hasPrefix("http"), !line.hasPrefix("*") {
            status = line
        }
    }

    private func enter(_ s: Stage) {
        guard s.rawValue >= stage.rawValue else { return }
        stage = s
        fraction = max(fraction, share(s).start)
    }

    /// Where nothing can be counted (Docker's installer, a start), the bar
    /// creeps on within the step, never past it, so it never sits still.
    private func startCreep() {
        creep?.cancel()
        creep = Task {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                let (start, size) = share(stage)
                let end = start + size * 0.95
                if fraction < end { fraction += (end - fraction) * 0.01 }
            }
        }
    }

    // MARK: The script, the password window, what it ended with

    /// The newest install.sh on main, at the commit it is at.
    nonisolated static func fetchScript() async throws -> (URL, String?) {
        #if DEBUG
        // A checkout's own, to try a change before it is pushed.
        if let local = UserDefaults.standard.string(forKey: "script") {
            let copy = FileManager.default.temporaryDirectory.appending(path: "emberstorm-install-\(UUID().uuidString).sh")
            try FileManager.default.copyItem(at: URL(fileURLWithPath: local), to: copy)
            return (copy, UserDefaults.standard.string(forKey: "branch"))
        }
        #endif
        var commit: String?
        var request = URLRequest(url: URL(string: "https://api.github.com/repos/\(repo)/commits/main")!, timeoutInterval: 20)
        request.setValue("application/vnd.github.sha", forHTTPHeaderField: "Accept")
        if let (data, response) = try? await URLSession.shared.data(for: request),
           (response as? HTTPURLResponse)?.statusCode == 200,
           let sha = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines),
           sha.count == 40, sha.allSatisfy(\.isHexDigit) {
            commit = sha
        }
        let url = URL(string: "https://raw.githubusercontent.com/\(repo)/\(commit ?? "main")/install.sh")!
        let (data, response): (Data, URLResponse)
        do {
            (data, response) = try await URLSession.shared.data(from: url)
        } catch {
            throw Failed(message: "EmberStorm's setup could not be downloaded. Check this Mac is connected to the internet, then try again.")
        }
        guard (response as? HTTPURLResponse)?.statusCode == 200, data.starts(with: Array("#!/bin/sh".utf8)) else {
            throw Failed(message: "EmberStorm's setup could not be downloaded (GitHub answered something else). Try again in a minute.")
        }
        let file = FileManager.default.temporaryDirectory.appending(path: "emberstorm-install-\(UUID().uuidString).sh")
        try data.write(to: file)
        return (file, commit)
    }

    /// A program sudo runs for the password, as there is no terminal: a
    /// window of macOS's own, saying why it is asked.
    private static var privateFolder: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appending(path: "EmberStorm")
    }

    private static func forgetPassword() {
        try? FileManager.default.removeItem(at: privateFolder.appending(path: "pw"))
    }

    private static func askpass(_ password: String?) throws -> URL {
        let dir = privateFolder
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: dir.path)
        let file = dir.appending(path: "askpass")
        if let password {
            let pw = dir.appending(path: "pw")
            FileManager.default.createFile(atPath: pw.path, contents: Data((password + "\n").utf8), attributes: [.posixPermissions: 0o600])
            let script = "#!/bin/sh\n# The password asked in EmberStorm's window, for sudo.\nexec /bin/cat '\(pw.path.replacingOccurrences(of: "'", with: "'\\''"))'\n"
            try script.write(to: file, atomically: true, encoding: .utf8)
            try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: file.path)
            return file
        }
        // Nothing asked first (an update, an uninstall): asked if needed, in a
        // window of macOS's own.
        let script = """
        #!/bin/sh
        # Asked by sudo for the Mac's password (EmberStorm's setup has no terminal).
        exec /usr/bin/osascript \\
          -e 'set iconPath to system attribute "EMBERSTORM_ICON"' \\
          -e 'set msg to "EmberStorm needs your Mac password to install Docker Desktop and to keep this Mac awake while plugged in. It is not kept or sent anywhere."' \\
          -e 'try' \\
          -e 'set r to display dialog msg default answer "" with hidden answer with title "EmberStorm" with icon (POSIX file iconPath as alias) buttons {"Cancel", "OK"} default button "OK"' \\
          -e 'on error number n' \\
          -e 'if n is -128 then error number -128' \\
          -e 'set r to display dialog msg default answer "" with hidden answer with title "EmberStorm" buttons {"Cancel", "OK"} default button "OK"' \\
          -e 'end try' \\
          -e 'return text returned of r'
        """
        try script.write(to: file, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: file.path)
        return file
    }

    private static func readResult(_ file: URL) -> Result {
        var r = Result()
        let text = (try? String(contentsOf: file, encoding: .utf8)) ?? ""
        try? FileManager.default.removeItem(at: file)
        for line in text.split(separator: "\n") {
            guard let eq = line.firstIndex(of: "=") else { continue }
            let value = String(line[line.index(after: eq)...])
            switch line[..<eq] {
            case "URL": r.url = value
            case "SETUP": r.setup = value
            case "CODE": r.code = value
            case "SECURE": r.secure = value
            case "LAN": r.lan = value
            case "LIBRARY": r.library = value
            case "DIR": r.dir = value
            case "UPGRADE": r.upgrade = value == "1"
            default: break
            }
        }
        return r
    }

    /// What the script said when it stopped, without its heading, and
    /// without telling somebody to type commands (the window has buttons).
    /// Its message, said for a window with buttons rather than a terminal.
    private static func forTheWindow(_ message: String) -> String {
        message
            .replacingOccurrences(of: "then run this again", with: "then choose Try again")
            .replacingOccurrences(of: "run this again", with: "choose Try again")
    }

    private static func cleanError(_ text: String) -> String {
        var lines = text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        while let first = lines.first, first.trimmingCharacters(in: .whitespaces).isEmpty { lines.removeFirst() }
        // The heading ("EmberStorm could not start."): the window has its own.
        if !lines.isEmpty { lines.removeFirst() }
        let kept = lines.filter { !$0.hasPrefix("  cd ") }
        let message = kept.joined(separator: "\n")
            .replacingOccurrences(of: "then run this again", with: "then choose Try again")
            .replacingOccurrences(of: "run this again", with: "choose Try again")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return message.isEmpty ? "The setup stopped without saying why." : message
    }
}

/// Bytes from a pipe into whole lines; one per pipe, fed only by its handler.
nonisolated final class LineBuffer: @unchecked Sendable {
    private var pending = Data()

    func add(_ data: Data) -> [String] {
        pending.append(data)
        var lines: [String] = []
        while let nl = pending.firstIndex(of: 10) {
            lines.append(String(decoding: pending[pending.startIndex..<nl], as: UTF8.self))
            pending.removeSubrange(pending.startIndex...nl)
        }
        return lines
    }

    func rest() -> String? {
        defer { pending = Data() }
        return pending.isEmpty ? nil : String(decoding: pending, as: UTF8.self)
    }
}

extension Installer {
    /// Whether this is the Mac's password for this account, and the account
    /// may use it to install (an administrator's): sudo asked to check, and
    /// told to forget straight after.
    nonisolated static func checkPassword(_ password: String) async -> Bool {
        await withCheckedContinuation { done in
            let p = Process()
            p.executableURL = URL(fileURLWithPath: "/usr/bin/sudo")
            p.arguments = ["-S", "-k", "-p", "", "-v"]
            let input = Pipe()
            p.standardInput = input
            p.standardOutput = FileHandle.nullDevice
            p.standardError = FileHandle.nullDevice
            p.terminationHandler = { p in
                let ok = p.terminationStatus == 0
                let forget = Process()
                forget.executableURL = URL(fileURLWithPath: "/usr/bin/sudo")
                forget.arguments = ["-k"]
                try? forget.run()
                forget.waitUntilExit()
                done.resume(returning: ok)
            }
            do {
                try p.run()
                input.fileHandleForWriting.write(Data((password + "\n").utf8))
                try? input.fileHandleForWriting.close()
            } catch {
                done.resume(returning: false)
            }
        }
    }

    /// Docker Desktop's sign-in and survey, answered before it first starts
    /// (the Windows setup's DisplayedOnboarding): only when it has no
    /// settings yet. macOS may ask whether EmberStorm may reach Docker's
    /// folder - asked here, while somebody has just clicked; refused, Docker
    /// may show those windows later and the setup carries on.
    static func quietDockerFirstStart() {
        let folder = FileManager.default.homeDirectoryForCurrentUser
            .appending(path: "Library/Group Containers/group.com.docker")
        let file = folder.appending(path: "settings-store.json")
        guard !FileManager.default.fileExists(atPath: file.path) else { return }
        try? FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let settings = #"{"DisplayedOnboarding": true, "OpenUIOnStartupDisabled": true}"#
        try? Data(settings.utf8).write(to: file)
    }
}
