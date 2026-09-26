/* SoundStorm's ebook reader.
 *
 * Rendering is foliate-js (MIT, vendored under vendor/foliate-js). We do not
 * render EPUB ourselves for the same reason we do not transcode video: the part
 * that looks easy is not. Reflowable text means a page number is meaningless -
 * change the font size and "page 47" is different words - so reading position
 * has to be a content-anchored locator. That is what EPUB CFI is, and it is
 * 13KB of somebody else's carefully debugged code.
 *
 * What IS ours, and what makes this feel like one product rather than a file
 * viewer: SoundStorm unzips the book server-side and remembers where you stopped.
 *
 * This is a module (foliate-js is ES modules) while app.js is a classic script,
 * so the two talk through window.soundstormReader rather than imports.
 */
'use strict';

import './vendor/foliate-js/view.js';
import { EPUB } from './vendor/foliate-js/epub.js';

const $ = (id) => document.getElementById(id);

// How often a reading position is written back. A page turn emits a location;
// persisting every one of them would rewrite SoundStorm's state file on every tap.
const SAVE_INTERVAL_MS = 3000;

const session = {
  view: null,
  item: null,
  sizes: new Map(),
  pendingLocation: null,
  saveTimer: null,
  follow: null, // read-along: the timeline being followed, and where it is
};

function bookParams(item) {
  return new URLSearchParams({ source: item.sourceId, id: item.id });
}

/* ------------------------------------------------------------------ loading */

// foliate-js asks for resources by path; SoundStorm has already unzipped the book,
// so the loader is three fetches rather than a zip implementation in the
// browser. The Go side owns the only EPUB parser in the project.
// A book downloaded to this device is read from there when the server does
// not answer - the same addresses, kept in the downloads cache by app.js.
const OFFLINE_CACHE = 'soundstorm-offline-v1';
async function fetchOrKept(url) {
  // Downloaded: from the device, connection or not - quicker, and no data.
  try {
    const kept = await (await caches.open(OFFLINE_CACHE)).match(url);
    if (kept) return kept;
  } catch {
    // no cache here; ask the server
  }
  try {
    const resp = await fetch(url, { credentials: 'same-origin' });
    if (resp.ok) return resp;
  } catch {
    // no connection
  }
  return null;
}

function makeLoader(item) {
  const base = bookParams(item);

  const resourceURL = (name) => {
    const params = new URLSearchParams(base);
    params.set('path', name);
    return `/api/book/resource?${params}`;
  };

  return {
    loadText: async (name) => {
      const resp = await fetchOrKept(resourceURL(name));
      return resp ? resp.text() : null;
    },
    loadBlob: async (name) => {
      const resp = await fetchOrKept(resourceURL(name));
      return resp ? resp.blob() : null;
    },
    getSize: (name) => session.sizes.get(name) ?? 0,
  };
}

async function loadManifest(item) {
  const resp = await fetchOrKept(`/api/book/manifest?${bookParams(item)}`);
  if (!resp) throw new Error('could not read the book');
  const body = await resp.json();

  session.sizes = new Map((body.entries || []).map((e) => [e.name, e.size]));
}

/* ----------------------------------------------------------------- progress */

// Where somebody is in a book is also kept on the device, so a book read
// offline opens where it was left and the place reaches the server with the
// next save that gets through.
const readKey = (item) => `soundstorm-read:${item.sourceId}/${item.id}`;
function localRead(item) {
  try {
    return JSON.parse(localStorage.getItem(readKey(item)) || 'null');
  } catch {
    return null;
  }
}
function keepLocalRead(item, place, synced) {
  try {
    localStorage.setItem(readKey(item), JSON.stringify({ ...place, synced, at: Date.now() }));
  } catch { /* storage full */ }
}

async function loadProgress(item) {
  const here = localRead(item);
  if (here && !here.synced && here.location) return here;
  try {
    const resp = await fetch(`/api/book/progress?${bookParams(item)}`, {
      credentials: 'same-origin',
    });
    if (resp.ok) {
      const body = await resp.json();
      if (body.found) return body;
    }
  } catch {
    // offline
  }
  return here && here.location ? here : null;
}

function scheduleSave(location, fraction) {
  session.pendingLocation = { location, fraction };
  if (session.item) keepLocalRead(session.item, { location, fraction }, false);
  if (session.saveTimer) return;
  session.saveTimer = setTimeout(flushProgress, SAVE_INTERVAL_MS);
}

async function flushProgress() {
  clearTimeout(session.saveTimer);
  session.saveTimer = null;

  const pending = session.pendingLocation;
  const item = session.item;
  if (!pending || !item) return;
  session.pendingLocation = null;

  try {
    const resp = await fetch(`/api/book/progress?${bookParams(item)}`, {
      method: 'PUT',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      // keepalive lets the last position survive the tab closing.
      keepalive: true,
      body: JSON.stringify(pending),
    });
    if (resp.ok) keepLocalRead(item, pending, true);
  } catch {
    // Losing a bookmark is not worth interrupting someone's reading over.
  }
}

/* ------------------------------------------------------------------- reader */

function isPDF(item) {
  return ((item.extra && item.extra.format) || '').toLowerCase() === 'pdf';
}

export async function open(item, options = {}) {
  const overlay = $('reader-overlay');
  const host = $('reader-host');

  $('reader-title').textContent = item.title;
  $('reader-sub').textContent =
    (item.creators && item.creators.join(', ')) || '';
  $('reader-progress').textContent = '';
  $('reader-error').classList.add('hidden');
  host.replaceChildren();
  overlay.classList.remove('hidden');

  session.item = item;

  // A PDF has no spine to walk and no CFI to remember: it goes to the
  // browser's viewer whole. Reading position is lost, which is an honest gap
  // rather than a hidden one - browser PDF viewers do not expose it.
  if (isPDF(item)) {
    show($('reader-host'), false);
    for (const control of ['reader-prev', 'reader-next']) show($(control), false);
    $('reader-progress').textContent = 'PDF';

    const frame = $('reader-pdf');
    frame.src = (await window.soundstormOfflineURL?.(item)) || streamPath(item);
    show(frame, true);
    return;
  }

  show($('reader-host'), true);
  for (const control of ['reader-prev', 'reader-next']) show($(control), true);
  show($('reader-pdf'), false);

  try {
    await loadManifest(item);

    const book = await new EPUB(makeLoader(item)).init();

    const view = document.createElement('foliate-view');
    host.append(view);
    session.view = view;
    // Each chapter is its own document inside the view, and a touch on the
    // text never reaches this page, so swipe-to-close listens in each one.
    view.addEventListener('load', (event) => {
      if (event.detail && event.detail.doc) swipeToClose(event.detail.doc);
    });

    view.addEventListener('relocate', (event) => {
      const { cfi, fraction } = event.detail || {};
      if (typeof fraction === 'number') {
        $('reader-progress').textContent = `${Math.round(fraction * 100)}%`;
      }
      if (cfi) scheduleSave(cfi, fraction ?? 0);
    });

    await view.open(book);

    // Which renderer foliate picked decides whether a finger can turn the
    // page, and the arrows are hidden on touch only where it can.
    //
    // paginator.js - the reflowable one, which is almost every EPUB - handles
    // touch itself, and swiping was measured turning the page to exactly the
    // positions the arrows reach. fixed-layout.js contains no touch handling
    // at all, so for a comic or an illustrated book the arrows are the only
    // way forward on a phone. view.isFixedLayout is set by open(), which is
    // why this reads it here rather than before.
    overlay.classList.toggle('fixed-layout', view.isFixedLayout === true);

    // open() parses the book and builds the renderer but paints nothing. init()
    // is what puts a page on screen - either the one we left off on, or the
    // first. Missing this is a blank reader with no error anywhere.
    const saved = await loadProgress(item);
    try {
      await view.init({ lastLocation: saved?.location || null });
    } catch {
      // A stale locator - the file changed under us - should not stop the book
      // from opening. Start at the beginning instead.
      await view.init({});
    }

    applyTheme(view);
    if (options.timeline && options.timeline.length && options.audiobook) {
      startFollowing(view, options.timeline, options.audiobook);
    }
  } catch (err) {
    const error = $('reader-error');
    error.textContent = `Could not open this book: ${err.message}`;
    error.classList.remove('hidden');
  }
}

// foliate-view renders the book inside an iframe it controls, so the reading
// theme has to be pushed in rather than inherited.
function applyTheme(view) {
  view.renderer?.setStyles?.(`
    @namespace epub "http://www.idpf.org/2007/ops";
    html { color-scheme: dark; }
    body {
      background: #14181f;
      color: #dfe3ea;
      font-size: 1.05rem;
      line-height: 1.6;
    }
    a { color: #6aa8ff; }
    img { max-width: 100%; height: auto; }
    .ss-reading {
      background: rgba(106, 168, 255, 0.24);
      border-radius: 3px;
      box-decoration-break: clone;
      -webkit-box-decoration-break: clone;
    }
  `);
}

/* --------------------------------------------------------------- read-along */

// The page follows the audiobook. The timeline is every sentence of the synced
// book with its place on the recording's whole timeline, in order; four times
// a second the reader finds the sentence being read, turns to it if it is not
// on the page, and lights it. Turning the page by hand pauses the turning for
// a little while (the sentence is still lit), so somebody can look back
// without being dragged forward mid-thought.
const FOLLOW_EVERY_MS = 250;
const HANDS_OFF_MS = 12000;

function startFollowing(view, timeline, audiobook) {
  stopFollowing();
  const follow = {
    view, timeline, audiobook, index: -1, lit: null, movedByUs: false, handsOffUntil: 0,
    highlight: window.soundstormHighlight ? window.soundstormHighlight() : true,
  };
  renderHighlightButton(follow);
  session.follow = follow;
  // The book laying itself out when it opens (and again when its styles are
  // set) reports a page change too; that is not somebody turning the page,
  // and taking it for one held the page still for the first twelve seconds.
  follow.settledAt = Date.now() + 2500;
  follow.onRelocate = () => {
    if (follow.movedByUs || Date.now() < follow.settledAt) return;
    follow.handsOffUntil = Date.now() + HANDS_OFF_MS;
  };
  view.addEventListener('relocate', follow.onRelocate);
  follow.timer = setInterval(() => tick(follow), FOLLOW_EVERY_MS);
  // The first page shown is wherever the voice is, not the saved place.
  follow.handsOffUntil = 0;
}

// The Highlight button: shown while reading along, pressed while the
// sentence being read is lit. Off, the page still turns with the voice.
function renderHighlightButton(follow) {
  const button = $('reader-highlight');
  button.classList.toggle('hidden', !follow);
  if (!follow) return;
  button.setAttribute('aria-pressed', String(follow.highlight));
  button.classList.toggle('on', follow.highlight);
}

$('reader-highlight').addEventListener('click', () => {
  const follow = session.follow;
  if (!follow) return;
  follow.highlight = !follow.highlight;
  if (!follow.highlight) unlight(follow);
  else follow.index = -1; // light the current sentence straight away
  renderHighlightButton(follow);
  window.soundstormSetHighlight?.(follow.highlight);
});

function stopFollowing() {
  const follow = session.follow;
  renderHighlightButton(null);
  if (!follow) return;
  clearInterval(follow.timer);
  follow.view.removeEventListener('relocate', follow.onRelocate);
  unlight(follow);
  session.follow = null;
}

// The sentence playing at t: the last one starting at or before it.
function sentenceAt(timeline, t) {
  let lo = 0;
  let hi = timeline.length - 1;
  let found = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (timeline[mid].t <= t) { found = mid; lo = mid + 1; } else hi = mid - 1;
  }
  return found;
}

function unlight(follow) {
  const el = follow.lit && follow.lit.deref();
  if (el) el.classList.remove('ss-reading');
  follow.lit = null;
}

async function tick(follow) {
  if (session.follow !== follow || follow.busy) return;
  const t = window.soundstormListening?.(follow.audiobook.sourceId, follow.audiobook.id);
  if (typeof t !== 'number' || !Number.isFinite(t)) return;
  let index = sentenceAt(follow.timeline, t);
  // Before the first sentence - an audiobook's opening credits, which are not
  // in the book - go to where the reading will begin rather than sitting on
  // the cover, but light nothing yet.
  const early = index < 0;
  if (early) {
    if (follow.primed) return;
    follow.primed = true;
    index = 0;
  }
  if (index === follow.index) return;
  follow.index = index;
  follow.busy = true;
  try {
    const { view } = follow;
    const resolved = view.resolveNavigation(follow.timeline[index].h);
    if (!resolved) return;
    if (Date.now() >= follow.handsOffUntil) {
      follow.movedByUs = true;
      try {
        await view.renderer.goTo(resolved);
      } finally {
        // The relocate this causes can arrive a little after goTo settles.
        setTimeout(() => { follow.movedByUs = false; }, 400);
      }
    }
    const contents = view.renderer.getContents?.() || [];
    const shown = contents.find((c) => c.index === resolved.index);
    const el = shown && resolved.anchor && resolved.anchor(shown.doc);
    unlight(follow);
    if (early) follow.index = -1; // the first sentence, not yet being read
    else if (el && el.classList && follow.highlight) {
      el.classList.add('ss-reading');
      follow.lit = new WeakRef(el);
    }
  } catch {
    // A sentence that cannot be found is skipped; the next one may be.
  } finally {
    follow.busy = false;
  }
}

export async function close() {
  stopFollowing();
  await flushProgress();

  const frame = $('reader-pdf');
  frame.removeAttribute('src');
  show(frame, false);

  session.view?.close?.();
  session.view = null;
  session.item = null;
  session.sizes = new Map();

  $('reader-host').replaceChildren();
  $('reader-overlay').classList.add('hidden');
  // The position was just saved, so the app's Continue row can catch up.
  window.dispatchEvent(new Event('soundstorm:reader-closed'));
}

function turn(direction) {
  if (!session.view) return;
  if (direction < 0) session.view.prev();
  else session.view.next();
}

/* ------------------------------------------------------------------- wiring */

$('reader-close').addEventListener('click', close);
$('reader-download').addEventListener('click', () => {
  if (session.item) window.soundstormDownloadBook?.(session.item);
});
$('reader-prev').addEventListener('click', () => turn(-1));
$('reader-next').addEventListener('click', () => turn(1));

document.addEventListener('keydown', (event) => {
  if ($('reader-overlay').classList.contains('hidden')) return;
  switch (event.key) {
    case 'Escape':
      close();
      break;
    case 'ArrowLeft':
    case 'PageUp':
      turn(-1);
      break;
    case 'ArrowRight':
    case 'PageDown':
    case ' ':
      event.preventDefault();
      turn(1);
      break;
  }
});

// Save the position if the tab goes away mid-chapter.
window.addEventListener('pagehide', () => {
  if (session.pendingLocation) flushProgress();
});

// app.js is a classic script and cannot import this module.
window.soundstormReader = { open, close };

/* ------------------------------------------------------ swipe down to close */

// Drag the book down to put it away, as Now Playing, a photo and a film go:
// it follows the finger and goes past a third of the screen or on a flick,
// or springs back. Sideways stays the page turn. Measured on the screen, not
// in the chapter: the chapter moves with the finger, so its own coordinates
// would say the finger had not moved at all.
function swipeToClose(target) {
  const overlay = $('reader-overlay');
  let start = null;
  target.addEventListener('touchstart', (event) => {
    start = null;
    if (event.touches.length !== 1) return;
    const t = event.touches[0];
    start = { x: t.screenX, y: t.screenY, at: Date.now(), down: false, dy: 0 };
  }, { passive: true });
  target.addEventListener('touchmove', (event) => {
    if (!start || event.touches.length !== 1) return;
    const t = event.touches[0];
    const dx = t.screenX - start.x;
    const dy = t.screenY - start.y;
    if (!start.down) {
      if (dy < 14 || Math.abs(dy) < Math.abs(dx) * 1.6) {
        if (Math.abs(dx) > 14) start = null; // a page turn
        return;
      }
      start.down = true;
      overlay.style.transition = 'none';
    }
    start.dy = Math.max(0, dy);
    overlay.style.transform = `translateY(${start.dy}px)`;
  }, { passive: true });
  const end = () => {
    if (!start || !start.down) { start = null; return; }
    const { dy, at } = start;
    start = null;
    const speed = dy / Math.max(Date.now() - at, 1);
    overlay.style.transition = 'transform 0.22s ease-out';
    if (dy > overlay.clientHeight * 0.3 || (speed > 0.6 && dy > 50)) {
      overlay.style.transform = `translateY(${overlay.clientHeight}px)`;
      setTimeout(async () => {
        await close();
        overlay.style.transition = '';
        overlay.style.transform = '';
      }, 220);
    } else {
      overlay.style.transform = '';
      setTimeout(() => { overlay.style.transition = ''; }, 220);
    }
  };
  target.addEventListener('touchend', end);
  target.addEventListener('touchcancel', end);
}

// The bar and the margins around the book are this page's own.
swipeToClose($('reader-overlay'));
