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
/// app is open, when a photo is added while it is open, the half minute iOS
/// allows after the phone locks, and as a background processing task iOS
/// starts when it chooses - usually charging, on Wi-Fi, overnight. Newest
/// first. Files are handed to iOS ahead - up to 200, or a gigabyte, waiting
/// at once - in a background URLSession, which goes on sending them with the
/// phone locked and the app suspended or ended; each is counted as sent when
/// iOS says it arrived, whenever that is. (Sending one at a time and waiting
/// for each, backup stopped a few seconds after the screen locked.)
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
            "running": run != nil || Uploader.shared.pending.count > 0,
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
        // Another server than before: its secure address is not this one's.
        // Kept, photos went on going to the old server (and were refused).
        if let origin = Self.origin(typed), origin.absoluteString != defaults.string(forKey: "backup.typed") {
            set("server", nil)
        }
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
        // The phone locking (or the app put away): iOS allows about half a
        // minute more, spent handing it as many files as fit; it sends those
        // with the app suspended.
        NotificationCenter.default.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { _ in
            Task { @MainActor in PhotoBackup.shared.wentToBackground() }
        }
        if enabled { start() }
    }

    private var backgroundTask: UIBackgroundTaskIdentifier = .invalid

    private func wentToBackground() {
        guard enabled, permitted else { return }
        if backgroundTask == .invalid {
            backgroundTask = UIApplication.shared.beginBackgroundTask(withName: "Photo backup") {
                Task { @MainActor in PhotoBackup.shared.endBackgroundTime() }
            }
        }
        start()
    }

    private func endBackgroundTime() {
        // Out of time: what is already with iOS goes on; the run stops here.
        run?.cancel()
        if backgroundTask != .invalid {
            UIApplication.shared.endBackgroundTask(backgroundTask)
            backgroundTask = .invalid
        }
    }

    /// Starts a run, unless one is going or backup is off.
    func start() {
        guard enabled, permitted, run == nil else { return }
        if !watching {
            PHPhotoLibrary.shared().register(self)
            watching = true
        }
        run = Task {
            await Uploader.shared.restored()
            await backUp()
            run = nil
            schedule()
            if backgroundTask != .invalid {
                UIApplication.shared.endBackgroundTask(backgroundTask)
                backgroundTask = .invalid
            }
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

    /// Files waiting with iOS at once, at most: enough to go on for hours
    /// with the phone locked, not so many that their copies fill the phone.
    static let maxQueued = 200
    static let maxQueuedBytes: Int64 = 1 << 30

    /// Stops handing iOS more in this run: a refusal that will not mend by
    /// itself (signed out, no Pictures, no room).
    private var halted = false

    private func backUp() async {
        let servers = candidates()
        guard var server = servers.first else { return }
        let list = sentList
        let roll = cameraRoll()
        var sent = loadSent(list)
        // A Live Photo counts as one, still and clip together.
        let waiting = roll.filter { !Self.complete($0, sent) }
        set("done", roll.count - waiting.count)
        set("total", roll.count)
        set("problem", "")
        halted = false
        do {
            var start = 0
            while start < waiting.count {
                let assets = Array(waiting[start..<min(start + 60, waiting.count)])
                start += 60
                // Nothing to send of something with no file at all (a photo
                // shared to the phone but never downloaded): done, or it
                // would count as waiting for ever.
                var batch: [Item] = []
                for asset in assets {
                    let units = items(asset)
                    if units.isEmpty {
                        markSent([asset.localIdentifier], list)
                        sent.insert(asset.localIdentifier)
                        bumpDone()
                    }
                    batch += units.filter { !sent.contains($0.key) && !Uploader.shared.pending.keys.contains($0.key) }
                }
                guard !batch.isEmpty else { continue }
                guard !Task.isCancelled, mayRun, !halted else { return }
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
                for (item, had) in zip(batch, have) where had {
                    markSent([item.key], list)
                    sent.insert(item.key)
                    if isLast(item, sent: sent) { bumpDone() }
                }
                for (item, had) in zip(batch, have) where !had {
                    // Room with iOS first: topped up as files arrive.
                    while Uploader.shared.pending.count >= Self.maxQueued || Uploader.shared.pending.bytes >= Self.maxQueuedBytes {
                        try await Task.sleep(for: .seconds(3))
                    }
                    guard !Task.isCancelled, mayRun, !halted else { return }
                    let pendingOthers = batch.filter { $0.assetID == item.assetID && $0.key != item.key && !sent.contains($0.key) }
                    try await enqueue(item, to: server, list: list, last: pendingOthers.allSatisfy { Uploader.shared.pending.keys.contains($0.key) })
                }
            }
        } catch let e as Refused {
            stopFor(e)
        } catch is CancellationError {
            // Put away or switched off; what is with iOS goes on, and the next
            // run carries on from there.
        } catch {
            if !Task.isCancelled { set("problem", "Could not reach the server; it will try again.") }
        }
    }

    /// Whether this file completes its photo (a Live Photo's still and clip
    /// both there).
    private func isLast(_ item: Item, sent: Set<String>) -> Bool {
        sent.contains(item.assetID) && (sent.contains(item.assetID + "#live") || !item.key.hasSuffix("#live") && !hasClip(item.assetID))
    }

    private func hasClip(_ id: String) -> Bool {
        PHAsset.fetchAssets(withLocalIdentifiers: [id], options: nil).firstObject.map(Self.isLive) ?? false
    }

    private func bumpDone() { set("done", defaults.integer(forKey: "backup.done") + 1) }

    /// Said plainly in Settings. A full photo space or disk, or a sign-in
    /// that has ended, will not mend itself in a minute: nothing more is
    /// handed to iOS this run.
    private func stopFor(_ e: Refused) {
        halted = true
        let problem = switch e.code {
            case 401: "Sign in to SoundStorm again to carry on backing up."
            case 403: e.message.isEmpty ? "This account does not have Pictures." : e.message
            case 507: e.message.isEmpty ? "There is no room left for photos." : e.message
            default: e.message.isEmpty ? "The server refused a photo." : e.message
        }
        set("problem", problem)
    }

    /// A file iOS says arrived (or the server had already): counted, even
    /// when this is a relaunch after the app was ended.
    func uploaded(_ meta: Uploader.Meta) {
        markSent([meta.key], meta.list)
        if meta.last { bumpDone() }
    }

    /// A file the server refused. Signed out, no Pictures or no room stop the
    /// run; anything else is that one file, named and passed over - one bad
    /// file used to stop backup for good (as Android 0.18 found).
    func refused(_ meta: Uploader.Meta, code: Int, message: String) {
        if [401, 403, 507].contains(code) {
            stopFor(Refused(code: code, message: message))
        } else {
            set("problem", "Couldn't back up \(meta.name)\(message.isEmpty ? "" : ": " + message). The rest carry on.")
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

    /// Hands one photo or video to iOS: the original written to a file of
    /// its own, then that file as the whole request body, in the background
    /// session - not waited for. iOS sends it when it can, the phone locked
    /// or not.
    private func enqueue(_ item: Item, to server: URL, list: String, last: Bool) async throws {
        let file = try await export(item.resource)
        var query = account()
        query.insert(URLQueryItem(name: "taken", value: String(item.taken)), at: 0)
        query.insert(URLQueryItem(name: "name", value: item.name), at: 0)
        var request = URLRequest(url: address(server, "/api/photos/backup", query: query))
        request.httpMethod = "PUT"
        request.setValue(item.video ? "video/*" : "image/*", forHTTPHeaderField: "Content-Type")
        do {
            try await signIn(&request)
        } catch {
            try? FileManager.default.removeItem(at: file)
            throw error
        }
        let size = (try? FileManager.default.attributesOfItem(atPath: file.path)[.size] as? Int64) ?? 0
        Uploader.shared.enqueue(request, file: file, size: size, wifiOnly: wifiOnly,
                                meta: Uploader.Meta(key: item.key, list: list, last: last, name: item.name, video: item.video))
    }

    /// The original resource to a file of its own, from iCloud if that is
    /// where it is - kept with the app's own files rather than in temporary
    /// ones, which iOS may clear while it still has them to send.
    private func export(_ resource: PHAssetResource) async throws -> URL {
        let ext = (resource.originalFilename as NSString).pathExtension
        let file = Uploader.queueFolder
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
        // The web view's cookies are read from its store only once a web view
        // exists in this process: at launch, before the page's own, and in a
        // background run, which has none, the store answered empty and backup
        // said "sign in again". One that is never shown loads it.
        if Self.cookieLoader == nil { Self.cookieLoader = WKWebView(frame: .zero) }
        var all = await WKWebsiteDataStore.default().httpCookieStore.allCookies()
        if all.isEmpty {
            try await Task.sleep(for: .seconds(2))
            all = await WKWebsiteDataStore.default().httpCookieStore.allCookies()
        }
        let now = Date()
        let mine = all.filter { c in
            let domain = c.domain.lowercased()
            let matches = domain.hasPrefix(".") ? (host == String(domain.dropFirst()) || host.hasSuffix(domain)) : host == domain
            return matches && url.path().hasPrefix(c.path) && (!c.isSecure || url.scheme == "https")
                && (c.expiresDate.map { $0 > now } ?? true)
        }
        // None at all: not known yet, rather than signed out - the run ends
        // quietly and the next (the page asking, opening the app) tries again.
        guard !all.isEmpty else { throw CancellationError() }
        guard !mine.isEmpty else { throw Refused(code: 401, message: "") }
        for (field, value) in HTTPCookie.requestHeaderFields(with: mine) {
            request.setValue(value, forHTTPHeaderField: field)
        }
    }

    private static var cookieLoader: WKWebView?

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

    /// What was sent is remembered per account: sent to one person's (or one
    /// server's) folder is not sent to another's. One list for the whole
    /// phone showed a second server or person "all backed up" with nothing
    /// sent. The account id is the server's own random id, so it tells
    /// servers apart too.
    private var sentList: String {
        let account = defaults.string(forKey: "backup.account") ?? ""
        let name = account.isEmpty ? "default" : String(account.filter { $0.isLetter || $0.isNumber }.prefix(40))
        // A phone from before kept one list: it was this account's.
        let old = Self.folder.appending(path: "backup-sent.txt")
        if FileManager.default.fileExists(atPath: old.path) {
            try? FileManager.default.moveItem(at: old, to: Self.sentFile(name))
        }
        return name
    }

    private static var folder: URL {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    private static func sentFile(_ list: String) -> URL { folder.appending(path: "backup-sent-\(list).txt") }

    private func loadSent(_ list: String) -> Set<String> {
        guard let text = try? String(contentsOf: Self.sentFile(list), encoding: .utf8) else { return [] }
        return Set(text.split(separator: "\n").map(String.init))
    }

    func markSent(_ keys: [String], _ list: String) {
        guard !keys.isEmpty, let line = (keys.joined(separator: "\n") + "\n").data(using: .utf8) else { return }
        let file = Self.sentFile(list)
        if let handle = try? FileHandle(forWritingTo: file) {
            _ = try? handle.seekToEnd()
            try? handle.write(contentsOf: line)
            try? handle.close()
        } else {
            try? line.write(to: file)
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

/// The background sessions the files go up in: iOS carries the uploads on
/// with the phone locked and the app suspended or ended, and wakes the app to
/// say how each ended. Two sessions, as a session's network rules are fixed
/// when it is made: one that waits for Wi-Fi (not cellular, a hotspot or Low
/// Data Mode), one that sends on anything.
///
/// What each upload is goes with its task (taskDescription), so a relaunch
/// knows; `pending` is what iOS still has, by file, read back from the
/// sessions at launch so nothing already waiting is handed over again.
nonisolated final class Uploader: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    static let shared = Uploader()
    static let identifier = "dev.soundstorm.app.backup"
    static let wifiIdentifier = "dev.soundstorm.app.backup.wifi"

    /// What an upload is: the sent-list key, which list, whether it completes
    /// its photo, and its name for a message.
    struct Meta: Codable, Sendable {
        let key: String
        let list: String
        let last: Bool
        let name: String
        let video: Bool
        var file = ""
        var size: Int64 = 0
    }

    /// The exported copies waiting to go.
    static var queueFolder: URL {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appending(path: "backup-queue")
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    private let lock = NSLock()
    private var waiting: [String: Meta] = [:] // by key
    private var bodies: [Int: Data] = [:]
    private var shown = Date.distantPast
    private var finishedEvents: (@Sendable () -> Void)?
    private var restoring: Task<Void, Never>?

    private(set) var anyNetwork: URLSession!
    private(set) var wifiOnly: URLSession!

    /// Files with iOS now: how many, how many bytes, and their keys.
    var pending: (count: Int, bytes: Int64, keys: Set<String>) {
        lock.withLock { (waiting.count, waiting.values.reduce(0) { $0 + $1.size }, Set(waiting.keys)) }
    }

    func whenFinished(_ handler: @escaping @Sendable () -> Void) {
        lock.withLock { finishedEvents = handler }
    }

    private override init() {
        super.init()
        anyNetwork = Self.session(Self.identifier, wifi: false, delegate: self)
        wifiOnly = Self.session(Self.wifiIdentifier, wifi: true, delegate: self)
        restoring = Task { await self.restore() }
    }

    private static func session(_ id: String, wifi: Bool, delegate: Uploader) -> URLSession {
        let config = URLSessionConfiguration.background(withIdentifier: id)
        config.sessionSendsLaunchEvents = true
        config.isDiscretionary = false
        config.httpCookieStorage = nil
        config.httpShouldSetCookies = false
        if wifi {
            config.allowsExpensiveNetworkAccess = false
            config.allowsConstrainedNetworkAccess = false
        }
        return URLSession(configuration: config, delegate: delegate, delegateQueue: nil)
    }

    /// What iOS still has from before (a relaunch), and copies it no longer
    /// has a task for cleared away.
    private func restore() async {
        var keep = Set<String>()
        for session in [anyNetwork!, wifiOnly!] {
            for task in await session.allTasks {
                guard let meta = Self.meta(task), task.state == .running || task.state == .suspended else { continue }
                lock.withLock { waiting[meta.key] = meta }
                keep.insert(meta.file)
            }
        }
        let files = (try? FileManager.default.contentsOfDirectory(at: Self.queueFolder, includingPropertiesForKeys: nil)) ?? []
        for file in files where !keep.contains(file.path) { try? FileManager.default.removeItem(at: file) }
    }

    func restored() async { await restoring?.value }

    private static func meta(_ task: URLSessionTask) -> Meta? {
        task.taskDescription.flatMap { $0.data(using: .utf8) }.flatMap { try? JSONDecoder().decode(Meta.self, from: $0) }
    }

    func enqueue(_ request: URLRequest, file: URL, size: Int64, wifiOnly onWifi: Bool, meta: Meta) {
        var meta = meta
        meta.file = file.path
        meta.size = size
        let task = (onWifi ? wifiOnly! : anyNetwork!).uploadTask(with: request, fromFile: file)
        task.taskDescription = (try? JSONEncoder().encode(meta)).flatMap { String(data: $0, encoding: .utf8) }
        lock.withLock { waiting[meta.key] = meta }
        task.resume()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didSendBodyData bytesSent: Int64,
                    totalBytesSent: Int64, totalBytesExpectedToSend: Int64) {
        let now = Date()
        let due = lock.withLock { () -> Bool in
            guard now.timeIntervalSince(shown) > 1 else { return false }
            shown = now
            return true
        }
        guard due else { return }
        // How far through the file sending now, for Settings.
        UserDefaults.standard.set(Int(totalBytesSent), forKey: "backup.sendingSent")
        UserDefaults.standard.set(Int(totalBytesExpectedToSend), forKey: "backup.sendingSize")
        UserDefaults.standard.set(Self.meta(task)?.video ?? false, forKey: "backup.sendingVideo")
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.withLock { bodies[dataTask.taskIdentifier, default: Data()].append(data) }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        completionHandler(nil) // the cookie goes only to the server it was made for
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        let body = lock.withLock { bodies.removeValue(forKey: task.taskIdentifier) ?? Data() }
        guard let meta = Self.meta(task) else { return }
        let remaining = lock.withLock { () -> Int in
            waiting.removeValue(forKey: meta.key)
            return waiting.count
        }
        try? FileManager.default.removeItem(atPath: meta.file)
        if remaining == 0 {
            UserDefaults.standard.set(0, forKey: "backup.sendingSent")
            UserDefaults.standard.set(0, forKey: "backup.sendingSize")
        }
        let code = (task.response as? HTTPURLResponse)?.statusCode ?? 0
        let message = ((try? JSONSerialization.jsonObject(with: body)) as? [String: Any])?["error"] as? String ?? ""
        Task { @MainActor in
            if error == nil, (200..<300).contains(code) || code == 409 {
                // 409: the server had it after all, under that name.
                PhotoBackup.shared.uploaded(meta)
            } else if code >= 400 {
                PhotoBackup.shared.refused(meta, code: code, message: message)
            }
            // A network failure: not counted, so the next run sends it again.
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
