import Foundation
import ImageIO
import UIKit

/// Fetching and decoding with a ceiling. A book, a PDF or a picture is the
/// server's to hand over, and the server hands over what is in the library;
/// one built to be huge (or a small file that unpacks to an enormous picture)
/// must fail to load, not take the Apple TV's memory with it.
enum SafeLoad {
    /// Fetches to disk first, so an oversized answer never sits in memory,
    /// and reads it in only if it is within `limit` bytes.
    nonisolated static func data(_ request: URLRequest, limit: Int) async throws -> (Data, URLResponse) {
        let (file, response) = try await URLSession.shared.download(for: request)
        defer { try? FileManager.default.removeItem(at: file) }
        let size = (try? FileManager.default.attributesOfItem(atPath: file.path)[.size] as? Int) ?? Int.max
        guard size <= limit else { throw API.Failure.status(0, "That file is too large to open here.") }
        return (try Data(contentsOf: file), response)
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
