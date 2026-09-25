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

// Streaming quality is this device's own choice - a phone on mobile data and
// the computer on the Wi-Fi want different things - so it lives here, not in
// the account. "auto" lowers it only where the browser says the connection is
// mobile data or the data saver is on, which Android's Chrome can tell and an
// iPhone cannot (there it streams the original). Downloads always keep the
// original: streamPath, not playPath.
const QUALITY_KEY = 'soundstorm.quality';
function streamingKbps() {
  const choice = localStorage.getItem(QUALITY_KEY) || 'original';
  if (choice === 'auto') {
    const c = navigator.connection;
    return c && (c.type === 'cellular' || c.saveData) ? 128 : 0;
  }
  return Number(choice) || 0;
}
const playPath = (item) => {
  const kbps = item.kind === 'music' ? streamingKbps() : 0;
  return streamPath(item) + (kbps ? `?kbps=${kbps}` : '');
};

/* ------------------------------------------------------------------- gate */

// The setup code arrives in the address the installer opened. It is read here,
// but only taken out of the address once the page knows it is staying - see
// forgetSetupCodeInAddress.
const setupFromAddress = new URLSearchParams(location.search).get('setup') || '';

// forgetSetupCodeInAddress takes the setup code out of the address, so it is
// not left in the history or a bookmark.
//
// Not at load, which is when it used to happen. The page moves itself to the
// https name as soon as it has one (moveToSecureName), carrying the query
// across - and by then the code had already been stripped, so it arrived on
// the new page as nothing. The installer waits for that name before opening
// the browser, so on a fresh install the move nearly always happened, and the
// first screen asked for a setup code the person had never been shown.
function forgetSetupCodeInAddress() {
  const params = new URLSearchParams(location.search);
  if (!params.has('setup')) return;
  params.delete('setup');
  const rest = params.toString();
  history.replaceState(null, '', location.pathname + (rest ? `?${rest}` : '') + location.hash);
}

function showGate(hasAccount, setupCodeRequired) {
  show($('boot'), false);
  show($('app'), false);
  show($('tabs'), false);
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
  setAccountOpen(false);
  stopAudio();
  closeVideo();
  // Downloads are the signed-in person's; a shared device keeps nothing of
  // theirs after they sign out.
  await clearDownloads();
  await api('/api/logout', { method: 'POST' });
  if (state.setupTimer) clearInterval(state.setupTimer);
  clearTimeout(state.remotePoll);
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
  renderTabs();
  renderAccount();
  await loadFavouriteKeys();
  maybeShowHoldTip();
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
    ? `Signed in as ${me.name}, the owner of this server.`
    : `Signed in as ${me.name}.`;

  // Hiding the controls is presentation, not permission - the server refuses
  // these calls for a member whether or not the form is on screen.
  show($('people-block'), Boolean(me.owner));
  show($('select-toggle'), Boolean(me.owner));
  if (me.owner) {
    loadPeople();
    refreshRemote();
    refreshLyricsSetting();
  } else {
    show($('lyrics-block'), false);
  }
}

// The lyrics setting is on the owner's session only, and only when the server
// can look lyrics up at all.
async function refreshLyricsSetting() {
  const { ok, body } = await api('/api/session');
  const has = ok && body && typeof body.onlineLyrics === 'boolean';
  show($('lyrics-block'), has);
  if (has) $('lyrics-toggle').checked = body.onlineLyrics;
}

$('lyrics-toggle').addEventListener('change', async (event) => {
  const enabled = event.target.checked;
  const { ok, body } = await api('/api/settings/lyrics', { method: 'PUT', body: JSON.stringify({ enabled }) });
  if (!ok) {
    event.target.checked = !enabled;
    note($('lyrics-note'), (body && body.error) || 'Could not change it.', true);
    return;
  }
  note($('lyrics-note'), enabled ? 'On. Songs without lyrics are looked up as they play.' : 'Off. Only lyrics in your own files are shown.', false);
});

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
  if (!remote || !remote.available) {
    clearTimeout(state.remotePoll);
    return;
  }
  renderRemote(remote);

  // While it is on but not yet reachable, reachability is still being
  // established (a certificate, a DNS record, the port opening), so re-check for
  // a while - but only while the panel is open, and never in a tight loop.
  clearTimeout(state.remotePoll);
  if (remote.enabled && !remote.reachable && !$('account').classList.contains('hidden')) {
    state.remotePoll = setTimeout(refreshRemote, 8000);
  }
}

function renderRemote(remote) {
  $('remote-toggle').checked = Boolean(remote.enabled);
  const share = $('remote-share');
  const url = $('remote-share-url');
  const status = $('remote-status');

  if (remote.enabled && remote.reachable && remote.name) {
    const href = awayAddress(remote);
    url.textContent = href;
    url.href = href;
    show(share, true);
  } else {
    show(share, false);
  }

  const howto = $('remote-tailscale-howto');
  show(howto, false);
  if (!remote.enabled) {
    show(status, false);
    return;
  }
  // On: say whether it actually worked, and if not, exactly what to do.
  if (remote.reachable) {
    status.textContent = remote.mapped
      ? `Reachable. The port was opened automatically${remote.method ? ` (${remote.method})` : ''}.`
      : 'Reachable.';
  } else if (remote.mapped) {
    status.textContent = 'Opening the door… the port was opened on your router; '
      + 'waiting for it to be reachable from the internet. This can take a minute.';
  } else if (remote.upstream === 'shared') {
    // The router's own internet address is in the range providers use when many
    // homes share one address. Asking for a port forward here would send
    // somebody to their router for something that cannot work.
    status.textContent = `This can't work on your internet connection. Your provider `
      + `shares one internet address between many homes, so nothing from outside `
      + `can reach your router, and opening a port won't change that. Tailscale `
      + `works here instead: it connects your own devices privately, with nothing to open.`;
    show(howto, true);
  } else if (remote.upstream === 'router') {
    const port = remote.port || 8099;
    status.textContent = `Your router is plugged into another router, often the box `
      + `from your internet provider, so opening a port on this one isn't enough. `
      + `Either set the provider's box to bridge mode, or forward port ${port} (TCP) `
      + `on both. Or use Tailscale, which needs neither.`;
    show(howto, true);
  } else {
    const port = remote.port || 8099;
    status.textContent = 'Not reachable from the internet yet. If it stays this way, '
      + `forward port ${port} (TCP) to this computer on your router, then check again.`;
  }
  show(status, true);
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
  note(
    $('remote-note'),
    enabled
      ? 'On. It can take a minute for the address to work the first time while the certificate is issued.'
      : 'Off. Your server is only reachable on your home network again.',
    false,
  );
  // Render and, while it comes up, keep checking (refreshRemote schedules the poll).
  refreshRemote();
});

// The account is a page of its own: the library steps aside while it is open,
// rather than the account being pushed in between the setup box and the
// results, where it read as part of the library.
function setAccountOpen(opening) {
  show($('account'), opening);
  $('app').classList.toggle('viewing-account', opening);
  if (opening) {
    renderAccount();
    refreshDevices();
    window.scrollTo(0, 0);
  } else {
    clearTimeout(state.remotePoll); // stop polling once the page is closed
  }
}


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
  state.shelfFiles = {};
  for (const f of folders) state.shelfFiles[f.kind] = (state.shelfFiles[f.kind] || 0) + (f.files || 0);
  renderTabs();
  const indexing = folders.some(
    (f) => typeof f.indexed === 'number' && f.indexed < f.files);
  show($('library-indexing'), indexing);

  // The address for another device lives behind one button, and the button
  // only exists when the server has an honest answer to put there.
  const link = $('library-share-url');
  if (body.shareURL) {
    link.textContent = body.shareURL;
    link.href = body.shareURL;
  }
  show($('devices'), Boolean(body.shareURL));

  // The disk the library is on, when it is getting full.
  const space = body.space;
  const warning = $('space-warning');
  if (space && space.level) {
    const left = formatBytes(space.free);
    warning.textContent = space.level === 'critical'
      ? `The library drive is almost full: ${left} left. Large files like films won't fit.`
      : `The library drive is getting full: ${left} left.`;
    warning.classList.toggle('critical', space.level === 'critical');
  }
  show(warning, Boolean(space && space.level));
}

// "Use on your phone or TV": the home address, and the away-from-home one when
// remote access is on and working. Only the owner's session carries the latter,
// so a member sees the home address alone.
$('quality-select').value = localStorage.getItem(QUALITY_KEY) || 'original';
$('quality-select').addEventListener('change', (event) => {
  localStorage.setItem(QUALITY_KEY, event.target.value);
  note($('playback-note'), 'Saved. It applies from the next song.', false);
});

// The away-from-home address, with its port. The router forwards the port
// SoundStorm listens on (8099 unless changed), not 443, so an address without
// it knocks on the router's own door - which answers, if at all, with its own
// certificate and a warning that the site is impersonating SoundStorm.
function awayAddress(remote) {
  const port = Number(remote.port) || 443;
  return `https://${remote.name}${port === 443 ? '' : `:${port}`}`;
}

// The away-from-home address, filled in each time the account page opens:
// only the owner's session carries it, so a member sees the home address alone.
async function refreshDevices() {
  const { ok, body } = await api('/api/session');
  const remote = ok && body && body.remote;
  const away = remote && remote.enabled && remote.reachable && remote.name;
  if (away) {
    const href = awayAddress(remote);
    $('devices-away-url').textContent = href;
    $('devices-away-url').href = href;
  }
  show($('devices-away'), Boolean(away));
}

// Copy buttons, beside every address somebody has to carry to another device.
// The clipboard API only exists in a secure context; on plain http the buttons
// go, and the address - large and selectable - is still there to copy by hand.
for (const button of document.querySelectorAll('button.copy')) {
  if (!(navigator.clipboard && window.isSecureContext)) {
    button.remove();
    continue;
  }
  button.addEventListener('click', async () => {
    const text = $(button.dataset.copy).textContent.trim();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      button.textContent = 'Copied';
    } catch {
      button.textContent = 'Could not copy';
    }
    setTimeout(() => { button.textContent = 'Copy'; }, 1800);
  });
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

$('choose-files').addEventListener('click', () => $('file-picker').click());

$('file-picker').addEventListener('change', (event) => {
  const files = [...(event.target.files || [])];
  if (!files.length) return;
  // Reset first: picking the same file twice in a row fires no change event
  // otherwise, which reads as the button having stopped working.
  event.target.value = '';
  intake({ items: [], files });
});

// What each backend is to the person using SoundStorm: a shelf.
const SETUP_SHELVES = {
  navidrome: 'Music',
  jellyfin: 'Films and TV',
  audiobookshelf: 'Audiobooks',
  immich: 'Pictures',
  ebooks: 'Ebooks',
  documents: 'Documents',
  calibreweb: 'Calibre library',
};

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

    // The shelf, never the server behind it. Somebody setting this up never
    // learns Jellyfin exists, and this list is where they used to.
    const name = document.createElement('span');
    name.className = 'setup-name';
    name.textContent = SETUP_SHELVES[backend.id] || 'Your library';

    const detail = document.createElement('span');
    detail.className = 'setup-detail';
    detail.textContent =
      backend.status === 'ready' ? 'Ready'
        : backend.status === 'failed' ? 'Needs attention'
          : 'Getting ready\u2026';

    li.append(dot, name, detail);
    // Why a shelf failed is the owner's to fix, and only the owner's session
    // carries it. Folded away, because it quotes the server behind the shelf.
    if (backend.status === 'failed' && backend.error) {
      const why = document.createElement('details');
      why.className = 'setup-why';
      const summary = document.createElement('summary');
      summary.textContent = 'Details';
      const text = document.createElement('p');
      text.textContent = `${backend.error} Restarting SoundStorm usually clears this.`;
      why.append(summary, text);
      li.append(why);
    }
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
    const visible = everything || kind === '' || chip.classList.contains('chip-own') || allowed.includes(kind);
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
    noteTabKind();
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
  refreshContinue();

  // Keep only the newest response: debounced typing means several can be in
  // flight and they do not necessarily come back in order.
  const seq = ++state.searchSeq;

  // The two personal views are not a search of a shelf.
  const own = state.kind === 'favourites' || state.kind === 'playlists';
  renderSearchHint();
  const musicBrowse = state.kind === 'music' && state.musicView !== 'songs'
    && !(state.musicView === 'mixes' && state.query);
  show($('music-tabs'), state.kind === 'music' || state.kind === 'playlists');
  markMusicTabs();
  show($('album-sort'), state.kind === 'music' && state.musicView === 'albums');
  show($('music-view'), musicBrowse);
  show($('playlists-view'), state.kind === 'playlists');
  const home = state.kind === '' && !query;
  show($('home-view'), home);
  show($('results-bar'), !home);
  show($('results'), state.kind !== 'playlists' && !musicBrowse && !home);
  if (home) {
    show($('select-toggle'), false);
    state.hasMore = false;
    await renderHome(seq);
    return;
  }
  if (musicBrowse) {
    show($('select-toggle'), false);
    state.hasMore = false;
    await showMusicView(seq);
    return;
  }
  show($('select-toggle'), Boolean(state.me && state.me.owner) && !own);
  if (state.kind === 'favourites') {
    await showFavourites(seq);
    return;
  }
  if (state.kind === 'playlists') {
    $('status').textContent = '';
    state.hasMore = false;
    await showPlaylists();
    return;
  }
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
    // A phone cannot drag anything, so it is pointed at the button instead.
    $('status').textContent = matchMedia('(pointer: coarse)').matches
      ? 'Nothing here yet. Open Settings and choose Add media to add music, films, books, documents or photos.'
      : 'Nothing here yet. Drag music, films, books, documents or photos anywhere on this window, or use Add media in Settings.';
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
function loadMore() {
  if (state.loadingMore) return state.loadMorePromise;
  if (!state.hasMore) return Promise.resolve();
  state.loadMorePromise = fetchMore();
  return state.loadMorePromise;
}

async function fetchMore() {
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

  // A tick, shown only while selecting.
  const check = document.createElement('span');
  check.className = 'check';
  check.setAttribute('aria-hidden', 'true');
  check.textContent = '\u2713';
  wrap.append(check);

  const key = selectionKey(item);
  card.dataset.key = key;
  card.classList.toggle('selected', state.selected.has(key));
  card.addEventListener('click', (event) => {
    // The click that ends a long press is not a tap: the menu is open.
    if (state.suppressClick) {
      state.suppressClick = false;
      event.preventDefault();
      // Nor may it reach the page, where a click outside the menu closes it:
      // lifting the finger would close the menu it had just opened.
      event.stopPropagation();
      return;
    }
    if (state.selecting) toggleSelected(item, card);
    else play(item);
  });
  attachItemMenuGestures(card, item);
  if (state.favourites.has(key)) wrap.classList.add('is-favourite');

  // The card is itself a button, and a button cannot hold another, so the
  // "..." sits beside it in a wrapper, positioned over the cover.
  const holder = document.createElement('div');
  holder.className = 'item-holder';
  const more = document.createElement('button');
  more.type = 'button';
  more.className = 'item-more';
  more.setAttribute('aria-label', `More for ${item.title}`);
  more.append(icon('more'));
  more.addEventListener('click', (event) => {
    event.stopPropagation();
    openItemMenu(item, more);
  });
  holder.append(card, more);
  return holder;
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

const NO_COVER = '/static/no-cover.svg';

function fallbackArt(item) {
  if (item.kind === 'music') {
    const img = document.createElement('img');
    img.src = NO_COVER;
    img.alt = '';
    img.className = 'no-cover';
    return img;
  }
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
      if (item.kind === 'music') openNowPlaying();
  }
}

// The photo viewer. Photos only: stepping onto a clip would mean switching
// players mid-browse, so the arrows skip over clips and a click on one plays
// it instead.
function photosOnScreen() {
  return state.items.filter((i) => i.kind === 'picture' && !(i.extra && i.extra.type === 'video'));
}

let photoShown = null;
let photoWaiting = false;

const photoPreview = (item) => `/api/art/${encodeURIComponent(item.sourceId)}/${escapeId(item.artId + '@preview')}`;

function showPhoto(item) {
  stopAudio();
  closeVideo();
  photoShown = item;

  const img = $('photo-image');
  img.src = photoPreview(item);
  img.alt = item.title;
  const place = item.extra && item.extra.place;
  $('photo-caption').textContent = [item.title, item.subtitle, place].filter(Boolean).join(' — ');

  const download = $('photo-download');
  download.href = streamPath(item);
  download.download = item.title;

  const photos = photosOnScreen();
  const at = photos.findIndex((p) => p.id === item.id && p.sourceId === item.sourceId);
  renderPhotoPlace();
  // The viewer walks the same list as the page, which arrives fifty at a time
  // as it is scrolled. Nearing the end of what has arrived, it fetches the
  // next page, so swiping carries on through the whole shelf.
  if (at >= photos.length - 3 && state.hasMore) loadMore().then(renderPhotoPlace);
  photoZoom.reset();
  show($('photo-overlay'), true);
  document.body.classList.add('photo-open');
  // The photos either side, fetched now, so a swipe lands on a picture that is
  // already there rather than on a blank while it loads.
  for (const near of [photos[at - 1], photos[at + 1]]) {
    if (near) new Image().src = photoPreview(near);
  }
}

// Where the photo on screen is in the list: the arrows, and "12 of 50+"
// while there are more to come.
function renderPhotoPlace() {
  if (!photoShown) return;
  const photos = photosOnScreen();
  const at = photos.findIndex((p) => p.id === photoShown.id && p.sourceId === photoShown.sourceId);
  $('photo-prev').disabled = at <= 0;
  $('photo-next').disabled = at < 0 || (at >= photos.length - 1 && !state.hasMore);
  $('photo-count').textContent = at >= 0 && (photos.length > 1 || state.hasMore)
    ? `${at + 1} of ${photos.length}${state.hasMore ? '+' : ''}` : '';
}

async function stepPhoto(by) {
  if (!photoShown) return false;
  let photos = photosOnScreen();
  const at = photos.findIndex((p) => p.id === photoShown.id && p.sourceId === photoShown.sourceId);
  if (!photos[at + by] && by > 0 && state.hasMore) {
    // Faster than the page could arrive: wait for it, and take no other step
    // meanwhile - each would start from this same photo and land on the same
    // next one.
    if (photoWaiting) return false;
    photoWaiting = true;
    try {
      await loadMore();
    } finally {
      photoWaiting = false;
    }
    photos = photosOnScreen();
  }
  const next = photos[at + by];
  if (next) showPhoto(next);
  return Boolean(next);
}

function closePhoto() {
  photoShown = null;
  show($('photo-overlay'), false);
  document.body.classList.remove('photo-open');
  $('photo-overlay').classList.remove('pv-bare');
  $('photo-overlay').style.backgroundColor = '';
  photoZoom.reset();
  $('photo-image').removeAttribute('src');
}

$('photo-close').addEventListener('click', closePhoto);
$('photo-prev').addEventListener('click', () => stepPhoto(-1));
$('photo-next').addEventListener('click', () => stepPhoto(1));
// Looking at a photo, with a finger or a mouse:
//   swipe left or right   the next or previous photo, which slides in;
//   swipe down            close, the background fading as the photo drops;
//   pinch                 zoom, about the point between the fingers;
//   drag, when zoomed     look around the photo, which stops at its edges;
//   double-tap            zoom in on that point, or back out;
//   tap                   hide or show the bar and the caption;
//   wheel, on a computer  zoom about the pointer; double-click as double-tap.
// Swipes only step or close at normal size - zoomed in, a drag is looking
// around. All of it is pointer events, so a finger and a mouse share one path.
const photoZoom = (() => {
  const stage = $('photo-stage');
  const img = $('photo-image');
  const overlay = $('photo-overlay');
  const MAX = 5;
  let s = 1;
  let tx = 0;
  let ty = 0;
  const pointers = new Map();
  let gesture = null;
  let lastTap = { t: 0, x: 0, y: 0 };
  let tapTimer = 0;

  const centre = () => {
    const r = stage.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  };
  // Points relative to the middle of the stage, which is where the photo's
  // transform is anchored.
  const rel = (e) => { const c = centre(); return { x: e.clientX - c.x, y: e.clientY - c.y }; };
  const clamp = () => {
    const w = img.clientWidth * s;
    const h = img.clientHeight * s;
    const mx = Math.max(0, (w - stage.clientWidth) / 2);
    const my = Math.max(0, (h - stage.clientHeight) / 2);
    tx = Math.min(mx, Math.max(-mx, tx));
    ty = Math.min(my, Math.max(-my, ty));
  };
  const apply = (animate) => {
    img.style.transition = animate ? 'transform 0.22s ease-out, opacity 0.22s ease-out' : 'none';
    img.style.transform = `translate(${tx}px, ${ty}px) scale(${s})`;
  };
  const zoomAbout = (p, next, animate) => {
    next = Math.min(MAX, Math.max(1, next));
    // Keep the photo point under p where it is: p = t + q*s for the same q.
    tx = p.x - ((p.x - tx) * next) / s;
    ty = p.y - ((p.y - ty) * next) / s;
    s = next;
    if (s === 1) { tx = 0; ty = 0; }
    clamp();
    apply(animate);
  };
  const reset = () => { s = 1; tx = 0; ty = 0; img.style.opacity = ''; apply(false); };

  // The photos either side ride along beside this one during a swipe, a
  // small gap apart, so the next one is already there as this one leaves.
  const GAP = 16;
  const sides = { '-1': $('photo-prev-image'), 1: $('photo-next-image') };
  const neighbours = () => {
    const photos = photosOnScreen();
    const at = photos.findIndex((p) => p.id === photoShown.id && p.sourceId === photoShown.sourceId);
    return { '-1': at > 0 ? photos[at - 1] : null, 1: at >= 0 ? photos[at + 1] : null };
  };
  const prepSides = () => {
    const near = neighbours();
    for (const by of ['-1', '1']) {
      const el = sides[by];
      if (near[by]) {
        const url = photoPreview(near[by]);
        if (el.dataset.src !== url) { el.src = url; el.dataset.src = url; }
        el.style.visibility = 'visible';
      } else {
        el.style.visibility = 'hidden';
      }
    }
  };
  const placeSides = (offset, animate) => {
    const w = stage.clientWidth + GAP;
    for (const by of ['-1', '1']) {
      const el = sides[by];
      el.style.transition = animate ? 'transform 0.22s ease-out' : 'none';
      el.style.transform = `translateX(${offset + Number(by) * w}px)`;
    }
  };
  const hideSides = () => { for (const el of Object.values(sides)) el.style.visibility = 'hidden'; };
  const toggleBars = () => overlay.classList.toggle('pv-bare');

  const onTap = (e) => {
    const now = performance.now();
    const p = rel(e);
    if (now - lastTap.t < 300 && Math.hypot(p.x - lastTap.x, p.y - lastTap.y) < 30) {
      clearTimeout(tapTimer);
      lastTap.t = 0;
      zoomAbout(p, s > 1 ? 1 : 2.5, true);
      return;
    }
    lastTap = { t: now, x: p.x, y: p.y };
    // A single tap waits a moment, in case it is the first of two.
    tapTimer = setTimeout(toggleBars, 300);
  };

  const swipeEnd = (g) => {
    const width = stage.clientWidth;
    const speed = (g.axis === 'x' ? Math.abs(g.dx) : g.dy) / Math.max(performance.now() - g.t0, 1);
    if (g.axis === 'y') {
      if (g.dy > 120 || (speed > 0.6 && g.dy > 40)) {
        ty = stage.clientHeight;
        img.style.opacity = '0';
        apply(true);
        setTimeout(closePhoto, 200);
      } else {
        tx = 0; ty = 0; apply(true);
        overlay.style.backgroundColor = '';
      }
      return;
    }
    const by = g.dx < 0 ? 1 : -1;
    const photos = photosOnScreen();
    const at = photos.findIndex((p) => p.id === photoShown.id && p.sourceId === photoShown.sourceId);
    const more = photos[at + by] || (by > 0 && state.hasMore);
    const beside = sides[String(by)];
    const ready = beside.style.visibility === 'visible' && beside.complete && beside.naturalWidth > 0;
    if (more && (Math.abs(g.dx) > width * 0.25 || (speed > 0.5 && Math.abs(g.dx) > 30))) {
      if (ready) {
        // The neighbour slides into the middle; then it becomes the photo.
        tx = -by * (width + GAP); apply(true);
        placeSides(-by * (width + GAP), true);
        setTimeout(async () => {
          img.style.visibility = 'hidden';
          if (await stepPhoto(by)) {
            try { await img.decode(); } catch { /* shown when it loads */ }
          }
          img.style.visibility = '';
          hideSides();
        }, 230);
      } else {
        // Not loaded yet (the next page of photos, say): out, then in.
        hideSides();
        tx = -by * width; apply(true);
        setTimeout(async () => {
          await stepPhoto(by);
          tx = by * width; apply(false);
          requestAnimationFrame(() => requestAnimationFrame(() => { tx = 0; apply(true); }));
        }, 200);
      }
    } else {
      tx = 0; apply(true);
      placeSides(0, true);
      setTimeout(hideSides, 230);
    }
  };

  stage.addEventListener('pointerdown', (e) => {
    if (!photoShown) return;
    stage.setPointerCapture(e.pointerId);
    pointers.set(e.pointerId, rel(e));
    if (pointers.size === 2) {
      const [a, b] = [...pointers.values()];
      gesture = { kind: 'pinch', d0: Math.hypot(a.x - b.x, a.y - b.y), s0: s, tx0: tx, ty0: ty,
        m0: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 } };
    } else if (pointers.size === 1) {
      const p = rel(e);
      gesture = { kind: s > 1 ? 'pan' : 'swipe', p0: p, tx0: tx, ty0: ty, t0: performance.now(),
        dx: 0, dy: 0, axis: null, moved: false, touch: e.pointerType !== 'mouse' };
    }
  });

  stage.addEventListener('pointermove', (e) => {
    if (!pointers.has(e.pointerId) || !gesture) return;
    pointers.set(e.pointerId, rel(e));
    if (gesture.kind === 'pinch' && pointers.size >= 2) {
      const [a, b] = [...pointers.values()];
      const d = Math.hypot(a.x - b.x, a.y - b.y);
      const m = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
      const next = Math.min(MAX, Math.max(1, (gesture.s0 * d) / gesture.d0));
      // The pinch point follows the fingers as they move, too.
      tx = m.x - ((gesture.m0.x - gesture.tx0) * next) / gesture.s0;
      ty = m.y - ((gesture.m0.y - gesture.ty0) * next) / gesture.s0;
      s = next;
      clamp();
      apply(false);
      return;
    }
    const p = rel(e);
    const dx = p.x - gesture.p0.x;
    const dy = p.y - gesture.p0.y;
    if (!gesture.moved && Math.hypot(dx, dy) < 8) return;
    gesture.moved = true;
    if (gesture.kind === 'pan') {
      tx = gesture.tx0 + dx;
      ty = gesture.ty0 + dy;
      clamp();
      apply(false);
    } else if (gesture.kind === 'swipe' && gesture.touch) {
      if (!gesture.axis) {
        gesture.axis = Math.abs(dx) > Math.abs(dy) ? 'x' : 'y';
        if (gesture.axis === 'x') prepSides();
      }
      gesture.dx = dx;
      gesture.dy = dy;
      if (gesture.axis === 'x') { tx = dx; ty = 0; placeSides(dx, false); }
      else {
        tx = 0; ty = Math.max(0, dy);
        overlay.style.backgroundColor = `rgba(0, 0, 0, ${Math.max(0.3, 1 - ty / 450)})`;
      }
      apply(false);
    }
  });

  const up = (e) => {
    if (!pointers.has(e.pointerId)) return;
    pointers.delete(e.pointerId);
    const g = gesture;
    if (!g) return;
    if (g.kind === 'pinch') {
      // One finger left on the glass carries on as a drag of the zoomed photo.
      if (pointers.size === 1) {
        const p = [...pointers.values()][0];
        gesture = { kind: 'pan', p0: p, tx0: tx, ty0: ty, moved: true };
      } else {
        gesture = null;
        if (s < 1.05) zoomAbout({ x: 0, y: 0 }, 1, true);
      }
      return;
    }
    if (pointers.size) return;
    gesture = null;
    if (!g.moved) { onTap(e); return; }
    if (g.kind === 'swipe' && g.axis) swipeEnd(g);
  };
  stage.addEventListener('pointerup', up);
  stage.addEventListener('pointercancel', up);

  stage.addEventListener('wheel', (e) => {
    if (!photoShown) return;
    e.preventDefault();
    zoomAbout(rel(e), s * Math.exp(-e.deltaY * 0.0022), false);
  }, { passive: false });

  return { reset, zoomed: () => s > 1 };
})();

document.addEventListener('keydown', (event) => {
  if (!photoShown) return;
  if (event.key === 'Escape') closePhoto();
  else if (event.key === 'ArrowLeft' && !photoZoom.zoomed()) stepPhoto(-1);
  else if (event.key === 'ArrowRight' && !photoZoom.zoomed()) stepPhoto(1);
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
  startWatching(item);

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
  // Before the source goes: once it does, currentTime is back to nothing.
  saveWatchPosition(true);
  state.watching = null;
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

/* Where you are in a film or an episode.
 *
 * Kept by SoundStorm, per person, in the same record as a book's place - not
 * in Jellyfin, because the house shares one Jellyfin account and a position
 * there would be everybody's. The location is the time, "t=1234.5"; the
 * fraction is what the Continue row draws. Saved every half minute while
 * playing, on pause and on close - often enough to be where you left it,
 * rarely enough not to rewrite the state file on every frame. */

const WATCH_SAVE_EVERY = 30000;

function watchParams(item) {
  return new URLSearchParams({ source: item.sourceId, id: item.id });
}

// startWatching remembers what is playing and, once the player knows how long
// it is, jumps to where this person stopped last time.
async function startWatching(item) {
  const watching = { item, lastSave: 0 };
  state.watching = watching;
  const { ok, body } = await api(`/api/book/progress?${watchParams(item)}`);
  if (state.watching !== watching || !ok || !body || !body.found) return;
  const match = /^t=([0-9.]+)$/.exec(body.location || '');
  if (!match) return;
  const seconds = Number(match[1]);
  const player = $('video-player');
  const jump = () => {
    if (state.watching !== watching) return;
    const length = player.duration || item.durationSeconds || 0;
    // Not from the very start, and not into the credits: both are a fresh
    // start rather than a place to carry on from.
    if (seconds > 10 && (!length || seconds < length * 0.93)) {
      player.currentTime = seconds;
    }
  };
  if (player.readyState >= 1) jump();
  else player.addEventListener('loadedmetadata', jump, { once: true });
}

async function saveWatchPosition(force) {
  const watching = state.watching;
  const player = $('video-player');
  if (!watching || !player.src && !hls) return;
  const now = Date.now();
  if (!force && now - watching.lastSave < WATCH_SAVE_EVERY) return;
  const seconds = player.currentTime;
  // A progressive transcode reports no length until it has finished, so the
  // backend's own runtime stands in for it.
  const length = Number.isFinite(player.duration) && player.duration > 0
    ? player.duration : (watching.item.durationSeconds || 0);
  if (!(seconds > 5) || !(length > 0)) return;
  watching.lastSave = now;
  await api(`/api/book/progress?${watchParams(watching.item)}`, {
    method: 'PUT',
    body: JSON.stringify({
      location: `t=${seconds.toFixed(1)}`,
      fraction: Math.min(1, Math.max(0, seconds / length)),
    }),
  });
  if (force) setTimeout(refreshContinue, 300);
}

$('video-player').addEventListener('timeupdate', () => saveWatchPosition(false));
$('video-player').addEventListener('pause', () => saveWatchPosition(true));
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

function playAudio(item, fromQueue) {
  audio.counted = false;
  // Anything started by hand ends a playlist; the queue only carries on
  // through its own songs.
  if (!fromQueue) audio.queue = null;
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
  const backdrop = $('dock-backdrop');
  if (src) backdrop.src = src;
  else backdrop.removeAttribute('src');
  if (src) {
    art.src = src;
    art.onerror = () => {
      art.onerror = null;
      offlineArtURL(item).then((u) => { if (audio.item === item) art.src = u || NO_COVER; });
    };
  } else {
    art.onerror = null;
    art.src = NO_COVER;
  }
  show(art, true);

  renderTracks();
  renderQueue();
  showDock(true);
  updateMediaSession();
  renderNowPlaying();

  // A song starts now: a round trip before the first note is felt, and nothing
  // about a four minute track needs the answer. An audiobook waits, because it
  // may be resuming into chapter twelve, and starting chapter one first would
  // play a second of the wrong thing before correcting itself.
  if (item.kind !== 'audiobook') {
    const handoff = takeCrossfade(item);
    const preloaded = handoff ? null : takePreloaded(item);
    if (handoff) {
      startAt(handoff.url, handoff.at);
    } else if (preloaded) {
      startAt(preloaded, 0);
    } else if (isDownloaded(item)) {
      // From the device: through a tunnel, out of Wi-Fi range, on a plane.
      offlineURL(item).then((url) => {
        if (audio.item === item) startAt(url || playPath(item), 0);
      });
    } else {
      startAt(playPath(item), 0);
    }
  }
  applyLevel(item);

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
  startAt(playPath(item), seconds);
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
  // The lock screen shows the chapter, so it changes with it.
  updateMediaSession();
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
$('audio-player').addEventListener('pause', () => {
  savePosition();
  // Give the position a moment to reach Audiobookshelf before asking again.
  setTimeout(refreshContinue, 1500);
});

// The whole point of a track list: chapter 1 ending means chapter 2 starting,
// not silence.
$('audio-player').addEventListener('ended', () => {
  if (sleep.atSongEnd) {
    // The sleep timer said "at the end of this song": it has ended, so stop
    // here rather than going on to the next song or chapter.
    if (audio.index + 1 >= audio.tracks.length) savePosition({ finished: true });
    else savePosition();
    setSleep(null);
    return;
  }
  if (audio.index + 1 < audio.tracks.length) {
    selectTrack(audio.index + 1);
    return;
  }
  // The end of the last file is the end of the book.
  savePosition({ finished: true });
  // And in a queue, whatever comes next - including going round again.
  queueAdvance();
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
  clearMediaSession();
  closeNowPlaying();
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
    // Moving keeps the whole address, setup code included; staying is when
    // it can come out of the address.
    if (body.secureName && await moveToSecureName(body.secureName)) return;
    forgetSetupCodeInAddress();
    if (body.signedIn) showApp(body.user);
    else showGate(body.hasAccount, body.setupCodeRequired);
    return;
  }
  forgetSetupCodeInAddress();

  if (offline && hasDownloads()) {
    showOfflineApp();
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

/* -------------------------------------------------------------- selecting */

// Selecting and deleting, for the owner. The server refuses a member anyway;
// the button is simply not offered to one.
//
// Nothing is deleted at once. Files go to the library's bin for thirty days,
// and the bar that confirmed the delete offers an undo straight after.

state.selecting = false;
state.selected = new Map();

function selectionKey(item) {
  return `${item.sourceId}\n${item.id}`;
}

function setSelecting(on) {
  state.selecting = on;
  // Selecting is for the library below; the Continue row is not part of it.
  if (on) show($('continue'), false);
  else refreshContinue();
  if (!on) state.selected.clear();
  $('results').classList.toggle('selecting', on);
  for (const card of $('results').querySelectorAll('.item.selected')) {
    card.classList.remove('selected');
  }
  $('select-toggle').textContent = on ? 'Done' : 'Select';
  show($('select-bar'), on);
  resetSelectBar();
}

function toggleSelected(item, card) {
  const key = selectionKey(item);
  if (state.selected.has(key)) state.selected.delete(key);
  else state.selected.set(key, item);
  card.classList.toggle('selected', state.selected.has(key));
  resetSelectBar();
}

// resetSelectBar puts the bar back to "N selected" with its ordinary buttons,
// out of the confirm or undo state it may have been in.
function resetSelectBar() {
  const n = state.selected.size;
  $('select-count').textContent = n
    ? `${n} selected`
    : 'Tap the things you want to delete';
  show($('select-all'), true);
  show($('select-cancel'), true);
  const del = $('select-delete');
  del.textContent = 'Delete';
  del.disabled = n === 0;
  state.confirming = false;
}

function formatBytes(n) {
  if (n >= 1e12) return `${(n / 1e12).toFixed(1)} TB`;
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} GB`;
  if (n >= 1e6) return `${Math.round(n / 1e6)} MB`;
  if (n >= 1e3) return `${Math.round(n / 1e3)} KB`;
  return `${n} bytes`;
}

function selectedPayload() {
  return JSON.stringify({
    items: [...state.selected.values()].map((item) => ({
      source: item.sourceId, id: item.id, title: item.title,
    })),
  });
}

$('select-toggle').addEventListener('click', () => setSelecting(!state.selecting));
$('select-cancel').addEventListener('click', () => setSelecting(false));

$('select-all').addEventListener('click', () => {
  for (const item of state.items || []) state.selected.set(selectionKey(item), item);
  for (const card of $('results').querySelectorAll('.item')) card.classList.add('selected');
  resetSelectBar();
});

$('select-delete').addEventListener('click', async () => {
  const del = $('select-delete');
  // The same button is Undo for a few seconds after a delete.
  if (state.undoEntry) {
    await undoDelete();
    return;
  }
  if (!state.confirming) {
    // First press: ask the server exactly what would go, and say it.
    del.disabled = true;
    const { ok, body } = await api('/api/delete/preview', { method: 'POST', body: selectedPayload() });
    del.disabled = false;
    if (!ok || !body) {
      $('select-count').textContent = (body && body.error) || 'Could not work out what that would delete.';
      return;
    }
    const n = body.items;
    $('select-count').textContent =
      `Delete ${n} item${n === 1 ? '' : 's'}? ${body.files} file${body.files === 1 ? '' : 's'}, ` +
      `${formatBytes(body.bytes)}. They stay in the bin for 30 days.`;
    show($('select-all'), false);
    del.textContent = `Delete ${n}`;
    state.confirming = true;
    return;
  }

  // Second press: delete.
  del.disabled = true;
  const deleted = [...state.selected.keys()];
  const { ok, body } = await api('/api/delete', { method: 'POST', body: selectedPayload() });
  del.disabled = false;
  if (!ok || !body) {
    $('select-count').textContent = (body && body.error) || 'Could not delete.';
    state.confirming = false;
    del.textContent = 'Delete';
    return;
  }
  // Gone from the screen now; the backends catch up with a rescan.
  for (const key of deleted) {
    const card = $('results').querySelector(`.item[data-key="${CSS.escape(key)}"]`);
    if (card) (card.closest('.item-holder') || card).remove();
  }
  state.items = (state.items || []).filter((item) => !deleted.includes(selectionKey(item)));
  setSelecting(false);
  offerUndo(body);
});

// offerUndo shows the undo in the same bar, for long enough to notice.
function offerUndo(result) {
  state.undoEntry = result.entry;
  show($('select-bar'), true);
  const n = result.items;
  $('select-count').textContent = `Deleted ${n} item${n === 1 ? '' : 's'}.`;
  show($('select-all'), false);
  show($('select-cancel'), false);
  const del = $('select-delete');
  del.disabled = false;
  del.textContent = 'Undo';
  del.classList.replace('danger', 'ghost');
  clearTimeout(state.undoTimer);
  state.undoTimer = setTimeout(endUndo, 12000);
}

// endUndo puts the bar back to what it was before the undo was offered.
function endUndo() {
  clearTimeout(state.undoTimer);
  state.undoEntry = null;
  $('select-delete').classList.replace('ghost', 'danger');
  show($('select-bar'), state.selecting);
  resetSelectBar();
}

async function undoDelete() {
  const entry = state.undoEntry;
  $('select-delete').disabled = true;
  const { ok, body } = await api('/api/delete/undo', {
    method: 'POST', body: JSON.stringify({ entry }),
  });
  endUndo();
  if (!ok) {
    show($('select-bar'), true);
    $('select-count').textContent = (body && body.error) || 'Could not undo.';
    setTimeout(() => show($('select-bar'), state.selecting), 6000);
    return;
  }
  // Back on disk now; give the shelf a moment to notice before listing it.
  setTimeout(runSearch, 3000);
}

document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && state.selecting) setSelecting(false);
});

/* --------------------------------------------------------------- continue */

// The kinds the Continue row can hold; on any other shelf it stays hidden.
const CONTINUE_KINDS = new Set(['video', 'tv', 'audiobook', 'ebook', 'document']);

// refreshContinue shows what this person is part way through, when they are
// looking at a shelf rather than searching: on Everything, or on the one shelf
// an item belongs to.
async function refreshContinue() {
  const section = $('continue');
  const browsing = !state.query && !state.selecting;
  const kindFits = !state.kind || CONTINUE_KINDS.has(state.kind);
  if (!browsing || !kindFits) {
    show(section, false);
    return;
  }
  const seq = (state.continueSeq = (state.continueSeq || 0) + 1);
  const { ok, body } = await api('/api/continue');
  if (seq !== state.continueSeq) return; // a newer refresh is on its way
  const items = ((ok && body && body.items) || [])
    .filter((item) => !state.kind || item.kind === state.kind);

  const row = $('continue-row');
  row.replaceChildren();
  for (const item of items) {
    const card = renderItem(item);
    card.classList.add('continue-item');
    // How far in, along the bottom of the cover.
    const bar = document.createElement('span');
    bar.className = 'progress';
    const fill = document.createElement('span');
    fill.style.width = `${Math.round(item.progress * 100)}%`;
    bar.append(fill);
    card.querySelector('.art-wrap').append(bar);
    card.title = `${Math.round(item.progress * 100)}% of the way through`;
    row.append(card);
  }
  show(section, items.length > 0 && !state.query && !state.selecting);
}

window.addEventListener('soundstorm:reader-closed', () => setTimeout(refreshContinue, 300));

/* ---------------------------------------------------- favourites, playlists */

// Each person's own, kept by SoundStorm (see internal/collections). The set
// of favourite keys is loaded once and kept current, so a card can show its
// heart and the menu can say "remove" without asking the server per card.
state.favourites = new Set();

async function loadFavouriteKeys() {
  const { ok, body } = await api('/api/favourites');
  if (!ok || !body) return [];
  state.favourites = new Set(body.items.map(selectionKey));
  return body.items;
}

async function showFavourites(seq) {
  $('status').textContent = 'Loading…';
  const items = await loadFavouriteKeys();
  if (seq !== state.searchSeq) return;
  // Typing narrows the list, as it does a shelf.
  const words = state.query.toLowerCase().split(/\s+/).filter(Boolean);
  const shown = items.filter((item) => {
    const text = [item.title, item.subtitle, ...(item.creators || [])].join(' ').toLowerCase();
    return words.every((w) => text.includes(w));
  });
  state.hasMore = false;
  const grid = $('results');
  grid.replaceChildren(...shown.map(renderItem));
  state.items = shown;
  $('status').textContent = items.length
    ? `${shown.length} favourite${shown.length === 1 ? '' : 's'}`
    : `Nothing here yet. ${MENU_HOW} anything and choose Add to favourites.`;
  show($('loading-more'), false);
}

async function setFavourite(item, on) {
  const q = new URLSearchParams({ source: item.sourceId, id: item.id });
  const { ok, body } = await api(`/api/favourites?${q}`, { method: on ? 'PUT' : 'DELETE' });
  if (!ok) return (body && body.error) || 'Could not change that.';
  const key = selectionKey(item);
  if (on) state.favourites.add(key);
  else state.favourites.delete(key);
  for (const card of document.querySelectorAll(`.item[data-key="${CSS.escape(key)}"]`)) {
    card.querySelector('.art-wrap').classList.toggle('is-favourite', on);
  }
  // Taken off the list while looking at the list: it goes.
  if (!on && state.kind === 'favourites') runSearch();
  return '';
}

// --- the "..." menu -----------------------------------------------------------

// Icons are drawn as SVG rather than typed as characters: "\u22EF" came out
// as three dashes in the UI font, and a heart glyph sits on a different
// baseline in every font that has one.
const ICONS = {
  more: '<circle cx="5" cy="12" r="2"/><circle cx="12" cy="12" r="2"/><circle cx="19" cy="12" r="2"/>',
  heart: '<path d="M12 20.5s-7.5-4.6-9.2-9.1C1.6 8.2 3.6 5 6.9 5c2 0 3.6 1.1 5.1 3 1.5-1.9 3.1-3 5.1-3 3.3 0 5.3 3.2 4.1 6.4C19.5 15.9 12 20.5 12 20.5z"/>',
  playlist: '<path d="M4 6h11M4 11h11M4 16h7M17 14v6M14 17h6"/>',
  chevron: '<path d="M9 6l6 6-6 6"/>',
  back: '<path d="M15 6l-6 6 6 6"/>',
  forward: '<path d="M9 6l6 6-6 6"/>',
  home: '<path d="M4 11l8-7 8 7M6 9.5V20h4.5v-6h3v6H18V9.5"/>',
  note: '<path d="M9 18V6l10-2v12M9 18a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0zM19 16a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0z"/>',
  film: '<path d="M4 6h16v12H4zM4 10h16M8 6l-1.5 4M13 6l-1.5 4M18 6l-1.5 4"/>',
  book: '<path d="M5 5.5A2.5 2.5 0 0 1 7.5 3H19v15H7.5A2.5 2.5 0 0 0 5 20.5zM5 20.5A2.5 2.5 0 0 1 7.5 18H19v3H7.5"/>',
  photo: '<path d="M4 6h16v12H4zM4 15l4.5-4.5 4 4 2.5-2.5L20 17M15.5 9.5h.01"/>',
  gear: '<path d="M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3h.1a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8v.1a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  next: '<path d="M4 7h10M4 12h10M4 17h6M16 14l5 3-5 3z"/>',
  queue: '<path d="M4 7h16M4 12h16M4 17h10M18 15v6M15 18h6"/>',
  play: '<path d="M8 5.5v13l11-6.5z"/>',
  pause: '<path d="M7 5h3.5v14H7zM13.5 5H17v14h-3.5z"/>',
  prev: '<path d="M6 5v14M19 5.5v13L9 12z"/>',
  skip: '<path d="M18 5v14M5 5.5v13L15 12z"/>',
  shuffle: '<path d="M3 7h3c4 0 6 10 10 10h5M17 14l3 3-3 3M3 17h3c1.6 0 2.8-1.6 3.9-3.5M13.5 9.2C14.5 8 15.3 7 16 7h5M17 4l3 3-3 3"/>',
  repeat: '<path d="M4 11V9a3 3 0 0 1 3-3h12M16 3l3 3-3 3M20 13v2a3 3 0 0 1-3 3H5M8 21l-3-3 3-3"/>',
  down: '<path d="M6 9l6 6 6-6"/>',
  lyrics: '<path d="M4 6h16M4 10h16M4 14h10M4 18h7M17 21a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM19.5 18.5V11"/>',
  close: '<path d="M6 6l12 12M18 6L6 18"/>',
  moon: '<path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"/>',
  grip: '<path d="M9 6h.01M15 6h.01M9 12h.01M15 12h.01M9 18h.01M15 18h.01" stroke-width="3.2"/>',
  volume: '<path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4zM15.5 9a4 4 0 0 1 0 6M18 6.5a7.5 7.5 0 0 1 0 11"/>',
  download: '<path d="M12 4v11M7 10l5 5 5-5M5 20h14"/>',
};

function icon(name, filled) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  svg.classList.add('icon', `icon-${name}`);
  if (filled) svg.classList.add('filled');
  // Markup is ours and fixed, never data: parsed from the constant above.
  const doc = new DOMParser().parseFromString(
    `<svg xmlns="http://www.w3.org/2000/svg">${ICONS[name]}</svg>`, 'image/svg+xml');
  for (const child of [...doc.documentElement.childNodes]) svg.append(document.importNode(child, true));
  return svg;
}

function menuItem(iconName, label, action, opts = {}) {
  const b = document.createElement('button');
  b.type = 'button';
  b.setAttribute('role', 'menuitem');
  b.className = 'menu-item';
  if (opts.className) b.classList.add(opts.className);
  b.append(icon(iconName, opts.filled));
  const text = document.createElement('span');
  text.className = 'menu-label';
  text.textContent = label;
  b.append(text);
  if (opts.detail) {
    const detail = document.createElement('span');
    detail.className = 'menu-detail';
    detail.textContent = opts.detail;
    b.append(detail);
  }
  if (opts.chevron) b.append(icon('chevron'));
  b.addEventListener('click', action);
  return b;
}

function menuHeader(item) {
  const head = document.createElement('div');
  head.className = 'menu-head';
  const title = document.createElement('strong');
  title.textContent = item.title;
  const sub = document.createElement('span');
  sub.textContent = subtitleFor(item);
  head.append(title, sub);
  return head;
}

function closeItemMenu() {
  show($('item-menu'), false);
  show($('item-menu-backdrop'), false);
  for (const lifted of document.querySelectorAll('.item-holder.lifted')) lifted.classList.remove('lifted');
  state.menuFor = null;
}

function openItemMenu(item, anchor) {
  const menu = $('item-menu');
  if (state.menuFor === item && !menu.classList.contains('hidden')) {
    closeItemMenu();
    return;
  }
  state.menuFor = item;
  state.menuAnchor = anchor;
  renderMainMenu(item);
  placeMenu(menu, anchor);
}

function menuNote() {
  const note = document.createElement('p');
  note.className = 'menu-note hidden';
  return note;
}

function renderMainMenu(item) {
  const menu = $('item-menu');
  const note = menuNote();
  const say = (text) => { note.textContent = text; show(note, Boolean(text)); };
  const faved = state.favourites.has(selectionKey(item));
  const entries = [
    menuHeader(item),
    menuItem('heart', faved ? 'Remove from favourites' : 'Add to favourites', async () => {
      const problem = await setFavourite(item, !faved);
      if (problem) say(problem);
      else closeItemMenu();
    }, { filled: faved, className: 'menu-favourite' }),
  ];
  if (item.kind === 'music') {
    entries.push(menuItem('next', 'Play next', () => {
      queuePlayNext(item);
      closeItemMenu();
    }));
    entries.push(menuItem('queue', 'Add to queue', () => {
      queueAdd(item);
      closeItemMenu();
    }));
    const downloaded = isDownloaded(item);
    entries.push(menuItem('download', downloaded ? 'Remove download' : 'Download', async () => {
      closeItemMenu();
      if (downloaded) await removeDownload(`song:${selectionKey(item)}`);
      else await download({ id: `song:${selectionKey(item)}`, type: 'song', title: item.title,
        subtitle: (item.creators || []).join(', '), sourceId: item.sourceId, artId: item.artId }, [item]);
    }));
    entries.push(menuItem('playlist', 'Add to playlist', async () => {
      const { ok, body } = await api('/api/playlists');
      renderPlaylistMenu(item, (ok && body && body.playlists) || []);
    }, { chevron: true }));
  }
  menu.replaceChildren(...entries, note);
}

// renderPlaylistMenu is the second page: which playlist, or a new one.
function renderPlaylistMenu(item, lists) {
  const menu = $('item-menu');
  const note = menuNote();
  const say = (text) => { note.textContent = text; show(note, Boolean(text)); };

  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const backLabel = document.createElement('span');
  backLabel.textContent = 'Add to playlist';
  back.append(backLabel);
  back.addEventListener('click', () => renderMainMenu(item));

  const add = async (id) => {
    const { ok, body } = await api(`/api/playlists/${encodeURIComponent(id)}/items`, {
      method: 'POST', body: JSON.stringify({ source: item.sourceId, id: item.id }),
    });
    if (!ok) say((body && body.error) || 'Could not add it.');
    else closeItemMenu();
  };

  const rows = lists.map((list) => menuItem('playlist', list.name, () => add(list.id),
    { detail: `${list.count}` }));

  const form = document.createElement('form');
  form.className = 'menu-new';
  const input = document.createElement('input');
  input.placeholder = lists.length ? 'New playlist' : 'Name your first playlist';
  input.maxLength = 100;
  input.setAttribute('aria-label', 'New playlist name');
  const create = document.createElement('button');
  create.type = 'submit';
  create.className = 'menu-create';
  create.setAttribute('aria-label', 'Create playlist');
  create.append(icon('plus'));
  form.append(input, create);
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const { ok, body } = await api('/api/playlists', {
      method: 'POST', body: JSON.stringify({ name: input.value }),
    });
    if (!ok || !body) {
      say((body && body.error) || 'Could not make it.');
      return;
    }
    await add(body.id);
  });

  const list = document.createElement('div');
  list.className = 'menu-scroll';
  list.append(...rows);
  menu.replaceChildren(back, list, form, note);
  // A keyboard lands in the box; a phone does not pop its keyboard over the
  // list somebody is about to tap.
  if (!state.sheetMenus) setTimeout(() => input.focus(), 0);
}

// On a touch screen the menu is a sheet from the bottom of the screen, where a
// thumb is; with a mouse it opens beside the button that asked.
state.sheetMenus = Boolean(window.matchMedia && window.matchMedia('(pointer: coarse)').matches);

function placeMenu(menu, anchor) {
  // On a touch screen the page dims and the card lifts above it, so it is
  // plain which item the menu belongs to.
  show($('item-menu-backdrop'), state.sheetMenus);
  for (const lifted of document.querySelectorAll('.item-holder.lifted')) lifted.classList.remove('lifted');
  const holder = anchor.closest('.item-holder');
  if (holder && state.sheetMenus) holder.classList.add('lifted');
  show(menu, true);
  const box = anchor.getBoundingClientRect();
  const width = menu.offsetWidth;
  const height = menu.offsetHeight;
  const left = Math.min(Math.max(8, box.right - width), window.innerWidth - width - 8);
  // Below the button, or above it when there is no room below.
  const below = box.bottom + 6;
  const top = below + height > window.innerHeight - 8 ? Math.max(8, box.top - height - 6) : below;
  menu.style.left = `${left + window.scrollX}px`;
  menu.style.top = `${top + window.scrollY}px`;
}

document.addEventListener('click', (event) => {
  const menu = $('item-menu');
  if (!menu.classList.contains('hidden') && !menu.contains(event.target)
      && !event.target.closest('.item-more')) closeItemMenu();
});
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') closeItemMenu();
});
window.addEventListener('resize', () => {
  if (!$('item-menu').classList.contains('hidden') && state.menuAnchor) {
    placeMenu($('item-menu'), state.menuAnchor);
  }
});

// --- playlists ----------------------------------------------------------------

// The search box says what it will search: the shelf on screen, and under
// Music the tab - albums and artists are searched as albums and artists.
function renderSearchHint() {
  const shelves = {
    '': 'everything', music: 'music', video: 'films', tv: 'TV', audiobook: 'audiobooks',
    ebook: 'ebooks', document: 'documents', picture: 'pictures',
    favourites: 'your favourites', playlists: 'your playlists',
  };
  let what = shelves[state.kind] || 'everything';
  if (state.kind === 'music' && ['songs', 'albums', 'artists'].includes(state.musicView)) what = state.musicView;
  const hint = `Search ${what}\u2026`;
  $('search-input').placeholder = hint;
  $('search-input').setAttribute('aria-label', hint.slice(0, -1));
}

async function showPlaylists() {
  const view = $('playlists-view');
  const { ok, body } = await api('/api/playlists');
  const all = (ok && body && body.playlists) || [];
  // Typing narrows the playlists by name, as it narrows every other page.
  const words = (state.query || '').toLowerCase().split(/\s+/).filter(Boolean);
  const lists = all.filter((list) => words.every((w) => list.name.toLowerCase().includes(w)));
  view.replaceChildren();

  const title = document.createElement('h2');
  title.textContent = 'Playlists';
  view.append(title);

  if (!lists.length) {
    const empty = document.createElement('p');
    empty.className = 'muted';
    empty.textContent = all.length
      ? 'No playlists match.'
      : `No playlists yet. ${MENU_HOW} any song and choose Add to playlist.`;
    view.append(empty);
    return;
  }
  const ul = document.createElement('ul');
  ul.className = 'playlist-list';
  for (const list of lists) {
    const li = document.createElement('li');
    const open = document.createElement('button');
    open.type = 'button';
    open.className = 'playlist-open';
    open.textContent = list.name;
    open.addEventListener('click', () => showPlaylist(list.id));
    const count = document.createElement('span');
    count.className = 'muted';
    count.textContent = `${list.count} song${list.count === 1 ? '' : 's'}`;
    const play = document.createElement('button');
    play.type = 'button';
    play.className = 'small';
    play.textContent = 'Play';
    play.disabled = !list.count;
    play.addEventListener('click', async () => {
      const got = await api(`/api/playlists/${encodeURIComponent(list.id)}`);
      if (got.ok && got.body) playQueue(got.body.items, 0);
    });
    li.append(open, count, play);
    ul.append(li);
  }
  view.append(ul);
}

async function showPlaylist(id) {
  const view = $('playlists-view');
  const { ok, body } = await api(`/api/playlists/${encodeURIComponent(id)}`);
  if (!ok || !body) {
    showPlaylists();
    return;
  }
  const path = `/api/playlists/${encodeURIComponent(id)}`;
  const songs = body.items;
  view.replaceChildren();

  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'back';
  back.textContent = '\u2190 All playlists';
  back.addEventListener('click', showPlaylists);

  // The header is an album's: a cover, the name, what is in it, and Play,
  // Shuffle and Download. The name renames in place.
  const head = document.createElement('div');
  head.className = 'album-head';
  const first = songs[0] || {};
  const cover = coverArt(first.artId ? artUrl(first.sourceId, first.artId) : '', body.name);
  cover.classList.add('album-cover');
  const text = document.createElement('div');
  text.className = 'album-text';
  const kind = document.createElement('span');
  kind.className = 'album-kind';
  kind.textContent = 'Playlist';
  const title = document.createElement('button');
  title.type = 'button';
  title.className = 'playlist-title';
  title.textContent = body.name;
  title.title = 'Rename';
  title.addEventListener('click', () => renamePlaylistInPlace(title, path, body.name, id));
  const total = songs.reduce((sum, song) => sum + (song.durationSeconds || 0), 0);
  const facts = document.createElement('span');
  facts.className = 'muted';
  facts.textContent = [`${songs.length} song${songs.length === 1 ? '' : 's'}`, formatLength(total)].filter(Boolean).join(' \u00b7 ');
  const buttons = playButtons(async () => songs);
  buttons.append(downloadButton({
    id: `playlist:${id}`, type: 'playlist', title: body.name, subtitle: 'Playlist',
    sourceId: first.sourceId, artId: first.artId,
  }, songs));
  const remove = document.createElement('button');
  remove.type = 'button';
  remove.className = 'ghost small playlist-delete';
  remove.textContent = 'Delete playlist';
  // Two taps, not a browser dialog: the first asks, the second deletes.
  remove.addEventListener('click', async () => {
    if (!remove.classList.contains('confirming')) {
      remove.classList.add('confirming');
      remove.textContent = 'Tap again to delete';
      setTimeout(() => { remove.classList.remove('confirming'); remove.textContent = 'Delete playlist'; }, 4000);
      return;
    }
    await api(path, { method: 'DELETE' });
    showPlaylists();
  });
  text.append(kind, title, facts, buttons, remove);
  head.append(cover, text);
  view.append(back, head);

  if (!songs.length) {
    const empty = document.createElement('p');
    empty.className = 'muted';
    empty.textContent = `Nothing in it yet. ${MENU_HOW} any song and choose Add to playlist.`;
    view.append(empty);
    return;
  }

  const list = document.createElement('ol');
  list.className = 'playlist-rows';
  songs.forEach((song, i) => {
    const li = document.createElement('li');
    li.className = 'playlist-row';
    li.dataset.index = String(i);

    const play = document.createElement('button');
    play.type = 'button';
    play.className = 'playlist-play';
    const thumb = document.createElement('img');
    thumb.alt = '';
    thumb.loading = 'lazy';
    thumb.src = song.artId ? artUrl(song.sourceId, song.artId) : NO_COVER;
    thumb.addEventListener('error', () => { thumb.src = NO_COVER; }, { once: true });
    const words = document.createElement('span');
    words.className = 'playlist-words';
    const t = document.createElement('strong');
    t.textContent = song.title;
    const sub = document.createElement('span');
    sub.textContent = subtitleFor(song);
    words.append(t, sub);
    const length = document.createElement('span');
    length.className = 'track-length';
    length.textContent = formatDuration(song.durationSeconds);
    play.append(thumb, words, length);
    play.addEventListener('click', () => playQueue(songs, i));

    const drop = document.createElement('button');
    drop.type = 'button';
    drop.className = 'np-icon playlist-remove';
    drop.setAttribute('aria-label', `Remove ${song.title}`);
    drop.append(icon('close'));
    drop.addEventListener('click', async () => {
      await api(`${path}/items/${song.position}`, { method: 'DELETE' });
      showPlaylist(id);
      showToast(`Removed \u201c${song.title}\u201d`, 'Undo', async () => {
        // Back where it was: added at the end, then moved into its place.
        const added = await api(`${path}/items`, { method: 'POST', body: JSON.stringify({ source: song.sourceId, id: song.id }) });
        // The answer is the new length; the song went on the end.
        const at = added.ok && added.body && typeof added.body.count === 'number' ? added.body.count - 1 : null;
        if (at !== null && at !== song.position) {
          await api(`${path}/move`, { method: 'POST', body: JSON.stringify({ from: at, to: song.position }) });
        }
        showPlaylist(id);
      });
    });

    const handle = document.createElement('button');
    handle.type = 'button';
    handle.className = 'np-icon playlist-handle';
    handle.setAttribute('aria-label', `Move ${song.title}`);
    handle.append(icon('grip'));
    attachReorder(handle, li, list, async (from, to) => {
      await api(`${path}/move`, { method: 'POST', body: JSON.stringify({ from: songs[from].position, to: songs[to].position }) });
      showPlaylist(id);
    });

    li.append(play, drop, handle);
    list.append(li);
  });
  view.append(list);
}

// Renaming happens where the name is: it becomes a text box, Enter or leaving
// it saves, Escape puts it back.
function renamePlaylistInPlace(title, path, current, id) {
  const input = document.createElement('input');
  input.type = 'text';
  input.className = 'playlist-title-input';
  input.value = current;
  input.maxLength = 100;
  input.setAttribute('aria-label', 'Playlist name');
  title.replaceWith(input);
  input.focus();
  input.select();
  let done = false;
  const finish = async (save) => {
    if (done) return;
    done = true;
    const name = input.value.trim();
    if (save && name && name !== current) {
      await api(path, { method: 'PATCH', body: JSON.stringify({ name }) });
    }
    showPlaylist(id);
  };
  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') finish(true);
    if (event.key === 'Escape') finish(false);
  });
  input.addEventListener('blur', () => finish(true));
}

// Drag to reorder, by the handle, with a finger or a mouse. The row follows
// the pointer, the others step aside as it passes their middle, and letting
// go saves the new place. Keyboard: the handle's arrow keys move it by one.
function attachReorder(handle, row, list, save) {
  handle.addEventListener('pointerdown', (event) => {
    if (event.button !== 0) return;
    event.preventDefault();
    handle.setPointerCapture(event.pointerId);
    const rows = [...list.children];
    const from = rows.indexOf(row);
    const startY = event.clientY;
    const height = row.getBoundingClientRect().height + parseFloat(getComputedStyle(list).rowGap || '0');
    let to = from;
    row.classList.add('dragging');
    const move = (e) => {
      const dy = e.clientY - startY;
      row.style.transform = `translateY(${dy}px)`;
      to = Math.max(0, Math.min(rows.length - 1, from + Math.round(dy / height)));
      rows.forEach((other, i) => {
        if (other === row) return;
        let shift = 0;
        if (from < to && i > from && i <= to) shift = -height;
        if (from > to && i < from && i >= to) shift = height;
        other.style.transform = shift ? `translateY(${shift}px)` : '';
      });
    };
    const end = () => {
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', end);
      handle.removeEventListener('pointercancel', end);
      rows.forEach((other) => { other.style.transform = ''; });
      row.classList.remove('dragging');
      if (to !== from) save(from, to);
    };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', end);
    handle.addEventListener('pointercancel', end);
  });
  handle.addEventListener('keydown', (event) => {
    const from = [...list.children].indexOf(row);
    const to = event.key === 'ArrowUp' ? from - 1 : event.key === 'ArrowDown' ? from + 1 : from;
    if (to === from || to < 0 || to >= list.children.length) return;
    event.preventDefault();
    save(from, to);
  });
}

// A short message at the bottom of the screen, with one action - Undo.
function showToast(message, actionLabel, action, ms = 6000) {
  const toast = $('toast');
  clearTimeout(state.toastTimer);
  $('toast-text').textContent = message;
  const button = $('toast-action');
  button.textContent = actionLabel || '';
  show(button, Boolean(action));
  button.onclick = async () => {
    show(toast, false);
    if (action) await action();
  };
  show(toast, true);
  state.toastTimer = setTimeout(() => show(toast, false), ms);
}

// --- the queue ------------------------------------------------------------------

function playQueue(items, start) {
  if (!items.length) return;
  audio.queue = { items: items.slice(), index: 0, original: null };
  playQueueAt(start);
  // Starting music by hand opens the full player, as a music app does. The
  // queue moving on by itself (playQueueAt) leaves the screen alone.
  if (items[start] && items[start].kind === 'music') openNowPlaying();
  // Shuffle stays on between queues, as it does in any music player.
  if (audio.shuffle) {
    audio.queue.original = audio.queue.items.slice();
    const q = audio.queue;
    q.items = q.items.slice(0, q.index + 1).concat(shuffled(q.items.slice(q.index + 1)));
    queueChanged();
  }
}

function playQueueAt(index) {
  if (!audio.queue) return;
  audio.queue.index = index;
  playAudio(audio.queue.items[index], true);
}

function renderQueue() {
  const q = audio.queue;
  show($('audio-queue'), Boolean(q));
  if (!q) return;
  $('audio-queue-pos').textContent = `${q.index + 1} of ${q.items.length}`;
  $('audio-prev').disabled = q.index === 0;
  $('audio-next').disabled = q.index + 1 >= q.items.length;
}

$('audio-prev').addEventListener('click', () => {
  if (audio.queue && audio.queue.index > 0) playQueueAt(audio.queue.index - 1);
});
$('audio-next').addEventListener('click', () => {
  if (audio.queue && audio.queue.index + 1 < audio.queue.items.length) playQueueAt(audio.queue.index + 1);
});

/* ---------------------------------------------------------- press and hold */

// How to reach an item's menu, in the words for this device.
const MENU_HOW = state.sheetMenus ? 'Hold down on' : 'Right-click';

// attachItemMenuGestures opens a card's menu without a button for it: hold
// down on a touch screen, right-click with a mouse, the menu key on a
// keyboard. The menu opens beside the card.
//
// A hold is a finger resting in place: it is cancelled the moment the finger
// moves more than a few pixels (that is a scroll), lifts early (that is a
// tap), or the browser takes the touch over for itself.
const HOLD_MS = 450;
const HOLD_SLOP = 10;

function attachItemMenuGestures(card, item) {
  let timer = null;
  let startX = 0;
  let startY = 0;
  const cancel = () => {
    clearTimeout(timer);
    timer = null;
    card.classList.remove('pressing');
  };
  card.addEventListener('pointerdown', (event) => {
    if (event.pointerType === 'mouse' || state.selecting) return;
    startX = event.clientX;
    startY = event.clientY;
    card.classList.add('pressing');
    timer = setTimeout(() => {
      timer = null;
      card.classList.remove('pressing');
      state.suppressClick = true;
      if (navigator.vibrate) navigator.vibrate(12);
      openItemMenu(item, card.querySelector('.art-wrap') || card);
    }, HOLD_MS);
  });
  card.addEventListener('pointermove', (event) => {
    if (timer && Math.hypot(event.clientX - startX, event.clientY - startY) > HOLD_SLOP) cancel();
  });
  card.addEventListener('pointerup', cancel);
  card.addEventListener('pointercancel', cancel);
  card.addEventListener('pointerleave', cancel);
  // Right-click, and the long press Android also reports as a context menu:
  // either way it is ours, not the browser's "open in new tab".
  card.addEventListener('contextmenu', (event) => {
    event.preventDefault();
    if (state.selecting) return;
    cancel();
    if (!state.menuFor) openItemMenu(item, card.querySelector('.art-wrap') || card);
  });
  card.addEventListener('keydown', (event) => {
    if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
      event.preventDefault();
      openItemMenu(item, card.querySelector('.art-wrap') || card);
    }
  });
}

// A hidden gesture has to be told once. Only on a touch screen - a mouse has
// the "..." on hover - and only until it has been seen.
function maybeShowHoldTip() {
  if (!state.sheetMenus) return;
  try {
    if (localStorage.getItem('soundstorm-hold-tip')) return;
    localStorage.setItem('soundstorm-hold-tip', '1');
  } catch {
    return;
  }
  const tip = $('hold-tip');
  show(tip, true);
  setTimeout(() => show(tip, false), 7000);
}

/* ------------------------------------------------ lock screen and headphones */

// The Media Session API is how a web page tells the phone what is playing -
// the lock screen, the notification shade, a car stereo over Bluetooth, a
// smartwatch - and how their buttons reach the player. Without it a song
// playing from SoundStorm shows up as "a tab is playing audio" with no cover,
// no title and a skip button that does nothing.
//
// Next and previous mean the next song in a playlist, or the next chapter of an
// audiobook made of several files. Where there is neither, the lock screen
// offers a skip of fifteen seconds instead, which for a single long file is
// what somebody pressing it wants.

const hasMediaSession = 'mediaSession' in navigator;
const SKIP_SECONDS = 15;

function mediaArtwork(item) {
  const path = artPath(item);
  if (!path) return [];
  // Absolute, because the lock screen fetches it outside the page. Same origin
  // and cookie-authenticated like every other image here.
  const src = new URL(path, location.href).href;
  return [
    { src, sizes: '256x256' },
    { src, sizes: '512x512' },
  ];
}

function updateMediaSession() {
  if (!hasMediaSession || !audio.item) return;
  const item = audio.item;
  const track = audio.tracks.length > 1 ? audio.tracks[audio.index] : null;
  try {
    navigator.mediaSession.metadata = new MediaMetadata({
      // For a chaptered audiobook the chapter is the title and the book is
      // the album, which is how a lock screen reads best.
      title: track ? (track.title || item.title) : item.title,
      artist: (item.creators || []).join(', ') || item.subtitle || '',
      album: track ? item.title : ((item.extra && item.extra.album) || item.subtitle || ''),
      artwork: mediaArtwork(item),
    });
  } catch {
    // An old browser with half of the API: the player still works.
  }
  const hasNext = (audio.queue && audio.queue.index + 1 < audio.queue.items.length)
    || audio.index + 1 < audio.tracks.length;
  const hasPrev = (audio.queue && audio.queue.index > 0) || audio.index > 0;
  setMediaAction('nexttrack', hasNext ? mediaNext : null);
  setMediaAction('previoustrack', hasPrev ? mediaPrevious : null);
}

function clearMediaSession() {
  if (!hasMediaSession) return;
  navigator.mediaSession.metadata = null;
  navigator.mediaSession.playbackState = 'none';
}

// setMediaAction sets or clears one handler. Browsers throw for an action they
// do not support, and one unsupported action must not stop the rest.
function setMediaAction(action, handler) {
  try {
    navigator.mediaSession.setActionHandler(action, handler);
  } catch {
    // not supported here
  }
}

function mediaNext() {
  if (audio.queue && audio.queue.index + 1 < audio.queue.items.length) {
    playQueueAt(audio.queue.index + 1);
  } else if (audio.index + 1 < audio.tracks.length) {
    selectTrack(audio.index + 1);
    updateMediaSession();
  }
}

function mediaPrevious() {
  const player = $('audio-player');
  // As every music player does: a few seconds in, "previous" means the start
  // of this song; only right at the start does it mean the one before.
  if (player.currentTime > 3) {
    player.currentTime = 0;
    return;
  }
  if (audio.queue && audio.queue.index > 0) {
    playQueueAt(audio.queue.index - 1);
  } else if (audio.index > 0) {
    selectTrack(audio.index - 1);
    updateMediaSession();
  } else {
    player.currentTime = 0;
  }
}

if (hasMediaSession) {
  const player = $('audio-player');
  setMediaAction('play', () => player.play().catch(() => {}));
  setMediaAction('pause', () => player.pause());
  setMediaAction('stop', () => stopAudio());
  setMediaAction('seekbackward', (details) => {
    player.currentTime = Math.max(0, player.currentTime - ((details && details.seekOffset) || SKIP_SECONDS));
  });
  setMediaAction('seekforward', (details) => {
    const end = Number.isFinite(player.duration) ? player.duration : Infinity;
    player.currentTime = Math.min(end, player.currentTime + ((details && details.seekOffset) || SKIP_SECONDS));
  });
  setMediaAction('seekto', (details) => {
    if (details && Number.isFinite(details.seekTime)) player.currentTime = details.seekTime;
  });

  // The lock screen's own progress bar, kept in step with the player's.
  const syncPosition = () => {
    if (!audio.item || !Number.isFinite(player.duration) || player.duration <= 0) return;
    try {
      navigator.mediaSession.setPositionState({
        duration: player.duration,
        playbackRate: player.playbackRate || 1,
        position: Math.min(player.currentTime, player.duration),
      });
    } catch {
      // A position past a duration the browser disagrees with: skip this one.
    }
  };
  player.addEventListener('play', () => { navigator.mediaSession.playbackState = 'playing'; syncPosition(); });
  player.addEventListener('pause', () => { navigator.mediaSession.playbackState = 'paused'; syncPosition(); });
  player.addEventListener('loadedmetadata', () => { syncPosition(); updateMediaSession(); });
  player.addEventListener('seeked', syncPosition);
  player.addEventListener('ratechange', syncPosition);
}

/* ---------------------------------------------------------- albums, artists */

// Under the Music chip, three ways in: songs (the search grid), albums and
// artists. Album and artist pages open in place and "back" returns to the grid
// they came from.
// Mixes first: what a music app opens on is something to play, not a list.
state.musicView = 'mixes';
state.albumOrder = 'name';

for (const tab of document.querySelectorAll('#music-tabs [data-view]')) {
  tab.addEventListener('click', () => {
    // Playlists sit with the music now, though they are a shelf of their own.
    if (tab.dataset.view === 'playlists') {
      selectKind('playlists');
      markMusicTabs();
      return;
    }
    state.musicView = tab.dataset.view;
    if (state.kind !== 'music') selectKind('music');
    else runSearch();
  });
}
$('album-order').addEventListener('change', (event) => {
  state.albumOrder = event.target.value;
  runSearch();
});

function markMusicTabs() {
  show($('downloads-tab'), hasDownloads());
  for (const tab of document.querySelectorAll('#music-tabs [data-view]')) {
    const on = state.kind === 'playlists' ? tab.dataset.view === 'playlists' : tab.dataset.view === state.musicView;
    tab.classList.toggle('active', on);
    tab.setAttribute('aria-selected', String(on));
    // A swipe can land on a pill that is off the side of its strip.
    if (on) {
      const row = $('music-tabs');
      const left = tab.offsetLeft - row.offsetLeft;
      if (left < row.scrollLeft || left + tab.offsetWidth > row.scrollLeft + row.clientWidth) {
        row.scrollTo({ left: left - 16, behavior: 'smooth' });
      }
    }
  }
}

function artUrl(sourceId, artId) {
  return artId ? `/api/art/${encodeURIComponent(sourceId)}/${escapeId(artId)}` : '';
}

function formatLength(seconds) {
  if (!seconds) return '';
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  return `${Math.floor(minutes / 60)} hr ${minutes % 60} min`;
}

async function showMusicView(seq) {
  markMusicTabs();
  const view = $('music-view');
  $('status').textContent = 'Loading…';
  const q = state.query ? `q=${encodeURIComponent(state.query)}` : '';
  if (state.musicView === 'downloads') {
    view.replaceChildren(await downloadsView());
    $('status').textContent = '';
    return;
  }
  if (state.musicView === 'mixes') {
    const { ok, body } = await api('/api/music/mixes');
    if (seq !== state.searchSeq) return;
    const mixes = (ok && body && body.mixes) || [];
    view.replaceChildren(mixGrid(mixes));
    $('status').textContent = mixes.length ? '' : 'No music yet.';
    return;
  }
  if (state.musicView === 'albums') {
    const params = q || `order=${encodeURIComponent(state.albumOrder)}`;
    const { ok, body } = await api(`/api/music/albums?${params}`);
    if (seq !== state.searchSeq) return;
    const albums = (ok && body && body.albums) || [];
    view.replaceChildren(albumGrid(albums));
    $('status').textContent = albums.length
      ? `${albums.length} album${albums.length === 1 ? '' : 's'}`
      : (state.query ? 'No albums match.' : 'No albums yet.');
    return;
  }
  const { ok, body } = await api(`/api/music/artists?${q}`);
  if (seq !== state.searchSeq) return;
  const artists = (ok && body && body.artists) || [];
  view.replaceChildren(artistGrid(artists));
  $('status').textContent = artists.length
    ? `${artists.length} artist${artists.length === 1 ? '' : 's'}`
    : (state.query ? 'No artists match.' : 'No artists yet.');
}

function coverArt(src, label, round) {
  const wrap = document.createElement('div');
  wrap.className = `art-wrap${round ? ' round' : ''}`;
  if (src) {
    const img = document.createElement('img');
    img.src = src;
    img.alt = '';
    img.loading = 'lazy';
    img.addEventListener('error', () => img.replaceWith(round ? initials(label) : noCover()));
    wrap.append(img);
  } else {
    wrap.append(round ? initials(label) : noCover());
  }
  return wrap;
}

// An album with no cover shows the cloud; an artist with no picture keeps
// their initials, since a face is not album art.
function noCover() {
  const img = document.createElement('img');
  img.src = NO_COVER;
  img.alt = '';
  img.className = 'no-cover';
  return img;
}

// initials stand in for a missing cover: the name's first letters on a colour
// taken from the name, so the same album is always the same colour.
function initials(label) {
  const span = document.createElement('span');
  span.className = 'initials';
  span.textContent = (label || '?').split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]).join('').toUpperCase();
  let hash = 0;
  for (const ch of label || '') hash = (hash * 31 + ch.charCodeAt(0)) % 360;
  span.style.background = `hsl(${hash} 35% 28%)`;
  return span;
}

function albumCard(album) {
  const card = document.createElement('button');
  card.type = 'button';
  card.className = 'item album-card';
  const meta = document.createElement('div');
  meta.className = 'meta';
  const title = document.createElement('span');
  title.className = 'title';
  title.textContent = album.title;
  title.title = album.title;
  const sub = document.createElement('span');
  sub.className = 'sub';
  sub.textContent = [album.artist, album.year].filter(Boolean).join(' · ');
  meta.append(title, sub);
  card.append(coverArt(artUrl(album.sourceId, album.artId), album.title), meta);
  card.addEventListener('click', () => showAlbum(album.sourceId, album.id));
  return card;
}

function albumGrid(albums) {
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid';
  grid.append(...albums.map(albumCard));
  return grid;
}

function artistGrid(artists) {
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid artist-grid';
  for (const artist of artists) {
    const card = document.createElement('button');
    card.type = 'button';
    card.className = 'item artist-card';
    const meta = document.createElement('div');
    meta.className = 'meta';
    const name = document.createElement('span');
    name.className = 'title';
    name.textContent = artist.name;
    const sub = document.createElement('span');
    sub.className = 'sub';
    sub.textContent = `${artist.albumCount} album${artist.albumCount === 1 ? '' : 's'}`;
    meta.append(name, sub);
    card.append(coverArt(artUrl(artist.sourceId, artist.artId), artist.name, true), meta);
    card.addEventListener('click', () => showArtist(artist.sourceId, artist.id));
    grid.append(card);
  }
  return grid;
}

function backButton(label, action) {
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'back';
  back.textContent = `\u2190 ${label}`;
  back.addEventListener('click', action);
  return back;
}

function shuffled(items) {
  const out = items.slice();
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

function playButtons(getSongs) {
  const row = document.createElement('div');
  row.className = 'play-row';
  const play = document.createElement('button');
  play.type = 'button';
  play.className = 'play-main';
  play.textContent = '\u25B6  Play';
  play.addEventListener('click', async () => {
    const songs = await getSongs();
    if (songs.length) playQueue(songs, 0);
  });
  const shuffle = document.createElement('button');
  shuffle.type = 'button';
  shuffle.className = 'ghost';
  shuffle.textContent = 'Shuffle';
  shuffle.addEventListener('click', async () => {
    const songs = await getSongs();
    if (songs.length) playQueue(shuffled(songs), 0);
  });
  row.append(play, shuffle);
  return row;
}

async function showAlbum(sourceId, id) {
  const view = $('music-view');
  const { ok, body } = await api(`/api/music/albums/${encodeURIComponent(sourceId)}/${escapeId(id)}`);
  if (!ok || !body) return;
  const { album, songs } = body;
  $('status').textContent = '';

  const head = document.createElement('div');
  head.className = 'album-head';
  const cover = coverArt(artUrl(album.sourceId, album.artId), album.title);
  cover.classList.add('album-cover');
  const text = document.createElement('div');
  text.className = 'album-text';
  const kind = document.createElement('span');
  kind.className = 'album-kind';
  kind.textContent = 'Album';
  const title = document.createElement('h2');
  title.textContent = album.title;
  const artist = document.createElement('button');
  artist.type = 'button';
  artist.className = 'album-artist';
  artist.textContent = album.artist;
  artist.disabled = !album.artistId;
  artist.addEventListener('click', () => showArtist(album.sourceId, album.artistId));
  const facts = document.createElement('span');
  facts.className = 'muted';
  facts.textContent = [album.year, `${songs.length} song${songs.length === 1 ? '' : 's'}`,
    formatLength(album.durationSeconds)].filter(Boolean).join(' · ');
  const albumButtons = playButtons(async () => songs);
  albumButtons.append(downloadButton({
    id: `album:${album.sourceId}/${album.id}`, type: 'album', title: album.title,
    subtitle: album.artist, sourceId: album.sourceId, artId: album.artId,
  }, songs));
  text.append(kind, title, artist, facts, albumButtons);
  head.append(cover, text);

  const list = document.createElement('ol');
  list.className = 'track-rows';
  songs.forEach((song, i) => list.append(trackRow(song, i, songs)));

  view.replaceChildren(backButton(state.musicView === 'artists' ? 'Artists' : 'Albums', () => runSearch()), head, list);
  window.scrollTo(0, 0);
}

function trackRow(song, index, songs) {
  const li = document.createElement('li');
  const row = document.createElement('button');
  row.type = 'button';
  row.className = 'item track-row';
  const number = document.createElement('span');
  number.className = 'track-no';
  number.textContent = String(index + 1);
  const name = document.createElement('span');
  name.className = 'track-title';
  name.textContent = song.title;
  const length = document.createElement('span');
  length.className = 'track-length';
  length.textContent = formatDuration(song.durationSeconds);
  row.append(number, name, length);
  row.dataset.key = selectionKey(song);
  row.addEventListener('click', (event) => {
    if (state.suppressClick) {
      state.suppressClick = false;
      event.stopPropagation();
      return;
    }
    playQueue(songs, index);
  });
  // The same hold-down / right-click menu as a card: favourites, playlists.
  attachItemMenuGestures(row, song);
  li.append(row);
  return li;
}

async function showArtist(sourceId, id) {
  const view = $('music-view');
  const { ok, body } = await api(`/api/music/artists/${encodeURIComponent(sourceId)}/${escapeId(id)}`);
  if (!ok || !body) return;
  const { artist, albums } = body;
  $('status').textContent = '';

  const head = document.createElement('div');
  head.className = 'album-head artist-head';
  const photo = coverArt(artUrl(artist.sourceId || sourceId, artist.artId), artist.name, true);
  photo.classList.add('album-cover');
  const text = document.createElement('div');
  text.className = 'album-text';
  const kind = document.createElement('span');
  kind.className = 'album-kind';
  kind.textContent = 'Artist';
  const name = document.createElement('h2');
  name.textContent = artist.name;
  const facts = document.createElement('span');
  facts.className = 'muted';
  facts.textContent = `${albums.length} album${albums.length === 1 ? '' : 's'}`;
  // Every song by the artist: each album's songs, in album order.
  const everySong = async () => {
    const lists = await Promise.all(albums.map((a) =>
      api(`/api/music/albums/${encodeURIComponent(a.sourceId || sourceId)}/${escapeId(a.id)}`)));
    return lists.flatMap((r) => (r.ok && r.body && r.body.songs) || []);
  };
  const buttons = playButtons(everySong);
  const radio = document.createElement('button');
  radio.type = 'button';
  radio.className = 'ghost';
  radio.textContent = 'Artist mix';
  radio.title = 'Their songs, with others from the same genres';
  radio.addEventListener('click', () => playMix(`artist:${id}`));
  buttons.append(radio);
  text.append(kind, name, facts, buttons);
  head.append(photo, text);

  const heading = document.createElement('h3');
  heading.className = 'section-title';
  heading.textContent = 'Albums';
  view.replaceChildren(backButton(state.musicView === 'albums' ? 'Albums' : 'Artists', () => runSearch()),
    head, heading, albumGrid(albums.map((a) => ({ ...a, sourceId: a.sourceId || sourceId }))));
  window.scrollTo(0, 0);
}

/* ----------------------------------------------------- the queue, grown up */

// Every song played is in a queue now, not only a playlist's: Play next and
// Add to queue turn a single song into one. audio.queue.items is the order
// things will play in; shuffle rearranges what is still to come and keeps the
// order it had, so turning it off puts things back.
audio.shuffle = false;
audio.repeat = 'off'; // 'off', 'all', 'one'

function ensureQueue() {
  if (!audio.queue && audio.item && audio.item.kind === 'music') {
    audio.queue = { items: [audio.item], index: 0 };
  }
  return audio.queue;
}

function queuePlayNext(item) {
  if (!ensureQueue()) {
    playQueue([item], 0);
    return;
  }
  audio.queue.items.splice(audio.queue.index + 1, 0, item);
  if (audio.queue.original) audio.queue.original.push(item);
  queueChanged();
}

function queueAdd(item) {
  if (!ensureQueue()) {
    playQueue([item], 0);
    return;
  }
  audio.queue.items.push(item);
  if (audio.queue.original) audio.queue.original.push(item);
  queueChanged();
}

function queueRemove(position) {
  const q = audio.queue;
  if (!q || position <= q.index || position >= q.items.length) return;
  const [gone] = q.items.splice(position, 1);
  if (q.original) {
    const at = q.original.indexOf(gone);
    if (at >= 0) q.original.splice(at, 1);
  }
  queueChanged();
}

// queueAdvance is the end of a song: the next one, or the same one again, or
// round to the start, or stop.
function queueAdvance() {
  const q = audio.queue;
  if (!q) return;
  if (audio.repeat === 'one') {
    playQueueAt(q.index);
  } else if (q.index + 1 < q.items.length) {
    playQueueAt(q.index + 1);
  } else if (audio.repeat === 'all' && q.items.length) {
    playQueueAt(0);
  } else {
    renderNowPlaying();
  }
}

function setShuffle(on) {
  audio.shuffle = on;
  const q = ensureQueue();
  if (q) {
    const played = q.items.slice(0, q.index + 1);
    if (on) {
      q.original = q.items.slice();
      q.items = played.concat(shuffled(q.items.slice(q.index + 1)));
    } else if (q.original) {
      // Back to the order it had, carrying on after the current song.
      const current = q.items[q.index];
      q.items = q.original;
      q.index = Math.max(0, q.items.indexOf(current));
      q.original = null;
    }
  }
  queueChanged();
}

function cycleRepeat() {
  audio.repeat = { off: 'all', all: 'one', one: 'off' }[audio.repeat];
  queueChanged();
}

function queueChanged() {
  renderQueue();
  renderDockButtons();
  updateMediaSession();
  renderNowPlaying();
}

/* ------------------------------------------------------------- now playing */

function openNowPlaying() {
  if (!audio.item) return;
  audio.showQueue = false;
  audio.lyricsBig = false;
  renderLyrics();
  show($('now-playing'), true);
  document.body.classList.add('np-open');
  renderNowPlaying();
}

function closeNowPlaying() {
  show($('now-playing'), false);
  document.body.classList.remove('np-open');
  $('now-playing').style.transform = '';
}

// Moving between the big cover and the full lyrics is one smooth change, not
// a jump: the cover shrinks into the small one by the title (and back), and
// everything else slides to its new place. Browsers without view transitions
// simply switch.
function npTransition(update) {
  if (!document.startViewTransition || matchMedia('(prefers-reduced-motion: reduce)').matches) {
    update();
    return;
  }
  document.startViewTransition(update);
}

// The strip grows into the full lyrics when tapped - a tap there never jumps
// to a line, since the lines are too small a target to aim at. The small
// cover at the top shrinks them back.
$('np-lyrics').addEventListener('click', (event) => {
  if (audio.npMode !== 'strip') return;
  event.stopPropagation();
  event.preventDefault();
  npTransition(() => {
    audio.lyricsBig = true;
    renderLyrics();
  });
}, true);
document.querySelector('#now-playing .np-head').addEventListener('click', () => {
  if (audio.npMode !== 'lyrics' || !matchMedia('(max-width: 760px)').matches) return;
  npTransition(() => {
    audio.lyricsBig = false;
    renderLyrics();
  });
});

// Drag down to put the full player away, on a touch screen. The panel follows
// the finger, and goes if it was dragged far enough or flicked; otherwise it
// springs back. Not from the seek bar, and not from a list that is scrolled
// down, where dragging down means scrolling up.
(function dragToClose() {
  const panel = $('now-playing');
  let startY = 0;
  let startT = 0;
  let dy = 0;
  let dragging = false;
  let armed = false;
  const scrolledDown = (el) => {
    for (let n = el; n && n !== panel; n = n.parentElement) {
      if (n.scrollTop > 0 && n.scrollHeight > n.clientHeight) return true;
    }
    return false;
  };
  panel.addEventListener('touchstart', (event) => {
    const t = event.touches[0];
    armed = event.touches.length === 1 &&
      !event.target.closest('input') &&
      !(event.target.closest('#np-lyrics, #np-queue') && scrolledDown(event.target));
    dragging = false;
    dy = 0;
    startY = t.clientY;
    startT = performance.now();
  }, { passive: true });
  panel.addEventListener('touchmove', (event) => {
    if (!armed) return;
    const d = event.touches[0].clientY - startY;
    if (!dragging) {
      if (d < 8) {
        if (d < -8) armed = false; // an upward drag is a scroll, not a close
        return;
      }
      dragging = true;
      panel.style.transition = 'none';
    }
    dy = Math.max(0, d);
    panel.style.transform = `translateY(${dy}px)`;
    if (event.cancelable) event.preventDefault();
  }, { passive: false });
  const end = () => {
    if (!dragging) return;
    dragging = false;
    armed = false;
    const speed = dy / Math.max(performance.now() - startT, 1); // px per ms
    panel.style.transition = 'transform 0.22s ease-out';
    if (dy > panel.clientHeight * 0.25 || (speed > 0.6 && dy > 40)) {
      panel.style.transform = `translateY(${panel.clientHeight}px)`;
      setTimeout(() => {
        panel.style.transition = '';
        closeNowPlaying();
      }, 220);
    } else {
      panel.style.transform = '';
      setTimeout(() => { panel.style.transition = ''; }, 220);
    }
  };
  panel.addEventListener('touchend', end);
  panel.addEventListener('touchcancel', end);
})();

function setIcon(button, name, filled) {
  button.replaceChildren(icon(name, filled));
}

setIcon($('np-close'), 'down');
setIcon($('np-queue-toggle'), 'queue');
setIcon($('dock-play'), 'play', true);
setIcon($('dock-prev'), 'prev', true);
setIcon($('dock-next'), 'skip', true);
setIcon($('audio-close'), 'close');
$('dock-volume-icon').replaceChildren(icon('volume'));

// The card's own previous and next do what Now Playing's do.
$('dock-prev').addEventListener('click', () => mediaPrevious());
$('dock-next').addEventListener('click', () => $('np-next').click());

function renderDockButtons() {
  const q = audio.queue;
  const more = q
    ? q.index + 1 < q.items.length || audio.repeat !== 'off'
    : audio.index + 1 < audio.tracks.length;
  $('dock-next').disabled = !more;
}

// Where the song is, on the card: the line along its bottom edge, and on a
// computer the seek bar and the times.
function renderDockProgress() {
  const player = $('audio-player');
  const length = Number.isFinite(player.duration) ? player.duration : audio.duration || 0;
  const at = player.currentTime || 0;
  $('dock-progress-fill').style.width = length ? `${Math.min(100, (at / length) * 100)}%` : '0';
  if (!state.dockSeeking) {
    $('dock-seek').value = length ? String(Math.round((at / length) * 1000)) : '0';
    $('dock-time').textContent = formatDuration(at) || '0:00';
  }
  $('dock-length').textContent = formatDuration(length) || '0:00';
}
for (const event of ['timeupdate', 'loadedmetadata', 'emptied']) {
  $('audio-player').addEventListener(event, renderDockProgress);
}
for (const event of ['play', 'loadedmetadata']) {
  $('audio-player').addEventListener(event, renderDockButtons);
}
$('dock-seek').addEventListener('input', () => {
  state.dockSeeking = true;
  const player = $('audio-player');
  if (Number.isFinite(player.duration)) {
    $('dock-time').textContent = formatDuration((Number($('dock-seek').value) / 1000) * player.duration) || '0:00';
  }
});
$('dock-seek').addEventListener('change', () => {
  const player = $('audio-player');
  if (Number.isFinite(player.duration)) player.currentTime = (Number($('dock-seek').value) / 1000) * player.duration;
  state.dockSeeking = false;
});

// Volume is the listener's own level, under the ReplayGain levelling that
// sets the element's real volume.
$('dock-volume').addEventListener('input', () => {
  audio.userVolume = Number($('dock-volume').value) / 100;
  if (audio.item) applyLevel(audio.item);
});
$('audio-player').addEventListener('volumechange', () => {
  $('dock-volume').value = String(Math.round((audio.userVolume || 0) * 100));
});

// On a phone: swipe the card up for Now Playing, down to put it away.
(function dockSwipe() {
  const dock = $('audio-dock');
  let startY = 0;
  let dy = 0;
  let active = false;
  dock.addEventListener('touchstart', (event) => {
    if (event.touches.length !== 1 || !matchMedia('(max-width: 760px)').matches) return;
    active = true;
    dy = 0;
    startY = event.touches[0].clientY;
    dock.style.transition = 'none';
  }, { passive: true });
  dock.addEventListener('touchmove', (event) => {
    if (!active) return;
    dy = event.touches[0].clientY - startY;
    if (dy > 0) {
      dock.style.transform = `translateY(${dy}px)`;
      dock.style.opacity = String(Math.max(0.2, 1 - dy / 160));
    }
  }, { passive: true });
  const end = () => {
    if (!active) return;
    active = false;
    dock.style.transition = 'transform 0.2s ease-out, opacity 0.2s ease-out';
    if (dy < -30) {
      dock.style.transform = '';
      dock.style.opacity = '';
      openNowPlaying();
    } else if (dy > 70) {
      dock.style.transform = 'translateY(140%)';
      dock.style.opacity = '0';
      setTimeout(() => {
        stopAudio();
        dock.style.transform = '';
        dock.style.opacity = '';
        dock.style.transition = '';
      }, 200);
      return;
    } else {
      dock.style.transform = '';
      dock.style.opacity = '';
    }
    setTimeout(() => { dock.style.transition = ''; }, 200);
  };
  dock.addEventListener('touchend', end);
  dock.addEventListener('touchcancel', end);
})();
$('dock-play').addEventListener('click', () => {
  const player = $('audio-player');
  if (player.paused) player.play().catch(() => {});
  else player.pause();
});
for (const event of ['play', 'pause']) {
  $('audio-player').addEventListener(event, () => {
    setIcon($('dock-play'), $('audio-player').paused ? 'play' : 'pause', true);
  });
}
setIcon($('np-prev'), 'prev', true);
setIcon($('np-next'), 'skip', true);
setIcon($('np-shuffle'), 'shuffle');
setIcon($('np-repeat'), 'repeat');

function renderNowPlaying() {
  if ($('now-playing').classList.contains('hidden') || !audio.item) return;
  const item = audio.item;
  const art = artPath(item);
  for (const img of [$('np-cover'), $('np-thumb')]) {
    img.src = art || NO_COVER;
    img.onerror = () => { img.onerror = null; img.src = NO_COVER; };
  }
  if (art) $('np-backdrop').src = art;
  else $('np-backdrop').removeAttribute('src');
  $('np-title').textContent = item.title;
  $('np-sub').textContent = [(item.creators || []).join(', '), (item.extra && item.extra.album) || item.subtitle]
    .filter(Boolean).join(' \u2014 ');

  const player = $('audio-player');
  setIcon($('np-play'), player.paused ? 'play' : 'pause', true);
  $('np-shuffle').setAttribute('aria-pressed', String(audio.shuffle));
  $('np-shuffle').classList.toggle('on', audio.shuffle);
  $('np-repeat').classList.toggle('on', audio.repeat !== 'off');
  $('np-repeat').dataset.mode = audio.repeat;
  $('np-repeat').setAttribute('aria-label', `Repeat: ${audio.repeat}`);
  const q = audio.queue;
  $('np-prev').disabled = false;
  $('np-next').disabled = !q || (q.index + 1 >= q.items.length && audio.repeat === 'off');

  const list = $('np-queue');
  list.replaceChildren();
  const upcoming = q ? q.items.slice(q.index + 1) : [];
  upcoming.forEach((song, i) => {
    const position = q.index + 1 + i;
    const li = document.createElement('li');
    const go = document.createElement('button');
    go.type = 'button';
    go.className = 'np-queue-song';
    const t = document.createElement('strong');
    t.textContent = song.title;
    const s = document.createElement('span');
    s.textContent = (song.creators || []).join(', ') || song.subtitle || '';
    go.append(t, s);
    go.addEventListener('click', () => playQueueAt(position));
    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'np-icon np-remove';
    remove.setAttribute('aria-label', `Remove ${song.title} from the queue`);
    remove.append(icon('close'));
    remove.addEventListener('click', () => queueRemove(position));
    li.append(go, remove);
    list.append(li);
  });
  show($('np-queue-empty'), upcoming.length === 0);
  loadLyrics(item);
  syncNowPlayingTime();
}

function syncNowPlayingTime() {
  if ($('now-playing').classList.contains('hidden')) return;
  const player = $('audio-player');
  const length = Number.isFinite(player.duration) ? player.duration : 0;
  if (!state.seeking) {
    $('np-seek').value = length ? String(Math.round((player.currentTime / length) * 1000)) : '0';
  }
  $('np-time').textContent = formatDuration(player.currentTime) || '0:00';
  $('np-length').textContent = formatDuration(length) || '0:00';
}

$('audio-open').addEventListener('click', openNowPlaying);
$('audio-meta').addEventListener('click', openNowPlaying);
$('audio-meta').addEventListener('keydown', (event) => {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    openNowPlaying();
  }
});
$('np-close').addEventListener('click', closeNowPlaying);
$('np-play').addEventListener('click', () => {
  const player = $('audio-player');
  if (player.paused) player.play().catch(() => {});
  else player.pause();
});
$('np-prev').addEventListener('click', () => mediaPrevious());
$('np-next').addEventListener('click', () => {
  const q = audio.queue;
  if (q && q.index + 1 >= q.items.length && audio.repeat !== 'off') playQueueAt(0);
  else mediaNext();
});
$('np-shuffle').addEventListener('click', () => setShuffle(!audio.shuffle));
$('np-repeat').addEventListener('click', cycleRepeat);
$('np-seek').addEventListener('input', () => {
  state.seeking = true;
  const player = $('audio-player');
  if (Number.isFinite(player.duration)) {
    $('np-time').textContent = formatDuration((Number($('np-seek').value) / 1000) * player.duration) || '0:00';
  }
});
$('np-seek').addEventListener('change', () => {
  const player = $('audio-player');
  if (Number.isFinite(player.duration)) player.currentTime = (Number($('np-seek').value) / 1000) * player.duration;
  state.seeking = false;
});
for (const event of ['play', 'pause', 'loadedmetadata']) {
  $('audio-player').addEventListener(event, renderNowPlaying);
}
$('audio-player').addEventListener('timeupdate', () => {
  syncNowPlayingTime();
  syncLyrics();
});
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && !$('now-playing').classList.contains('hidden')) closeNowPlaying();
});

/* ------------------------------------------------- gapless, and even levels */

// Gapless, or as near as a browser gets: while a song plays, the next one in
// the queue is downloaded whole, so when this one ends the next starts from
// memory instead of waiting on the network. Measured in Chrome against a real
// Navidrome before this existed: 263-275 ms of silence between songs on a
// fast connection, 425-447 ms on a slow one.
//
// A Blob rather than a second audio element: the player keeps one element and
// every listener on it, and a blob: URL is same-origin and already allowed by
// the page's media-src.
const PRELOAD_LEAD_S = 30;          // start fetching this long before the end
const PRELOAD_MAX_BYTES = 200e6;    // a whole live album as one FLAC is not worth holding

audio.preloaded = null; // { key, url }
audio.preloading = null;

function upcomingItem() {
  const q = audio.queue;
  if (!q) return null;
  if (audio.repeat === 'one') return q.items[q.index];
  if (q.index + 1 < q.items.length) return q.items[q.index + 1];
  if (audio.repeat === 'all' && q.items.length) return q.items[0];
  return null;
}

function takePreloaded(item) {
  const p = audio.preloaded;
  if (!p || p.key !== selectionKey(item)) return null;
  // The blob stays alive while it plays; it is revoked when replaced.
  audio.playingBlob = p.url;
  audio.preloaded = null;
  return p.url;
}

async function preloadNext() {
  const next = upcomingItem();
  if (!next || next.kind !== 'music' || isDownloaded(next)) return;
  const key = selectionKey(next);
  if ((audio.preloaded && audio.preloaded.key === key) || audio.preloading === key) return;
  audio.preloading = key;
  try {
    const resp = await fetch(playPath(next), { credentials: 'same-origin' });
    const size = Number(resp.headers.get('Content-Length') || 0);
    if (!resp.ok || size > PRELOAD_MAX_BYTES) {
      if (resp.body) resp.body.cancel().catch(() => {});
      return;
    }
    const blob = await resp.blob();
    if (audio.preloading !== key) return; // the queue moved on meanwhile
    if (audio.preloaded && audio.preloaded.url !== audio.playingBlob) URL.revokeObjectURL(audio.preloaded.url);
    audio.preloaded = { key, url: URL.createObjectURL(blob) };
  } catch {
    // Offline or refused: the next song simply streams as it always did.
  } finally {
    if (audio.preloading === key) audio.preloading = null;
  }
}

$('audio-player').addEventListener('timeupdate', () => {
  const player = $('audio-player');
  if (!audio.queue || !Number.isFinite(player.duration)) return;
  if (player.duration - player.currentTime < PRELOAD_LEAD_S) preloadNext();
});
$('audio-player').addEventListener('emptied', () => {
  // A blob that has finished playing is let go - unless it is the one now
  // loaded, which 'emptied' also fires for on the way in.
  const player = $('audio-player');
  if (audio.lastBlob && audio.lastBlob !== player.currentSrc && audio.lastBlob !== audio.playingBlob) {
    URL.revokeObjectURL(audio.lastBlob);
  }
  audio.lastBlob = audio.playingBlob;
});

// Even levels, from the ReplayGain tags most ripped and bought music carries:
// a quiet 70s album and a loud modern single play at the same loudness.
//
// An album played in order uses the album's gain, so its quiet songs stay
// quiet next to its loud ones, as the artist meant; anything else uses each
// track's own. Everything plays LEVEL_PREAMP_DB below full, so that a quiet
// track can be brought *up* - an audio element can turn down but never past
// full - and a track's peak is never pushed past full.
//
// Set through the element's volume, not the Web Audio API. Routing a phone's
// music through Web Audio is what makes an iPhone stop the music when the
// screen locks. The cost is that iOS ignores the volume a page sets, so there
// levelling does nothing, where the alternative is music that stops.
const LEVEL_PREAMP_DB = -6;
audio.userVolume = 1;
audio.settingVolume = false;

function levelFor(item) {
  const extra = (item && item.extra) || {};
  const q = audio.queue;
  const inAlbumOrder = q && !audio.shuffle && q.items.length > 1
    && q.items.every((it) => ((it.extra && it.extra.album) || it.subtitle) === ((extra.album) || item.subtitle));
  const gain = Number(inAlbumOrder && extra.albumGain !== undefined ? extra.albumGain : extra.trackGain);
  const peak = Number(inAlbumOrder && extra.albumPeak !== undefined ? extra.albumPeak : extra.trackPeak);
  const anyTagged = (q ? q.items : [item]).some((it) => it && it.extra && it.extra.trackGain !== undefined);
  if (!Number.isFinite(gain)) {
    // An untagged song in a queue of tagged ones sits at the same pre-amp, so
    // it does not jump out; in an untagged queue, nothing changes at all.
    return anyTagged ? 10 ** (LEVEL_PREAMP_DB / 20) : 1;
  }
  let factor = 10 ** ((gain + LEVEL_PREAMP_DB) / 20);
  if (Number.isFinite(peak) && peak > 0) factor = Math.min(factor, 1 / peak);
  return Math.min(1, factor);
}

function applyLevel(item) {
  const player = $('audio-player');
  const level = item && item.kind === 'music' ? levelFor(item) : 1;
  audio.level = level;
  audio.settingVolume = true;
  player.volume = Math.max(0, Math.min(1, level * audio.userVolume));
  audio.settingVolume = false;
}

// The volume somebody chose with the player's own slider is theirs, and
// levelling works under it rather than replacing it.
$('audio-player').addEventListener('volumechange', () => {
  // A fade (the sleep timer's, a crossfade) is not the listener moving the
  // volume; counted as one, it left their volume at zero afterwards.
  if (audio.settingVolume || audio.fading) return;
  const player = $('audio-player');
  const level = audio.level || 1;
  audio.userVolume = Math.min(1, player.volume / level);
});

/* -------------------------------------------------------------------- mixes */

// A mix is a queue made for you: tap it and it plays. Its cover is a collage
// of four of the covers in it.
function mixCover(mix) {
  const wrap = document.createElement('div');
  wrap.className = 'art-wrap mix-cover';
  const art = (mix.covers || []).slice(0, 4);
  if (art.length >= 4) {
    wrap.classList.add('collage');
    for (const id of art) {
      const img = document.createElement('img');
      img.src = artUrl(mix.sourceId, id);
      img.alt = '';
      img.loading = 'lazy';
      wrap.append(img);
    }
  } else if (art.length) {
    const img = document.createElement('img');
    img.src = artUrl(mix.sourceId, art[0]);
    img.alt = '';
    wrap.append(img);
  } else {
    wrap.append(initials(mix.title));
  }
  const play = document.createElement('span');
  play.className = 'mix-play';
  play.append(icon('play', true));
  wrap.append(play);
  return wrap;
}

function mixGrid(mixes) {
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid mix-grid';
  for (const mix of mixes) {
    const card = document.createElement('button');
    card.type = 'button';
    card.className = 'item mix-card';
    const meta = document.createElement('div');
    meta.className = 'meta';
    const title = document.createElement('span');
    title.className = 'title';
    title.textContent = mix.title;
    const sub = document.createElement('span');
    sub.className = 'sub';
    sub.textContent = mix.subtitle;
    meta.append(title, sub);
    card.append(mixCover(mix), meta);
    card.addEventListener('click', () => playMix(mix.id));
    grid.append(card);
  }
  return grid;
}

async function playMix(id) {
  const { ok, body } = await api(`/api/music/mixes/${encodeURIComponent(id)}`);
  const songs = (ok && body && body.songs) || [];
  if (songs.length) playQueue(songs, 0);
}

// A play counts once half the song, or four minutes, has been heard - the
// rule most music services use - and once per time it is started.
$('audio-player').addEventListener('timeupdate', () => {
  const player = $('audio-player');
  const item = audio.item;
  if (!item || item.kind !== 'music' || audio.counted || !Number.isFinite(player.duration)) return;
  if (player.currentTime >= Math.min(240, player.duration / 2)) {
    audio.counted = true;
    api('/api/history', { method: 'POST', body: JSON.stringify({ source: item.sourceId, id: item.id }) });
  }
});

/* ------------------------------------------------------------------ lyrics */

// Synced lyrics in Now Playing: the line being sung lights up and stays in the
// middle; tap a line to go to it. Words without timings simply show. They come
// from a .lrc file beside the song or the file's own tags, through Navidrome.
audio.lyrics = null;    // { key, synced, lines }
// Lyrics always show when a song has them; Up next takes their place when asked for.
audio.showQueue = false;

async function loadLyrics(item) {
  const key = selectionKey(item);
  if (audio.lyrics && audio.lyrics.key === key) return;
  audio.lyrics = { key, synced: false, lines: [], loading: true };
  renderLyrics();
  if (item.kind !== 'music') return;
  const { ok, body } = await api(`/api/music/lyrics/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`);
  if (!audio.lyrics || audio.lyrics.key !== key) return;
  audio.lyrics = { key, synced: Boolean(ok && body && body.synced), lines: (ok && body && body.lines) || [], from: (ok && body && body.from) || '' };
  // A long intro gets the same breathing dots as an instrumental break, so
  // the screen is not a column of grey waiting for the first word.
  const first = audio.lyrics.lines[0];
  if (audio.lyrics.synced && first && first.start >= 5000) audio.lyrics.lines.unshift({ start: 0, text: '' });
  renderLyrics();
}

function renderLyrics() {
  const has = Boolean(audio.lyrics && audio.lyrics.lines.length);
  const toggle = $('np-queue-toggle');
  toggle.classList.toggle('on', audio.showQueue);
  toggle.setAttribute('aria-pressed', String(audio.showQueue));
  // Four ways the screen can be laid out:
  //   queue  - Up next in the middle, a small cover beside the title;
  //   lyrics - the lyrics in the middle, the same small cover;
  //   strip  - a phone's default: the big cover, and under the title a strip
  //            of the few lines around the one being sung (tap it for lyrics);
  //   cover  - no lyrics at all: the big cover and nothing else.
  // A computer has room for the cover beside the lyrics, so it never strips.
  const phone = matchMedia('(max-width: 760px)').matches;
  const mode = audio.showQueue ? 'queue'
    : !has ? 'cover'
    : (audio.lyricsBig || !phone) ? 'lyrics' : 'strip';
  audio.npMode = mode;
  const showing = mode === 'lyrics' || mode === 'strip';
  show($('np-lyrics'), showing);
  show($('np-next-block'), mode === 'queue');
  // The panel layout - title at the top, the middle for lyrics or the queue,
  // controls at the bottom - whenever the middle is not the cover.
  $('now-playing').classList.toggle('panel-on', mode === 'lyrics' || mode === 'queue');
  $('now-playing').classList.toggle('strip-on', mode === 'strip');
  const box = $('np-lyrics');
  box.replaceChildren();
  if (!has) return;
  box.classList.toggle('unsynced', !audio.lyrics.synced);
  audio.lyrics.lines.forEach((line, i) => {
    const el = document.createElement(audio.lyrics.synced ? 'button' : 'p');
    el.className = 'np-lyric';
    el.dataset.index = String(i);
    const text = (line.text || '').trim();
    if (audio.lyrics.synced && !text) {
      // An instrumental break: three dots that fill as it runs.
      el.classList.add('gap');
      el.setAttribute('aria-label', 'Instrumental');
      for (let d = 0; d < 3; d++) el.append(document.createElement('i'));
    } else {
      el.textContent = text || '\u00A0';
    }
    if (audio.lyrics.synced) {
      el.type = 'button';
      el.addEventListener('click', () => {
        $('audio-player').currentTime = line.start / 1000;
        syncLyrics(true);
      });
    }
    box.append(el);
  });
  if (audio.lyrics.from === 'lrclib') {
    // LRCLIB asks nothing in return; saying where the words came from is the least owed.
    const credit = document.createElement('p');
    credit.className = 'np-lyrics-credit';
    credit.textContent = 'Lyrics from LRCLIB';
    box.append(credit);
  }
  audio.lyricIndex = -1;
  syncLyrics(true);
}

const LYRIC_LEAD_MS = 150;

function syncLyrics(force) {
  const lyrics = audio.lyrics;
  if (!lyrics || !lyrics.synced || audio.showQueue || $('now-playing').classList.contains('hidden')) return;
  // A touch early: the eye needs a moment to find the line, and the fade in
  // takes a moment more, so a line lit exactly on time reads as late.
  const ms = $('audio-player').currentTime * 1000 + LYRIC_LEAD_MS;
  let index = -1;
  for (let i = 0; i < lyrics.lines.length; i++) {
    if (lyrics.lines[i].start <= ms) index = i;
    else break;
  }
  const box = $('np-lyrics');
  if (index >= 0) fillLyric(box, lyrics, index, ms);
  if (index === audio.lyricIndex && !force) return;
  audio.lyricIndex = index;
  for (const el of box.querySelectorAll('.np-lyric')) {
    const i = Number(el.dataset.index);
    el.classList.toggle('current', i === index);
    el.classList.toggle('past', i < index);
    // Distance from the line being sung, for the blur that falls off around it.
    el.style.setProperty('--d', String(Math.min(Math.abs(i - index), 4)));
  }
  if (index >= 0) fillLyric(box, lyrics, index, ms);
  const current = box.querySelector('.np-lyric.current');
  if (current) {
    // Kept a little above the middle of the box, as the words go by. Measured
    // against the box itself: offsetTop counts from the nearest positioned
    // ancestor, which is not the box, and parked the line near the top.
    const offset = current.getBoundingClientRect().top - box.getBoundingClientRect().top + box.scrollTop;
    const at = audio.npMode === 'strip' ? 0.5 : 0.42;
    box.scrollTo({ top: offset - box.clientHeight * at + current.clientHeight / 2, behavior: force ? 'auto' : 'smooth' });
  }
}

// How far through an instrumental break the song is, for its dots. Lines
// themselves light up whole: lyrics carry a time per line, not per word, and
// guessing the words drifted visibly from the singing.
function fillLyric(box, lyrics, index, ms) {
  const el = box.querySelector(`.np-lyric.gap[data-index="${index}"]`);
  if (!el) return;
  const start = lyrics.lines[index].start;
  const next = lyrics.lines[index + 1];
  const end = next ? next.start : start + 4000;
  const p = Math.max(0, Math.min(1, (ms - start) / Math.max(end - start, 1)));
  el.style.setProperty('--p', p.toFixed(3));
}

$('np-queue-toggle').addEventListener('click', () => {
  audio.showQueue = !audio.showQueue;
  renderLyrics();
});
// timeupdate comes four times a second; a line change between them would lag,
// so while lyrics are showing they are also checked on every frame.
(function lyricFrame() {
  if (!audio.showQueue && !$('audio-player').paused) syncLyrics();
  requestAnimationFrame(lyricFrame);
})();

/* --------------------------------------------------------------- downloads */

// Songs downloaded to this device, for playing without a connection: through
// a tunnel, out of Wi-Fi range, on a plane. The bytes are in the Cache API
// (OFFLINE_CACHE), keyed by each song's own stream address; what was
// downloaded - albums, playlists, single songs - is listed in localStorage.
//
// Downloaded songs always play from the device, connection or not. Opening
// the app with no connection at all is the service worker's part; see sw.js
// for the one case where it answers a page load.
const OFFLINE_CACHE = 'soundstorm-offline-v1';
const OFFLINE_SHELL = 'soundstorm-offline-shell-v1';
const DOWNLOADS_KEY = 'soundstorm-downloads';

function loadDownloadIndex() {
  try {
    const raw = JSON.parse(localStorage.getItem(DOWNLOADS_KEY) || 'null');
    if (raw && raw.items && raw.groups) return raw;
  } catch {
    // A damaged index lists nothing; the cache is cleared with the next change.
  }
  return { items: {}, groups: [] };
}
state.downloads = loadDownloadIndex();

function saveDownloadIndex() {
  try {
    localStorage.setItem(DOWNLOADS_KEY, JSON.stringify(state.downloads));
  } catch {
    // Storage full: the downloads still play this session.
  }
}

function hasDownloads() {
  return state.downloads.groups.length > 0;
}

function isDownloaded(item) {
  return Boolean(item && state.downloads.items[selectionKey(item)]);
}

async function offlineURL(item) {
  try {
    const cache = await caches.open(OFFLINE_CACHE);
    const resp = await cache.match(streamPath(item));
    return resp ? URL.createObjectURL(await resp.blob()) : '';
  } catch {
    return '';
  }
}

async function offlineArtURL(item) {
  const path = artPath(item);
  if (!path) return '';
  try {
    const resp = await (await caches.open(OFFLINE_CACHE)).match(path);
    return resp ? URL.createObjectURL(await resp.blob()) : '';
  } catch {
    return '';
  }
}

// keepShell saves the app itself for opening offline. The service worker only
// serves it back on the real *.soundstorm.dev names (see sw.js).
async function keepShell() {
  try {
    const cache = await caches.open(OFFLINE_SHELL);
    await cache.addAll(['/', '/static/app.js', '/static/style.css', '/static/favicon.svg', '/static/sw-register.js']);
  } catch {
    // Offline start-up is a bonus; downloads still play with the app open.
  }
}

// download saves a group of songs - an album, a playlist, one song - and
// reports progress through onProgress(done, total).
async function download(group, items, onProgress) {
  const songs = items.filter((it) => it && it.kind === 'music');
  if (!songs.length) return;
  if (navigator.storage && navigator.storage.persist) navigator.storage.persist().catch(() => {});
  const cache = await caches.open(OFFLINE_CACHE);
  let done = 0;
  for (const song of songs) {
    const key = selectionKey(song);
    if (!state.downloads.items[key]) {
      try {
        const resp = await fetch(streamPath(song), { credentials: 'same-origin' });
        if (!resp.ok) throw new Error(`status ${resp.status}`);
        await cache.put(streamPath(song), resp);
        const art = artPath(song);
        if (art && !(await cache.match(art))) {
          const artResp = await fetch(art, { credentials: 'same-origin' });
          if (artResp.ok) await cache.put(art, artResp);
        }
        state.downloads.items[key] = song;
      } catch {
        continue; // one song failing does not stop the rest
      }
    }
    done++;
    if (onProgress) onProgress(done, songs.length);
  }
  state.downloads.groups = state.downloads.groups.filter((g) => g.id !== group.id);
  state.downloads.groups.unshift({ ...group, keys: songs.map(selectionKey), at: Date.now() });
  saveDownloadIndex();
  keepShell();
  markMusicTabs();
}

// removeDownload forgets a group, and deletes each song no other group needs.
async function removeDownload(groupID) {
  const group = state.downloads.groups.find((g) => g.id === groupID);
  if (!group) return;
  state.downloads.groups = state.downloads.groups.filter((g) => g.id !== groupID);
  const stillNeeded = new Set(state.downloads.groups.flatMap((g) => g.keys));
  const cache = await caches.open(OFFLINE_CACHE);
  for (const key of group.keys) {
    if (stillNeeded.has(key)) continue;
    const song = state.downloads.items[key];
    if (song) await cache.delete(streamPath(song));
    delete state.downloads.items[key];
  }
  saveDownloadIndex();
  markMusicTabs();
}

async function clearDownloads() {
  state.downloads = { items: {}, groups: [] };
  try {
    localStorage.removeItem(DOWNLOADS_KEY);
    await caches.delete(OFFLINE_CACHE);
    await caches.delete(OFFLINE_SHELL);
  } catch {
    // nothing to clear
  }
}

function downloadButton(group, songs) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'ghost download-button';
  const label = () => {
    const have = state.downloads.groups.some((g) => g.id === group.id);
    button.replaceChildren(icon('download'), document.createTextNode(have ? ' Downloaded' : ' Download'));
    button.classList.toggle('done', have);
    button.title = have ? 'On this device. Click to remove.' : 'Keep on this device for playing offline';
  };
  label();
  button.addEventListener('click', async () => {
    if (state.downloads.groups.some((g) => g.id === group.id)) {
      if (!window.confirm(`Remove "${group.title}" from this device? It stays in your library.`)) return;
      await removeDownload(group.id);
      label();
      return;
    }
    button.disabled = true;
    await download(group, songs, (done, total) => {
      button.replaceChildren(document.createTextNode(`Downloading ${done} of ${total}\u2026`));
    });
    button.disabled = false;
    label();
  });
  return button;
}

function formatStorage(bytes) {
  if (!bytes) return '0 MB';
  return bytes >= 1e9 ? `${(bytes / 1e9).toFixed(1)} GB` : `${Math.round(bytes / 1e6)} MB`;
}

// downloadsView lists what is on this device: to play, and to remove.
async function downloadsView(offlineMode) {
  const wrap = document.createElement('div');
  wrap.className = 'downloads';
  const head = document.createElement('div');
  head.className = 'downloads-head';
  const h = document.createElement('h2');
  h.textContent = 'Downloads';
  const used = document.createElement('span');
  used.className = 'muted';
  if (navigator.storage && navigator.storage.estimate) {
    const est = await navigator.storage.estimate().catch(() => null);
    if (est) used.textContent = `${formatStorage(est.usage)} used on this device`;
  }
  head.append(h, used);
  wrap.append(head);

  if (!hasDownloads()) {
    const empty = document.createElement('p');
    empty.className = 'muted';
    empty.textContent = 'Nothing downloaded yet. Use Download on an album or playlist to keep it on this device.';
    wrap.append(empty);
    return wrap;
  }
  const list = document.createElement('ul');
  list.className = 'download-list';
  for (const group of state.downloads.groups) {
    const songs = group.keys.map((k) => state.downloads.items[k]).filter(Boolean);
    const li = document.createElement('li');
    const cover = document.createElement('div');
    cover.className = 'download-cover';
    const img = document.createElement('img');
    img.alt = '';
    const artItem = songs.find((s) => s.artId);
    if (artItem) offlineArtURL(artItem).then((u) => { if (u) img.src = u; });
    cover.append(img);
    const text = document.createElement('button');
    text.type = 'button';
    text.className = 'download-text';
    const t = document.createElement('strong');
    t.textContent = group.title;
    const s = document.createElement('span');
    s.className = 'muted';
    s.textContent = [group.subtitle, `${songs.length} song${songs.length === 1 ? '' : 's'}`].filter(Boolean).join(' \u00B7 ');
    text.append(t, s);
    text.addEventListener('click', () => playQueue(songs, 0));
    const play = document.createElement('button');
    play.type = 'button';
    play.className = 'np-icon download-play';
    play.setAttribute('aria-label', `Play ${group.title}`);
    play.append(icon('play', true));
    play.addEventListener('click', () => playQueue(songs, 0));
    li.append(cover, text, play);
    if (!offlineMode) {
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'np-icon download-remove';
      remove.setAttribute('aria-label', `Remove ${group.title} from this device`);
      remove.append(icon('close'));
      remove.addEventListener('click', async () => {
        await removeDownload(group.id);
        li.remove();
        if (!hasDownloads()) runSearch();
      });
      li.append(remove);
    }
    list.append(li);
  }
  wrap.append(list);
  return wrap;
}

// showOfflineApp is the app with no server: downloads, and the player.
async function showOfflineApp() {
  show($('boot'), false);
  show($('gate'), false);
  show($('app'), false);
  show($('tabs'), false);
  show($('offline-app'), true);
  $('offline-view').replaceChildren(await downloadsView(true));
}

$('offline-retry').addEventListener('click', () => location.reload());

/* ------------------------------------------------------------- sleep timer */

// A sleep timer, in Now Playing: stop in 15, 30, 45 or 60 minutes, or at the
// end of the song (or audiobook chapter). The time left shows under "Now
// Playing". When it runs out the music fades over eight seconds rather than
// cutting off, then pauses; on an iPhone, which does not let a page set the
// volume, it simply pauses.
const sleep = { until: 0, atSongEnd: false, tick: 0, fading: false };
const SLEEP_FADE_MS = 8000;

function setSleep(choice) {
  clearInterval(sleep.tick);
  sleep.until = 0;
  sleep.atSongEnd = false;
  if (choice === 'song') {
    sleep.atSongEnd = true;
  } else if (Number(choice) > 0) {
    sleep.until = Date.now() + Number(choice) * 60000;
    sleep.tick = setInterval(sleepTick, 1000);
  }
  renderSleep();
}

function sleepTick() {
  const left = sleep.until - Date.now();
  if (left <= 0) {
    setSleep(null);
    fadeOutAndPause();
    return;
  }
  renderSleep();
}

function renderSleep() {
  const on = Boolean(sleep.until || sleep.atSongEnd);
  $('np-sleep').classList.toggle('on', on);
  $('np-sleep').setAttribute('aria-pressed', String(on));
  show($('np-sleep-off'), on);
  const label = $('np-sleep-left');
  if (sleep.atSongEnd) {
    label.textContent = 'Stops after this song';
  } else if (sleep.until) {
    const secs = Math.max(0, Math.ceil((sleep.until - Date.now()) / 1000));
    label.textContent = `Stops in ${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}`;
  }
  show(label, on);
}

function fadeOutAndPause() {
  const player = $('audio-player');
  if (player.paused || sleep.fading) return;
  const start = player.volume;
  const began = performance.now();
  sleep.fading = true;
  audio.fading = true;
  const step = () => {
    const t = Math.min(1, (performance.now() - began) / SLEEP_FADE_MS);
    player.volume = start * (1 - t);
    if (t < 1 && !player.paused) {
      requestAnimationFrame(step);
      return;
    }
    player.pause();
    sleep.fading = false;
    // Back to the listener's own level for next time. The fade's own
    // volumechange events are still queued; released only after they have
    // been seen, so none of them is taken for the listener's choice.
    if (audio.item) applyLevel(audio.item);
    else player.volume = start;
    setTimeout(() => { audio.fading = false; }, 0);
  };
  requestAnimationFrame(step);
}

setIcon($('np-sleep'), 'moon');
$('np-sleep').addEventListener('click', (event) => {
  event.stopPropagation();
  const menu = $('np-sleep-menu');
  const opening = menu.classList.contains('hidden');
  show(menu, opening);
  $('np-sleep').setAttribute('aria-expanded', String(opening));
});
for (const choice of document.querySelectorAll('#np-sleep-menu [data-minutes]')) {
  choice.addEventListener('click', (event) => {
    event.stopPropagation();
    setSleep(choice.dataset.minutes === 'off' ? null : choice.dataset.minutes);
    show($('np-sleep-menu'), false);
    $('np-sleep').setAttribute('aria-expanded', 'false');
  });
}
document.addEventListener('click', () => {
  show($('np-sleep-menu'), false);
  $('np-sleep').setAttribute('aria-expanded', 'false');
});
renderSleep();

/* --------------------------------------------------------------- crossfade */

// Crossfade: the last few seconds of a song blend into the next, as a radio
// does. The page has one audio element, and everything in it - the lyrics,
// the lock screen, the queue, the levelling - listens to that one. So a second,
// hidden element plays only the fade-in; when the first song ends, the main
// element takes the next song over from exactly where the hidden one has got
// to (it is usually a blob already in memory, so that is instant), and the
// hidden one stops. The rest of the app never learns there were two.
//
// Not between consecutive songs of one album, which should flow as the album
// does; not on repeat-one; not when the sleep timer is to stop after this
// song; and not on an iPhone, which ignores a page's volume and would simply
// play both songs over each other at full volume.
const FADE_KEY = 'soundstorm.crossfade';
const canSetVolume = (() => {
  const probe = document.createElement('audio');
  probe.volume = 0.5;
  return probe.volume === 0.5;
})();
function crossfadeSeconds() {
  return canSetVolume ? Number(localStorage.getItem(FADE_KEY) || 0) : 0;
}

const xfade = { el: null, key: null, started: 0, secs: 0, from: 1, to: 1, handingOff: false };

function fadeElement() {
  if (!xfade.el) {
    xfade.el = document.createElement('audio');
    xfade.el.preload = 'auto';
  }
  return xfade.el;
}

function albumOf(item) {
  const extra = (item && item.extra) || {};
  return `${(item.creators || [])[0] || ''}\u0000${extra.album || item.subtitle || ''}`;
}

function maybeStartCrossfade() {
  const secs = crossfadeSeconds();
  if (!secs || xfade.key || sleep.atSongEnd || audio.repeat === 'one') return;
  const player = $('audio-player');
  if (player.paused || !audio.item || audio.item.kind !== 'music' || !Number.isFinite(player.duration)) return;
  const left = player.duration - player.currentTime;
  if (left > secs || left < 1) return;
  const next = upcomingItem();
  if (!next || next.kind !== 'music' || isDownloaded(next) || albumOf(next) === albumOf(audio.item)) return;
  const key = selectionKey(next);
  const el = fadeElement();
  el.src = audio.preloaded && audio.preloaded.key === key ? audio.preloaded.url : playPath(next);
  el.volume = 0;
  el.play().catch(() => {});
  Object.assign(xfade, {
    key, started: performance.now(), secs: left, from: player.volume, handingOff: false,
    to: Math.max(0, Math.min(1, levelFor(next) * audio.userVolume)),
  });
  audio.fading = true;
  const step = () => {
    if (xfade.key !== key || xfade.handingOff) return;
    const t = Math.min(1, (performance.now() - xfade.started) / (xfade.secs * 1000));
    // Equal power: the two together stay as loud as one through the middle.
    player.volume = xfade.from * Math.cos((t * Math.PI) / 2);
    el.volume = xfade.to * Math.sin((t * Math.PI) / 2);
    if (t < 1) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}

// takeCrossfade hands the fading-in song to the main player: its address and
// where it has got to, allowing a moment for the main element to load it.
function takeCrossfade(item) {
  if (!xfade.key) return null;
  if (xfade.key !== selectionKey(item)) {
    cancelCrossfade(false);
    return null;
  }
  const el = fadeElement();
  const url = el.currentSrc || el.src;
  if (audio.preloaded && audio.preloaded.url === url) {
    audio.playingBlob = url;
    audio.preloaded = null;
  }
  xfade.handingOff = true;
  return { url, at: el.currentTime + 0.08 };
}

function finishCrossfade() {
  const el = fadeElement();
  el.pause();
  el.removeAttribute('src');
  el.load();
  xfade.key = null;
  xfade.handingOff = false;
  setTimeout(() => { audio.fading = false; }, 0);
}

// cancelCrossfade stops a fade part way: a skip, a pause, a seek. The song
// playing comes back to its own level.
function cancelCrossfade(restore = true) {
  if (!xfade.key) return;
  finishCrossfade();
  if (restore && audio.item) applyLevel(audio.item);
}

$('audio-player').addEventListener('timeupdate', maybeStartCrossfade);
$('audio-player').addEventListener('playing', () => {
  if (xfade.handingOff) finishCrossfade();
});
$('audio-player').addEventListener('pause', () => {
  // A song reaching its end pauses too, and that is when the handover
  // happens, not a reason to stop the fade.
  if (!$('audio-player').ended && !xfade.handingOff) cancelCrossfade();
});
$('audio-player').addEventListener('seeking', () => {
  if (!xfade.handingOff) cancelCrossfade();
});

$('crossfade-select').value = String(crossfadeSeconds());
$('crossfade-select').disabled = !canSetVolume;
show($('crossfade-unavailable'), !canSetVolume);
$('crossfade-select').addEventListener('change', (event) => {
  localStorage.setItem(FADE_KEY, event.target.value);
  note($('playback-note'), 'Saved.', false);
});

// The photo viewer's buttons, drawn once the icons above exist.
setIcon($('photo-close'), 'down');
$('photo-download').replaceChildren(icon('download'));
setIcon($('photo-prev'), 'back');
setIcon($('photo-next'), 'forward');

/* ------------------------------------------------------------------ tabs */

// Five tabs instead of ten chips: each is a group of shelves, one tap away,
// along the bottom of a phone and down the side of a computer. The chips still
// exist, hidden, and still do the choosing - a tab is a way of pressing one.
// A tab remembers which of its shelves was last open. A shelf this account may
// not see, or that has nothing on it, is left out, and a tab left with nothing
// is hidden.
const TABS = {
  home: [{ kind: '', label: 'Home' }, { kind: 'favourites', label: 'Favourites', inMusicTabs: true }],
  music: [{ kind: 'music', label: 'Music' }, { kind: 'playlists', label: 'Playlists', inMusicTabs: true }],
  watch: [{ kind: 'video', label: 'Films' }, { kind: 'tv', label: 'TV' }],
  books: [{ kind: 'audiobook', label: 'Audiobooks' }, { kind: 'ebook', label: 'Ebooks' }, { kind: 'document', label: 'Documents' }],
  photos: [{ kind: 'picture', label: 'Photos' }],
};
state.tab = 'home';
state.tabKind = {};

function tabOf(kind) {
  return Object.keys(TABS).find((tab) => TABS[tab].some((o) => o.kind === kind)) || 'home';
}

function shelfAvailable(kind) {
  if (kind === '' || kind === 'favourites' || kind === 'playlists') return true;
  const chip = document.querySelector(`#filters .chip[data-kind="${kind}"]`);
  if (!chip || chip.classList.contains('hidden')) return false; // not allowed
  const files = state.shelfFiles && state.shelfFiles[kind];
  return files === undefined || files > 0;
}

function tabShelves(tab) {
  return (TABS[tab] || []).filter((o) => shelfAvailable(o.kind));
}

function selectKind(kind) {
  const chip = document.querySelector(`#filters .chip[data-kind="${kind}"]`);
  if (chip) chip.click();
}

function selectTab(tab) {
  if (tab === 'settings') {
    state.tab = 'settings';
    setAccountOpen(true);
    renderTabs();
    return;
  }
  if (!$('account').classList.contains('hidden')) setAccountOpen(false);
  const shelves = tabShelves(tab).filter((o) => !o.inMusicTabs);
  if (!shelves.length) return;
  const remembered = state.tabKind[tab];
  const kind = TABS[tab].some((o) => o.kind === remembered) && shelfAvailable(remembered) ? remembered : shelves[0].kind;
  state.tab = tab;
  if (kind === state.kind) {
    renderTabs();
    window.scrollTo({ top: 0, behavior: 'smooth' }); // a tab tapped again goes back to the top
    return;
  }
  selectKind(kind);
  window.scrollTo(0, 0);
}

// noteTabKind follows the chips: whatever shelf is open, its tab is lit.
function noteTabKind() {
  state.tab = tabOf(state.kind);
  state.tabKind[state.tab] = state.kind;
  renderTabs();
}

function renderTabs() {
  let any = false;
  for (const button of document.querySelectorAll('#tabs [data-tab]')) {
    const tab = button.dataset.tab;
    const available = tab === 'home' || tab === 'settings' || tabShelves(tab).some((o) => !o.inMusicTabs);
    show(button, available);
    any = any || (available && tab !== 'home');
    const on = tab === state.tab;
    button.classList.toggle('active', on);
    button.setAttribute('aria-current', on ? 'page' : 'false');
  }
  // Only with the library on screen - not over the sign-in page.
  show($('tabs'), !$('app').classList.contains('hidden'));
  // The shelves inside the tab, when there is more than one to choose from.
  const box = $('subtabs');
  const shelves = state.tab === 'settings' ? [] : tabShelves(state.tab).filter((o) => !o.inMusicTabs);
  box.replaceChildren();
  if (shelves.length > 1) {
    for (const o of shelves) {
      const b = document.createElement('button');
      b.type = 'button';
      b.setAttribute('role', 'tab');
      b.textContent = o.label;
      const on = o.kind === state.kind;
      b.classList.toggle('active', on);
      b.setAttribute('aria-selected', String(on));
      b.addEventListener('click', () => selectKind(o.kind));
      box.append(b);
    }
  }
  show(box, shelves.length > 1);
  // A tab whose shelves have all gone (emptied, or taken away): back home.
  if (state.tab !== 'home' && state.tab !== 'settings' && !tabShelves(state.tab).length) selectTab('home');
}

for (const button of document.querySelectorAll('#tabs [data-tab]')) {
  button.querySelector('.tab-icon').replaceChildren(icon(button.querySelector('.tab-icon').dataset.icon));
  button.addEventListener('click', () => selectTab(button.dataset.tab));
}
renderTabs();

/* -------------------------------------------------------------- home page */

// Home is a front page, not a list of everything: Continue on top (its own
// row, above), then a strip each of the newest albums, favourites, what was
// played lately, and what arrived on every other shelf. Each strip scrolls
// sideways and has a "See all" into its tab. Strips with nothing in them are
// left out, so a music-only library has a music-only home.
const HOME_SHELVES = {
  video: 'New films', tv: 'New TV', audiobook: 'New audiobooks',
  ebook: 'New books', document: 'New documents', picture: 'New photos',
};

async function renderHome(seq) {
  const view = $('home-view');
  $('status').textContent = '';
  const [home, favs, played] = await Promise.all([
    api('/api/home'),
    api('/api/favourites'),
    api('/api/music/mixes/recently-played'),
  ]);
  if (seq !== state.searchSeq) return;
  view.replaceChildren();
  const all = [];

  const albums = (home.ok && home.body && home.body.albums) || [];
  if (albums.length) {
    view.append(homeRow('New music', albums.map((a) => albumCardFromHome(a)), () => {
      state.albumOrder = 'newest';
      $('album-order').value = 'newest';
      state.musicView = 'albums';
      selectTab('music');
      if (state.kind === 'music') runSearch();
    }));
  }
  const favourites = ((favs.ok && favs.body && favs.body.items) || []).slice(0, 12);
  if (favourites.length) {
    all.push(...favourites);
    view.append(homeRow('\u2665 Favourites', favourites.map(renderItem), () => selectKind('favourites')));
  }
  const recent = ((played.ok && played.body && played.body.songs) || []).slice(0, 12);
  if (recent.length) {
    all.push(...recent);
    view.append(homeRow('Recently played', recent.map(renderItem), () => {
      state.musicView = 'mixes';
      selectTab('music');
    }));
  }
  for (const shelf of (home.ok && home.body && home.body.shelves) || []) {
    all.push(...shelf.items);
    view.append(homeRow(HOME_SHELVES[shelf.kind] || 'New', shelf.items.map(renderItem), () => selectKind(shelf.kind)));
  }
  // What the cards open into - the photo viewer steps through these.
  state.items = all;
  if (!view.children.length) {
    const empty = document.createElement('p');
    empty.className = 'muted home-empty';
    empty.textContent = state.libraryEmpty
      ? 'Nothing here yet. Open Settings and choose Add media to add music, films, books or photos.'
      : 'Nothing new lately.';
    view.append(empty);
  }
}

function homeRow(title, cards, seeAll) {
  const row = document.createElement('section');
  row.className = 'home-row';
  const head = document.createElement('div');
  head.className = 'home-row-head';
  const h = document.createElement('h2');
  h.textContent = title;
  head.append(h);
  if (seeAll) {
    const more = document.createElement('button');
    more.type = 'button';
    more.className = 'linkish home-see-all';
    more.textContent = 'See all';
    more.addEventListener('click', seeAll);
    head.append(more);
  }
  const strip = document.createElement('div');
  strip.className = 'home-strip';
  strip.append(...cards);
  row.append(head, strip);
  return row;
}

// An album on Home opens the same album page as under Music, whose back
// button then leads to the albums.
function albumCardFromHome(album) {
  const card = albumCard(album);
  card.replaceWith();
  const clone = card.cloneNode(true);
  clone.addEventListener('click', () => {
    state.musicView = 'albums';
    for (const chip of document.querySelectorAll('#filters .chip')) {
      chip.classList.toggle('active', chip.dataset.kind === 'music');
    }
    state.kind = 'music';
    noteTabKind();
    renderSearchHint();
    show($('home-view'), false);
    show($('results-bar'), true);
    show($('results'), false);
    show($('music-tabs'), true);
    markMusicTabs();
    show($('music-view'), true);
    showAlbum(album.sourceId, album.id);
  });
  return clone;
}

/* ------------------------------------------ swiping between a tab's pills */

// Wherever a row of pills picks what is on the page - Music's Mixes, Songs,
// Albums, Artists and Playlists; Books' audiobooks, ebooks and documents;
// Watch's films and TV - a sideways swipe steps to the neighbouring pill.
// The page follows the finger, and on letting go either carries on off the
// side while the next one slides in, or springs back. Not on an album,
// artist or playlist page (those have a back button and the swipe would
// throw the page away), and not from something that scrolls sideways
// itself, like a strip of mixes.
(function pillSwipe() {
  const pages = ['album-sort', 'continue', 'results-bar', 'results', 'music-view', 'playlists-view'];
  let g = null;      // the gesture under way
  let busy = false;  // a switch is animating

  const pills = () => {
    const row = ['music-tabs', 'subtabs'].map($).find((el) => !el.classList.contains('hidden'));
    return row ? [...row.querySelectorAll('button')].filter((b) => !b.classList.contains('hidden')) : [];
  };

  function scrollsSideways(el) {
    for (; el && el !== document.body; el = el.parentElement) {
      if (el.scrollWidth > el.clientWidth + 1) {
        const overflow = getComputedStyle(el).overflowX;
        if (overflow === 'auto' || overflow === 'scroll') return true;
      }
      if (el.matches('input, select, textarea')) return true;
    }
    return false;
  }

  // The whole page below the header counts, including the empty space under
  // a short list - but not the header, the pills, the tab bar, the player or
  // anything laid over the page.
  function eligible(target) {
    if (busy || $('app').classList.contains('hidden') || $('app').classList.contains('viewing-account')) return false;
    if (pills().length < 2) return false;
    const onPage = target === document.body || target === document.documentElement || target.closest('#app');
    if (!onPage || target.closest('header, #music-tabs, #subtabs, #album-sort, #tabs')) return false;
    if (pages.some((id) => !$(id).classList.contains('hidden') && $(id).querySelector(':scope > .back'))) return false;
    return !scrollsSideways(target);
  }

  function place(x, opacity, ms, easing) {
    for (const id of pages) {
      const el = $(id);
      el.style.transition = ms ? `transform ${ms}ms ${easing}, opacity ${ms}ms ${easing}` : 'none';
      el.style.transform = x ? `translateX(${x}px)` : '';
      el.style.opacity = opacity === 1 ? '' : String(opacity);
    }
  }
  const settle = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const frame = () => new Promise((resolve) => requestAnimationFrame(() => resolve()));

  // The next pill's page arrives by fetch. Wait until the page has stopped
  // changing for a moment (a list is often cleared, then filled), at most
  // half a second, so what slides in is the page and not a blank.
  function whenDrawn() {
    return new Promise((resolve) => {
      let quiet;
      const done = () => { observer.disconnect(); clearTimeout(quiet); clearTimeout(cap); resolve(); };
      const observer = new MutationObserver(() => { clearTimeout(quiet); quiet = setTimeout(done, 50); });
      for (const id of pages) observer.observe($(id), { childList: true, subtree: true, attributes: true, attributeFilter: ['class'] });
      const cap = setTimeout(done, 450);
      quiet = setTimeout(done, 100);
    });
  }

  // The page that is leaving is a copy laid where it was, so it can carry
  // on off the side while the real one - already the next pill's - follows
  // right behind it, edge to edge, as the pages of one strip.
  function ghostOf(dx) {
    const shown = pages.map($).filter((el) => !el.classList.contains('hidden'));
    const rects = shown.map((el) => el.getBoundingClientRect());
    // The next page starts at its top, as pressing the pill would show it.
    window.scrollTo(0, 0);
    const app = $('app').getBoundingClientRect();
    const ghost = document.createElement('div');
    ghost.className = 'swipe-ghost';
    ghost.inert = true;
    ghost.setAttribute('aria-hidden', 'true');
    shown.forEach((el, i) => {
      const copy = el.cloneNode(true);
      copy.style.cssText = '';
      copy.style.position = 'absolute';
      copy.style.margin = '0';
      copy.style.left = `${rects[i].left - dx - app.left}px`;
      copy.style.top = `${rects[i].top - app.top}px`;
      copy.style.width = `${rects[i].width}px`;
      ghost.append(copy);
    });
    ghost.style.transform = `translateX(${dx}px)`;
    $('app').append(ghost);
    return ghost;
  }

  const ease = 'cubic-bezier(.2,.7,.2,1)';
  const slide = (ghost, x, ms) => {
    ghost.style.transition = ms ? `transform ${ms}ms ${ease}` : 'none';
    ghost.style.transform = `translateX(${x}px)`;
  };

  // The swipe has gone sideways towards a neighbour: switch to it now, so
  // its page is loading - and usually there - while the finger is still
  // down, riding beside the copy of this one.
  function begin(target, dir) {
    g.target = target;
    g.dir = dir;
    g.scroll = window.scrollY;
    g.ghost = ghostOf(0);
    place(dir * window.innerWidth, 1, 0);
    g.drawn = whenDrawn();
    target.click();
  }

  function follow(dx) {
    const width = window.innerWidth;
    // Towards the neighbour the pages follow the finger; the other way the
    // page gives only a little.
    const d = Math.sign(dx) === -g.dir ? dx : dx / 4;
    g.dx = d;
    slide(g.ghost, d, 0);
    place(d + g.dir * width, 1, 0);
  }

  async function finish(swipe, commit) {
    busy = true;
    const width = window.innerWidth;
    const { ghost, dir, dx, drawn, from, scroll } = swipe;
    if (commit) {
      await drawn;
      const ms = Math.round(140 + 160 * (1 - Math.abs(dx) / width));
      slide(ghost, -dir * width, ms);
      place(0, 1, ms, ease);
      await settle(ms + 20);
      ghost.remove();
      place(0, 1, 0);
    } else {
      // Not far enough: this page springs back, and the pill it was on is
      // pressed again behind it.
      slide(ghost, 0, 200);
      place(dir * width, 1, 200, ease);
      await settle(210);
      await drawn;
      const back = whenDrawn();
      from.click();
      await back;
      ghost.remove();
      place(0, 1, 0);
      window.scrollTo(0, scroll);
    }
    busy = false;
  }

  document.addEventListener('touchstart', (event) => {
    g = null;
    if (event.touches.length !== 1 || !eligible(event.target)) return;
    const t = event.touches[0];
    g = { x: t.clientX, y: t.clientY, at: Date.now(), lock: null, dx: 0, samples: [] };
  }, { passive: true });

  document.addEventListener('touchmove', (event) => {
    if (!g || event.touches.length !== 1) return;
    const t = event.touches[0];
    const dx = t.clientX - g.x;
    const dy = t.clientY - g.y;
    if (!g.lock) {
      if (Math.max(Math.abs(dx), Math.abs(dy)) < 10) return;
      g.lock = Math.abs(dx) > Math.abs(dy) * 1.2 ? 'x' : 'y';
      if (g.lock === 'y') { g = null; return; }
      const list = pills();
      const at = list.findIndex((b) => b.classList.contains('active'));
      g.from = list[at];
      const target = dx < 0 ? list[at + 1] : at > 0 ? list[at - 1] : null;
      if (at >= 0 && target) begin(target, dx < 0 ? 1 : -1);
    }
    if (event.cancelable) event.preventDefault();
    g.samples.push({ x: t.clientX, at: Date.now() });
    if (g.samples.length > 5) g.samples.shift();
    if (g.target) {
      follow(dx);
    } else {
      // Past the first or last pill the page gives, but only a little.
      g.dx = dx / 4;
      place(g.dx, 1, 0);
    }
  }, { passive: false });

  function release() {
    const swipe = g;
    g = null;
    if (!swipe || swipe.lock !== 'x') return;
    if (!swipe.target) { place(0, 1, 220, 'cubic-bezier(0,0,.2,1)'); return; }
    const { dx, samples, dir } = swipe;
    const first = samples[0];
    const last = samples[samples.length - 1];
    const speed = first && last && last.at > first.at ? (last.x - first.x) / (last.at - first.at) : 0;
    const towards = Math.sign(dx) === -dir;
    const far = Math.abs(dx) > window.innerWidth * 0.3;
    const flung = Math.abs(dx) > 30 && Math.abs(speed) > 0.35 && Math.sign(speed) === -dir;
    finish(swipe, towards && (far || flung));
  }
  document.addEventListener('touchend', release, { passive: true });
  document.addEventListener('touchcancel', () => {
    const swipe = g;
    g = null;
    if (swipe && swipe.target) finish(swipe, false);
    else place(0, 1, 200, 'ease-out');
  }, { passive: true });
})();
