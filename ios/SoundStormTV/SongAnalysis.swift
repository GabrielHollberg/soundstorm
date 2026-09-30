import AVFoundation

/// What is heard in a song, for the visualizers: its beats, which of four
/// starts each bar, and per-frame loudness, bass hits and treble hits, 0 to 1.
/// The web page works this out on the device (hearSong in app.js) and so does
/// the TV - nothing of it is kept on the server - with the same steps and
/// constants, so a song moves the same on both.
struct Heard: Sendable {
    let fps: Double
    let loud: [Float]
    let low: [Float]
    let high: [Float]
    let beats: [Double]
    let down: Int
    /// The loudness a drop needs: the song's loudest 30%.
    let loudTop: Float

    /// Decodes the song at 11025 a second, mono, and hears it. nil for
    /// anything shorter than five seconds or that will not decode.
    nonisolated static func hear(file: URL, tempo: Double) async -> Heard? {
        guard let samples = await decode(file) else { return nil }
        return hear(samples: samples, rate: 11025, tempo: tempo)
    }

    nonisolated private static func decode(_ file: URL) async -> [Float]? {
        let asset = AVURLAsset(url: file)
        guard let reader = try? AVAssetReader(asset: asset),
              let track = try? await asset.loadTracks(withMediaType: .audio).first
        else { return nil }
        let output = AVAssetReaderTrackOutput(track: track, outputSettings: [
            AVFormatIDKey: kAudioFormatLinearPCM,
            AVSampleRateKey: 11025,
            AVNumberOfChannelsKey: 1,
            AVLinearPCMBitDepthKey: 32,
            AVLinearPCMIsFloatKey: true,
            AVLinearPCMIsNonInterleaved: false,
            AVLinearPCMIsBigEndianKey: false,
        ])
        reader.add(output)
        guard reader.startReading() else { return nil }
        var out: [Float] = []
        while let buffer = output.copyNextSampleBuffer(), let block = CMSampleBufferGetDataBuffer(buffer) {
            let length = CMBlockBufferGetDataLength(block)
            var chunk = [Float](repeating: 0, count: length / 4)
            chunk.withUnsafeMutableBytes { raw in
                _ = CMBlockBufferCopyDataBytes(block, atOffset: 0, dataLength: length, destination: raw.baseAddress!)
            }
            out += chunk
        }
        return reader.status == .completed ? out : nil
    }

    nonisolated static func hear(samples x: [Float], rate SR: Double, tempo: Double) -> Heard? {
        let HOP = 256
        let fps = SR / Double(HOP)
        let frames = x.count / HOP
        guard Double(frames) >= fps * 5 else { return nil }
        var loud = [Float](repeating: 0, count: frames)
        var low = loud, high = loud
        // Two one-pole filters split the low end (kick, bass) from the top
        // (snare, hats): cheap, and enough to tell them apart.
        let kLow = Float(1 - exp(-2 * Double.pi * 150 / SR))
        let kHigh = Float(1 - exp(-2 * Double.pi * 2500 / SR))
        var lp: Float = 0, hpState: Float = 0
        for f in 0..<frames {
            var sx: Float = 0, sl: Float = 0, sh: Float = 0
            for i in (f * HOP)..<(f * HOP + HOP) {
                let v = x[i]
                lp += kLow * (v - lp)
                hpState += kHigh * (v - hpState)
                let hp = v - hpState
                sx += v * v; sl += lp * lp; sh += hp * hp
            }
            loud[f] = 10 * log10(sx / Float(HOP) + 1e-10)
            low[f] = 10 * log10(sl / Float(HOP) + 1e-10)
            high[f] = 10 * log10(sh / Float(HOP) + 1e-10)
        }
        // Onsets: how sharply each band got louder.
        var lowOn = [Float](repeating: 0, count: frames)
        var highOn = lowOn, onset = lowOn
        for f in 1..<frames {
            lowOn[f] = max(0, low[f] - low[f - 1])
            highOn[f] = max(0, high[f] - high[f - 1])
            onset[f] = lowOn[f] + 0.6 * highOn[f] + 0.4 * max(0, loud[f] - loud[f - 1])
        }
        // How loud a part feels is its energy over about half a second.
        let win = max(1, Int((fps * 0.25).rounded()))
        let energy = loud.map { pow(10, Double($0) / 10) }
        var felt = [Float](repeating: 0, count: frames)
        var run = 0.0
        for f in 0..<(frames + win) {
            if f < frames { run += energy[f] }
            if f - 2 * win - 1 >= 0 { run -= energy[f - 2 * win - 1] }
            let c = f - win
            if c >= 0 && c < frames {
                let n = min(frames - 1, c + win) - max(0, c - win) + 1
                felt[c] = Float(10 * log10(run / Double(n) + 1e-10))
            }
        }
        let loudN = scale(felt, 0.05, 0.97)
        let lowOnN = scale(lowOn, 0.5, 0.995)
        let highOnN = scale(highOn, 0.5, 0.995)

        // The tempo: the lag at which the onsets repeat best, 70 to 180 a
        // minute, leaning towards the analysis's own tempo when there is one.
        let mean = onset.reduce(0, +) / Float(frames)
        var sd = sqrt(onset.reduce(0) { $0 + ($1 - mean) * ($1 - mean) } / Float(frames))
        if sd == 0 { sd = 1 }
        let on = onset.map { ($0 - mean) / sd }
        let prior = tempo > 0 ? tempo : 120
        var bestLag = 60 / prior * fps
        var best = -Double.infinity
        var lag = 60.0 / 180 * fps
        while lag <= 60.0 / 70 * fps {
            let whole = Int(lag)
            let part = Float(lag - Double(whole))
            var sum: Float = 0
            if whole + 1 < frames {
                for f in (whole + 1)..<frames {
                    sum += on[f] * (on[f - whole] * (1 - part) + on[f - whole - 1] * part)
                }
            }
            let avg = Double(sum) / Double(max(1, frames - whole - 1))
            let octaves = log2(60 * fps / lag / prior)
            let weighted = avg * exp(-0.5 * pow(octaves / (tempo > 0 ? 0.25 : 0.9), 2))
            if weighted > best { best = weighted; bestLag = lag }
            lag += 0.1
        }
        let period = bestLag

        // The beats: dynamic programming (Ellis, 2007) - every frame's best
        // chain of beats ending there, so a tempo that wanders is followed.
        var score = [Float](repeating: 0, count: frames)
        var back = [Int](repeating: -1, count: frames)
        let tight: Float = 100
        for i in 0..<frames {
            var bestPrev = -Float.infinity
            var bj = -1
            let from = i - Int((period * 2).rounded())
            let to = i - Int((period / 2).rounded())
            if to >= max(0, from) {
                for j in max(0, from)...to {
                    let gap = Float(log(Double(i - j) / period))
                    let v = score[j] - tight * gap * gap
                    if v > bestPrev { bestPrev = v; bj = j }
                }
            }
            score[i] = on[i] + (bj >= 0 ? bestPrev : 0)
            back[i] = bj
        }
        var end = frames - 1
        for i in max(0, frames - Int(period))..<frames where score[i] > score[end] { end = i }
        var beatFrames: [Int] = []
        var i = end
        while i >= 0 { beatFrames.append(i); i = back[i] }
        beatFrames.reverse()
        // Which beat starts each bar: the one of four where the kick lands hardest.
        var hit = [Float](repeating: 0, count: 4)
        for (k, f) in beatFrames.enumerated() {
            hit[k % 4] += lowOnN[f] + lowOnN[min(frames - 1, f + 1)]
        }
        let down = hit.firstIndex(of: hit.max() ?? 0) ?? 0
        let sorted = loudN.sorted()
        return Heard(fps: fps, loud: loudN, low: lowOnN, high: highOnN,
                     beats: beatFrames.map { Double($0) / fps }, down: down,
                     loudTop: sorted[Int(Double(sorted.count) * 0.7)])
    }

    /// 0 to 1 within this song, between two of its percentiles.
    nonisolated private static func scale(_ arr: [Float], _ lo: Double, _ hi: Double) -> [Float] {
        let sorted = arr.sorted()
        let a = sorted[Int(Double(sorted.count) * lo)]
        let b = sorted[min(sorted.count - 1, Int(Double(sorted.count) * hi))]
        let span = b - a == 0 ? 1 : b - a
        return arr.map { max(0, min(1, ($0 - a) / span)) }
    }
}

/// Hears each song once and keeps the last six, as the page does; asks the
/// server only for the song's tempo and energy (AudioMuse's), which the
/// analysis leans on.
@Observable
final class SongListener {
    /// The song's beat length and energy, before and after hearing it.
    private(set) var beat: Double = 1.6
    private(set) var energy: Double = 0.3
    private(set) var heard: Heard?
    private(set) var heardKey: String?

    private let api: API
    private var kept: [(key: String, heard: Heard, beat: Double, energy: Double)] = []
    private var working: String?

    init(api: API) { self.api = api }

    func listen(to item: Item) {
        guard item.kind == "music", working != item.key else { return }
        if let k = kept.first(where: { $0.key == item.key }) {
            (heard, heardKey, beat, energy) = (k.heard, k.key, k.beat, k.energy)
            return
        }
        working = item.key
        heard = nil
        heardKey = nil
        beat = 1.6
        energy = 0.3
        Task {
            defer { if working == item.key { working = nil } }
            var tempo = 0.0
            if let sound = await api.sound(item), sound.tempo > 0 {
                // Very slow or fast tempos are felt at double or half.
                var bpm = sound.tempo
                while bpm < 70 { bpm *= 2 }
                while bpm > 150 { bpm /= 2 }
                tempo = bpm
                beat = 60 / bpm
                energy = max(0, min(1, sound.energy ?? 0))
            }
            guard let file = try? await api.download(item, kbps: 96) else { return }
            let result = await Task.detached(priority: .utility) { await Heard.hear(file: file, tempo: tempo) }.value
            try? FileManager.default.removeItem(at: file)
            guard let result, working == item.key else { return }
            heard = result
            heardKey = item.key
            kept.append((item.key, result, beat, energy))
            if kept.count > 6 { kept.removeFirst() }
        }
    }
}
