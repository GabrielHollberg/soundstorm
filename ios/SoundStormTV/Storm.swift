import SwiftUI
import UIKit

/// Storm, rebuilt as the page rebuilt it (storm and its stormX helpers in
/// app.js): a deck of cloud over the whole top, always there, lit its own
/// way by each strike; lightning shaped like lightning - ground strikes
/// jagged at every scale with a few short forking branches, bolts far off,
/// lightning spreading through the clouds, or the clouds lit alone - sized by
/// how loud and bassy the moment of the strike is (dropLoud, dropBass); and
/// rain with depth, gusts, heavier with the music, and mist along the ground.
/// Soft shapes are pictures made once and stretched, as the page's sprites.
final class StormScene {
    private typealias RGB = (r: Double, g: Double, b: Double)

    private struct Drop { var x, y: Double; let z: Double; let layer: Int; let s: Double; let c: Int; let k: Double; let ground: Double; var off: Bool }
    private struct Mist { var x: Double; let y, rx, ry, sp, ph: Double }
    private struct Cloud { var x: Double; let y, rx, sp: Double; let shape: Int; let thin: Double; var lit: Double }
    private struct Light { let x, y, r, k, delay: Double }
    private struct Branch { let pts: [CGPoint]; let w: Double }
    private struct Bolt {
        let tier: Int; let s: Double; var age: Double; let life: Double
        var main: [CGPoint] = []; var branches: [Branch] = []
        var scale = 1.0; var landed = false; var crawl = false
        var lights: [Light] = []; var ex = 0.0, ey = 0.0; var I = 0.0
    }
    private struct Splash { let x, y: Double; var life: Double; let c: Int; let big: Double; let bolt: Bool }
    private struct Spark { var x, y, vx, vy, life: Double }

    private var drops: [Drop] = []
    private var mist: [Mist] = []
    private var clouds: [Cloud] = []
    private var bolts: [Bolt] = []
    private var splashes: [Splash] = []
    private var sparks: [Spark] = []
    private let shapes: [[(x: Double, y: Double, r: Double)]] = (0..<5).map { _ in StormScene.puffShape() }
    private var fallRate: Double?, heavy: Double?
    private var gustT: Double?, gustHold = 0.0, gustAim = 0.0, gust = 0.0

    // The pictures, made again when the cover's colours change.
    private var tintFor: String?
    private var core: RGB = (238, 242, 255), tint: RGB = (180, 198, 255)
    private var glow = Image(uiImage: UIImage())
    private var mistImg = Image(uiImage: UIImage())
    private var dark: [Image] = [], lit: [Image] = []

    // MARK: Pictures

    private static func puffShape() -> [(x: Double, y: Double, r: Double)] {
        (0..<(7 + Int.random(in: 0..<5))).map { _ in
            let bx = 0.18 + .random(in: 0..<0.64)
            return (bx, 0.62 - sin(.pi * bx) * (0.12 + .random(in: 0..<0.2)), 0.09 + .random(in: 0..<0.07))
        }
    }

    private static func color(_ c: RGB, _ a: Double) -> UIColor {
        UIColor(red: c.r / 255, green: c.g / 255, blue: c.b / 255, alpha: a)
    }

    /// A soft round sprite, stretched into clouds' glow and mist.
    private static func sprite(_ c: RGB, _ a0: Double, _ a1: Double) -> Image {
        let image = UIGraphicsImageRenderer(size: CGSize(width: 64, height: 64)).image { ctx in
            let colors = [color(c, a0).cgColor, color(c, a1).cgColor, color(c, 0).cgColor] as CFArray
            let gradient = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(), colors: colors, locations: [0, 0.45, 1])!
            ctx.cgContext.drawRadialGradient(gradient, startCenter: CGPoint(x: 32, y: 32), startRadius: 0,
                                             endCenter: CGPoint(x: 32, y: 32), endRadius: 32, options: [])
        }
        return Image(uiImage: image)
    }

    /// A puff: soft blobs bumped along the top, drawn into a padded picture so
    /// no blob is cut square at an edge (32 points all round, as the page).
    private static func puff(_ shape: [(x: Double, y: Double, r: Double)], _ c: RGB, _ a: Double) -> Image {
        let image = UIGraphicsImageRenderer(size: CGSize(width: 256, height: 160)).image { ctx in
            for (bx, by, r) in shape {
                let colors = [color(c, a).cgColor, color(c, a * 0.6).cgColor, color(c, 0).cgColor] as CFArray
                let gradient = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(), colors: colors, locations: [0, 0.55, 1])!
                let center = CGPoint(x: 32 + bx * 192, y: 32 + by * 96)
                ctx.cgContext.drawRadialGradient(gradient, startCenter: center, startRadius: 0, endCenter: center,
                                                 endRadius: r * 192, options: [])
            }
        }
        return Image(uiImage: image)
    }

    private func drawPuff(_ g: inout GraphicsContext, _ img: Image, _ c: Cloud, _ w: Double, _ h: Double, _ R: Double) {
        let pw = c.rx * R * 2, ph = c.rx * R
        g.draw(img, in: CGRect(x: c.x * w - pw / 2 - pw * 32 / 192, y: c.y * h - ph / 2 - ph * 32 / 96,
                               width: pw * 256 / 192, height: ph * 160 / 96))
    }

    // MARK: Lightning's shapes

    /// Midpoint displacement: the middle pushed aside by up to rough times
    /// the length, each half the same, depth times. Adds the points after p1.
    private static func jag(_ out: inout [CGPoint], _ p1: CGPoint, _ p2: CGPoint, _ rough: Double, _ depth: Int) {
        guard depth > 0 else { out.append(p2); return }
        let dx = p2.x - p1.x, dy = p2.y - p1.y
        let len = max(hypot(dx, dy), 1)
        let off = (.random(in: 0..<1) - 0.5) * len * rough
        let mid = CGPoint(x: (p1.x + p2.x) / 2 - dy / len * off, y: (p1.y + p2.y) / 2 + dx / len * off)
        jag(&out, p1, mid, rough, depth - 1)
        jag(&out, mid, p2, rough, depth - 1)
    }

    /// A branch to one side, 20-55 degrees from straight down, perhaps forking once more.
    private static func branch(_ out: inout [Branch], _ p: CGPoint, _ len: Double, _ side: Double, _ wgt: Double, _ forks: Int) {
        let ang = (20 + Double.random(in: 0..<35)) * Double.pi / 180
        var pts = [p]
        jag(&pts, p, CGPoint(x: p.x + sin(ang) * side * len, y: p.y + cos(ang) * len), 0.45, 4)
        out.append(Branch(pts: pts, w: wgt))
        for _ in 0..<forks where Double.random(in: 0..<1) <= 0.55 {
            let at = Int((0.2 + .random(in: 0..<0.4)) * Double(pts.count))
            branch(&out, pts[min(at, pts.count - 1)], len * (0.35 + .random(in: 0..<0.25)),
                   Double.random(in: 0..<1) < 0.7 ? side : -side, wgt * 0.6, 0)
        }
    }

    /// A branch at an angle (radians, 0 to the right), forking up to forks times.
    private static func branchAt(_ out: inout [Branch], _ p: CGPoint, _ len: Double, _ ang: Double, _ wgt: Double, _ forks: Int) {
        var pts = [p]
        jag(&pts, p, CGPoint(x: p.x + cos(ang) * len, y: p.y + sin(ang) * len), 0.5, 3)
        out.append(Branch(pts: pts, w: wgt))
        for f in 0..<forks where Double.random(in: 0..<1) <= 0.6 {
            _ = f
            let at = Int((0.3 + .random(in: 0..<0.4)) * Double(pts.count))
            branchAt(&out, pts[min(at, pts.count - 1)], len * (0.4 + .random(in: 0..<0.3)),
                     ang + (Bool.random() ? 1 : -1) * (0.35 + .random(in: 0..<0.45)), wgt * 0.65, forks - 1)
        }
    }

    /// Where a strike lights the clouds, so no two light them alike.
    private static func lights(_ tier: Int, _ x: Double, _ w: Double, _ h: Double) -> [Light] {
        var out: [Light] = []
        if tier == 2 {
            out.append(Light(x: x, y: h * 0.08, r: w * (0.55 + .random(in: 0..<0.25)), k: 0.8, delay: 0))
            for _ in 0..<(1 + Int.random(in: 0..<2)) {
                out.append(Light(x: x + (.random(in: 0..<1) - 0.5) * w * 0.8, y: h * (0.04 + .random(in: 0..<0.25)),
                                 r: w * (0.25 + .random(in: 0..<0.2)), k: 0.5 + .random(in: 0..<0.4), delay: .random(in: 0..<0.08)))
            }
        } else if tier == 1 {
            out.append(Light(x: x, y: h * 0.07, r: w * (0.4 + .random(in: 0..<0.2)), k: 0.8, delay: 0))
            if Double.random(in: 0..<1) < 0.6 {
                out.append(Light(x: x + (.random(in: 0..<1) - 0.5) * w * 0.6, y: h * (0.04 + .random(in: 0..<0.2)),
                                 r: w * (0.2 + .random(in: 0..<0.2)), k: 0.5, delay: .random(in: 0..<0.1)))
            }
        } else {
            var lx = w * .random(in: 0..<1)
            let dir: Double = Bool.random() ? -1 : 1
            for i in 0..<(1 + Int.random(in: 0..<3)) {
                out.append(Light(x: lx, y: h * (0.04 + .random(in: 0..<0.26)), r: w * (0.35 + .random(in: 0..<0.3)),
                                 k: 0.6 + .random(in: 0..<0.4), delay: Double(i) * (0.08 + .random(in: 0..<0.12))))
                lx += dir * w * (0.2 + .random(in: 0..<0.25))
            }
        }
        return out
    }

    /// One strike: sheet lightning (the clouds lit alone), a thin bolt far off,
    /// or a thick close bolt to the ground with branches. Half the bolts
    /// spread through the clouds instead of falling; a quarter lean.
    private static func bolt(_ w: Double, _ h: Double, _ s: Double, _ age: Double) -> Bolt {
        let tier = s < 0.3 ? 0 : s < 0.7 ? 1 : 2
        let x0 = w * (0.15 + .random(in: 0..<0.7))
        var bo = Bolt(tier: tier, s: s, age: age, life: tier > 0 ? 1.4 : 1.1, lights: lights(tier, x0, w, h))
        guard tier > 0 else { return bo }
        bo.scale = tier == 2 ? 1 + 0.6 * (s - 0.7) / 0.3 : 0.45 + 0.3 * (s - 0.3) / 0.4
        let bottom = tier == 2 ? h * (0.9 + .random(in: 0..<0.06)) : h * (0.36 + .random(in: 0..<0.2))
        let r = Double.random(in: 0..<1)
        let crawl = r < 0.5
        let lean = !crawl && r < 0.75
        let side: Double = Bool.random() ? -1 : 1
        bo.crawl = crawl
        if crawl { return spider(bo, tier, w, h) }
        let sx = x0, sy = -h * 0.02
        var tx = lean ? sx + side * w * (0.25 + .random(in: 0..<0.3)) : sx + (.random(in: 0..<1) - 0.5) * w * 0.15
        tx = min(w * 0.95, max(w * 0.05, tx))
        let ty = bottom
        var main = [CGPoint(x: sx, y: sy)]
        let kinks = 4 + Int.random(in: 0..<3)
        var p = CGPoint(x: sx, y: sy)
        for k in 1...kinks {
            let f = Double(k) / Double(kinks)
            let last = k == kinks
            let n = CGPoint(x: sx + (tx - sx) * f + (last ? 0 : (.random(in: 0..<1) - 0.5) * w * 0.12),
                            y: sy + (ty - sy) * f + (last ? 0 : (.random(in: 0..<1) - 0.5) * h * 0.04))
            jag(&main, p, n, 0.42, 4)
            p = n
        }
        bo.main = main
        bo.ex = p.x; bo.ey = p.y
        // Few branches, short, angled down and forking, gathered near the top;
        // a close bolt has one strong one.
        let count = tier == 2 ? 3 + Int(Double.random(in: 0..<3) + s * 2) : 1 + Int.random(in: 0..<2)
        for b in 0..<count {
            let t = Double.random(in: 0..<1) * Double.random(in: 0..<1) * 0.7
            let at = min(main.count - 1, max(2, Int(t * Double(main.count))))
            let strong = tier == 2 && b == 0
            let len = h * (strong ? 0.22 + .random(in: 0..<0.12) : 0.07 + .random(in: 0..<0.12)) * (1 - t * 0.8) * (tier == 2 ? 1 : 0.6)
            let bs: Double = lean && Double.random(in: 0..<1) < 0.7 ? side : (Bool.random() ? -1 : 1)
            branch(&bo.branches, main[at], len, bs, strong ? 0.75 : 0.35 + .random(in: 0..<0.15), strong ? 2 : 1)
        }
        return bo
    }

    /// Lightning in the clouds spreads rather than aims: two or three arms
    /// wander off from a point under the clouds, branching all along.
    private static func spider(_ bo0: Bolt, _ tier: Int, _ w: Double, _ h: Double) -> Bolt {
        var bo = bo0
        let o = CGPoint(x: w * (0.15 + .random(in: 0..<0.7)), y: h * (0.18 + .random(in: 0..<0.17)))
        let arms = 2 + (Double.random(in: 0..<1) < 0.45 ? 1 : 0)
        let first = (Bool.random() ? 0 : Double.pi) + (.random(in: 0..<1) - 0.5) * 0.8
        var paths: [[CGPoint]] = []
        bo.lights = [Light(x: o.x, y: min(h * 0.2, o.y), r: w * (0.35 + .random(in: 0..<0.2)), k: tier == 2 ? 0.95 : 0.7, delay: 0)]
        for a in 0..<arms {
            let ang = a == 0 ? first : a == 1 ? first + .pi + (.random(in: 0..<1) - 0.5) * 0.9
                : first + (Bool.random() ? 1 : -1) * (1.2 + .random(in: 0..<0.6))
            let len = w * (a == 0 ? 0.35 + .random(in: 0..<0.25) : 0.18 + .random(in: 0..<0.22))
            var pts = [o]
            let kinks = 3 + Int.random(in: 0..<3)
            var p = o
            var dir = ang
            for _ in 1...kinks {
                dir += (.random(in: 0..<1) - 0.5) * 0.9
                let step = len / Double(kinks)
                let n = CGPoint(x: p.x + cos(dir) * step, y: max(h * 0.08, min(h * 0.5, p.y + sin(dir) * step * 0.7)))
                jag(&pts, p, n, 0.45, 4)
                p = n
            }
            paths.append(pts)
            bo.lights.append(Light(x: p.x, y: min(h * 0.2, p.y), r: w * (0.25 + .random(in: 0..<0.2)),
                                   k: tier == 2 ? 0.8 : 0.6, delay: 0.05 + .random(in: 0..<0.1)))
            for _ in 0..<((tier == 2 ? 4 : 3) + Int.random(in: 0..<3)) {
                let at = min(pts.count - 1, max(1, Int((0.1 + .random(in: 0..<0.85)) * Double(pts.count - 1))))
                let bang = ang + (Bool.random() ? 1 : -1) * (0.5 + .random(in: 0..<0.9))
                branchAt(&bo.branches, pts[at], h * (0.05 + .random(in: 0..<0.1)), bang, 0.3 + .random(in: 0..<0.2), 2)
            }
        }
        bo.main = paths[0]
        for a in 1..<paths.count { bo.branches.append(Branch(pts: paths[a], w: 0.7)) }
        bo.ex = o.x; bo.ey = o.y
        return bo
    }

    // MARK: Drawing

    func draw(_ g: inout GraphicsContext, _ canvas: CGSize, _ m: VizFrame, _ pal: VizPalette) {
        let (w, h) = (canvas.width, canvas.height)
        if drops.isEmpty { setUp(w, h) }
        tintPictures(pal)

        if m.drop {
            // How big a strike is, from how loud the song is then and whether
            // a bass hit comes with it (the page's measure over 300 songs: big
            // close bolts 12%, far or across the clouds 37%, clouds lit 51%).
            let ld = m.dropLoud, bass = m.dropBass
            let s = ld >= 0.85 && bass >= 0.3 ? 0.7 + 0.3 * min(1, bass)
                : ld >= 0.82 ? 0.3 + 0.39 * min(1, (ld - 0.82) / 0.18)
                : 0.29 * min(1, ld / 0.82)
            for k in 0..<(m.firstDrop ? 2 : 1) {
                bolts.append(Self.bolt(w, h, k == 1 ? max(0, s - 0.25) : s, -0.14 * Double(k)))
            }
            if bolts.count > 6 { bolts.removeFirst(bolts.count - 6) }
        }
        var sky = 0.0, rainLit = 0.0
        for i in bolts.indices {
            bolts[i].age += m.dt
            let b = bolts[i]
            bolts[i].I = b.age < 0 ? 0 : max(0, 1 - b.age * (b.tier > 0 ? 2.6 : 1.8))
            let I = bolts[i].I
            sky = max(sky, I * (b.tier == 2 ? 1 : b.tier > 0 ? 0.25 : 0.2))
            rainLit = max(rainLit, I * (b.tier == 2 ? 1 : b.tier > 0 ? 0.5 : 0.3))
        }
        let shine = 0.5 + 0.5 * m.bright
        if sky > 0.01 { g.fill(Path(CGRect(origin: .zero, size: canvas)), with: .color(rgb(tint, sky * sky * 0.4 * shine))) }

        // The clouds: a dark band with the deck in it, a glow round each place
        // a strike lights, and each puff lit by its own shape.
        let band = Gradient(stops: [
            .init(color: Color(red: 34 / 255, green: 38 / 255, blue: 52 / 255), location: 0),
            .init(color: Color(red: 30 / 255, green: 34 / 255, blue: 47 / 255).opacity(0.97), location: 0.22),
            .init(color: Color(red: 16 / 255, green: 18 / 255, blue: 28 / 255).opacity(0.7), location: 0.5),
            .init(color: Color(red: 10 / 255, green: 12 / 255, blue: 20 / 255).opacity(0), location: 1)])
        g.fill(Path(CGRect(x: 0, y: 0, width: w, height: h * 0.5)), with: .linearGradient(band, startPoint: .zero, endPoint: CGPoint(x: 0, y: h * 0.5)))
        let R = max(w, h * 0.7)
        // Gusts: now and then, and sometimes with a big strike.
        gustT = (gustT ?? 8 + .random(in: 0..<10)) - m.dt
        if gustT! <= 0 || (m.drop && m.dropPower > 0.85 && gustHold <= 0 && Double.random(in: 0..<1) < 0.35) {
            gustHold = 2 + .random(in: 0..<2)
            gustAim = (0.18 + .random(in: 0..<0.17)) * (Double.random(in: 0..<1) < 0.8 ? 1 : -0.6)
            gustT = 10 + .random(in: 0..<15)
        }
        gustHold = max(0, gustHold - m.dt)
        let gustTo = gustHold > 0 ? gustAim : 0
        gust += (gustTo - gust) * min(1, m.dt * (gustTo != 0 ? 1.8 : 0.7))
        let wind = sin(m.ck * 0.3) * 0.15 + 0.12 + gust
        let darkR = dark.map { g.resolve($0) }, litR = lit.map { g.resolve($0) }
        for i in clouds.indices {
            clouds[i].x += m.dt * 0.006 * clouds[i].sp * (0.5 + wind * 3)
            if clouds[i].x > 1.35 { clouds[i].x -= 1.7 }
            clouds[i].lit = 0
            g.opacity = 0.9
            drawResolved(&g, darkR[clouds[i].shape], clouds[i], w, h, R)
        }
        g.blendMode = .plusLighter
        let glowR = g.resolve(glow)
        for b in bolts where b.age >= 0 {
            for l in b.lights {
                let I = max(0, 1 - (b.age - l.delay) * (b.tier > 0 ? 2.6 : 1.8)) * (b.age >= l.delay ? 1 : 0) * l.k
                guard I >= 0.01 else { continue }
                g.opacity = min(1, I * 0.18 * shine)
                g.draw(glowR, in: CGRect(x: l.x - l.r * 1.3, y: l.y - l.r * 0.6, width: l.r * 2.6, height: l.r * 1.2))
                for i in clouds.indices {
                    let dx = (clouds[i].x * w - l.x) / l.r, dy = (clouds[i].y * h - l.y) / (l.r * 0.6)
                    clouds[i].lit += I * clouds[i].thin * exp(-(dx * dx + dy * dy))
                }
            }
        }
        // Lit puffs painted over, not added: added they burned the deck white.
        g.blendMode = .normal
        for c in clouds where c.lit >= 0.01 {
            g.opacity = min(0.85, c.lit * 0.85) * shine
            drawResolved(&g, litR[c.shape], c, w, h, R)
        }
        g.opacity = 1
        g.blendMode = .plusLighter

        // Rain: speed easing to what the music asks, heavier with it (more
        // drops, not only faster), three depths, lit up in a flash.
        let target = 0.28 + 0.5 * m.loud * m.lv + 0.15 * m.kick
        fallRate = (fallRate ?? target) + (target - (fallRate ?? target)) * min(1, m.dt * 2.5)
        let heavyTo = 0.3 + 0.7 * min(1, m.loud * m.lv * 1.15)
        heavy = (heavy ?? heavyTo) + (heavyTo - (heavy ?? heavyTo)) * min(1, m.dt * 0.6)
        let fall = h * fallRate! * m.dt
        let alpha = min(1, (0.35 + 0.35 * m.lv) * (0.5 + 0.5 * m.bright) * (1 + 1.5 * rainLit))
        let minLen = h * 0.015
        let layerW = [1.2, 2, 3.2], layerA = [0.45, 0.75, 1.0], layerL = [0.6, 1.0, 1.5]
        func streak(_ d: Drop) -> (CGPoint, CGPoint) {
            let step = fall * d.s, len = layerL[d.layer]
            return (CGPoint(x: d.x, y: d.y), CGPoint(x: d.x + step * wind * 2.2 * len, y: d.y + max(step * 2.2, minLen) * len))
        }
        for layer in 0..<3 {
            for c in 0..<3 {
                var path = Path()
                for d in drops where !d.off && d.layer == layer && d.c == c {
                    let (a, b) = streak(d)
                    path.move(to: a); path.addLine(to: b)
                }
                g.stroke(path, with: .color(pal.color(c, alpha * layerA[layer])), style: StrokeStyle(lineWidth: layerW[layer], lineCap: .round))
            }
        }
        if rainLit > 0.04 {
            var path = Path()
            for d in drops where !d.off { let (a, b) = streak(d); path.move(to: a); path.addLine(to: b) }
            g.stroke(path, with: .color(rgb(core, min(0.7, rainLit * 0.75))), lineWidth: 1.5)
        }
        // A drop starts where the wind now will carry it across the screen,
        // so no corner goes dry, a gust included.
        let drift = h * wind * 1.1
        let from = min(0, -drift) - w * 0.05, across = w * 1.1 + abs(drift)
        for i in drops.indices {
            if drops[i].off {
                guard drops[i].k <= heavy! else { continue }
                drops[i].off = false
                drops[i].y = -h * 0.05 * .random(in: 0..<1)
                drops[i].x = from + .random(in: 0..<1) * across
                continue
            }
            let step = fall * drops[i].s
            drops[i].y += step
            drops[i].x += step * wind
            if drops[i].y > drops[i].ground {
                if Double.random(in: 0..<1) < 0.3 && splashes.count < 80 {
                    splashes.append(Splash(x: drops[i].x, y: drops[i].ground + .random(in: 0..<1) * h * 0.015, life: 1,
                                           c: drops[i].c, big: 0.4 + 0.8 * drops[i].z, bolt: false))
                }
                drops[i].off = drops[i].k > heavy!
                drops[i].y = -h * 0.05 * .random(in: 0..<1)
                drops[i].x = from + .random(in: 0..<1) * across
            }
        }
        // Mist along the ground, thicker in a downpour and lit by the flash.
        let mistR = g.resolve(mistImg)
        for i in mist.indices {
            mist[i].x += m.dt * 0.01 * mist[i].sp * (0.4 + wind * 3)
            if mist[i].x > 1.4 { mist[i].x -= 1.8 }
            if mist[i].x < -0.4 { mist[i].x += 1.8 }
            let ms = mist[i]
            g.opacity = min(1, (0.06 + 0.1 * heavy! + 0.3 * rainLit) * shine * (0.8 + 0.2 * sin(m.ck * 0.5 + ms.ph)))
            g.draw(mistR, in: CGRect(x: ms.x * w - ms.rx * w, y: ms.y * h - ms.ry * h, width: ms.rx * w * 2, height: ms.ry * h * 2))
        }
        g.opacity = 1
        splashes = splashes.compactMap { sp in
            var sp = sp
            sp.life -= m.dt * (sp.bolt ? 1.4 : 2.5)
            guard sp.life > 0 else { return nil }
            let rx = ((1 - sp.life) * 21 + 2) * sp.big, ry = ((1 - sp.life) * 6 + 1) * sp.big
            let col = sp.bolt ? rgb(core, sp.life * 0.6 * shine) : pal.color(sp.c, sp.life * 0.5 * m.bright)
            g.stroke(Path(ellipseIn: CGRect(x: sp.x - rx, y: sp.y - ry, width: rx * 2, height: ry * 2)), with: .color(col), lineWidth: 1.5)
            return sp
        }
        var sparkPath = Path()
        sparks = sparks.compactMap { sk in
            var sk = sk
            sk.life -= m.dt * 1.6
            guard sk.life > 0 else { return nil }
            sk.vy += h * 1.6 * m.dt
            sk.x += sk.vx * m.dt
            sk.y += sk.vy * m.dt
            sparkPath.move(to: CGPoint(x: sk.x, y: sk.y))
            sparkPath.addLine(to: CGPoint(x: sk.x - sk.vx * 0.03, y: sk.y - sk.vy * 0.03))
            return sk
        }
        g.stroke(sparkPath, with: .color(rgb(core, 0.6 * shine)), lineWidth: 2)

        // The bolts that fall (crawlers are drawn over the clouds, below).
        bolts.removeAll { $0.age >= $0.life }
        for i in bolts.indices where bolts[i].tier > 0 && bolts[i].age >= 0 && !bolts[i].crawl {
            drawBolt(&g, i, w, h, shine, glowR)
        }
        g.blendMode = .normal
        // The upper clouds again over the bolts and rain, so a bolt comes out
        // of a cloud lit by it, and the top edge solid.
        g.fill(Path(CGRect(x: 0, y: 0, width: w, height: h * 0.16)), with: .linearGradient(
            Gradient(stops: [.init(color: Color(red: 34 / 255, green: 38 / 255, blue: 52 / 255), location: 0),
                             .init(color: Color(red: 34 / 255, green: 38 / 255, blue: 52 / 255).opacity(0.9), location: 0.4),
                             .init(color: Color(red: 34 / 255, green: 38 / 255, blue: 52 / 255).opacity(0), location: 1)]),
            startPoint: .zero, endPoint: CGPoint(x: 0, y: h * 0.16)))
        for c in clouds where c.y <= 0.2 {
            g.opacity = 0.9
            drawResolved(&g, darkR[c.shape], c, w, h, R)
            if c.lit >= 0.01 {
                g.opacity = min(0.85, c.lit * 0.85) * shine
                drawResolved(&g, litR[c.shape], c, w, h, R)
            }
        }
        g.opacity = 1
        // A crawler goes across the clouds, so it is drawn over them.
        g.blendMode = .plusLighter
        for i in bolts.indices where bolts[i].crawl && bolts[i].tier > 0 && bolts[i].age >= 0 {
            drawBolt(&g, i, w, h, shine, glowR)
        }
        g.blendMode = .normal
    }

    /// A bolt: a white core thick at the top thinning towards the end, one
    /// faint glow round it, branches dimming faster, a faint image lingering,
    /// and where a close one lands a glow, spray and splashes.
    private func drawBolt(_ g: inout GraphicsContext, _ i: Int, _ w: Double, _ h: Double, _ shine: Double,
                          _ glowR: GraphicsContext.ResolvedImage) {
        let bo = bolts[i]
        let I = bo.I, Ib = I * I
        let sc = bo.scale * 1.5 // the page's canvas pixels, as points on a TV
        func path(_ pts: [CGPoint], _ a: Int, _ z: Int) -> Path {
            var p = Path()
            guard z > a else { return p }
            p.addLines(Array(pts[a...z]))
            return p
        }
        if I > 0.01 {
            g.stroke(path(bo.main, 0, bo.main.count - 1), with: .color(rgb(tint, 0.14 * I)), style: StrokeStyle(lineWidth: 7 * sc, lineJoin: .round))
            if Ib > 0.01 {
                var all = Path()
                for b in bo.branches { all.addPath(path(b.pts, 0, b.pts.count - 1)) }
                g.stroke(all, with: .color(rgb(tint, 0.14 * Ib)), style: StrokeStyle(lineWidth: 4 * sc, lineJoin: .round))
            }
            let n = bo.main.count
            for q in 0..<4 {
                let a = (n - 1) * q / 4, z = (n - 1) * (q + 1) / 4
                g.stroke(path(bo.main, a, z), with: .color(rgb(core, I)),
                         style: StrokeStyle(lineWidth: (3.4 - 0.75 * Double(q)) * sc, lineCap: .round, lineJoin: .round))
            }
            if Ib > 0.01 {
                for b in bo.branches {
                    let bn = b.pts.count
                    for q in 0..<3 {
                        let k = 1 - Double(q) * 0.28
                        g.stroke(path(b.pts, (bn - 1) * q / 3, (bn - 1) * (q + 1) / 3), with: .color(rgb(core, Ib * k)),
                                 style: StrokeStyle(lineWidth: 2.6 * b.w * sc * k, lineCap: .round, lineJoin: .round))
                    }
                }
            }
        }
        let after = 0.22 * (1 - bo.age / bo.life)
        if after > 0.01 {
            g.stroke(path(bo.main, 0, bo.main.count - 1), with: .color(rgb(tint, after)), lineWidth: 1.6 * sc)
        }
        if bo.tier == 2 && !bo.crawl {
            if I > 0.01 {
                g.opacity = min(1, I * 0.75 * shine)
                g.draw(glowR, in: CGRect(x: bo.ex - w * 0.3, y: bo.ey - h * 0.07, width: w * 0.6, height: h * 0.14))
                g.opacity = 1
            }
            if !bo.landed {
                bolts[i].landed = true
                for _ in 0..<26 where sparks.count < 90 {
                    sparks.append(Spark(x: bo.ex, y: bo.ey, vx: (.random(in: 0..<1) - 0.5) * w * 0.5,
                                        vy: -(0.15 + .random(in: 0..<0.45)) * h, life: 0.6 + .random(in: 0..<0.4)))
                }
                for _ in 0..<5 where splashes.count < 90 {
                    splashes.append(Splash(x: bo.ex + (.random(in: 0..<1) - 0.5) * w * 0.12, y: bo.ey + .random(in: 0..<1) * h * 0.02,
                                           life: 1, c: 0, big: 2.5 + .random(in: 0..<2), bolt: true))
                }
            }
        }
    }

    private func drawResolved(_ g: inout GraphicsContext, _ img: GraphicsContext.ResolvedImage, _ c: Cloud,
                              _ w: Double, _ h: Double, _ R: Double) {
        let pw = c.rx * R * 2, ph = c.rx * R
        g.draw(img, in: CGRect(x: c.x * w - pw / 2 - pw * 32 / 192, y: c.y * h - ph / 2 - ph * 32 / 96,
                               width: pw * 256 / 192, height: ph * 160 / 96))
    }

    private func rgb(_ c: RGB, _ a: Double) -> Color {
        Color(red: c.r / 255, green: c.g / 255, blue: c.b / 255).opacity(max(0, min(1, a)))
    }

    private func setUp(_ w: Double, _ h: Double) {
        // Drops start far enough left that the strongest wind still carries
        // rain into the bottom left corner; each has a depth and a place in
        // the heaviness. 480 on the page at its width, halved on a TV.
        let span = w * 1.1 + h * 0.34
        drops = (0..<Int((480 * 0.5 * span / (w * 1.35)).rounded())).map { _ in
            let z = Double.random(in: 0..<1)
            return Drop(x: -(h * 0.3 + w * 0.05) + .random(in: 0..<1) * span, y: .random(in: 0..<h), z: z,
                        layer: z < 0.45 ? 0 : z < 0.8 ? 1 : 2, s: 0.5 + z * 1.2, c: .random(in: 0..<3), k: .random(in: 0..<1),
                        ground: h * (0.85 + 0.12 * z + .random(in: 0..<0.02)), off: false)
        }
        mist = (0..<6).map { _ in
            Mist(x: .random(in: 0..<1.4) - 0.2, y: 0.86 + .random(in: 0..<0.11), rx: 0.35 + .random(in: 0..<0.3),
                 ry: 0.06 + .random(in: 0..<0.05), sp: 0.5 + .random(in: 0..<1), ph: .random(in: 0..<6))
        }
        // Five rows of puffs, the first right along the top edge.
        for row in 0..<5 {
            let n = 9 + row * 2
            for i in 0..<n {
                clouds.append(Cloud(x: (Double(i) + .random(in: 0..<0.8)) / Double(n) * 1.5 - 0.25,
                                    y: -0.02 + Double(row) * 0.07 + .random(in: 0..<0.04),
                                    rx: 0.16 + Double(row) * 0.03 + .random(in: 0..<0.1),
                                    sp: 0.5 + Double(row) * 0.35 + .random(in: 0..<0.3),
                                    shape: .random(in: 0..<5), thin: 0.6 + .random(in: 0..<0.7), lit: 0))
            }
        }
    }

    /// The light, the clouds and the mist take a touch of the cover's colour.
    private func tintPictures(_ pal: VizPalette) {
        let p0 = pal.colors[0]
        let key = "\(Int(p0.r)),\(Int(p0.g)),\(Int(p0.b))"
        guard key != tintFor else { return }
        tintFor = key
        func mix(_ a: RGB, _ k: Double) -> RGB {
            ((a.r + (p0.r - a.r) * k).rounded(), (a.g + (p0.g - a.g) * k).rounded(), (a.b + (p0.b - a.b) * k).rounded())
        }
        core = mix((238, 242, 255), 0.1)
        tint = mix((180, 198, 255), 0.35)
        glow = Self.sprite(mix((200, 212, 255), 0.3), 1, 0.35)
        dark = shapes.enumerated().map { i, sh in Self.puff(sh, mix(i % 2 == 1 ? (34, 38, 52) : (58, 62, 80), 0.12), 0.75) }
        lit = shapes.map { Self.puff($0, mix((185, 195, 235), 0.25), 0.7) }
        mistImg = Self.sprite(mix((150, 160, 185), 0.15), 1, 0.45)
    }
}
