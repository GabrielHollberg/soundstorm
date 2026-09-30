import Foundation

/// WebVTT, read into cues, so the app can draw subtitles itself. AVPlayer
/// shows subtitles only from inside the stream it plays, and SoundStorm serves
/// them beside it (/api/subtitle), as a browser's <track> takes them.
struct Subtitles {
    struct Cue {
        let start: Double
        let end: Double
        let text: String
    }

    private(set) var cues: [Cue] = []

    init(webVTT: String) {
        // Blocks are separated by blank lines; a cue's block has a timing
        // line, optionally after an id, then its text.
        let normalized = webVTT.replacingOccurrences(of: "\r\n", with: "\n").replacingOccurrences(of: "\r", with: "\n")
        for block in normalized.components(separatedBy: "\n\n") {
            let lines = block.split(separator: "\n", omittingEmptySubsequences: true).map(String.init)
            guard let t = lines.firstIndex(where: { $0.contains("-->") }) else { continue }
            let parts = lines[t].components(separatedBy: "-->")
            guard parts.count == 2,
                  let start = Self.seconds(parts[0]),
                  let end = Self.seconds(parts[1].split(separator: " ").first.map(String.init) ?? "")
            else { continue }
            let text = lines[(t + 1)...].map(Self.plain).joined(separator: "\n")
            if !text.isEmpty { cues.append(Cue(start: start, end: end, text: text)) }
        }
        cues.sort { $0.start < $1.start }
    }

    /// The text showing at a moment: every cue that covers it, in order.
    func text(at time: Double) -> String {
        // The last cue starting at or before the time, found by halving, then
        // back over any earlier ones still running (overlaps are allowed).
        var lo = 0, hi = cues.count
        while lo < hi {
            let mid = (lo + hi) / 2
            if cues[mid].start <= time { lo = mid + 1 } else { hi = mid }
        }
        var showing: [String] = []
        var i = lo - 1
        while i >= 0 && showing.count < 3 {
            if cues[i].end > time { showing.insert(cues[i].text, at: 0) }
            if time - cues[i].start > 30 { break } // nothing that old still runs
            i -= 1
        }
        return showing.joined(separator: "\n")
    }

    /// "01:02:03.456" or "02:03.456" (a comma allowed, as SRT has it).
    static func seconds(_ s: String) -> Double? {
        let f = s.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: ",", with: ".").split(separator: ":")
        guard (2...3).contains(f.count), let last = Double(f[f.count - 1]) else { return nil }
        let rest = f.dropLast().compactMap { Double($0) }
        guard rest.count == f.count - 1 else { return nil }
        return rest.reduce(0) { $0 * 60 + $1 } * 60 + last
    }

    /// Without the markup (<i>, <c.yellow>, <v Speaker>) and with entities read.
    private static func plain(_ line: String) -> String {
        var out = line.replacingOccurrences(of: "<[^>]*>", with: "", options: .regularExpression)
        for (entity, char) in [("&amp;", "&"), ("&lt;", "<"), ("&gt;", ">"), ("&nbsp;", " "), ("&lrm;", ""), ("&rlm;", "")] {
            out = out.replacingOccurrences(of: entity, with: char)
        }
        return out.trimmingCharacters(in: .whitespaces)
    }
}
