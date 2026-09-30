import Foundation

/// An EPUB's shape, read the way any reader reads it: META-INF/container.xml
/// names the package (the OPF), whose spine is the chapters in reading order
/// and whose manifest says where each file is; the contents come from the
/// EPUB 3 nav document, or the EPUB 2 NCX. The server unzips, so each of these
/// is one /api/book/resource request.
struct EpubBook {
    struct Chapter {
        /// The file's path inside the EPUB.
        let path: String
    }

    struct Contents: Identifiable {
        let id = UUID()
        let title: String
        /// Which chapter (spine entry), and the fragment within it, if any.
        let chapter: Int
        let fragment: String?
    }

    let title: String
    let chapters: [Chapter]
    let contents: [Contents]

    static func load(_ item: Item, api: API) async throws -> EpubBook {
        let container = try await api.bookResource(item, path: "META-INF/container.xml")
        guard let opfPath = XML.first(container, element: "rootfile", attribute: "full-path") else {
            throw API.Failure.status(0, "This book has no package file.")
        }
        let opf = try await api.bookResource(item, path: opfPath)
        let package = XML.parse(opf)
        let base = (opfPath as NSString).deletingLastPathComponent

        // The manifest: id -> path (relative to the package, made whole).
        var paths: [String: String] = [:]
        var navPath: String?
        for node in package.all("item") {
            guard let id = node.attributes["id"], let href = node.attributes["href"] else { continue }
            let path = Self.join(base, href)
            paths[id] = path
            if (node.attributes["properties"] ?? "").split(separator: " ").contains("nav") { navPath = path }
        }
        let spine = package.all("itemref").compactMap { $0.attributes["idref"].flatMap { paths[$0] } }
        let ncxPath = package.all("spine").first?.attributes["toc"].flatMap { paths[$0] }
        let title = package.all("title").first?.text.trimmingCharacters(in: .whitespacesAndNewlines) ?? item.title

        var book = EpubBook(title: title.isEmpty ? item.title : title, chapters: spine.map { Chapter(path: $0) }, contents: [])
        if let navPath, let nav = try? await api.bookResource(item, path: navPath) {
            book = book.with(contents: Self.navContents(nav, at: navPath, spine: spine))
        }
        if book.contents.isEmpty, let ncxPath, let ncx = try? await api.bookResource(item, path: ncxPath) {
            book = book.with(contents: Self.ncxContents(ncx, at: ncxPath, spine: spine))
        }
        return book
    }

    private func with(contents: [Contents]) -> EpubBook {
        EpubBook(title: title, chapters: chapters, contents: contents)
    }

    /// EPUB 3: the <nav epub:type="toc"> list's links.
    private static func navContents(_ data: Data, at path: String, spine: [String]) -> [Contents] {
        let doc = XML.parse(data)
        let toc = doc.all("nav").first { ($0.attributes["epub:type"] ?? $0.attributes["type"] ?? "").contains("toc") }
            ?? doc.all("nav").first
        let base = (path as NSString).deletingLastPathComponent
        return (toc?.all("a") ?? []).compactMap { a in
            entry(a.text, href: a.attributes["href"], base: base, spine: spine)
        }
    }

    /// EPUB 2: each navPoint's label and content src.
    private static func ncxContents(_ data: Data, at path: String, spine: [String]) -> [Contents] {
        let base = (path as NSString).deletingLastPathComponent
        return XML.parse(data).all("navPoint").compactMap { point in
            let label = point.all("text").first?.text ?? ""
            return entry(label, href: point.all("content").first?.attributes["src"], base: base, spine: spine)
        }
    }

    private static func entry(_ title: String, href: String?, base: String, spine: [String]) -> Contents? {
        guard let href, !href.isEmpty else { return nil }
        let parts = href.split(separator: "#", maxSplits: 1).map(String.init)
        let path = join(base, parts[0])
        guard let i = spine.firstIndex(of: path) else { return nil }
        let name = title.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        return Contents(title: name.isEmpty ? "Chapter \(i + 1)" : name, chapter: i, fragment: parts.count > 1 ? parts[1] : nil)
    }

    /// A path relative to a folder inside the EPUB, "../" and all.
    static func join(_ base: String, _ href: String) -> String {
        let decoded = href.removingPercentEncoding ?? href
        var parts = base.isEmpty ? [] : base.split(separator: "/").map(String.init)
        for p in decoded.split(separator: "/", omittingEmptySubsequences: false).map(String.init) {
            switch p {
            case "..": if !parts.isEmpty { parts.removeLast() }
            case ".", "": continue
            default: parts.append(p)
            }
        }
        return parts.joined(separator: "/")
    }
}

/// A small XML tree, enough for container.xml, the OPF, nav and NCX. Names are
/// kept with their prefix removed ("opf:item" is "item"), except attributes,
/// where epub:type matters.
final class XML {
    let name: String
    let attributes: [String: String]
    var children: [XML] = []
    var text = ""

    init(name: String, attributes: [String: String]) {
        self.name = name
        self.attributes = attributes
    }

    /// Every element of that name, at any depth, in document order.
    func all(_ name: String) -> [XML] {
        children.flatMap { ($0.name == name ? [$0] : []) + $0.all(name) }
    }

    static func first(_ data: Data, element: String, attribute: String) -> String? {
        parse(data).all(element).first?.attributes[attribute]
    }

    static func parse(_ data: Data) -> XML {
        let builder = Builder()
        let parser = XMLParser(data: Entities.numeric(data))
        parser.delegate = builder
        parser.parse()
        return builder.root
    }

    private final class Builder: NSObject, XMLParserDelegate {
        let root = XML(name: "#document", attributes: [:])
        private lazy var stack: [XML] = [root]

        func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?,
                    qualifiedName: String?, attributes: [String: String] = [:]) {
            let local = name.split(separator: ":").last.map(String.init) ?? name
            let node = XML(name: local, attributes: attributes)
            stack.last?.children.append(node)
            stack.append(node)
        }

        func parser(_ parser: XMLParser, didEndElement name: String, namespaceURI: String?, qualifiedName: String?) {
            let done = stack.removeLast()
            // An element's text includes its children's, as textContent does.
            stack.last?.text += done.text
        }

        func parser(_ parser: XMLParser, foundCharacters string: String) {
            stack.last?.text += string
        }
    }
}

/// XMLParser knows only XML's five entities; XHTML in the wild uses HTML's
/// (&nbsp; above all). They are turned into numbers before parsing, or the
/// parser stops at the first one.
enum Entities {
    static let named: [String: Int] = [
        "nbsp": 160, "mdash": 8212, "ndash": 8211, "hellip": 8230, "lsquo": 8216, "rsquo": 8217,
        "ldquo": 8220, "rdquo": 8221, "copy": 169, "reg": 174, "trade": 8482, "shy": 173, "middot": 183,
        "eacute": 233, "egrave": 232, "aacute": 225, "agrave": 224, "iacute": 237, "oacute": 243,
        "uacute": 250, "ccedil": 231, "ntilde": 241, "uuml": 252, "ouml": 246, "auml": 228,
        "szlig": 223, "laquo": 171, "raquo": 187, "bull": 8226, "deg": 176, "times": 215,
        "thinsp": 8201, "ensp": 8194, "emsp": 8195, "zwnj": 8204, "zwj": 8205, "prime": 8242,
    ]

    static func numeric(_ data: Data) -> Data {
        guard var s = String(data: data, encoding: .utf8), s.contains("&") else { return data }
        for (name, code) in named {
            s = s.replacingOccurrences(of: "&\(name);", with: "&#\(code);")
        }
        return Data(s.utf8)
    }
}
