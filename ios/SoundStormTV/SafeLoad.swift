import Foundation
import ImageIO
import SwiftUI
import UIKit

/// Fetching and decoding with a ceiling. A book, a PDF or a picture is the
/// server's to hand over, and the server hands over what is in the library;
/// one built to be huge (or a small file that unpacks to an enormous picture)
/// must fail to load, not take the Apple TV's memory with it.
enum SafeLoad {
    /// Fetches to disk, stopping the moment the answer passes `limit` bytes,
    /// and reads it in only then: an oversized answer never sits in memory,
    /// nor fills the disk on the way.
    nonisolated static func data(_ request: URLRequest, limit: Int) async throws -> (Data, URLResponse) {
        let (file, response) = try await download(request, limit: limit)
        defer { try? FileManager.default.removeItem(at: file) }
        return (try Data(contentsOf: file), response)
    }

    /// The file itself, for what is read from disk (a song to hear). The
    /// caller removes it.
    nonisolated static func download(_ request: URLRequest, limit: Int) async throws -> (URL, URLResponse) {
        try await CappedDownload.run(request, limit: Int64(limit))
    }

    nonisolated static func data(from url: URL, limit: Int) async throws -> (Data, URLResponse) {
        try await data(URLRequest(url: url), limit: limit)
    }

    /// A picture decoded at most `maxPixels` on its longer side, whatever
    /// size the file claims to be.
    nonisolated static func image(_ data: Data, maxPixels: Int) -> UIImage? {
        guard let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary)
        else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: maxPixels,
        ]
        guard let cg = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        return UIImage(cgImage: cg)
    }

    /// Ceilings: far above any real file of each kind.
    static let bookPart = 32 << 20
    static let wholeFile = 256 << 20
    static let picture = 40 << 20
}

/// AsyncImage, with SafeLoad's ceilings: the file fetched with a size limit
/// and decoded at most `maxPixels` across. AsyncImage decodes whatever the
/// server sends at full size, and a grid of covers is a lot of them.
struct SafeImage<Content: View, Placeholder: View>: View {
    let url: URL?
    var maxPixels = 600
    @ViewBuilder let content: (Image) -> Content
    @ViewBuilder let placeholder: () -> Placeholder
    @State private var image: UIImage?

    var body: some View {
        Group {
            if let image {
                content(Image(uiImage: image))
            } else {
                placeholder()
            }
        }
        .task(id: url) {
            image = nil
            guard let url, let (data, _) = try? await SafeLoad.data(from: url, limit: SafeLoad.picture) else { return }
            let px = maxPixels
            image = await Task.detached(priority: .utility) { SafeLoad.image(data, maxPixels: px) }.value
        }
    }
}

/// A download that is cancelled as soon as it is, or says it will be, more
/// than its limit. Its own session, so the progress reaches a delegate; the
/// shared cookie store, as URLSession.shared has, so the session's cookie goes.
nonisolated private final class CappedDownload: NSObject, URLSessionDownloadDelegate, @unchecked Sendable {
    private let limit: Int64
    private let lock = NSLock()
    private var continuation: CheckedContinuation<(URL, URLResponse), Error>?
    private var tooLarge = false

    private init(limit: Int64) { self.limit = limit }

    static func run(_ request: URLRequest, limit: Int64) async throws -> (URL, URLResponse) {
        let download = CappedDownload(limit: limit)
        let session = URLSession(configuration: .default, delegate: download, delegateQueue: nil)
        defer { session.finishTasksAndInvalidate() }
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                download.lock.withLock { download.continuation = continuation }
                session.downloadTask(with: request).resume()
            }
        } onCancel: {
            session.invalidateAndCancel()
        }
    }

    private func finish(_ result: Result<(URL, URLResponse), Error>) {
        let continuation = lock.withLock { () -> CheckedContinuation<(URL, URLResponse), Error>? in
            defer { self.continuation = nil }
            return self.continuation
        }
        continuation?.resume(with: result)
    }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didWriteData bytesWritten: Int64,
                    totalBytesWritten: Int64, totalBytesExpectedToWrite: Int64) {
        if totalBytesWritten > limit || totalBytesExpectedToWrite > limit {
            lock.withLock { tooLarge = true }
            downloadTask.cancel()
        }
    }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didFinishDownloadingTo location: URL) {
        // The file is gone once this returns, so it is moved first.
        let kept = FileManager.default.temporaryDirectory.appending(path: "safeload-" + UUID().uuidString)
        do {
            let size = (try FileManager.default.attributesOfItem(atPath: location.path)[.size] as? Int64) ?? .max
            guard size <= limit, let response = downloadTask.response else {
                throw API.Failure.status(0, "That file is too large to open here.")
            }
            try FileManager.default.moveItem(at: location, to: kept)
            finish(.success((kept, response)))
        } catch {
            finish(.failure(error))
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard let error else { return }
        let large = lock.withLock { tooLarge }
        finish(.failure(large ? API.Failure.status(0, "That file is too large to open here.") : error))
    }
}
