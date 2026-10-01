package dev.soundstorm.app

/**
 * Run in the page before any of its own scripts, on the server's origin only.
 *
 * Three things:
 *  - `window.soundstormApp`, as the iPhone app sets it, which is how app.js
 *    knows to offer "Change server";
 *  - `window.webkit.messageHandlers.soundstorm.postMessage`, the iPhone app's
 *    channel, answered here too - so app.js has one way to talk to either app;
 *  - `navigator.mediaSession`, which Android's WebView does not have. The page
 *    already describes what is playing through it (the song, playing or
 *    paused, the position, which buttons work); this passes that to the app,
 *    which shows it on the lock screen and in the notification, and hands the
 *    buttons pressed there back to the page's own handlers. That is what the
 *    APK note under Music said a WebView app would lose.
 *  - and the page's theme colour, which the status bar takes, as an installed
 *    web app's does.
 *
 * `SoundStormNative` is injected by addWebMessageListener, for the server's
 * origin alone.
 */
object PageScript {
    const val SOURCE = """
(() => {
  if (window.soundstormApp || typeof SoundStormNative === 'undefined') return;
  const post = (message) => SoundStormNative.postMessage(JSON.stringify(message));
  window.soundstormApp = { version: 1, platform: 'android', nativeAudio: true };
  // The page's next song, handed to the native player ahead, so it moves
  // into it without stopping (NativeAudio "queue").
  window.soundstormApp.queueNext = (url) => post({ type: 'audio', cmd: url ? 'queue' : 'unqueue', url: url || '' });
  // The songs after it too, each with its title and cover, so the player
  // carries on if Android ends the page in the background.
  window.soundstormApp.queueUpcoming = (items) => post({ type: 'audio', cmd: 'upcoming', items: items || [] });
  // Phone photo backup (PhotoBackup), on a phone only: Settings turns it on
  // and shows how it is going, answered through window.__soundstormBackup.
  window.soundstormApp.photoBackup = !/SoundStormTV/.test(navigator.userAgent);
  window.soundstormApp.backup = (cmd, options) => post({ type: 'backup', cmd, options: options || {} });
  window.webkit = window.webkit || {};
  window.webkit.messageHandlers = window.webkit.messageHandlers || {};
  window.webkit.messageHandlers.soundstorm = { postMessage: post };

  if (!('mediaSession' in navigator)) {
    const handlers = {};
    let metadata = null;
    let state = 'none';
    let position = null;
    let queued = false;
    // Sent at most once a frame's worth of changes: app.js sets the song,
    // the state and the position one after another.
    const send = () => {
      if (queued) return;
      queued = true;
      setTimeout(() => {
        queued = false;
        post({
          type: 'media',
          state,
          actions: Object.keys(handlers),
          position,
          metadata: metadata && {
            title: metadata.title || '',
            artist: metadata.artist || '',
            album: metadata.album || '',
            artwork: (metadata.artwork || []).map((a) => {
              try { return new URL(a.src, location.href).href; } catch { return ''; }
            }).filter(Boolean),
          },
        });
      }, 0);
    };
    window.MediaMetadata = class MediaMetadata {
      constructor(init = {}) {
        this.title = init.title || '';
        this.artist = init.artist || '';
        this.album = init.album || '';
        this.artwork = init.artwork || [];
      }
    };
    const session = {
      get metadata() { return metadata; },
      set metadata(value) { metadata = value; send(); },
      get playbackState() { return state; },
      set playbackState(value) { state = value || 'none'; send(); },
      setActionHandler(action, handler) {
        if (handler) handlers[action] = handler; else delete handlers[action];
        send();
      },
      setPositionState(p) {
        position = p && Number.isFinite(p.duration) ? {
          duration: p.duration, position: p.position || 0, playbackRate: p.playbackRate || 1,
        } : null;
        send();
      },
    };
    Object.defineProperty(navigator, 'mediaSession', { value: session, configurable: true });
    // The app calls this for a button pressed on the lock screen, in the
    // notification, or on headphones.
    window.__soundstormMediaAction = (action, details) => {
      const handler = handlers[action];
      if (handler) handler(Object.assign({ action }, details || {}));
    };
  }

  // The page's audio element, played by Android's own media player
  // (NativeAudio, AudioService): a song on the server is handed over and
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
    // song: a page made again after Android ended the last one finds the
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

  // Paused by something other than the page - an alarm, a call - rather than
  // by a button or the page itself: the app is told, keeps itself running and
  // plays again once that is over (PlaybackService). Reported as music that
  // stayed stopped after an alarm. Everything the page pauses goes through
  // pause(), so a pause event with no pause() just before it came from
  // outside; a song that ended is not an interruption.
  let pausedAt = 0;
  const pause = HTMLMediaElement.prototype.pause;
  HTMLMediaElement.prototype.pause = function () { pausedAt = Date.now(); return pause.apply(this, arguments); };
  document.addEventListener('pause', (ev) => {
    const el = ev.target;
    if (!el || el.id !== 'audio-player' || el.ended) return;
    if (Date.now() - pausedAt > 500) post({ type: 'interrupted' });
  }, true);
  document.addEventListener('playing', (ev) => {
    if (ev.target && ev.target.id === 'audio-player') post({ type: 'resumed' });
  }, true);

  const themeColor = () => {
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) post({ type: 'themeColor', color: meta.content || '' });
  };
  const watch = () => {
    themeColor();
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) new MutationObserver(themeColor).observe(meta, { attributes: true, attributeFilter: ['content'] });
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', watch);
  else watch();
})();
"""
}
