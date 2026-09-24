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

// One page. Small enough that the first screenful arrives quickly, large
// enough that a fast scroll does not outrun it.
const PAGE_SIZE = 50;

const state = {
  kind: '',
  query: '',
  searchSeq: 0,
  offset: 0,        // how many items are already on screen
  hasMore: false,   // whether the server says another page exists
  loadingMore: false,
  setupTimer: null,
  libraryEmpty: true,
  me: null, // the signed-in account, from /api/session
  items: [], // what is on screen, in order - the photo viewer steps through it
};

/* ---------------------------------------------------------------- helpers */

async function api(path, options = {}) {
  let resp;
  try {
    resp = await fetch(path, {
      credentials: 'same-origin',
      headers: options.body ? { 'Content-Type': 'application/json' } : {},
      ...options,
    });
  } catch (err) {
    // fetch rejects rather than resolving when the request never completes at
    // all: the server is down, the wifi dropped, or - the common one here -
    // the browser refused the certificate. Callers only ever checked `ok`, so
    // this used to reject straight through them and leave the boot spinner
    // turning for ever with "Failed to fetch" in a console nobody opens.
    return { ok: false, status: 0, body: null, offline: true };
  }
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

// The setup code arrives in the address the installer opened, and is taken
// out of it straight away so it is not left in the history or a bookmark.
// It survives the move to the https name because that keeps the query.
const setupFromAddress = (() => {
  const params = new URLSearchParams(location.search);
  const code = params.get('setup');
  if (code === null) return '';
  params.delete('setup');
  const rest = params.toString();
  history.replaceState(null, '', location.pathname + (rest ? `?${rest}` : '') + location.hash);
  return code;
})();

function showGate(hasAccount, setupCodeRequired) {
  show($('boot'), false);
  show($('app'), false);
  show($('gate'), true);

  $('gate-blurb').textContent = hasAccount
    ? 'Sign in to your library.'
    : 'Create the account for this server. You will not need any API keys.';
  $('gate-submit').textContent = hasAccount ? 'Sign in' : 'Create account';
  $('gate-form').dataset.mode = hasAccount ? 'login' : 'signup';
  // Asked for only when the address did not carry it, which is the unusual
  // case: somebody opened the page by hand instead of from setup.
  $('gate-setup-code').value = setupFromAddress;
  show($('gate-setup'), !hasAccount && setupCodeRequired && !setupFromAddress);
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
      ...(mode === 'signup' ? { setupCode: $('gate-setup-code').value } : {}),
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

async function showApp(me) {
  state.me = me || null;
  show($('boot'), false);
  show($('gate'), false);
  show($('app'), true);
  $('search-input').focus();
  applyLibraryTabs();
  renderAccount();
  pollSetup();
  state.setupTimer = setInterval(pollSetup, 2000);
  // Awaited before the first browse so that libraryEmpty is known by the time
  // anything decides what to show. Without it a fresh signed-in load flashes
  // the drop card for one frame before the grid replaces it.
  await loadLibrary();
  runSearch();
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
  if (me.owner) {
    loadPeople();
    refreshRemote();
  }
}

// refreshRemote reads the current remote-access status from the session and
// paints the owner's toggle. The status lives on /api/session rather than in
// state.me, and it can change (the owner toggles it, or auto HTTPS comes up
// after sign-in), so it is fetched fresh each time the panel opens rather than
// cached at boot. The block stays hidden unless remote access can be offered at
// all - without a real certificate there is nothing to turn on.
async function refreshRemote() {
  const { ok, body } = await api('/api/session');
  const remote = ok && body && body.remote;
  show($('remote-block'), Boolean(remote && remote.available));
  if (!remote || !remote.available) return;
  renderRemote(remote);
}

function renderRemote(remote) {
  $('remote-toggle').checked = Boolean(remote.enabled);
  const share = $('remote-share');
  const url = $('remote-share-url');
  if (remote.enabled && remote.name) {
    const href = `https://${remote.name}`;
    url.textContent = href;
    url.href = href;
    show(share, true);
  } else {
    show(share, false);
  }
}

$('remote-toggle').addEventListener('change', async (event) => {
  const enabled = event.target.checked;
  const { ok, body } = await api('/api/remote', {
    method: 'PUT',
    body: JSON.stringify({ enabled }),
  });
  if (!ok) {
    // Put the switch back where it was - the server did not accept the change.
    event.target.checked = !enabled;
    note($('remote-note'), (body && body.error) || 'Could not change it.', true);
    return;
  }
  renderRemote(body);
  note(
    $('remote-note'),
    enabled
      ? 'On. It can take a minute for the address to work the first time while the certificate is issued.'
      : 'Off. Your server is only reachable on your home network again.',
    false,
  );
});

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
  const current = $('current-password');
  const field = $('new-password');
  const { ok, body } = await api('/api/account/password', {
    method: 'POST',
    body: JSON.stringify({ current: current.value, password: field.value }),
  });
  if (!ok) {
    note($('password-note'), (body && body.error) || 'Could not change it.', true);
    return;
  }
  current.value = '';
  field.value = '';
  note($('password-note'), 'Changed. Your other devices will need to sign in again.', false);
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
  ['document', 'Documents'],
  ['picture', 'Pictures'],
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

  // The one signal worth keeping from the per-kind counts the card used to
  // show: files on disk that no backend has indexed yet. It tells "nothing
  // has been added" apart from "a scan is still running", which are the two
  // reasons a search can come back empty and look broken.
  const folders = body.folders || [];
  const indexing = folders.some(
    (f) => typeof f.indexed === 'number' && f.indexed < f.files);
  show($('library-indexing'), indexing);

  // Two places, one answer: the hint line, and the account panel.
  for (const [row, anchor] of [
    ['library-share', 'library-share-url'],
    ['account-share', 'account-share-url'],
  ]) {
    const link = $(anchor);
    if (body.shareURL) {
      link.textContent = body.shareURL;
      link.href = body.shareURL;
    }
    show($(row), Boolean(body.shareURL));
  }
}



// The scan button. Dropping files on the window already triggers this, so it
// is here for media that arrived some other way - copied in from a file
// manager, or synced from another machine.
$('rescan').addEventListener('click', async () => {
  const button = $('rescan');
  const note = $('rescan-note');

  button.disabled = true;
  const original = button.textContent;
  button.textContent = 'Checking…';

  const { ok, body } = await api('/api/library/rescan', { method: 'POST' });

  button.disabled = false;
  button.textContent = original;

  if (!ok) {
    note.textContent = (body && body.error) || 'Could not start a scan.';
    note.classList.add('error');
    show(note, true);
    return;
  }
  note.classList.remove('error');
  note.textContent =
    'Looking for new files. Anything found appears in search within a minute.';
  show(note, true);

  // The counts move as each backend gets through it, so look again shortly.
  setTimeout(loadLibrary, 4000);
  setTimeout(loadLibrary, 15000);
});

// Choose files, for every device that cannot drag one.
//
// A phone has no drag and drop at all, so on the screen whose entire job is
// to ask for media, "drag it here" is an instruction half the devices cannot
// follow. This goes through exactly the same intake as a drop: collectFiles
// already falls back to a flat file list when a DataTransfer carries no
// directory entries, which is precisely the shape a file input gives.
// A phone cannot drag anything, so telling one to is an instruction it cannot
// follow - and it is the first line of the app. Asked of the pointer rather
// than the width, because a narrow desktop window still has a mouse.
if (window.matchMedia && window.matchMedia('(pointer: coarse)').matches) {
  $('hint-add').textContent = 'Add music, films, books, documents or photos:';
}

$('choose-files').addEventListener('click', () => $('file-picker').click());

$('file-picker').addEventListener('change', (event) => {
  const files = [...(event.target.files || [])];
  if (!files.length) return;
  // Reset first: picking the same file twice in a row fires no change event
  // otherwise, which reads as the button having stopped working.
  event.target.value = '';
  intake({ items: [], files });
});

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
    runSearch();
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
      runSearch();
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

// runSearch also runs with nothing typed, and that is the point.
//
// An empty box is a request to see the shelf, not a request for nothing:
// picking Audiobooks lists every audiobook, and typing narrows from there.
// Every adapter turns an empty query into its backend's own listing call, and
// the merged list comes back alphabetical because relevance scores everything
// 0 when there is no question for it to be relevant to.
async function runSearch() {
  const query = $('search-input').value.trim();
  state.query = query;

  if (!query) {
    // Counts may have moved while the user was searching, and the shelf about
    // to be listed is the thing they describe.
    loadLibrary();
  }

  // Keep only the newest response: debounced typing means several can be in
  // flight and they do not necessarily come back in order.
  const seq = ++state.searchSeq;
  $('status').textContent = query ? 'Searching…' : 'Loading…';

  // Back to the top of the list. Anything already on screen belongs to the
  // previous query and must not be appended to.
  state.offset = 0;
  state.hasMore = false;

  const params = new URLSearchParams({ q: query, limit: String(PAGE_SIZE) });
  if (state.kind) params.set('kind', state.kind);

  const { ok, body } = await api(`/api/search?${params}`);
  if (seq !== state.searchSeq) return;

  if (!ok || !body) {
    $('status').textContent = 'Search failed.';
    return;
  }
  renderResults(body);
}

function renderResults(result, append) {
  const grid = $('results');
  if (!append) grid.replaceChildren();

  for (const item of result.items) {
    grid.append(renderItem(item));
  }
  state.items = append ? state.items.concat(result.items) : result.items.slice();

  // Trust the server's own count of where this page ended rather than adding
  // up what arrived: a page clipped at the depth cap would otherwise leave the
  // next request asking from the wrong place.
  state.offset = (result.offset || 0) + result.items.length;
  state.hasMore = Boolean(result.hasMore);

  const failed = (result.sources || []).filter((s) => !s.ok);
  if (failed.length) {
    $('degraded').textContent =
      `Some libraries did not answer: ${failed.map((s) => `${s.sourceId} (${s.error})`).join('; ')}`;
  }
  show($('degraded'), failed.length > 0);

  // What is on screen, not what this page brought, or the count resets to 50
  // on every scroll.
  const shown = grid.childElementCount;
  const browsing = !state.query;
  if (shown) {
    // A browse is a list, not an answer: "50 results" for a shelf nobody
    // asked a question of reads like a search that went wrong.
    // The ellipsis goes after the noun, not inside the number: "100 items…"
    // rather than "100… items", which reads like a broken number.
    const more = state.hasMore ? '…' : '';
    $('status').textContent = browsing
      ? `${shown} item${shown === 1 ? '' : 's'}${more}`
      : `${shown} result${shown === 1 ? '' : 's'}${more} in ${result.tookMs} ms`;
  } else if (state.libraryEmpty) {
    // "Nothing matched" is a lie when there is nothing to match against - and
    // this is now the whole of the first-run guidance, since the box that used
    // to carry it is gone. It has to say what to do, not just what happened.
    $('status').textContent =
      'Nothing here yet. Drag music, films, books, documents or photos anywhere on this window.';
  } else if (browsing) {
    // Empty shelf, full library: they filtered to a kind they have none of,
    // or its backend is still doing its first scan.
    $('status').textContent = 'Nothing on this shelf yet.';
  } else {
    $('status').textContent = 'Nothing matched.';
  }

  show($('loading-more'), false);
  // The page just appended may not have filled the screen - on a short list,
  // or a tall monitor - in which case the sentinel is still in view and no
  // scroll will ever happen to trigger it. Check once layout has settled.
  requestAnimationFrame(maybeLoadMore);
}

/* --------------------------------------------------------- infinite scroll */

// loadMore appends the next page.
//
// It deliberately does not bump searchSeq: this request belongs to the search
// already on screen. It captures the sequence instead, so that a page which
// comes back after the user has typed something else is dropped rather than
// appended under results it has nothing to do with.
async function loadMore() {
  if (state.loadingMore || !state.hasMore) return;
  state.loadingMore = true;
  show($('loading-more'), true);

  const seq = state.searchSeq;
  const params = new URLSearchParams({
    q: state.query,
    limit: String(PAGE_SIZE),
    offset: String(state.offset),
  });
  if (state.kind) params.set('kind', state.kind);

  const { ok, body } = await api(`/api/search?${params}`);
  state.loadingMore = false;

  if (seq !== state.searchSeq) return;
  if (!ok || !body) {
    show($('loading-more'), false);
    // Leave hasMore alone: scrolling again is a perfectly good retry, and a
    // list that silently stops growing after one dropped request is worse
    // than one that tries again.
    return;
  }
  renderResults(body, true);
}

// maybeLoadMore asks whether the end of the list is close enough to be worth
// fetching for. Used both by the observer and after each append.
function maybeLoadMore() {
  if (!state.hasMore || state.loadingMore) return;
  const sentinel = $('scroll-sentinel');
  const box = sentinel.getBoundingClientRect();
  // 600px of lead time, so the next page is usually there before the gap is.
  if (box.top < window.innerHeight + 600) loadMore();
}

// The observer catches scrolling; maybeLoadMore after each append catches the
// case where the new page still did not fill the screen. An observer alone
// would stall there, because a sentinel that never left the viewport never
// crosses back into it and so never fires again.
if ('IntersectionObserver' in window) {
  new IntersectionObserver((entries) => {
    if (entries.some((e) => e.isIntersecting)) maybeLoadMore();
  }, { rootMargin: '600px' }).observe($('scroll-sentinel'));
} else {
  // Old browser: scrolling still works, it just asks on every scroll event.
  window.addEventListener('scroll', maybeLoadMore, { passive: true });
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
  document: '📄',
  picture: '🖼️',
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
    case 'picture':
      // A clip from a camera roll plays like any video; everything else is a
      // photo, shown in the viewer.
      if (item.extra && item.extra.type === 'video') playVideo(item);
      else showPhoto(item);
      break;
    case 'ebook':
    case 'document':
      // A document is always a PDF, and opens in the same viewer a PDF book
      // does; the shelves differ in what is on them, not how it is read.
      readBook(item);
      break;
    default:
      // Music and audiobooks are both just audio as far as a browser cares.
      playAudio(item);
  }
}

// The photo viewer. Photos only: stepping onto a clip would mean switching
// players mid-browse, so the arrows skip over clips and a click on one plays
// it instead.
function photosOnScreen() {
  return state.items.filter((i) => i.kind === 'picture' && !(i.extra && i.extra.type === 'video'));
}

let photoShown = null;

function showPhoto(item) {
  stopAudio();
  closeVideo();
  photoShown = item;

  const img = $('photo-image');
  img.src = `/api/art/${encodeURIComponent(item.sourceId)}/${escapeId(item.artId + '@preview')}`;
  img.alt = item.title;
  const place = item.extra && item.extra.place;
  $('photo-caption').textContent = [item.title, item.subtitle, place].filter(Boolean).join(' — ');

  const download = $('photo-download');
  download.href = streamPath(item);
  download.download = item.title;

  const photos = photosOnScreen();
  const at = photos.findIndex((p) => p.id === item.id && p.sourceId === item.sourceId);
  $('photo-prev').disabled = at <= 0;
  $('photo-next').disabled = at < 0 || at >= photos.length - 1;
  show($('photo-overlay'), true);
}

function stepPhoto(by) {
  if (!photoShown) return;
  const photos = photosOnScreen();
  const at = photos.findIndex((p) => p.id === photoShown.id && p.sourceId === photoShown.sourceId);
  const next = photos[at + by];
  if (next) showPhoto(next);
}

function closePhoto() {
  photoShown = null;
  show($('photo-overlay'), false);
  $('photo-image').removeAttribute('src');
}

$('photo-close').addEventListener('click', closePhoto);
$('photo-prev').addEventListener('click', () => stepPhoto(-1));
$('photo-next').addEventListener('click', () => stepPhoto(1));
document.addEventListener('keydown', (event) => {
  if (!photoShown) return;
  if (event.key === 'Escape') closePhoto();
  else if (event.key === 'ArrowLeft') stepPhoto(-1);
  else if (event.key === 'ArrowRight') stepPhoto(1);
});

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
  showDock(true);

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
  showDock(false);
}

// showDock also marks the body, because the dock is position: fixed and the
// page has to keep a strip clear underneath it so the last row of results is
// not permanently hidden behind a player.
//
// Conditional rather than always reserved: 96px of dead space at the bottom
// of every screen is what stopped the library card looking vertically centred
// when nothing was playing, which is most of the time.
function showDock(visible) {
  show($('audio-dock'), visible);
  document.body.classList.toggle('dock-open', visible);
}

$('audio-close').addEventListener('click', stopAudio);

/* ------------------------------------------------------- dropping in files
 *
 * The folders are the interface, so dragging a film onto the window should do
 * what dragging it into the movies folder would have done - including working
 * out that it was a film. The server decides that; this end collects the drop,
 * asks, shows the answer, and then sends the bytes.
 *
 * Three things make it more than a file picker. Folders can be dropped, and the
 * structure has to survive - Jellyfin needs "Arrival (2016)/Arrival.mkv" to be
 * a folder. The answer is worth showing before a gigabyte moves. And some
 * things genuinely cannot be worked out, in which case the server says so and
 * this end asks - once per dropped folder, not once per file.
 */

const LIBRARY_NAMES = {
  music: 'Music',
  video: 'Films',
  tv: 'TV',
  audiobook: 'Audiobooks',
  ebook: 'Ebooks',
  document: 'Documents',
  picture: 'Pictures',
};

// dragDepth counts enter/leave pairs. Moving the pointer between two elements
// fires leave on one before enter on the other, so a naive handler flickers the
// overlay on every mouse move.
let dragDepth = 0;

function draggingFiles(event) {
  const types = event.dataTransfer && event.dataTransfer.types;
  return Boolean(types) && [...types].includes('Files');
}

function showDropOverlay() {
  show($('drop-overlay'), true);
}

function hideDropOverlay() {
  dragDepth = 0;
  show($('drop-overlay'), false);
}

window.addEventListener('dragenter', (event) => {
  if (!draggingFiles(event) || $('app').classList.contains('hidden')) return;
  dragDepth += 1;
  showDropOverlay();
});

window.addEventListener('dragover', (event) => {
  // Without this the browser navigates to the file, which loses the page.
  if (draggingFiles(event)) event.preventDefault();
});

window.addEventListener('dragleave', () => {
  dragDepth -= 1;
  if (dragDepth <= 0) hideDropOverlay();
});

window.addEventListener('drop', (event) => {
  if (!draggingFiles(event) || $('app').classList.contains('hidden')) return;
  event.preventDefault();
  hideDropOverlay();
  intake(event.dataTransfer);
});

/* ---- reading what was dropped ---- */

// collectFiles turns a drop into a flat list of {file, path}, walking into any
// folders. The relative path is what preserves an album or a film folder.
async function collectFiles(dataTransfer) {
  const entries = [...(dataTransfer.items || [])]
    .map((item) => (item.webkitGetAsEntry ? item.webkitGetAsEntry() : null))
    .filter(Boolean);

  // Older browsers, and some drops, give no entries at all. A flat file list
  // is worse than nothing only if we refuse it.
  if (!entries.length) {
    return [...(dataTransfer.files || [])].map((file) => ({ file, path: file.name }));
  }

  const out = [];
  for (const entry of entries) await walkEntry(entry, '', out);
  return out;
}

function walkEntry(entry, prefix, out) {
  const here = prefix ? `${prefix}/${entry.name}` : entry.name;

  if (entry.isFile) {
    return new Promise((resolve) => {
      entry.file((file) => { out.push({ file, path: here }); resolve(); }, resolve);
    });
  }

  return new Promise((resolve) => {
    const reader = entry.createReader();
    const found = [];
    // readEntries hands back at most a hundred at a time and has to be called
    // until it returns none. Reading once silently truncates a big folder,
    // which is the classic way to lose half an album.
    const readMore = () => reader.readEntries(async (batch) => {
      if (!batch.length) {
        for (const child of found) await walkEntry(child, here, out);
        resolve();
        return;
      }
      found.push(...batch);
      readMore();
    }, resolve);
    readMore();
  });
}

/* ---- the drop itself ---- */

let intakeBusy = false;

async function intake(dataTransfer) {
  if (intakeBusy) return;
  intakeBusy = true;
  try {
    await runIntake(dataTransfer);
  } finally {
    intakeBusy = false;
  }
}

async function runIntake(dataTransfer) {
  show($('intake'), true);
  show($('intake-bar'), false);
  $('intake-list').replaceChildren();
  $('intake-questions').replaceChildren();
  $('intake-title').textContent = 'Reading what you dropped…';

  const dropped = await collectFiles(dataTransfer);
  if (!dropped.length) {
    $('intake-title').textContent = 'Nothing usable was dropped.';
    return;
  }

  const paths = dropped.map((d) => d.path);
  const choices = {};

  // Ask until there is nothing left to ask. Each answer goes back to the
  // server, which re-plans - so the destination shown is always the one the
  // server will actually use, rather than something worked out twice.
  for (;;) {
    const { ok, body } = await api('/api/upload/plan', {
      method: 'POST',
      body: JSON.stringify({ paths, choices }),
    });
    if (!ok || !body) {
      $('intake-title').textContent = (body && body.error) || 'SoundStorm could not take those.';
      return;
    }

    // Remembered from the plan rather than fetched separately, so the figure is
    // from the same moment as the placements. Absent on a build that cannot
    // measure it, which enoughRoom treats as "do not block".
    lastPlanFree = typeof body.freeBytes === 'number' ? body.freeBytes : null;
    $('intake-note').textContent = '';
    show($('intake-note'), false);

    const questions = body.questions || [];
    if (!questions.length) {
      await sendFiles(body.files || [], dropped);
      return;
    }

    $('intake-title').textContent = questions.length === 1
      ? 'One thing SoundStorm cannot tell'
      : `${questions.length} things SoundStorm cannot tell`;
    const answer = await askQuestion(questions[0]);
    if (answer === null) {
      $('intake-title').textContent = 'Cancelled — nothing was added.';
      $('intake-questions').replaceChildren();
      return;
    }
    choices[questions[0].group] = answer;
  }
}

// askQuestion shows one question and resolves with the chosen library, or null
// if the drop was abandoned.
function askQuestion(question) {
  return new Promise((resolve) => {
    const host = $('intake-questions');
    host.replaceChildren();

    const li = document.createElement('li');

    const text = document.createElement('span');
    text.className = 'question-text';
    const name = document.createElement('strong');
    name.textContent = question.label;
    text.append(
      name,
      document.createTextNode(
        question.count === 1
          ? ' — is this one of your…'
          : ` — are these ${question.count} files…`),
    );

    const options = document.createElement('span');
    options.className = 'question-options';
    for (const kind of question.options) {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = LIBRARY_NAMES[kind] || kind;
      button.addEventListener('click', () => { host.replaceChildren(); resolve(kind); });
      options.append(button);
    }

    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'ghost';
    cancel.textContent = 'Cancel';
    cancel.addEventListener('click', () => { host.replaceChildren(); resolve(null); });
    options.append(cancel);

    li.append(text, options);
    host.append(li);
  });
}

// sendFiles uploads everything the final plan accepted.
// What the last plan said was free on the library disk, or null when the server
// could not measure it.
let lastPlanFree = null;

// Files whose own tags decide their folder go first within a group.
//
// An Audible book is an .m4b beside a companion .pdf. The server reads the m4b's
// tags to file it under its author, and the pdf has no tags to read - so it joins
// whichever folder its group already made. That only works if the m4b has
// already been sent, which is what this guarantees. Get it backwards and the
// book lands under two authors, one of them "Unknown Author".
//
// Stable within a group and it does not reorder groups, so the progress list
// still reads in the order somebody dropped things.
//
// Which files describe themselves depends on the shelf: a .pdf is a book on the
// ebook shelf and a companion to an audiobook, so it goes first in one and
// last in the other.
const TAGGABLE = /\.(mp3|m4a|m4b|mp4|flac)$/i;
const SELF_DESCRIBING_BOOK = /\.(epub|pdf)$/i;

function describesItself(item) {
  return (item.kind === 'ebook' ? SELF_DESCRIBING_BOOK : TAGGABLE).test(item.file.name);
}

function orderTaggableFirst(queue) {
  const groups = [];
  const seen = new Map();
  for (const item of queue) {
    const key = item.group || item.path;
    if (!seen.has(key)) {
      seen.set(key, []);
      groups.push(key);
    }
    seen.get(key).push(item);
  }
  let at = 0;
  for (const key of groups) {
    const members = seen.get(key);
    for (const item of members) {
      if (describesItself(item)) queue[at++] = item;
    }
    for (const item of members) {
      if (!describesItself(item)) queue[at++] = item;
    }
  }
}

// enoughRoom stops a drop that cannot fit before it starts.
//
// Ninety-four audiobooks were dropped onto a host with no space left. Each is a
// separate request, so ninety-one of them failed one at a time over several
// minutes, and the only sign was a per-file error nobody could act on. The
// server reports what it has; the browser is the only side that knows what is
// coming, so the comparison happens here.
//
// A tenth on top, because the staging copy and the rename are not free and a
// disk at exactly zero is a bad place to find the edge.
function enoughRoom(total) {
  if (typeof lastPlanFree !== 'number') return true;  // unmeasurable; do not block
  const needed = total * 1.1;
  if (needed <= lastPlanFree) return true;
  $('intake-title').textContent =
    `Not enough room: ${bytes(total)} to add, ${bytes(lastPlanFree)} free.`;
  $('intake-note').textContent =
    'Nothing was copied. Free some space on the drive holding your library, then drop these again.';
  show($('intake-note'), true);
  return false;
}

// bytes formats for somebody reading a sentence, so it rounds.
function bytes(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GB';
  if (n >= 1 << 20) return Math.round(n / (1 << 20)) + ' MB';
  if (n >= 1 << 10) return Math.round(n / (1 << 10)) + ' KB';
  return n + ' bytes';
}

async function sendFiles(plan, dropped) {
  $('intake-list').replaceChildren();

  const queue = [];
  plan.forEach((placement, index) => {
    renderIntakeRow(placement);
    if (!placement.skipped && !placement.waiting) {
      queue.push({ ...placement, file: dropped[index].file });
    }
  });

  if (!queue.length) {
    $('intake-title').textContent = 'Nothing here could be added.';
    return;
  }

  orderTaggableFirst(queue);

  const total = queue.reduce((sum, item) => sum + item.file.size, 0);
  if (!enoughRoom(total)) return;
  show($('intake-bar'), true);

  let done = 0;
  let sent = 0;
  let declined = 0; // already there, or the same recording is
  for (const item of queue) {
    $('intake-title').textContent =
      `Adding ${done + 1} of ${queue.length} — ${item.file.name}`;
    const result = await uploadOne(item, (loaded) => {
      setIntakeProgress(total ? (sent + loaded) / total : 0);
    });
    sent += item.file.size;
    done += 1;
    setIntakeProgress(total ? sent / total : 1);
    if (!result.ok && result.skipped) declined += 1;
    markIntakeRow(item.path, result);
  }

  const failed = document.querySelectorAll('#intake-list .intake-failed').length;
  // Skipped at planning, and skipped by the server as already there.
  const skipped = plan.filter((p) => p.skipped).length + declined;
  $('intake-title').textContent = summary(done - failed - declined, skipped, failed);
  show($('intake-bar'), false);

  // Counts on the folder guide have moved, and a scan is probably running.
  loadLibrary();
}

function summary(added, skipped, failed) {
  const parts = [`Added ${added} file${added === 1 ? '' : 's'}`];
  if (skipped) parts.push(`${skipped} skipped`);
  if (failed) parts.push(`${failed} failed`);
  return parts.join(' · ') + '. It may take a minute to appear in search.';
}

// uploadOne sends one file. XMLHttpRequest rather than fetch, only because
// fetch cannot report upload progress, and a four gigabyte film with no
// progress bar looks like a hang.
function uploadOne(item, onProgress) {
  return new Promise((resolve) => {
    const params = new URLSearchParams({ path: item.path, kind: item.kind });
    const request = new XMLHttpRequest();
    request.open('PUT', `/api/upload?${params}`);
    request.withCredentials = true;

    request.upload.addEventListener('progress', (event) => {
      if (event.lengthComputable) onProgress(event.loaded);
    });
    request.addEventListener('load', () => {
      if (request.status === 200) {
        let dest = '';
        try {
          dest = JSON.parse(request.responseText).dest || '';
        } catch {
          // The file is in; only the display of where is lost.
        }
        resolve({ ok: true, dest });
        return;
      }
      let message = `failed (${request.status})`;
      try {
        message = JSON.parse(request.responseText).error || message;
      } catch {
        // A non-JSON error body is still an error; the status will do.
      }
      // 409 is the server declining on purpose - the file, or the same
      // recording, is already there - which is a skip, not a failure.
      resolve({ ok: false, skipped: request.status === 409, error: message });
    });
    request.addEventListener('error', () => resolve({ ok: false, error: 'connection lost' }));
    request.addEventListener('abort', () => resolve({ ok: false, error: 'cancelled' }));
    request.send(item.file);
  });
}

function setIntakeProgress(fraction) {
  $('intake-fill').style.width = `${Math.min(Math.max(fraction, 0), 1) * 100}%`;
}

function renderIntakeRow(placement) {
  const li = document.createElement('li');
  li.dataset.path = placement.path;

  const name = document.createElement('span');
  name.className = 'intake-name';
  name.textContent = placement.path;
  name.title = placement.path;

  const dest = document.createElement('span');
  dest.className = 'intake-dest';
  dest.textContent = placement.skipped ? placement.reason : placement.dest;

  if (placement.skipped) li.className = 'intake-skipped';
  li.append(name, dest);
  $('intake-list').append(li);
}

function markIntakeRow(path, result) {
  const li = $('intake-list').querySelector(`li[data-path="${CSS.escape(path)}"]`);
  if (!li) return;
  const dest = li.querySelector('.intake-dest');
  if (result.ok) {
    // Where it actually went, which can differ from the plan: the plan is made
    // before the bytes arrive, so it cannot read the tags that name an author,
    // and a book dropped inside a "Books" folder was shown heading for
    // "Unknown Author" right up until it landed under the real one.
    dest.textContent = `${result.dest || dest.textContent} ✓`;
    return;
  }
  li.classList.add(result.skipped ? 'intake-skipped' : 'intake-failed');
  dest.textContent = result.error;
}

$('intake-close').addEventListener('click', () => {
  show($('intake'), false);
  $('intake-questions').replaceChildren();
});

/* ------------------------------------------------------------------- boot */

// moveToSecureName sends the page to the install's real https address - a
// soundstorm.dev name with a certificate every browser trusts - but only after
// checking this browser can actually reach it. Plenty of routers refuse to
// resolve a public name that points at a home address (DNS rebinding
// protection), and a redirect into a failure is worse than staying on http.
//
// no-cors is enough: the fetch resolves only if the name resolved, the
// connection opened and the certificate verified, which is the whole question.
// The port is the one in use now, because the server answers http and https
// on the same one. Resolves false when it stays put.
async function moveToSecureName(name) {
  const port = location.port || (location.protocol === 'https:' ? '443' : '80');
  const target = 'https://' + name + (port === '443' ? '' : ':' + port);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 2500);
  try {
    await fetch(target + '/healthz', { mode: 'no-cors', cache: 'no-store', signal: controller.signal });
  } catch {
    return false;
  } finally {
    clearTimeout(timer);
  }
  location.replace(target + location.pathname + location.search + location.hash);
  return true;
}

(async function boot() {
  const { ok, body, offline } = await api('/api/session');
  if (ok && body) {
    if (body.secureName && await moveToSecureName(body.secureName)) return;
    if (body.signedIn) showApp(body.user);
    else showGate(body.hasAccount, body.setupCodeRequired);
    return;
  }

  // Whatever went wrong, stop spinning. A spinner that never resolves is the
  // one failure that tells somebody nothing at all.
  const boot = $('boot');
  boot.replaceChildren();

  const message = document.createElement('p');
  if (offline) {
    // The page itself loaded, so this is the server going away after that,
    // or a certificate that changed underneath an open tab. Reloading covers
    // both: the service worker never answers a page load (see sw.js, rule 3),
    // so a changed certificate comes back as the browser's own warning rather
    // than this screen again. The text says so, because that warning is
    // alarming if nobody said it was coming.
    message.textContent = 'Cannot reach SoundStorm.';
    const detail = document.createElement('p');
    detail.className = 'muted';
    detail.textContent =
      'Check that the computer running it is on, then try again. If your '
      + 'browser warns that the connection is not private, that is expected '
      + 'after SoundStorm is reinstalled: choose Advanced, then continue.';
    const again = document.createElement('button');
    again.type = 'button';
    again.textContent = 'Try again';
    again.addEventListener('click', () => location.reload());
    boot.append(message, detail, again);
  } else {
    message.textContent = 'SoundStorm is not responding.';
    boot.append(message);
  }
})();
