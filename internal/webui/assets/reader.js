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

if (window.top !== window.self) throw new Error('SoundStorm does not run inside a frame');

// A book is somebody's upload, and its chapters render as documents with this
// app's origin - where a script, even one naming our own files by absolute
// address, would run as whoever is reading. The reader needs none of a
// book's scripts, so every chapter document loses them before it is shown.
//
// The type is the book's own say (its manifest), so it is read loosely - any
// case, any parameters, and anything XML - or a chapter declared text/xml or
// "text/html;charset=utf-8" went through untouched (a security review). And
// not only <script>: event attributes, javascript: links, frames, objects and
// refreshes go too. The page's CSP stops most of these already; this does not
// lean on every web view passing it on to a book's frames.
// SVG animation can set an href to javascript: where no attribute shows it,
// and a link other than a stylesheet can tell an outside party the book was
// opened (DNS prefetch is outside the CSP) - both found by a review.
const DROP = 'script, iframe, frame, object, embed, meta[http-equiv], base, set, animate, animateMotion, animateTransform, link:not([rel~="stylesheet" i])';
// What a book may point at: its own files, which are relative. A reference to
// the server itself (root-relative, or this address) would be fetched with the
// reader's sign-in - a cover's <img> starting film conversions as whoever opens
// the book (a security review).
const REF_ATTRS = new Set(['src', 'href', 'xlink:href', 'poster', 'srcset', 'data', 'background', 'action', 'formaction']);
function pointsAtServer(value) {
  const v = value.replace(/[\s\u0000-\u001f]/g, '');
  if (v.startsWith('/') || v.startsWith('\\')) return true;
  try {
    const u = new URL(v);
    return u.host === location.host;
  } catch {
    return false;
  }
}
function documentType(type) {
  const base = String(type || '').split(';')[0].trim().toLowerCase();
  if (base === 'text/html') return 'text/html';
  if (base === 'image/svg+xml') return 'image/svg+xml';
  if (base === 'application/xhtml+xml') return 'application/xhtml+xml';
  if (base.endsWith('/xml') || base.endsWith('+xml')) return 'application/xml';
  return null;
}
function withoutScripts(book) {
  book.transformTarget?.addEventListener('data', ({ detail }) => {
    const type = documentType(detail.type);
    if (!type) {
      // A part the book gives no type at all becomes an untyped Blob, which
      // a browser sniffs - and renders as a page, uncleaned, if it looks like
      // one (a security review). Typed as text, it can only ever be text.
      // A stylesheet keeps no type, which a browser still accepts as CSS.
      if (!String(detail.type || '').trim() && !/\.css$/i.test(detail.name || '')) detail.type = 'text/plain';
      return;
    }
    // foliate parses and rewrites as text only the four exact types it knows;
    // any other spelling of a page (text/html;charset=utf-8, TEXT/HTML,
    // application/xml) arrives here as a Blob it builds the frame from as it
    // is, so the stripping below never saw it (a security review). Such a
    // Blob is read as text and cleaned like the rest, and given the plain
    // type, so what the frame renders is what was cleaned.
    detail.type = type;
    detail.data = Promise.resolve(detail.data).then(async (data) => {
      if (data && typeof data !== 'string' && typeof data.text === 'function') data = await data.text();
      if (typeof data !== 'string') return data;
      const doc = new DOMParser().parseFromString(data, type);
      let changed = false;
      for (const el of doc.querySelectorAll(DROP)) { el.remove(); changed = true; }
      // Processing instructions other than a CSS stylesheet (XSLT).
      for (const node of [...doc.childNodes]) {
        if (node.nodeType === Node.PROCESSING_INSTRUCTION_NODE && !(node.target === 'xml-stylesheet' && /type\s*=\s*["']text\/css["']/i.test(node.data))) {
          node.remove();
          changed = true;
        }
      }
      for (const el of doc.querySelectorAll('style')) {
        if (/url\(\s*["']?(\/|https?:)/i.test(el.textContent || '')) {
          el.textContent = el.textContent.replace(/url\(\s*["']?(\/|https?:)[^)]*\)/gi, 'none');
          changed = true;
        }
      }
      for (const el of doc.querySelectorAll('*')) {
        for (const attr of [...el.attributes]) {
          const name = attr.name.toLowerCase();
          const value = attr.value.replace(/[\s\u0000-\u001f]/g, '').toLowerCase();
          if (name.startsWith('on') || ((name === 'href' || name.endsWith(':href') || name === 'src' || name === 'action' || name === 'formaction')
              && (value.startsWith('javascript:') || value.startsWith('vbscript:')))) {
            el.removeAttribute(attr.name);
            changed = true;
          } else if ((REF_ATTRS.has(name) || name.endsWith(':href')) && attr.value.split(',').some((part) => pointsAtServer(part.trim().split(/\s+/)[0] || ''))) {
            el.removeAttribute(attr.name);
            changed = true;
          } else if (name === 'style' && /url\(\s*["']?(\/|https?:)/i.test(attr.value)) {
            el.removeAttribute(attr.name);
            changed = true;
          }
        }
      }
      return changed ? new XMLSerializer().serializeToString(doc) : data;
    });
  });
}

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

// opening counts opens: a book closed while it was still opening carried on
// behind the app - its view added, read-along's timer and wake lock started
// behind a hidden reader (a review).
let opening = 0;

export async function open(item, options = {}) {
  const me = ++opening;
  const stillOpen = () => me === opening;
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
    if (!stillOpen()) return;
    withoutScripts(book);

    const view = document.createElement('foliate-view');
    host.append(view);
    session.view = view;
    // Each chapter is its own document inside the view, and a touch on the
    // text never reaches this page, so swipe-to-close listens in each one.
    view.addEventListener('load', (event) => {
      if (event.detail && event.detail.doc) swipeToClose(event.detail.doc);
    });

    // A link out of the book opens in a new tab that cannot reach back into
    // this one, and only if it is a web address.
    view.addEventListener('external-link', (event) => {
      event.preventDefault();
      const href = event.detail && event.detail.a && event.detail.a.href;
      if (/^https?:/i.test(href || '')) window.open(href, '_blank', 'noopener,noreferrer');
    });

    view.addEventListener('relocate', (event) => {
      const { cfi, fraction } = event.detail || {};
      if (typeof fraction === 'number') {
        $('reader-progress').textContent = `${Math.round(fraction * 100)}%`;
      }
      if (cfi) scheduleSave(cfi, fraction ?? 0);
    });

    await view.open(book);
    if (!stillOpen()) return;

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

    if (!stillOpen()) return;
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
  keepAwake(true);
}

// While a book reads along, the screen stays on: the pages turn by
// themselves, so nobody is touching the phone to keep it awake. A wake lock
// lasts only while the page is visible - the browser drops it when the
// phone is locked or the app is left - so it is asked for again on return.
// Where there is no Wake Lock (older browsers, plain http, which is not a
// secure context), the screen simply sleeps as before.
let wakeLock = null;
let wakeAsking = false;
async function keepAwake(on) {
  if (!on) {
    const lock = wakeLock;
    wakeLock = null;
    if (lock) lock.release().catch(() => {});
    return;
  }
  // One request at a time: two quick calls took two locks, and the first was
  // never let go, keeping the screen on after following stopped (a review).
  if (wakeLock || wakeAsking || !('wakeLock' in navigator) || document.visibilityState !== 'visible') return;
  wakeAsking = true;
  try {
    const lock = await navigator.wakeLock.request('screen');
    if (!session.follow) {
      lock.release().catch(() => {});
      return;
    }
    wakeLock = lock;
    lock.addEventListener('release', () => { if (wakeLock === lock) wakeLock = null; });
  } catch {
    // Refused - low battery mode, or not allowed here. Nothing to do.
  } finally {
    wakeAsking = false;
  }
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible' && session.follow) keepAwake(true);
});

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
  keepAwake(false);
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
  if (index === follow.index) {
    followWithinSentence(follow, t);
    return;
  }
  follow.index = index;
  follow.within = null;
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
    if (!early && el) {
      const m = follow.timeline[index];
      const end = m.e > m.t ? m.e : follow.timeline[index + 1]?.t;
      if (end - m.t > 2) follow.within = { el: new WeakRef(el), start: m.t, end, at: 0 };
    }
  } catch {
    // A sentence that cannot be found is skipped; the next one may be.
  } finally {
    follow.busy = false;
  }
}

// A long sentence runs over the page, and the page used to wait for the next
// sentence to begin before turning - reported as run-on sentences turning
// late. So within a sentence the voice's place is estimated from how far
// through its time it is, as a share of its letters (a narrator's pace is
// near even within a sentence), and the page goes to the one holding that
// letter: it turns as the voice reaches the first words over the page. Only
// for sentences over two seconds, and only once the estimate has moved on by
// a few letters; scrolling to the page already showing changes nothing.
// Aimed a little ahead of the voice: a reader has finished the last line a
// moment before the narrator reaches the first word over the page, and the
// estimate drifts with the narrator's pace - aimed at the voice itself it was
// "better, but still a little late, sometimes perfect" (the owner).
const WITHIN_LEAD_S = 1.0;
function followWithinSentence(follow, t) {
  const w = follow.within;
  if (!w || follow.busy || Date.now() < follow.handsOffUntil) return;
  const el = w.el.deref();
  if (!el || !el.isConnected) { follow.within = null; return; }
  const share = Math.min(1, Math.max(0, (t + WITHIN_LEAD_S - w.start) / (w.end - w.start)));
  const texts = [];
  let total = 0;
  const walk = el.ownerDocument.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) {
    texts.push(n);
    total += n.data.length;
  }
  if (!total) return;
  let want = Math.floor(share * total);
  if (want - w.at < 12) return;
  w.at = want;
  for (const n of texts) {
    if (want <= n.data.length) {
      const range = el.ownerDocument.createRange();
      range.setStart(n, Math.min(want, n.data.length));
      range.collapse(true);
      follow.movedByUs = true;
      Promise.resolve(follow.view.renderer.scrollToAnchor?.(range))
        .catch(() => {})
        .finally(() => setTimeout(() => { follow.movedByUs = false; }, 400));
      return;
    }
    want -= n.data.length;
  }
}

export async function close() {
  opening++;
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
  // Now Playing or a menu over the book has the keys: Escape closed them and
  // the book at once, and the arrows turned pages behind them (a review).
  if (document.body.classList.contains('np-open') || !$('item-menu').classList.contains('hidden')) return;
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
