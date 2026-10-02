import AVFoundation
import SwiftUI

/// Played from a phone (the server's players.go, the page's playerLoop): this
/// TV says hello as a player, asks the server for commands - a long poll, so
/// there is no pairing and no "same Wi-Fi" - and does them: play something,
/// pause, skip, seek, volume, stop. It reports what it is doing every couple
/// of seconds, for the phone's remote. Anybody in the house may send
/// something to a TV, which switches it to them (asking first, here, when
/// somebody else is using it); only its own person's phones steer it
/// otherwise - the server decides that.
struct RemoteControlled: ViewModifier {
    @Environment(AppModel.self) private var model
    @Environment(API.self) private var api
    @Environment(Player.self) private var player
    @State private var asking: (ask: String, from: String)?
    @State private var lastState = ""
    @State private var stateAt = Date.distantPast

    /// This TV, as the server knows it: kept, so a phone's choice of it
    /// lasts across launches.
    static let id: String = {
        let key = "playerID"
        if let id = UserDefaults.standard.string(forKey: key), id.count == 32 { return id }
        let id = (0..<16).map { _ in String(format: "%02x", UInt8.random(in: 0...255)) }.joined()
        UserDefaults.standard.set(id, forKey: key)
        return id
    }()

    func body(content: Content) -> some View {
        content
            .task { await listen() }
            .task {
                while !Task.isCancelled {
                    await report(force: false)
                    try? await Task.sleep(for: .seconds(2))
                }
            }
            .alert(asking.map { "\($0.from) wants to play something" } ?? "",
                   isPresented: Binding(get: { asking != nil }, set: { if !$0 { asking = nil } })) {
                Button("Let them") { answer(true) }
                // Back is a cancel, and says nothing: after fifteen seconds
                // the server takes no answer for yes, as a TV playing to an
                // empty room should not hold anybody up.
                Button("No", role: .destructive) { answer(false) }
            } message: {
                Text("Let \(asking?.from ?? "them") take over this TV? What is playing now is saved, so it can carry on later.")
            }
    }

    private func listen() async {
        try? await api.playerHello(Self.id, name: "Apple TV")
        while !Task.isCancelled {
            do {
                let commands = try await api.playerCommands(Self.id)
                for command in commands { await run(command) }
                if !commands.isEmpty {
                    try? await Task.sleep(for: .milliseconds(300))
                    await report(force: true)
                }
            } catch API.Failure.status(404, _) {
                // Not known to the server (restarted, or a new person): again.
                try? await api.playerHello(Self.id, name: "Apple TV")
                try? await Task.sleep(for: .seconds(2))
            } catch is CancellationError {
                return
            } catch {
                try? await Task.sleep(for: .seconds(8))
            }
        }
    }

    private func answer(_ allow: Bool) {
        guard let ask = asking?.ask else { return }
        asking = nil
        Task { await api.answerTakeOver(Self.id, ask: ask, allow: allow) }
    }

    // MARK: Commands

    private func run(_ c: [String: Any]) async {
        switch c["type"] as? String {
        case "switch":
            // Taken over from somebody's phone: the book playing keeps its
            // place as the person listening, then the TV is the new person.
            guard let code = c["code"] as? String else { return }
            await model.beforeSwitch()
            if (try? await api.playerSwitch(Self.id, code: code)) != nil {
                model.switched()
                try? await api.playerHello(Self.id, name: "Apple TV")
            }
        case "ask":
            guard let ask = c["ask"] as? String else { return }
            asking = (ask, c["from"] as? String ?? "Someone")
            Task {
                try? await Task.sleep(for: .seconds(15))
                if asking?.ask == ask { asking = nil }
            }
        case "play":
            await play(c)
        case "control":
            control(c["action"] as? String ?? "", value: (c["value"] as? NSNumber)?.doubleValue ?? 0)
        case "volume":
            let v = Float((c["value"] as? NSNumber)?.doubleValue ?? 1)
            if let video = model.video { video.player.volume = max(0, min(1, v)) } else { player.setVolume(v) }
        case "stop":
            if let video = model.video { video.stop(); model.video = nil }
            await player.stopSaving()
            model.photos = nil
            model.showingNowPlaying = false
        default:
            break // "claim": a phone chose this TV; nothing to do but be ready
        }
    }

    private static func item(_ any: Any?) -> Item? {
        guard let any, let data = try? JSONSerialization.data(withJSONObject: any) else { return nil }
        return try? JSONDecoder().decode(Item.self, from: data)
    }

    private func play(_ c: [String: Any]) async {
        guard let item = Self.item(c["item"]) else { return }
        // Whatever was open gives way to what was sent.
        if let video = model.video { video.stop(); model.video = nil }
        model.photos = nil
        switch item.kind {
        case "music":
            let queue = (c["queue"] as? [Any])?.compactMap(Self.item) ?? []
            let list = queue.isEmpty ? [item] : queue
            let at = max(0, min(list.count - 1, (c["index"] as? NSNumber)?.intValue ?? 0))
            player.play(list, from: at)
            // Moved from a phone mid-song: on from the same moment.
            if let from = (c["at"] as? NSNumber)?.doubleValue, from > 1 { player.seek(to: from) }
            model.showingNowPlaying = true
        case "audiobook":
            await model.playBook(item)
        case "picture" where item.extra?["type"] != "video":
            model.showingNowPlaying = false
            model.photos = PhotoViewing(photos: [item], index: 0)
        default:
            model.showingNowPlaying = false
            model.playVideo(item)
        }
    }

    private func control(_ action: String, value: Double) {
        if action == "closephoto" {
            model.photos = nil
            return
        }
        if let video = model.video {
            let p = video.player
            switch action {
            case "toggle": p.timeControlStatus == .paused ? p.play() : p.pause()
            case "pause": p.pause()
            case "play": p.play()
            case "seek": p.seek(to: CMTime(seconds: value, preferredTimescale: 600))
            case "skip": p.seek(to: CMTime(seconds: max(0, p.currentTime().seconds + value), preferredTimescale: 600))
            default: break
            }
            return
        }
        guard player.current != nil else { return }
        switch action {
        case "toggle": player.togglePlay()
        case "pause": player.pause()
        case "play": player.resume()
        case "next": player.next()
        case "prev": player.previous()
        case "seek": player.seek(to: value)
        case "skip": player.skip(by: value)
        case "rate" where player.isBook && value >= 0.5 && value <= 3: player.setSpeed(value)
        default: break
        }
    }

    // MARK: What it is doing

    private static func card(_ item: Item) -> [String: Any] {
        var out: [String: Any] = ["sourceId": item.sourceId, "id": item.id, "title": item.title, "kind": item.kind,
                                  "subtitle": item.artist]
        if let art = item.artId { out["artId"] = art }
        if let d = item.durationSeconds { out["durationSeconds"] = d }
        if let type = item.extra?["type"] { out["extra"] = ["type": type] }
        return out
    }

    private func state() -> [String: Any] {
        if let video = model.video {
            let p = video.player
            let d = p.currentItem?.duration.seconds ?? 0
            return ["playing": p.timeControlStatus != .paused, "kind": "video", "item": Self.card(video.item),
                    "position": p.currentTime().seconds.isFinite ? p.currentTime().seconds : 0,
                    "duration": d.isFinite ? d : 0, "volume": p.volume]
        }
        if let item = player.current {
            var out: [String: Any] = ["playing": player.isPlaying, "kind": "audio", "item": Self.card(item),
                                      "position": player.time, "duration": player.duration,
                                      "volume": player.userVolume, "rate": player.isBook ? player.speed : 1]
            if let q = player.queuePosition { out["queue"] = ["index": q.index, "length": q.length] }
            return out
        }
        if let viewing = model.photos, viewing.photos.indices.contains(viewing.index) {
            return ["playing": false, "kind": "photo", "item": Self.card(viewing.photos[viewing.index])]
        }
        return ["playing": false]
    }

    /// On a change, and every half minute regardless: the phone's timeline
    /// runs from the last report.
    private func report(force: Bool) async {
        guard api.user != nil else { return }
        var st = state()
        let position = (st["position"] as? Double).map { Int($0 / 5) } ?? 0
        let sig = "\(st["kind"] ?? "")|\(st["playing"] ?? "")|\((st["item"] as? [String: Any])?["id"] ?? "")|\(position)|\(st["volume"] ?? "")"
        if !force && sig == lastState && Date().timeIntervalSince(stateAt) < 30 { return }
        lastState = sig
        stateAt = Date()
        st["at"] = Int(Date().timeIntervalSince1970 * 1000)
        await api.playerState(Self.id, st)
    }
}
