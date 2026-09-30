import SwiftUI
import UIKit

/// The Now Playing looks the TV draws itself, as the page draws them
/// (VIZ_SCENES in app.js): the same shapes, counts and responses to the
/// music, in SwiftUI's Canvas at 30 frames a second - the page's rate on a TV,
/// and its halved particle counts (VIZ_DENSITY).
enum Looks {
    static let covers: [(key: String, label: String)] = [
        ("lyrics", "Lyrics"), ("square", "Cover"), ("spin", "Spinning disc"), ("vinyl", "Record"),
    ]
    /// The visualizers drawn so far; the page's others (Warp, Waves,
    /// Kaleidoscope, Fireworks, Flow, Storm, Synthwave, Galaxy, Aurora, Lava)
    /// are still to come.
    static let visualizers: [(key: String, label: String)] = [("pulse", "Orb"), ("bars", "Spectrum")]
    static let all = Set((covers + visualizers).map(\.key))
    static let pageVisualizers: Set<String> = ["pulse", "bars", "warp", "waves", "kaleido", "fireworks",
                                                "flow", "storm", "synthwave", "galaxy", "aurora", "lava"]

    /// The account's look as the TV can show it: one of the page's
    /// visualizers not drawn here yet is shown as the Orb.
    static func shown(_ key: String?) -> String {
        guard let key else { return "lyrics" }
        if all.contains(key) { return key }
        return pageVisualizers.contains(key) ? "pulse" : "lyrics"
    }

    static func isVisualizer(_ key: String) -> Bool { visualizers.contains { $0.key == key } }
}

/// Three colours from the cover, the page's coverPalette: hues in twelve bins
/// weighted by how vivid they are, the top three averaged and lifted.
struct VizPalette {
    var colors: [(r: Double, g: Double, b: Double)] = [(255, 255, 255), (200, 220, 255), (255, 210, 230)]

    init() {}

    init(image: UIImage) {
        let side = 16
        var px = [UInt8](repeating: 0, count: side * side * 4)
        guard let cg = image.cgImage,
              let ctx = CGContext(data: &px, width: side, height: side, bitsPerComponent: 8, bytesPerRow: side * 4,
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        else { return }
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: side, height: side))
        var bins = [(w: Double, r: Double, g: Double, b: Double)](repeating: (0, 0, 0, 0), count: 12)
        for i in stride(from: 0, to: px.count, by: 4) {
            let (R, G, B) = (Double(px[i]), Double(px[i + 1]), Double(px[i + 2]))
            let r = R / 255, g = G / 255, b = B / 255
            let mx = max(r, g, b), mn = min(r, g, b)
            let sat = mx > 0 ? (mx - mn) / mx : 0
            var h = 0.0
            if mx != mn {
                if mx == r { h = ((g - b) / (mx - mn)).truncatingRemainder(dividingBy: 6) }
                else if mx == g { h = (b - r) / (mx - mn) + 2 }
                else { h = (r - g) / (mx - mn) + 4 }
            }
            let bin = Int(((h * 60 + 360).truncatingRemainder(dividingBy: 360)) / 30) % 12
            let w = 0.15 + sat * mx
            bins[bin].w += w; bins[bin].r += R * w; bins[bin].g += G * w; bins[bin].b += B * w
        }
        let top = bins.filter { $0.w > 0 }.sorted { $0.w > $1.w }.prefix(3)
        var out = top.map { x -> (r: Double, g: Double, b: Double) in
            let c = [x.r / x.w, x.g / x.w, x.b / x.w]
            let lift = min(3, 230 / max(c.max() ?? 1, 1))
            return (min(255, c[0] * lift), min(255, c[1] * lift), min(255, c[2] * lift))
        }
        while out.count < 3 {
            out.append(out.first.map { ($0.r + (255 - $0.r) * 0.5, $0.g + (255 - $0.g) * 0.5, $0.b + (255 - $0.b) * 0.5) }
                       ?? (255, 255, 255))
        }
        colors = out
    }

    func color(_ i: Int, _ a: Double) -> Color {
        let c = colors[((i % 3) + 3) % 3]
        return Color(red: c.r / 255, green: c.g / 255, blue: c.b / 255).opacity(max(0, min(1, a)))
    }
}

/// A visualizer, the whole screen. The engine reads the music each frame; the
/// scene keeps its own particles between frames.
struct VisualizerView: View {
    let look: String
    let palette: VizPalette
    let player: Player
    let listener: SongListener
    @State private var engine = VizEngine()
    @State private var orb = OrbScene()
    @State private var bars = BarsScene()

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 30)) { timeline in
            Canvas { g, size in
                let heard = listener.heardKey == player.current?.key ? listener.heard : nil
                let m = engine.step(now: timeline.date.timeIntervalSinceReferenceDate, t: player.exactTime,
                                    playing: player.isPlaying, beat: listener.beat, energy: listener.energy, heard: heard)
                switch look {
                case "bars": bars.draw(&g, size, m, palette)
                default: orb.draw(&g, size, m, palette)
                }
            }
        }
        .background(Color.black)
    }
}

private let TAU = Double.pi * 2

/// Orb: a glowing orb in the cover's colours that morphs and swells with the
/// music, light rays, rippling rings, a shock ring on a drop, and a vortex of
/// particles thrown outward on the beats that stand out.
final class OrbScene {
    private struct Layer { let spin: Double; let waves: [(k: Double, speed: Double, ph: Double)] }
    private let layers: [Layer] = (0..<6).map { l in
        let sign: Double = l % 2 == 1 ? -1 : 1
        var waves: [(k: Double, speed: Double, ph: Double)] = []
        for (j, k) in [2, 3, 5, 7].enumerated() {
            let way: Double = j % 2 == 1 ? -1 : 1
            let speed = (0.4 + Double((l * 3 + j * 5) % 7) * 0.13) * way
            waves.append((Double(k + l % 3), speed, Double(l) * 1.3 + Double(j) * 2.1))
        }
        return Layer(spin: sign * (0.05 + Double(l) * 0.025), waves: waves)
    }
    private struct Dot { var a, r, home, v, spin, size: Double; let c: Int; var trail: [CGPoint] }
    private var dots: [Dot] = []
    private var lastBeat = -1

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let (w, h) = (canvas.width, canvas.height)
        let cx = w / 2, cy = h / 2
        let size = min(w, h) * 0.85
        let R0 = size * 0.3
        let (kick, snare, drive, bright, lv, ck, e) = (m.kick, m.snare, m.drive, m.bright, m.lv, m.ck, m.e)
        g.blendMode = .plusLighter

        // A nebula behind it, in the cover's colours.
        for i in 0..<3 {
            let ang = ck * 0.3 + Double(i) * 2.1
            let p = CGPoint(x: cx + cos(ang) * R0 * 0.9, y: cy + sin(ang * 1.2) * R0 * 0.9)
            let r = R0 * (2.2 + 0.5 * kick * drive)
            g.fill(Path(CGRect(origin: .zero, size: canvas)), with: .radialGradient(
                Gradient(colors: [pal.color(i, 0.22 * (0.4 + 0.6 * lv) * bright), pal.color(i, 0)]),
                center: p, startRadius: 0, endRadius: r))
        }
        // Light rays turning slowly, brighter on the beat.
        for i in 0..<18 {
            let a0 = Double(i) / 18 * TAU + ck * 0.12
            let len = R0 * (2.4 + 0.8 * sin(ck * 1.3 + Double(i) * 1.9) + 1.2 * kick * drive)
            var wedge = Path()
            wedge.move(to: CGPoint(x: cx, y: cy))
            wedge.addArc(center: CGPoint(x: cx, y: cy), radius: len, startAngle: .radians(a0 - 0.05),
                         endAngle: .radians(a0 + 0.05), clockwise: false)
            wedge.closeSubpath()
            g.fill(wedge, with: .linearGradient(
                Gradient(colors: [pal.color(i, 0.16 * (0.3 + kick)), pal.color(i, 0)]),
                startPoint: CGPoint(x: cx, y: cy), endPoint: CGPoint(x: cx + cos(a0) * len, y: cy + sin(a0) * len)))
        }
        // The orb: six layers, each a closed shape whose edge is a sum of
        // waves travelling round it; the beat swells them, the snare spikes them.
        let pts = 120
        for (l, layer) in layers.enumerated() {
            let R = R0 * (0.72 + Double(l) * 0.085) * (1 + 0.22 * kick * drive)
            let amp = 0.05 + 0.06 * e + 0.1 * kick * drive
            let spike = 0.07 * snare * drive
            var shape = Path()
            for p in 0...pts {
                let th = Double(p) / Double(pts) * TAU
                var r = 1.0
                for wv in layer.waves { r += amp / Double(layer.waves.count) * 2 * sin(wv.k * th + wv.speed * ck * 2 + wv.ph) }
                r += spike * sin(13 * th + ck * 6 + Double(l))
                let a = th + layer.spin * ck * 2
                let pt = CGPoint(x: cx + cos(a) * R * r, y: cy + sin(a) * R * r)
                if p == 0 { shape.move(to: pt) } else { shape.addLine(to: pt) }
            }
            shape.closeSubpath()
            g.fill(shape, with: .radialGradient(
                Gradient(stops: [.init(color: pal.color(l, 0.05 * bright), location: 0),
                                 .init(color: pal.color(l, (0.2 + 0.1 * lv) * bright), location: 0.6),
                                 .init(color: pal.color(l, 0.04 * bright), location: 1)]),
                center: CGPoint(x: cx, y: cy), startRadius: R * 0.15, endRadius: R * 1.25))
        }
        // A bright core that flashes on the kick.
        g.fill(Path(CGRect(origin: .zero, size: canvas)), with: .radialGradient(
            Gradient(colors: [.white.opacity(0.1 + 0.1 * bright + 0.4 * kick), .white.opacity(0)]),
            center: CGPoint(x: cx, y: cy), startRadius: 0, endRadius: R0 * (0.9 + 0.5 * kick)))
        // Thin rings round it, rippling fast.
        for i in 0..<3 {
            let R = R0 * (1.35 + Double(i) * 0.16) * (1 + 0.12 * kick * drive)
            var ring = Path()
            for p in 0...pts {
                let th = Double(p) / Double(pts) * TAU
                let r = 1 + (0.015 + 0.05 * snare * drive) * sin(Double(9 + i * 3) * th - ck * Double(3 + i))
                    + 0.02 * sin(4 * th + ck * 1.7 + Double(i))
                let pt = CGPoint(x: cx + cos(th) * R * r, y: cy + sin(th) * R * r)
                if p == 0 { ring.move(to: pt) } else { ring.addLine(to: pt) }
            }
            ring.closeSubpath()
            g.stroke(ring, with: .color(pal.color(i + 1, (0.35 + 0.35 * lv) * bright)), lineWidth: 2)
        }
        // A white shock ring and a flash, for a drop only.
        if m.dropEnv > 0 && lv > 0.05 {
            let p = 1 - m.dropEnv
            let r = R0 * (1.2 + p * 2.6)
            g.stroke(Path(ellipseIn: CGRect(x: cx - r, y: cy - r, width: r * 2, height: r * 2)),
                     with: .color(.white.opacity(m.dropEnv * 0.7 * lv)), lineWidth: 3 + 9 * m.dropEnv)
            g.fill(Path(CGRect(origin: .zero, size: canvas)), with: .radialGradient(
                Gradient(colors: [.white.opacity(m.dropEnv * m.dropEnv * 0.45), .white.opacity(0)]),
                center: CGPoint(x: cx, y: cy), startRadius: 0, endRadius: R0 * 3))
        }

        // The vortex: thrown outward on a beat that stands out, sprung back.
        if dots.isEmpty {
            for i in 0..<120 { // 240 on the page, halved on a TV
                let home = 1.25 + Double.random(in: 0..<1.3)
                dots.append(Dot(a: .random(in: 0..<TAU), r: home, home: home, v: 0, spin: 0.25 + .random(in: 0..<0.6),
                                size: 0.8 + .random(in: 0..<1.8), c: i % 3, trail: []))
            }
        }
        if m.playing && m.beatNo != lastBeat && (m.novelty > 0.4 || m.downbeat) {
            lastBeat = m.beatNo
            let push = (0.4 + 0.9 * e) * (0.6 + 1.4 * m.novelty) * (0.3 + 0.9 * m.loud)
            for i in dots.indices { dots[i].v += push * (0.4 + .random(in: 0..<0.8)) }
        }
        for i in dots.indices {
            var d = dots[i]
            d.v += ((d.home - d.r) * 9 - d.v * 3.2) * m.dt
            d.r += d.v * m.dt
            d.a += d.spin / d.r * m.dt * (0.6 + 1.4 * lv) * (1 + kick)
            let rr = R0 * d.r
            d.trail.append(CGPoint(x: cx + cos(d.a) * rr, y: cy + sin(d.a) * rr * 0.92))
            // The page fades its trail canvas by a fifth each frame; the TV
            // keeps the last few places and fades them the same way.
            if d.trail.count > 7 { d.trail.removeFirst() }
            dots[i] = d
        }
        let streak = (0.55 + 0.4 * lv) * (0.45 + 0.55 * bright)
        for c in 0..<3 {
            for big in [false, true] {
                for age in 0..<6 {
                    var path = Path()
                    for d in dots where d.c == c && (d.size >= 1.7) == big && d.trail.count > age + 1 {
                        let n = d.trail.count
                        path.move(to: d.trail[n - 2 - age])
                        path.addLine(to: d.trail[n - 1 - age])
                    }
                    g.stroke(path, with: .color(pal.color(c, streak * pow(0.8, Double(age)))),
                             style: StrokeStyle(lineWidth: (big ? 3.5 : 2) * (1 + kick * 0.6), lineCap: .round))
                }
            }
        }
        g.blendMode = .normal
    }
}

/// Spectrum: a mirrored equalizer across the screen, bass in the middle
/// jumping with the kick, highs at the edges snapping with the snare, peak
/// caps falling slowly, and a reflection below.
final class BarsScene {
    private let half = 24
    private lazy var v = [Double](repeating: 0, count: half)
    private lazy var peak = [Double](repeating: 0, count: half)

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let (w, h) = (canvas.width, canvas.height)
        let cx = w / 2, cy = h / 2
        let size = min(w, h) * 0.85
        let span = size * 0.56
        let base = cy + size * 0.18
        let bw = span / Double(half)
        g.blendMode = .plusLighter
        g.fill(Path(CGRect(origin: .zero, size: canvas)), with: .radialGradient(
            Gradient(colors: [pal.color(0, 0.25 * m.bright * (0.4 + 0.6 * m.lv)), pal.color(0, 0)]),
            center: CGPoint(x: cx, y: base), startRadius: 0, endRadius: size * 0.9))
        for i in 0..<half {
            let fq = Double(i) / Double(half - 1)
            let low = exp(-fq * 3.5)
            let high = pow(fq, 1.4)
            let wander = 0.5 + 0.5 * sin(m.ck * (2.1 + Double(i % 5) * 0.37) + Double(i) * 1.9)
            let target = m.lv * (low * m.kick * 1.1 + high * m.snare * 0.9
                + (0.15 + 0.55 * m.loud) * (0.35 + 0.65 * wander) * (1 - 0.4 * fq)) * m.drive
            v[i] = target > v[i] ? v[i] + (target - v[i]) * 0.6 : max(target, v[i] - m.dt * 1.6)
            peak[i] = max(peak[i] - m.dt * 0.45, v[i])
        }
        for side in [-1.0, 1.0] {
            for i in 0..<half {
                let x = cx + side * (Double(i) + 0.5) * bw - bw * 0.36
                let bh = max(size * 0.012, v[i] * size * 0.62)
                g.fill(Path(CGRect(x: x, y: base - bh, width: bw * 0.72, height: bh)), with: .linearGradient(
                    Gradient(colors: [pal.color(i, 0.95 * (0.5 + 0.5 * m.bright)), pal.color(i + 1, 0.55 * (0.5 + 0.5 * m.bright))]),
                    startPoint: CGPoint(x: x, y: base - bh), endPoint: CGPoint(x: x, y: base)))
                g.fill(Path(CGRect(x: x, y: base + size * 0.02, width: bw * 0.72, height: bh * 0.35)),
                       with: .color(pal.color(i, 0.16 * m.bright)))
                g.fill(Path(CGRect(x: x, y: base - peak[i] * size * 0.62 - size * 0.02, width: bw * 0.72, height: size * 0.01)),
                       with: .color(.white.opacity(0.5 + 0.4 * m.bright)))
            }
        }
        g.blendMode = .normal
    }
}

/// The cover, turning once every twenty seconds while playing (Spinning disc),
/// or as a record: grooves, the cover as its label, turning every four.
struct TurningCover: View {
    let url: URL?
    let record: Bool
    let player: Player
    @State private var clock = TurnClock()

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 30, paused: !player.isPlaying)) { timeline in
            let turn = clock.angle(at: timeline.date, playing: player.isPlaying, period: record ? 4 : 20)
            ZStack {
                if record {
                    Circle().fill(Color(white: 0.06))
                    ForEach(0..<14, id: \.self) { i in
                        Circle().stroke(Color.white.opacity(0.05), lineWidth: 1).padding(CGFloat(18 + i * 13))
                    }
                    Cover(url: url).clipShape(Circle()).padding(560 * 0.26)
                    Circle().fill(Color.black).frame(width: 560 * 0.044)
                } else {
                    Cover(url: url).clipShape(Circle())
                }
            }
            .rotationEffect(.radians(turn))
        }
    }

}

/// How far round, kept between frames, advancing only while playing.
final class TurnClock {
    private var angle = 0.0
    private var last: Date?

    func angle(at now: Date, playing: Bool, period: Double) -> Double {
        let dt = last.map { min(0.1, now.timeIntervalSince($0)) } ?? 0
        last = now
        if playing { angle += dt * TAU / period }
        return angle
    }
}
