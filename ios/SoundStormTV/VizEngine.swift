import Foundation

/// One frame's reading of the music, for whichever look draws it: where the
/// beat is, how hard this moment hits, how loud this part is, and the rare big
/// moments. The web page's frame loop (viz.frame in app.js), step for step.
struct VizFrame {
    var t = 0.0, dt = 0.0
    var beat = 1.6, phase = 0.0
    var beatNo = 0
    var downbeat = false
    /// A beat that stands out, or the first of a bar - not all four.
    var newBeat = false
    var novelty = 0.0
    var drop = false, firstDrop = false
    var dropEnv = 0.0
    /// How hard the strike hit, and how loud and bassy that moment was:
    /// what sizes Storm's lightning.
    var dropPower = 0.0, dropLoud = 0.0, dropBass = 0.0
    var kick = 0.0, snare = 0.0, loud = 0.6
    /// Playing, eased: 1 playing, settling to 0 on pause.
    var lv = 0.0
    /// The song's energy (AudioMuse's, among the library).
    var e = 0.3
    var drive = 0.0, bright = 0.0
    /// A clock that runs with the music and idles slowly without it.
    var ck = 0.0
    var playing = false
}

final class VizEngine {
    private var level = 0.0
    private var kickEnv = 0.0, snareEnv = 0.0
    private var loudEnv: Double?
    private var loudAvg: Double?
    private var kickPeaks: [Double] = [], snarePeaks: [Double] = []
    private var peakBeat = -1
    private var beatNovelty = 0.0
    private var strikeBeat = -1
    private var bigAt: Double?
    private var strikeFrame: Int?
    private var sceneBeat = -1
    private var clock = 0.0
    private var lastT: Double?
    private var lastSongTime: Double?

    /// Reads the moment. `now` is the wall clock; `t` the song's position.
    func step(now: Double, t: Double, playing: Bool, beat defaultBeat: Double, energy e: Double, heard: Heard?) -> VizFrame {
        // A player can report no time at all (NaN) while it loads; read that
        // as the start rather than trap converting it to a frame number.
        let t = t.isFinite ? max(0, min(t, 1e7)) : 0
        let dt = min(0.1, now - (lastT ?? now))
        lastT = now
        // A seek: the last bars were somewhere else entirely.
        if let last = lastSongTime, abs(t - last) > 2 {
            kickPeaks = []; snarePeaks = []; bigAt = nil; loudAvg = nil
        }
        lastSongTime = t
        level += ((playing ? 1 : 0) - level) * min(1, dt * (playing ? 5 : 1.5))
        let lv = level

        var beat = defaultBeat.isFinite && defaultBeat >= 0.2 ? defaultBeat : 0.5
        var phase = t.truncatingRemainder(dividingBy: beat) / beat
        var beatNo = Int(t / beat)
        var downbeat = beatNo % 4 == 0
        // Without the song's own analysis, only a soft pulse, a little more at
        // the start of each bar.
        var kick = exp(-phase * 5) * lv * (downbeat ? 0.45 : 0.15)
        var snare = (beatNo % 2 == 1 ? exp(-phase * 6) * 0.15 : 0) * lv
        var loudness = 0.6
        var novelty = 0.0

        if let heard, !heard.loud.isEmpty {
            let bs = heard.beats
            var lo = 0, hi = bs.count - 1
            while lo < hi { let mid = (lo + hi + 1) >> 1; if bs[mid] <= t { lo = mid } else { hi = mid - 1 } }
            if bs.count > 1 && bs[lo] <= t {
                let next = lo + 1 < bs.count ? bs[lo + 1] : bs[lo] + (bs[lo] - bs[max(0, lo - 1)])
                beat = max(0.2, next - bs[lo])
                phase = min(1, (t - bs[lo]) / beat)
                beatNo = lo
                downbeat = ((lo - heard.down) % 4 + 4) % 4 == 0
            }
            let fi = frame(t, heard, heard.loud.count)
            let fall = exp(-dt * 7)
            kickEnv = max(kickEnv * fall, Double(heard.low[fi]))
            snareEnv = max(snareEnv * fall, Double(heard.high[fi]))
            let l = Double(heard.loud[fi])
            loudEnv = (loudEnv ?? l) + (l - (loudEnv ?? l)) * min(1, dt * 6)
            loudness = loudEnv!
            let hitScale = 0.25 + 0.75 * loudness
            // Each hit measured against the typical hit of the last two bars:
            // a steady beat settles into a soft pulse, and an accent, a fill or
            // a drop after a quiet bit hits hard.
            if beatNo != peakBeat {
                peakBeat = beatNo
                let ahead = max(fi, min(heard.low.count - 1, fi + 2))
                var kp = 0.0, sp = 0.0
                for i in fi...ahead { kp = max(kp, Double(heard.low[i])); sp = max(sp, Double(heard.high[i])) }
                let mean = kickPeaks.isEmpty ? 0.5 : kickPeaks.reduce(0, +) / Double(kickPeaks.count)
                beatNovelty = kickPeaks.count >= 4 ? max(0, min(1, (kp - mean * 1.05) / 0.35)) : 0
                kickPeaks.append(kp); snarePeaks.append(sp)
                if kickPeaks.count > 8 { kickPeaks.removeFirst() }
                if snarePeaks.count > 8 { snarePeaks.removeFirst() }
            }
            let typicalK = kickPeaks.isEmpty ? 0.5 : kickPeaks.reduce(0, +) / Double(kickPeaks.count)
            let typicalS = snarePeaks.isEmpty ? 0.5 : snarePeaks.reduce(0, +) / Double(snarePeaks.count)
            loudAvg = (loudAvg ?? loudness) + (loudness - (loudAvg ?? loudness)) * min(1, dt / 4)
            let surge = max(0, min(1, (loudness - loudAvg!) / 0.25))
            let kickNew = max(0, kickEnv - typicalK * 0.95)
            let snareNew = max(0, snareEnv - typicalS * 0.95)
            kick = min(1, 0.3 * kickEnv + 1.3 * kickNew + 0.5 * surge) * lv * hitScale
            snare = min(1, 0.3 * snareEnv + 1.3 * snareNew) * lv * hitScale
            novelty = max(beatNovelty, surge)
        }

        var drop = false, firstDrop = false
        var dropPower = 0.0, dropLoud = 0.0, dropBass = 0.0
        if let heard, heard.fixedScale {
            // Lightning: every strong sharp high near a beat (the page's
            // strikesAt), each counted once as it rises; every frame since the
            // last drawn is looked at, so a hit one frame long is not missed
            // at 30 frames a second. The first after six quiet seconds is
            // marked (Storm's double strike, Fireworks' finale).
            let fi = frame(t, heard, heard.high.count)
            if let b = bigAt, t < b { bigAt = nil } // a seek back
            let jumped = strikeFrame.map { fi < $0 || fi - $0 > 10 } ?? true
            var rose = false
            let from = jumped ? fi : strikeFrame! + 1
            for k in stride(from: from, through: fi, by: 1) where heard.strikesAt(k) {
                rose = true
                // The peak can come a frame or two after it crosses (the
                // analysis is the whole song, so it can look ahead).
                for j in k..<min(heard.high.count, k + 4) {
                    dropPower = max(dropPower, Double(heard.high[j]))
                    dropLoud = max(dropLoud, Double(heard.loud[j]))
                    dropBass = max(dropBass, Double(heard.low[j]))
                }
            }
            strikeFrame = fi
            if playing && rose {
                drop = true
                firstDrop = bigAt.map { t - $0 > 6 } ?? true
                bigAt = t
            }
        } else if let heard, playing, downbeat, beatNo != strikeBeat {
            // The TV's own (older) hearing: the first beat of a bar in the
            // song's loudest 30%, as the page did before.
            if loudness >= Double(heard.loudTop) {
                drop = true
                firstDrop = bigAt.map { t - $0 > beat * 4 * 1.5 || t < $0 } ?? true
                bigAt = t
            }
            strikeBeat = beatNo
        }
        let sinceBig = t - (bigAt ?? -99)
        let dropEnv = sinceBig >= 0 && sinceBig < 0.9 ? 1 - sinceBig / 0.9 : 0
        if drop { beatNovelty = 1 }

        let anyBeat = playing && beatNo != sceneBeat
        if anyBeat { sceneBeat = beatNo }
        clock += dt * (0.25 + 0.75 * lv) * (0.5 + 0.5 * e + 0.6 * loudness)

        return VizFrame(
            t: t, dt: dt, beat: beat, phase: phase, beatNo: beatNo, downbeat: downbeat,
            newBeat: anyBeat && (novelty > 0.4 || downbeat), novelty: novelty,
            drop: drop, firstDrop: firstDrop, dropEnv: dropEnv,
            dropPower: dropPower, dropLoud: dropLoud, dropBass: dropBass,
            kick: kick, snare: snare, loud: loudness, lv: lv, e: e,
            drive: (0.25 + 0.9 * loudness) * (0.6 + 0.7 * e), bright: 0.35 + 0.65 * loudness,
            ck: clock, playing: playing)
    }

    /// The analysis frame for song time `t`, kept inside a lane of `count`.
    private func frame(_ t: Double, _ heard: Heard, _ count: Int) -> Int {
        Int(max(0, min(Double(count - 1), (t * heard.fps).rounded(.down))))
    }
}
