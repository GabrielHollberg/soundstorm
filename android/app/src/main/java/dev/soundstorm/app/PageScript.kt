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
  window.soundstormApp = { version: 1, platform: 'android' };
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
