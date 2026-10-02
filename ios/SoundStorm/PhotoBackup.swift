import BackgroundTasks
import Foundation
import Network
import Photos
import UIKit
import WebKit

/// Backing up this iPhone's photos and videos to the person's own folder on
/// their server (pictures/Personal/<name>/<year>/<month>/), as the Android
/// app does (android/ PhotoBackup.kt); the server's half is
/// httpapi/personalphotos.go, and the page's Settings drives it through
/// window.soundstormApp.backup, answered through window.__soundstormBackup -
/// the same messages and status as Android, so the page has no iPhone code.
///
/// iOS decides when an app may work in the background, so it runs: while the
/// app is open, when a photo is added while it is open, and as a background
/// processing task iOS starts when it chooses - usually charging, on Wi-Fi,
/// overnight. Each file goes up in a background URLSession, so one already
/// sending finishes even if the app is put away. Newest first.
///
/// What was sent is remembered on the phone (each photo's local id), and the
/// server is asked first which it already has, so reinstalling the app or a
/// new phone sends nothing twice. It signs in with the web view's own cookie.
final class PhotoBackup: NSObject {
    static let shared = PhotoBackup()
    static let taskID = "dev.soundstorm.app.photobackup"

    private let defaults = UserDefaults.standard
    private let network = NWPathMonitor()
    private var onWiFi = true
    private var run: Task<Void, Never>?
    private var watching = false
    private var changed: Task<Void, Never>?

    private override init() {
        super.init()
        network.pathUpdateHandler = { @Sendable path in
            // Not "expensive" (cellular, a phone's hotspot) or "constrained"
            // (Low Data Mode): what Wi-Fi only means to a person.
            let wifi = path.status == .satisfied && !path.isExpensive && !path.isConstrained
            Task { @MainActor in PhotoBackup.shared.onWiFi = wifi }
        }
        network.start(queue: DispatchQueue(label: "photo-backup-network"))
        UIDevice.current.isBatteryMonitoringEnabled = true
    }

    // MARK: Settings

    private func bool(_ key: String, _ fallback: Bool) -> Bool {
        defaults.object(forKey: "backup." + key) as? Bool ?? fallback
    }
    private func set(_ key: String, _ value: Any?) { defaults.set(value, forKey: "backup." + key) }

    var enabled: Bool { bool("enabled", false) }
    private var wifiOnly: Bool { bool("wifiOnly", true) }
    private var videos: Bool { bool("videos", true) }
    private var charging: Bool { bool("charging", false) }

    var permitted: Bool {
        [.authorized, .limited].contains(PHPhotoLibrary.authorizationStatus(for: .readWrite))
    }

    /// The settings and how it is going, for the page's Settings.
    func status() -> [String: Any] {
        [
            "enabled": enabled,
            "decided": defaults.object(forKey: "backup.enabled") != nil,
            "wifiOnly": wifiOnly, "videos": videos, "charging": charging,
            "permission": permitted,
            "done": defaults.integer(forKey: "backup.done"),
            "total": defaults.integer(forKey: "backup.total"),
            "running": run != nil,
            "problem": defaults.string(forKey: "backup.problem") ?? "",
            "sendingSize": defaults.integer(forKey: "backup.sendingSize"),
            "sendingSent": defaults.integer(forKey: "backup.sendingSent"),
            "sendingVideo": bool("sendingVideo", false),
        ]
    }

    /// The page's own address, when it is a secure one: where to send. The
    /// address typed is often plain http, and the page's move to the secure
    /// name is not saved; over plain http every photo, and the cookie, would
    /// cross the Wi-Fi readable (as on Android).
    /// The address the app was opened with is kept too, the last resort.
    func rememberServer(_ url: URL, typed: URL) {
        if url.scheme == "https", let origin = Self.origin(url) { set("server", origin.absoluteString) }
        if let origin = Self.origin(typed) { set("typed", origin.absoluteString) }
    }

    private static func origin(_ url: URL) -> URL? {
        guard var parts = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return nil }
        parts.path = ""
        parts.query = nil
        parts.fragment = nil
        return parts.url
    }

    /// What the page asked: "status", or "set" with on or off and options -
    /// asking for the photo library first when it is turned on.
    func handle(_ command: String, options: [String: Any]) async {
        guard command == "set" else { return }
        var options = options
        if options["enabled"] as? Bool == true && !permitted {
            let answer = await PHPhotoLibrary.requestAuthorization(for: .readWrite)
            // Refused, backup stays off and the page says why.
            if ![.authorized, .limited].contains(answer) { options["enabled"] = false }
        }
        for key in ["enabled", "wifiOnly", "videos", "charging"] {
            if let v = options[key] as? Bool { set(key, v) }
        }
        // Whose backup it is: sent with every photo, so the server can refuse
        // one meant for another account when somebody else signs in here.
        if let account = options["account"] as? String { set("account", account) }
        set("problem", "")
        if enabled {
            start()
        } else {
            run?.cancel()
        }
    }

    // MARK: When it runs

    /// At launch: the background task, and watching for new photos.
    func launched() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.taskID, using: nil) { task in
            nonisolated(unsafe) let task = task
            Task { @MainActor in PhotoBackup.shared.runInBackground(task) }
        }
        NotificationCenter.default.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { _ in
            Task { @MainActor in PhotoBackup.shared.start() }
        }
        if enabled { start() }
    }

    /// Starts a run, unless one is going or backup is off.
    func start() {
        guard enabled, permitted, run == nil else { return }
        if !watching {
            PHPhotoLibrary.shared().register(self)
            watching = true
        }
        run = Task {
            await backUp()
            run = nil
            schedule()
        }
    }

    private func runInBackground(_ task: BGTask) {
        task.expirationHandler = {
            Task { @MainActor in PhotoBackup.shared.run?.cancel() }
        }
        start()
        Task {
            await run?.value
            task.setTaskCompleted(success: true)
        }
    }

    /// Asks iOS for the next background run: soon while there is more to
    /// send, in a few hours otherwise, for photos taken meanwhile.
    private func schedule() {
        guard enabled else {
            BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.taskID)
            return
        }
        let request = BGProcessingTaskRequest(identifier: Self.taskID)
        request.requiresNetworkConnectivity = true
        request.requiresExternalPower = charging
        let more = defaults.integer(forKey: "backup.done") < defaults.integer(forKey: "backup.total")
        request.earliestBeginDate = Date(timeIntervalSinceNow: more ? 15 * 60 : 6 * 3600)
        try? BGTaskScheduler.shared.submit(request)
    }

    /// Whether a file may go now: Wi-Fi and charging, if asked for.
    private var mayRun: Bool {
        if wifiOnly && !onWiFi { return false }
        if charging && ![.charging, .full].contains(UIDevice.current.batteryState) { return false }
        return true
    }

    // MARK: Sending

    struct Refused: Error { let code: Int; let message: String }

    /// One file to send: a photo, a video, or a Live Photo's moving part -
    /// which is kept track of apart from its still (`<id>#live`), so a phone
    /// whose stills went up before clips were sent still sends the clips.
    private struct Item {
        let key: String
        let assetID: String
        let resource: PHAssetResource
        let name: String
        let taken: Int64
        let video: Bool
    }

    private static func isLive(_ asset: PHAsset) -> Bool { asset.mediaSubtypes.contains(.photoLive) }
    private static func liveKey(_ asset: PHAsset) -> String { asset.localIdentifier + "#live" }

    /// Everything of this photo or video is on the server.
    private static func complete(_ asset: PHAsset, _ sent: Set<String>) -> Bool {
        sent.contains(asset.localIdentifier) && (!isLive(asset) || sent.contains(liveKey(asset)))
    }

    private func backUp() async {
        let servers = candidates()
        guard var server = servers.first else { return }
        let roll = cameraRoll()
        var sent = loadSent()
        // A Live Photo counts as one, still and clip together.
        let waiting = roll.filter { !Self.complete($0, sent) }
        var done = roll.count - waiting.count
        set("done", done)
        set("total", roll.count)
        set("problem", "")
        defer {
            set("sendingSize", 0)
            set("sendingSent", 0)
        }
        do {
            var start = 0
            while start < waiting.count {
                let assets = Array(waiting[start..<min(start + 60, waiting.count)])
                let batch = assets.flatMap(items).filter { !sent.contains($0.key) }
                start += 60
                // Counted as each photo is wholly there.
                let finished = { (key: String) in
                    sent.insert(key)
                    if let asset = assets.first(where: { key.hasPrefix($0.localIdentifier) }), Self.complete(asset, sent) {
                        done += 1
                        self.set("done", done)
                    }
                }
                guard !Task.isCancelled, mayRun else { return }
                // The first address that answers, for the rest of this run.
                var have: [Bool]?
                var lastError: Error = URLError(.cannotConnectToHost)
                for candidate in servers.drop(while: { $0 != server }) {
                    do {
                        have = try await check(candidate, batch)
                        server = candidate
                        break
                    } catch let e as Refused {
                        throw e
                    } catch {
                        lastError = error
                    }
                }
                guard let have else { throw lastError }
                let already = zip(batch, have).filter { $0.1 }.map { $0.0.key }
                markSent(already)
                already.forEach(finished)
                for (item, had) in zip(batch, have) where !had {
                    guard !Task.isCancelled, mayRun else { return }
                    try await send(item, to: server)
                    markSent([item.key])
                    finished(item.key)
                }
            }
        } catch let e as Refused {
            // Said plainly in Settings. A full photo space or disk, or a
            // sign-in that has ended, will not mend itself in a minute.
            let problem = switch e.code {
                case 401: "Sign in to SoundStorm again to carry on backing up."
                case 403: e.message.isEmpty ? "This account does not have Pictures." : e.message
                case 507: e.message.isEmpty ? "There is no room left for photos." : e.message
                default: e.message.isEmpty ? "The server refused a photo." : e.message
            }
            set("problem", problem)
        } catch is CancellationError {
            // Put away or switched off; the next run carries on.
        } catch {
            if !Task.isCancelled { set("problem", "Could not reach the server; it will try again.") }
        }
    }

    /// The addresses to try, best first: the page's secure one, its away
    /// twin (a home name does not answer away from home), the typed one.
    private func candidates() -> [URL] {
        var out: [URL] = []
        if let s = defaults.string(forKey: "backup.server"), let u = URL(string: s) {
            out.append(u)
            if let host = u.host(), host.hasSuffix(".home.soundstorm.dev"),
               var parts = URLComponents(url: u, resolvingAgainstBaseURL: false) {
                parts.host = String(host.dropLast(".home.soundstorm.dev".count)) + ".net.soundstorm.dev"
                if let twin = parts.url { out.append(twin) }
            }
        }
        for typed in [defaults.string(forKey: "backup.typed").flatMap(URL.init(string:)), ServerAddress.saved].compactMap({ $0 })
        where !out.contains(where: { $0.host() == typed.host() && $0.port == typed.port }) {
            out.append(typed)
        }
        return out
    }

    /// Every photo, and video if asked for, newest first.
    private func cameraRoll() -> [PHAsset] {
        let options = PHFetchOptions()
        options.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
        options.predicate = videos
            ? NSPredicate(format: "mediaType == %d || mediaType == %d", PHAssetMediaType.image.rawValue, PHAssetMediaType.video.rawValue)
            : NSPredicate(format: "mediaType == %d", PHAssetMediaType.image.rawValue)
        var out: [PHAsset] = []
        PHAsset.fetchAssets(with: options).enumerateObjects { asset, _, _ in out.append(asset) }
        return out
    }

    /// The files a photo is: the original, its name as the camera gave it -
    /// and for a Live Photo its moving part too, named as the still with .MOV
    /// (IMG_0090.HEIC and IMG_0090.MOV) and taken at the same moment, so it
    /// lands in the same month. The server's photo library joins the two by
    /// the identifier Apple writes inside both files, not by name. The clip
    /// goes even with videos left out: it is part of the photo.
    private func items(_ asset: PHAsset) -> [Item] {
        let resources = PHAssetResource.assetResources(for: asset)
        let video = asset.mediaType == .video
        let wanted: [PHAssetResourceType] = video ? [.video, .fullSizeVideo] : [.photo, .fullSizePhoto]
        guard let resource = resources.first(where: { wanted.contains($0.type) }) else { return [] }
        let when = asset.creationDate ?? asset.modificationDate ?? Date()
        let taken = Int64(when.timeIntervalSince1970 * 1000)
        var out = [Item(key: asset.localIdentifier, assetID: asset.localIdentifier, resource: resource,
                        name: resource.originalFilename, taken: taken, video: video)]
        if Self.isLive(asset), let clip = resources.first(where: { $0.type == .pairedVideo || $0.type == .fullSizePairedVideo }) {
            let base = (resource.originalFilename as NSString).deletingPathExtension
            out.append(Item(key: Self.liveKey(asset), assetID: asset.localIdentifier, resource: clip,
                            name: base + ".MOV", taken: taken, video: true))
        }
        return out
    }

    /// Which of these the server already has, by name and when taken.
    private func check(_ server: URL, _ items: [Item]) async throws -> [Bool] {
        var request = URLRequest(url: address(server, "/api/photos/backup/check", query: account()))
        request.httpMethod = "POST"
        request.timeoutInterval = 30
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        try await signIn(&request)
        // Size 0: asked before any file is read, which for a photo kept in
        // iCloud would mean downloading it to ask. The server then matches
        // name and month, as it does for a same-named file it keeps beside.
        let list = items.map { ["name": $0.name, "taken": $0.taken, "size": 0] as [String: Any] }
        request.httpBody = try JSONSerialization.data(withJSONObject: ["items": list])
        let (data, response) = try await Self.plain.data(for: request)
        let code = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(code) else { throw Refused(code: code, message: Self.errorText(data)) }
        let answer = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        let have = answer?["have"] as? [Bool] ?? []
        return items.indices.map { $0 < have.count ? have[$0] : false }
    }

    /// Sends one photo or video: the original written to a file, then that
    /// file as the whole request body, in the background session.
    private func send(_ item: Item, to server: URL) async throws {
        let file = try await export(item.resource)
        var query = account()
        query.insert(URLQueryItem(name: "taken", value: String(item.taken)), at: 0)
        query.insert(URLQueryItem(name: "name", value: item.name), at: 0)
        var request = URLRequest(url: address(server, "/api/photos/backup", query: query))
        request.httpMethod = "PUT"
        request.setValue(item.video ? "video/*" : "image/*", forHTTPHeaderField: "Content-Type")
        try await signIn(&request)
        let size = (try? FileManager.default.attributesOfItem(atPath: file.path)[.size] as? Int) ?? 0
        set("sendingSize", size)
        set("sendingSent", 0)
        set("sendingVideo", item.video)
        let (code, body) = try await Uploader.shared.upload(request, file: file, key: item.key)
        // 409: the server had it after all, under that name.
        guard (200..<300).contains(code) || code == 409 else { throw Refused(code: code, message: Self.errorText(body)) }
    }

    /// The original resource to a file of its own; from iCloud if that is
    /// where it is.
    private func export(_ resource: PHAssetResource) async throws -> URL {
        let ext = (resource.originalFilename as NSString).pathExtension
        let file = FileManager.default.temporaryDirectory
            .appending(path: "backup-" + UUID().uuidString + (ext.isEmpty ? "" : "." + ext))
        let options = PHAssetResourceRequestOptions()
        options.isNetworkAccessAllowed = true
        try await PHAssetResourceManager.default().writeData(for: resource, toFile: file, options: options)
        return file
    }

    private func account() -> [URLQueryItem] {
        guard let a = defaults.string(forKey: "backup.account"), !a.isEmpty else { return [] }
        return [URLQueryItem(name: "account", value: a)]
    }

    /// A path on the server with a query, "+" and ";" encoded, which
    /// URLComponents leaves and Go reads as a space and refuses.
    private func address(_ server: URL, _ path: String, query: [URLQueryItem]) -> URL {
        var parts = URLComponents(url: server, resolvingAgainstBaseURL: false)!
        parts.path = path
        if !query.isEmpty {
            parts.queryItems = query
            parts.percentEncodedQuery = parts.percentEncodedQuery?
                .replacingOccurrences(of: "+", with: "%2B").replacingOccurrences(of: ";", with: "%3B")
        }
        return parts.url!
    }

    /// The web view's session cookie, for this server only. The web view
    /// keeps its own cookies, apart from URLSession's, so it is copied over.
    private func signIn(_ request: inout URLRequest) async throws {
        guard let url = request.url, let host = url.host()?.lowercased() else { return }
        let all = await WKWebsiteDataStore.default().httpCookieStore.allCookies()
        let now = Date()
        let mine = all.filter { c in
            let domain = c.domain.lowercased()
            let matches = domain.hasPrefix(".") ? (host == String(domain.dropFirst()) || host.hasSuffix(domain)) : host == domain
            return matches && url.path().hasPrefix(c.path) && (!c.isSecure || url.scheme == "https")
                && (c.expiresDate.map { $0 > now } ?? true)
        }
        guard !mine.isEmpty else { throw Refused(code: 401, message: "") }
        for (field, value) in HTTPCookie.requestHeaderFields(with: mine) {
            request.setValue(value, forHTTPHeaderField: field)
        }
    }

    private static func errorText(_ data: Data) -> String {
        ((try? JSONSerialization.jsonObject(with: data)) as? [String: Any])?["error"] as? String ?? ""
    }

    /// For the check: no cookies of its own, no redirects (the cookie goes
    /// only to the server it was made for).
    private static let plain: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.httpCookieStorage = nil
        config.httpShouldSetCookies = false
        return URLSession(configuration: config, delegate: NoRedirects(), delegateQueue: nil)
    }()

    // MARK: What was sent

    private var sentFile: URL {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir.appending(path: "backup-sent.txt")
    }

    private func loadSent() -> Set<String> {
        guard let text = try? String(contentsOf: sentFile, encoding: .utf8) else { return [] }
        return Set(text.split(separator: "\n").map(String.init))
    }

    func markSent(_ keys: [String]) {
        guard !keys.isEmpty, let line = (keys.joined(separator: "\n") + "\n").data(using: .utf8) else { return }
        if let handle = try? FileHandle(forWritingTo: sentFile) {
            _ = try? handle.seekToEnd()
            try? handle.write(contentsOf: line)
            try? handle.close()
        } else {
            try? line.write(to: sentFile)
        }
    }
}

extension PhotoBackup: PHPhotoLibraryChangeObserver {
    /// A photo added while the app is open goes up a little later, once the
    /// camera has finished with it.
    nonisolated func photoLibraryDidChange(_ changeInstance: PHChange) {
        Task { @MainActor in
            let backup = PhotoBackup.shared
            backup.changed?.cancel()
            backup.changed = Task {
                try? await Task.sleep(for: .seconds(30))
                guard !Task.isCancelled else { return }
                backup.start()
            }
        }
    }
}

nonisolated private final class NoRedirects: NSObject, URLSessionTaskDelegate, Sendable {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

/// The background session the files go up in: iOS carries an upload on with
/// the app put away, and wakes the app to say how it ended. An upload that
/// ends after the app was closed is still counted as sent.
nonisolated final class Uploader: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    static let shared = Uploader()
    static let identifier = "dev.soundstorm.app.backup"

    private let lock = NSLock()
    private var waiting: [Int: CheckedContinuation<(Int, Data), Error>] = [:]
    private var bodies: [Int: Data] = [:]
    private var shown = Date.distantPast
    /// iOS's handler for a session's events delivered with the app closed.
    private var finishedEvents: (@Sendable () -> Void)?

    func whenFinished(_ handler: @escaping @Sendable () -> Void) {
        // Uploader.shared made the session again, which its events need.
        lock.withLock { finishedEvents = handler }
    }

    private(set) var session: URLSession!

    private override init() {
        super.init()
        let config = URLSessionConfiguration.background(withIdentifier: Self.identifier)
        config.sessionSendsLaunchEvents = true
        config.isDiscretionary = false
        config.httpCookieStorage = nil
        config.httpShouldSetCookies = false
        session = URLSession(configuration: config, delegate: self, delegateQueue: nil)
    }

    func upload(_ request: URLRequest, file: URL, key: String) async throws -> (Int, Data) {
        let task = session.uploadTask(with: request, fromFile: file)
        // What it is and which file, for an upload that outlives the app.
        task.taskDescription = key + "\n" + file.path
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                lock.withLock { waiting[task.taskIdentifier] = continuation }
                task.resume()
            }
        } onCancel: {
            task.cancel()
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didSendBodyData bytesSent: Int64,
                    totalBytesSent: Int64, totalBytesExpectedToSend: Int64) {
        let now = Date()
        let due = lock.withLock { () -> Bool in
            guard now.timeIntervalSince(shown) > 1 else { return false }
            shown = now
            return true
        }
        if due { UserDefaults.standard.set(Int(totalBytesSent), forKey: "backup.sendingSent") }
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.withLock { bodies[dataTask.taskIdentifier, default: Data()].append(data) }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        completionHandler(nil) // the cookie goes only to the server it was made for
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        let (continuation, body) = lock.withLock {
            (waiting.removeValue(forKey: task.taskIdentifier), bodies.removeValue(forKey: task.taskIdentifier) ?? Data())
        }
        let code = (task.response as? HTTPURLResponse)?.statusCode ?? 0
        let parts = (task.taskDescription ?? "").split(separator: "\n", maxSplits: 1).map(String.init)
        if parts.count == 2 { try? FileManager.default.removeItem(atPath: parts[1]) }
        if let continuation {
            if let error { continuation.resume(throwing: error) } else { continuation.resume(returning: (code, body)) }
        } else if error == nil, (200..<300).contains(code) || code == 409, let key = parts.first {
            // Finished after the app that started it was closed.
            Task { @MainActor in PhotoBackup.shared.markSent([key]) }
        }
    }

    func urlSessionDidFinishEvents(forBackgroundURLSession session: URLSession) {
        let done = lock.withLock { () -> (@Sendable () -> Void)? in
            defer { finishedEvents = nil }
            return finishedEvents
        }
        DispatchQueue.main.async { done?() }
    }
}
