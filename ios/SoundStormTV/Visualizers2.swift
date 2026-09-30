import SwiftUI

// Stage two of the page's visualizers (VIZ_SCENES in app.js): Warp, Waves,
// Kaleidoscope and Fireworks, ported with their counts and responses. Their
// size is the page's for these (the shorter side times 0.85).

private let TAU = Double.pi * 2
private func full(_ c: CGSize) -> Path { Path(CGRect(origin: .zero, size: c)) }

/// Warp: a hyperspace tunnel - stars streaking past, faster as it gets louder
/// and on every kick, and turning rings flashing on the first beat of a bar.
final class WarpScene {
    private struct Star { var x, y, z: Double; let c: Int }
    private var stars: [Star] = (0..<160).map { _ in // 320 on the page, halved on a TV
        Star(x: .random(in: -1..<1), y: .random(in: -1..<1), z: .random(in: 0..<1), c: .random(in: 0..<3))
    }
    private var rings: [Double] = (0..<9).map { Double($0) / 9 }

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let cx = canvas.width / 2, cy = canvas.height / 2
        let size = min(canvas.width, canvas.height) * 0.85
        let speed = (0.08 + (0.35 + 1.4 * m.loud) * m.lv * (0.6 + 0.6 * m.e) + 1.2 * m.kick + 2 * m.dropEnv) * m.dt
        let focal = size * 0.32
        // A flash in the middle on the kick.
        g.fill(full(canvas), with: .radialGradient(
            Gradient(colors: [.white.opacity(0.08 + 0.45 * m.kick), .white.opacity(0)]),
            center: CGPoint(x: cx, y: cy), startRadius: 0, endRadius: size * (0.35 + 0.3 * m.kick)))
        g.blendMode = .plusLighter
        // The tunnel: hexagons coming towards you, turning.
        for i in rings.indices {
            rings[i] -= speed * 0.35
            if rings[i] <= 0.05 { rings[i] += 1 }
            let z = rings[i]
            let r = focal * 0.9 / z
            let turn = m.ck * 0.5 + Double(i) * 0.35
            var hex = Path()
            for k in 0...6 {
                let a = turn + Double(k) / 6 * TAU
                let p = CGPoint(x: cx + cos(a) * r, y: cy + sin(a) * r)
                if k == 0 { hex.move(to: p) } else { hex.addLine(to: p) }
            }
            g.stroke(hex, with: .color(pal.color(i, min(1, (1 - z) * 1.2) * (0.25 + 0.5 * m.bright))),
                     lineWidth: 1.5 + 6 * m.dropEnv + 3 * (1 - z))
        }
        // The stars: each a streak from where it was a moment ago.
        for i in stars.indices {
            let s = stars[i]
            let z0 = s.z
            stars[i].z -= speed
            if stars[i].z <= 0.02 {
                stars[i] = Star(x: .random(in: -1..<1), y: .random(in: -1..<1), z: 1, c: s.c)
                continue
            }
            let z1 = stars[i].z
            var line = Path()
            line.move(to: CGPoint(x: cx + s.x / z0 * focal, y: cy + s.y / z0 * focal))
            line.addLine(to: CGPoint(x: cx + s.x / z1 * focal, y: cy + s.y / z1 * focal))
            g.stroke(line, with: .color(pal.color(s.c, min(1, (1 - z1) * 1.4) * (0.4 + 0.6 * m.bright))),
                     style: StrokeStyle(lineWidth: 1 + 3.3 * (1 - z1), lineCap: .round))
        }
        g.blendMode = .normal
    }
}

/// Waves: glowing ribbons across the screen, each the space between two
/// travelling waves, so it twists. Loudness raises them, a kick bulges them,
/// the snare ripples them.
final class WavesScene {
    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let cx = canvas.width / 2, cy = canvas.height / 2
        let size = min(canvas.width, canvas.height) * 0.85
        let x0 = cx - size * 0.9, x1 = cx + size * 0.9
        let steps = 90
        g.blendMode = .plusLighter
        for r in 0..<5 {
            let rr = Double(r)
            let amp = size * (0.05 + (0.1 + 0.22 * m.loud) * m.lv * m.drive * 0.8 + 0.16 * m.kick) * (1 - rr * 0.11)
            let k = 2.2 + rr * 0.7
            let sp = (0.8 + rr * 0.35) * (r % 2 == 1 ? -1 : 1)
            let ripple = 0.25 * m.snare
            func wave(_ u: Double, _ ph: Double) -> Double {
                cy + amp * sin(.pi * u) * (sin(k * u * TAU + m.ck * sp * 2 + ph + rr) + ripple * sin(u * 40 + m.ck * 9 + rr))
            }
            let lift = 0.9 + 0.3 * sin(m.ck + rr)
            let xAt = { (p: Int) in x0 + (x1 - x0) * Double(p) / Double(steps) }
            var top = Path(), ribbon = Path()
            for p in 0...steps {
                let pt = CGPoint(x: xAt(p), y: wave(Double(p) / Double(steps), 0))
                if p == 0 { top.move(to: pt); ribbon.move(to: pt) } else { top.addLine(to: pt); ribbon.addLine(to: pt) }
            }
            for p in stride(from: steps, through: 0, by: -1) {
                ribbon.addLine(to: CGPoint(x: xAt(p), y: wave(Double(p) / Double(steps), lift)))
            }
            ribbon.closeSubpath()
            g.fill(ribbon, with: .linearGradient(
                Gradient(stops: [.init(color: pal.color(r, 0), location: 0),
                                 .init(color: pal.color(r, (0.28 + 0.12 * m.lv) * (0.4 + 0.6 * m.bright)), location: 0.5),
                                 .init(color: pal.color(r, 0), location: 1)]),
                startPoint: CGPoint(x: x0, y: 0), endPoint: CGPoint(x: x1, y: 0)))
            // A bright edge along the top.
            g.stroke(top, with: .color(pal.color(r, 0.55 * (0.4 + 0.6 * m.bright))), lineWidth: 2.2)
        }
        g.blendMode = .normal
    }
}

/// Kaleidoscope: shapes in one wedge, mirrored ten ways. They swell on the
/// kick, turn on the snare, and the whole snaps round on each bar and spins
/// faster when it is loud.
final class KaleidoScene {
    private struct Shape { let r, a, s: Double; let kind, c: Int; let sp: Double }
    private let shapes: [Shape] = (0..<8).map { i in
        Shape(r: 0.15 + Double(i) / 8 * 0.75, a: .random(in: 0..<1), s: 0.04 + .random(in: 0..<0.07),
              kind: i % 3, c: i % 3, sp: 0.3 + .random(in: 0..<1))
    }
    private var spin = 0.0

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let cx = canvas.width / 2, cy = canvas.height / 2
        let size = min(canvas.width, canvas.height) * 0.85
        spin += m.dt * (0.1 + 0.9 * m.loud * m.lv) + (m.downbeat ? m.dt * 3 * (1 - m.phase) : 0)
        let seg = TAU / 10
        let R = size * 0.62
        g.blendMode = .plusLighter
        // Each shape's place in the wedge, worked out once for all twenty copies.
        var fills: [(Path, Int)] = []
        for sh in shapes {
            let a = (0.5 + 0.5 * sin(spin * sh.sp * 3 + sh.a * 6)) * seg * 0.5
            let r = R * (sh.r + 0.08 * sin(spin * 2 + sh.a * 9) + 0.1 * m.kick)
            let x = cos(a) * r, y = sin(a) * r
            let sz = R * sh.s * (1 + 1.2 * m.kick) * (0.6 + 0.6 * m.loud)
            var p = Path()
            if sh.kind == 0 {
                p.addEllipse(in: CGRect(x: x - sz, y: y - sz, width: sz * 2, height: sz * 2))
            } else {
                let turn = spin * 2 + m.snare * 2
                let n = sh.kind == 1 ? 3 : 4
                for q in 0...n {
                    let t = turn + Double(q) / Double(n) * TAU
                    let pt = CGPoint(x: x + cos(t) * sz, y: y + sin(t) * sz)
                    if q == 0 { p.move(to: pt) } else { p.addLine(to: pt) }
                }
            }
            fills.append((p, sh.c))
        }
        let alpha = (0.35 + 0.3 * m.lv) * (0.4 + 0.6 * m.bright)
        for k in 0..<10 {
            for mirror in [1.0, -1.0] {
                let t = CGAffineTransform(translationX: cx, y: cy)
                    .rotated(by: Double(k) * seg + spin)
                    .scaledBy(x: 1, y: mirror)
                for (p, c) in fills {
                    g.fill(p.applying(t), with: .color(pal.color(c, alpha)))
                }
            }
        }
        // A star in the middle.
        g.fill(Path(ellipseIn: CGRect(x: cx - R * 0.5, y: cy - R * 0.5, width: R, height: R)), with: .radialGradient(
            Gradient(colors: [.white.opacity(0.25 + 0.5 * m.kick), .white.opacity(0)]),
            center: CGPoint(x: cx, y: cy), startRadius: 0, endRadius: R * (0.25 + 0.2 * m.kick)))
        g.blendMode = .normal
    }
}

/// Fireworks: a burst on the beats that stand out, bigger on the first of a
/// bar, great ones on a drop, crackle on the snare, falling with gravity and
/// leaving trails.
final class FireworksScene {
    private struct Spark { var trail: [CGPoint]; var vx, vy, life: Double; let fade: Double; let c: Int }
    private struct Flash { let x, y, r: Double; var life: Double; let c: Int }
    private var sparks: [Spark] = []
    private var flashes: [Flash] = []
    private var lastSnare = 0.0

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let cx = canvas.width / 2, cy = canvas.height / 2
        let size = min(canvas.width, canvas.height) * 0.85
        func burst(_ x: Double, _ y: Double, _ n: Int, _ power: Double, _ c: Int) {
            for _ in 0..<n {
                let a = Double.random(in: 0..<TAU)
                let v = size * power * (0.35 + .random(in: 0..<0.75))
                sparks.append(Spark(trail: [CGPoint(x: x, y: y)], vx: cos(a) * v, vy: sin(a) * v, life: 1,
                                    fade: 0.55 + .random(in: 0..<0.45), c: c))
            }
            flashes.append(Flash(x: x, y: y, r: size * power * 0.6, life: 1, c: c))
        }
        if m.firstDrop {
            // The drop: a great burst in the middle, and two more beside it.
            burst(cx, cy - size * 0.1, 180, 1.5, 0)
            burst(cx - size * 0.45, cy, 70, 0.9, 1)
            burst(cx + size * 0.45, cy, 70, 0.9, 2)
        } else if m.drop {
            burst(cx + (.random(in: 0..<1) - 0.5) * size * 0.5, cy - size * 0.15, 110, 1.2, .random(in: 0..<3))
        } else if m.newBeat {
            let a = Double.random(in: 0..<TAU)
            let d = Double.random(in: 0..<1) * size * 0.45
            let n = Int(((24 + 60 * m.loud) * (m.downbeat ? 1.8 : 1) * (0.6 + 0.6 * m.e)).rounded())
            burst(cx + cos(a) * d, cy - size * 0.1 + sin(a) * d * 0.7, n,
                  (m.downbeat ? 1.1 : 0.75) * (0.6 + 0.6 * m.loud), .random(in: 0..<3))
        }
        if m.snare - lastSnare > 0.35 && m.lv > 0.3 {
            let a = Double.random(in: 0..<TAU)
            burst(cx + cos(a) * size * 0.5, cy + sin(a) * size * 0.4, 14, 0.35, .random(in: 0..<3))
        }
        lastSnare = m.snare
        if sparks.count > 650 { sparks.removeFirst(sparks.count - 650) }

        g.blendMode = .plusLighter
        // Glows where the bursts went off.
        flashes = flashes.compactMap { f in
            var f = f
            f.life -= m.dt * 2.2
            guard f.life > 0 else { return nil }
            g.fill(Path(ellipseIn: CGRect(x: f.x - f.r, y: f.y - f.r, width: f.r * 2, height: f.r * 2)), with: .radialGradient(
                Gradient(colors: [pal.color(f.c, 0.45 * f.life * m.bright), pal.color(f.c, 0)]),
                center: CGPoint(x: f.x, y: f.y), startRadius: 0, endRadius: f.r))
            return f
        }
        // The sparks, falling. The page's trail canvas fades by 0.14 a frame;
        // each spark keeps its last places and draws them fading as much.
        let gravity = size * 0.45
        var byColour: [[(Path, Double, Double)]] = [[], [], []]
        sparks = sparks.compactMap { p in
            var p = p
            p.life -= m.dt * p.fade
            guard p.life > 0 else { return nil }
            p.vx *= 1 - m.dt * 1.4
            p.vy = p.vy * (1 - m.dt * 1.4) + gravity * m.dt
            let last = p.trail.last!
            p.trail.append(CGPoint(x: last.x + p.vx * m.dt, y: last.y + p.vy * m.dt))
            if p.trail.count > 9 { p.trail.removeFirst() }
            var path = Path()
            path.addLines(p.trail)
            byColour[p.c].append((path, min(1, p.life * 1.3) * (0.5 + 0.5 * m.bright), 1.2 + 2.7 * p.life))
            return p
        }
        for c in 0..<3 {
            for (path, a, width) in byColour[c] {
                g.stroke(path, with: .color(pal.color(c, a)), style: StrokeStyle(lineWidth: width, lineCap: .round, lineJoin: .round))
            }
        }
        g.blendMode = .normal
    }
}
