import UIKit

/// A chapter's XHTML as text for TextKit - headings, paragraphs, italics and
/// bold, pictures - and a map from every piece of that text back to where it
/// was in the XHTML, in EPUB CFI terms, so the TV reads and writes the same
/// places as the page's reader (foliate-js), which saves a CFI.
///
/// CFI steps count only element children as even numbers (/2 is the first
/// element, /4 the second) and the text between them as odd ones (/1 before
/// the first element, /3 after it); a character offset follows a colon. The
/// path after "!" starts inside <html>, so <body> is usually /4.
struct EpubText {
    struct Run {
        let start: Int        // in the attributed string
        let length: Int
        let path: [Int]       // the element's steps from <html>
        let chunk: Int        // the odd step of the text within it
        let domStart: Int     // the offset of this piece within that text
        let domLength: Int
    }

    let text: NSAttributedString
    let runs: [Run]
    /// id attribute -> where it is, for the contents' "#fragment" links.
    let anchors: [String: Int]
    /// Pictures to fetch, by their place in the text.
    let images: [(offset: Int, path: String)]

    // MARK: Places

    /// "/4/2/10/1:120" for a place in the text.
    func cfiPath(at offset: Int) -> String {
        guard let run = runs.last(where: { $0.start <= offset }) ?? runs.first else { return "/4" }
        let within = max(0, min(offset - run.start, run.length))
        let dom = run.domStart + (run.length > 0 ? within * run.domLength / max(run.length, 1) : 0)
        return run.path.map { "/\($0)" }.joined() + "/\(run.chunk):\(dom)"
    }

    /// Where a CFI's path (after "!") is in the text: the run it names, else
    /// the first run inside the element it names, else the start.
    func offset(forCFIPath steps: [Int], charOffset: Int?) -> Int {
        guard !steps.isEmpty else { return 0 }
        let last = steps[steps.count - 1]
        if last % 2 == 1 {
            let path = Array(steps.dropLast())
            let matching = runs.filter { $0.path == path && $0.chunk == last }
            if let off = charOffset,
               let run = matching.last(where: { $0.domStart <= off }) {
                let into = min(off - run.domStart, run.domLength)
                return run.start + (run.domLength > 0 ? into * run.length / run.domLength : 0)
            }
            if let run = matching.first { return run.start }
            return offset(forCFIPath: path, charOffset: nil)
        }
        // An element: its first text, or its parent's.
        if let run = runs.first(where: { $0.path.starts(with: steps) }) { return run.start }
        return offset(forCFIPath: Array(steps.dropLast()), charOffset: nil)
    }

    // MARK: Reading the XHTML

    static func make(_ data: Data, at path: String, size: CGFloat) -> EpubText {
        let builder = Builder(base: (path as NSString).deletingLastPathComponent, size: size)
        let parser = XMLParser(data: Entities.numeric(data))
        parser.delegate = builder
        parser.shouldProcessNamespaces = false
        parser.parse()
        builder.trimEnd()
        return EpubText(text: builder.out, runs: builder.runs, anchors: builder.anchors, images: builder.images)
    }

    private final class Builder: NSObject, XMLParserDelegate {
        let out = NSMutableAttributedString()
        var runs: [Run] = []
        var anchors: [String: Int] = [:]
        var images: [(offset: Int, path: String)] = []
        let base: String
        let size: CGFloat

        private struct Frame {
            let name: String
            let path: [Int]
            var elements = 0     // element children so far
            var chunkOffset = 0  // characters into the current text chunk
        }
        private var stack: [Frame] = []
        private var skip = 0          // inside <head>, <script>, <style>
        private var italic = 0, bold = 0, heading = 0, small = 0, pre = 0
        private var pendingBreak = false
        /// ids whose element has begun but shown nothing yet: placed at its
        /// first text, after any paragraph break, so an anchor (a read-along
        /// sentence above all) never starts on the break before it.
        private var pendingAnchors: [String] = []
        private var lastWasSpace = true

        init(base: String, size: CGFloat) {
            self.base = base
            self.size = size
        }

        static let blocks: Set<String> = ["p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "li", "blockquote",
                                          "section", "article", "figure", "figcaption", "tr", "hr", "pre",
                                          "dt", "dd", "header", "footer", "aside", "table", "ul", "ol", "body"]

        func parser(_ parser: XMLParser, didStartElement raw: String, namespaceURI: String?,
                    qualifiedName: String?, attributes: [String: String] = [:]) {
            guard stack.count < maxDepth else { parser.abortParsing(); return }
            let name = (raw.split(separator: ":").last.map(String.init) ?? raw).lowercased()
            var path: [Int] = []
            if !stack.isEmpty {
                stack[stack.count - 1].elements += 1
                stack[stack.count - 1].chunkOffset = 0 // the text after this element is a new chunk
                path = stack[stack.count - 1].path + [stack[stack.count - 1].elements * 2]
            }
            stack.append(Frame(name: name, path: path))
            if ["head", "script", "style"].contains(name) { skip += 1 }
            guard skip == 0 else { return }

            if let id = attributes["id"] { pendingAnchors.append(id) }
            if Self.blocks.contains(name) { pendingBreak = out.length > 0 }
            switch name {
            case "em", "i", "cite", "dfn": italic += 1
            case "strong", "b": bold += 1
            case "h1", "h2", "h3", "h4", "h5", "h6": heading += 1; bold += 1
            case "sup", "sub", "small": small += 1
            case "pre", "code": pre += 1
            case "br": append("\n", domLength: 0)
            case "img", "image":
                let src = attributes["src"] ?? attributes["xlink:href"] ?? attributes["href"]
                if let src {
                    breakIfPending()
                    placeAnchors()
                    images.append((out.length, EpubBook.join(base, src)))
                    out.append(NSAttributedString(string: "\u{FFFC}", attributes: attrs()))
                    pendingBreak = true
                }
            default: break
            }
        }

        func parser(_ parser: XMLParser, didEndElement raw: String, namespaceURI: String?, qualifiedName: String?) {
            let frame = stack.removeLast()
            if ["head", "script", "style"].contains(frame.name) { skip -= 1; return }
            guard skip == 0 else { return }
            switch frame.name {
            case "em", "i", "cite", "dfn": italic -= 1
            case "strong", "b": bold -= 1
            case "h1", "h2", "h3", "h4", "h5", "h6": heading -= 1; bold -= 1
            case "sup", "sub", "small": small -= 1
            case "pre", "code": pre -= 1
            default: break
            }
            if Self.blocks.contains(frame.name) { pendingBreak = out.length > 0 }
        }

        func parser(_ parser: XMLParser, foundCharacters string: String) {
            guard !stack.isEmpty else { return }
            let domStart = stack[stack.count - 1].chunkOffset
            stack[stack.count - 1].chunkOffset += string.utf16.count
            guard skip == 0 else { return }
            let frame = stack[stack.count - 1]
            // Whitespace runs as one space, as a browser shows them, except in <pre>.
            var shown = ""
            for ch in string {
                if pre == 0 && ch.isWhitespace {
                    if !lastWasSpace { shown.append(" "); lastWasSpace = true }
                } else {
                    shown.append(ch)
                    lastWasSpace = false
                }
            }
            guard !shown.isEmpty, shown != " " || !pendingBreak else { return }
            if pendingBreak { breakIfPending(); if shown.hasPrefix(" ") { shown.removeFirst() } }
            guard !shown.isEmpty else { return }
            let start = out.length
            placeAnchors()
            out.append(NSAttributedString(string: shown, attributes: attrs()))
            runs.append(Run(start: start, length: out.length - start, path: frame.path,
                            chunk: frame.elements * 2 + 1, domStart: domStart, domLength: string.utf16.count))
        }

        private func append(_ s: String, domLength: Int) {
            out.append(NSAttributedString(string: s, attributes: attrs()))
            lastWasSpace = true
        }

        private func breakIfPending() {
            guard pendingBreak else { return }
            pendingBreak = false
            if out.length > 0 && !out.string.hasSuffix("\n") {
                out.append(NSAttributedString(string: "\n", attributes: attrs()))
            }
            lastWasSpace = true
        }

        private func placeAnchors() {
            for id in pendingAnchors { anchors[id] = out.length }
            pendingAnchors.removeAll()
        }

        func trimEnd() {
            placeAnchors()
            while out.string.hasSuffix("\n") || out.string.hasSuffix(" ") {
                out.deleteCharacters(in: NSRange(location: out.length - 1, length: 1))
            }
        }

        private func attrs() -> [NSAttributedString.Key: Any] {
            var pointSize = size
            if heading > 0 { pointSize = size * 1.35 }
            if small > 0 { pointSize = size * 0.75 }
            var traits: UIFontDescriptor.SymbolicTraits = []
            if italic > 0 { traits.insert(.traitItalic) }
            if bold > 0 { traits.insert(.traitBold) }
            var descriptor = UIFont.systemFont(ofSize: pointSize).fontDescriptor.withDesign(pre > 0 ? .monospaced : .serif)
                ?? UIFont.systemFont(ofSize: pointSize).fontDescriptor
            if let styled = descriptor.withSymbolicTraits(traits) { descriptor = styled }
            let paragraph = NSMutableParagraphStyle()
            paragraph.lineSpacing = pointSize * 0.3
            paragraph.paragraphSpacing = pointSize * (heading > 0 ? 0.9 : 0.6)
            paragraph.alignment = heading > 0 ? .center : .natural
            return [
                .font: UIFont(descriptor: descriptor, size: pointSize),
                .foregroundColor: UIColor(white: 0.92, alpha: 1),
                .paragraphStyle: paragraph,
            ]
        }
    }
}

/// An EPUB CFI as the page's reader writes it: "epubcfi(/6/14!/4/2/10/1:120)",
/// or a range, "epubcfi(/6/14!/4/2,/10/1:0,/10/1:30)", whose start is the
/// common part and the first part joined.
struct CFI {
    /// Which spine entry (chapter).
    let chapter: Int
    /// The steps after "!", and the character offset at the end, if any.
    let steps: [Int]
    let offset: Int?

    init?(_ s: String) {
        guard s.hasPrefix("epubcfi("), s.hasSuffix(")") else { return nil }
        var body = String(s.dropFirst(8).dropLast())
        // [id] assertions and ~/@ are only hints; a range starts at base + start.
        body = body.replacingOccurrences(of: "\\[[^\\]]*\\]", with: "", options: .regularExpression)
        let parts = body.split(separator: ",", omittingEmptySubsequences: false).map(String.init)
        if parts.count == 3 { body = parts[0] + parts[1] }
        let halves = body.split(separator: "!", maxSplits: 1).map(String.init)
        guard halves.count == 2 else { return nil }
        let spine = halves[0].split(separator: "/").compactMap { Int($0) }
        guard let itemref = spine.last, itemref >= 2 else { return nil }
        chapter = itemref / 2 - 1
        var inner = halves[1]
        var charOffset: Int?
        if let colon = inner.lastIndex(of: ":") {
            charOffset = Int(inner[inner.index(after: colon)...].prefix { $0.isNumber })
            inner = String(inner[..<colon])
        }
        steps = inner.split(separator: "/").compactMap { Int($0.prefix { $0.isNumber }) }
        offset = charOffset
    }

    static func make(chapter: Int, path: String) -> String {
        "epubcfi(/6/\((chapter + 1) * 2)!\(path))"
    }
}
