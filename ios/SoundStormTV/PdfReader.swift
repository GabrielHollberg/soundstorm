import SwiftUI
import UIKit

/// A PDF on the TV, a page at a time, drawn by Core Graphics (tvOS has no
/// PDFKit and no web view). The page's own reader hands PDFs to the browser's
/// viewer, which keeps no place; here the place is "page=<n>", kept with
/// how far through so Continue can show it.
@Observable
final class PdfReader {
    let item: Item
    private let api: API
    private(set) var document: CGPDFDocument?
    private(set) var page = 1
    private(set) var image: UIImage?
    private(set) var failed: String?
    private var saveTask: Task<Void, Never>?

    init(item: Item, api: API) {
        self.item = item
        self.api = api
    }

    var count: Int { document?.numberOfPages ?? 0 }

    func open() async {
        do {
            let data = try await api.file(item)
            guard let provider = CGDataProvider(data: data as CFData), let doc = CGPDFDocument(provider),
                  doc.numberOfPages > 0
            else { throw API.Failure.status(0, "This PDF can't be read.") }
            document = doc
            if let place = await api.readPlace(item), place.location.hasPrefix("page="),
               let n = Int(place.location.dropFirst(5)), (1...doc.numberOfPages).contains(n) {
                page = n
            }
            render()
        } catch {
            failed = error.localizedDescription
        }
    }

    func turn(_ by: Int) {
        let next = page + by
        guard (1...max(count, 1)).contains(next) else { return }
        page = next
        render()
        saveTask?.cancel()
        saveTask = Task {
            try? await Task.sleep(for: .seconds(4))
            if !Task.isCancelled { await write() }
        }
    }

    func save() async {
        saveTask?.cancel()
        await write()
    }

    /// Not save(), which would cancel the task it runs in (see BookReader).
    private func write() async {
        guard count > 0 else { return }
        await api.saveReadPlace(item, location: "page=\(page)", fraction: Double(page) / Double(count))
    }

    /// The page at the screen's height, on white, as paper is.
    private func render() {
        guard let pdfPage = document?.page(at: page) else { return }
        let box = pdfPage.getBoxRect(.cropBox)
        let height: CGFloat = 1000
        let scale = height / box.height
        let size = CGSize(width: box.width * scale, height: height)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 2
        image = UIGraphicsImageRenderer(size: size, format: format).image { ctx in
            UIColor.white.setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
            let c = ctx.cgContext
            c.translateBy(x: 0, y: size.height)
            c.scaleBy(x: scale, y: -scale)
            c.translateBy(x: -box.minX, y: -box.minY)
            c.drawPDFPage(pdfPage)
        }
    }
}

struct PdfView: View {
    let reader: PdfReader
    @Environment(\.dismiss) private var dismiss
    @FocusState private var focused: Bool

    var body: some View {
        ZStack {
            Color(white: 0.07).ignoresSafeArea()
            if let failed = reader.failed {
                Text(failed).foregroundStyle(.secondary)
            } else if let image = reader.image {
                VStack(spacing: 16) {
                    Image(uiImage: image).resizable().scaledToFit()
                    Text("Page \(reader.page) of \(reader.count)").font(.caption).foregroundStyle(.secondary)
                }
                .padding(.vertical, 20)
            } else {
                ProgressView()
            }
        }
        .focusable()
        .focused($focused)
        .focusEffectDisabled()
        .onAppear { focused = true }
        .task { await reader.open() }
        .onMoveCommand { direction in
            switch direction {
            case .right: reader.turn(1)
            case .left: reader.turn(-1)
            default: break
            }
        }
        .onExitCommand {
            Task { await reader.save() }
            dismiss()
        }
    }
}
