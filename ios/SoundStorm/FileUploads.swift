import Foundation
import PhotosUI
import UIKit
import UniformTypeIdentifiers
import WebKit

/// The web view's session cookie, for one server only. The web view keeps its
/// own cookies apart from URLSession's, so a request the app makes for the
/// page (photo backup, files added) copies it over.
enum WebCookies {
    /// A web view never shown: the store is read only once one exists in the
    /// process (at launch, or in a background run, which has no page).
    private static var loader: WKWebView?

    /// Sets the Cookie header for the request's server. No cookies at all is
    /// "not known yet" (thrown as a cancellation); none for this server is
    /// signed out (`false`).
    static func apply(to request: inout URLRequest) async throws -> Bool {
        guard let url = request.url, let host = url.host()?.lowercased() else { return false }
        if loader == nil { loader = WKWebView(frame: .zero) }
        var all = await WKWebsiteDataStore.default().httpCookieStore.allCookies()
        if all.isEmpty {
            try await Task.sleep(for: .seconds(2))
            all = await WKWebsiteDataStore.default().httpCookieStore.allCookies()
        }
        guard !all.isEmpty else { throw CancellationError() }
        let now = Date()
        let mine = all.filter { c in
            let domain = c.domain.lowercased()
            let matches = domain.hasPrefix(".") ? (host == String(domain.dropFirst()) || host.hasSuffix(domain)) : host == domain
            return matches && url.path().hasPrefix(c.path) && (!c.isSecure || url.scheme == "https")
                && (c.expiresDate.map { $0 > now } ?? true)
        }
        guard !mine.isEmpty else { return false }
        for (field, value) in HTTPCookie.requestHeaderFields(with: mine) { request.setValue(value, forHTTPHeaderField: field) }
        return true
    }
}

/// Files added with Add media, in the iPhone app (the owner's choice: the app
/// picks, so it can send after it is left). An iPhone's web view keeps the
/// files its own picker gives to itself, so the page asks the app to pick
/// (`soundstormApp.pickFiles`): the app copies what was picked into its own
/// storage, hands the page each file's name, size and an id - the page plans
/// as it always does, reading the few megabytes it needs through the app
/// (`readFile`) - and then hands the plan back (`uploads('add')`), which the
/// app sends in a background URLSession, going on with the app put away or
/// the phone locked. Status is the Android app's (`Uploads.kt`), so the
/// page's progress, chip and Stop need no iPhone code.
final class FileUploads: NSObject {
    static let shared = FileUploads()

    struct Picked: Codable {
        let id: String
        let name: String
        let size: Int64
        let modified: Double // ms
        let file: String
    }

    /// One file to send, and how it went: waiting, done, skipped, failed or
    /// stopped - the Android app's states.
    struct Job: Codable {
        let id: String
        let name: String
        let size: Int64
        let path: String
        let kind: String
        let group: String
        let conflict: String
        let `as`: String
        let taken: Int64
        var state: String
        var dest: String?
        var error: String?
        let file: String
    }

    private struct Queue: Codable {
        var server = ""
        var jobs: [Job] = []
        var stop = false
        var seen = true
    }

    private var picked: [String: Picked] = [:]
    private var queue = Queue()
    private var problem = ""
    private var sending: (name: String, sent: Int64, size: Int64)?
    private var picker: Picker?

    private override init() {
        super.init()
        queue = (try? JSONDecoder().decode(Queue.self, from: Data(contentsOf: Self.queueFile))) ?? Queue()
        NotificationCenter.default.addObserver(forName: UIDevice.batteryStateDidChangeNotification, object: nil, queue: .main) { _ in
            Task { @MainActor in FileUploads.shared.resume() }
        }
    }

    // MARK: Where things are kept

    static var folder: URL {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appending(path: "uploads")
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    private static var queueFile: URL { folder.appending(path: "queue.json") }

    private func save() {
        try? JSONEncoder().encode(queue).write(to: Self.queueFile, options: .atomic)
    }

    // MARK: Picking

    /// Asks Photos or Files, copies what was chosen into the app's storage,
    /// and gives back each file's id, name, size and date for the page.
    func pick(over host: UIViewController, done: @escaping ([Picked]) -> Void) {
        #if DEBUG
        // -pickTest YES: a generated photo, as if picked - Photos and Files
        // run outside the app, where a UI test cannot reach.
        if UserDefaults.standard.bool(forKey: "pickTest") {
            let image = UIGraphicsImageRenderer(size: CGSize(width: 64, height: 48)).jpegData(withCompressionQuality: 0.9) { ctx in
                UIColor(hue: .random(in: 0...1), saturation: 0.6, brightness: 0.8, alpha: 1).setFill()
                ctx.fill(CGRect(x: 0, y: 0, width: 64, height: 48))
            }
            let source = FileManager.default.temporaryDirectory.appending(path: "Test Photo.jpg")
            try? image.write(to: source)
            let id = UUID().uuidString.lowercased()
            let dest = Self.folder.appending(path: id + "-Test Photo.jpg")
            try? FileManager.default.moveItem(at: source, to: dest)
            let file = Picked(id: id, name: "Test Photo \(Int(Date().timeIntervalSince1970)).jpg", size: Int64(image.count),
                              modified: Date().timeIntervalSince1970 * 1000, file: dest.path)
            picked[id] = file
            done([file])
            return
        }
        #endif
        let picker = Picker { [weak self] files in
            for f in files { self?.picked[f.id] = f }
            self?.picker = nil
            done(files)
        }
        self.picker = picker
        picker.start(over: host)
    }

    /// A piece of a picked file, for the page: its sample for spotting copies,
    /// its ends for naming a different one, a zip's table of contents, an
    /// import's piece. At most 16MB at once.
    func read(_ id: String, from: Int64, to: Int64) -> Data? {
        guard let p = picked[id], to >= from, to - from <= 16 << 20,
              let handle = FileHandle(forReadingAtPath: p.file) else { return nil }
        defer { try? handle.close() }
        try? handle.seek(toOffset: UInt64(max(0, from)))
        return try? handle.read(upToCount: Int(to - from)) ?? Data()
    }

    // MARK: The page's commands (the Android app's)

    private var wifiOnly: Bool { UserDefaults.standard.bool(forKey: "uploads.wifiOnly") }
    private var charging: Bool { UserDefaults.standard.bool(forKey: "uploads.charging") }

    func handle(_ command: String, data: [String: Any], pageOrigin: URL?) async -> [String: Any]? {
        switch command {
        case "add":
            var answer = add(server: data["server"] as? String ?? "", jobs: data["jobs"] as? [[String: Any]] ?? [], pageOrigin: pageOrigin)
            answer["ev"] = "added"
            return answer
        case "stop":
            await stop()
        case "set":
            if let v = data["wifiOnly"] as? Bool { UserDefaults.standard.set(v, forKey: "uploads.wifiOnly") }
            if let v = data["charging"] as? Bool { UserDefaults.standard.set(v, forKey: "uploads.charging") }
            resume()
        case "seen":
            queue.seen = true
            save()
            return nil
        default:
            break
        }
        var st = status()
        st["ev"] = "status"
        return st
    }

    /// The page's plan: each job names a picked file by its id. Any not
    /// picked here - not from the app's picker - is reported, and nothing is
    /// queued (the page sends them itself, as before).
    private func add(server: String, jobs: [[String: Any]], pageOrigin: URL?) -> [String: Any] {
        guard let origin = pageOrigin?.absoluteString, server == origin || server + "/" == origin || server == origin + "/" else {
            return ["added": false, "why": "server"]
        }
        var found: [Job] = []
        for j in jobs {
            guard let id = j["id"] as? String, let p = picked[id] else { return ["added": false, "missing": jobs.count] }
            found.append(Job(id: id, name: p.name, size: p.size, path: j["path"] as? String ?? p.name,
                             kind: j["kind"] as? String ?? "", group: j["group"] as? String ?? "",
                             conflict: j["conflict"] as? String ?? "", as: j["as"] as? String ?? "",
                             taken: (j["taken"] as? NSNumber)?.int64Value ?? 0, state: "waiting", file: p.file))
        }
        // A batch that has finished is replaced; one still going is added to.
        let keep = queue.jobs.filter { $0.state == "waiting" }
        queue = Queue(server: server, jobs: keep + found, stop: false, seen: false)
        problem = ""
        save()
        resume()
        return ["added": true, "count": found.count]
    }

    private func stop() async {
        queue.stop = true
        for i in queue.jobs.indices where queue.jobs[i].state == "waiting" {
            queue.jobs[i].state = "stopped"
            try? FileManager.default.removeItem(atPath: queue.jobs[i].file)
        }
        save()
        for session in sessions {
            for task in await session.allTasks { task.cancel() }
        }
        inFlight.removeAll()
    }

    func status() -> [String: Any] {
        let all = queue.jobs
        let waiting = all.filter { $0.state == "waiting" }.count
        return [
            "total": all.count, "done": all.count - waiting, "waiting": waiting,
            "running": !inFlight.isEmpty && waiting > 0,
            "current": sending?.name ?? "", "currentSent": sending?.sent ?? 0, "currentSize": sending?.size ?? 0,
            "totalBytes": all.reduce(0) { $0 + max(0, $1.size) },
            "doneBytes": all.filter { $0.state != "waiting" }.reduce(0) { $0 + max(0, $1.size) },
            "seen": queue.seen, "problem": problem,
            "options": ["wifiOnly": wifiOnly, "charging": charging],
            "jobs": all.map { j -> [String: Any] in
                var o: [String: Any] = ["name": j.name, "size": j.size, "path": j.path, "kind": j.kind,
                                        "group": j.group, "state": j.state]
                if let d = j.dest { o["dest"] = d }
                if let e = j.error { o["error"] = e }
                return o
            },
        ]
    }

    // MARK: Sending

    private lazy var anyNetwork = session("dev.soundstorm.app.uploads", wifi: false)
    private lazy var wifiNetwork = session("dev.soundstorm.app.uploads.wifi", wifi: true)
    private var sessions: [URLSession] { [anyNetwork, wifiNetwork] }
    /// The jobs iOS has now, by id.
    private var inFlight: Set<String> = []
    private var bodies: [Int: Data] = [:]
    var finishedEvents: (() -> Void)?

    static let identifiers = ["dev.soundstorm.app.uploads", "dev.soundstorm.app.uploads.wifi"]

    private func session(_ id: String, wifi: Bool) -> URLSession {
        let config = URLSessionConfiguration.background(withIdentifier: id)
        config.sessionSendsLaunchEvents = true
        config.isDiscretionary = false
        config.httpCookieStorage = nil
        config.httpShouldSetCookies = false
        if wifi {
            config.allowsExpensiveNetworkAccess = false
            config.allowsConstrainedNetworkAccess = false
        }
        return URLSession(configuration: config, delegate: Relay.shared, delegateQueue: .main)
    }

    /// Hands iOS every waiting file it does not have yet - while charging, if
    /// that was asked for (a background session cannot wait for power, so
    /// this waits, and goes when the phone is plugged in).
    func resume() {
        guard !queue.stop, !queue.server.isEmpty, let server = URL(string: queue.server) else { return }
        if charging && ![.charging, .full].contains(UIDevice.current.batteryState) {
            UIDevice.current.isBatteryMonitoringEnabled = true
            return
        }
        Task {
            // What iOS still has from before (a relaunch).
            for session in sessions {
                for task in await session.allTasks where task.state == .running || task.state == .suspended {
                    if let id = task.taskDescription { inFlight.insert(id) }
                }
            }
            for job in queue.jobs where job.state == "waiting" && !inFlight.contains(job.id) {
                guard FileManager.default.fileExists(atPath: job.file) else {
                    finish(job.id, state: "failed", dest: nil, error: "the file can no longer be read")
                    continue
                }
                var request = URLRequest(url: address(server, job))
                request.httpMethod = "PUT"
                request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
                do {
                    guard try await WebCookies.apply(to: &request) else {
                        problem = "Signed out - open SoundStorm and sign in again."
                        return
                    }
                } catch {
                    return // not known yet: the page asking again tries again
                }
                let task = (wifiOnly ? wifiNetwork : anyNetwork).uploadTask(with: request, fromFile: URL(fileURLWithPath: job.file))
                task.taskDescription = job.id
                inFlight.insert(job.id)
                task.resume()
            }
        }
    }

    private func address(_ server: URL, _ job: Job) -> URL {
        var parts = URLComponents(url: server, resolvingAgainstBaseURL: false)!
        parts.path = "/api/upload"
        var q = [URLQueryItem(name: "path", value: job.path), URLQueryItem(name: "kind", value: job.kind)]
        // A photo or video is sorted by when it was taken; the file's own
        // date is what the server falls back on (the page's uploadOne).
        if job.taken > 0, job.kind == "picture" || job.kind == "video" {
            q.append(URLQueryItem(name: "taken", value: String(job.taken)))
        }
        if job.conflict == "keep" || job.conflict == "replace" { q.append(URLQueryItem(name: "conflict", value: job.conflict)) }
        if job.conflict == "keep", !job.as.isEmpty { q.append(URLQueryItem(name: "as", value: job.as)) }
        parts.queryItems = q
        parts.percentEncodedQuery = parts.percentEncodedQuery?
            .replacingOccurrences(of: "+", with: "%2B").replacingOccurrences(of: ";", with: "%3B")
        return parts.url!
    }

    private func finish(_ id: String, state: String, dest: String?, error: String?) {
        inFlight.remove(id)
        guard let i = queue.jobs.firstIndex(where: { $0.id == id && $0.state == "waiting" }) else { return }
        queue.jobs[i].state = state
        queue.jobs[i].dest = dest
        queue.jobs[i].error = error
        try? FileManager.default.removeItem(atPath: queue.jobs[i].file)
        picked[id] = nil
        save()
    }

    fileprivate func progress(_ task: URLSessionTask, sent: Int64, size: Int64) {
        let name = queue.jobs.first { $0.id == task.taskDescription }?.name ?? ""
        sending = (name, sent, size)
    }

    fileprivate func received(_ task: URLSessionTask, _ data: Data) {
        bodies[task.taskIdentifier, default: Data()].append(data)
    }

    fileprivate func completed(_ task: URLSessionTask, error: Error?) {
        let body = bodies.removeValue(forKey: task.taskIdentifier) ?? Data()
        guard let id = task.taskDescription else { return }
        if sending?.name == queue.jobs.first(where: { $0.id == id })?.name { sending = nil }
        let code = (task.response as? HTTPURLResponse)?.statusCode ?? 0
        let json = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
        let message = json?["error"] as? String
        switch code {
        case 200..<300:
            finish(id, state: "done", dest: json?["dest"] as? String ?? "", error: nil)
        case 409:
            // The server declining on purpose: it is already there.
            finish(id, state: "skipped", dest: nil, error: message ?? "already there")
        case 401, 403:
            inFlight.remove(id)
            problem = "Signed out - open SoundStorm and sign in again."
        case 429, 500...:
            // Busy, or the disk full: tried again later.
            inFlight.remove(id)
            problem = message ?? "The server could not take it now - trying again later."
        case 400..<500:
            finish(id, state: "failed", dest: nil, error: message ?? "refused")
        default:
            // The connection dropped (or Stop): this file again, later.
            inFlight.remove(id)
            if !queue.stop, error != nil { problem = "Could not reach the server - trying again when it can." }
        }
    }
}

/// The sessions' delegate, on the main queue, handing everything to the
/// queue's owner.
private final class Relay: NSObject, URLSessionDataDelegate {
    static let shared = Relay()

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, didSendBodyData bytesSent: Int64,
                                totalBytesSent: Int64, totalBytesExpectedToSend: Int64) {
        MainActor.assumeIsolated { FileUploads.shared.progress(task, sent: totalBytesSent, size: totalBytesExpectedToSend) }
    }

    nonisolated func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        MainActor.assumeIsolated { FileUploads.shared.received(dataTask, data) }
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                                newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        completionHandler(nil) // the cookie goes only to the server it was made for
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        MainActor.assumeIsolated { FileUploads.shared.completed(task, error: error) }
    }

    nonisolated func urlSessionDidFinishEvents(forBackgroundURLSession session: URLSession) {
        MainActor.assumeIsolated {
            FileUploads.shared.finishedEvents?()
            FileUploads.shared.finishedEvents = nil
        }
    }
}

/// Photos or Files, then each chosen file copied into the app's storage.
private final class Picker: NSObject, PHPickerViewControllerDelegate, UIDocumentPickerDelegate {
    private let done: ([FileUploads.Picked]) -> Void

    init(done: @escaping ([FileUploads.Picked]) -> Void) { self.done = done }

    func start(over host: UIViewController) {
        let sheet = UIAlertController(title: "Add media", message: nil, preferredStyle: .actionSheet)
        sheet.addAction(UIAlertAction(title: "Photos and videos", style: .default) { _ in
            var config = PHPickerConfiguration(photoLibrary: .shared())
            config.selectionLimit = 0
            config.preferredAssetRepresentationMode = .current // the original, not a converted copy
            let picker = PHPickerViewController(configuration: config)
            picker.delegate = self
            host.present(picker, animated: true)
        })
        sheet.addAction(UIAlertAction(title: "Files", style: .default) { _ in
            let picker = UIDocumentPickerViewController(forOpeningContentTypes: [.item], asCopy: true)
            picker.allowsMultipleSelection = true
            picker.delegate = self
            host.present(picker, animated: true)
        })
        sheet.addAction(UIAlertAction(title: "Cancel", style: .cancel) { [weak self] _ in self?.done([]) })
        sheet.popoverPresentationController?.sourceView = host.view
        sheet.popoverPresentationController?.sourceRect = CGRect(x: host.view.bounds.midX, y: host.view.bounds.maxY - 80, width: 1, height: 1)
        host.present(sheet, animated: true)
    }

    /// A file copied (moved, from a picker's own copy) into the app's storage.
    private static func keep(_ url: URL, name: String, modified: Date?) -> FileUploads.Picked? {
        let id = UUID().uuidString.lowercased()
        let dest = FileUploads.folder.appending(path: id + "-" + name.replacingOccurrences(of: "/", with: "-"))
        do {
            try FileManager.default.copyItem(at: url, to: dest)
        } catch {
            return nil
        }
        let attrs = try? FileManager.default.attributesOfItem(atPath: dest.path)
        let size = (attrs?[.size] as? NSNumber)?.int64Value ?? 0
        let when = modified ?? (attrs?[.modificationDate] as? Date) ?? Date()
        return FileUploads.Picked(id: id, name: name, size: size, modified: when.timeIntervalSince1970 * 1000, file: dest.path)
    }

    nonisolated func picker(_ picker: PHPickerViewController, didFinishPicking results: [PHPickerResult]) {
        nonisolated(unsafe) let results = results
        MainActor.assumeIsolated {
            picker.dismiss(animated: true)
            Task { done(await Self.copy(results)) }
        }
    }

    private static func copy(_ results: [PHPickerResult]) async -> [FileUploads.Picked] {
        var out: [FileUploads.Picked] = []
        for result in results {
            let provider = result.itemProvider
            let type = provider.registeredTypeIdentifiers.compactMap(UTType.init).first {
                $0.conforms(to: .movie) || $0.conforms(to: .image)
            }
            guard let type else { continue }
            let kept: FileUploads.Picked? = await withCheckedContinuation { continuation in
                _ = provider.loadFileRepresentation(for: type) { url, _, _ in
                    // The provider's copy lasts only for this call: copied now.
                    guard let url else { return continuation.resume(returning: nil) }
                    let base = provider.suggestedName ?? url.deletingPathExtension().lastPathComponent
                    let name = base + "." + (url.pathExtension.isEmpty ? (type.preferredFilenameExtension ?? "bin") : url.pathExtension)
                    continuation.resume(returning: keep(url, name: name, modified: nil))
                }
            }
            if let kept { out.append(kept) }
        }
        return out
    }

    nonisolated func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        MainActor.assumeIsolated {
            done(urls.compactMap { url in
                let modified = (try? url.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate
                defer { try? FileManager.default.removeItem(at: url) }
                return Self.keep(url, name: url.lastPathComponent, modified: modified)
            })
        }
    }

    nonisolated func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
        MainActor.assumeIsolated { done([]) }
    }
}
