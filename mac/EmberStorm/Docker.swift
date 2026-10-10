import AppKit
import Foundation

/// Docker Desktop, and where EmberStorm is installed - what the app needs to
/// know to set it up, or to start it once it is.
enum Docker {
    /// A Mac app is started with almost no PATH; docker lives in one of these.
    nonisolated static let path = ["/usr/local/bin", "/opt/homebrew/bin", "/Applications/Docker.app/Contents/Resources/bin",
                       "/usr/bin", "/bin", "/usr/sbin", "/sbin"].joined(separator: ":")

    static var installed: Bool { FileManager.default.fileExists(atPath: "/Applications/Docker.app") }

    /// Runs a command, waiting for it, with what it printed.
    @discardableResult
    nonisolated static func run(_ tool: String, _ args: [String], in dir: URL? = nil) async -> (code: Int32, out: String) {
        await withCheckedContinuation { done in
            let p = Process()
            p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
            p.arguments = [tool] + args
            var env = ProcessInfo.processInfo.environment
            env["PATH"] = path
            p.environment = env
            if let dir { p.currentDirectoryURL = dir }
            p.standardInput = FileHandle.nullDevice
            let pipe = Pipe()
            p.standardOutput = pipe
            p.standardError = pipe
            p.terminationHandler = { p in
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                done.resume(returning: (p.terminationStatus, String(decoding: data, as: UTF8.self)))
            }
            do { try p.run() } catch { done.resume(returning: (-1, "")) }
        }
    }

    static func reachable() async -> Bool { await run("docker", ["info"]).code == 0 }

    /// Starts Docker Desktop (in the background, no window) and waits for it,
    /// three minutes at most.
    static func start(status: (String) -> Void) async -> Bool {
        if await reachable() { return true }
        guard installed else { return false }
        status("Starting Docker...")
        _ = await run("open", ["-g", "-a", "Docker"])
        for i in 0..<90 {
            try? await Task.sleep(for: .seconds(2))
            if await reachable() { return true }
            if i == 15 { status("Starting Docker... this takes a minute or two after the Mac starts.") }
        }
        return false
    }
}

/// Where EmberStorm is installed on this Mac, if it is.
enum Install {
    static var defaultFolder: URL { FileManager.default.homeDirectoryForCurrentUser.appending(path: "EmberStorm") }

    /// The folder this app set up or found before, else one of the usual
    /// places holding an install (the one-line installer's ~/soundstorm),
    /// else where Docker says the running one is.
    static func find() async -> URL? {
        if let saved = UserDefaults.standard.string(forKey: "installDir"), isInstall(URL(fileURLWithPath: saved)) {
            return URL(fileURLWithPath: saved)
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        for name in ["EmberStorm", "soundstorm", "SoundStorm"] where isInstall(home.appending(path: name)) {
            return home.appending(path: name)
        }
        if await Docker.reachable() {
            let r = await Docker.run("docker", ["inspect", "-f", "{{index .Config.Labels \"com.docker.compose.project.working_dir\"}}", "soundstorm"])
            let dir = r.out.trimmingCharacters(in: .whitespacesAndNewlines)
            if r.code == 0, !dir.isEmpty, isInstall(URL(fileURLWithPath: dir)) { return URL(fileURLWithPath: dir) }
        }
        return nil
    }

    static func isInstall(_ dir: URL) -> Bool {
        FileManager.default.fileExists(atPath: dir.appending(path: "docker-compose.yml").path)
            && FileManager.default.fileExists(atPath: dir.appending(path: ".env").path)
    }

    /// A setting from the install's .env.
    static func setting(_ key: String, in dir: URL) -> String? {
        guard let text = try? String(contentsOf: dir.appending(path: ".env"), encoding: .utf8) else { return nil }
        for line in text.split(separator: "\n") where line.hasPrefix(key + "=") {
            return String(line.dropFirst(key.count + 1)).trimmingCharacters(in: CharacterSet(charactersIn: "\"' "))
        }
        return nil
    }

    static func port(in dir: URL) -> String {
        let p = setting("SOUNDSTORM_PORT", in: dir) ?? ""
        return !p.isEmpty && p.allSatisfy(\.isNumber) ? p : "8099"
    }

    /// Starts it (Docker first), waits for it to answer, and opens it in the
    /// browser - marked as this computer, so the page offers no "install as
    /// an app" here (`?here=server`, as the Windows desktop icon does).
    static func launch(_ dir: URL, status: (String) -> Void) async throws {
        guard await Docker.start(status: status) else {
            throw Installer.Failed(message: Docker.installed
                ? "Docker Desktop did not start. Open Docker Desktop from Applications; once it says it is running, choose Try again."
                : "Docker Desktop is not on this Mac any more. Choose Set up again to put it back.")
        }
        status("Starting EmberStorm...")
        _ = await Docker.run("docker", ["compose", "-f", "docker-compose.yml", "up", "-d"], in: dir)
        let base = "http://localhost:\(port(in: dir))"
        for i in 0..<90 {
            if await answers(base + "/healthz") {
                NSWorkspace.shared.open(URL(string: base + "/?here=server")!)
                return
            }
            if i == 10 { status("Starting EmberStorm... this takes a minute after Docker starts.") }
            try? await Task.sleep(for: .seconds(2))
        }
        throw Installer.Failed(message: "EmberStorm did not answer. Choose Try again; if it happens again, restart the Mac.")
    }

    nonisolated static func answers(_ address: String) async -> Bool {
        guard let url = URL(string: address) else { return false }
        var request = URLRequest(url: url, timeoutInterval: 4)
        request.cachePolicy = .reloadIgnoringLocalCacheData
        guard let (_, response) = try? await URLSession.shared.data(for: request) else { return false }
        return (response as? HTTPURLResponse)?.statusCode == 200
    }
}

/// What this Mac is like, for the questions asked before setting up.
enum ThisMac {
    /// Whether it is already kept awake while plugged in (pmset's "sleep 0"
    /// on AC Power): then there is nothing to ask.
    static func neverSleeps() async -> Bool {
        let r = await Docker.run("pmset", ["-g", "custom"])
        var ac = false
        for line in r.out.split(separator: "\n") {
            if line.contains("AC Power") { ac = true; continue }
            if line.hasSuffix(":") || line.contains("Battery Power") { ac = false }
            let words = line.split(separator: " ")
            if ac, words.count >= 2, words[0] == "sleep" { return words[1] == "0" }
        }
        return false
    }

    static var isLaptop: Bool {
        get async { await Docker.run("pmset", ["-g", "batt"]).out.contains("InternalBattery") }
    }

    /// Free space where a folder is, in bytes.
    static func freeSpace(at url: URL) -> Int64 {
        var probe = url
        while !FileManager.default.fileExists(atPath: probe.path), probe.pathComponents.count > 1 {
            probe.deleteLastPathComponent()
        }
        let values = try? probe.resourceValues(forKeys: [.volumeAvailableCapacityForImportantUsageKey])
        return values?.volumeAvailableCapacityForImportantUsage ?? 0
    }
}
