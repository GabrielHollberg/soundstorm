import AVFoundation
import MediaPlayer
import UIKit

/// Music and audiobooks, played by tvOS itself. AVQueuePlayer holds what is
/// playing and what comes next - the next song, or a book's next file - so it
/// is already loading when this one ends (gapless, as the web page's preload
/// is). The system's Now Playing - the TV's Control Center and the remote's
/// media keys - is told what plays and answers here.
@Observable
final class Player {
    private(set) var queue: [Item] = []
    private(set) var index = 0
    private(set) var isPlaying = false
    /// Into the song, or into the whole book (across its files).
    private(set) var time: Double = 0
    private(set) var duration: Double = 0

    var current: Item? { queue.indices.contains(index) ? queue[index] : nil }

    /// The song's position now, read from the player - not the twice-a-second
    /// `time`, which is too coarse to animate by.
    var exactTime: Double {
        let s = player.currentTime().seconds
        return s.isFinite ? s : time
    }

    private let api: API
    private let player = AVQueuePlayer()
    private var timeObserver: Any?
    private var endObserver: NSObjectProtocol?
    private var counted = false
    /// The AVPlayerItem of what is playing, and of what is loaded after it.
    private var playing: AVPlayerItem?
    private var upNext: AVPlayerItem?
    private var artwork: MPMediaItemArtwork?

    init(api: API) {
        self.api = api
        player.actionAtItemEnd = .advance
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: 1, timescale: 2), queue: .main) { [weak self] t in
            MainActor.assumeIsolated { self?.tick(t.seconds) }
        }
        endObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.didPlayToEndTimeNotification, object: nil, queue: .main
        ) { [weak self] note in
            let ended = note.object as? AVPlayerItem
            MainActor.assumeIsolated { self?.itemEnded(ended) }
        }
        setUpRemoteCommands()
    }

    // MARK: Music

    func play(_ items: [Item], from start: Int = 0) {
        let songs = items.filter { $0.kind == "music" }
        guard !songs.isEmpty else { return }
        let first = songs.firstIndex(of: items[min(start, items.count - 1)]) ?? 0
        leaveBook()
        station = nil
        queue = songs
        load(at: first)
    }

    /// A radio station: its first songs now, and more before they run out.
    func play(station: API.Station, first batch: API.Batch) {
        guard !batch.songs.isEmpty else { return }
        leaveBook()
        queue = batch.songs
        self.station = station
        stationFrom = batch.next
        load(at: 0)
    }

    /// Any song in the queue, from Up next.
    func jump(to i: Int) {
        guard !isBook, queue.indices.contains(i) else { return }
        load(at: i)
    }

    private(set) var station: API.Station?
    private var stationFrom: Int?
    private var tuning = false

    /// Two songs from the end of a station, its next batch is fetched and
    /// added, leaving out everything already in the queue.
    private func topUpStation() {
        guard let station, !tuning, index + 2 >= queue.count else { return }
        tuning = true
        Task {
            defer { tuning = false }
            guard let batch = try? await api.tune(station, exclude: queue.map(\.id), from: stationFrom),
                  self.station == station
            else { return }
            let known = Set(queue.map(\.key))
            queue += batch.songs.filter { !known.contains($0.key) }
            stationFrom = batch.next ?? stationFrom
            enqueueNext()
        }
    }

    // MARK: Audiobooks

    /// A book's files, each placed on the book's own clock.
    private struct BookFile {
        let url: URL
        let start: Double
    }

    private(set) var isBook = false
    private(set) var chapters: [API.Chapter] = []
    /// Audiobooks only; music always plays at its own speed, as on the page.
    private(set) var speed: Double = 1
    private var files: [BookFile] = []
    private var fileIndex = 0
    private var lastSave = Date.distantPast

    /// A book, from where this person got to (the server's record, shared
    /// with the page and Audiobookshelf's own apps) - or from the start, once
    /// finished.
    func play(book: Item, _ playback: API.BookPlayback, speed: Double) {
        station = nil
        isBook = true
        queue = [book]
        index = 0
        self.speed = speed
        player.defaultRate = Float(speed)
        // Books are mastered loud and films keep dialogue quiet: at one volume
        // a book was far louder, so it plays 8dB down, as on the page
        // (bookGainDb).
        player.volume = Float(pow(10, -8.0 / 20))
        if let tracks = playback.tracks, tracks.count > 1 {
            files = tracks.compactMap { t in (try? api.absolute(t.url)).map { BookFile(url: $0, start: t.startSeconds) } }
        } else {
            files = [(try? api.absolute(playback.url)).map { BookFile(url: $0, start: 0) }].compactMap { $0 }
        }
        // Nothing on the server to play (an answer pointing elsewhere is refused).
        guard !files.isEmpty else { isBook = false; queue = []; return }
        // Chapter marks where the book has them, else one per file.
        chapters = playback.chapters ?? (playback.tracks?.count ?? 0 > 1
            ? playback.tracks!.map { API.Chapter(title: $0.title, startSeconds: $0.startSeconds) } : [])
        duration = playback.position?.duration ?? book.durationSeconds ?? 0
        let from = playback.position.flatMap { $0.finished == true ? nil : $0.seconds } ?? 0
        artwork = nil
        startBook(at: from)
        loadArtwork(for: book)
        updateRemoteCommands()
    }

    /// The chapter being heard, and where it starts and ends.
    var chapter: (index: Int, title: String, start: Double, end: Double)? {
        guard isBook, !chapters.isEmpty else { return nil }
        let i = chapters.lastIndex { $0.startSeconds <= time + 0.5 } ?? 0
        let end = chapters.indices.contains(i + 1) ? chapters[i + 1].startSeconds : duration
        return (i, chapters[i].title, chapters[i].startSeconds, end)
    }

    func jump(toChapter i: Int) {
        guard chapters.indices.contains(i) else { return }
        seek(to: chapters[i].startSeconds)
    }

    /// How fast books play, kept on the account so every device agrees.
    func setSpeed(_ v: Double) {
        speed = v
        player.defaultRate = Float(v)
        if isBook && isPlaying { player.rate = Float(v) }
        publish()
        Task { await api.setBookSpeed(v) }
    }

    private func startBook(at seconds: Double) {
        let f = files.lastIndex { $0.start <= seconds } ?? 0
        fileIndex = f
        player.removeAllItems()
        upNext = nil
        playing = AVPlayerItem(asset: AVURLAsset(url: files[f].url, options: [AVURLAssetHTTPCookiesKey: api.cookies]))
        player.insert(playing!, after: nil)
        enqueueNext()
        let into = seconds - files[f].start
        if into > 0.5 {
            player.seek(to: CMTime(seconds: into, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
        }
        time = seconds
        try? AVAudioSession.sharedInstance().setActive(true)
        player.playImmediately(atRate: Float(speed))
        isPlaying = true
        publish()
    }

    /// Before the player turns to music: where the book was left.
    private func leaveBook() {
        guard isBook else { return }
        saveBook(force: true)
        isBook = false
        files = []
        chapters = []
        player.defaultRate = 1
        player.volume = 1
        updateRemoteCommands()
    }

    /// Every ten seconds while playing, on pause and on leaving, as the page
    /// saves it; finished at the end of the last file.
    private func saveBook(force: Bool, finished: Bool = false) {
        guard isBook, let book = current else { return }
        if !force && Date().timeIntervalSince(lastSave) < 10 { return }
        lastSave = Date()
        let (t, d) = (time, duration)
        Task { await api.saveBookPosition(book, seconds: t, duration: d, finished: finished) }
    }

    // MARK: Control

    func togglePlay() { isPlaying ? pause() : resume() }

    func resume() {
        guard current != nil else { return }
        if isBook { player.playImmediately(atRate: Float(speed)) } else { player.play() }
        isPlaying = true
        publish()
    }

    func pause() {
        player.pause()
        isPlaying = false
        saveBook(force: true)
        publish()
    }

    /// The next song; in a book, the next chapter (the page's TV rule, as a
    /// swipe does), or thirty seconds on in a book without chapters.
    func next() {
        if isBook {
            if let c = chapter, chapters.indices.contains(c.index + 1) { return jump(toChapter: c.index + 1) }
            return skip(by: 30)
        }
        guard index + 1 < queue.count else { return }
        load(at: index + 1)
    }

    /// Back to the start of the song, or the one before within its first
    /// three seconds - the rule every player has, the web page's included.
    /// In a book, back to this chapter's start, or the one before within its
    /// first three seconds; thirty seconds back without chapters.
    func previous() {
        if isBook {
            guard let c = chapter else { return skip(by: -30) }
            return time - c.start > 3 || c.index == 0 ? seek(to: c.start) : jump(toChapter: c.index - 1)
        }
        if time > 3 || index == 0 {
            seek(to: 0)
        } else {
            load(at: index - 1)
        }
    }

    func seek(to seconds: Double) {
        let t = max(0, min(seconds, duration > 0 ? duration - 0.5 : seconds))
        if isBook {
            let f = files.lastIndex { $0.start <= t } ?? 0
            if f != fileIndex {
                // Another file: the queue is rebuilt from it.
                let wasPlaying = isPlaying
                startBook(at: t)
                if !wasPlaying { pause() }
                return
            }
            player.seek(to: CMTime(seconds: t - files[f].start, preferredTimescale: 600),
                        toleranceBefore: .zero, toleranceAfter: .zero)
        } else {
            player.seek(to: CMTime(seconds: t, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
        }
        time = t
        // Not forced: a run of thirty-second skips would meet the server's
        // limit on saves. Pausing and leaving save at once.
        saveBook(force: false)
        publish()
    }

    func skip(by seconds: Double) { seek(to: time + seconds) }

    func stop() {
        leaveBook()
        station = nil
        sleepAt = nil
        sleepAtEnd = false
        player.removeAllItems()
        playing = nil
        upNext = nil
        queue = []
        index = 0
        isPlaying = false
        time = 0
        duration = 0
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
    }

    // MARK: Sleep timer

    /// When the music stops by itself, or at the end of the song or chapter.
    private(set) var sleepAt: Date?
    private(set) var sleepAtEnd = false
    private var sleepChapter: Int?

    func sleep(minutes: Int?) {
        sleepAtEnd = false
        sleepAt = minutes.map { Date().addingTimeInterval(Double($0) * 60) }
    }

    func sleepAtEndOfThis() {
        sleepAt = nil
        sleepAtEnd = true
        sleepChapter = chapter?.index
    }

    private func checkSleep() {
        if let sleepAt, Date() >= sleepAt {
            self.sleepAt = nil
            pause()
        }
        // A book's chapter ending: the next one has begun.
        if sleepAtEnd, isBook, let now = chapter?.index, let then = sleepChapter, now != then {
            sleepAtEnd = false
            pause()
            seek(to: chapters[now].startSeconds)
        }
    }

    // MARK: Queue

    private func load(at i: Int) {
        index = i
        counted = false
        time = 0
        duration = queue[i].durationSeconds ?? 0
        player.removeAllItems()
        upNext = nil
        playing = playerItem(queue[i])
        player.insert(playing!, after: nil)
        enqueueNext()
        try? AVAudioSession.sharedInstance().setActive(true)
        player.play()
        isPlaying = true
        artwork = nil
        publish()
        loadArtwork(for: queue[i])
        updateRemoteCommands()
    }

    /// The song after this one, or the book's next file, loading while this
    /// one plays.
    private func enqueueNext() {
        guard let playing, upNext == nil else { return }
        let next: AVPlayerItem
        if isBook {
            guard fileIndex + 1 < files.count else { return }
            next = AVPlayerItem(asset: AVURLAsset(url: files[fileIndex + 1].url,
                                                  options: [AVURLAssetHTTPCookiesKey: api.cookies]))
        } else {
            topUpStation()
            guard index + 1 < queue.count else { return }
            next = playerItem(queue[index + 1])
        }
        player.insert(next, after: playing)
        upNext = next
    }

    private func playerItem(_ item: Item) -> AVPlayerItem {
        let asset = AVURLAsset(url: api.streamURL(item), options: [AVURLAssetHTTPCookiesKey: api.cookies])
        return AVPlayerItem(asset: asset)
    }

    private func itemEnded(_ ended: AVPlayerItem?) {
        // Only what was playing: an item from a queue since replaced can
        // still report its end.
        guard let ended, ended === playing else { return }
        if isBook {
            if let upNext {
                playing = upNext
                self.upNext = nil
                fileIndex += 1
                enqueueNext()
                // A multi-file book's next file plays at the book's speed.
                player.rate = Float(speed)
            } else {
                isPlaying = false
                saveBook(force: true, finished: true)
                publish()
            }
            return
        }
        if sleepAtEnd {
            // Stopping at the end of the song: the next one stays unplayed.
            sleepAtEnd = false
            player.pause()
        }
        if index + 1 < queue.count, let upNext {
            // AVQueuePlayer has already moved on to the preloaded next.
            playing = upNext
            self.upNext = nil
            index += 1
            counted = false
            time = 0
            duration = queue[index].durationSeconds ?? 0
            isPlaying = player.timeControlStatus != .paused
            enqueueNext()
            artwork = nil
            publish()
            loadArtwork(for: queue[index])
        } else {
            isPlaying = false
            time = 0
            publish()
        }
    }

    private func tick(_ seconds: Double) {
        guard seconds.isFinite else { return }
        checkSleep()
        if isBook {
            time = (files.indices.contains(fileIndex) ? files[fileIndex].start : 0) + seconds
            if isPlaying { saveBook(force: false) }
            return
        }
        time = seconds
        if let d = player.currentItem?.duration.seconds, d.isFinite, d > 0 { duration = d }
        // Counted once per start, as the web page counts it: half the song,
        // or four minutes of a long one.
        if !counted, let item = current, duration > 0, seconds >= min(duration / 2, 240) {
            counted = true
            Task { await api.recordPlayed(item) }
        }
    }

    // MARK: The system's Now Playing

    private func publish() {
        guard let item = current else { return }
        var info: [String: Any] = [
            MPMediaItemPropertyPlaybackDuration: duration,
            MPNowPlayingInfoPropertyElapsedPlaybackTime: time,
            MPNowPlayingInfoPropertyPlaybackRate: isPlaying ? (isBook ? speed : 1.0) : 0.0,
            MPNowPlayingInfoPropertyDefaultPlaybackRate: isBook ? speed : 1.0,
        ]
        if isBook {
            info[MPMediaItemPropertyTitle] = chapter?.title ?? item.title
            info[MPMediaItemPropertyAlbumTitle] = item.title
            info[MPMediaItemPropertyArtist] = item.artist
        } else {
            info[MPMediaItemPropertyTitle] = item.title
            info[MPMediaItemPropertyArtist] = item.artist
            if let album = item.album { info[MPMediaItemPropertyAlbumTitle] = album }
        }
        if let artwork { info[MPMediaItemPropertyArtwork] = artwork }
        MPNowPlayingInfoCenter.default().nowPlayingInfo = info
        MPNowPlayingInfoCenter.default().playbackState = isPlaying ? .playing : .paused
    }

    private func loadArtwork(for item: Item) {
        guard let url = api.artURL(source: item.sourceId, artId: item.artId, size: 800) else { return }
        Task {
            guard let (data, _) = try? await URLSession.shared.data(from: url),
                  let image = UIImage(data: data), current == item
            else { return }
            // Asked for on MediaPlayer's own queue, not the main one: the
            // closure must not inherit the app's main-actor default, or
            // Swift's isolation check stops the app the first time it runs.
            artwork = MPMediaItemArtwork(boundsSize: image.size) { @Sendable _ in image }
            publish()
        }
    }

    private func setUpRemoteCommands() {
        let c = MPRemoteCommandCenter.shared()
        c.playCommand.addTarget { [weak self] _ in self?.resume(); return .success }
        c.pauseCommand.addTarget { [weak self] _ in self?.pause(); return .success }
        c.togglePlayPauseCommand.addTarget { [weak self] _ in self?.togglePlay(); return .success }
        c.nextTrackCommand.addTarget { [weak self] _ in self?.next(); return .success }
        c.previousTrackCommand.addTarget { [weak self] _ in self?.previous(); return .success }
        c.skipForwardCommand.preferredIntervals = [30]
        c.skipBackwardCommand.preferredIntervals = [30]
        c.skipForwardCommand.addTarget { [weak self] _ in self?.skip(by: 30); return .success }
        c.skipBackwardCommand.addTarget { [weak self] _ in self?.skip(by: -30); return .success }
        c.changePlaybackPositionCommand.addTarget { [weak self] event in
            guard let e = event as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
            self?.seek(to: e.positionTime)
            return .success
        }
        updateRemoteCommands()
    }

    /// A book offers thirty-second skips where music offers songs.
    private func updateRemoteCommands() {
        let c = MPRemoteCommandCenter.shared()
        c.nextTrackCommand.isEnabled = !isBook
        c.previousTrackCommand.isEnabled = !isBook
        c.skipForwardCommand.isEnabled = isBook
        c.skipBackwardCommand.isEnabled = isBook
    }
}
