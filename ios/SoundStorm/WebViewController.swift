import UIKit
import WebKit

/// The server's own web app, full screen. The page is the app: this only
/// gives it what a browser tab would (dialogs, links out, recovering from a
/// failed load) plus the one thing a tab cannot - playing on with the phone
/// locked, which comes from the audio session in SoundStormApp.
final class WebViewController: UIViewController {
    var onChangeServer: (() -> Void)?

    private let server: URL
    /// Where the page is now. Usually `server`, but the page moves itself to
    /// the install's home name when it can reach it (see sameInstall).
    private var current: URL
    private var webView: WKWebView!
    private let failure = FailureView()

    /// A TV's sign-in code to hand the page when it has loaded.
    private var pendingLink: String?
    private let scanner = CodeScanner()
    private let volumeKeys = VolumeKeys()
    #if DEBUG
    private var audioTested = false
    #endif

    init(server: URL, link: String? = nil) {
        self.server = server
        self.current = server
        self.pendingLink = link
        super.init(nibName: nil, bundle: nil)
    }

    /// The server this shows, for a link that names one.
    var serverURL: URL { server }

    /// A TV's code, scanned or opened from a link: the page asks "Sign in a
    /// TV?" at once if it can (music playing in it carries on); a page that
    /// cannot - not signed in yet, or older - is loaded again with the code
    /// in its address, asked once signed in.
    func handLink(_ code: String) {
        let js = "(typeof window.__soundstormLink === 'function' && window.__soundstormLink(\"\(code)\")) === true"
        webView.evaluateJavaScript(js) { [weak self] took, _ in
            guard (took as? Bool) != true, let self else { return }
            self.pendingLink = code
            self.load()
        }
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    override var preferredStatusBarStyle: UIStatusBarStyle { .lightContent }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black

        let config = WKWebViewConfiguration()
        // What an installed web app gets in Safari: media plays in the page
        // rather than jumping to the system player, and may start without a
        // fresh tap - the queue moving on to the next song is not a tap.
        config.allowsInlineMediaPlayback = true
        config.mediaTypesRequiringUserActionForPlayback = []
        config.allowsAirPlayForMediaPlayback = true
        config.allowsPictureInPictureMediaPlayback = true
        // Keeps Safari's own user agent and adds a name the page can test for.
        config.applicationNameForUserAgent = "Mobile/15E148 Safari/604.1 SoundStormApp/1"

        // window.soundstormApp tells the page it is inside this app, so it
        // can offer what only the app can do (see "Change server" in app.js).
        let content = config.userContentController
        content.addUserScript(WKUserScript(
            source: Self.pageScript,
            injectionTime: .atDocumentStart,
            forMainFrameOnly: true))
        content.add(MessageRelay(self), name: "soundstorm")

        webView = WKWebView(frame: view.bounds, configuration: config)
        webView.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        webView.navigationDelegate = self
        webView.uiDelegate = self
        // The page lays itself out under the status bar and home indicator
        // with env(safe-area-inset-*), as it does installed from Safari.
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        // Black until the page paints, instead of a white flash.
        webView.isOpaque = false
        webView.backgroundColor = .black
        webView.scrollView.backgroundColor = .black
        #if DEBUG
        webView.isInspectable = true // Safari → Develop → this iPhone
        #endif
        view.addSubview(webView)
        NativeAudio.shared.webView = webView
        NativeAudio.shared.isServer = { [weak self] url in self?.isServer(url) ?? false }

        failure.frame = view.bounds
        failure.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        failure.isHidden = true
        failure.onRetry = { [weak self] in self?.load() }
        failure.onChangeServer = { [weak self] in self?.onChangeServer?() }
        view.addSubview(failure)

        load()
    }

    /// The away twin has been tried since the last Try again.
    private var triedTwin = false

    private func load() {
        failure.isHidden = true
        triedTwin = false
        current = server
        AppChrome.shared.statusBarHidden = false
        var address = server
        if let code = pendingLink {
            pendingLink = nil
            address = server.appending(queryItems: [URLQueryItem(name: "link", value: code)])
        }
        webView.load(URLRequest(url: address))
    }

    /// Runs before the page's own scripts. It tells the page it is inside
    /// the app, and tells the app when Now Playing opens and closes (app.js
    /// marks the body np-open), so the status bar can go - watched from here
    /// rather than sent by app.js, so it needs no change to the web app.
    private static let pageScript = """
        window.soundstormApp = { version: 1 };
        // Phone photo backup (PhotoBackup): Settings turns it on and shows how
        // it is going, answered through window.__soundstormBackup - the same
        // messages as the Android app's, so the page has no iPhone code.
        window.soundstormApp.photoBackup = true;
        // Scanning a TV's sign-in QR code, and the volume buttons turning a
        // TV this phone controls (AppLinks.swift), as in the Android app.
        window.soundstormApp.scanCode = () =>
          window.webkit.messageHandlers.soundstorm.postMessage({ type: 'scanCode' });
        // Files added: the app picks (an iPhone's web view keeps its own
        // picker's files to itself), the page plans, reading the little it
        // needs through the app, and the app sends them in the background
        // (FileUploads.swift) - the Android app's uploads, answered the same.
        window.soundstormApp.pickFiles = () =>
          window.webkit.messageHandlers.soundstorm.postMessage({ type: 'pickFiles' });
        window.soundstormApp.readFile = (req, id, from, to) =>
          window.webkit.messageHandlers.soundstorm.postMessage({ type: 'readFile', req, id, from, to });
        window.soundstormApp.uploads = (cmd, data) =>
          window.webkit.messageHandlers.soundstorm.postMessage({ type: 'uploads', cmd, data: data || {} });
        window.soundstormApp.remoteVolume = (on) =>
          window.webkit.messageHandlers.soundstorm.postMessage({ type: 'remoteVolume', on: Boolean(on) });
        window.soundstormApp.backup = (cmd, options) =>
          window.webkit.messageHandlers.soundstorm.postMessage({ type: 'backup', cmd, options: options || {} });
        // Songs played by the app's own player (NativeAudio.swift), as the
        // Android app's: paused on the lock screen, the page's own player
        // could not be started again there, as iOS had put the app to sleep.
        (() => {
          const post = (m) => window.webkit.messageHandlers.soundstorm.postMessage(m);
          window.soundstormApp.nativeAudio = true;
          window.soundstormApp.queueNext = (url) => post({ type: 'audio', cmd: url ? 'queue' : 'unqueue', url: url || '' });
          window.soundstormApp.queueUpcoming = (items) => post({ type: 'audio', cmd: 'upcoming', items: items || [] });

          // The page's media session, passed to the app for the lock screen
          // (the page's own audio no longer plays a song from the server, so
          // the web view has nothing to show there), and still to the web
          // view's own for a downloaded song, which plays in the page.
          const real = navigator.mediaSession;
          const handlers = {};
          let metadata = null;
          let state = 'none';
          let queued = false;
          const send = () => {
            if (queued) return;
            queued = true;
            setTimeout(() => {
              queued = false;
              post({
                type: 'media', state, actions: Object.keys(handlers),
                metadata: metadata && {
                  title: metadata.title || '', artist: metadata.artist || '', album: metadata.album || '',
                  artwork: (metadata.artwork || []).map((a) => {
                    try { return new URL(a.src, location.href).href; } catch (e) { return ''; }
                  }).filter(Boolean),
                },
              });
            }, 0);
          };
          const session = {
            get metadata() { return metadata; },
            set metadata(value) { metadata = value; try { if (real) real.metadata = value; } catch (e) {} send(); },
            get playbackState() { return state; },
            set playbackState(value) { state = value || 'none'; try { if (real) real.playbackState = value; } catch (e) {} send(); },
            setActionHandler(action, handler) {
              if (handler) handlers[action] = handler; else delete handlers[action];
              try { if (real) real.setActionHandler(action, handler); } catch (e) {}
              send();
            },
            setPositionState(p) { try { if (real) real.setPositionState(p); } catch (e) {} },
          };
          Object.defineProperty(navigator, 'mediaSession', { value: session, configurable: true });
          window.__soundstormMediaAction = (action, details) => {
            const handler = handlers[action];
            if (handler) handler(Object.assign({ action }, details || {}));
          };

        // The page's audio element, played by the phone's own media player
        // (NativeAudio.swift; the Android app's NativeAudio): a song on the server is handed over and
        // played natively, and what the player does comes back as the element's
        // own events, so the page cannot tell. A song kept on the device (blob:)
        // plays in the page as before.
        const nativeAudio = (el) => {
          const proto = HTMLMediaElement.prototype;
          const getter = (name) => Object.getOwnPropertyDescriptor(proto, name).get;
          const setter = (name) => Object.getOwnPropertyDescriptor(proto, name).set;
          const def = (name, desc) => Object.defineProperty(el, name, Object.assign({ configurable: true }, desc));
          let native = false;
          let src = '';
          let pwr = false;
          let volume = 1;
          let rate = 1;
          let defaultRate = 1;
          let seekPending = false;
          const st = { state: 'idle', playing: false, position: 0, at: performance.now(), duration: NaN, buffered: 0, ended: false, error: null, url: '' };
          const fire = (type) => el.dispatchEvent(new Event(type));
          const send = (cmd, extra) => post(Object.assign({ type: 'audio', cmd: cmd }, extra || {}));
          const time = () => {
            let t = st.position;
            if (st.playing) t += ((performance.now() - st.at) / 1000) * rate;
            return Number.isFinite(st.duration) ? Math.min(t, st.duration) : t;
          };
          const ranges = (end) => ({ length: end > 0 ? 1 : 0, start: () => 0, end: () => end });
          let ticker = 0;
          const tick = (on) => {
            clearInterval(ticker);
            ticker = on ? setInterval(() => fire('timeupdate'), 250) : 0;
          };
          const serverSong = (v) => {
            try {
              const u = new URL(v, location.href);
              return (u.protocol === 'http:' || u.protocol === 'https:') && u.origin === location.origin ? u.href : '';
            } catch (e) { return ''; }
          };
          const toWeb = () => {
            if (!native) return;
            native = false;
            src = '';
            pwr = false;
            st.url = '';
            tick(false);
            send('stop');
          };
          def('src', {
            get() { return native ? src : getter('src').call(el); },
            set(v) {
              const url = serverSong(String(v));
              if (!url) { toWeb(); setter('src').call(el, v); return; }
              // Off the page's own player first, if it had a song.
              if (!native && getter('src').call(el)) { proto.pause.call(el); proto.removeAttribute.call(el, 'src'); proto.load.call(el); }
              // Taking over the song the player is already playing, for a page
              // made again: nothing is loaded again, and the next report brings the
              // page's play and playing.
              const adopting = !native && st.url === url && st.state !== 'idle' && st.state !== 'ended';
              if (adopting) { st.playing = false; pwr = false; }
              native = true;
              src = url;
              st.ended = false;
              st.error = null;
              // The player may already be in it (moved in by itself): then the
              // length is known and nothing is loaded again.
              const already = st.url === url && st.state !== 'idle' && st.state !== 'ended';
              if (!already) { st.duration = NaN; st.position = 0; st.at = performance.now(); st.playing = false; st.url = url; }
              send('load', { url: url });
              fire('emptied');
              fire('loadstart');
              if (already && Number.isFinite(st.duration)) { fire('durationchange'); fire('loadedmetadata'); fire('canplay'); }
            },
          });
          def('currentSrc', { get() { return native ? src : getter('currentSrc').call(el); } });
          def('currentTime', {
            get() { return native ? time() : getter('currentTime').call(el); },
            set(v) {
              if (!native) { setter('currentTime').call(el, v); return; }
              const s = Math.max(0, Number(v) || 0);
              st.position = s;
              st.at = performance.now();
              seekPending = true;
              send('seek', { s: s });
              fire('seeking');
              fire('timeupdate');
            },
          });
          def('duration', { get() { return native ? st.duration : getter('duration').call(el); } });
          def('paused', { get() { return native ? !pwr : getter('paused').call(el); } });
          def('ended', { get() { return native ? st.ended : getter('ended').call(el); } });
          def('readyState', { get() { return native ? (st.state === 'ready' || st.playing ? 4 : (Number.isFinite(st.duration) ? 1 : 0)) : getter('readyState').call(el); } });
          def('networkState', { get() { return native ? 2 : getter('networkState').call(el); } });
          def('buffered', { get() { return native ? ranges(st.buffered) : getter('buffered').call(el); } });
          def('seekable', { get() { return native ? ranges(Number.isFinite(st.duration) ? st.duration : 0) : getter('seekable').call(el); } });
          def('error', { get() { return native ? (st.error ? { code: st.error } : null) : getter('error').call(el); } });
          def('volume', {
            get() { return native ? volume : getter('volume').call(el); },
            set(v) {
              volume = Math.min(1, Math.max(0, Number(v)));
              setter('volume').call(el, volume);
              if (native) { send('volume', { v: volume }); fire('volumechange'); }
            },
          });
          def('playbackRate', {
            get() { return native ? rate : getter('playbackRate').call(el); },
            set(v) {
              rate = Number(v) || 1;
              setter('playbackRate').call(el, rate);
              if (native) { send('rate', { r: rate }); fire('ratechange'); }
            },
          });
          def('defaultPlaybackRate', {
            get() { return native ? defaultRate : getter('defaultPlaybackRate').call(el); },
            set(v) { defaultRate = Number(v) || 1; setter('defaultPlaybackRate').call(el, defaultRate); },
          });
          def('play', { value() {
            if (!native) return proto.play.call(el);
            send('rate', { r: rate });
            send('volume', { v: volume });
            send('play');
            if (!pwr) { pwr = true; st.ended = false; fire('play'); }
            if (st.playing) fire('playing');
            return Promise.resolve();
          } });
          def('pause', { value() {
            if (!native) return proto.pause.call(el);
            // The page's own pause: not an interruption (see pausedAt below).
            window.__soundstormPausedAt = Date.now();
            send('pause');
            if (pwr) {
              pwr = false;
              st.position = time();
              st.at = performance.now();
              st.playing = false;
              tick(false);
              setTimeout(() => fire('pause'), 0);
            }
          } });
          def('load', { value() {
            if (!native) return proto.load.call(el);
            if (src) send('load', { url: src });
          } });
          def('removeAttribute', { value(name) {
            if (String(name).toLowerCase() === 'src' && native) toWeb();
            return proto.removeAttribute.call(el, name);
          } });

          // What the native player did.
          // What the player is doing, kept even while the page has not handed it a
          // song: a page made again finds the
          // music still playing and takes it over (app.js adoptNativePlayback).
          let outside = null;
          window.soundstormApp.nativeState = () => outside;
          window.soundstormApp.askState = () => send('state');
          window.__soundstormAudio = (m) => {
            if (!native) {
              if (m.ev === 'state' && m.url) {
                outside = { url: m.url, pwr: !!m.pwr, playing: !!m.playing, position: m.position || 0 };
                st.url = m.url;
                st.state = m.state;
                if (typeof m.position === 'number') { st.position = m.position; st.at = performance.now(); }
                st.duration = typeof m.duration === 'number' ? m.duration : NaN;
                if (typeof m.buffered === 'number') st.buffered = m.buffered;
              }
              return;
            }
            if (m.ev === 'ended') {
              // It moved into the queued song by itself: the page hears this one
              // end, and sets the next, which it will find already playing.
              st.url = m.next || '';
              st.ended = true;
              st.duration = NaN;
              // Starting afresh, so the next report's "playing" starts the clock
              // and its time updates again (they queue the song after it).
              st.playing = false;
              st.position = 0;
              st.at = performance.now();
              pwr = false;
              tick(false);
              fire('pause');
              fire('ended');
              return;
            }
            // A report about the song before the one the page just set: ignored.
            if (m.url && m.url !== st.url) return;
            const wasPlaying = st.playing;
            const hadDuration = Number.isFinite(st.duration);
            st.state = m.state;
            st.playing = !!m.playing;
            if (typeof m.position === 'number') { st.position = m.position; st.at = performance.now(); }
            if (typeof m.duration === 'number') st.duration = m.duration;
            if (typeof m.buffered === 'number') st.buffered = m.buffered;
            // The player's volume is the page's to set: should the two ever differ
            // (the sound gone while the song plays on, reported and not explained),
            // the page's is sent again.
            if (typeof m.volume === 'number' && Math.abs(m.volume - volume) > 0.02) send('volume', { v: volume });
            if (m.error) { st.error = m.error; fire('error'); return; }
            if (!hadDuration && Number.isFinite(st.duration)) { fire('durationchange'); fire('loadedmetadata'); fire('canplay'); }
            // Played or paused from outside the page: the lock screen, a
            // notification, headphones, an alarm taking the sound.
            if (!!m.pwr !== pwr && m.state !== 'ended') {
              pwr = !!m.pwr;
              fire(pwr ? 'play' : 'pause');
            }
            if (st.playing && !wasPlaying) { fire('playing'); tick(true); }
            if (!st.playing && wasPlaying) tick(false);
            if (pwr && m.state === 'buffering') fire('waiting');
            if (seekPending && m.seeked) { seekPending = false; fire('seeked'); fire('timeupdate'); }
            if (m.state === 'ended' && !st.ended) {
              st.ended = true;
              pwr = false;
              tick(false);
              fire('pause');
              fire('ended');
            }
          };
        };
        // In place the moment the element is parsed - before the page's own
        // script, later in the page, first touches it.
        const found = () => {
          const el = document.getElementById('audio-player');
          if (!el || el.__native) return !!el;
          el.__native = true;
          nativeAudio(el);
          return true;
        };
        if (!found()) {
          const watch = new MutationObserver(() => { if (found()) watch.disconnect(); });
          watch.observe(document, { childList: true, subtree: true });
        }
        })();
        (() => {
          let open = false;
          const report = () => {
            const now = document.body.classList.contains('np-open');
            if (now === open) return;
            open = now;
            window.webkit.messageHandlers.soundstorm.postMessage({ type: 'nowPlaying', open });
          };
          const watch = () => {
            new MutationObserver(report).observe(document.body, { attributes: true, attributeFilter: ['class'] });
            report();
          };
          if (document.body) watch(); else document.addEventListener('DOMContentLoaded', watch);
        })();
        """

    fileprivate func showFailure(_ error: Error) {
        let error = error as NSError
        // A navigation replaced by another, or turned into a download, is
        // not a failure anybody needs to see.
        if error.domain == NSURLErrorDomain && error.code == NSURLErrorCancelled { return }
        if error.domain == WKError.errorDomain && error.code == 102 { return } // frame load interrupted
        // A home name is not reached away from home: its away twin is tried
        // once (followed, not saved), as the Android app does.
        if error.domain == NSURLErrorDomain,
           [NSURLErrorCannotFindHost, NSURLErrorCannotConnectToHost, NSURLErrorTimedOut,
            NSURLErrorDNSLookupFailed, NSURLErrorNetworkConnectionLost].contains(error.code),
           !triedTwin, let failed = (error.userInfo[NSURLErrorFailingURLErrorKey] as? URL) ?? current as URL?,
           let host = failed.host(), host.hasSuffix(".home.soundstorm.dev"),
           var parts = URLComponents(url: failed, resolvingAgainstBaseURL: false) {
            triedTwin = true
            parts.host = String(host.dropLast(".home.soundstorm.dev".count)) + ".net.soundstorm.dev"
            if let twin = parts.url {
                current = twin
                webView.load(URLRequest(url: twin))
                return
            }
        }
        AppChrome.shared.statusBarHidden = false
        failure.show(host: server.host() ?? server.absoluteString, detail: error.localizedDescription)
    }

    /// Calls a page function with a JSON argument.
    private func callPage(_ function: String, _ value: Any) {
        guard let data = try? JSONSerialization.data(withJSONObject: value),
              let json = String(data: data, encoding: .utf8) else { return }
        webView.evaluateJavaScript("window.\(function) && window.\(function)(\(json))")
    }

    /// How photo backup is going, to the page's Settings.
    private func reportBackup() {
        guard let data = try? JSONSerialization.data(withJSONObject: PhotoBackup.shared.status()),
              let json = String(data: data, encoding: .utf8) else { return }
        webView.evaluateJavaScript("window.__soundstormBackup && window.__soundstormBackup(\(json))")
    }

    fileprivate func received(_ message: WKScriptMessage) {
        // Only the server's own pages may ask the app for anything.
        let origin = message.frameInfo.securityOrigin
        // Scheme, host and port, as isServer: a page on another port of the
        // same machine is not the server.
        let defaultPort = current.scheme == "https" ? 443 : 80
        guard message.frameInfo.isMainFrame,
              origin.host.lowercased() == current.host()?.lowercased(),
              origin.protocol == current.scheme,
              (origin.port == 0 ? defaultPort : origin.port) == (current.port ?? defaultPort)
        else { return }
        guard let body = message.body as? [String: Any], let type = body["type"] as? String else { return }
        switch type {
        case "changeServer":
            AppChrome.shared.statusBarHidden = false
            onChangeServer?()
        case "nowPlaying":
            AppChrome.shared.statusBarHidden = body["open"] as? Bool ?? false
        case "audio":
            NativeAudio.shared.handle(body)
        case "media":
            NativeAudio.shared.media(body)
        case "scanCode":
            let tell = { [weak self] (what: String) in
                self?.webView.evaluateJavaScript("window.__soundstormScanned && window.__soundstormScanned(\"\(what)\")")
            }
            scanner.scan(over: self, failed: { tell("failed") }) { [weak self] text in
                guard let text else { return } // cancelled
                guard let code = TVLink.code(in: text) else {
                    tell("not-ours")
                    return
                }
                self?.handLink(code)
            }
        case "pickFiles":
            FileUploads.shared.pick(over: self) { [weak self] files in
                let list = files.map { ["id": $0.id, "name": $0.name, "size": $0.size, "lastModified": $0.modified] as [String: Any] }
                self?.callPage("__soundstormPicked", list)
            }
        case "readFile":
            let req = body["req"] as? Int ?? 0
            let from = (body["from"] as? NSNumber)?.int64Value ?? 0
            let to = (body["to"] as? NSNumber)?.int64Value ?? 0
            let data = FileUploads.shared.read(body["id"] as? String ?? "", from: from, to: to)
            let b64 = data.map { "\"" + $0.base64EncodedString() + "\"" } ?? "null"
            webView.evaluateJavaScript("window.__soundstormFileData && window.__soundstormFileData(\(req), \(b64))")
        case "uploads":
            let command = body["cmd"] as? String ?? ""
            let data = body["data"] as? [String: Any] ?? [:]
            let origin = URL(string: message.frameInfo.securityOrigin.protocol + "://" + message.frameInfo.securityOrigin.host
                             + (message.frameInfo.securityOrigin.port == 0 ? "" : ":\(message.frameInfo.securityOrigin.port)"))
            Task {
                if let answer = await FileUploads.shared.handle(command, data: data, pageOrigin: origin) {
                    callPage("__soundstormUploads", answer)
                }
            }
        case "remoteVolume":
            volumeKeys.onPress = { [weak self] dir in
                self?.webView.evaluateJavaScript("window.__soundstormVolumeKey && window.__soundstormVolumeKey(\(dir))")
            }
            volumeKeys.set(body["on"] as? Bool ?? false, in: view.window)
        case "backup":
            PhotoBackup.shared.rememberServer(current, typed: server)
            let command = body["cmd"] as? String ?? ""
            let options = body["options"] as? [String: Any] ?? [:]
            Task {
                await PhotoBackup.shared.handle(command, options: options)
                reportBackup()
                // The page is up and signed in: a run that found no sign-in
                // at launch can go now.
                PhotoBackup.shared.start()
            }
        default:
            break
        }
    }

    /// Same scheme, host and port as where the page is now: everything else
    /// leaves the app for Safari, as a link out of an installed web app does.
    private func isServer(_ url: URL) -> Bool {
        url.scheme == current.scheme && url.host()?.lowercased() == current.host()?.lowercased()
            && url.port == current.port
    }

    /// The page moving itself to this install's secure home name, which it
    /// does after checking it can reach it (moveToSecureName in app.js): from
    /// the away-from-home name <id>.net.soundstorm.dev when the phone is at
    /// home, or from a LAN address like http://192.168.0.19:8099. That is the
    /// same server, not a link out, and was being sent to Safari.
    ///
    /// Only this install's names count. Anybody can get a *.net.soundstorm.dev
    /// name for a server of their own, so from a soundstorm.dev address the id
    /// must match. From a LAN address the id is not known, and any home name
    /// on the same port is accepted: home names only ever point at private
    /// addresses, and the page asking to go there is the server's own.
    private func sameInstall(_ url: URL) -> Bool {
        guard url.scheme == "https", url.port == current.port,
              let host = url.host()?.lowercased(),
              let (id, level) = Self.installName(host), level == "home"
        else { return false }
        if let (currentID, _) = current.host().flatMap({ Self.installName($0.lowercased()) }) {
            return id == currentID
        }
        return current.scheme == "http"
    }

    /// "<id>.home.soundstorm.dev" -> (id, "home"); nil for anything else.
    private static func installName(_ host: String) -> (String, String)? {
        let parts = host.split(separator: ".")
        guard parts.count == 4, parts[2] == "soundstorm", parts[3] == "dev",
              parts[1] == "home" || parts[1] == "net"
        else { return nil }
        return (String(parts[0]), String(parts[1]))
    }
}

extension WebViewController: WKNavigationDelegate {
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction) async -> WKNavigationActionPolicy {
        guard let url = action.request.url else { return .cancel }
        // No target frame is a new window, which createWebViewWith decides
        // (only for a link somebody tapped) - not the page itself.
        let mainFrame = action.targetFrame?.isMainFrame ?? false
        if mainFrame && sameInstall(url) {
            // Followed for now, never saved: the address typed stays the one
            // the app starts from. Saving a name reached from a plain-http
            // page let someone on the same Wi-Fi pin the app to a server of
            // their own for good (the security review; Android the same).
            current = url
            return .allow
        }
        if mainFrame && !isServer(url) && ["http", "https"].contains(url.scheme ?? "") {
            // Safari only for a link somebody followed: a page sending itself
            // elsewhere by script would otherwise throw the person out of the
            // app, as often as it liked (the security review; Android the same).
            if action.navigationType == .linkActivated {
                await UIApplication.shared.open(url)
            }
            return .cancel
        }
        if mainFrame && ["blob", "data"].contains(url.scheme ?? "") && !action.shouldPerformDownload {
            // A page made of a blob or data address would sit in the app's
            // window, looking like SoundStorm, with no address to show it is
            // not. Frames may (a book's pages are blobs); the app never is.
            return .cancel
        }
        if !["http", "https", "blob", "data", "about"].contains(url.scheme ?? "") {
            // mailto:, tel: and the like belong to other apps - only when
            // somebody followed such a link on the page itself, never because
            // a frame or a script asked.
            if mainFrame && action.navigationType == .linkActivated {
                await UIApplication.shared.open(url)
            }
            return .cancel
        }
        return .allow
    }

    #if DEBUG
    /// For the simulator, which has no lock screen to press: `-audioTest
    /// <path>` plays that address through the page's audio element, pauses
    /// it, then plays it again as the lock screen's play does, printing what
    /// the page sees at each step.
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard let path = UserDefaults.standard.string(forKey: "audioTest"), !audioTested else { return }
        audioTested = true
        let look = { (step: String) in
            webView.evaluateJavaScript("(() => { const a = document.getElementById('audio-player'); return JSON.stringify({ paused: a.paused, t: a.currentTime, d: a.duration, src: a.src }); })()") { r, _ in
                NSLog("audioTest %@: %@", step, "\(r ?? "nil")")
            }
        }
        Task {
            try? await Task.sleep(for: .seconds(3))
            _ = try? await webView.evaluateJavaScript("(() => { const a = document.getElementById('audio-player'); a.src = location.origin + '\(path)'; a.play(); return 1; })()")
            try? await Task.sleep(for: .seconds(6)); look("playing")
            _ = try? await webView.evaluateJavaScript("document.getElementById('audio-player').pause(); 1")
            try? await Task.sleep(for: .seconds(4)); look("paused")
            NativeAudio.shared.debugRemotePlay()
            try? await Task.sleep(for: .seconds(4)); look("lock screen play")
        }
    }
    #endif

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        showFailure(error)
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        showFailure(error)
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        // iOS reclaims a background web view's memory by killing its page.
        // Reloading is what Safari does when you come back to such a tab.
        load()
    }
}

extension WebViewController: WKUIDelegate {
    // A web view shows no alert(), confirm() or prompt() unless its app does,
    // and SoundStorm asks before removing downloads and big files.
    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo) async {
        await withCheckedContinuation { done in
            let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
            alert.addAction(UIAlertAction(title: "OK", style: .default) { _ in done.resume() })
            show(alert) { done.resume() }
        }
    }

    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo) async -> Bool {
        await withCheckedContinuation { done in
            let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
            alert.addAction(UIAlertAction(title: "Cancel", style: .cancel) { _ in done.resume(returning: false) })
            alert.addAction(UIAlertAction(title: "OK", style: .default) { _ in done.resume(returning: true) })
            show(alert) { done.resume(returning: false) }
        }
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String,
                 defaultText: String?, initiatedByFrame frame: WKFrameInfo) async -> String? {
        await withCheckedContinuation { done in
            let alert = UIAlertController(title: nil, message: prompt, preferredStyle: .alert)
            alert.addTextField { $0.text = defaultText }
            alert.addAction(UIAlertAction(title: "Cancel", style: .cancel) { _ in done.resume(returning: nil) })
            alert.addAction(UIAlertAction(title: "OK", style: .default) { [weak alert] _ in
                done.resume(returning: alert?.textFields?.first?.text ?? "")
            })
            show(alert) { done.resume(returning: nil) }
        }
    }

    /// Shows a dialog over whatever is on top. When it cannot be shown - the
    /// app is not on screen, or another dialog is up - it answers at once as
    /// Cancel would (`otherwise`): a question never shown never answered,
    /// and the page's confirm() waited for ever.
    private func show(_ alert: UIAlertController, otherwise: @escaping () -> Void) {
        var top: UIViewController = self
        while let next = top.presentedViewController { top = next }
        guard view.window != nil, !(top is UIAlertController), !top.isBeingDismissed else {
            otherwise()
            return
        }
        top.present(alert, animated: true)
    }

    /// target="_blank" (the ListenBrainz and LRCLIB links in Account): Safari.
    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        // Only a link somebody followed, and only to the web: not a window a
        // script opened, nor any other kind of address.
        if let url = action.request.url, action.navigationType == .linkActivated,
           ["http", "https"].contains(url.scheme ?? "") {
            UIApplication.shared.open(url)
        }
        return nil
    }
}

/// WKUserContentController holds its handlers strongly, and the controller
/// holds the web view, so a direct reference would never be freed.
private final class MessageRelay: NSObject, WKScriptMessageHandler {
    weak var target: WebViewController?

    init(_ target: WebViewController) { self.target = target }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.received(message)
    }
}

/// Shown when the server cannot be reached, with the two ways forward: try
/// again, or point the app at a different address.
private final class FailureView: UIView {
    var onRetry: (() -> Void)?
    var onChangeServer: (() -> Void)?

    private let title = UILabel()
    private let detail = UILabel()

    override init(frame: CGRect) {
        super.init(frame: frame)
        backgroundColor = .black

        title.font = .preferredFont(forTextStyle: .title3)
        title.textColor = .white
        title.numberOfLines = 0
        title.textAlignment = .center

        detail.font = .preferredFont(forTextStyle: .footnote)
        detail.textColor = .secondaryLabel
        detail.numberOfLines = 0
        detail.textAlignment = .center

        var retry = UIButton.Configuration.filled()
        retry.title = "Try again"
        retry.baseBackgroundColor = UIColor(red: 0x6a / 255, green: 0xa8 / 255, blue: 0xff / 255, alpha: 1)
        retry.baseForegroundColor = .black
        retry.cornerStyle = .large
        let retryButton = UIButton(configuration: retry)
        retryButton.addAction(UIAction { [weak self] _ in self?.onRetry?() }, for: .primaryActionTriggered)

        var change = UIButton.Configuration.plain()
        change.title = "Change server"
        change.baseForegroundColor = .secondaryLabel
        let changeButton = UIButton(configuration: change)
        changeButton.addAction(UIAction { [weak self] _ in self?.onChangeServer?() }, for: .primaryActionTriggered)

        let stack = UIStackView(arrangedSubviews: [title, detail, retryButton, changeButton])
        stack.axis = .vertical
        stack.alignment = .center
        stack.spacing = 12
        stack.setCustomSpacing(24, after: detail)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            stack.leadingAnchor.constraint(equalTo: layoutMarginsGuide.leadingAnchor, constant: 16),
            stack.trailingAnchor.constraint(equalTo: layoutMarginsGuide.trailingAnchor, constant: -16),
        ])
    }

    required init?(coder: NSCoder) { fatalError("not used") }

    func show(host: String, detail text: String) {
        title.text = "Can't reach SoundStorm at \(host)"
        detail.text = text
        isHidden = false
    }
}
