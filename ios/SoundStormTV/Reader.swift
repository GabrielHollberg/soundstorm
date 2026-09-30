import SwiftUI
import UIKit

/// A book on the TV. tvOS has no web view for foliate-js, so an EPUB is laid
/// out here: each chapter's text (EpubText) poured through TextKit into
/// screen-sized pages. The place is saved as the page's reader saves it - an
/// EPUB CFI and how far through - so the TV and the page open at the same
/// spot.
@Observable
final class BookReader {
    let item: Item
    private let api: API

    private(set) var book: EpubBook?
    private(set) var chapter = 0
    private(set) var page = 0
    private(set) var pages: [NSRange] = []
    /// Counts layouts, so the page is drawn again once one finishes: the
    /// first chapter's page is on screen before its text is laid out, and
    /// with the same chapter and page it was never drawn again - a blank
    /// title page.
    private(set) var generation = 0
    private(set) var failed: String?
    private(set) var loading = true
    /// The text size, kept between books.
    var size: CGFloat = CGFloat(UserDefaults.standard.double(forKey: "readerSize") == 0
        ? 38 : UserDefaults.standard.double(forKey: "readerSize"))

    /// The page is this big; the text is laid out to it.
    static let pageSize = CGSize(width: 1300, height: 820)

    private(set) var text: EpubText?
    let layout = NSLayoutManager()
    private var storage = NSTextStorage()
    private var saveTask: Task<Void, Never>?

    init(item: Item, api: API) {
        self.item = item
        self.api = api
    }

    var chapterTitle: String {
        guard let book else { return item.title }
        return book.contents.last { $0.chapter <= chapter }?.title ?? book.title
    }

    /// How far through the book: the chapters before, and this one's share.
    var fraction: Double {
        guard let book, !book.chapters.isEmpty else { return 0 }
        let within = pages.isEmpty ? 0 : Double(page + 1) / Double(pages.count)
        return (Double(chapter) + within) / Double(book.chapters.count)
    }

    func open() async {
        do {
            let book = try await EpubBook.load(item, api: api)
            guard !book.chapters.isEmpty else { throw API.Failure.status(0, "This book has no chapters.") }
            self.book = book
            // Where this person got to: the CFI the page saved, or failing
            // that how far through, by chapter.
            if let place = await api.readPlace(item) {
                if let cfi = CFI(place.location), book.chapters.indices.contains(cfi.chapter) {
                    await show(chapter: cfi.chapter) { text in text.offset(forCFIPath: cfi.steps, charOffset: cfi.offset) }
                    return
                }
                let c = min(book.chapters.count - 1, Int(place.fraction * Double(book.chapters.count)))
                await show(chapter: c) { _ in 0 }
                return
            }
            await show(chapter: 0) { _ in 0 }
        } catch {
            failed = error.localizedDescription
            loading = false
        }
    }

    // MARK: Turning

    func next() async {
        if page + 1 < pages.count {
            page += 1
            scheduleSave()
        } else if let book, chapter + 1 < book.chapters.count {
            await show(chapter: chapter + 1) { _ in 0 }
        }
    }

    func previous() async {
        if page > 0 {
            page -= 1
            scheduleSave()
        } else if chapter > 0 {
            await show(chapter: chapter - 1) { $0.text.length } // its last page
        }
    }

    func go(to entry: EpubBook.Contents) async {
        await show(chapter: entry.chapter) { text in entry.fragment.flatMap { text.anchors[$0] } ?? 0 }
    }

    func setSize(_ v: CGFloat) async {
        size = v
        UserDefaults.standard.set(Double(v), forKey: "readerSize")
        // The same words stay on screen: laid out again from where the page began.
        let at = pages.indices.contains(page) ? pages[page].location : 0
        await show(chapter: chapter) { _ in at }
    }

    /// Lays a chapter out into pages and shows the page holding `offset`.
    private func show(chapter c: Int, at offset: (EpubText) -> Int) async {
        guard let book else { return }
        loading = true
        defer { loading = false }
        guard let data = try? await api.bookResource(item, path: book.chapters[c].path) else {
            failed = "This chapter didn't load."
            return
        }
        let text = EpubText.make(data, at: book.chapters[c].path, size: size)
        let full = NSMutableAttributedString(attributedString: text.text)
        await addImages(text, to: full)
        paginate(full)
        self.text = text
        chapter = c
        let target = offset(text)
        page = pages.lastIndex { $0.location <= target } ?? 0
        failed = nil
        scheduleSave()
    }

    /// Pictures, fetched and scaled to fit a page.
    private func addImages(_ text: EpubText, to full: NSMutableAttributedString) async {
        for image in text.images.reversed() {
            guard let data = try? await api.bookResource(item, path: image.path),
                  let picture = UIImage(data: data)
            else { continue }
            let attachment = NSTextAttachment()
            attachment.image = picture
            let box = Self.pageSize
            let scale = min(1, box.width / picture.size.width, (box.height * 0.9) / picture.size.height)
            attachment.bounds = CGRect(origin: .zero, size: CGSize(width: picture.size.width * scale,
                                                                   height: picture.size.height * scale))
            // Pictures centred, as most books' own styles have them.
            let centred = NSMutableAttributedString(attachment: attachment)
            let paragraph = NSMutableParagraphStyle()
            paragraph.alignment = .center
            centred.addAttribute(.paragraphStyle, value: paragraph, range: NSRange(location: 0, length: centred.length))
            full.replaceCharacters(in: NSRange(location: image.offset, length: 1), with: centred)
        }
    }

    private func paginate(_ full: NSAttributedString) {
        storage.removeLayoutManager(layout)
        while !layout.textContainers.isEmpty { layout.removeTextContainer(at: 0) }
        storage = NSTextStorage(attributedString: full)
        storage.addLayoutManager(layout)
        var ranges: [NSRange] = []
        while true {
            let container = NSTextContainer(size: Self.pageSize)
            container.lineFragmentPadding = 0
            layout.addTextContainer(container)
            let glyphs = layout.glyphRange(for: container)
            let chars = layout.characterRange(forGlyphRange: glyphs, actualGlyphRange: nil)
            if chars.length == 0 && !ranges.isEmpty {
                layout.removeTextContainer(at: layout.textContainers.count - 1)
                break
            }
            ranges.append(chars)
            if NSMaxRange(chars) >= storage.length || ranges.count > 2000 { break }
        }
        pages = ranges.isEmpty ? [NSRange(location: 0, length: 0)] : ranges
        generation += 1
    }

    // MARK: Saving

    /// A few seconds after the page stops turning, and on closing.
    private func scheduleSave() {
        saveTask?.cancel()
        saveTask = Task {
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled else { return }
            await write()
        }
    }

    /// Now, on closing. The pending save goes; this one takes its place.
    func save() async {
        saveTask?.cancel()
        await write()
    }

    /// Not save(): cancelling the pending save from inside it cancels the
    /// very task doing the saving, and URLSession then sends nothing.
    private func write() async {
        guard let text, pages.indices.contains(page) else { return }
        let location = CFI.make(chapter: chapter, path: text.cfiPath(at: pages[page].location))
        await api.saveReadPlace(item, location: location, fraction: fraction)
    }
}

/// One page: TextKit's glyphs for that page's container, drawn.
private struct PageView: UIViewRepresentable {
    let layout: NSLayoutManager
    let index: Int

    func makeUIView(context: Context) -> PageCanvas { PageCanvas() }

    func updateUIView(_ view: PageCanvas, context: Context) {
        view.layout = layout
        view.index = index
        view.setNeedsDisplay()
    }

    final class PageCanvas: UIView {
        var layout: NSLayoutManager?
        var index = 0

        override init(frame: CGRect) {
            super.init(frame: frame)
            isOpaque = false
            backgroundColor = .clear
        }

        required init?(coder: NSCoder) { fatalError("not used") }

        override func draw(_ rect: CGRect) {
            guard let layout, layout.textContainers.indices.contains(index) else { return }
            let glyphs = layout.glyphRange(for: layout.textContainers[index])
            layout.drawBackground(forGlyphRange: glyphs, at: .zero)
            layout.drawGlyphs(forGlyphRange: glyphs, at: .zero)
        }
    }
}

/// The reader: the page, the chapter and how far through beneath. Left and
/// right turn the page (the TV rule); down pauses or plays the music, which
/// plays on while reading; holding OK opens Contents and Text size; Back
/// closes.
struct ReaderView: View {
    let reader: BookReader
    @Environment(Player.self) private var player
    @Environment(\.dismiss) private var dismiss
    @State private var contents = false
    @State private var toast: String?
    @FocusState private var focused: Bool

    var body: some View {
        ZStack {
            Color(white: 0.07).ignoresSafeArea()
            if let failed = reader.failed {
                VStack(spacing: 30) {
                    Text("This book can't be shown.").font(.title3)
                    Text(failed).foregroundStyle(.secondary)
                }
            } else if reader.book == nil {
                ProgressView()
            } else {
                VStack(spacing: 24) {
                    PageView(layout: reader.layout, index: reader.page)
                        .frame(width: BookReader.pageSize.width, height: BookReader.pageSize.height)
                        .id("\(reader.generation)-\(reader.chapter)-\(reader.page)")
                    HStack {
                        Text(reader.chapterTitle).lineLimit(1)
                        Spacer()
                        Text("\(Int((reader.fraction * 100).rounded()))%")
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(width: BookReader.pageSize.width)
                }
            }
            if let toast {
                VStack {
                    Text(toast).padding(.horizontal, 30).padding(.vertical, 14)
                        .background(.ultraThinMaterial, in: Capsule())
                    Spacer()
                }
                .padding(.top, 30)
            }
        }
        .focusable()
        .focused($focused)
        .focusEffectDisabled()
        .onAppear { focused = true }
        .task { await reader.open() }
        .onMoveCommand { direction in
            switch direction {
            case .right: Task { await reader.next() }
            case .left: Task { await reader.previous() }
            case .down: toggleMusic()
            default: break
            }
        }
        .onPlayPauseCommand { toggleMusic() }
        .onExitCommand {
            Task { await reader.save() }
            dismiss()
        }
        .contextMenu {
            if let book = reader.book, !book.contents.isEmpty {
                Button { contents = true } label: { Label("Contents", systemImage: "list.bullet") }
            }
            Menu {
                ForEach([("Small", 30.0), ("Medium", 38.0), ("Large", 46.0), ("Largest", 56.0)], id: \.1) { name, v in
                    Button { Task { await reader.setSize(CGFloat(v)) } } label: {
                        if CGFloat(v) == reader.size { Label(name, systemImage: "checkmark") } else { Text(name) }
                    }
                }
            } label: { Label("Text size", systemImage: "textformat.size") }
        }
        .sheet(isPresented: $contents) {
            NavigationStack {
                List(reader.book?.contents ?? []) { entry in
                    Button(entry.title) {
                        contents = false
                        Task { await reader.go(to: entry) }
                    }
                }
                .navigationTitle("Contents")
            }
        }
    }

    private func toggleMusic() {
        guard player.current != nil else { return }
        player.togglePlay()
        toast = player.isPlaying ? "Music playing" : "Music paused"
        Task {
            try? await Task.sleep(for: .seconds(2))
            toast = nil
        }
    }
}
