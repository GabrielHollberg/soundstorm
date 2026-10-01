import SwiftUI

// Stage three of the page's visualizers: Flow (flowScene) and the rest of
// FULL_SCENES in app.js - Storm, Synthwave, Galaxy, Aurora, Lava. These fill
// the screen, sized to its shorter side (S), with the page's TV counts.

private let TAU = Double.pi * 2
private func full(_ c: CGSize) -> Path { Path(CGRect(origin: .zero, size: c)) }
/// A few alpha levels, so many small marks are drawn as a few paths rather
/// than one each - the page batches the same way for a phone's sake.
private func level(_ a: Double) -> Int { max(0, min(3, Int(a * 4))) }

/// Flow: particles riding a current - a swirl round the middle mixed with a
/// field of waves that tightens when loud - pushed outward by a pulse on the
/// beats that stand out, trails fading behind.
final class FlowScene {
    private struct P { var x, y, vx, vy, life: Double; let c: Int; var trail: [CGPoint] }
    private struct Pulse { var r, life: Double; let big: Bool }
    private var ps: [P] = []
    private var pulses: [Pulse] = []

    private func spawn(_ w: Double, _ h: Double, anywhere: Bool) -> P {
        let c = Int.random(in: 0..<3)
        if anywhere || Double.random(in: 0..<1) < 0.35 {
            return P(x: .random(in: 0..<w), y: .random(in: 0..<h), vx: 0, vy: 0, life: 0.4 + .random(in: 0..<1), c: c, trail: [])
        }
        let a = Double.random(in: 0..<TAU), r = Double.random(in: 0..<1) * min(w, h) * 0.12
        return P(x: w / 2 + cos(a) * r, y: h / 2 + sin(a) * r, vx: 0, vy: 0, life: 0.6 + .random(in: 0..<1), c: c, trail: [])
    }

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let (w, h) = (canvas.width, canvas.height)
        let cx = w / 2, cy = h / 2, S = min(w, h)
        if ps.isEmpty { ps = (0..<260).map { _ in spawn(w, h, anywhere: true) } } // 520 on the page
        if m.newBeat || m.drop {
            pulses.append(Pulse(r: S * 0.05, life: 1, big: m.drop))
            let push = S * (0.15 + 0.5 * m.loud) * (m.drop ? 2.4 : 1) * (0.6 + 0.6 * m.e)
            for i in ps.indices {
                let dx = ps[i].x - cx, dy = ps[i].y - cy
                let d = max(1, hypot(dx, dy))
                let near = exp(-d / (S * 0.6))
                ps[i].vx += dx / d * push * near
                ps[i].vy += dy / d * push * near
            }
        }
        g.blendMode = .plusLighter
        g.fill(full(canvas), with: .radialGradient(
            Gradient(colors: [pal.color(0, (0.1 + 0.3 * m.kick) * (0.4 + 0.6 * m.bright)), pal.color(0, 0)]),
            center: CGPoint(x: cx, y: cy), startRadius: 0, endRadius: S * (0.3 + 0.25 * m.kick)))
        pulses = pulses.compactMap { pl in
            var pl = pl
            pl.r += m.dt * S * (0.8 + 0.7 * m.loud)
            pl.life -= m.dt * 0.7
            guard pl.life > 0 else { return nil }
            let c: Color = pl.big ? .white.opacity(pl.life * 0.45 * (0.4 + 0.6 * m.bright))
                : pal.color(1, pl.life * 0.22 * (0.4 + 0.6 * m.bright))
            g.stroke(Path(ellipseIn: CGRect(x: cx - pl.r, y: cy - pl.r, width: pl.r * 2, height: pl.r * 2)),
                     with: .color(c), lineWidth: pl.big ? 6 : 3)
            return pl
        }
        let speed = S * (0.04 + (0.1 + 0.35 * m.loud) * m.lv * (0.6 + 0.6 * m.e) + 0.25 * m.kick)
        let k = (1.1 + 1.4 * m.loud) / S * .pi
        let sw = 0.45
        for i in ps.indices {
            var p = ps[i]
            let swirl = atan2(p.y - cy, p.x - cx) + .pi / 2
            let n = sin(p.x * k + m.ck * 0.7) + cos(p.y * k * 1.3 - m.ck * 0.5) + sin((p.x + p.y) * k * 0.7 + m.ck * 1.1)
            let na = n * .pi * 0.4
            let fx = cos(na) * (1 - sw) + cos(swirl) * sw
            let fy = sin(na) * (1 - sw) + sin(swirl) * sw
            p.vx *= 1 - m.dt * 2.2
            p.vy *= 1 - m.dt * 2.2
            if p.trail.isEmpty { p.trail.append(CGPoint(x: p.x, y: p.y)) }
            p.x += (fx * speed + p.vx) * m.dt
            p.y += (fy * speed + p.vy) * m.dt
            p.trail.append(CGPoint(x: p.x, y: p.y))
            // The page's trail canvas fades by a tenth a frame.
            if p.trail.count > 12 { p.trail.removeFirst() }
            p.life -= m.dt * 0.22
            if p.life <= 0 || p.x < -20 || p.y < -20 || p.x > w + 20 || p.y > h + 20 { p = spawn(w, h, anywhere: false) }
            ps[i] = p
        }
        let alpha = (0.35 + 0.45 * m.lv) * (0.4 + 0.6 * m.bright)
        for c in 0..<3 {
            // Older halves of the trails fainter, as the fade leaves them.
            for (from, fade) in [(0, 0.45), (6, 1.0)] {
                var path = Path()
                for p in ps where p.c == c && p.trail.count > from + 1 {
                    path.addLines(Array(p.trail[from...min(p.trail.count - 1, from + 6)]))
                }
                g.stroke(path, with: .color(pal.color(c, alpha * fade)),
                         style: StrokeStyle(lineWidth: 1.4 + 1.8 * m.snare, lineCap: .round, lineJoin: .round))
            }
        }
        g.blendMode = .normal
    }
}

/// Synthwave: a neon grid racing towards you under a striped sunset sun that
/// pulses on the kick, with mountains on the horizon.
final class SynthwaveScene {
    private let stars: [(x: Double, y: Double, b: Double)] = (0..<90).map { _ in (.random(in: 0..<1), .random(in: 0..<0.42), .random(in: 0..<1)) }
    private static func ridge(_ n: Int, _ rough: Double) -> [Double] {
        (0...n).map { i -> Double in
            let x = Double(i)
            let jag: Double = abs(sin(x * 1.7 + rough))
            let roll: Double = 0.5 + 0.5 * sin(x * 0.6 + rough * 2)
            return 0.35 + 0.65 * jag * roll
        }
    }
    private let far = ridge(18, 1.3), near = ridge(12, 4.1)
    private var scroll = 0.0

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let (w, h) = (canvas.width, canvas.height)
        let S = min(w, h), hz = h * 0.46
        var starPaths = [Path](repeating: Path(), count: 4)
        for s in stars {
            let a = (0.25 + 0.5 * s.b * (0.5 + 0.5 * sin(m.ck * 2 + s.x * 40))) * m.bright
            starPaths[level(a)].addRect(CGRect(x: s.x * w, y: s.y * h, width: 2.2, height: 2.2))
        }
        for (i, p) in starPaths.enumerated() { g.fill(p, with: .color(.white.opacity((Double(i) + 0.5) / 4))) }
        // The sun, striped across its lower half.
        let r = S * 0.24 * (1 + 0.06 * m.kick)
        let sy = hz - r * 0.35
        let sunRect = CGRect(x: w / 2 - r, y: sy - r, width: r * 2, height: r * 2)
        g.drawLayer { layer in
            layer.fill(Path(ellipseIn: sunRect), with: .linearGradient(
                Gradient(colors: [pal.color(0, 0.95), pal.color(1, 0.95)]),
                startPoint: CGPoint(x: 0, y: sy - r), endPoint: CGPoint(x: 0, y: sy + r)))
            layer.blendMode = .destinationOut
            for i in 0..<7 {
                let t = (Double(i) / 7 + m.ck * 0.05).truncatingRemainder(dividingBy: 1)
                layer.fill(Path(CGRect(x: w / 2 - r, y: sy + r * t, width: r * 2, height: r * 0.02 + r * 0.09 * t)), with: .color(.black))
            }
        }
        g.fill(Path(CGRect(x: 0, y: 0, width: w, height: hz)), with: .radialGradient(
            Gradient(colors: [pal.color(0, (0.25 + 0.3 * m.kick) * m.bright), pal.color(0, 0)]),
            center: CGPoint(x: w / 2, y: sy), startRadius: r * 0.8, endRadius: r * 2.2))
        // Mountains, far then near.
        for (ridge, height, alpha) in [(far, 0.13, 0.75), (near, 0.09, 0.92)] {
            var p = Path()
            p.move(to: CGPoint(x: 0, y: hz))
            for (i, v) in ridge.enumerated() { p.addLine(to: CGPoint(x: Double(i) / Double(ridge.count - 1) * w, y: hz - v * h * height)) }
            p.addLine(to: CGPoint(x: w, y: hz))
            p.closeSubpath()
            g.fill(p, with: .color(Color(red: 12 / 255, green: 6 / 255, blue: 24 / 255).opacity(alpha)))
        }
        // The floor, and its grid.
        g.fill(Path(CGRect(x: 0, y: hz, width: w, height: h - hz)), with: .color(Color(red: 10 / 255, green: 4 / 255, blue: 20 / 255).opacity(0.85)))
        scroll = (scroll + m.dt * (0.25 + 1.1 * m.loud * m.lv + 0.6 * m.kick)).truncatingRemainder(dividingBy: 1)
        let line = pal.color(2, (0.55 + 0.35 * m.kick) * (0.5 + 0.5 * m.bright))
        var rays = Path()
        for i in -14...14 {
            rays.move(to: CGPoint(x: w / 2 + Double(i) * w * 0.04, y: hz))
            rays.addLine(to: CGPoint(x: w / 2 + Double(i) * w * 0.5, y: h))
        }
        g.stroke(rays, with: .color(line), lineWidth: 2)
        for k in 0..<14 {
            let t = (Double(k) / 14 + scroll).truncatingRemainder(dividingBy: 1)
            let y = hz + (h - hz) * t * t
            var across = Path()
            across.move(to: CGPoint(x: 0, y: y))
            across.addLine(to: CGPoint(x: w, y: y))
            g.stroke(across, with: .color(line), lineWidth: 0.9 + 3 * t)
        }
        // A glow along the horizon.
        g.fill(Path(CGRect(x: 0, y: hz - h * 0.03, width: w, height: h * 0.08)), with: .linearGradient(
            Gradient(stops: [.init(color: pal.color(2, 0), location: 0), .init(color: pal.color(2, 0.5 * m.bright), location: 0.5),
                             .init(color: pal.color(2, 0), location: 1)]),
            startPoint: CGPoint(x: 0, y: hz - h * 0.03), endPoint: CGPoint(x: 0, y: hz + h * 0.05)))
    }
}

/// Galaxy: a spiral turning round the middle, inner stars faster than outer,
/// arms winding tighter when loud, the core flashing on the kick and a ripple
/// on each drop.
final class GalaxyScene {
    private struct Star { let r: Double; let arm: Int; let off: Double; let c: Int; let b: Double }
    private let stars: [Star] = (0..<450).map { i in // 900 on the page
        Star(r: sqrt(.random(in: 0..<1)), arm: i % 3, off: (.random(in: 0..<1) - 0.5) * 0.6, c: i % 3, b: .random(in: 0..<1))
    }
    private let background: [(x: Double, y: Double, b: Double)] = (0..<120).map { _ in (.random(in: 0..<1), .random(in: 0..<1), .random(in: 0..<1)) }
    private var turn = 0.0
    private var ripples: [(r: Double, life: Double)] = []

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let (w, h) = (canvas.width, canvas.height)
        let cx = w / 2, cy = h / 2, S = min(w, h)
        var bg = [Path](repeating: Path(), count: 4)
        for s in background { bg[level(0.15 + 0.35 * s.b)].addRect(CGRect(x: s.x * w, y: s.y * h, width: 1.5, height: 1.5)) }
        for (i, p) in bg.enumerated() { g.fill(p, with: .color(.white.opacity((Double(i) + 0.5) / 4))) }
        turn += m.dt * (0.12 + 0.5 * m.loud * m.lv)
        let R = S * 0.46 * (1 + 0.06 * m.kick)
        let wind = 3.2 + 1.8 * m.loud
        g.blendMode = .plusLighter
        g.fill(full(canvas), with: .radialGradient(
            Gradient(colors: [Color(red: 1, green: 245 / 255, blue: 235 / 255).opacity((0.35 + 0.5 * m.kick) * (0.5 + 0.5 * m.bright)), pal.color(0, 0)]),
            center: CGPoint(x: cx, y: cy), startRadius: 0, endRadius: R * (0.35 + 0.2 * m.kick)))
        var groups = [[Path]](repeating: [Path](repeating: Path(), count: 4), count: 3)
        for p in stars {
            let a = Double(p.arm) * TAU / 3 + p.r * wind + turn / (0.25 + p.r) + p.off / (0.4 + p.r)
            let x = cx + cos(a) * p.r * R, y = cy + sin(a) * p.r * R * 0.8
            // The page's 0.8 to 2.4 canvas pixels come out bigger on a TV's
            // screen than points do here; this is that size.
            let sz = 2.4 + 3.6 * p.b
            let alpha = (0.35 + 0.55 * p.b) * (1 - p.r * 0.45)
            groups[p.c][level(alpha)].addRect(CGRect(x: x, y: y, width: sz, height: sz))
        }
        for c in 0..<3 {
            for (i, p) in groups[c].enumerated() {
                g.fill(p, with: .color(pal.color(c, (Double(i) + 0.5) / 4 * (0.45 + 0.55 * m.bright))))
            }
        }
        if m.drop { ripples.append((R * 0.2, 1)) }
        ripples = ripples.compactMap { rp in
            let r = rp.r + m.dt * S * 0.7, life = rp.life - m.dt * 0.9
            guard life > 0 else { return nil }
            g.stroke(Path(ellipseIn: CGRect(x: cx - r, y: cy - r * 0.8, width: r * 2, height: r * 1.6)),
                     with: .color(pal.color(1, life * 0.4 * m.bright)), lineWidth: 3)
            return (r, life)
        }
        g.blendMode = .normal
    }
}

/// Aurora: curtains of northern lights swaying over a starry sky, flaring on
/// the beat and brighter when loud.
final class AuroraScene {
    private let stars: [(x: Double, y: Double, b: Double, t: Double)] = (0..<140).map { _ in
        (.random(in: 0..<1), .random(in: 0..<0.8), .random(in: 0..<1), .random(in: 0..<6))
    }

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let (w, h) = (canvas.width, canvas.height)
        var sky = [Path](repeating: Path(), count: 4)
        for s in stars {
            let a = (0.2 + 0.5 * s.b * (0.5 + 0.5 * sin(m.ck * 1.5 + s.t))) * m.bright
            sky[level(a)].addRect(CGRect(x: s.x * w, y: s.y * h, width: 2.1, height: 2.1))
        }
        for (i, p) in sky.enumerated() { g.fill(p, with: .color(.white.opacity((Double(i) + 0.5) / 4))) }
        g.blendMode = .plusLighter
        let steps = 60
        for c in 0..<3 {
            let cc = Double(c)
            let base = h * (0.16 + cc * 0.07)
            let depth = h * (0.22 + 0.22 * m.loud * m.lv + 0.08 * m.kick) * (1 - cc * 0.15)
            func topAt(_ u: Double) -> Double {
                base + h * 0.05 * sin(u * 6 + m.ck * (0.5 + cc * 0.2) + cc) + h * 0.03 * sin(u * 13 - m.ck * 0.8 + cc * 2)
            }
            let glow = (0.28 + 0.4 * m.kick + 0.12 * m.snare) * (0.35 + 0.65 * m.bright)
            var curtain = Path()
            for i in 0...steps {
                let u = Double(i) / Double(steps)
                let p = CGPoint(x: u * w, y: topAt(u))
                if i == 0 { curtain.move(to: p) } else { curtain.addLine(to: p) }
            }
            for i in stride(from: steps, through: 0, by: -1) {
                let u = Double(i) / Double(steps)
                curtain.addLine(to: CGPoint(x: u * w, y: topAt(u) + depth * (0.75 + 0.25 * sin(u * 9 + m.ck + cc))))
            }
            curtain.closeSubpath()
            g.fill(curtain, with: .linearGradient(
                Gradient(stops: [.init(color: pal.color(c, 0), location: 0), .init(color: pal.color(c, glow * 0.9), location: 0.25),
                                 .init(color: pal.color(c, glow * 0.35), location: 0.7), .init(color: pal.color(c, 0), location: 1)]),
                startPoint: CGPoint(x: 0, y: base - h * 0.08), endPoint: CGPoint(x: 0, y: base + depth + h * 0.08)))
            // The folds: faint rays hanging from the top edge.
            var folds = [Path](repeating: Path(), count: 4)
            for i in 0...50 {
                let u = Double(i) / 50
                let y = topAt(u)
                let a = 0.5 + 0.5 * sin(u * 40 + m.ck * 2 + cc * 3)
                folds[level(a)].move(to: CGPoint(x: u * w, y: y))
                folds[level(a)].addLine(to: CGPoint(x: u * w, y: y + depth * 0.8))
            }
            for (i, p) in folds.enumerated() {
                g.stroke(p, with: .color(pal.color(c, glow * 0.35 * (Double(i) + 0.5) / 4)), lineWidth: 3)
            }
        }
        g.blendMode = .normal
    }
}

/// Lava: big soft glowing blobs rising and wobbling like a lava lamp,
/// bouncing on the kick, with small bubbles popping on the snare.
final class LavaScene {
    private struct Blob { var x, y: Double; let r, v, ph: Double; let c: Int }
    private struct Bubble { let x: Double; var y: Double; let r: Double; var life: Double; let c: Int }
    private var blobs: [Blob] = []
    private var bubbles: [Bubble] = []
    private var lastSnare = 0.0

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let (w, h) = (canvas.width, canvas.height)
        let S = min(w, h)
        if blobs.isEmpty {
            blobs = (0..<9).map { i in
                Blob(x: .random(in: 0..<w), y: .random(in: 0..<h), r: S * (0.12 + .random(in: 0..<0.14)),
                     v: 0.3 + .random(in: 0..<0.7), ph: .random(in: 0..<6), c: i % 3)
            }
        }
        g.blendMode = .plusLighter
        let rise = h * (0.03 + 0.12 * m.loud * m.lv) * m.dt
        for i in blobs.indices {
            blobs[i].y -= rise * blobs[i].v
            if blobs[i].y < -blobs[i].r * 1.5 { blobs[i].y = h + blobs[i].r * 1.5; blobs[i].x = .random(in: 0..<w) }
            let b = blobs[i]
            let x = b.x + sin(m.ck * 0.4 * b.v + b.ph) * S * 0.08
            let r = b.r * (1 + 0.08 * sin(m.ck * 1.3 + b.ph) + 0.18 * m.kick)
            let k = 0.45 + 0.55 * m.bright
            g.fill(Path(ellipseIn: CGRect(x: x - r, y: b.y - r, width: r * 2, height: r * 2)), with: .radialGradient(
                Gradient(stops: [.init(color: pal.color(b.c, 0.75 * k), location: 0), .init(color: pal.color(b.c, 0.45 * k), location: 0.55),
                                 .init(color: pal.color(b.c, 0), location: 1)]),
                center: CGPoint(x: x, y: b.y), startRadius: 0, endRadius: r))
        }
        if m.snare - lastSnare > 0.3 && m.lv > 0.3 {
            for _ in 0..<6 {
                bubbles.append(Bubble(x: .random(in: 0..<w), y: h * (0.6 + .random(in: 0..<0.4)), r: 3 + .random(in: 0..<7.5),
                                      life: 1, c: .random(in: 0..<3)))
            }
        }
        lastSnare = m.snare
        bubbles = bubbles.compactMap { bb in
            var bb = bb
            bb.life -= m.dt * 0.9
            bb.y -= h * 0.25 * m.dt
            guard bb.life > 0 else { return nil }
            let r = bb.r * (1 + (1 - bb.life) * 0.6)
            g.stroke(Path(ellipseIn: CGRect(x: bb.x - r, y: bb.y - r, width: r * 2, height: r * 2)),
                     with: .color(pal.color(bb.c, bb.life * 0.8 * m.bright)), lineWidth: 1.8)
            return bb
        }
        g.blendMode = .normal
    }
}
