// SoundStorm's service worker.
//
// It exists so the app can be installed to a home screen. It is deliberately
// the smallest thing that does that, because the failure modes of a cache in
// front of a media server are all worse than the problem being solved.
//
// Three rules it must never break:
//
//   1. Nothing under /api/ is ever cached. Search results, sessions, reading
//      positions and the account list are all live state, and serving a stale
//      one is worse than not answering.
//   2. Media bytes are never touched. Audio, video, HLS segments and book
//      resources arrive as range requests, and a service worker that answers
//      one without honouring the Range header breaks seeking in a way that
//      looks like a corrupt file.
//   3. Page loads are never answered - with one exception, below. It used to
//      serve a cached copy of the
//      shell whenever loading the page failed, which hid the one thing
//      somebody needs to see when the certificate changes: the browser's own
//      warning. A browser pins a clicked-through exception to one exact
//      certificate, so a reinstall - or the yearly renewal - made the page
//      fail, the worker answered with the cached shell, every request behind
//      it failed, and "cannot reach SoundStorm" was all there was. Opening a
//      new tab changed nothing, because the worker answered that too. Left to
//      the browser, the same failure is a warning people know how to get past.
//
// The exception to rule 3 is opening the app with no connection, for
// downloads. It applies only on the real *.soundstorm.dev names, whose
// certificates are trusted and renew themselves: the trap rule 3 exists for -
// a clicked-through self-signed certificate changing underneath a cached page -
// cannot happen there. On an IP address, localhost, or anything self-signed,
// rule 3 holds exactly as written. And only when the network actually failed
// and the page kept a copy of itself because something was downloaded
// (OFFLINE_SHELL, filled by app.js); otherwise the browser's own error stands.
//
// Requests it does not explicitly handle are left alone entirely - not passed
// through fetch(), but never given to respondWith() in the first place, so the
// browser handles them as if no worker existed.
//
// What it does answer is network-first. On a home network the server is metres
// away, so the cache is a fallback for the seconds it is restarting, not a
// performance layer to reason about. That also means a docker compose pull
// cannot leave somebody pinned to an old build.

// v2 dropped the cached page; bumping it clears what v1 stored.
const VERSION = 'v2';
const CACHE = `soundstorm-shell-${VERSION}`;

// The shell only. Not the vendored reader or hls.js: those are large, loaded
// lazily and only for one screen each, so precaching them would spend a
// megabyte on first visit to save nothing on the common path.
const SHELL = [
  '/static/style.css',
  '/static/app.js',
  '/static/favicon.svg',
];

self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(CACHE)
      .then(cache => cache.addAll(SHELL))
      // A failed precache must not leave a worker that never installs; the
      // fetch handler copes with an empty cache perfectly well.
      .catch(() => {})
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys()
      .then(names => Promise.all(
        names.filter(n => n.startsWith('soundstorm-shell-') && n !== CACHE)
             .map(n => caches.delete(n))))
      .then(() => self.clients.claim())
  );
});

// handles decides what this worker is willing to answer for. Anything else is
// none of its business.
function handles(request, url) {
  if (request.method !== 'GET') return false;
  if (url.origin !== self.location.origin) return false;
  // Range requests are the media path, whatever the URL says.
  if (request.headers.has('range')) return false;
  if (url.pathname.startsWith('/api/')) return false;
  // See rule 3: the browser, not this worker, has to be the one that fails a
  // page load, or a changed certificate becomes a dead end.
  if (request.mode === 'navigate') return false;
  return SHELL.includes(url.pathname);
}

const OFFLINE_SHELL = 'soundstorm-offline-shell-v1';

function offlineStartAllowed(request, url) {
  return request.mode === 'navigate'
    && url.origin === self.location.origin
    && /\.soundstorm\.dev$/.test(url.hostname);
}

self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);
  if (offlineStartAllowed(event.request, url)) {
    event.respondWith(fetch(event.request).catch(async () => {
      const cache = await caches.open(OFFLINE_SHELL);
      return (await cache.match('/')) || Response.error();
    }));
    return;
  }
  if (!handles(event.request, url)) return;

  event.respondWith((async () => {
    try {
      const response = await fetch(event.request);
      // Only a real answer is worth keeping. Caching a 404 or a redirect to
      // the login gate would survive the thing that caused it.
      if (response && response.ok && response.type === 'basic') {
        const copy = response.clone();
        caches.open(CACHE).then(cache => cache.put(event.request, copy)).catch(() => {});
      }
      return response;
    } catch (err) {
      const cached = await caches.match(event.request);
      if (cached) return cached;
      throw err;
    }
    // (caches.match searches every cache, the offline shell's included.)
  })());
});
