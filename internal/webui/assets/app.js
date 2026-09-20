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
  me: null, // the signed-in account, from /api/session
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
  showApp(body && body.user);
});

$('logout').addEventListener('click', async () => {
  show($('account'), false);
  stopAudio();
  closeVideo();
  await api('/api/logout', { method: 'POST' });
  if (state.setupTimer) clearInterval(state.setupTimer);
  location.reload();
});

/* -------------------------------------------------------------- app shell */

function showApp(me) {
  state.me = me || null;
  show($('boot'), false);
  show($('gate'), false);
  show($('app'), true);
  $('search-input').focus();
  applyLibraryTabs();
  renderAccount();
  pollSetup();
  state.setupTimer = setInterval(pollSetup, 2000);
  loadLibrary();
}

/* --------------------------------------------------------------- accounts */

function renderAccount() {
  const me = state.me;
  if (!me) return;

  $('account-who').textContent = me.owner
    ? `Signed in as ${me.name}. You set this server up, so you can add and remove people.`
    : `Signed in as ${me.name}.`;

  // Hiding the controls is presentation, not permission - the server refuses
  // these calls for a member whether or not the form is on screen.
  show($('people-block'), Boolean(me.owner));
  if (me.owner) loadPeople();
}

$('account-toggle').addEventListener('click', () => {
  const opening = $('account').classList.contains('hidden');
  show($('account'), opening);
  $('account-toggle').setAttribute('aria-expanded', String(opening));
  if (opening) renderAccount();
});

function note(el, message, isError) {
  el.textContent = message;
  el.classList.toggle('error', Boolean(isError));
  el.classList.toggle('muted', !isError);
  show(el, true);
}

$('password-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const field = $('new-password');
  const { ok, body } = await api('/api/account/password', {
    method: 'POST',
    body: JSON.stringify({ password: field.value }),
  });
  if (!ok) {
    note($('password-note'), (body && body.error) || 'Could not change it.', true);
    return;
  }
  field.value = '';
  note($('password-note'), 'Changed. Your other devices stay signed in.', false);
});

async function loadPeople() {
  const { ok, body } = await api('/api/users');
  if (!ok || !body) return;

  const list = $('people-list');
  list.replaceChildren();

  for (const person of body.users || []) {
    const li = document.createElement('li');

    const name = document.createElement('span');
    name.className = 'person-name';
    name.textContent = person.name;

    const role = document.createElement('span');
    role.className = 'person-role';
    role.textContent = person.owner ? 'owner' : 'member';

    const spacer = document.createElement('span');
    spacer.className = 'person-spacer';

    li.append(name, role);
    // The owner always sees everything and cannot be narrowed, so there is
    // nothing to offer - a row of ticked boxes that refuse to be unticked
    // would be worse than none.
    if (person.owner) {
      const everything = document.createElement('span');
      everything.className = 'person-everything muted';
      everything.textContent = 'every library';
      li.append(everything);
    } else {
      li.append(libraryPicker(person));
    }
    li.append(spacer);

    // The owner is not removable and neither are you: the server refuses both,
    // and offering a button that always fails is worse than offering none.
    if (!person.owner && person.id !== (state.me && state.me.id)) {
      const remove = document.createElement('button');
      remove.className = 'ghost';
      remove.type = 'button';
      remove.textContent = 'Remove';
      remove.addEventListener('click', () => removePerson(person));
      li.append(remove);
    }
    list.append(li);
  }
}

// libraryPicker is the five shelves, ticked for the ones this person can see.
function libraryPicker(person) {
  const wrap = document.createElement('div');
  wrap.className = 'person-libraries';

  for (const [kind, label] of LIBRARY_LABELS) {
    const field = document.createElement('label');
    const box = document.createElement('input');
    box.type = 'checkbox';
    box.checked = person.libraries.includes(kind);
    field.classList.toggle('on', box.checked);

    const text = document.createElement('span');
    text.textContent = label;

    box.addEventListener('change', () => {
      const chosen = [...wrap.querySelectorAll('input')]
        .filter((b) => b.checked)
        .map((b) => b.dataset.kind);
      saveLibraries(person, chosen, wrap);
    });

    box.dataset.kind = kind;
    field.append(box, text);
    wrap.append(field);
  }
  return wrap;
}

const LIBRARY_LABELS = [
  ['music', 'Music'],
  ['video', 'Films'],
  ['tv', 'TV'],
  ['audiobook', 'Audiobooks'],
  ['ebook', 'Ebooks'],
];

async function saveLibraries(person, chosen, wrap) {
  // Sending every kind and sending "everything" are different in the state
  // file, and only the second keeps up if a sixth library is ever added.
  const payload = chosen.length === LIBRARY_LABELS.length ? null : chosen;

  for (const box of wrap.querySelectorAll('input')) box.disabled = true;
  const { ok, body } = await api(`/api/users/${encodeURIComponent(person.id)}/libraries`, {
    method: 'PUT',
    body: JSON.stringify({ libraries: payload }),
  });
  for (const box of wrap.querySelectorAll('input')) box.disabled = false;

  if (!ok) {
    note($('people-note'), (body && body.error) || 'Could not change that.', true);
    loadPeople();
    return;
  }
  note($('people-note'),
    chosen.length === 0
      ? `${person.name} can no longer see any library.`
      : `${person.name} can see ${chosen.length} of ${LIBRARY_LABELS.length} libraries.`,
    false);
  loadPeople();
}

async function removePerson(person) {
  // Removing somebody deletes their place in every book and the account they
  // were given on the audiobook server, so it is worth one question.
  if (!window.confirm(
    `Remove ${person.name}? Their logins stop working immediately and their `
    + 'reading and listening positions are deleted. Your media is untouched.')) {
    return;
  }
  const { ok, body } = await api(`/api/users/${encodeURIComponent(person.id)}`,
    { method: 'DELETE' });
  if (!ok) {
    note($('people-note'), (body && body.error) || 'Could not remove them.', true);
    return;
  }
  note($('people-note'), `${person.name} was removed.`, false);
  loadPeople();
}

$('add-person-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const name = $('new-username');
  const password = $('new-person-password');

  const { ok, body } = await api('/api/users', {
    method: 'POST',
    body: JSON.stringify({ username: name.value, password: password.value }),
  });
  if (!ok) {
    note($('people-note'), (body && body.error) || 'Could not add them.', true);
    return;
  }
  const added = body && body.user ? body.user.name : name.value;
  name.value = '';
  password.value = '';
  note($('people-note'),
    `${added} can sign in now. Tell them the password you just chose - they can change it themselves.`,
    false);
  loadPeople();
});

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

// Hide the tabs for libraries this account cannot see. The server refuses them
// either way - this is so a child account is not looking at a Films tab that
// returns nothing and wondering what it did wrong.
function applyLibraryTabs() {
  const allowed = (state.me && state.me.libraries) || [];
  const everything = !state.me || state.me.allLibraries;

  for (const chip of document.querySelectorAll('.chip')) {
    const kind = chip.dataset.kind;
    const visible = everything || kind === '' || allowed.includes(kind);
    show(chip, visible);
    // If the active filter just became invisible, fall back to everything
    // rather than leaving a search pinned to a library that is not there.
    if (!visible && state.kind === kind) {
      state.kind = '';
      for (const other of document.querySelectorAll('.chip')) {
        other.classList.toggle('active', other.dataset.kind === '');
      }
      if (state.query) runSearch();
    }
  }
}

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

// hls.js is 620KB, so it is fetched the first time a video actually needs a
// transcoded stream and never for music, books, or films that play directly.
let hlsLoader = null;

function loadHls() {
  if (window.Hls) return Promise.resolve(window.Hls);
  if (!hlsLoader) {
    hlsLoader = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = '/static/vendor/hls.js/hls.min.js';
      script.onload = () => resolve(window.Hls);
      script.onerror = () => { hlsLoader = null; reject(new Error('could not load the video player')); };
      document.head.append(script);
    });
  }
  return hlsLoader;
}

let hls = null;

function detachHls() {
  if (hls) {
    hls.destroy();
    hls = null;
  }
}

async function playVideo(item) {
  stopAudio();
  detachHls();

  const player = $('video-player');
  $('video-caption').textContent = [item.title, subtitleFor(item)]
    .filter(Boolean)
    .join(' — ');
  show($('video-overlay'), true);

  // Ask before building a player: the answer decides which one to build.
  const { ok, body } = await api(
    `/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`);
  const mode = ok && body ? body.mode : 'direct';
  const url = ok && body && body.url ? body.url : streamPath(item);

  attachSubtitles(player, (ok && body && body.subtitles) || []);

  if (mode !== 'hls') {
    player.src = url;
    player.play().catch(() => {});
    return;
  }

  // Safari plays HLS natively and does it better than any library can.
  if (player.canPlayType('application/vnd.apple.mpegurl')) {
    player.src = url;
    player.play().catch(() => {});
    return;
  }

  try {
    const Hls = await loadHls();
    if (!Hls || !Hls.isSupported()) throw new Error('this browser cannot play transcoded video');

    hls = new Hls({ enableWorker: true });
    hls.on(Hls.Events.ERROR, (_event, data) => {
      // Only fatal errors are worth surfacing; hls.js recovers from the rest
      // by itself and says so loudly in the console either way.
      if (data && data.fatal) {
        $('video-caption').textContent = `Could not play this video: ${data.details || 'stream error'}`;
        detachHls();
      }
    });
    hls.loadSource(url);
    hls.attachMedia(player);
    hls.on(Hls.Events.MANIFEST_PARSED, () => player.play().catch(() => {}));
  } catch (err) {
    $('video-caption').textContent = err.message;
  }
}

// Subtitles are attached as track elements, which works the same whether the
// video is a plain file or an HLS stream: text tracks are independent of how
// the media itself arrives.
function attachSubtitles(player, tracks) {
  for (const existing of [...player.querySelectorAll('track')]) existing.remove();

  const picker = $('subtitle-picker');
  const select = $('subtitle-select');
  select.replaceChildren();

  if (!tracks.length) {
    show(picker, false);
    return;
  }

  const off = document.createElement('option');
  off.value = '';
  off.textContent = 'Off';
  select.append(off);

  tracks.forEach((track, index) => {
    const el = document.createElement('track');
    el.kind = 'subtitles';
    el.src = track.url;
    el.label = track.label || `Track ${index + 1}`;
    if (track.language) el.srclang = track.language;
    player.append(el);

    const option = document.createElement('option');
    option.value = String(index);
    option.textContent = el.label;
    select.append(option);
  });

  // Default to off. Turning subtitles on for someone who did not ask is more
  // annoying than leaving them a control.
  select.value = '';
  applySubtitleChoice(player, '');
  show(picker, true);
}

function applySubtitleChoice(player, value) {
  const wanted = value === '' ? -1 : Number(value);
  // textTracks is a live list in the same order the track elements were added.
  for (let i = 0; i < player.textTracks.length; i++) {
    player.textTracks[i].mode = i === wanted ? 'showing' : 'disabled';
  }
}

$('subtitle-select').addEventListener('change', (event) => {
  applySubtitleChoice($('video-player'), event.target.value);
});

function closeVideo() {
  detachHls();
  const player = $('video-player');
  player.pause();
  player.removeAttribute('src');
  for (const track of [...player.querySelectorAll('track')]) track.remove();
  player.load();
  show($('subtitle-picker'), false);
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

/* Audio, the chapters an audiobook is made of, and where you left off.
 *
 * A LibriVox book is one MP3 per chapter - thirty of them for a volume of
 * Aesop - and a browser handed the first file plays it and goes quiet. So the
 * dock keeps a track list, advances through it, and offers it as a panel.
 *
 * Position is measured across the whole book rather than within a file,
 * because that is the timeline the backend stores and its own apps read. A
 * track's startSeconds converts between the two.
 */
const audio = {
  item: null,       // what is playing, and the guard for stale responses
  tracks: [],       // empty for anything that is a single file
  index: 0,
  resumable: false, // whether the backend will remember a position for this
  duration: 0,      // the whole item's length, across every file
  savedAt: 0,       // when a position was last sent
  started: false,
};

// How often a position is sent while playing. Every timeupdate would be four
// requests a second per listener; re-hearing a few seconds after a hard kill
// is not worth that.
const SAVE_EVERY_MS = 10000;

function playAudio(item) {
  closeVideo();
  // Whatever was playing is being abandoned; record where it got to before
  // the state that describes it is overwritten.
  savePosition();

  audio.item = item;
  audio.tracks = [];
  audio.index = 0;
  audio.resumable = false;
  audio.duration = item.durationSeconds || 0;
  audio.savedAt = 0;
  audio.started = false;

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

  renderTracks();
  show($('audio-dock'), true);

  // A song starts now: a round trip before the first note is felt, and nothing
  // about a four minute track needs the answer. An audiobook waits, because it
  // may be resuming into chapter twelve, and starting chapter one first would
  // play a second of the wrong thing before correcting itself.
  if (item.kind !== 'audiobook') startAt(streamPath(item), 0);

  loadPlayback(item);
}

async function loadPlayback(item) {
  const { ok, body } = await api(
    `/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`);

  // Something else was started while this was in flight.
  if (audio.item !== item) return;

  const info = (ok && body) || {};

  // Sent only for genuinely multi-file items, so anything else needs nothing.
  if (Array.isArray(info.tracks) && info.tracks.length > 1) {
    audio.tracks = info.tracks;
    const last = info.tracks[info.tracks.length - 1];
    audio.duration = (last.startSeconds || 0) + (last.durationSeconds || 0);
    renderTracks();
  }

  // The key being present is the capability: this source will remember a
  // position, whether or not one has been recorded yet.
  audio.resumable = Boolean(info.position);
  if (info.position && info.position.duration > 0) audio.duration = info.position.duration;

  if (!audio.started) {
    // A book somebody finished starts again from the beginning rather than
    // from its last second.
    const resume = audio.resumable && !info.position.finished ? info.position.seconds : 0;
    seekTo(resume, item);
  }
}

// seekTo starts playing at a position on the whole item's timeline, picking
// whichever file contains it.
function seekTo(seconds, item) {
  if (audio.tracks.length > 1) {
    const index = trackContaining(seconds);
    audio.index = index;
    renderTracks();
    startAt(audio.tracks[index].url, seconds - audio.tracks[index].startSeconds);
    return;
  }
  startAt(streamPath(item), seconds);
}

function trackContaining(seconds) {
  for (let i = audio.tracks.length - 1; i >= 0; i--) {
    if (seconds >= audio.tracks[i].startSeconds) return i;
  }
  return 0;
}

// startAt loads a url and begins at an offset into it.
//
// currentTime cannot be set before the browser knows how long the file is, and
// a media element silently ignores the assignment rather than queueing it.
function startAt(url, offset) {
  const player = $('audio-player');
  audio.started = true;
  // Start the clock now, or the first timeupdate is already older than the
  // interval and every play begins by writing back the position it just read.
  audio.savedAt = Date.now();
  player.src = url;

  const begin = () => player.play().catch(() => {});
  if (offset > 0) {
    player.addEventListener('loadedmetadata', () => {
      // Landing exactly on the end would fire 'ended' and skip the chapter.
      player.currentTime = Math.min(offset, Math.max(0, player.duration - 1));
      begin();
    }, { once: true });
  } else {
    begin();
  }
}

function renderTracks() {
  const list = $('audio-tracks');
  const toggle = $('audio-tracks-toggle');
  list.replaceChildren();

  if (audio.tracks.length < 2) {
    show(list, false);
    show(toggle, false);
    toggle.setAttribute('aria-expanded', 'false');
    return;
  }

  audio.tracks.forEach((track, index) => {
    const li = document.createElement('li');
    li.classList.toggle('current', index === audio.index);
    if (index === audio.index) li.setAttribute('aria-current', 'true');

    const button = document.createElement('button');
    button.type = 'button';

    const num = document.createElement('span');
    num.className = 'track-num';
    num.textContent = index + 1;

    const name = document.createElement('span');
    name.className = 'track-name';
    name.textContent = track.title || `Part ${index + 1}`;
    name.title = name.textContent;

    const time = document.createElement('span');
    time.className = 'track-time';
    time.textContent = formatDuration(track.durationSeconds);

    button.append(num, name, time);
    button.addEventListener('click', () => {
      selectTrack(index);
      showTrackList(false);
    });

    li.append(button);
    list.append(li);
  });

  show(toggle, true);
  updateTrackCaption();
}

function selectTrack(index, offset = 0) {
  const track = audio.tracks[index];
  if (!track) return;

  // Save where we were before leaving it, or skipping ahead loses the place.
  if (audio.started) savePosition();

  audio.index = index;
  startAt(track.url, offset);
  renderTracks();
}

// The dock's second line becomes the chapter, because "13 of 30" is the thing
// somebody actually wants to know mid-book. The item's own subtitle is already
// above it in the title line.
function updateTrackCaption() {
  const track = audio.tracks[audio.index];
  if (!track) return;
  $('audio-sub').textContent =
    `${audio.index + 1} of ${audio.tracks.length} · ${track.title}`;
}

function showTrackList(visible) {
  show($('audio-tracks'), visible);
  $('audio-tracks-toggle').setAttribute('aria-expanded', String(visible));
  if (visible) {
    const current = $('audio-tracks').querySelector('.current');
    if (current) current.scrollIntoView({ block: 'nearest' });
  }
}

$('audio-tracks-toggle').addEventListener('click', () => {
  showTrackList($('audio-tracks').classList.contains('hidden'));
});

/* --------------------------------------------------------------- position */

// elapsed is where we are on the whole item's timeline, which is what the
// backend stores. Inside a file the player's own clock is the answer.
function elapsed() {
  const player = $('audio-player');
  const offset = audio.tracks.length > 1 ? (audio.tracks[audio.index].startSeconds || 0) : 0;
  return offset + (player.currentTime || 0);
}

// savePosition sends where we are, if there is anywhere to send it.
//
// keepalive, so the last save survives the page being closed - which is the
// most important one of the session and exactly the one a normal fetch drops.
function savePosition(options = {}) {
  if (!audio.resumable || !audio.item || !audio.started) return;

  const seconds = elapsed();
  if (!Number.isFinite(seconds)) return;

  audio.savedAt = Date.now();
  const item = audio.item;
  fetch(`/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`, {
    method: 'PUT',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      seconds,
      duration: Number.isFinite(audio.duration) ? audio.duration : 0,
      finished: Boolean(options.finished),
    }),
    keepalive: true,
  }).catch(() => {
    // Losing a position is not worth interrupting somebody's book over.
  });
}

$('audio-player').addEventListener('timeupdate', () => {
  if (Date.now() - audio.savedAt >= SAVE_EVERY_MS) savePosition();
});

// Pausing is the clearest "I am stopping here" a player ever gets.
$('audio-player').addEventListener('pause', () => savePosition());

// The whole point of a track list: chapter 1 ending means chapter 2 starting,
// not silence.
$('audio-player').addEventListener('ended', () => {
  if (audio.index + 1 < audio.tracks.length) {
    selectTrack(audio.index + 1);
    return;
  }
  // The end of the last file is the end of the book.
  savePosition({ finished: true });
});

// pagehide rather than unload: it is the one that fires on a phone when the
// browser is backgrounded, which is how an audiobook session usually ends.
window.addEventListener('pagehide', () => savePosition());

function stopAudio() {
  savePosition();

  // Clear the state before touching the player, not after. load() resets
  // currentTime to zero and pause() fires an event that saves again - so the
  // position just recorded would be overwritten with the start of the file.
  audio.item = null;
  audio.tracks = [];
  audio.index = 0;
  audio.resumable = false;
  audio.started = false;

  const player = $('audio-player');
  player.pause();
  player.removeAttribute('src');
  player.load();
  showTrackList(false);
  renderTracks();
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
  if (body.signedIn) showApp(body.user);
  else showGate(body.hasAccount);
})();
