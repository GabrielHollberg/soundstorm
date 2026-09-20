/* SoundStorm UI.
 *
 * Vanilla JS, no build step. Three jobs:
 *   1. gate on /api/session so there is exactly one login
 *   2. show provisioning progress, because an empty library during setup is
 *      expected rather than broken
 *   3. search, and play the result in place - never linking out to a backend,
 *      which is the whole point of the project
 */
'use strict';

const $ = (id) => document.getElementById(id);

const state = {
  kind: '',
  query: '',
  searchSeq: 0,
  setupTimer: null,
  libraryEmpty: true,
};

/* ---------------------------------------------------------------- helpers */

async function api(path, options = {}) {
  const resp = await fetch(path, {
    credentials: 'same-origin',
    headers: options.body ? { 'Content-Type': 'application/json' } : {},
    ...options,
  });
  let body = null;
  try {
    body = await resp.json();
  } catch {
    // Streaming and error responses may not be JSON; callers check resp.ok.
  }
  return { ok: resp.ok, status: resp.status, body };
}

function show(el, visible) {
  el.classList.toggle('hidden', !visible);
}

function formatDuration(seconds) {
  if (!seconds || seconds <= 0) return '';
  const total = Math.round(seconds);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n) => String(n).padStart(2, '0');
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

function subtitleFor(item) {
  const parts = [];
  // An episode's series name and number matter more than anything else on the
  // card, so they win over the creator line rather than being dropped.
  if (item.kind === 'tv') {
    const episode = item.extra && item.extra.episode;
    if (item.subtitle && episode) parts.push(`${item.subtitle} · ${episode}`);
    else if (item.subtitle) parts.push(item.subtitle);
    else if (episode) parts.push(episode);
  } else if (item.creators && item.creators.length) parts.push(item.creators.join(', '));
  else if (item.subtitle) parts.push(item.subtitle);
  if (item.year) parts.push(item.year);
  return parts.join(' · ');
}

// Ids are escaped per path segment, not as a whole: an OPDS acquisition
// reference legitimately contains slashes, and the stream route matches them
// with a trailing wildcard.
const escapeId = (id) => String(id).split('/').map(encodeURIComponent).join('/');

const artPath = (item) =>
  item.artId ? `/api/art/${encodeURIComponent(item.sourceId)}/${escapeId(item.artId)}` : '';
const streamPath = (item) =>
  `/api/stream/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`;

/* ------------------------------------------------------------------- gate */

function showGate(hasAccount) {
  show($('boot'), false);
  show($('app'), false);
  show($('gate'), true);

  $('gate-blurb').textContent = hasAccount
    ? 'Sign in to your library.'
    : 'Create the account for this server. You will not need any API keys.';
  $('gate-submit').textContent = hasAccount ? 'Sign in' : 'Create account';
  $('gate-form').dataset.mode = hasAccount ? 'login' : 'signup';
  $('gate-username').focus();
}

$('gate-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const submit = $('gate-submit');
  const errorEl = $('gate-error');
  show(errorEl, false);
  submit.disabled = true;

  const mode = $('gate-form').dataset.mode;
  const original = submit.textContent;
  submit.textContent = mode === 'signup' ? 'Creating…' : 'Signing in…';

  const { ok, body } = await api(`/api/${mode}`, {
    method: 'POST',
    body: JSON.stringify({
      username: $('gate-username').value,
      password: $('gate-password').value,
    }),
  });

  submit.disabled = false;
  submit.textContent = original;

  if (!ok) {
    errorEl.textContent = (body && body.error) || 'Something went wrong.';
    show(errorEl, true);
    return;
  }
  $('gate-password').value = '';
  showApp();
});

$('logout').addEventListener('click', async () => {
  stopAudio();
  closeVideo();
  await api('/api/logout', { method: 'POST' });
  if (state.setupTimer) clearInterval(state.setupTimer);
  location.reload();
});

/* -------------------------------------------------------------- app shell */

function showApp() {
  show($('boot'), false);
  show($('gate'), false);
  show($('app'), true);
  $('search-input').focus();
  pollSetup();
  state.setupTimer = setInterval(pollSetup, 2000);
  loadLibrary();
}

/* ---------------------------------------------------------------- library */

// The folder guide. Visible whenever nothing is being searched, which means a
// brand new user's first screen explains where media goes instead of being an
// empty grid with a search box above it.
async function loadLibrary() {
  const { ok, body } = await api('/api/library');
  if (!ok || !body) return;

  state.libraryEmpty = body.empty;

  $('library-heading').textContent = body.empty
    ? 'Put your media in these folders'
    : 'Your library';
  $('library-blurb').textContent = body.empty
    ? 'soundstorm made these for you. Drop files in and they will show up here — nothing else to set up.'
    : 'Drop files into any of these and they appear in search automatically.';

  const list = $('library-folders');
  list.replaceChildren();

  for (const folder of body.folders || []) {
    const li = document.createElement('li');
    li.className = 'folder';

    const glyph = document.createElement('span');
    glyph.className = 'folder-glyph';
    glyph.textContent = GLYPHS[folder.kind] || '●';

    const text = document.createElement('div');
    text.className = 'folder-text';

    const path = document.createElement('code');
    path.textContent = folder.path;

    const desc = document.createElement('span');
    desc.className = 'folder-desc';
    desc.textContent = folder.description;

    const example = document.createElement('code');
    example.className = 'folder-example';
    example.textContent = folder.example;

    text.append(path, desc, example);

    const count = document.createElement('span');
    count.className = 'folder-count';
    count.textContent = folderCount(folder);

    li.append(glyph, text, count);
    list.append(li);
  }

  updateLibraryVisibility();
}

// The gap between files on disk and files indexed is the useful number: it
// tells "you have not added anything" apart from "a scan is still running".
function folderCount(folder) {
  if (!folder.files) return 'empty';
  const files = `${folder.files} file${folder.files === 1 ? '' : 's'}`;
  if (typeof folder.indexed === 'number' && folder.indexed < folder.files) {
    return `${files} · indexing…`;
  }
  return files;
}

function updateLibraryVisibility() {
  show($('library'), !state.query);
}

async function pollSetup() {
  const { ok, body } = await api('/api/setup');
  if (!ok || !body) return;

  const list = $('setup-list');
  list.replaceChildren();

  for (const backend of body.backends || []) {
    const li = document.createElement('li');

    const dot = document.createElement('span');
    dot.className = 'dot';
    if (backend.status === 'ready') dot.classList.add('ready');
    else if (backend.status === 'failed') dot.classList.add('failed');
    else dot.classList.add('working');

    const name = document.createElement('span');
    name.className = 'setup-name';
    name.textContent = backend.id;

    const detail = document.createElement('span');
    detail.className = 'setup-detail';
    detail.textContent =
      backend.status === 'ready'
        ? 'ready'
        : backend.error || backend.detail || backend.status;

    li.append(dot, name, detail);
    list.append(li);
  }

  show($('setup'), !body.allReady);
  if (body.allReady && state.setupTimer) {
    clearInterval(state.setupTimer);
    state.setupTimer = null;
    // Backends that finished after the last search would otherwise be missing
    // from results until the user typed again.
    if (state.query) runSearch();
    loadLibrary();
  }
}

/* ----------------------------------------------------------------- search */

$('search-form').addEventListener('submit', (event) => {
  event.preventDefault();
  runSearch();
});

let debounce = null;
$('search-input').addEventListener('input', () => {
  clearTimeout(debounce);
  debounce = setTimeout(runSearch, 280);
});

for (const chip of document.querySelectorAll('.chip')) {
  chip.addEventListener('click', () => {
    for (const other of document.querySelectorAll('.chip')) {
      other.classList.toggle('active', other === chip);
    }
    state.kind = chip.dataset.kind;
    runSearch();
  });
}

async function runSearch() {
  const query = $('search-input').value.trim();
  state.query = query;

  updateLibraryVisibility();

  if (!query) {
    $('results').replaceChildren();
    $('status').textContent = '';
    show($('degraded'), false);
    // Counts may have moved while the user was searching.
    loadLibrary();
    return;
  }

  // Keep only the newest response: debounced typing means several can be in
  // flight and they do not necessarily come back in order.
  const seq = ++state.searchSeq;
  $('status').textContent = 'Searching…';

  const params = new URLSearchParams({ q: query });
  if (state.kind) params.set('kind', state.kind);

  const { ok, body } = await api(`/api/search?${params}`);
  if (seq !== state.searchSeq) return;

  if (!ok || !body) {
    $('status').textContent = 'Search failed.';
    return;
  }
  renderResults(body);
}

function renderResults(result) {
  const grid = $('results');
  grid.replaceChildren();

  for (const item of result.items) {
    grid.append(renderItem(item));
  }

  const failed = (result.sources || []).filter((s) => !s.ok);
  if (failed.length) {
    $('degraded').textContent =
      `Some libraries did not answer: ${failed.map((s) => `${s.sourceId} (${s.error})`).join('; ')}`;
  }
  show($('degraded'), failed.length > 0);

  const n = result.items.length;
  if (n) {
    $('status').textContent = `${n} result${n === 1 ? '' : 's'} in ${result.tookMs} ms`;
  } else if (state.libraryEmpty) {
    // "Nothing matched" is a lie when there is nothing to match against.
    $('status').textContent = 'Nothing to search yet — your library is empty.';
    show($('library'), true);
  } else {
    $('status').textContent = 'Nothing matched.';
  }
}

function renderItem(item) {
  const card = document.createElement('button');
  card.className = 'item';
  card.type = 'button';

  const wrap = document.createElement('div');
  wrap.className = 'art-wrap';

  const art = artPath(item);
  if (art) {
    const img = document.createElement('img');
    img.src = art;
    img.alt = '';
    img.loading = 'lazy';
    // A backend can have an artwork id but no actual file; fall back rather
    // than showing a broken image.
    img.addEventListener('error', () => img.replaceWith(fallbackArt(item)));
    wrap.append(img);
  } else {
    wrap.append(fallbackArt(item));
  }

  const badge = document.createElement('span');
  badge.className = 'badge';
  badge.textContent = item.kind;
  wrap.append(badge);

  const duration = formatDuration(item.durationSeconds);
  if (duration) {
    const el = document.createElement('span');
    el.className = 'duration';
    el.textContent = duration;
    wrap.append(el);
  }

  const meta = document.createElement('div');
  meta.className = 'meta';

  const title = document.createElement('span');
  title.className = 'title';
  title.textContent = item.title;
  title.title = item.title;

  const sub = document.createElement('span');
  sub.className = 'sub';
  sub.textContent = subtitleFor(item);

  meta.append(title, sub);
  card.append(wrap, meta);
  card.addEventListener('click', () => play(item));
  return card;
}

const GLYPHS = {
  video: '▶',
  tv: '📺',
  music: '♪',
  audiobook: '🎧',
  ebook: '📖',
};

function fallbackArt(item) {
  const span = document.createElement('span');
  span.className = 'art-fallback';
  span.textContent = GLYPHS[item.kind] || '●';
  return span;
}

/* ---------------------------------------------------------------- players */

function play(item) {
  switch (item.kind) {
    case 'video':
      playVideo(item);
      break;
    case 'ebook':
      readBook(item);
      break;
    default:
      // Music and audiobooks are both just audio as far as a browser cares.
      playAudio(item);
  }
}

// Ebooks open in SoundStorm's own reader. Downloading is still offered, but as a
// choice rather than the only option - a result that leaves SoundStorm is a seam,
// and this was the last one.
function readBook(item) {
  stopAudio();
  closeVideo();

  if (window.soundstormReader) {
    window.soundstormReader.open(item);
    return;
  }
  // The reader is a module; if it failed to load, handing over the file still
  // beats doing nothing.
  downloadBook(item);
}

function downloadBook(item) {
  const format = (item.extra && item.extra.format) || 'epub';
  const author = (item.creators && item.creators[0]) || '';

  const link = document.createElement('a');
  link.href = streamPath(item);
  // Name the file after the book. Without this the browser derives a name from
  // the URL, and every download is called "download.epub".
  link.download = [item.title, author].filter(Boolean).join(' - ').replace(/[\/:*?"<>|]/g, '_') + '.' + format;
  link.rel = 'noopener';
  document.body.append(link);
  link.click();
  link.remove();

  $('status').textContent = 'Downloading “' + item.title + '” as ' + format + '…';
}

// The reader's download button needs the item it is showing.
window.soundstormDownloadBook = downloadBook;

function playVideo(item) {
  stopAudio();
  const player = $('video-player');
  player.src = streamPath(item);
  $('video-caption').textContent = [item.title, subtitleFor(item)]
    .filter(Boolean)
    .join(' — ');
  show($('video-overlay'), true);
  player.play().catch(() => {
    /* The browser may require a gesture; the controls are right there. */
  });
}

function closeVideo() {
  const player = $('video-player');
  player.pause();
  player.removeAttribute('src');
  player.load();
  show($('video-overlay'), false);
}

$('video-close').addEventListener('click', closeVideo);
$('video-overlay').addEventListener('click', (event) => {
  if (event.target === $('video-overlay')) closeVideo();
});
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && !$('video-overlay').classList.contains('hidden')) {
    closeVideo();
  }
});

function playAudio(item) {
  closeVideo();
  const player = $('audio-player');
  player.src = streamPath(item);
  $('audio-title').textContent = item.title;
  $('audio-sub').textContent = subtitleFor(item);

  const art = $('audio-art');
  const src = artPath(item);
  if (src) {
    art.src = src;
    show(art, true);
  } else {
    art.removeAttribute('src');
    show(art, false);
  }

  show($('audio-dock'), true);
  player.play().catch(() => {});
}

function stopAudio() {
  const player = $('audio-player');
  player.pause();
  player.removeAttribute('src');
  player.load();
  show($('audio-dock'), false);
}

$('audio-close').addEventListener('click', stopAudio);

/* ------------------------------------------------------------------- boot */

(async function boot() {
  const { ok, body } = await api('/api/session');
  if (!ok || !body) {
    $('boot').textContent = 'soundstorm is not responding.';
    return;
  }
  if (body.signedIn) showApp();
  else showGate(body.hasAccount);
})();
