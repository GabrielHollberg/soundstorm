/* atrium's ebook reader.
 *
 * Rendering is foliate-js (MIT, vendored under vendor/foliate-js). We do not
 * render EPUB ourselves for the same reason we do not transcode video: the part
 * that looks easy is not. Reflowable text means a page number is meaningless -
 * change the font size and "page 47" is different words - so reading position
 * has to be a content-anchored locator. That is what EPUB CFI is, and it is
 * 13KB of somebody else's carefully debugged code.
 *
 * What IS ours, and what makes this feel like one product rather than a file
 * viewer: atrium unzips the book server-side and remembers where you stopped.
 *
 * This is a module (foliate-js is ES modules) while app.js is a classic script,
 * so the two talk through window.atriumReader rather than imports.
 */
'use strict';

import './vendor/foliate-js/view.js';
import { EPUB } from './vendor/foliate-js/epub.js';

const $ = (id) => document.getElementById(id);

// How often a reading position is written back. A page turn emits a location;
// persisting every one of them would rewrite atrium's state file on every tap.
const SAVE_INTERVAL_MS = 3000;

const session = {
  view: null,
  item: null,
  sizes: new Map(),
  pendingLocation: null,
  saveTimer: null,
};

function bookParams(item) {
  return new URLSearchParams({ source: item.sourceId, id: item.id });
}

/* ------------------------------------------------------------------ loading */

// foliate-js asks for resources by path; atrium has already unzipped the book,
// so the loader is three fetches rather than a zip implementation in the
// browser. The Go side owns the only EPUB parser in the project.
function makeLoader(item) {
  const base = bookParams(item);

  const resourceURL = (name) => {
    const params = new URLSearchParams(base);
    params.set('path', name);
    return `/api/book/resource?${params}`;
  };

  return {
    loadText: async (name) => {
      const resp = await fetch(resourceURL(name), { credentials: 'same-origin' });
      if (!resp.ok) return null;
      return resp.text();
    },
    loadBlob: async (name) => {
      const resp = await fetch(resourceURL(name), { credentials: 'same-origin' });
      if (!resp.ok) return null;
      return resp.blob();
    },
    getSize: (name) => session.sizes.get(name) ?? 0,
  };
}

async function loadManifest(item) {
  const resp = await fetch(`/api/book/manifest?${bookParams(item)}`, {
    credentials: 'same-origin',
  });
  if (!resp.ok) throw new Error('could not read the book');
  const body = await resp.json();

  session.sizes = new Map((body.entries || []).map((e) => [e.name, e.size]));
}

/* ----------------------------------------------------------------- progress */

async function loadProgress(item) {
  const resp = await fetch(`/api/book/progress?${bookParams(item)}`, {
    credentials: 'same-origin',
  });
  if (!resp.ok) return null;
  const body = await resp.json();
  return body.found ? body : null;
}

function scheduleSave(location, fraction) {
  session.pendingLocation = { location, fraction };
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
    await fetch(`/api/book/progress?${bookParams(item)}`, {
      method: 'PUT',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      // keepalive lets the last position survive the tab closing.
      keepalive: true,
      body: JSON.stringify(pending),
    });
  } catch {
    // Losing a bookmark is not worth interrupting someone's reading over.
  }
}

/* ------------------------------------------------------------------- reader */

export async function open(item) {
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

  try {
    await loadManifest(item);

    const book = await new EPUB(makeLoader(item)).init();

    const view = document.createElement('foliate-view');
    host.append(view);
    session.view = view;

    view.addEventListener('relocate', (event) => {
      const { cfi, fraction } = event.detail || {};
      if (typeof fraction === 'number') {
        $('reader-progress').textContent = `${Math.round(fraction * 100)}%`;
      }
      if (cfi) scheduleSave(cfi, fraction ?? 0);
    });

    await view.open(book);

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
  `);
}

export async function close() {
  await flushProgress();

  session.view?.close?.();
  session.view = null;
  session.item = null;
  session.sizes = new Map();

  $('reader-host').replaceChildren();
  $('reader-overlay').classList.add('hidden');
}

function turn(direction) {
  if (!session.view) return;
  if (direction < 0) session.view.prev();
  else session.view.next();
}

/* ------------------------------------------------------------------- wiring */

$('reader-close').addEventListener('click', close);
$('reader-download').addEventListener('click', () => {
  if (session.item) window.atriumDownloadBook?.(session.item);
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
window.atriumReader = { open, close };
