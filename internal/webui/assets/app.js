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
  // The search box is not focused on opening: on a phone that pops the
  // keyboard over the library, and it drew a highlight round the box.
  applyLibraryTabs();
  renderTabs();
  renderAccount();
  await loadFavoriteKeys();
  await loadPrefs();
  if (/\.soundstorm\.dev$/.test(location.hostname)) keepShell();
  refreshPairs();
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
  if (me.owner) {
    loadPeople();
    refreshRemote();
    refreshLyricsSetting();
  }
  refreshDownloadsCard();
  refreshScrobbling();
  if (!me.owner) {
    show($('lyrics-block'), false);
    show($('readalong-block'), false);
  }
}

// The lyrics setting is on the owner's session only, and only when the server
// can look lyrics up at all.
async function refreshLyricsSetting() {
  const { ok, body } = await api('/api/session');
  const has = ok && body && typeof body.onlineLyrics === 'boolean';
  show($('lyrics-block'), has);
  if (has) $('lyrics-toggle').checked = body.onlineLyrics;
  // Read-along's setting rides on the same answer: owner only, and only where
  // read-along is set up.
  const discovery = ok && body && typeof body.onlineDiscovery === 'boolean';
  show($('discovery-block'), discovery);
  if (discovery) $('discovery-toggle').checked = body.onlineDiscovery;
  const along = ok && body && typeof body.autoReadAlong === 'boolean';
  show($('readalong-block'), along);
  if (along) $('readalong-toggle').checked = body.autoReadAlong;
}

$('readalong-toggle').addEventListener('change', async (event) => {
  const enabled = event.target.checked;
  const { ok, body } = await api('/api/settings/readalong', { method: 'PUT', body: JSON.stringify({ enabled }) });
  if (!ok) {
    event.target.checked = !enabled;
    note($('readalong-note'), (body && body.error) || 'Could not change it.', true);
    return;
  }
  note($('readalong-note'), enabled
    ? 'On. Books on both shelves are synced one at a time, starting now.'
    : 'Off. Sync a book yourself from Books, Read Along.', false);
});

$('discovery-toggle').addEventListener('change', async (event) => {
  const enabled = event.target.checked;
  const { ok, body } = await api('/api/settings/discovery', { method: 'PUT', body: JSON.stringify({ enabled }) });
  if (!ok) {
    event.target.checked = !enabled;
    note($('discovery-note'), (body && body.error) || 'Could not change it.', true);
    return;
  }
  note($('discovery-note'), enabled
    ? 'On. Open an artist to see their bio and the artists like them.'
    : 'Off. Artist mixes go back to mixing in songs of the same genre.', false);
});

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
  if (opening) enterSettingsSearch();
  else leaveSettingsSearch();
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
  storyteller: 'Read-along',
  audiomuse: 'Sound analysis',
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
  if (state.settingsSearching) applySettingsView();
  else runSearch();
});

let debounce = null;
$('search-input').addEventListener('input', () => {
  clearTimeout(debounce);
  if (state.settingsSearching) { applySettingsView(); return; }
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
    const fromTab = state.tab;
    state.kind = chip.dataset.kind;
    noteTabKind();
    // Nothing of the page being left comes along: not a selection, not an
    // open menu, and not a search from another tab - "dune" typed in Books
    // used to filter Music too. Between a tab's own pills the search stays,
    // so it can be asked of audiobooks and ebooks alike.
    if (state.selecting) setSelecting(false);
    closeItemMenu();
    if (state.tab !== fromTab && !state.settingsSearching) $('search-input').value = '';
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
  state.detailPage = false;

  if (state.offline) {
    const seq = ++state.searchSeq;
    renderSearchHint();
    await showOfflineShelf(seq, query);
    return;
  }

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
  const own = state.kind === 'favorites' || state.kind === 'playlists' || state.kind === 'pairs'
    || Boolean(FAV_KINDS[state.kind]) || BOOK_BROWSE.has(state.kind);
  renderSearchHint();
  const musicBrowse = state.kind === 'music' && state.musicView !== 'songs'
    && !(state.musicView === 'mixes' && state.query);
  show($('music-tabs'), ['music', 'playlists', 'fav-music', 'genres-music'].includes(state.kind));
  markMusicTabs();
  const bookBrowse = BOOK_BROWSE.has(state.kind);
  show($('music-view'), musicBrowse || bookBrowse);
  show($('playlists-view'), state.kind === 'playlists');
  const home = state.kind === '' && !query;
  // Another page - a tab, a pill, back from an album - shows nothing of the
  // last one while it loads: the old list sitting there read as the new one.
  // Typing on the same page keeps the results until the new ones arrive.
  const page = `${state.kind}|${state.kind === 'music' ? state.musicView : ''}|${home}`;
  const fresh = page !== state.shownPage;
  if (fresh) {
    state.shownPage = page;
    for (const id of ['results', 'music-view', 'playlists-view', 'home-view']) $(id).replaceChildren();
    $('status').textContent = '';
  }
  show($('home-view'), home);
  show($('results-bar'), !home);
  show($('results'), state.kind !== 'playlists' && !musicBrowse && !bookBrowse && !home);
  if (fresh) {
    if (home) showSkeleton($('home-view'), 'home');
    else if (musicBrowse || bookBrowse) {
      showSkeleton($('music-view'), 'grid', state.musicView === 'artists' || state.kind === 'authors' || state.kind === 'people');
    } else if (state.kind === 'playlists') showSkeleton($('playlists-view'), 'grid');
    else showSkeleton($('results'), 'grid');
  }
  if (home) {
    state.hasMore = false;
    await renderHome(seq);
    return;
  }
  if (musicBrowse) {
    state.hasMore = false;
    await showMusicView(seq);
    return;
  }
  if (state.kind === 'favorites' || FAV_KINDS[state.kind]) {
    await showFavorites(seq, FAV_KINDS[state.kind]);
    return;
  }
  if (bookBrowse) {
    state.hasMore = false;
    if (GENRE_KINDS[state.kind]) await showGenres(seq);
    else await (PHOTO_BROWSE.has(state.kind) ? showPhotoBrowse(seq) : showBookBrowse(seq));
    return;
  }
  if (state.kind === 'pairs') {
    await showPairs(seq, query);
    return;
  }
  if (state.kind === 'playlists') {
    $('status').textContent = '';
    state.hasMore = false;
    await showPlaylists();
    return;
  }
  $('status').textContent = '';
  if (!$('results').children.length) showSkeleton($('results'), 'grid');

  // Back to the top of the list. Anything already on screen belongs to the
  // previous query and must not be appended to.
  state.offset = 0;
  state.hasMore = false;

  const params = new URLSearchParams({ q: query, limit: String(PAGE_SIZE) });
  if (state.kind) params.set('kind', state.kind);

  const { ok, body, offline } = await api(`/api/search?${params}`);
  if (seq !== state.searchSeq) return;

  if (offline && hasDownloads()) {
    enterOffline();
    return;
  }
  if (!ok || !body) {
    $('results').replaceChildren();
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
    // No count: the items themselves are on screen.
    $('status').textContent = '';
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
    if (state.offline) offlineArtURL(item).then((u) => { img.src = u || NO_COVER; });
    else img.src = art;
    img.alt = '';
    img.loading = 'lazy';
    img.decoding = 'async';
    // A backend can have an artwork id but no actual file; fall back rather
    // than showing a broken image.
    img.addEventListener('error', () => img.replaceWith(fallbackArt(item)));
    wrap.append(img);
  } else {
    wrap.append(fallbackArt(item));
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
  card.soundstormItem = item;
  card.classList.toggle('selected', state.selected.has(key));
  if (isDownloaded(item)) wrap.append(downloadedBadge());
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
  if (state.favorites.has(key)) wrap.classList.add('is-favorite');

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
  // Nothing else over the cover: every option is in the menu a hold (or a
  // right-click) opens. Only the green tick for a download is drawn on it.
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
    case 'tv':
      // A show opens its page; an episode plays.
      if (item.extra && item.extra.type === 'Series') showShow(item);
      else playVideo(item);
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
  if (isDownloaded(item)) {
    offlineURLFor(photoPreview(item)).then((u) => { if (u && photoShown === item) img.src = u; });
  }
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

  const center = () => {
    const r = stage.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  };
  // Points relative to the middle of the stage, which is where the photo's
  // transform is anchored.
  const rel = (e) => { const c = center(); return { x: e.clientX - c.x, y: e.clientY - c.y }; };
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
  const neighbors = () => {
    const photos = photosOnScreen();
    const at = photos.findIndex((p) => p.id === photoShown.id && p.sourceId === photoShown.sourceId);
    return { '-1': at > 0 ? photos[at - 1] : null, 1: at >= 0 ? photos[at + 1] : null };
  };
  const prepSides = () => {
    const near = neighbors();
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
        // The neighbor slides into the middle; then it becomes the photo.
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
  // Music or an audiobook carries on: reading to something is the point,
  // and the player stays on top of the book (see .reader in the CSS).
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

async function playVideo(item, options = {}) {
  stopAudio();
  detachHls();
  hideUpNext();
  startWatching(item);

  const player = $('video-player');
  $('video-caption').textContent = [item.title, subtitleFor(item)]
    .filter(Boolean)
    .join(' — ');
  show($('video-overlay'), true);

  // Downloaded: from the device, connection or not.
  if (isDownloaded(item) && (await playKeptVideo(item, player))) return;

  // Ask before building a player: the answer decides which one to build.
  const audioQuery = options.audio !== undefined ? `?audio=${options.audio}` : '';
  const { ok, body } = await api(
    `/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}${audioQuery}`);
  const mode = ok && body ? body.mode : 'direct';
  const url = ok && body && body.url ? body.url : streamPath(item);

  attachSubtitles(player, (ok && body && body.subtitles) || []);
  attachAudioChoice(item, (ok && body && body.audio) || [], options.audio);

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
  hideUpNext();
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
$('video-player').addEventListener('ended', () => {
  const watching = state.watching;
  if (watching && watching.item.kind === 'tv') offerNextEpisode(watching.item);
});
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
  if (item && item.kind === 'music' && state.scrobbling && !state.offline) {
    // Their ListenBrainz profile shows what is playing; the play itself is
    // sent by the server once it counts.
    api('/api/scrobble/now', { method: 'POST', body: JSON.stringify({ source: item.sourceId, id: item.id }) });
  }
  // Anything started by hand ends a playlist; the queue only carries on
  // through its own songs.
  if (!fromQueue) audio.queue = null;
  closeVideo();
  // Whatever was playing is being abandoned; record where it got to before
  // the state that describes it is overwritten.
  savePosition();

  audio.item = item;
  audio.tracks = [];
  audio.urlMap = null;
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

  // A downloaded audiobook plays from the device whether or not the server
  // answered, from the file list kept with it, and from the place it was last
  // listened to here if that never reached the server.
  if (item.kind === 'audiobook' && isDownloaded(item)) {
    const kept = state.downloads.items[selectionKey(item)];
    const dl = (kept && kept.dl) || {};
    if (!Array.isArray(info.tracks) && Array.isArray(dl.tracks)) info.tracks = dl.tracks;
    const map = {};
    for (const url of dl.files || []) {
      const local = await offlineURLFor(url);
      if (local) map[url] = local;
    }
    if (audio.item !== item) return;
    audio.urlMap = map;
    const here = localPosition(item);
    if (here && !here.synced) {
      info.position = { ...(info.position || {}), seconds: here.seconds, finished: false };
    } else if (!info.position && here) {
      info.position = { seconds: here.seconds, finished: false };
    }
    if (!info.position) info.position = { seconds: 0, finished: false };
  }

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
  // A downloaded audiobook plays from the device: its files' server
  // addresses map to copies kept here.
  player.src = (audio.urlMap && audio.urlMap[url]) || url;

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

// For read-along in the reader (a module, so it asks through window): where
// this audiobook is on its whole timeline, or null when something else plays.
window.soundstormListening = (sourceId, id) =>
  audio.item && audio.item.sourceId === sourceId && audio.item.id === id ? elapsed() : null;

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
  const kept = isDownloaded(item);
  if (kept) keepLocalPosition(item, seconds, false);
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
  }).then((resp) => {
    if (kept && resp.ok) keepLocalPosition(item, seconds, true);
  }).catch(() => {
    // Losing a position is not worth interrupting somebody's book over. For a
    // downloaded book it is kept here, and sent with the next save that
    // reaches the server.
  });
}

// Where somebody got to in a downloaded audiobook, on this device: sent to
// the server when it can be, and used when the server has not heard of it.
function localPositionKey(item) {
  return `soundstorm-pos:${selectionKey(item)}`;
}
function localPosition(item) {
  try {
    const v = JSON.parse(localStorage.getItem(localPositionKey(item)) || 'null');
    return v && Number.isFinite(v.seconds) ? v : null;
  } catch {
    return null;
  }
}
function keepLocalPosition(item, seconds, synced) {
  try {
    localStorage.setItem(localPositionKey(item), JSON.stringify({ seconds, synced, at: Date.now() }));
  } catch { /* storage full */ }
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
// of every screen is what stopped the library card looking vertically centered
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
      $('intake-title').textContent = 'Canceled — nothing was added.';
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
    request.addEventListener('abort', () => resolve({ ok: false, error: 'canceled' }));
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
    const detail = document.createElement('p');
    detail.className = 'muted';
    if (navigator.onLine === false) {
      // The device itself has no connection: say so plainly, and what does
      // work without one.
      message.textContent = 'You are offline.';
      detail.textContent =
        'SoundStorm needs a connection to your server. Songs you download play '
        + 'without one: open an album or playlist and choose Download.';
    } else {
      message.textContent = 'Cannot reach SoundStorm.';
      detail.textContent =
        'Check that the computer running it is on, then try again. If your '
        + 'browser warns that the connection is not private, that is expected '
        + 'after SoundStorm is reinstalled: choose Advanced, then continue.';
    }
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
  if (!on && state.menuFor === 'selection') closeItemMenu();
  resetSelectBar();
}

function toggleSelected(item, card) {
  const key = selectionKey(item);
  if (state.selected.has(key)) state.selected.delete(key);
  else state.selected.set(key, item);
  card.classList.toggle('selected', state.selected.has(key));
  if (!state.selected.size) {
    setSelecting(false);
    return;
  }
  resetSelectBar();
}

// The selection's menu: the same menu as one item's, from the bottom of the
// screen, with only what can be done to many at once. It stays open while
// selecting, so more can be tapped in or out, and says how many.
// Nothing at the foot of the screen while selecting: the ticks say what is
// selected, and the menu for them is a hold on any selected one away (see
// attachItemMenuGestures). A menu already open follows the selection.
function resetSelectBar() {
  if (state.menuFor === 'selection' && !$('item-menu').classList.contains('hidden')) renderSelectMenu();
}

// The menu for what is selected, opened by holding one of them: one alone
// gets its full menu, as a hold always gave; several get what can be done to
// many at once.
function openSelectionMenu(anchor) {
  const items = [...state.selected.values()];
  const menu = $('item-menu');
  state.menuAnchor = anchor;
  if (items.length === 1) {
    state.menuFor = items[0];
    state.menuOpts = { fromSelection: true };
    renderMainMenu(items[0], state.menuOpts);
  } else {
    state.menuFor = 'selection';
    state.menuOpts = {};
    renderSelectMenu();
  }
  placeMenu(menu, anchor);
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

function selectMenuBack(label) {
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const text = document.createElement('span');
  text.textContent = label;
  back.append(text);
  back.addEventListener('click', () => renderSelectMenu());
  return back;
}

function renderSelectMenu() {
  const box = $('item-menu');
  const items = [...state.selected.values()];
  const n = items.length;
  const head = document.createElement('div');
  head.className = 'menu-head';
  const title = document.createElement('strong');
  title.textContent = n ? `${n} selected` : 'Nothing selected';
  const sub = document.createElement('span');
  sub.textContent = 'Tap more to add them, or tap one to take it off';
  head.append(title, sub);
  const entries = [head];
  const songs = n > 0 && items.every((it) => it.kind === 'music');
  if (songs) {
    entries.push(menuItem('next', 'Play next', () => {
      // One after another in the order shown, each after the last.
      for (const it of [...items].reverse()) queuePlayNext(it);
      setSelecting(false);
      showToast(`${n} song${n === 1 ? '' : 's'} play next.`);
    }));
    entries.push(menuItem('queue', 'Add to queue', () => {
      for (const it of items) queueAdd(it);
      setSelecting(false);
      showToast(`Added ${n} song${n === 1 ? '' : 's'} to the queue.`);
    }));
    entries.push(menuItem('playlist', 'Add to playlist', async () => {
      const { ok, body } = await api('/api/playlists');
      renderSelectPlaylists(items, (ok && body && body.playlists) || []);
    }, { chevron: true }));
  }
  if (n) {
    const allFaved = items.every((it) => state.favorites.has(selectionKey(it)));
    entries.push(menuItem('heart', allFaved ? 'Remove from favorites' : 'Add to favorites', async () => {
      const on = !allFaved;
      let done = 0;
      for (const it of items) {
        if (state.favorites.has(selectionKey(it)) === on) continue;
        if (!(await setFavorite(it, on))) done++;
      }
      setSelecting(false);
      showToast(on ? `Added ${done} to your favorites.` : `Took ${done} off your favorites.`);
    }, { filled: allFaved, className: 'menu-favorite' }));
  }
  const downloadable = items.filter((it) => !isDownloaded(it) && (it.kind === 'music' || canDownload(it)));
  if (downloadable.length && downloadsPossible()) {
    entries.push(menuItem('download', 'Download', () => {
      setSelecting(false);
      bulkDownload(`${downloadable.length} item${downloadable.length === 1 ? '' : 's'}`, async () => downloadable
        .filter((it) => !isDownloaded(it))
        .map((it) => (it.kind === 'music'
          ? {
            label: it.title,
            run: (progress, stopped) => download({
              id: `song:${selectionKey(it)}`, type: 'song', title: it.title,
              subtitle: (it.creators || []).join(', '), sourceId: it.sourceId, artId: it.artId,
            }, [it], (k, total) => progress(k / total), stopped),
          }
          : bookTask(it))));
    }));
  }
  const kept = items.filter((it) => isDownloaded(it));
  if (kept.length) {
    entries.push(menuItem('download', 'Remove downloads', async () => {
      setSelecting(false);
      for (const it of kept) await removeItemDownload(it);
      showToast(`Removed ${kept.length} from this device.`);
    }));
  }
  entries.push(menuItem('check', 'Select all shown', () => {
    for (const item of state.items || []) state.selected.set(selectionKey(item), item);
    for (const card of $('results').querySelectorAll('.item')) card.classList.add('selected');
    resetSelectBar();
  }));
  if (n && state.me && state.me.owner && !state.offline) {
    entries.push(menuItem('trash', 'Delete from library', () => renderSelectDelete(), { className: 'menu-danger' }));
  }
  entries.push(menuItem('close', 'Clear selection', () => setSelecting(false)));
  box.replaceChildren(...entries);
}

function renderSelectPlaylists(items, lists) {
  const box = $('item-menu');
  const note = menuNote();
  const add = async (id) => {
    let failed = '';
    for (const it of items) {
      const { ok, body } = await api(`/api/playlists/${encodeURIComponent(id)}/items`, {
        method: 'POST', body: JSON.stringify({ source: it.sourceId, id: it.id }),
      });
      if (!ok) { failed = (body && body.error) || 'Could not add them all.'; break; }
    }
    if (failed) {
      note.textContent = failed;
      show(note, true);
      return;
    }
    setSelecting(false);
    showToast(`Added ${items.length} song${items.length === 1 ? '' : 's'} to the playlist.`);
  };
  const list = document.createElement('div');
  list.className = 'menu-scroll';
  list.append(...lists.map((pl) => menuItem('playlist', pl.name, () => add(pl.id), { detail: `${pl.count}` })));
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
    const { ok, body } = await api('/api/playlists', { method: 'POST', body: JSON.stringify({ name: input.value }) });
    if (!ok || !body) {
      note.textContent = (body && body.error) || 'Could not make it.';
      show(note, true);
      return;
    }
    await add(body.id);
  });
  box.replaceChildren(selectMenuBack('Add to playlist'), list, form, note);
}

// Delete: the server says exactly what would go, the owner confirms, and it
// goes to the bin for thirty days with an Undo in the message at the bottom.
async function renderSelectDelete() {
  const box = $('item-menu');
  const text = document.createElement('p');
  text.className = 'menu-confirm';
  text.textContent = 'Working out what that would delete\u2026';
  box.replaceChildren(selectMenuBack('Delete from library'), text);
  const payload = selectedPayload();
  const { ok, body } = await api('/api/delete/preview', { method: 'POST', body: payload });
  if (!ok || !body) {
    text.textContent = (body && body.error) || 'Could not work out what that would delete.';
    return;
  }
  const n = body.items;
  text.textContent = `Delete ${n} item${n === 1 ? '' : 's'}? ${body.files} file${body.files === 1 ? '' : 's'}, `
    + `${formatBytes(body.bytes)}. They stay in the bin for 30 days.`;
  const confirm = menuItem('trash', `Delete ${n}`, async () => {
    confirm.disabled = true;
    const deleted = [...state.selected.keys()];
    const result = await api('/api/delete', { method: 'POST', body: payload });
    if (!result.ok || !result.body) {
      text.textContent = (result.body && result.body.error) || 'Could not delete.';
      confirm.disabled = false;
      return;
    }
    for (const key of deleted) {
      const card = $('results').querySelector(`.item[data-key="${CSS.escape(key)}"]`);
      if (card) (card.closest('.item-holder') || card).remove();
    }
    state.items = (state.items || []).filter((item) => !deleted.includes(selectionKey(item)));
    setSelecting(false);
    const entry = result.body.entry;
    showToast(`Deleted ${n} item${n === 1 ? '' : 's'}.`, 'Undo', async () => {
      const undone = await api('/api/delete/undo', { method: 'POST', body: JSON.stringify({ entry }) });
      if (!undone.ok) {
        showToast((undone.body && undone.body.error) || 'Could not undo.');
        return;
      }
      showToast('They are back.');
      setTimeout(runSearch, 3000);
    }, 12000);
  }, { className: 'menu-danger' });
  box.replaceChildren(selectMenuBack('Delete from library'), text, confirm,
    menuItem('close', 'Cancel', () => renderSelectMenu()));
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
  if (!browsing || !kindFits || state.detailPage) {
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
  // Checked again: an album may have opened while this was on its way.
  show(section, items.length > 0 && !state.query && !state.selecting && !state.detailPage);
}

window.addEventListener('soundstorm:reader-closed', () => setTimeout(refreshContinue, 300));

/* ---------------------------------------------------- favorites, playlists */

// Each person's own, kept by SoundStorm (see internal/collections). The set
// of favorite keys is loaded once and kept current, so a card can show its
// heart and the menu can say "remove" without asking the server per card.
state.favorites = new Set();

async function loadFavoriteKeys() {
  const { ok, body } = await api('/api/favorites');
  if (!ok || !body) return [];
  state.favorites = new Set(body.items.map(selectionKey));
  return body.items;
}

async function showFavorites(seq, kinds) {
  $('status').textContent = '';
  if (!$('results').children.length) showSkeleton($('results'), 'grid');
  // In a tab, only what belongs to it: Watch's favorites are films and TV.
  const items = (await loadFavoriteKeys()).filter((item) => !kinds || kinds.includes(item.kind));
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
    ? ''
    : `Nothing here yet. ${MENU_HOW} anything and choose Add to favorites.`;
  show($('loading-more'), false);
}

async function setFavorite(item, on) {
  const q = new URLSearchParams({ source: item.sourceId, id: item.id });
  const { ok, body } = await api(`/api/favorites?${q}`, { method: on ? 'PUT' : 'DELETE' });
  if (!ok) return (body && body.error) || 'Could not change that.';
  const key = selectionKey(item);
  if (on) state.favorites.add(key);
  else state.favorites.delete(key);
  for (const card of document.querySelectorAll(`.item[data-key="${CSS.escape(key)}"]`)) {
    card.querySelector('.art-wrap').classList.toggle('is-favorite', on);
    const heart = card.closest('.item-holder') && card.closest('.item-holder').querySelector('.fav-toggle');
    if (heart && heart.soundstormPaint) heart.soundstormPaint(on);
  }
  // Taken off the list while looking at the list: it goes.
  if (!on && state.kind === 'favorites') runSearch();
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
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7.5v.5"/>',
  forward: '<path d="M9 6l6 6-6 6"/>',
  home: '<path d="M4 11l8-7 8 7M6 9.5V20h4.5v-6h3v6H18V9.5"/>',
  note: '<path d="M9 18V6l10-2v12M9 18a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0zM19 16a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0z"/>',
  film: '<path d="M4 6h16v12H4zM4 10h16M8 6l-1.5 4M13 6l-1.5 4M18 6l-1.5 4"/>',
  book: '<path d="M5 5.5A2.5 2.5 0 0 1 7.5 3H19v15H7.5A2.5 2.5 0 0 0 5 20.5zM5 20.5A2.5 2.5 0 0 1 7.5 18H19v3H7.5"/>',
  check: '<path d="M5 12.5l4.5 4.5L19 7"/>',
  trash: '<path d="M4 7h16M9 7V4.5h6V7M6.5 7l1 13h9l1-13M10 11v6M14 11v6"/>',
  link: '<path d="M10 14a4 4 0 0 0 5.66 0l3-3a4 4 0 0 0-5.66-5.66l-1 1M14 10a4 4 0 0 0-5.66 0l-3 3a4 4 0 0 0 5.66 5.66l1-1"/>',
  headphones: '<path d="M4 15v-3a8 8 0 0 1 16 0v3M4 15a2 2 0 0 1 2-2h1v7H6a2 2 0 0 1-2-2zM20 15a2 2 0 0 0-2-2h-1v7h1a2 2 0 0 0 2-2z"/>',
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
  radio: '<path d="M12 12h.01M8.5 8.5a5 5 0 0 0 0 7M15.5 8.5a5 5 0 0 1 0 7M5.6 5.6a9 9 0 0 0 0 12.8M18.4 5.6a9 9 0 0 1 0 12.8" stroke-width="2.2"/>',
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
  // A lone selected item's menu was the end of selecting it.
  const wasSelection = state.menuOpts && state.menuOpts.fromSelection;
  state.menuOpts = {};
  if (wasSelection && state.selecting) setSelecting(false);
}

function openItemMenu(item, anchor) {
  const menu = $('item-menu');
  if (state.menuFor === item && !menu.classList.contains('hidden')) {
    closeItemMenu();
    return;
  }
  state.menuFor = item;
  state.menuAnchor = anchor;
  state.menuOpts = {};
  renderMainMenu(item);
  placeMenu(menu, anchor);
}

function menuNote() {
  const note = document.createElement('p');
  note.className = 'menu-note hidden';
  return note;
}

function renderMainMenu(item, opts = {}) {
  const menu = $('item-menu');
  const note = menuNote();
  const say = (text) => { note.textContent = text; show(note, Boolean(text)); };
  if (item.kind === 'pair') {
    renderPairMenu(item.pair, menu, note);
    return;
  }
  const faved = state.favorites.has(selectionKey(item));
  const entries = [menuHeader(item)];
  if (opts.nowPlaying) entries.push(...playerMenuItems(item, opts));
  entries.push(
    menuItem('heart', faved ? 'Remove from favorites' : 'Add to favorites', async () => {
      const problem = await setFavorite(item, !faved);
      if (problem) say(problem);
      else closeItemMenu();
    }, { filled: faved, className: 'menu-favorite' }),
  );
  // What is playing needs no radio of its own (Songs like this, above), and
  // is already in the queue.
  if (item.kind === 'music' && !opts.nowPlaying) {
    entries.push(menuItem('radio', 'Start radio', () => {
      closeItemMenu();
      startRadio({ mode: 'song', seed: item.id });
    }));
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
      if (downloaded) await removeItemDownload(item);
      else await download({ id: `song:${selectionKey(item)}`, type: 'song', title: item.title,
        subtitle: (item.creators || []).join(', '), sourceId: item.sourceId, artId: item.artId }, [item]);
    }));
  }
  if (item.kind === 'music' && opts.nowPlaying) {
    const downloaded = isDownloaded(item);
    entries.push(menuItem('download', downloaded ? 'Remove download' : 'Download', async () => {
      closeItemMenu();
      if (downloaded) await removeItemDownload(item);
      else await download({ id: `song:${selectionKey(item)}`, type: 'song', title: item.title,
        subtitle: (item.creators || []).join(', '), sourceId: item.sourceId, artId: item.artId }, [item]);
    }));
  }
  if (item.kind === 'music') {
    entries.push(menuItem('playlist', 'Add to playlist', async () => {
      const { ok, body } = await api('/api/playlists');
      renderPlaylistMenu(item, (ok && body && body.playlists) || []);
    }, { chevron: true }));
  }
  if (canDownload(item)) {
    const downloaded = isDownloaded(item);
    entries.push(menuItem('download', downloaded ? 'Remove download' : 'Download', async () => {
      closeItemMenu();
      if (downloaded) {
        await removeItemDownload(item);
        showToast(`${item.title} removed from this device.`);
      } else {
        const big = item.kind === 'video' || item.kind === 'tv' || (item.extra && item.extra.type === 'video');
        if (big && !window.confirm(`Download "${item.title}"? A film or episode is usually one to a few GB, `
          + 'and one that needs converting takes a while.')) return;
        await downloadWithToast(item.title, (progress) => downloadBook(item, progress));
      }
    }));
  }
  entries.push(menuItem('info', 'Info', (event) => {
    event.stopPropagation();
    renderInfoMenu(item);
  }, { chevron: true }));
  const pairable = (item.kind === 'audiobook'
    || (item.kind === 'ebook' && ((item.extra && item.extra.format) || '').toLowerCase() !== 'pdf'));
  if (pairable && state.me && state.me.owner && !state.offline) {
    const other = item.kind === 'ebook' ? 'audiobook' : 'ebook';
    entries.push(menuItem('link', `Pair with its ${other}`, (event) => {
      event.stopPropagation();
      renderPairPicker(item);
    }, { chevron: true }));
  }
  if (state.me && state.me.owner && !state.offline && item.sourceId !== 'storyteller') {
    // Stopped here: the menu is redrawn at once, and a click reaching the
    // page from a button no longer in the menu reads as a click outside it.
    entries.push(menuItem('trash', 'Delete from library', (event) => {
      event.stopPropagation();
      renderDeleteMenu(item);
    }, { className: 'menu-danger' }));
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
  back.addEventListener('click', () => renderMainMenu(item, state.menuOpts));

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
  // The finger lifting at the end of a hold on Now Playing's cover lands on
  // the dimmed page over it, and is not a click outside the menu it opened.
  if (performance.now() - (state.heldAt || 0) < 700) return;
  // An option that redraws the menu in place (Shuffle, Repeat, a second
  // page) has taken its own button out of the page by the time the click
  // arrives here; it came from inside, not outside.
  if (!event.target.isConnected) return;
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
    favorites: 'your favorites', playlists: 'your playlists', pairs: 'books to read along with',
    authors: 'authors', series: 'series', people: 'people', places: 'places',
    'genres-music': 'genres', 'genres-watch': 'genres', 'genres-books': 'genres',
    'fav-music': 'your favorites', 'fav-watch': 'your favorites',
    'fav-books': 'your favorites', 'fav-photos': 'your favorites',
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
  startLoading(view);
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
    play.append(thumb, words);
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
  // Any queue started by hand ends a station; startRadio sets it again after.
  audio.radio = null;
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
  topUpRadio();
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
const MENU_HOW = state.sheetMenus ? 'Hold down twice on' : 'Right-click';

// attachItemMenuGestures opens a card's menu without a button for it: hold
// down on a touch screen, right-click with a mouse, the menu key on a
// keyboard. The menu opens beside the card.
//
// A hold is a finger resting in place: it is canceled the moment the finger
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
  // In a shelf's list a hold selects: the item is selected, and the finger
  // may slide on across others to select them too, or lift and tap more.
  // A hold on one already selected opens the menu for the selection. Where
  // there is nothing to select (Home's rows, say) a hold opens the menu.
  const inList = () => Boolean(card.closest('#results'));
  card.addEventListener('pointerdown', (event) => {
    if (event.button !== 0 || (event.pointerType === 'mouse' && !inList())) return;
    startX = event.clientX;
    startY = event.clientY;
    card.classList.add('pressing');
    timer = setTimeout(() => {
      timer = null;
      card.classList.remove('pressing');
      state.suppressClick = true;
      if (navigator.vibrate) navigator.vibrate(12);
      holdStarted();
      const anchor = card.querySelector('.art-wrap') || card;
      if (inList() && state.selecting && state.selected.has(selectionKey(item))) {
        openSelectionMenu(anchor);
        followMenuFinger(event.pointerId, startX, startY);
      } else if (inList()) {
        if (!state.selecting) setSelecting(true);
        if (!state.selected.has(selectionKey(item))) toggleSelected(item, card);
        armDragSelect(card, item, event.pointerId, startX, startY);
      } else {
        openItemMenu(item, anchor);
        followMenuFinger(event.pointerId, startX, startY);
      }
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
    cancel();
    // Right-click is a mouse's menu, straight away; Android also reports a
    // long press as one, which the hold above has already answered.
    if (state.holding || state.menuFor) return;
    if (state.selecting && state.selected.has(selectionKey(item))) openSelectionMenu(card.querySelector('.art-wrap') || card);
    else if (!state.selecting) openItemMenu(item, card.querySelector('.art-wrap') || card);
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
    if (tab.dataset.view === 'favorites') {
      selectKind('fav-music');
      markMusicTabs();
      return;
    }
    if (tab.dataset.view === 'genres') {
      selectKind('genres-music');
      markMusicTabs();
      return;
    }
    state.musicView = tab.dataset.view;
    // Albums are A to Z; only New music's See all lists them newest first.
    state.albumOrder = 'name';
    if (state.kind !== 'music') selectKind('music');
    else runSearch();
  });
}

function markMusicTabs() {
  // Offline, Music is what is downloaded: songs, albums, playlists.
  for (const view of ['mixes', 'radio', 'artists', 'favorites', 'genres']) {
    const pill = document.querySelector(`#music-tabs [data-view="${view}"]`);
    if (pill) pill.classList.toggle('hidden', state.offline);
  }
  const put = hiddenPills('music');
  for (const tab of document.querySelectorAll('#music-tabs [data-view]')) {
    tab.classList.toggle('pill-off', put.includes(tab.dataset.view));
  }
  for (const tab of document.querySelectorAll('#music-tabs [data-view]')) {
    const on = state.kind === 'playlists' ? tab.dataset.view === 'playlists'
      : state.kind === 'fav-music' ? tab.dataset.view === 'favorites'
        : state.kind === 'genres-music' ? tab.dataset.view === 'genres' : tab.dataset.view === state.musicView;
    tab.classList.toggle('active', on);
    tab.setAttribute('aria-selected', String(on));
    if (on && !state.pillSwiping) centerPill($('music-tabs'), tab, 'smooth');
  }
}

// The lit pill sits in the middle of its row. pillCenter is where the row
// scrolls to for that; the row's spacers let the first and last pills get
// there too.
function pillCenter(row, pill) {
  const r = row.getBoundingClientRect();
  const p = pill.getBoundingClientRect();
  return row.scrollLeft + (p.left + p.width / 2) - (r.left + r.width / 2);
}

function centerPill(row, pill, behavior) {
  if (!row || !pill || !row.getClientRects().length) return;
  row.scrollTo({ left: pillCenter(row, pill), behavior });
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
  $('status').textContent = '';
  if (!view.children.length) showSkeleton(view, 'grid', state.musicView === 'artists');
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
    view.replaceChildren(...(mixes.length ? [recapBanner()] : []), mixGrid(mixes));
    $('status').textContent = mixes.length ? '' : 'No music yet.';
    return;
  }
  if (state.musicView === 'radio') {
    const { ok, body } = await api('/api/music/radio');
    if (seq !== state.searchSeq) return;
    view.replaceChildren(...radioPage(ok ? body : null));
    $('status').textContent = ok ? '' : 'No music yet.';
    return;
  }
  if (state.musicView === 'albums') {
    const params = q || `order=${encodeURIComponent(state.albumOrder)}`;
    const { ok, body } = await api(`/api/music/albums?${params}`);
    if (seq !== state.searchSeq) return;
    const albums = (ok && body && body.albums) || [];
    view.replaceChildren(albumGrid(albums));
    $('status').textContent = albums.length
      ? ''
      : (state.query ? 'No albums match.' : 'No albums yet.');
    return;
  }
  const { ok, body } = await api(`/api/music/artists?${q}`);
  if (seq !== state.searchSeq) return;
  const artists = (ok && body && body.artists) || [];
  view.replaceChildren(artistGrid(artists));
  $('status').textContent = artists.length
    ? ''
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
    img.decoding = 'async';
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

// initials stand in for a missing cover: the name's first letters on a color
// taken from the name, so the same album is always the same color.
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
  const cover = coverArt(artUrl(album.sourceId, album.artId), album.title);
  if (state.downloads.groups.some((g) => g.id === `album:${album.sourceId}/${album.id}`)) cover.append(downloadedBadge());
  card.dataset.album = `album:${album.sourceId}/${album.id}`;
  card.append(cover, meta);
  card.addEventListener('click', (event) => {
    // The lift at the end of a hold: not a tap, and not a click outside the
    // menu the hold just opened, which would close it.
    if (state.suppressClick) {
      state.suppressClick = false;
      event.stopPropagation();
      return;
    }
    showAlbum(album.sourceId, album.id);
  });
  attachAlbumMenu(card, album);
  return card;
}

// Holding an album (or right-clicking it) opens its menu, as a song's does:
// play, shuffle, its radio, and download. The download arrow on its cover
// went with every other button over covers.
function attachAlbumMenu(card, album) {
  let timer = null;
  let x = 0;
  let y = 0;
  const open = () => openAlbumMenu(album, card.querySelector('.art-wrap') || card);
  const cancel = () => {
    clearTimeout(timer);
    timer = null;
    card.classList.remove('pressing');
  };
  card.addEventListener('pointerdown', (event) => {
    if (event.pointerType === 'mouse' || state.selecting) return;
    x = event.clientX;
    y = event.clientY;
    card.classList.add('pressing');
    timer = setTimeout(() => {
      timer = null;
      card.classList.remove('pressing');
      state.suppressClick = true;
      if (navigator.vibrate) navigator.vibrate(12);
      holdStarted();
      open();
      followMenuFinger(event.pointerId, x, y);
    }, HOLD_MS);
  });
  card.addEventListener('pointermove', (event) => {
    if (timer && Math.hypot(event.clientX - x, event.clientY - y) > HOLD_SLOP) cancel();
  });
  for (const type of ['pointerup', 'pointercancel', 'pointerleave']) card.addEventListener(type, cancel);
  card.addEventListener('contextmenu', (event) => {
    event.preventDefault();
    cancel();
    open();
  });
}

function openAlbumMenu(album, anchor) {
  const menu = $('item-menu');
  state.menuFor = album;
  state.menuAnchor = anchor;
  state.menuOpts = {};
  const songs = async () => {
    const { ok, body } = await api(`/api/music/albums/${encodeURIComponent(album.sourceId)}/${escapeId(album.id)}`);
    return (ok && body && body.songs) || [];
  };
  const id = `album:${album.sourceId}/${album.id}`;
  const downloaded = state.downloads.groups.some((g) => g.id === id);
  const head = menuHeader({ title: album.title, kind: 'music', creators: album.artist ? [album.artist] : [] });
  const entries = [
    head,
    menuItem('play', 'Play', async () => {
      closeItemMenu();
      const list = await songs();
      if (list.length) playQueue(list, 0);
    }),
    menuItem('shuffle', 'Shuffle', async () => {
      closeItemMenu();
      const list = await songs();
      if (list.length) playQueue(shuffled(list), 0);
    }),
    menuItem('radio', 'Album radio', async () => {
      closeItemMenu();
      const list = await songs();
      if (list.length) startRadio({ mode: 'album', seed: list[0].id });
    }),
  ];
  if (downloadsPossible()) {
    entries.push(menuItem('download', downloaded ? 'Remove download' : 'Download', async () => {
      closeItemMenu();
      if (downloaded) {
        await removeDownload(id);
        showToast(`${album.title} removed from this device.`);
      } else {
        const list = await songs();
        if (!list.length) return;
        await downloadWithToast(album.title, (progress) => download(
          { id, type: 'album', title: album.title, subtitle: album.artist, sourceId: album.sourceId, artId: album.artId },
          list, (done, total) => progress(done / total)));
      }
      // The album card's tick: markDownloads knows songs, not albums.
      const have = state.downloads.groups.some((g) => g.id === id);
      for (const wrap of document.querySelectorAll(`.album-card[data-album="${CSS.escape(id)}"] .art-wrap`)) {
        const badge = wrap.querySelector('.dl-badge');
        if (have && !badge) wrap.append(downloadedBadge());
        else if (!have && badge) badge.remove();
      }
    }));
  }
  menu.replaceChildren(...entries);
  placeMenu(menu, anchor);
}

function albumGrid(albums) {
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid';
  grid.append(...albums.map((album) => {
    const holder = document.createElement('div');
    holder.className = 'item-holder';
    holder.append(albumCard(album));
    return holder;
  }));
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
  startLoading(view);
  const { ok, body } = await api(`/api/music/albums/${encodeURIComponent(sourceId)}/${escapeId(id)}`);
  if (!ok || !body) {
    view.replaceChildren(backButton('Albums', () => runSearch()));
    $('status').textContent = 'Could not load that album.';
    return;
  }
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
  if (songs.length && !state.offline) {
    const radio = document.createElement('button');
    radio.type = 'button';
    radio.className = 'ghost';
    radio.textContent = 'Radio';
    radio.title = 'A station that starts from this album and keeps going';
    radio.addEventListener('click', () => startRadio({ mode: 'album', seed: songs[0].id }));
    albumButtons.append(radio);
  }
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
  row.append(number, name);
  row.dataset.key = selectionKey(song);
  if (isDownloaded(song)) name.append(downloadedBadge());
  row.addEventListener('click', (event) => {
    if (state.suppressClick) {
      state.suppressClick = false;
      event.stopPropagation();
      return;
    }
    playQueue(songs, index);
  });
  // The same hold-down / right-click menu as a card: favorites, playlists.
  attachItemMenuGestures(row, song);
  row.soundstormItem = song;
  li.append(row);
  const get = getButton(song, true);
  if (get) li.append(get);
  return li;
}

async function showArtist(sourceId, id) {
  const view = $('music-view');
  startLoading(view);
  const { ok, body } = await api(`/api/music/artists/${encodeURIComponent(sourceId)}/${escapeId(id)}`);
  if (!ok || !body) {
    view.replaceChildren(backButton('Artists', () => runSearch()));
    $('status').textContent = 'Could not load that artist.';
    return;
  }
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
  radio.textContent = 'Artist radio';
  radio.title = 'Their songs, with artists like them, without end';
  radio.addEventListener('click', () => startRadio({ mode: 'artist', seed: artist.name }));
  const keepAll = document.createElement('button');
  keepAll.type = 'button';
  keepAll.className = 'ghost';
  keepAll.append(icon('download'), document.createTextNode(' Download all'));
  keepAll.addEventListener('click', () => bulkDownload(artist.name, async () => [{
    label: artist.name,
    run: async (progress, stopped) => download(
      { id: `artist:${sourceId}/${id}`, type: 'artist', title: artist.name, subtitle: 'Every album',
        sourceId, artId: artist.artId },
      await everySong(), (done, total) => progress(done / total), stopped),
  }]));
  buttons.append(radio, keepAll);
  text.append(kind, name, facts, buttons);
  head.append(photo, text);

  const heading = document.createElement('h3');
  heading.className = 'section-title';
  heading.textContent = 'Albums';
  const about = document.createElement('div');
  about.className = 'artist-about';
  view.replaceChildren(backButton(state.musicView === 'albums' ? 'Albums' : 'Artists', () => runSearch()),
    head, heading, albumGrid(albums.map((a) => ({ ...a, sourceId: a.sourceId || sourceId }))), about);
  window.scrollTo(0, 0);
  showArtistAbout(sourceId, id, about);
}

// Below an artist's albums, with music discovery on: a few lines about them
// and the artists like them that are in the library. Filled in once it has
// been found out, so the page never waits on it.
async function showArtistAbout(sourceId, id, box) {
  const { ok, body } = await api(`/api/music/artists/${encodeURIComponent(sourceId)}/${escapeId(id)}/about`);
  if (!box.isConnected || !ok || !body || !body.enabled) return;
  const parts = [];
  if (body.similar && body.similar.length) {
    const h = document.createElement('h3');
    h.className = 'section-title';
    h.textContent = 'Similar artists in your library';
    const strip = document.createElement('div');
    strip.className = 'home-strip similar-strip';
    for (const a of body.similar) {
      const card = document.createElement('button');
      card.type = 'button';
      card.className = 'item artist-card';
      const meta = document.createElement('div');
      meta.className = 'meta';
      const name = document.createElement('span');
      name.className = 'title';
      name.textContent = a.name;
      meta.append(name);
      card.append(coverArt(artUrl(a.sourceId, a.artId), a.name, true), meta);
      card.addEventListener('click', () => showArtist(a.sourceId, a.id));
      strip.append(card);
    }
    parts.push(h, strip);
  }
  if (body.bio) {
    const h = document.createElement('h3');
    h.className = 'section-title';
    h.textContent = 'About';
    const text = document.createElement('p');
    text.className = 'artist-bio';
    text.textContent = body.bio;
    parts.push(h, text);
    if (body.bioUrl) {
      const more = document.createElement('a');
      more.className = 'artist-bio-link';
      more.href = body.bioUrl;
      more.target = '_blank';
      more.rel = 'noopener noreferrer';
      more.textContent = 'More on Wikipedia';
      parts.push(more);
    }
    // Wikipedia's text is CC BY-SA: it says where it came from and under what terms.
    const credit = document.createElement('p');
    credit.className = 'artist-bio-credit';
    credit.append(document.createTextNode('From Wikipedia, available under '));
    const license = document.createElement('a');
    license.href = 'https://creativecommons.org/licenses/by-sa/4.0/';
    license.target = '_blank';
    license.rel = 'noopener noreferrer';
    license.textContent = 'CC BY-SA 4.0';
    credit.append(license, document.createTextNode('.'));
    parts.push(credit);
  }
  box.replaceChildren(...parts);
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

// Whether a song in Up next is being dragged to a new place (see below).
const queueDrag = { active: false };

// Moves a song still to come from one place in the queue to another.
function queueMove(from, to) {
  const q = audio.queue;
  if (!q || from === to) return;
  if (from <= q.index || to <= q.index || from >= q.items.length || to >= q.items.length) return;
  const [song] = q.items.splice(from, 1);
  q.items.splice(to, 0, song);
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
  // Measured again once it has a size: a title set while it was hidden
  // could not tell whether it fitted.
  requestAnimationFrame(() => fitNpLines());
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
  setStatusBar(null);
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
  // A transition the browser declines (another already running, the page
  // hidden) still applies the change; the refusal is not an error to show.
  const t = document.startViewTransition(update);
  t.ready.catch(() => {});
  t.finished.catch(() => {});
}

// The strip grows into the full lyrics when tapped - a tap there never jumps
// to a line, since the lines are too small a target to aim at. The small
// cover at the top shrinks them back.
$('np-lyrics').addEventListener('click', (event) => {
  // Nothing to grow into without lyrics.
  if (audio.npMode !== 'strip' || !(audio.lyrics && audio.lyrics.lines.length)) return;
  event.stopPropagation();
  event.preventDefault();
  npTransition(() => {
    audio.lyricsBig = true;
    renderLyrics();
  });
}, true);
document.querySelector('#now-playing .np-head').addEventListener('click', () => {
  // On a phone the lyrics and Up next hide the big cover, and with it the
  // buttons on it - Up next's own among them - so the small cover is the way
  // back from either.
  if (!['lyrics', 'queue'].includes(audio.npMode) || !matchMedia('(max-width: 760px)').matches) return;
  npTransition(() => {
    audio.lyricsBig = false;
    audio.showQueue = false;
    renderLyrics();
  });
});

// Songs either side, for swiping: in a queue the ones before and after
// (round to the start on repeat), and in an audiobook the chapters, which
// share the book's cover.
function neighborTrack(by) {
  const q = audio.queue;
  if (q) {
    let at = q.index + by;
    if (by > 0 && at >= q.items.length && audio.repeat !== 'off') at = 0;
    const item = q.items[at];
    return item ? { item } : null;
  }
  const at = audio.index + by;
  return at >= 0 && at < audio.tracks.length ? { item: audio.item } : null;
}

// A swipe changes song outright: back means the song before, not the start
// of this one, since the previous cover is what slid in.
function stepTrack(by) {
  if (by > 0) {
    const q = audio.queue;
    if (q && q.index + 1 >= q.items.length && audio.repeat !== 'off') playQueueAt(0);
    else mediaNext();
  } else if (audio.queue && audio.queue.index > 0) {
    playQueueAt(audio.queue.index - 1);
  } else if (audio.index > 0) {
    selectTrack(audio.index - 1);
    updateMediaSession();
  }
}

// Now Playing's sideways swipe: the cover follows the finger with the next
// and previous covers riding beside it, a small gap apart, and on letting go
// either the neighbor slides into the middle and its song plays, or it all
// springs back. With the lyrics or the queue in the middle (no big cover),
// the title moves instead.
const npSwipe = (() => {
  const GAP = 24;
  const wrap = document.querySelector('.np-cover-wrap');
  const cover = $('np-cover');
  const head = document.querySelector('#now-playing .np-head');
  const sides = {};
  for (const by of [-1, 1]) {
    const img = document.createElement('img');
    img.className = 'np-cover np-cover-side';
    img.alt = '';
    wrap.append(img);
    sides[by] = img;
  }
  let w = 0;
  let near = {};
  let bigCover = false;
  const self = { busy: false };

  // The translate property, not transform: a spinning record turns with
  // rotate, and a browser applies transform after rotate - so a slide by
  // transform went off at the record's angle. translate comes first, and a
  // slide stays sideways whatever the turn.
  const put = (el, x, ms, opacity) => {
    // Back at rest, the stylesheet's own transitions (the cover rounding
    // into a record) apply again.
    el.style.transition = ms ? `translate ${ms}ms ease-out, opacity ${ms}ms ease-out` : (x ? 'none' : '');
    el.style.translate = x ? `${x}px 0` : '';
    if (opacity !== undefined) el.style.opacity = opacity === 1 ? '' : String(opacity);
  };
  const artOf = (n) => (n && artPath(n.item)) || NO_COVER;

  self.start = () => {
    near = { '-1': neighborTrack(-1), 1: neighborTrack(1) };
    bigCover = cover.offsetWidth > 0;
    if (!bigCover) return;
    w = cover.offsetWidth + GAP;
    for (const by of [-1, 1]) {
      const img = sides[by];
      Object.assign(img.style, {
        left: `${cover.offsetLeft}px`, top: `${cover.offsetTop}px`,
        width: `${cover.offsetWidth}px`, height: `${cover.offsetHeight}px`,
      });
      if (near[by]) {
        const url = artOf(near[by]);
        if (img.dataset.src !== url) { img.src = url; img.dataset.src = url; }
        img.style.visibility = 'visible';
      } else {
        img.style.visibility = 'hidden';
      }
    }
  };

  const draw = (d, ms) => {
    if (bigCover) {
      put(cover, d, ms);
      put(sides[-1], d - w, ms);
      put(sides[1], d + w, ms);
      put(head, 0, ms, 1 - Math.min(Math.abs(d) / w, 1) * 0.7);
    } else {
      put(head, d, ms, 1 - Math.min(Math.abs(d) / 300, 1) * 0.7);
    }
  };

  self.move = (dx) => {
    const by = dx < 0 ? 1 : -1;
    // With nothing that way, the cover gives only a little.
    draw(near[by] ? dx : dx / 4, 0);
  };

  const settle = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const frame = () => new Promise((r) => requestAnimationFrame(r));

  // The covers either side are fetched and decoded as soon as a song starts,
  // so the one a swipe brings in is already drawn. They used to be asked for
  // only when the finger moved, and slid in blank on a phone.
  self.prime = () => {
    if (self.busy) return;
    for (const by of [-1, 1]) {
      const n = neighborTrack(by);
      if (!n) continue;
      const img = sides[by];
      const url = artOf(n);
      if (img.dataset.src !== url) {
        img.src = url;
        img.dataset.src = url;
        img.decode().catch(() => {});
      }
    }
  };

  self.end = async (dx, speed) => {
    const by = dx < 0 ? 1 : -1;
    const far = Math.abs(dx) > (bigCover ? w : 300) * 0.3;
    const flung = Math.abs(dx) > 30 && Math.abs(speed) > 0.35 && Math.sign(speed) === Math.sign(dx);
    if (!near[by] || !(far || flung)) {
      draw(0, 220);
      // The neighbors tuck away again once the cover is back.
      setTimeout(() => { if (!self.busy) for (const b of [-1, 1]) sides[b].style.visibility = 'hidden'; }, 230);
      return;
    }
    self.busy = true;
    if (bigCover) {
      draw(-by * w, 230);
      await settle(240);
      // The cover that slid in stays where it is. Behind it the main cover
      // takes the same picture - already loaded, so at once - and returns to
      // the middle, the song changes (which starts a record from the top, as
      // the one that slid in is), and only then does the slid-in one step
      // aside. It used to hide the main cover and wait for the new song's
      // picture to be asked for and drawn, which was the pop-in.
      cover.src = sides[by].src || artOf(near[by]);
      try { await cover.decode(); } catch { /* shown when it loads */ }
      put(cover, 0, 0);
      stepTrack(by);
      await frame();
      for (const b of [-1, 1]) { put(sides[b], 0, 0); sides[b].style.visibility = 'hidden'; }
      put(head, 0, 0, 1);
      self.busy = false;
      self.prime();
      return;
    } else {
      put(head, -by * 300, 160, 0);
      await settle(170);
      stepTrack(by);
      put(head, by * 120, 0, 0);
      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      put(head, 0, 200, 1);
      await settle(210);
      put(head, 0, 0, 1);
    }
    self.busy = false;
  };
  return self;
})();

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
  let startX = 0;
  let scrollsY = false;
  let axis = null;   // 'y' closes, 'x' changes song
  let dx = 0;
  panel.addEventListener('touchstart', (event) => {
    const t = event.touches[0];
    armed = event.touches.length === 1 && !npSwipe.busy && !event.target.closest('input');
    // Up next, and lyrics scrolled down, scroll up and down themselves: a
    // drag that way there is theirs, but a sideways one still changes song.
    scrollsY = Boolean(event.target.closest('#np-queue')
      || (event.target.closest('#np-lyrics') && scrolledDown(event.target)));
    dragging = false;
    axis = null;
    dy = 0;
    dx = 0;
    startY = t.clientY;
    startX = t.clientX;
    startT = performance.now();
  }, { passive: true });
  panel.addEventListener('touchmove', (event) => {
    if (!armed) return;
    if (shown('item-menu')) {
      armed = false;
      return;
    }
    const t = event.touches[0];
    const d = t.clientY - startY;
    const across = t.clientX - startX;
    // A song being moved in Up next is the finger's only job.
    if (queueDrag.active) {
      armed = false;
      return;
    }
    if (!axis) {
      if (Math.max(Math.abs(d), Math.abs(across)) < 8) return;
      if (Math.abs(across) <= Math.abs(d) && scrollsY) {
        armed = false;
        return;
      }
      if (Math.abs(across) > Math.abs(d)) {
        axis = 'x';
        npSwipe.start();
      } else if (d > 0) {
        axis = 'y';
      } else {
        armed = false; // an upward drag is a scroll, not a close
        return;
      }
      dragging = true;
      if (axis === 'y') panel.style.transition = 'none';
    }
    if (event.cancelable) event.preventDefault();
    if (axis === 'x') {
      dx = across;
      npSwipe.move(dx);
      return;
    }
    dy = Math.max(0, d);
    panel.style.transform = `translateY(${dy}px)`;
  }, { passive: false });
  const end = () => {
    if (!dragging) return;
    npSwipe.draggedAt = performance.now();
    dragging = false;
    armed = false;
    if (axis === 'x') {
      const speed = dx / Math.max(performance.now() - startT, 1);
      npSwipe.end(dx, speed);
      return;
    }
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

// Volume is the listener's own level, under the ReplayGain leveling that
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
  let startX = 0;
  let dx = 0;
  let axis = null;
  let startT = 0;
  let busy = false;
  dock.addEventListener('touchstart', (event) => {
    if (busy || event.touches.length !== 1 || !matchMedia('(max-width: 760px)').matches) return;
    active = true;
    axis = null;
    dy = 0;
    dx = 0;
    startY = event.touches[0].clientY;
    startX = event.touches[0].clientX;
    startT = performance.now();
    dock.style.transition = 'none';
  }, { passive: true });
  dock.addEventListener('touchmove', (event) => {
    if (!active) return;
    // A hold opened the menu: the finger is the menu's now.
    if (shown('item-menu')) {
      active = false;
      dock.style.transition = '';
      dock.style.transform = '';
      dock.style.opacity = '';
      return;
    }
    const t = event.touches[0];
    dy = t.clientY - startY;
    dx = t.clientX - startX;
    if (!axis) {
      if (Math.max(Math.abs(dx), Math.abs(dy)) < 8) return;
      axis = Math.abs(dx) > Math.abs(dy) ? 'x' : 'y';
    }
    if (axis === 'x') {
      // Sideways: the next or previous song. Nothing that way, and the card
      // gives only a little.
      const d = neighborTrack(dx < 0 ? 1 : -1) ? dx : dx / 4;
      dock.style.transform = `translateX(${d}px)`;
      dock.style.opacity = String(Math.max(0.3, 1 - Math.abs(d) / 300));
    } else if (dy > 0) {
      dock.style.transform = `translateY(${dy}px)`;
      dock.style.opacity = String(Math.max(0.2, 1 - dy / 160));
    }
  }, { passive: true });
  const end = async () => {
    if (!active) return;
    active = false;
    dock.style.transition = 'transform 0.2s ease-out, opacity 0.2s ease-out';
    if (axis === 'x') {
      const by = dx < 0 ? 1 : -1;
      const speed = Math.abs(dx) / Math.max(performance.now() - startT, 1);
      if (neighborTrack(by) && (Math.abs(dx) > 80 || (speed > 0.35 && Math.abs(dx) > 30))) {
        busy = true;
        const width = dock.offsetWidth;
        dock.style.transition = 'transform 0.16s ease-in, opacity 0.16s ease-in';
        dock.style.transform = `translateX(${-by * width}px)`;
        dock.style.opacity = '0';
        await new Promise((r) => setTimeout(r, 170));
        stepTrack(by);
        dock.style.transition = 'none';
        dock.style.transform = `translateX(${by * width * 0.5}px)`;
        await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
        dock.style.transition = 'transform 0.22s ease-out, opacity 0.22s ease-out';
        dock.style.transform = '';
        dock.style.opacity = '';
        await new Promise((r) => setTimeout(r, 230));
        dock.style.transition = '';
        busy = false;
        return;
      }
      dock.style.transform = '';
      dock.style.opacity = '';
      setTimeout(() => { dock.style.transition = ''; }, 200);
      return;
    }
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

// Songs like this one: the song keeps playing, and what comes after it
// becomes songs that sound like it (or, before the sound analysis has heard
// it, songs by artists like it) - a station that carries on without end.
async function playLikeThis() {
  const item = audio.item;
  if (!item || item.kind !== 'music') return;
  const params = { mode: 'song', seed: item.id };
  const { ok, body } = await api('/api/music/radio', {
    method: 'POST', body: JSON.stringify({ ...params, exclude: [`${item.sourceId}/${item.id}`] }),
  });
  if (audio.item !== item) return;
  const songs = ((ok && body && body.songs) || []).filter((s) => !(s.id === item.id && s.sourceId === item.sourceId));
  if (!songs.length) {
    showToast((body && body.error) || 'No songs like this one yet.');
    return;
  }
  // Said plainly, since the button is new to people: what was coming up is
  // replaced, and saying so beats somebody wondering where their queue went.
  const q = audio.queue;
  const replaced = q && q.items[q.index] === item && q.items.length > q.index + 1;
  audio.queue = { items: [item, ...songs], index: 0, original: null };
  audio.radio = { params, title: body.title, next: 0, loading: false };
  queueChanged();
  showToast(replaced
    ? 'Similar songs are up next, replacing your queue.'
    : 'Similar songs added to the queue.');
}

function renderNowPlaying() {
  if ($('now-playing').classList.contains('hidden') || !audio.item) return;
  const item = audio.item;
  show($('np-speed-wrap'), item.kind === 'audiobook');
  renderSpeed();
  const art = artPath(item);
  for (const img of [$('np-cover'), $('np-thumb')]) {
    img.src = art || NO_COVER;
    img.onerror = () => { img.onerror = null; img.src = NO_COVER; };
  }
  setBackdrop(art);
  tintStatusBar(art);
  setNpLine($('np-title'), item.title);
  setNpLine($('np-sub'), [(item.creators || []).join(', '), (item.extra && item.extra.album) || item.subtitle]
    .filter(Boolean).join(' \u2014 '));

  const player = $('audio-player');
  setIcon($('np-play'), player.paused ? 'play' : 'pause', true);
  $('now-playing').classList.toggle('spin', coverSpins());
  npSwipe.prime();
  // A new song is a new record: it starts from the top, as the cover that
  // slid in did, rather than at the angle the last one had reached.
  if (renderNowPlaying.song !== selectionKey(item)) {
    renderNowPlaying.song = selectionKey(item);
    for (const img of [$('np-cover'), $('np-thumb')]) {
      img.style.animation = 'none';
      void img.offsetWidth;
      img.style.animation = '';
    }
  }
  $('now-playing').classList.toggle('playing', !$('audio-player').paused);
  const q = audio.queue;
  $('np-prev').disabled = false;
  $('np-next').disabled = !q || (q.index + 1 >= q.items.length && audio.repeat === 'off');

  const list = $('np-queue');
  // Mid-drag the rows are the drag's; they are drawn afresh when it ends.
  if (queueDrag.active) {
    loadLyrics(item);
    syncNowPlayingTime();
    return;
  }
  list.replaceChildren();
  const upcoming = q ? q.items.slice(q.index + 1) : [];
  upcoming.forEach((song, i) => {
    const position = q.index + 1 + i;
    const li = document.createElement('li');
    const go = document.createElement('button');
    go.type = 'button';
    go.className = 'np-queue-song';
    // Its cover beside it, as in an album's list, so the queue can be read
    // at a glance.
    const art = document.createElement('img');
    art.className = 'np-queue-art';
    art.alt = '';
    art.loading = 'lazy';
    art.decoding = 'async';
    art.src = artPath(song) || NO_COVER;
    art.onerror = () => { art.onerror = null; art.src = NO_COVER; };
    const words = document.createElement('span');
    words.className = 'np-queue-words';
    const t = document.createElement('strong');
    t.textContent = song.title;
    const s = document.createElement('span');
    s.textContent = (song.creators || []).join(', ') || song.subtitle || '';
    words.append(t, s);
    go.append(art, words);
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
setIcon($('np-exit'), 'close');
$('np-exit').addEventListener('click', stopAudio);
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
// leveling does nothing, where the alternative is music that stops.
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
// leveling works under it rather than replacing it.
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
      img.decoding = 'async';
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

/* ------------------------------------------------------------------- radio */

// Stations that never end. The server picks each batch (internal/httpapi
// radio.go); the app asks for the next one while five songs are still to
// come, sending what is already queued so nothing repeats.
audio.radio = null; // { params, title, next, loading }

async function startRadio(params) {
  const { ok, body } = await api('/api/music/radio', { method: 'POST', body: JSON.stringify(params) });
  if (!ok || !body || !body.songs || !body.songs.length) {
    showToast((body && body.error) || 'Nothing in the library fits that station.');
    return;
  }
  playQueue(body.songs, 0);
  audio.radio = { params, title: body.title, next: body.next || 0, loading: false };
}

async function topUpRadio() {
  const r = audio.radio;
  const q = audio.queue;
  if (!r || !q || r.loading || q.items.length - q.index > 5) return;
  r.loading = true;
  const params = { ...r.params, exclude: q.items.slice(-1500).map((it) => `${it.sourceId}/${it.id}`) };
  if (params.mode === 'time' && r.next) params.from = r.next;
  const { ok, body } = await api('/api/music/radio', { method: 'POST', body: JSON.stringify(params) });
  r.loading = false;
  if (audio.radio !== r || audio.queue !== q || !ok || !body || !body.songs) return;
  if (body.next) r.next = body.next;
  q.items.push(...body.songs);
  if (q.original) q.original.push(...body.songs);
  queueChanged();
}

// The Radio page: the stations, then a tuner to build one by hand.
function radioPage(catalog) {
  const parts = [];
  const heard = catalog && catalog.listening;
  // Duplicates are heard once, so a finished library can sit just short of its total.
  if (heard && heard.total && (heard.running || heard.songs < heard.total * 0.9)) {
    // The sound analysis listens to every song once, which for a big library
    // takes hours; saying how far it has got explains missing moods.
    const line = document.createElement('p');
    line.className = 'radio-listening';
    line.textContent = heard.songs
      ? `Listening to your music to learn how it sounds: ${heard.songs.toLocaleString()} of ${heard.total.toLocaleString()} songs so far. Moods fill in as it goes.`
      : 'SoundStorm will listen to each of your songs once, in the background, to learn its mood. Moods appear here as it goes.';
    parts.push(line);
  }
  parts.push(stationGrid((catalog && catalog.stations) || []));
  const moods = (catalog && catalog.moods) || [];
  if (moods.length) {
    const h = document.createElement('h3');
    h.className = 'section-title';
    h.textContent = 'Moods';
    parts.push(h, stationGrid(moods));
  }
  if (catalog) parts.push(radioTuner(catalog));
  return parts;
}

function stationGrid(stations) {
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid mix-grid';
  for (const st of stations) {
    const card = document.createElement('button');
    card.type = 'button';
    card.className = 'item mix-card radio-card';
    const meta = document.createElement('div');
    meta.className = 'meta';
    const title = document.createElement('span');
    title.className = 'title';
    title.textContent = st.title;
    const sub = document.createElement('span');
    sub.className = 'sub';
    sub.textContent = st.subtitle;
    meta.append(title, sub);
    const cover = mixCover(st);
    const badge = document.createElement('span');
    badge.className = 'radio-badge';
    badge.append(icon('radio'));
    cover.append(badge);
    card.append(cover, meta);
    card.addEventListener('click', () => startRadio(st.seed ? { mode: st.mode, seed: st.seed } : { mode: st.mode }));
    grid.append(card);
  }
  return grid;
}

const TUNER_KEY = 'soundstorm-tuner';

function radioTuner(catalog) {
  let saved = {};
  try { saved = JSON.parse(localStorage.getItem(TUNER_KEY) || '{}') || {}; } catch { saved = {}; }
  const box = document.createElement('section');
  box.className = 'radio-tuner';
  const h = document.createElement('h3');
  h.className = 'section-title';
  h.textContent = 'Build a station';

  const famLabel = document.createElement('label');
  famLabel.className = 'tuner-row';
  const famName = document.createElement('span');
  famName.textContent = 'How familiar';
  const fam = document.createElement('input');
  fam.type = 'range';
  fam.min = '0';
  fam.max = '100';
  fam.value = String(Number.isFinite(saved.familiar) ? Math.round(saved.familiar * 100) : 50);
  const ends = document.createElement('div');
  ends.className = 'tuner-ends';
  for (const t of ['New to you', 'A mix', 'Favorites']) {
    const s = document.createElement('span');
    s.textContent = t;
    ends.append(s);
  }
  famLabel.append(famName, fam, ends);

  const years = (catalog && catalog.years) || {};
  const yearRow = document.createElement('div');
  yearRow.className = 'tuner-row tuner-years';
  const selects = [];
  if (years.from && years.until) {
    const decades = [];
    for (let d = Math.floor(years.from / 10) * 10; d <= years.until; d += 10) decades.push(d);
    const pickYear = (label, value, until) => {
      const l = document.createElement('label');
      const t = document.createElement('span');
      t.textContent = label;
      const sel = document.createElement('select');
      const any = document.createElement('option');
      any.value = '';
      any.textContent = 'Any';
      sel.append(any);
      for (const d of decades) {
        const o = document.createElement('option');
        o.value = String(until ? d + 9 : d);
        o.textContent = until ? `${d + 9}` : `${d}`;
        sel.append(o);
      }
      sel.value = value ? String(value) : '';
      l.append(t, sel);
      selects.push(sel);
      return l;
    };
    yearRow.append(pickYear('From', saved.from, false), pickYear('Until', saved.until, true));
  }

  // Moods and energy, once the sound analysis has heard something.
  const moodChoice = new Set(Array.isArray(saved.moods) ? saved.moods : []);
  const moodRow = document.createElement('div');
  moodRow.className = 'tuner-genres tuner-moods';
  let energy = null;
  const energyLabel = document.createElement('label');
  energyLabel.className = 'tuner-row';
  const moodsKnown = (catalog && catalog.moods) || [];
  if (moodsKnown.length) {
    for (const m of moodsKnown) {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'chip';
      b.textContent = m.title;
      const mark = () => {
        b.classList.toggle('active', moodChoice.has(m.seed));
        b.setAttribute('aria-pressed', String(moodChoice.has(m.seed)));
      };
      mark();
      b.addEventListener('click', () => {
        if (moodChoice.has(m.seed)) moodChoice.delete(m.seed);
        else moodChoice.add(m.seed);
        mark();
      });
      moodRow.append(b);
    }
    const energyName = document.createElement('span');
    energyName.textContent = 'Energy';
    energy = document.createElement('input');
    energy.type = 'range';
    energy.min = '0';
    energy.max = '100';
    energy.value = String(Number.isFinite(saved.energy) ? Math.round(saved.energy * 100) : 50);
    // Off until moved: most stations should not be held to one energy.
    energy.dataset.set = Number.isFinite(saved.energy) ? '1' : '';
    energy.classList.toggle('unset', !energy.dataset.set);
    energy.addEventListener('input', () => { energy.dataset.set = '1'; energy.classList.remove('unset'); });
    const energyEnds = document.createElement('div');
    energyEnds.className = 'tuner-ends';
    for (const t of ['Calm', 'Any', 'Full on']) {
      const s = document.createElement('span');
      s.textContent = t;
      energyEnds.append(s);
    }
    energyLabel.append(energyName, energy, energyEnds);
  }

  const chosen = new Set(Array.isArray(saved.genres) ? saved.genres : []);
  const genreRow = document.createElement('div');
  genreRow.className = 'tuner-genres';
  for (const g of (catalog && catalog.genres) || []) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'chip';
    b.textContent = g;
    b.classList.toggle('active', chosen.has(g));
    b.setAttribute('aria-pressed', String(chosen.has(g)));
    b.addEventListener('click', () => {
      if (chosen.has(g)) chosen.delete(g);
      else chosen.add(g);
      b.classList.toggle('active', chosen.has(g));
      b.setAttribute('aria-pressed', String(chosen.has(g)));
    });
    genreRow.append(b);
  }

  const go = document.createElement('button');
  go.type = 'button';
  go.className = 'play-main';
  go.textContent = '\u25B6  Play station';
  go.addEventListener('click', () => {
    const params = {
      mode: 'custom',
      familiar: Number(fam.value) / 100,
      from: selects[0] && selects[0].value ? Number(selects[0].value) : 0,
      until: selects[1] && selects[1].value ? Number(selects[1].value) : 0,
      genres: [...chosen],
      moods: [...moodChoice],
      energy: energy && energy.dataset.set ? Number(energy.value) / 100 : undefined,
    };
    try { localStorage.setItem(TUNER_KEY, JSON.stringify(params)); } catch { /* private mode */ }
    startRadio(params);
  });

  box.append(h, famLabel);
  if (moodRow.children.length) box.append(moodRow, energyLabel);
  if (yearRow.children.length) box.append(yearRow);
  if (genreRow.children.length) box.append(genreRow);
  box.append(go);
  return box;
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
  // the screen is not a column of gray waiting for the first word.
  const first = audio.lyrics.lines[0];
  if (audio.lyrics.synced && first && first.start >= 5000) audio.lyrics.lines.unshift({ start: 0, text: '' });
  renderLyrics();
}

function renderLyrics() {
  const has = Boolean(audio.lyrics && audio.lyrics.lines.length);
  // Four ways the screen can be laid out:
  //   queue  - Up next in the middle, a small cover beside the title;
  //   lyrics - the lyrics in the middle, the same small cover;
  //   strip  - a phone's default: the big cover, and under the title a strip
  //            of the few lines around the one being sung (tap it for lyrics);
  //   cover  - an audiobook: the big cover and nothing else.
  // A computer has room for the cover beside the lyrics, so it never strips.
  //
  // A song keeps the lyrics layout whether or not it has lyrics, and while
  // they load: everything sits exactly where it would with them, and the
  // space says so quietly when there are none. Switching layout on the
  // answer made every song change jump once its lyrics arrived, and jump
  // back for a song without.
  const phone = matchMedia('(max-width: 760px)').matches;
  const music = Boolean(audio.item && audio.item.kind === 'music');
  const mode = audio.showQueue ? 'queue'
    : !music ? 'cover'
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
  if (!has) {
    box.classList.remove('unsynced');
    // Nothing while they load, so a song that has them does not flash a
    // "none" first.
    if (music && audio.lyrics && !audio.lyrics.loading) {
      const none = document.createElement('p');
      none.className = 'np-lyrics-none';
      none.textContent = 'No lyrics for this song';
      box.append(none);
    }
    return;
  }
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
    credit.textContent = 'Lyrics from LRCLIB, contributed by its community';
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
    if (raw && raw.items && raw.groups) {
      // Downloaded favorites were once a group named the British way.
      for (const g of raw.groups) {
        if (g.id === 'favourites') Object.assign(g, { id: 'favorites', type: 'favorites', title: 'Favorites' });
      }
      return raw;
    }
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
// serves it back on the real *.soundstorm.dev names (see sw.js), and it is
// kept every time the app loads on one - not only once something has been
// downloaded, or opening the app with no connection and no downloads was
// Chrome's bare ERR_FAILED page rather than the app saying it is offline.
// Each address is its own origin with its own copy.
const SHELL_FILES = [
  '/', '/static/app.js', '/static/style.css', '/static/reader.js', '/static/sw-register.js',
  '/static/favicon.svg', '/static/cloud.svg', '/static/no-cover.svg', '/manifest.webmanifest',
  // The reader's own modules, for books downloaded to read offline.
  ...['view', 'epub', 'epubcfi', 'fixed-layout', 'overlayer', 'paginator', 'progress', 'search', 'text-walker']
    .map((m) => `/static/vendor/foliate-js/${m}.js`),
];
async function keepShell() {
  if (!('caches' in window)) return;
  try {
    const cache = await caches.open(OFFLINE_SHELL);
    // One at a time, so one missing file does not lose the rest.
    await Promise.all(SHELL_FILES.map((f) => cache.add(f).catch(() => {})));
  } catch {
    // Offline start-up is a bonus; downloads still play with the app open.
  }
}

// download saves a group of songs - an album, a playlist, one song - and
// reports progress through onProgress(done, total).
async function download(group, items, onProgress, shouldStop) {
  const songs = items.filter((it) => it && it.kind === 'music');
  if (!songs.length) return;
  if (navigator.storage && navigator.storage.persist) navigator.storage.persist().catch(() => {});
  const cache = await caches.open(OFFLINE_CACHE);
  let done = 0;
  for (const song of songs) {
    if (shouldStop && shouldStop()) break;
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
  markDownloads();
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
    const kept = state.downloads.items[key];
    if (kept) {
      const video = kept.dl && kept.dl.video;
      if (video && video.mode === 'hls') {
        const playlist = await cache.match(video.variant);
        if (playlist) for (const url of hlsParts(await playlist.text(), video.variant)) await cache.delete(url);
      }
      for (const url of (kept.dl && kept.dl.files) || [streamPath(kept)]) await cache.delete(url);
    }
    delete state.downloads.items[key];
  }
  saveDownloadIndex();
  markMusicTabs();
  markDownloads();
}

async function clearDownloads() {
  state.downloads = { items: {}, groups: [] };
  try {
    localStorage.removeItem(DOWNLOADS_KEY);
    // Places kept for downloaded books are this person's too.
    for (const key of Object.keys(localStorage)) {
      if (key.startsWith('soundstorm-pos:') || key.startsWith('soundstorm-read:')) localStorage.removeItem(key);
    }
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
async function downloadsView(offlineMode, only) {
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
    empty.textContent = 'Nothing downloaded yet. Use Download on an album, a playlist, a book or an audiobook to keep it on this device.';
    wrap.append(empty);
    return wrap;
  }
  const list = document.createElement('ul');
  list.className = 'download-list';
  for (const group of state.downloads.groups.filter((g) => !only || only(g))) {
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
    const what = {
      audiobook: 'Audiobook', ebook: 'Book', document: 'Document', pair: 'Read Along',
      video: 'Film', tv: 'Episode', picture: 'Photo',
    }[group.type] || `${songs.length} song${songs.length === 1 ? '' : 's'}`;
    s.textContent = [group.subtitle, what].filter(Boolean).join(' \u00B7 ');
    text.append(t, s);
    const open = () => {
      if (group.type === 'pair') readAlong(group.pair, group);
      else if (group.type === 'picture') {
        // The viewer steps through the photos on this device.
        state.items = state.downloads.groups.filter((g) => g.type === 'picture')
          .map((g) => state.downloads.items[g.keys[0]]).filter(Boolean);
        play(songs[0]);
      } else if (SINGLE_DOWNLOADS.includes(group.type)) play(songs[0]);
      else playQueue(songs, 0);
    };
    text.addEventListener('click', open);
    const openButton = document.createElement('button');
    openButton.type = 'button';
    openButton.className = 'np-icon download-play';
    openButton.setAttribute('aria-label', `Open ${group.title}`);
    openButton.append(icon(group.type === 'ebook' || group.type === 'document' ? 'book' : 'play', true));
    openButton.addEventListener('click', open);
    li.append(cover, text, openButton);
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
  show($('app'), true);
  enterOffline(true);
}


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

renderSleep();

/* --------------------------------------------------------------- crossfade */

// Crossfade: the last few seconds of a song blend into the next, as a radio
// does. The page has one audio element, and everything in it - the lyrics,
// the lock screen, the queue, the leveling - listens to that one. So a second,
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
  home: [{ kind: '', label: 'Home' }, { kind: 'favorites', label: 'Favorites', inMusicTabs: true }],
  music: [{ kind: 'music', label: 'Music' }, { kind: 'playlists', label: 'Playlists', inMusicTabs: true },
    { kind: 'fav-music', label: 'Favorites', inMusicTabs: true }, { kind: 'genres-music', label: 'Genres', inMusicTabs: true }],
  watch: [{ kind: 'video', label: 'Films' }, { kind: 'tv', label: 'TV' }, { kind: 'fav-watch', label: 'Favorites' },
    { kind: 'genres-watch', label: 'Genres' }],
  books: [{ kind: 'audiobook', label: 'Audiobooks' }, { kind: 'ebook', label: 'Ebooks' },
    { kind: 'authors', label: 'Authors' }, { kind: 'series', label: 'Series' },
    { kind: 'pairs', label: 'Read Along' }, { kind: 'document', label: 'Documents' },
    { kind: 'fav-books', label: 'Favorites' }, { kind: 'genres-books', label: 'Genres' }],
  photos: [{ kind: 'picture', label: 'Photos' }, { kind: 'people', label: 'People' },
    { kind: 'places', label: 'Places' }, { kind: 'fav-photos', label: 'Favorites' }],
};
// Each tab's Favorites pill shows the favorites of the kinds it holds; the
// Books tab also browses by author and by series. Here, beside TABS, because
// the tabs are first drawn while the page loads.
const FAV_KINDS = {
  'fav-music': ['music'], 'fav-watch': ['video', 'tv'],
  'fav-books': ['audiobook', 'ebook', 'document'], 'fav-photos': ['picture'],
};
// Pages of groups rather than a shelf's list: books by author and series,
// photos by who is in them and where.
const BOOK_BROWSE = new Set(['authors', 'series', 'people', 'places', 'genres-music', 'genres-watch', 'genres-books']);
// Each tab's genres are of these shelves.
const GENRE_KINDS = { 'genres-music': ['music'], 'genres-watch': ['video', 'tv'], 'genres-books': ['audiobook', 'ebook'] };
// A category the + button starts with put away: nobody's tab changes until they add it.
const DEFAULT_HIDDEN = { music: ['genres'], watch: ['genres-watch'], books: ['genres-books'] };
const PHOTO_BROWSE = new Set(['people', 'places']);
state.tab = 'home';
state.tabKind = {};

function tabOf(kind) {
  return Object.keys(TABS).find((tab) => TABS[tab].some((o) => o.kind === kind)) || 'home';
}

function shelfAvailable(kind) {
  if (state.offline) return kind === '' || offlineKinds().has(kind);
  if (kind === '' || kind === 'favorites' || kind === 'playlists') return true;
  // Only once there is at least one book on both shelves.
  if (kind === 'pairs') return state.pairCount > 0 && shelfAvailable('ebook') && shelfAvailable('audiobook');
  // Books by author and series: wherever there are books.
  if (kind === 'authors' || kind === 'series') return shelfAvailable('ebook') || shelfAvailable('audiobook');
  if (kind === 'people' || kind === 'places') return shelfAvailable('picture');
  if (GENRE_KINDS[kind]) return GENRE_KINDS[kind].some((k) => shelfAvailable(k));
  // A tab's favorites: while the tab has a shelf of its own to favorite from.
  if (FAV_KINDS[kind]) return FAV_KINDS[kind].some((k) => shelfAvailable(k));
  const chip = document.querySelector(`#filters .chip[data-kind="${kind}"]`);
  if (!chip || chip.classList.contains('hidden')) return false; // not allowed
  const files = state.shelfFiles && state.shelfFiles[kind];
  return files === undefined || files > 0;
}

function tabShelves(tab) {
  const order = pillOrder(tab);
  const put = hiddenPills(tab);
  const rank = (kind) => { const i = order.indexOf(kind); return i < 0 ? 1e6 : i; };
  return (TABS[tab] || []).filter((o) => shelfAvailable(o.kind) && !put.includes(o.kind))
    .map((o, i) => ({ o, i }))
    .sort((a, b) => (rank(a.o.kind) - rank(b.o.kind)) || (a.i - b.i))
    .map(({ o }) => o);
}

// The order somebody has put a row of pills in, by holding one and sliding
// it. Kept on the device, like streaming quality: the phone and the computer
// can differ.
function pillOrder(row) {
  const saved = state.prefs && state.prefs.pills && state.prefs.pills[row];
  // An order saved before the spelling changed names Music's pill the British way.
  return Array.isArray(saved) ? saved.map((k) => (k === 'favourites' ? 'favorites' : k)) : [];
}

function savePillOrder(row, keys) {
  state.prefs = state.prefs || {};
  state.prefs.pills = { ...(state.prefs.pills || {}), [row]: keys };
  savePrefs({ pills: { [row]: keys } });
}

/* ------------------------------------------------------------ preferences */

// A person's small choices - the order of each tab's pills, read-along's
// highlight, audiobook speed - kept on their account so they follow them from
// the phone to the computer.
state.prefs = {};

async function loadPrefs() {
  const { ok, body } = await api('/api/prefs');
  state.prefs = ok && body ? body : {};
  // An order put together on this device before it was kept on the account
  // is carried over the first time, then the device's copy is dropped.
  if (!state.prefs.pills || !Object.keys(state.prefs.pills).length) {
    const local = {};
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (!key || !key.startsWith('soundstorm-pills-')) continue;
      try {
        const keys = JSON.parse(localStorage.getItem(key));
        if (Array.isArray(keys)) local[key.slice('soundstorm-pills-'.length)] = keys;
      } catch { /* not ours to worry about */ }
    }
    if (Object.keys(local).length) {
      state.prefs.pills = local;
      savePrefs({ pills: local });
    }
  }
  for (const key of Object.keys(localStorage).filter((k) => k.startsWith('soundstorm-pills-'))) {
    localStorage.removeItem(key);
  }
  applyMusicPillOrder();
  renderTabs();
  applySpeed();
}

async function savePrefs(change) {
  const { ok, body } = await api('/api/prefs', { method: 'PATCH', body: JSON.stringify(change) });
  if (ok && body) state.prefs = body;
}

// Now Playing's cover as a spinning disc, a tap away and back: kept on the
// account, so it stays the way somebody left it, on every device.
function coverSpins() {
  return Boolean(state.prefs && state.prefs.coverSpin);
}

$('np-cover').addEventListener('click', (event) => {
  // The end of a hold that opened the menu is not a tap, and must not reach
  // the page as a click outside the menu, which would close it.
  if (performance.now() - npHold.at < 700) {
    event.stopPropagation();
    return;
  }
  // Nor is the end of a swipe.
  if (npSwipe.busy || performance.now() - (npSwipe.draggedAt || 0) < 400) return;
  const on = !coverSpins();
  state.prefs = state.prefs || {};
  state.prefs.coverSpin = on;
  $('now-playing').classList.toggle('spin', on);
  savePrefs({ coverSpin: on });
});
// It spins only while the music plays, and stops where it is on pause.
for (const type of ['play', 'pause', 'ended']) {
  $('audio-player').addEventListener(type, () => {
    $('now-playing').classList.toggle('playing', !$('audio-player').paused);
  });
}

// Now Playing's title and artist line are always one line each, so the
// screen is laid out the same for every song: one too long for its line
// scrolls sideways, pausing at the start of each pass, as music apps do.
// They used to wrap, and a two- or three-line title pushed everything under
// it up over the cover. The whole title is in Info.
const MARQUEE_GAP = 48;      // px between the end of the text and its repeat
const MARQUEE_SPEED = 32;    // px a second
function setNpLine(el, text) {
  if (el.dataset.text === text) return;
  el.dataset.text = text;
  const run = document.createElement('span');
  run.className = 'np-line-run';
  run.textContent = text;
  el.replaceChildren(run);
  el.setAttribute('aria-label', text);
  fitNpLine(el);
}

function fitNpLine(el) {
  const run = el.querySelector('.np-line-run');
  if (!run) return;
  // Measured without the repeat, as the plain text.
  for (const a of run.getAnimations()) a.cancel();
  el.classList.remove('scrolling');
  run.textContent = el.dataset.text || '';
  const width = run.scrollWidth;
  if (!el.clientWidth || width <= el.clientWidth + 1 || matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  const copy = document.createElement('span');
  copy.className = 'np-line-copy';
  copy.setAttribute('aria-hidden', 'true');
  copy.textContent = el.dataset.text || '';
  run.append(copy);
  const shift = width + MARQUEE_GAP;
  // Two seconds' pause at the start of every pass, then a steady scroll that
  // ends where the repeat has taken the text's place, so the loop is seamless.
  const moving = (shift / MARQUEE_SPEED) * 1000;
  const hold = 2000 / (moving + 2000);
  el.classList.add('scrolling');
  run.animate([
    { transform: 'translateX(0)', offset: 0 },
    { transform: 'translateX(0)', offset: hold },
    { transform: `translateX(-${shift}px)`, offset: 1 },
  ], { duration: moving + 2000, iterations: Infinity });
}

function fitNpLines() {
  fitNpLine($('np-title'));
  fitNpLine($('np-sub'));
}
window.addEventListener('resize', () => {
  if (!$('now-playing').classList.contains('hidden')) fitNpLines();
});

// Now Playing's blurred backdrop fades from one cover to the next, only once
// the new one is decoded, rather than swapping (or, for a song without a
// cover, vanishing) at once. Two layers take turns in front.
const backdrops = [$('np-backdrop'), $('np-backdrop-2')];
let frontBackdrop = 0;
function setBackdrop(art) {
  const want = art ? new URL(art, location.href).href : '';
  if (setBackdrop.want === want) return;
  setBackdrop.want = want;
  const front = backdrops[frontBackdrop];
  if (!art) {
    front.classList.add('faded');
    return;
  }
  const back = backdrops[1 - frontBackdrop];
  const swap = () => {
    if (setBackdrop.want !== want) return;
    back.classList.remove('faded');
    front.classList.add('faded');
    frontBackdrop = 1 - frontBackdrop;
  };
  back.src = art;
  back.decode().then(swap, swap);
}

// For the reader, a module: read-along's highlight, on unless turned off.
window.soundstormHighlight = () => !(state.prefs && state.prefs.readAlongHighlight === false);
window.soundstormSetHighlight = (on) => {
  state.prefs.readAlongHighlight = on;
  savePrefs({ readAlongHighlight: on });
};

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
  // A search belongs to the tab it was typed in; another tab, or coming back
  // from Settings, starts with an empty box.
  if (tab !== state.tab) $('search-input').value = '';
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
    const available = tab === 'home' || (tab === 'settings' && !state.offline)
      || tabShelves(tab).some((o) => !o.inMusicTabs);
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
  const shelves = state.tab === 'settings' ? settingsCategories() : tabShelves(state.tab).filter((o) => !o.inMusicTabs);
  // The same pills as now: only the lit one changes, and the row stays put -
  // rebuilding it mid-swipe snapped it back for a frame.
  const sameRow = box.dataset.tab === state.tab;
  const current = [...box.querySelectorAll('button[data-kind]')].map((b) => b.dataset.kind).join();
  // Music has its own row of pills, with its own +; Home and Settings have
  // nothing to add. Only the other tabs' rows get one here.
  const plusHere = !['settings', 'music', 'home'].includes(state.tab);
  const addable = plusHere && pillsToAdd(state.tab).length > 0;
  if (sameRow && shelves.length > 1 && current === shelves.map((o) => o.kind).join()) {
    for (const b of box.querySelectorAll('button[data-kind]')) {
      const on = state.tab === 'settings' ? b.dataset.kind === state.settingsCat : b.dataset.kind === state.kind;
      b.classList.toggle('active', on);
      b.setAttribute('aria-selected', String(on));
    }
    show(box, true);
    if (state.tab === 'settings') applySettingsView();
    if (!state.pillSwiping) centerPill(box, box.querySelector('button.active'), 'smooth');
    if (state.tab !== 'home' && state.tab !== 'settings' && !tabShelves(state.tab).length) selectTab('home');
    return;
  }
  // Otherwise rebuilt: put back where it was, then glide the lit pill to the
  // middle - or jump there, for another tab's row.
  const keep = box.scrollLeft;
  box.dataset.tab = state.tab;
  box.replaceChildren();
  if (shelves.length > 1) {
    for (const o of shelves) {
      const b = document.createElement('button');
      b.type = 'button';
      b.setAttribute('role', 'tab');
      b.textContent = o.label;
      b.dataset.kind = o.kind;
      const on = o.kind === state.kind;
      b.classList.toggle('active', on);
      b.setAttribute('aria-selected', String(on));
      b.addEventListener('click', () => (state.tab === 'settings' ? selectSettingsCat(o.kind) : selectKind(o.kind)));
      box.append(b);
    }
  }
  if (plusHere && shelves.length) box.append(addPillButton());
  show(box, shelves.length > 1 || addable);
  if (state.tab === 'settings') applySettingsView();
  if (sameRow) box.scrollLeft = keep;
  if (!state.pillSwiping) centerPill(box, box.querySelector('button.active'), sameRow ? 'smooth' : 'auto');
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
// row, above), then a strip each of the newest albums, favorites, what was
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
  const [home, favs, played, memories] = await Promise.all([
    api('/api/home'),
    api('/api/favorites'),
    api('/api/music/mixes/recently-played'),
    shelfAvailable('picture') ? api('/api/photos/on-this-day') : Promise.resolve({}),
  ]);
  if (seq !== state.searchSeq) return;
  view.replaceChildren();
  const all = [];

  // In three runs, each together: what you were just playing (under the
  // Continue row), what is new on every shelf, then your favorites. They
  // used to interleave - New music, favorites, Recently played, then the
  // other New rows - which read as no order at all.
  const recent = ((played.ok && played.body && played.body.songs) || []).slice(0, 12);
  if (recent.length) {
    all.push(...recent);
    view.append(homeRow('Recently played', recent.map(renderItem), () => {
      state.musicView = 'mixes';
      selectTab('music');
    }));
  }
  // Photos from this day in earlier years, a row for each year.
  for (const day of (memories.ok && memories.body && memories.body.days) || []) {
    all.push(...day.items);
    const ago = new Date().getFullYear() - day.year;
    view.append(homeRow(`On this day, ${ago} year${ago === 1 ? '' : 's'} ago`, day.items.map(renderItem)));
  }
  const albums = (home.ok && home.body && home.body.albums) || [];
  if (albums.length) {
    view.append(homeRow('New music', albums.map((a) => albumCardFromHome(a)), () => {
      state.albumOrder = 'newest';
      state.musicView = 'albums';
      selectTab('music');
      if (state.kind === 'music') runSearch();
    }));
  }
  for (const shelf of (home.ok && home.body && home.body.shelves) || []) {
    all.push(...shelf.items);
    view.append(homeRow(HOME_SHELVES[shelf.kind] || 'New', shelf.items.map(renderItem), () => selectKind(shelf.kind)));
  }
  // A row of favorites for each tab, rather than one mixed list: See all
  // opens that tab's own Favorites.
  const favorites = (favs.ok && favs.body && favs.body.items) || [];
  for (const [kind, title] of [['fav-music', 'Favorite songs'], ['fav-watch', 'Favorite films and TV'],
    ['fav-books', 'Favorite books'], ['fav-photos', 'Favorite photos']]) {
    const list = favorites.filter((it) => FAV_KINDS[kind].includes(it.kind)).slice(0, 12);
    if (!list.length) continue;
    all.push(...list);
    view.append(homeRow(title, list.map(renderItem), () => selectKind(kind)));
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
// Watch's films and TV - a sideways swipe steps to the neighboring pill.
// The page follows the finger, and on letting go either carries on off the
// side while the next one slides in, or springs back. Not on an album,
// artist or playlist page (those have a back button and the swipe would
// throw the page away), and not from something that scrolls sideways
// itself, like a strip of mixes.
(function pillSwipe() {
  const pages = ['continue', 'results-bar', 'results', 'music-view', 'playlists-view', 'account'];
  let g = null;      // the gesture under way
  let busy = false;  // a switch is animating
  let lift = 0;      // how far the real pages are drawn down, to look scrolled to the top
  const listPages = new Set(['results', 'music-view', 'playlists-view']);

  const pills = () => {
    const row = ['music-tabs', 'subtabs'].map($).find((el) => !el.classList.contains('hidden'));
    return row ? [...row.querySelectorAll('button')].filter(isPill) : [];
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
    if (busy || $('app').classList.contains('hidden')) return false;
    if (pills().length < 2) return false;
    const onPage = target === document.body || target === document.documentElement || target.closest('#app');
    if (!onPage || target.closest('header, #music-tabs, #subtabs, #tabs')) return false;
    if (pages.some((id) => !$(id).classList.contains('hidden') && $(id).querySelector(':scope > .back'))) return false;
    return !scrollsSideways(target);
  }

  function place(x, ms, easing) {
    for (const id of pages) {
      const el = $(id);
      el.style.transition = ms ? `transform ${ms}ms ${easing}` : 'none';
      el.style.transform = x || lift ? `translate3d(${x}px, ${lift}px, 0)` : '';
    }
  }
  const promote = (on) => { for (const id of pages) $(id).style.willChange = on ? 'transform' : ''; };
  const settle = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const frame = () => new Promise((resolve) => requestAnimationFrame(() => resolve()));

  // The next pill's page arrives by fetch. Wait until the page has stopped
  // changing for a moment (a list is often cleared, then filled), at most
  // half a second, so what slides in is the page and not a blank.
  function whenDrawn() {
    return new Promise((resolve) => {
      let quiet;
      const done = () => { observer.disconnect(); clearTimeout(quiet); clearTimeout(cap); resolve(); };
      // Quiet is not drawn while the page still says it is loading.
      const settled = () => { if (!document.querySelector('#app .skeleton')) done(); };
      const observer = new MutationObserver(() => { clearTimeout(quiet); quiet = setTimeout(settled, 50); });
      for (const id of pages) observer.observe($(id), { childList: true, subtree: true, attributes: true, attributeFilter: ['class'] });
      const cap = setTimeout(done, 450);
      quiet = setTimeout(settled, 100);
    });
  }

  // The page that is leaving is laid where it was in a ghost: its own nodes,
  // moved rather than copied, so its pictures stay drawn and nothing is
  // rebuilt. It carries on off the side while the real page - already the
  // next pill's - follows right behind it, edge to edge.
  function ghostOf() {
    const shown = pages.map($).filter((el) => !el.classList.contains('hidden') && el.getClientRects().length);
    const ghost = document.createElement('div');
    ghost.className = 'swipe-ghost';
    ghost.inert = true;
    ghost.setAttribute('aria-hidden', 'true');
    // Every place is measured before anything moves, or each move would
    // shift the ones after it.
    const rects = shown.map((el) => el.getBoundingClientRect());
    shown.forEach((el, i) => {
      const r = rects[i];
      let shell;
      if (listPages.has(el.id)) {
        // A list: its own cards, moved, which the next render replaces anyway.
        shell = el.cloneNode(false);
        shell.append(...el.childNodes);
      } else {
        // Fixed parts with ids of their own (the count, Select, the Continue
        // row) stay where they are; the ghost gets a copy.
        shell = el.cloneNode(true);
        for (const img of shell.querySelectorAll('img')) img.loading = 'eager';
        for (const node of shell.querySelectorAll('[id]')) node.removeAttribute('id');
      }
      shell.removeAttribute('id');
      shell.style.cssText = '';
      shell.style.position = 'absolute';
      shell.style.margin = '0';
      // Where it is on the screen: the ghost is the screen's size and clips
      // the rest, so a long list costs one screenful to draw, not its whole
      // length - which an iPhone paints up front, as a hitch.
      shell.style.left = `${r.left}px`;
      shell.style.top = `${r.top}px`;
      shell.style.width = `${r.width}px`;
      ghost.append(shell);
    });
    $('app').append(ghost);
    return ghost;
  }

  const ease = 'cubic-bezier(.2,.7,.2,1)';
  // The pills drawn shifted along their row, by a transform the compositor
  // moves - scrolling the row every frame made the phone lay it out again
  // each time. Eased over ms, in step with the page, when ms is given.
  function shiftPills(swipe, by, ms) {
    for (const b of swipe.rowPills) {
      b.style.transition = ms ? `transform ${ms}ms ${ease}` : 'none';
      b.style.transform = by ? `translate3d(${-by}px, 0, 0)` : '';
    }
  }
  const slide = (ghost, x, ms) => {
    ghost.style.transition = ms ? `transform ${ms}ms ${ease}` : 'none';
    ghost.style.transform = `translate3d(${x}px, 0, 0)`;
  };

  // The swipe has gone sideways toward a neighbor: switch to it now, so
  // its page is loading - and usually there - while the finger is still
  // down. Nothing scrolls: the next page is drawn down by the scroll so far,
  // which shows it from its top under the pills (they stay pinned), and the
  // real scroll to the top happens in the same frame the ghost goes.
  function begin(target, dir) {
    g.target = target;
    g.dir = dir;
    g.scroll = window.scrollY;
    // Nodes leaving the page must not make the browser re-aim the scroll -
    // nor, with the list's cards gone into the ghost, shorten the page and
    // pull the scroll back, which drew the next page that much lower.
    document.documentElement.style.overflowAnchor = 'none';
    document.body.style.minHeight = `${document.documentElement.scrollHeight}px`;
    g.ghost = ghostOf();
    lift = g.scroll;
    promote(true);
    place(dir * window.innerWidth, 0);
    g.drawn = whenDrawn();
    document.documentElement.classList.add('swiping');
    // The row of pills rides along: as the page follows the finger, the next
    // pill moves toward the middle, arriving as the page does.
    g.row = target.parentElement;
    g.rowFrom = g.row.scrollLeft;
    g.rowTo = pillCenter(g.row, target);
    // Every button in the row slides, the + included: left out, it stayed
    // where it was until the swipe ended, then jumped.
    g.rowPills = [...g.row.querySelectorAll('button')];
    // No layer of their own, from here or the stylesheet: on Android a layer
    // that starts off the side of the row is drawn only once it has come into
    // view, and the +, last in the row, popped in after the finger lifted.
    // Painted with the row, which is small and drawn whole, they are already
    // there as they slide in.
    state.pillSwiping = true;
    // A frame later: pressing the pill starts the next page's work - clearing,
    // ghost content, a request - which must not hold up the first frame of
    // the page following the finger.
    requestAnimationFrame(() => target.click());
  }

  // Touch events arrive faster than frames on many phones: draw once a frame.
  let pending = null;
  function follow(dx) {
    const d = Math.sign(dx) === -g.dir ? dx : dx / 4;
    g.dx = d;
    if (pending) { pending.d = d; return; }
    pending = { d, swipe: g };
    requestAnimationFrame(() => {
      const { d: at, swipe } = pending;
      pending = null;
      if (swipe !== g) return;
      slide(swipe.ghost, at, 0);
      place(at + swipe.dir * window.innerWidth, 0);
      const k = Math.min(1, Math.max(0, (-at * swipe.dir) / window.innerWidth));
      shiftPills(swipe, (swipe.rowTo - swipe.rowFrom) * k, 0);
    });
  }

  async function finish(swipe, commit) {
    busy = true;
    pending = null;
    const width = window.innerWidth;
    const { ghost, dir, dx, drawn, from, scroll } = swipe;
    if (commit) {
      // Straight away: the page slides in with its ghost content if the real
      // one is not there yet. Waiting for it first froze the swipe for as
      // long as the server took, which on a phone is the stall people felt.
      const ms = Math.round(150 + 150 * (1 - Math.abs(dx) / width));
      shiftPills(swipe, swipe.rowTo - swipe.rowFrom, ms);
      slide(ghost, -dir * width, ms);
      place(0, ms, ease);
      await settle(ms + 20);
      ghost.remove();
      lift = 0;
      place(0, 0);
      window.scrollTo(0, 0);
    } else {
      // Not far enough: this page springs back, and the pill it was on is
      // pressed again behind it.
      shiftPills(swipe, 0, 200);
      slide(ghost, 0, 200);
      place(dir * width, 200, ease);
      await settle(210);
      await drawn;
      const back = whenDrawn();
      from.click();
      await back;
      ghost.remove();
      lift = 0;
      place(0, 0);
      window.scrollTo(0, scroll);
    }
    promote(false);
    document.documentElement.style.overflowAnchor = '';
    document.body.style.minHeight = '';
    document.documentElement.classList.remove('swiping');
    // The pills were only drawn moved: now the row really scrolls there, in
    // the same frame as they come back to their places, so nothing jumps.
    swipe.row.scrollLeft = commit ? swipe.rowTo : swipe.rowFrom;
    // With transitions off while they go back: the + has one of its own (for
    // growing under a dragged pill), which animated it back from
    // where it was drawn - leaving its place, then sliding in: a pop.
    for (const b of swipe.rowPills) {
      b.style.transition = 'none';
      b.style.transform = '';
      b.style.willChange = '';
    }
    requestAnimationFrame(() => { for (const b of swipe.rowPills) b.style.transition = ''; });
    state.pillSwiping = false;
    busy = false;
  }

  document.addEventListener('touchstart', (event) => {
    g = null;
    if (event.touches.length !== 1 || !eligible(event.target)) return;
    const t = event.touches[0];
    g = { x: t.clientX, y: t.clientY, at: Date.now(), lock: null, dx: 0, samples: [] };
  }, { passive: true });

  document.addEventListener('touchmove', (event) => {
    if (state.dragSelect || state.holding) { g = null; return; }
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
      place(g.dx, 0);
    }
  }, { passive: false });

  function release() {
    const swipe = g;
    g = null;
    if (!swipe || swipe.lock !== 'x') return;
    if (!swipe.target) { place(0, 220, 'cubic-bezier(0,0,.2,1)'); return; }
    const { dx, samples, dir } = swipe;
    const first = samples[0];
    const last = samples[samples.length - 1];
    const speed = first && last && last.at > first.at ? (last.x - first.x) / (last.at - first.at) : 0;
    const toward = Math.sign(dx) === -dir;
    const far = Math.abs(dx) > window.innerWidth * 0.3;
    const flung = Math.abs(dx) > 30 && Math.abs(speed) > 0.35 && Math.sign(speed) === -dir;
    finish(swipe, toward && (far || flung));
  }
  document.addEventListener('touchend', release, { passive: true });
  document.addEventListener('touchcancel', () => {
    const swipe = g;
    g = null;
    if (swipe && swipe.target) finish(swipe, false);
    else place(0, 200, 'ease-out');
  }, { passive: true });
})();

// The header's height, for the pills pinned under it: it wraps to two lines
// on a phone, and its search box can change it.
(function headerHeight() {
  const header = document.querySelector('#app header');
  if (!header) return;
  // The real height, fractions and all: offsetHeight rounds, and on a phone
  // with a fractional pixel ratio that left a sliver between the header and
  // the pills where the page scrolled through.
  const set = () => document.documentElement.style.setProperty('--header-h', `${header.getBoundingClientRect().height}px`);
  set();
  if ('ResizeObserver' in window) new ResizeObserver(set).observe(header);
})();

/* ------------------------------------------------- the phone's status bar */

// The installed app's status bar is the page's theme color, and a page may
// change it while it runs. Everywhere else that is the app's own dark; in Now
// Playing it is the color of the top of the blurred cover behind it, so the
// bar reads as part of the screen instead of a dark strip across its top.
// Android's navigation bar at the bottom is Chrome's, and no page can color
// it.
function setStatusBar(color) {
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.content = color || '#000000';
}

function tintStatusBar(art) {
  tintStatusBar.for = art || '';
  if (!art) { setStatusBar('#07090d'); return; }
  const img = new Image();
  img.onload = () => {
    if (tintStatusBar.for !== art || $('now-playing').classList.contains('hidden')) return;
    try {
      const c = document.createElement('canvas');
      c.width = 16;
      c.height = 16;
      const g = c.getContext('2d', { willReadFrequently: true });
      g.drawImage(img, 0, 0, 16, 16);
      // The top third: what sits under the status bar once blurred.
      const px = g.getImageData(0, 0, 16, 6).data;
      let r = 0, gr = 0, b = 0;
      for (let i = 0; i < px.length; i += 4) { r += px[i]; gr += px[i + 1]; b += px[i + 2]; }
      const n = px.length / 4;
      [r, gr, b] = [r / n, gr / n, b / n];
      // The backdrop's own filter: saturate(1.4) brightness(0.45).
      const l = 0.2126 * r + 0.7152 * gr + 0.0722 * b;
      const out = [r, gr, b].map((v) => Math.round(Math.max(0, Math.min(255, (l + (v - l) * 1.4) * 0.45))));
      setStatusBar(`#${out.map((v) => v.toString(16).padStart(2, '0')).join('')}`);
    } catch {
      setStatusBar('#07090d');
    }
  };
  img.src = art;
}

/* ------------------------------------------------------- Read Along */

// Books there is both an ebook and an audiobook of, matched by the server on
// title and author. A card's cover reads along - the audiobook starts and the
// book opens over it, the player floating at the bottom - and its two
// buttons do one or the other. Where read-along is set up, a card can also be
// synced, after which the page turns with the voice and the sentence being
// read is lit.
async function refreshPairs() {
  const { ok, body } = await api('/api/books/pairs');
  if (!ok || !body) return [];
  state.pairCount = body.pairs.length;
  state.readAlong = Boolean(body.readalong);
  renderTabs();
  return body.pairs;
}

async function showPairs(seq, query) {
  if (!state.pairsShown && !$('results').children.length) showSkeleton($('results'), 'grid');
  state.hasMore = false;
  state.offset = 0;
  const pairs = await refreshPairs();
  if (seq !== state.searchSeq) return;
  const terms = (query || '').toLowerCase().split(/\s+/).filter(Boolean);
  const shown = pairs.filter((p) => {
    const text = [p.ebook.title, p.audiobook.title, ...(p.ebook.creators || []), ...(p.audiobook.creators || [])]
      .join(' ').toLowerCase();
    return terms.every((t) => text.includes(t));
  });
  state.items = [];
  state.pairsShown = true;
  $('results').replaceChildren(...shown.map(pairCard));
  $('status').textContent = shown.length
    ? ''
    : (terms.length ? 'Nothing matches.' : 'No book is on both shelves yet.');
  // While a book is syncing, keep its card current.
  clearTimeout(state.pairsPoll);
  if (pairs.some((p) => p.sync && (p.sync.state === 'queued' || p.sync.state === 'working'))) {
    state.pairsPoll = setTimeout(() => {
      if (state.kind === 'pairs' && seq === state.searchSeq) showPairs(seq, query);
    }, 4000);
  }
}

const pairRef = (item) => ({ sourceId: item.sourceId, id: item.id });

async function readAlong(pair, keptGroup) {
  const kept = keptGroup || state.downloads.groups.find((g) => g.id === pairDownloadID(pair));
  if ((pair.sync && pair.sync.state === 'ready') || (kept && kept.readalong)) {
    const params = new URLSearchParams({
      ebookSource: pair.ebook.sourceId, ebookId: pair.ebook.id,
      audiobookSource: pair.audiobook.sourceId, audiobookId: pair.audiobook.id,
    });
    let { ok, body } = await api(`/api/readalong?${params}`);
    // Offline, a downloaded pair brings its synced book and timeline with it.
    if (!(ok && body && body.item) && kept && kept.readalong) {
      ok = true;
      body = kept.readalong;
    }
    if (ok && body && body.item) {
      play(pair.audiobook);
      closeVideo();
      const book = { ...body.item, title: pair.ebook.title, creators: pair.ebook.creators };
      window.soundstormReader.open(book, {
        timeline: body.timeline || [],
        audiobook: pairRef(pair.audiobook),
      });
      if (!body.timeline || !body.timeline.length) {
        showToast("This book synced, but its chapters do not line up with the recording, so the page will not follow.");
      }
      return;
    }
  }
  play(pair.audiobook);
  play(pair.ebook);
}

async function startSync(pair, line) {
  line.replaceChildren(document.createTextNode('Starting\u2026'));
  const { ok, body } = await api('/api/readalong', {
    method: 'POST',
    body: JSON.stringify({ ebook: pairRef(pair.ebook), audiobook: pairRef(pair.audiobook) }),
  });
  if (!ok) {
    line.replaceChildren(document.createTextNode((body && body.error) || 'Could not start.'));
    return;
  }
  pair.sync = body;
  renderSyncLine(pair, line);
  if (state.kind === 'pairs') showPairs(state.searchSeq, state.query);
}

async function syncNext(pair, line) {
  line.replaceChildren(document.createTextNode('Moving it up\u2026'));
  const { ok, body } = await api('/api/readalong/next', {
    method: 'POST',
    body: JSON.stringify({ audiobook: pairRef(pair.audiobook) }),
  });
  if (!ok) {
    line.replaceChildren(document.createTextNode((body && body.error) || 'Could not move it up.'));
    return;
  }
  if (state.kind === 'pairs') showPairs(state.searchSeq, state.query);
}

function renderSyncLine(pair, line) {
  line.replaceChildren();
  line.className = 'pair-sync';
  if (!state.readAlong) return;
  const sync = pair.sync;
  const button = (label, action) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'linkish';
    b.textContent = label;
    b.addEventListener('click', action);
    return b;
  };
  if (!sync) {
    line.append(button('Turn pages with the audio', () => startSync(pair, line)));
    return;
  }
  switch (sync.state) {
    case 'ready':
      // Synced is the normal state: nothing to say.
      line.classList.add('ready');
      break;
    case 'failed':
    case 'stopped':
      line.append(document.createTextNode('Could not sync. '), button('Try again', () => startSync(pair, line)));
      break;
    default: {
      const pct = Math.round((sync.progress || 0) * 100);
      line.append(document.createTextNode(sync.state === 'queued' ? 'Waiting to sync'
        : `${sync.stage || 'Syncing'}\u2026 ${pct}%`));
      // Not already next in line: offer to make it so.
      if (sync.state === 'queued' && !(sync.place === 1)) {
        line.append(document.createTextNode(' \u00b7 '), button('Sync this next', () => syncNext(pair, line)));
      }
      const bar = document.createElement('div');
      bar.className = 'pair-bar';
      const fill = document.createElement('i');
      fill.style.width = `${pct}%`;
      bar.append(fill);
      line.append(bar);
    }
  }
}

function pairCard(pair) {
  const { ebook, audiobook } = pair;
  const holder = document.createElement('div');
  // An item holder, so the corner arrow is placed as it is on every card.
  holder.className = 'item-holder pair-holder';
  const card = document.createElement('button');
  card.type = 'button';
  card.className = 'item pair-card';
  card.setAttribute('aria-label', `Read ${ebook.title} while listening`);
  const art = artPath(audiobook) || artPath(ebook);
  const meta = document.createElement('div');
  meta.className = 'meta';
  const title = document.createElement('span');
  title.className = 'title';
  title.textContent = ebook.title;
  title.title = ebook.title;
  const sub = document.createElement('span');
  sub.className = 'sub';
  // Which recording, when it is not the plain one: "Full-Cast Edition".
  const edition = (audiobook.title.match(/\(([^)]*edition[^)]*)\)/i) || [])[1];
  sub.textContent = [(ebook.creators || audiobook.creators || [])[0], edition].filter(Boolean).join(' \u00b7 ');
  meta.append(title, sub);
  card.append(coverArt(art, ebook.title), meta);
  card.addEventListener('click', (event) => {
    // The click that ends a hold is not a tap: the menu is open.
    if (state.suppressClick) {
      state.suppressClick = false;
      event.preventDefault();
      event.stopPropagation();
      return;
    }
    readAlong(pair);
  });
  attachItemMenuGestures(card, { ...ebook, kind: 'pair', pair });

  // Downloading is the corner arrow every card has; the tick once it is here.
  const cover = card.querySelector('.art-wrap');
  if (state.downloads.groups.some((g) => g.id === pairDownloadID(pair))) cover.append(downloadedBadge());
  const get = downloadsPossible() && !state.downloads.groups.some((g) => g.id === pairDownloadID(pair))
    ? makeGet(ebook.title, false, async (progress) => {
      await downloadPair(pair, progress);
      cover.append(downloadedBadge());
    })
    : null;
  const line = document.createElement('div');
  renderSyncLine(pair, line);
  holder.append(card, line);
  if (get) holder.append(get);
  return holder;
}

/* ---------------------------------------------- reordering a row of pills */

// Hold a pill and slide it along the row to move it. The hold is the same
// 450ms as a card's; moving first is a scroll of the row, lifting early a tap.
// Once it lifts, the row stops scrolling under the finger, the pill follows
// it, and the others slide out of its way as it passes their middles. Music's
// row is the static buttons, put in order at start; a tab's shelf row is
// built by renderTabs, which reads the order through tabShelves.
function applyMusicPillOrder() {
  const row = $('music-tabs');
  const order = pillOrder('music');
  const buttons = [...row.querySelectorAll('[data-view]')];
  const rank = (b) => { const i = order.indexOf(b.dataset.view); return i < 0 ? 1e6 : i; };
  buttons.map((b, i) => ({ b, i }))
    .sort((x, y) => (rank(x.b) - rank(y.b)) || (x.i - y.i))
    .forEach(({ b }) => row.append(b));
  row.append(row.querySelector('.pill-add'));
}
applyMusicPillOrder();

(function pillReorder() {
  const HOLD_MS = 450;
  const SLOP = 10;
  let g = null;

  const rowOf = (el) => el && el.closest('#music-tabs, #subtabs');
  const keyOf = (b) => b.dataset.view || b.dataset.kind;
  const pillsIn = (row) => [...row.querySelectorAll('button')].filter(isPill);

  function start(event) {
    const pill = event.target.closest('#music-tabs button, #subtabs button');
    if (pill && !isPill(pill)) return;
    if (!pill || g || (event.pointerType === 'mouse' && event.button !== 0)) return;
    const row = rowOf(pill);
    g = { pill, row, x: event.clientX, y: event.clientY, id: event.pointerId, lifted: false };
    g.timer = setTimeout(() => lift(event.clientX), HOLD_MS);
  }

  function lift(x) {
    if (!g) return;
    g.lifted = true;
    g.grab = x - g.pill.getBoundingClientRect().left;
    g.row.classList.add('reordering');
    g.pill.classList.add('lifted');
    if (navigator.vibrate) navigator.vibrate(12);
  }

  // Slide the siblings from where they were to where they now are, so a
  // reorder reads as them moving aside rather than jumping.
  function flip(pills, before) {
    for (const p of pills) {
      if (p === g.pill) continue;
      const was = before.get(p);
      const now = p.getBoundingClientRect().left;
      if (was === undefined || was === now) continue;
      p.style.transition = 'none';
      p.style.transform = `translateX(${was - now}px)`;
      requestAnimationFrame(() => {
        p.style.transition = 'transform 0.18s ease-out';
        p.style.transform = '';
      });
    }
  }

  function follow(x) {
    const { pill, row } = g;
    // The row scrolls itself when the pill is dragged near either end.
    const box = row.getBoundingClientRect();
    if (x < box.left + 40) row.scrollLeft -= 8;
    else if (x > box.right - 40) row.scrollLeft += 8;

    const pills = pillsIn(row);
    const others = pills.filter((p) => p !== pill);
    let target = others.length;
    for (let i = 0; i < others.length; i++) {
      const r = others[i].getBoundingClientRect();
      if (x < r.left + r.width / 2) { target = i; break; }
    }
    const now = others.indexOf(pills[pills.indexOf(pill) + 1]);
    const at = now < 0 ? others.length : now;
    if (target !== at) {
      const before = new Map(pills.map((p) => [p, p.getBoundingClientRect().left]));
      row.insertBefore(pill, others[target] || row.querySelector('.pill-add'));
      flip(pillsIn(row), before);
    }
    // The pill itself sits under the finger, wherever its slot is now.
    pill.style.transition = 'none';
    pill.style.transform = '';
    const slot = pill.getBoundingClientRect().left;
    pill.style.transform = `translateX(${x - g.grab - slot}px) scale(1.06)`;
  }

  function move(event) {
    if (!g || event.pointerId !== g.id) return;
    if (!g.lifted) {
      if (Math.hypot(event.clientX - g.x, event.clientY - g.y) > SLOP) end(false);
      return;
    }
    // Over the +, it is being put away rather than moved - and the row holds
    // still: near the edge it would scroll, and slide the + from under the
    // finger.
    const plus = g.row.querySelector('.pill-add');
    const r = plus && plus.getBoundingClientRect();
    g.overPlus = Boolean(r && event.clientX > r.left - 10 && event.clientX < r.right + 10);
    if (plus) plus.classList.toggle('drop-target', g.overPlus);
    if (g.overPlus) {
      // Under the finger, a little smaller: it is on its way out.
      g.pill.style.transition = 'none';
      g.pill.style.transform = '';
      const slot = g.pill.getBoundingClientRect().left;
      g.pill.style.transform = `translateX(${event.clientX - g.grab - slot}px) scale(0.9)`;
      return;
    }
    follow(event.clientX);
  }

  function end(commit = true) {
    if (!g) return;
    clearTimeout(g.timer);
    const { pill, row, lifted, overPlus } = g;
    g = null;
    if (!lifted) return;
    const plus = row.querySelector('.pill-add');
    if (plus) plus.classList.remove('drop-target');
    if (commit && overPlus) {
      pill.classList.remove('lifted');
      row.classList.remove('reordering');
      pill.style.transition = '';
      pill.style.transform = '';
      const swallow = (e) => { e.stopPropagation(); e.preventDefault(); };
      pill.addEventListener('click', swallow, { capture: true, once: true });
      setTimeout(() => pill.removeEventListener('click', swallow, { capture: true }), 400);
      putPillAway(row, keyOf(pill));
      return;
    }
    pill.classList.remove('lifted');
    row.classList.remove('reordering');
    pill.style.transition = 'transform 0.18s ease-out';
    pill.style.transform = '';
    setTimeout(() => { pill.style.transition = ''; }, 200);
    // The lift ends in a click on the pill; it is not a choice of shelf.
    const swallow = (e) => { e.stopPropagation(); e.preventDefault(); };
    pill.addEventListener('click', swallow, { capture: true, once: true });
    setTimeout(() => pill.removeEventListener('click', swallow, { capture: true }), 400);
    if (!commit) return;
    const keys = [...row.querySelectorAll('button')].map(keyOf).filter(Boolean);
    if (row.id === 'music-tabs') {
      savePillOrder('music', keys);
    } else {
      // Shelves the account cannot see now keep their place for later.
      const kept = pillOrder(state.tab).filter((k) => !keys.includes(k));
      savePillOrder(state.tab, [...keys, ...kept]);
    }
  }

  document.addEventListener('pointerdown', start);
  document.addEventListener('pointermove', move);
  document.addEventListener('pointerup', () => end(true));
  document.addEventListener('pointercancel', () => end(false));
  // While a pill is up, the row must not scroll away under the finger, and a
  // held touch must not become the browser's own long-press.
  document.addEventListener('touchmove', (event) => {
    if (g && g.lifted && event.cancelable) event.preventDefault();
  }, { passive: false });
  document.addEventListener('contextmenu', (event) => {
    if (g && event.target.closest('#music-tabs, #subtabs')) event.preventDefault();
  });
})();

/* ------------------------------------------------------------------ speed */

// How fast audiobooks play, from 0.75x to 3x, remembered on the account.
// Music always plays at its own speed. Set as the default rate as well as
// the current one, because giving the player a new file (the next chapter
// of a multi-file book) resets the current rate to the default.
function bookSpeed() {
  const v = Number(state.prefs && state.prefs.audiobookSpeed);
  return v >= 0.5 && v <= 3.5 ? v : 1;
}

function applySpeed() {
  const player = $('audio-player');
  const rate = audio.item && audio.item.kind === 'audiobook' ? bookSpeed() : 1;
  player.defaultPlaybackRate = rate;
  if (player.playbackRate !== rate) player.playbackRate = rate;
}

function renderSpeed() {
  const v = bookSpeed();
  $('np-speed').textContent = `${v}\u00d7`;
  $('np-speed').classList.toggle('on', v !== 1);
  for (const b of document.querySelectorAll('#np-speed-menu [data-speed]')) {
    b.classList.toggle('chosen', Number(b.dataset.speed) === v);
  }
}

for (const event of ['loadedmetadata', 'play']) {
  $('audio-player').addEventListener(event, applySpeed);
}
$('np-speed').addEventListener('click', (event) => {
  event.stopPropagation();
  const menu = $('np-speed-menu');
  const opening = menu.classList.contains('hidden');
  show(menu, opening);
  $('np-speed').setAttribute('aria-expanded', String(opening));
});
for (const choice of document.querySelectorAll('#np-speed-menu [data-speed]')) {
  choice.addEventListener('click', (event) => {
    event.stopPropagation();
    const v = Number(choice.dataset.speed);
    state.prefs.audiobookSpeed = v;
    applySpeed();
    renderSpeed();
    updateMediaSession();
    show($('np-speed-menu'), false);
    $('np-speed').setAttribute('aria-expanded', 'false');
    savePrefs({ audiobookSpeed: v });
  });
}
document.addEventListener('click', () => {
  show($('np-speed-menu'), false);
  $('np-speed').setAttribute('aria-expanded', 'false');
});

/* ------------------------------------------------ downloading books */

// Audiobooks, ebooks and documents can be kept on the device like songs, and
// a Read Along book as both halves - with its synced text and timeline,
// so the page follows the voice offline too. Films and TV cannot: a film is
// gigabytes, and one a browser cannot play is converted by the server as it
// plays, which cannot happen with no server. Photos are not offered either:
// the phone keeps its own, and a single one can already be saved.
//
// Everything lands in the same cache as songs, under the address the app
// would ask the server for, so the one index and the one Remove serve both.
// The reader and the player look there when the server does not answer.

window.soundstormOfflineURL = (item) => offlineURL(item);

async function offlineURLFor(url) {
  try {
    const resp = await (await caches.open(OFFLINE_CACHE)).match(url);
    return resp ? URL.createObjectURL(await resp.blob()) : '';
  } catch {
    return '';
  }
}

// keepURL fetches one address into the cache, streaming, and reports bytes as
// they arrive, so a single 400MB audiobook file still shows progress.
async function keepURL(cache, url, onBytes) {
  const resp = await fetch(url, { credentials: 'same-origin' });
  if (!resp.ok || !resp.body) throw new Error(`status ${resp.status}`);
  const total = Number(resp.headers.get('Content-Length')) || 0;
  let got = 0;
  const counted = resp.body.pipeThrough(new TransformStream({
    transform(chunk, controller) {
      got += chunk.byteLength;
      if (onBytes) onBytes(got, total);
      controller.enqueue(chunk);
    },
  }));
  await cache.put(url, new Response(counted, { status: 200, headers: resp.headers }));
}

// The addresses the reader will ask for, built the way reader.js builds them.
function bookURLs(item, names) {
  const base = new URLSearchParams({ source: item.sourceId, id: item.id });
  const manifest = `/api/book/manifest?${base}`;
  const resources = names.map((name) => {
    const params = new URLSearchParams(base);
    params.set('path', name);
    return `/api/book/resource?${params}`;
  });
  return { manifest, resources };
}

// keepBook saves what reading one book needs; skip leaves out entries the
// reader will never ask for (a synced book's own copy of the audio).
async function keepBook(cache, item, onProgress, skip) {
  const isPDF = ((item.extra && item.extra.format) || '').toLowerCase() === 'pdf';
  if (isPDF) {
    await keepURL(cache, streamPath(item), (got, total) => onProgress(total ? got / total : 0));
    return [streamPath(item)];
  }
  const { manifest } = bookURLs(item, []);
  const resp = await fetch(manifest, { credentials: 'same-origin' });
  if (!resp.ok) throw new Error('could not read the book');
  const body = await resp.clone().json();
  await cache.put(manifest, resp);
  const names = (body.entries || []).map((e) => e.name).filter((n) => !(skip && skip(n)));
  const { resources } = bookURLs(item, names);
  let done = 0;
  for (const url of resources) {
    await keepURL(cache, url).catch(() => {}); // one missing picture does not lose the book
    onProgress(++done / resources.length);
  }
  return [manifest, ...resources];
}

async function keepArt(cache, item) {
  const art = artPath(item);
  if (!art) return [];
  if (!(await cache.match(art))) await keepURL(cache, art).catch(() => {});
  return [art];
}

// downloadBook keeps one audiobook, ebook or document; onProgress gets 0-1.
async function downloadBook(item, onProgress = () => {}, shouldStop) {
  if (navigator.storage && navigator.storage.persist) navigator.storage.persist().catch(() => {});
  const cache = await caches.open(OFFLINE_CACHE);
  const key = selectionKey(item);
  let files = [];
  let tracks = null;
  let video = null;
  if (item.kind === 'audiobook') {
    const { ok, body } = await api(`/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`);
    if (!ok) throw new Error('could not read the audiobook');
    tracks = body && Array.isArray(body.tracks) && body.tracks.length > 1 ? body.tracks : null;
    const urls = tracks ? tracks.map((t) => t.url) : [streamPath(item)];
    for (let i = 0; i < urls.length; i++) {
      await keepURL(cache, urls[i], (got, total) => onProgress((i + (total ? got / total : 0)) / urls.length));
      files.push(urls[i]);
    }
  } else if (isVideoItem(item)) {
    const kept = await keepVideo(cache, item, onProgress, shouldStop);
    files = kept.files;
    video = kept.video;
  } else if (item.kind === 'picture') {
    // The picture the viewer shows; the thumbnail is the artwork, below.
    await keepURL(cache, photoPreview(item));
    files = [photoPreview(item)];
    onProgress(1);
  } else {
    files = await keepBook(cache, item, onProgress);
  }
  files.push(...await keepArt(cache, item));
  state.downloads.items[key] = { ...item, dl: { files, tracks, video } };
  const id = `book:${key}`;
  state.downloads.groups = state.downloads.groups.filter((g) => g.id !== id);
  state.downloads.groups.unshift({
    id, type: item.kind, title: item.title, subtitle: (item.creators || []).join(', '),
    sourceId: item.sourceId, artId: item.artId, keys: [key], at: Date.now(),
  });
  saveDownloadIndex();
  keepShell();
  markMusicTabs();
  markDownloads();
}

function pairDownloadID(pair) {
  return `pair:${selectionKey(pair.ebook)}|${selectionKey(pair.audiobook)}`;
}

// downloadPair keeps both halves of a Read Along book, and where it is
// synced, the synced text (not its copy of the audio - the audiobook is
// already here) and the timeline.
async function downloadPair(pair, onProgress = () => {}) {
  const hasBook = (item) => isDownloaded(item);
  const parts = [pair.audiobook, pair.ebook].filter((it) => !hasBook(it));
  let step = 0;
  const steps = parts.length + 1;
  for (const it of parts) {
    await downloadBook(it, (p) => onProgress((step + p) / steps));
    step++;
    // Kept as part of the pair, not as books of their own.
    state.downloads.groups = state.downloads.groups.filter((g) => g.id !== `book:${selectionKey(it)}`);
  }
  const keys = [selectionKey(pair.ebook), selectionKey(pair.audiobook)];
  let readalong = null;
  if (pair.sync && pair.sync.state === 'ready') {
    const params = new URLSearchParams({
      ebookSource: pair.ebook.sourceId, ebookId: pair.ebook.id,
      audiobookSource: pair.audiobook.sourceId, audiobookId: pair.audiobook.id,
    });
    const { ok, body } = await api(`/api/readalong?${params}`);
    if (ok && body && body.item) {
      const cache = await caches.open(OFFLINE_CACHE);
      const files = await keepBook(cache, body.item, (p) => onProgress((step + p) / steps),
        (name) => name.startsWith('Audio/') || /\.(mp3|mp4|m4a|m4b|aac|ogg|opus)$/i.test(name));
      const syncedKey = selectionKey(body.item);
      state.downloads.items[syncedKey] = { ...body.item, dl: { files } };
      keys.push(syncedKey);
      readalong = { item: body.item, timeline: body.timeline || [] };
    }
  }
  onProgress(1);
  const id = pairDownloadID(pair);
  state.downloads.groups = state.downloads.groups.filter((g) => g.id !== id);
  state.downloads.groups.unshift({
    id, type: 'pair', title: pair.ebook.title, subtitle: (pair.ebook.creators || []).join(', '),
    sourceId: pair.audiobook.sourceId, artId: pair.audiobook.artId, keys, at: Date.now(),
    pair: { ebook: pair.ebook, audiobook: pair.audiobook, sync: pair.sync }, readalong,
  });
  saveDownloadIndex();
  keepShell();
  markMusicTabs();
  markDownloads();
}

// A download can take minutes for a long audiobook; the toast says how far.
async function downloadWithToast(title, run) {
  let shown = -1;
  showToast(`Downloading ${title}\u2026`, null, null, 600000);
  try {
    await run((p) => {
      const pct = Math.floor(p * 100);
      if (pct !== shown) {
        shown = pct;
        showToast(`Downloading ${title}\u2026 ${pct}%`, null, null, 600000);
      }
    });
    showToast(`${title} is on this device.`);
  } catch {
    showToast(`Could not download ${title}.`);
  }
}

/* -------------------------------------------------------- download all */

// Download all: an artist's albums, the favorites, every Read Along book,
// or a whole shelf. One job at a time, one item after another, with its
// progress and a Stop in the message at the bottom. A whole shelf says how
// much it is and how much room the device has, and asks first.
state.bulk = null;

async function bulkDownload(title, makeTasks, confirmFirst) {
  if (state.bulk) {
    showToast('Already downloading. Stop that first, from the message below.');
    return;
  }
  const job = { stop: false };
  state.bulk = job;
  try {
    showToast(`Getting ready to download ${title}\u2026`, 'Stop', () => { job.stop = true; }, 600000);
    const tasks = await makeTasks();
    if (!tasks.length) {
      showToast(`Everything in ${title} is already on this device.`);
      return;
    }
    if (confirmFirst && !(await confirmFirst(tasks))) {
      showToast('Nothing downloaded.');
      return;
    }
    if (navigator.storage && navigator.storage.persist) navigator.storage.persist().catch(() => {});
    let done = 0;
    for (let i = 0; i < tasks.length && !job.stop; i++) {
      const task = tasks[i];
      let shown = -1;
      const say = (p) => {
        const pct = Math.floor((p || 0) * 100);
        if (pct === shown) return;
        shown = pct;
        const place = tasks.length > 1 ? ` ${i + 1} of ${tasks.length}:` : '';
        showToast(`Downloading${place} ${task.label}\u2026 ${pct}%`, 'Stop', () => { job.stop = true; }, 600000);
      };
      say(0);
      try {
        await task.run(say, () => job.stop);
        done++;
      } catch {
        // One that fails does not stop the rest.
      }
    }
    markMusicTabs();
    showToast(job.stop
      ? `Stopped. ${done} of ${tasks.length} downloaded.`
      : `${done === tasks.length ? `Downloaded ${title}` : `Downloaded ${done} of ${tasks.length} from ${title}`}.`);
  } finally {
    state.bulk = null;
  }
}

// Every item on a shelf, a page at a time, as the list itself would load them.
async function wholeShelf(kind) {
  const items = [];
  for (let offset = 0; offset < 10000;) {
    const params = new URLSearchParams({ q: '', kind, limit: '200', offset: String(offset) });
    const { ok, body } = await api(`/api/search?${params}`);
    if (!ok || !body) break;
    items.push(...body.items);
    if (!body.hasMore || !body.items.length) break;
    offset = (body.offset || offset) + body.items.length;
  }
  return items;
}

const bookTask = (item) => ({ label: item.title, run: (progress, stopped) => downloadBook(item, progress, stopped) });

async function roomLeft() {
  if (!navigator.storage || !navigator.storage.estimate) return '';
  const est = await navigator.storage.estimate().catch(() => null);
  return est && est.quota ? ` This device has about ${formatStorage(est.quota - (est.usage || 0))} free for SoundStorm.` : '';
}

function downloadAllOf(kind) {
  if (kind === 'favorites') {
    bulkDownload('your favorites', async () => {
      const items = await loadFavoriteKeys();
      const songs = items.filter((it) => it.kind === 'music');
      const tasks = [];
      if (songs.some((s) => !isDownloaded(s))) {
        tasks.push({
          label: 'favorite songs',
          run: (progress, stopped) => download(
            { id: 'favorites', type: 'favorites', title: 'Favorites', subtitle: 'Songs' },
            songs, (done, total) => progress(done / total), stopped),
        });
      }
      for (const it of items) {
        if (it.kind !== 'music' && canDownload(it) && !isDownloaded(it)) tasks.push(bookTask(it));
      }
      return tasks;
    });
  } else if (kind === 'pairs') {
    bulkDownload('Read Along', async () => {
      const pairs = await refreshPairs();
      return pairs
        .filter((p) => !state.downloads.groups.some((g) => g.id === pairDownloadID(p)))
        .map((p) => ({ label: p.ebook.title, run: (progress) => downloadPair(p, progress) }));
    }, async (tasks) => window.confirm(
      `Download ${tasks.length} book${tasks.length === 1 ? '' : 's'}, each with its audiobook? `
      + `Audiobooks are often several hundred MB each.${await roomLeft()}`));
  } else if (kind === 'music') {
    bulkDownload('all songs', async () => {
      const songs = await wholeShelf('music');
      if (songs.every((s) => isDownloaded(s))) return [];
      return [{
        label: `${songs.length} songs`,
        run: (progress, stopped) => download(
          { id: 'shelf:music', type: 'shelf', title: 'All songs', subtitle: 'Your whole music library' },
          songs, (done, total) => progress(done / total), stopped),
      }];
    }, async (tasks) => window.confirm(
      `Download all ${tasks[0].label} to this device? That can be many GB.${await roomLeft()}`));
  } else if (['audiobook', 'ebook', 'document', 'video', 'tv', 'picture'].includes(kind)) {
    const names = {
      audiobook: 'audiobooks', ebook: 'ebooks', document: 'documents', video: 'films', tv: 'episodes', picture: 'photos',
    };
    const warn = {
      audiobook: ' Audiobooks are often several hundred MB each.',
      video: ' Films are usually one to a few GB each, and ones that need converting take a while.',
      tv: ' Episodes are often several hundred MB each, and ones that need converting take a while.',
    };
    bulkDownload(`all ${names[kind]}`,
      async () => (await wholeShelf(kind)).filter((it) => canDownload(it) && !isDownloaded(it)).map(bookTask),
      async (tasks) => window.confirm(
        `Download ${tasks.length} ${names[kind]} to this device?${warn[kind] || ''}${await roomLeft()}`));
  }
}

// Settings, Downloads: a button for each whole shelf this account has.
const WHOLE_SHELVES = [
  { kind: 'music', label: 'All songs' }, { kind: 'favorites', label: 'Favorites' },
  { kind: 'audiobook', label: 'Audiobooks' }, { kind: 'ebook', label: 'Ebooks' },
  { kind: 'pairs', label: 'Read Along' }, { kind: 'document', label: 'Documents' },
  { kind: 'video', label: 'Films' }, { kind: 'tv', label: 'TV' }, { kind: 'picture', label: 'Photos' },
];

function renderDownloadShelves() {
  const shelves = downloadsPossible() ? WHOLE_SHELVES.filter((s) => shelfAvailable(s.kind)) : [];
  $('download-shelves-row').replaceChildren(...shelves.map((s) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'ghost small';
    b.append(icon('download'), document.createTextNode(s.label));
    b.addEventListener('click', () => downloadAllOf(s.kind));
    return b;
  }));
  show($('download-shelves'), shelves.length > 0);
}

/* ------------------------------------------- downloading films and photos */

// Films, episodes and photos download too. A film the browser can play as
// it is is kept as it is. One it cannot is kept as the streaming version the
// app would play - the server's HLS playlist and every piece of it, which for
// a file that only needs repackaging is quick and for one that needs
// converting takes as long as the server takes to convert it. Played from the
// device with hls.js, from a playlist rewritten to point at the kept pieces.
// A series card itself is not a download: nothing lists its episodes here, so
// episodes are downloaded one by one, or with Download all on the TV shelf.
const SINGLE_DOWNLOADS = ['audiobook', 'ebook', 'document', 'video', 'tv', 'picture'];

function isVideoItem(item) {
  return item.kind === 'video' || item.kind === 'tv'
    || (item.kind === 'picture' && item.extra && item.extra.type === 'video');
}

function canDownload(item) {
  if (!item || !SINGLE_DOWNLOADS.includes(item.kind)) return false;
  // A series has no file of its own; an episode says which one it is.
  if (item.kind === 'tv') return Boolean(item.extra && item.extra.episode);
  return true;
}

// hlsParts is every address a media playlist leads to: its pieces, and the
// initialization piece an fMP4 playlist names in EXT-X-MAP.
function hlsParts(text, base) {
  const parts = [];
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (!line) continue;
    if (line.startsWith('#EXT-X-MAP:')) {
      const m = line.match(/URI="([^"]+)"/);
      if (m) parts.push(new URL(m[1], base).href);
    } else if (!line.startsWith('#')) {
      parts.push(new URL(line, base).href);
    }
  }
  return parts;
}

async function keepVideo(cache, item, onProgress, shouldStop) {
  const { ok, body } = await api(`/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`);
  if (!ok || !body) throw new Error('could not ask how to play it');
  const files = [];
  const subtitles = [];
  for (const sub of body.subtitles || []) {
    try {
      await keepURL(cache, sub.url);
      files.push(sub.url);
      subtitles.push(sub);
    } catch { /* a missing subtitle does not lose the film */ }
  }
  if (body.mode !== 'hls') {
    const url = body.url || streamPath(item);
    await keepURL(cache, url, (got, total) => onProgress(total ? got / total : 0));
    files.push(url);
    return { files, video: { mode: 'direct', url, subtitles } };
  }
  // The streaming version: the playlist names a variant, which names every piece.
  // hls.js plays it back, so it has to be on the device too.
  await keepURL(cache, '/static/vendor/hls.js/hls.min.js').catch(() => {});
  const master = new URL(body.url, location.href).href;
  const masterText = await (await fetch(master, { credentials: 'same-origin' })).text();
  const first = masterText.split('\n').map((l) => l.trim()).find((l) => l && !l.startsWith('#'));
  if (!first) throw new Error('the stream has no playlist');
  const variant = new URL(first, master).href;
  const variantResp = await fetch(variant, { credentials: 'same-origin' });
  if (!variantResp.ok) throw new Error('the stream did not start');
  const variantText = await variantResp.text();
  const parts = hlsParts(variantText, variant);
  for (let i = 0; i < parts.length; i++) {
    if (shouldStop && shouldStop()) throw new Error('stopped');
    await keepURL(cache, parts[i]);
    onProgress((i + 1) / parts.length);
  }
  // Kept last, so a download that stopped part way is not taken for a whole one.
  await cache.put(variant, new Response(variantText, { headers: { 'Content-Type': 'application/vnd.apple.mpegurl' } }));
  files.push(variant);
  return { files, video: { mode: 'hls', variant, subtitles } };
}

// playKeptVideo plays a downloaded film or episode from the device. False if
// what was kept cannot be found, so the network can be tried instead.
async function playKeptVideo(item, player) {
  const kept = state.downloads.items[selectionKey(item)];
  const video = kept && kept.dl && kept.dl.video;
  if (!video) return false;
  const subs = [];
  for (const sub of video.subtitles || []) {
    const url = await offlineURLFor(sub.url);
    if (url) subs.push({ ...sub, url });
  }
  attachSubtitles(player, subs);
  if (video.mode === 'direct') {
    const url = await offlineURLFor(video.url);
    if (!url) return false;
    player.src = url;
    player.play().catch(() => {});
    return true;
  }
  const cache = await caches.open(OFFLINE_CACHE);
  const playlist = await cache.match(video.variant);
  if (!playlist) return false;
  const text = await playlist.text();
  // Each piece's address becomes the device's copy of it.
  const local = {};
  for (const url of hlsParts(text, video.variant)) {
    local[url] = await offlineURLFor(url);
    if (!local[url]) return false;
  }
  const rewritten = text.split('\n').map((raw) => {
    const line = raw.trim();
    if (line.startsWith('#EXT-X-MAP:')) {
      return line.replace(/URI="([^"]+)"/, (_m, uri) => `URI="${local[new URL(uri, video.variant).href]}"`);
    }
    if (line && !line.startsWith('#')) return local[new URL(line, video.variant).href];
    return raw;
  }).join('\n');
  const src = URL.createObjectURL(new Blob([rewritten], { type: 'application/vnd.apple.mpegurl' }));
  // hls.js rather than the browser's own HLS, which will not follow a
  // playlist of blob: addresses.
  try {
    const Hls = await loadHls();
    if (!Hls || !Hls.isSupported()) throw new Error('no hls.js');
    hls = new Hls({ enableWorker: true });
    hls.on(Hls.Events.ERROR, (_event, data) => {
      if (data && data.fatal) {
        $('video-caption').textContent = `Could not play this download: ${data.details || 'stream error'}`;
        detachHls();
      }
    });
    hls.loadSource(src);
    hls.attachMedia(player);
    hls.on(Hls.Events.MANIFEST_PARSED, () => player.play().catch(() => {}));
  } catch {
    player.src = src;
    player.play().catch(() => {});
  }
  return true;
}

/* ------------------------------------------------------------ offline mode */

// With no connection to the server - opened offline, or the connection lost
// while open - the app stays itself: the same tabs and shelves, each showing
// only what is downloaded to this device, and search looking through that.
// What needs the server (Settings, the Select and Download all buttons) steps
// aside. When the connection comes back, so does everything else.
state.offline = false;

const MUSIC_GROUPS = ['song', 'album', 'playlist', 'artist', 'favorites', 'shelf', 'mix'];

function offlineKinds() {
  const kinds = new Set();
  for (const item of Object.values(state.downloads.items)) {
    if (item && item.kind && item.sourceId !== 'storyteller') kinds.add(item.kind);
  }
  if (state.downloads.groups.some((g) => g.type === 'pair')) kinds.add('pairs');
  return kinds;
}

function enterOffline(atStart) {
  if (state.offline) return;
  state.offline = true;
  document.body.classList.add('offline-mode');
  show($('tabs'), true);
  renderTabs();
  if (state.tab === 'settings' || !tabShelves(state.tab).length) selectTab('home');
  else runSearch();
  if (!atStart) showToast("You're offline. Showing what's on this device.");
}

async function leaveOffline() {
  if (!state.offline) return;
  const { ok } = await api('/api/session');
  if (!ok) return;
  // Opened offline, the app never signed in: start it properly.
  if (!state.me) {
    location.reload();
    return;
  }
  state.offline = false;
  document.body.classList.remove('offline-mode');
  renderTabs();
  runSearch();
  showToast('Back online.');
}

window.addEventListener('offline', () => {
  if (hasDownloads() && !$('app').classList.contains('hidden')) enterOffline();
});
window.addEventListener('online', () => { leaveOffline(); });

async function showOfflineShelf(seq, query) {
  let kind = state.kind;
  const words = (query || '').toLowerCase().split(/\s+/).filter(Boolean);
  const matches = (item) => {
    const text = [item.title, item.subtitle, ...(item.creators || [])].join(' ').toLowerCase();
    return words.every((w) => text.includes(w));
  };
  const music = kind === 'music' || kind === 'playlists';
  for (const id of ['playlists-view', 'continue', 'loading-more']) {
    show($(id), false);
  }
  show($('music-tabs'), music);
  show($('music-view'), false);
  show($('home-view'), kind === '' && !words.length);
  show($('results-bar'), !(kind === '' && !words.length));
  show($('results'), !(kind === '' && !words.length));
  const kept = (k) => Object.values(state.downloads.items)
    .filter((it) => it && it.kind === k && it.sourceId !== 'storyteller' && matches(it));

  // Home: the same strips as online, one for each kind that is downloaded.
  if (kind === '' && !words.length) {
    const view = $('home-view');
    view.replaceChildren();
    const albums = state.downloads.groups.filter((g) => MUSIC_GROUPS.includes(g.type) && g.type !== 'song');
    if (albums.length) view.append(homeRow('Music', albums.map(groupCard), () => selectKind('music')));
    const songs = kept('music');
    if (songs.length && !albums.length) view.append(homeRow('Songs', songs.map(renderItem), () => selectKind('music')));
    const pairs = offlinePairs(matches);
    if (pairs.length) view.append(homeRow('Read Along', pairs.map(pairCard), () => selectKind('pairs')));
    const names = { audiobook: 'Audiobooks', ebook: 'Books', document: 'Documents', video: 'Films', tv: 'TV', picture: 'Photos' };
    const all = [];
    for (const k of Object.keys(names)) {
      const items = kept(k);
      all.push(...items);
      if (items.length) view.append(homeRow(names[k], items.map(renderItem), () => selectKind(k)));
    }
    state.items = all;
    $('status').textContent = '';
    return;
  }

  let cards;
  if (music) {
    // Music: the same pills, over what is downloaded.
    if (kind === 'playlists') state.musicView = 'playlists';
    if (!['songs', 'albums', 'playlists'].includes(state.musicView)) state.musicView = 'songs';
    markMusicTabs();
    const view = state.musicView;
    if (view === 'songs') {
      const songs = kept('music');
      state.items = songs;
      cards = songs.map(renderItem);
    } else {
      const types = view === 'albums' ? ['album', 'artist'] : ['playlist', 'favorites', 'shelf'];
      cards = state.downloads.groups
        .filter((g) => types.includes(g.type) && (!words.length || [g.title, g.subtitle].join(' ').toLowerCase().includes(words.join(' '))))
        .map(groupCard);
    }
  } else if (kind === 'pairs') {
    cards = offlinePairs(matches).map(pairCard);
  } else {
    const items = kept(kind);
    state.items = items;
    cards = items.map(renderItem);
  }
  $('results').replaceChildren(...cards);
  $('status').textContent = cards.length
    ? ''
    : (words.length ? 'Nothing downloaded matches.' : 'Nothing here is downloaded.');
}

function offlinePairs(matches) {
  return state.downloads.groups.filter((g) => g.type === 'pair' && g.pair)
    .map((g) => ({ ...g.pair, sync: g.readalong ? { state: 'ready' } : g.pair.sync }))
    .filter((p) => matches(p.ebook));
}

// groupCard is a downloaded album, playlist or the like, drawn as an album
// is: its cover (from the device), its name, and what it is.
function groupCard(group) {
  const songs = group.keys.map((k) => state.downloads.items[k]).filter(Boolean);
  const card = document.createElement('button');
  card.type = 'button';
  card.className = 'item album-card';
  const withArt = songs.find((s) => s.artId);
  const cover = coverArt('', group.title);
  if (withArt) {
    offlineArtURL(withArt).then((u) => {
      if (!u) return;
      const img = document.createElement('img');
      img.src = u;
      img.alt = '';
      // In place of the stand-in picture; the badge stays.
      for (const child of [...cover.children]) if (!child.classList.contains('dl-badge')) child.remove();
      cover.prepend(img);
    });
  }
  cover.append(downloadedBadge());
  const meta = document.createElement('div');
  meta.className = 'meta';
  const title = document.createElement('span');
  title.className = 'title';
  title.textContent = group.title;
  const sub = document.createElement('span');
  sub.className = 'sub';
  sub.textContent = [group.subtitle, `${songs.length} song${songs.length === 1 ? '' : 's'}`].filter(Boolean).join(' \u00b7 ');
  meta.append(title, sub);
  card.append(cover, meta);
  card.addEventListener('click', () => showOfflineGroup(group, songs));
  return card;
}

// showOfflineGroup is a downloaded album or playlist's page, as the online
// album page lays it out.
function showOfflineGroup(group, songs) {
  const view = $('music-view');
  enterDetailPage();
  show($('results'), false);
  show($('results-bar'), false);
  show($('music-view'), true);
  const head = document.createElement('div');
  head.className = 'album-head';
  const text = document.createElement('div');
  text.className = 'album-text';
  const title = document.createElement('h2');
  title.textContent = group.title;
  const facts = document.createElement('span');
  facts.className = 'muted';
  facts.textContent = [group.subtitle, `${songs.length} song${songs.length === 1 ? '' : 's'}`].filter(Boolean).join(' \u00b7 ');
  text.append(title, facts, playButtons(async () => songs));
  head.append(text);
  const list = document.createElement('ol');
  list.className = 'track-rows';
  songs.forEach((song, i) => list.append(trackRow(song, i, songs)));
  view.replaceChildren(backButton(state.musicView === 'albums' ? 'Albums' : 'Playlists', () => runSearch()), head, list);
  window.scrollTo(0, 0);
}

/* --------------------------------------------------- downloaded, marked */

// A small badge on the cover of everything on this device, and beside a
// downloaded song in an album's list, kept current as downloads come and go.
function downloadedBadge() {
  const badge = document.createElement('span');
  badge.className = 'dl-badge';
  badge.title = 'On this device';
  badge.setAttribute('aria-label', 'Downloaded');
  badge.append(icon('check'));
  return badge;
}

function markDownloads() {
  for (const el of document.querySelectorAll('[data-key]')) {
    const on = Boolean(state.downloads.items[el.dataset.key]);
    const spot = el.classList.contains('track-row') ? el.querySelector('.track-title') : el.querySelector('.art-wrap');
    if (!spot) continue;
    const badge = spot.querySelector('.dl-badge');
    if (on && !badge) spot.append(downloadedBadge());
    else if (!on && badge) badge.remove();
    // The gray arrow beside a song in a list: gone once it is here, back if
    // it is removed. Covers carry no buttons; their menu downloads.
    if (!el.classList.contains('track-row')) continue;
    const box = el.parentElement;
    if (!box) continue;
    const get = box.querySelector(':scope > .dl-get');
    if (on && get && !get.classList.contains('working')) get.remove();
    else if (!on && !get && el.soundstormItem) {
      const again = getButton(el.soundstormItem, el.classList.contains('track-row'));
      if (again) box.append(again);
    }
  }
  refreshDownloadsCard();
}

// Settings, Downloads: what is on this device, how much room it takes, and
// a way to remove any of it - the list Music's Downloads pill used to hold.
async function refreshDownloadsCard() {
  const block = $('downloads-block');
  if (!block || !('caches' in window)) return;
  show(block, true);
  if ($('account').classList.contains('hidden')) return;
  $('downloads-manage').replaceChildren(await downloadsView(false));
  renderDownloadShelves();
  show($('downloads-clear'), hasDownloads());
}

$('downloads-clear').addEventListener('click', async () => {
  if (!window.confirm('Remove everything downloaded to this device? It all stays in your library.')) return;
  await clearDownloads();
  keepShell();
  markMusicTabs();
  markDownloads();
});

/* ------------------------------------------------------ tap to download */

// A gray arrow where the green tick goes, on anything that can be downloaded
// and is not: tap it and it downloads, the arrow pulsing until the tick takes
// its place. Not offline, and not where the browser keeps no downloads at
// all (plain http). A card is itself a button, so the arrow sits beside it,
// in the holder, over the corner of the cover - as the "..." does.
function downloadsPossible() {
  return 'caches' in window && !state.offline;
}

function makeGet(label, inRow, run) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = inRow ? 'dl-get dl-get-row' : 'dl-get';
  button.setAttribute('aria-label', `Download ${label}`);
  button.title = 'Download to this device';
  button.append(icon('download'));
  button.addEventListener('click', async (event) => {
    event.stopPropagation();
    event.preventDefault();
    if (button.classList.contains('working')) return;
    button.classList.add('working');
    try {
      await run((p) => { button.title = `Downloading\u2026 ${Math.floor((p || 0) * 100)}%`; });
      button.remove();
    } catch {
      button.classList.remove('working');
      button.title = 'Could not download. Tap to try again.';
    }
    markDownloads();
  });
  return button;
}

function getButton(item, inRow) {
  if (!downloadsPossible() || isDownloaded(item)) return null;
  if (item.kind === 'music') {
    return makeGet(item.title, inRow, () => download({
      id: `song:${selectionKey(item)}`, type: 'song', title: item.title,
      subtitle: (item.creators || []).join(', '), sourceId: item.sourceId, artId: item.artId,
    }, [item]));
  }
  if (!canDownload(item)) return null;
  return makeGet(item.title, inRow, async (progress) => {
    const big = isVideoItem(item);
    if (big && !window.confirm(`Download "${item.title}"? A film or episode is usually one to a few GB, `
      + 'and one that needs converting takes a while.')) throw new Error('declined');
    await downloadBook(item, progress);
  });
}

/* -------------------------------------------- delete, from the hold menu */

// Deleting is the owner's, from an item's hold (or right-click) menu, one
// item at a time: the menu asks the server exactly what would go and says
// so, the item goes to the bin for thirty days, and an Undo follows in the
// message at the bottom. It used to be a Select mode with a bar.
async function renderDeleteMenu(item) {
  const menu = $('item-menu');
  const note = menuNote();
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const backLabel = document.createElement('span');
  backLabel.textContent = 'Delete from library';
  back.append(backLabel);
  back.addEventListener('click', (event) => {
    event.stopPropagation();
    renderMainMenu(item, state.menuOpts);
  });
  const text = document.createElement('p');
  text.className = 'menu-confirm';
  text.textContent = 'Working out what that would delete\u2026';
  menu.replaceChildren(back, text, note);

  const payload = JSON.stringify({ items: [{ source: item.sourceId, id: item.id, title: item.title }] });
  const { ok, body } = await api('/api/delete/preview', { method: 'POST', body: payload });
  if (state.menuFor !== item) return;
  if (!ok || !body) {
    text.textContent = (body && body.error) || 'Could not work out what that would delete.';
    return;
  }
  text.textContent = `Delete "${item.title}"? ${body.files} file${body.files === 1 ? '' : 's'}, `
    + `${formatBytes(body.bytes)}. It stays in the bin for 30 days.`;
  const confirm = menuItem('trash', 'Delete', async () => {
    confirm.disabled = true;
    const result = await api('/api/delete', { method: 'POST', body: payload });
    if (!result.ok || !result.body) {
      text.textContent = (result.body && result.body.error) || 'Could not delete.';
      confirm.disabled = false;
      return;
    }
    closeItemMenu();
    const key = selectionKey(item);
    for (const el of document.querySelectorAll(`[data-key="${CSS.escape(key)}"]`)) {
      (el.closest('.item-holder') || el.closest('li') || el).remove();
    }
    state.items = (state.items || []).filter((it) => selectionKey(it) !== key);
    // A copy on this device of something no longer in the library goes too.
    if (isDownloaded(item)) removeItemDownload(item);
    const entry = result.body.entry;
    showToast(`Deleted "${item.title}".`, 'Undo', async () => {
      const undone = await api('/api/delete/undo', { method: 'POST', body: JSON.stringify({ entry }) });
      if (!undone.ok) {
        showToast((undone.body && undone.body.error) || 'Could not undo.');
        return;
      }
      showToast(`"${item.title}" is back.`);
      // Back on disk; give the shelf a moment to notice before listing it.
      setTimeout(runSearch, 3000);
    }, 12000);
  }, { className: 'menu-danger' });
  const cancel = menuItem('close', 'Cancel', () => closeItemMenu());
  menu.replaceChildren(back, text, confirm, cancel, note);
}

/* ----------------------------------- remove download, however it arrived */

// Removing one item's download takes it off the device whichever download
// brought it - on its own, as part of an album or playlist, or as half of a
// Read Along book (which goes whole: half of one cannot be read along).
async function removeItemDownload(item) {
  const key = selectionKey(item);
  for (const group of [...state.downloads.groups]) {
    if (!group.keys.includes(key)) continue;
    if (group.keys.length === 1 || group.type === 'pair') await removeDownload(group.id);
    else group.keys = group.keys.filter((k) => k !== key);
  }
  if (state.downloads.groups.some((g) => g.keys.includes(key))) return;
  const kept = state.downloads.items[key];
  if (kept) {
    const cache = await caches.open(OFFLINE_CACHE);
    const video = kept.dl && kept.dl.video;
    if (video && video.mode === 'hls') {
      const playlist = await cache.match(video.variant);
      if (playlist) for (const url of hlsParts(await playlist.text(), video.variant)) await cache.delete(url);
    }
    for (const url of (kept.dl && kept.dl.files) || [streamPath(kept)]) await cache.delete(url);
    delete state.downloads.items[key];
  }
  state.downloads.groups = state.downloads.groups.filter((g) => g.keys.length);
  saveDownloadIndex();
  markMusicTabs();
  markDownloads();
}

/* ------------------------------------------- Read Along: not the same */

// A Read Along card's hold menu. Title and author can match two different
// books, so the owner can say they are not the same: the pair leaves the
// shelf for everyone and is never synced (anything Storyteller made of it is
// deleted), with an Undo.
function renderPairMenu(pair, menu, note) {
  const head = menuHeader({ ...pair.ebook, kind: 'ebook' });
  const entries = [head];
  const id = pairDownloadID(pair);
  if (state.downloads.groups.some((g) => g.id === id)) {
    entries.push(menuItem('download', 'Remove download', async () => {
      closeItemMenu();
      await removeDownload(id);
      if (state.kind === 'pairs') runSearch();
    }));
  }
  if (state.me && state.me.owner && !state.offline) {
    entries.push(menuItem('close', 'Not the same book', async (event) => {
      event.stopPropagation();
      closeItemMenu();
      const mark = (wrong) => api('/api/books/pairs/not-same', {
        method: 'POST',
        body: JSON.stringify({
          ebook: { sourceId: pair.ebook.sourceId, id: pair.ebook.id },
          audiobook: { sourceId: pair.audiobook.sourceId, id: pair.audiobook.id },
          wrong,
        }),
      });
      const { ok, body } = await mark(true);
      if (!ok) {
        showToast((body && body.error) || 'Could not change that.');
        return;
      }
      if (state.kind === 'pairs') runSearch();
      else refreshPairs();
      showToast(`"${pair.ebook.title}" is no longer matched with its audiobook.`, 'Undo', async () => {
        await mark(false);
        if (state.kind === 'pairs') runSearch();
        else refreshPairs();
      }, 10000);
    }, { className: 'menu-danger' }));
  }
  if (entries.length === 1) {
    const none = document.createElement('p');
    none.className = 'menu-confirm';
    none.textContent = 'Tap the cover to read along.';
    entries.push(none);
  }
  menu.replaceChildren(...entries, note);
}

/* ------------------------------------------------------- pair by hand */

// Pair with its audiobook (or ebook): when the matching missed - a subtitle,
// an author spelled two ways - the owner picks the other half from a search
// that starts at the book's own title. The pair joins Read Along for
// everyone, and syncs by itself.
function renderPairPicker(item) {
  const menu = $('item-menu');
  const note = menuNote();
  const other = item.kind === 'ebook' ? 'audiobook' : 'ebook';
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const label = document.createElement('span');
  label.textContent = `Pair with its ${other}`;
  back.append(label);
  back.addEventListener('click', (event) => {
    event.stopPropagation();
    renderMainMenu(item, state.menuOpts);
  });
  const search = document.createElement('input');
  search.type = 'search';
  search.className = 'menu-search';
  search.placeholder = `Search ${other}s\u2026`;
  // The title without its edition noise: "[B017V4IM1G]", "(Unabridged)".
  search.value = item.title.replace(/\s*[\[(][^\])]*[\])]/g, '').split(':')[0].trim();
  const list = document.createElement('div');
  list.className = 'menu-results';
  menu.replaceChildren(back, search, list, note);
  if (state.menuAnchor) placeMenu(menu, state.menuAnchor);

  let seq = 0;
  const find = async () => {
    const mine = ++seq;
    const q = search.value.trim();
    const params = new URLSearchParams({ q, kind: other, limit: '12' });
    const { ok, body } = await api(`/api/search?${params}`);
    if (mine !== seq || state.menuFor !== item) return;
    const found = (ok && body && body.items) || [];
    list.replaceChildren(...(found.length ? found.map((candidate) => {
      const choice = menuItem(other === 'audiobook' ? 'headphones' : 'book', candidate.title, async (event) => {
        event.stopPropagation();
        const [ebook, audiobook] = item.kind === 'ebook' ? [item, candidate] : [candidate, item];
        const pairIt = (paired) => api('/api/books/pairs/by-hand', {
          method: 'POST',
          body: JSON.stringify({
            ebook: { sourceId: ebook.sourceId, id: ebook.id },
            audiobook: { sourceId: audiobook.sourceId, id: audiobook.id },
            paired,
          }),
        });
        const result = await pairIt(true);
        if (!result.ok) {
          note.textContent = (result.body && result.body.error) || 'Could not pair them.';
          show(note, true);
          return;
        }
        closeItemMenu();
        refreshPairs();
        showToast(`Paired "${ebook.title}" with its audiobook. It's in Books, Read Along.`, 'Undo', async () => {
          await pairIt(false);
          refreshPairs();
          if (state.kind === 'pairs') runSearch();
        }, 10000);
      }, { detail: (candidate.creators || []).join(', ') });
      return choice;
    }) : [(() => {
      const none = document.createElement('p');
      none.className = 'menu-confirm';
      none.textContent = q ? `No ${other} matches that.` : `Type to find the ${other}.`;
      return none;
    })()]));
  };
  let timer = 0;
  search.addEventListener('input', () => {
    clearTimeout(timer);
    timer = setTimeout(find, 250);
  });
  search.addEventListener('click', (event) => event.stopPropagation());
  find();
}

/* -------------------------------------------------------- settings pills */

// Settings has pills like Music and Books - its cards grouped by what they
// are about - and while it is open the search box searches settings: every
// card whose text (or a few extra words it carries, like "offline" on
// Downloads) holds every word typed, whichever pill it is under. The
// library's own search is put back on leaving.
const SETTINGS_CATS = [
  { kind: 'library', label: 'Library' },
  { kind: 'playback', label: 'Playback' },
  { kind: 'devices', label: 'Devices' },
  { kind: 'people', label: 'People' },
  { kind: 'account', label: 'Account' },
];

const settingsCards = () => [...document.querySelectorAll('#account .account-section[data-cat]')];
const settingsQuery = () => (state.settingsSearching ? $('search-input').value.trim().toLowerCase() : '');

// The pills with at least one card this account can see, in the order the
// person has put them (held and slid, like any row of pills).
function settingsCategories() {
  const cards = settingsCards();
  const order = pillOrder('settings');
  const rank = (kind) => { const i = order.indexOf(kind); return i < 0 ? 1e6 : i; };
  return SETTINGS_CATS
    .filter((c) => cards.some((card) => card.dataset.cat === c.kind && !card.classList.contains('hidden')))
    .map((c, i) => ({ c, i }))
    .sort((a, b) => (rank(a.c.kind) - rank(b.c.kind)) || (a.i - b.i))
    .map(({ c }) => c);
}

function selectSettingsCat(kind) {
  state.settingsCat = kind;
  $('search-input').value = '';
  applySettingsView();
  window.scrollTo(0, 0);
}

function applySettingsView() {
  const cats = settingsCategories();
  if (!cats.some((c) => c.kind === state.settingsCat)) state.settingsCat = cats.length ? cats[0].kind : '';
  const terms = settingsQuery().split(/\s+/).filter(Boolean);
  let matches = 0;
  for (const card of settingsCards()) {
    const text = `${card.textContent} ${card.dataset.words || ''}`.toLowerCase();
    const off = terms.length ? !terms.every((t) => text.includes(t)) : card.dataset.cat !== state.settingsCat;
    card.classList.toggle('off-cat', off);
    if (!off && !card.classList.contains('hidden')) matches++;
  }
  show($('settings-none'), terms.length > 0 && matches === 0);
  // Searching looks through every pill, so none is lit.
  for (const b of $('subtabs').querySelectorAll('button')) {
    const on = !terms.length && b.dataset.kind === state.settingsCat;
    b.classList.toggle('active', on);
    b.setAttribute('aria-selected', String(on));
    if (on && !state.pillSwiping) centerPill($('subtabs'), b, 'smooth');
  }
}

function enterSettingsSearch() {
  if (state.settingsSearching) return;
  state.settingsSearching = true;
  const input = $('search-input');
  state.librarySearch = input.value;
  state.libraryPlaceholder = input.placeholder;
  input.value = '';
  input.placeholder = 'Search settings\u2026';
}

function leaveSettingsSearch() {
  if (!state.settingsSearching) return;
  state.settingsSearching = false;
  const input = $('search-input');
  input.value = state.librarySearch || '';
  if (state.libraryPlaceholder) input.placeholder = state.libraryPlaceholder;
}

// Cards appear as the server answers (the owner's, remote access, devices),
// so the pills follow them - redrawn only when which pills exist changes.
(function followSettingsCards() {
  let last = '';
  const check = () => {
    if (state.tab !== 'settings') return;
    const now = settingsCategories().map((c) => c.kind).join();
    if (now !== last) { last = now; renderTabs(); }
  };
  const observer = new MutationObserver(check);
  for (const card of settingsCards()) observer.observe(card, { attributes: true, attributeFilter: ['class'] });
})();

/* ------------------------------------------------------------ item info */

// Info, from the hold menu: what the library knows about an item - the
// length that used to sit on every cover, and everything else its backend
// said - on a page of the menu with a way back.
const INFO_KIND = {
  music: 'Song', audiobook: 'Audiobook', ebook: 'Ebook', document: 'Document',
  video: 'Film', tv: 'TV', picture: 'Photo',
};
const INFO_BY = { music: 'Artist', audiobook: 'Author', ebook: 'Author', video: 'Director', tv: 'Director' };

function infoRows(item) {
  const x = item.extra || {};
  const rows = [['Type', INFO_KIND[item.kind] || item.kind]];
  const add = (label, value) => { if (value) rows.push([label, String(value)]); };
  if (item.creators && item.creators.length) add(INFO_BY[item.kind] || 'By', item.creators.join(', '));
  add('Narrator', x.narrator);
  if (item.kind === 'music') add('Album', x.album || item.subtitle);
  else if (item.kind === 'tv') add('Series', item.subtitle);
  add('Series', x.series && (x.seriesIndex ? `${x.series}, book ${x.seriesIndex}` : x.series));
  add('Episode', x.episode);
  add('Year', item.year);
  add('Length', formatDuration(item.durationSeconds));
  add('Genre', x.genre);
  add('Tags', x.tags);
  add('Rating', x.rating);
  add('Language', x.language);
  add('Format', x.format && x.format.toUpperCase());
  add('Taken', x.taken && new Date(x.taken).toLocaleString());
  add('Place', x.place);
  if (x.width && x.height) add('Size', `${x.width} \u00d7 ${x.height}`);
  add('ISBN', x.isbn);
  if (isDownloaded(item)) add('On this device', 'Downloaded');
  return rows;
}

function renderInfoMenu(item) {
  const menu = $('item-menu');
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const label = document.createElement('span');
  label.textContent = 'Info';
  back.append(label);
  back.addEventListener('click', (event) => {
    event.stopPropagation();
    renderMainMenu(item, state.menuOpts);
  });
  const title = document.createElement('p');
  title.className = 'menu-info-title';
  title.textContent = item.title;
  const list = document.createElement('dl');
  list.className = 'menu-info';
  for (const [k, v] of infoRows(item)) {
    const dt = document.createElement('dt');
    dt.textContent = k;
    const dd = document.createElement('dd');
    dd.textContent = v;
    list.append(dt, dd);
  }
  const parts = [back, title, list];
  const overview = item.extra && item.extra.overview;
  if (overview) {
    const p = document.createElement('p');
    p.className = 'menu-info-about';
    p.textContent = overview;
    parts.push(p);
  }
  menu.replaceChildren(...parts);
  // Taller than the menu it replaces: placed again, so none of it is off screen.
  if (state.menuAnchor) placeMenu(menu, state.menuAnchor);
}

/* ------------------------------------------------------ drag to select */

// Hold an item, and without lifting move the finger on: the menu gives way
// to selecting, and every item from the one held to the one under the finger
// is selected - back up and they come off again, as in a phone's photos.
// Near the bottom (or top) of the screen the page scrolls by itself, faster
// the closer the finger, so any number can be selected in one drag. Lifting
// leaves the bar to act on them: favorite, download, and for the owner
// delete. Only in a shelf's list; not in the strips on Home.
state.dragSelect = null;

function armDragSelect(card, item, pointerId, x0, y0) {
  if (!card.closest('#results')) return;
  const onMove = (event) => {
    if (event.pointerId !== pointerId) return;
    if (!state.dragSelect) {
      if (Math.hypot(event.clientX - x0, event.clientY - y0) < HOLD_SLOP) return;
      // A finger that has gone into the menu is choosing from it for the
      // rest of this touch, wherever it goes after.
      if ((menuFinger.id === pointerId && menuFinger.entered) || fingerOverMenu(event.clientX, event.clientY)) return;
      // And selecting starts on reaching another card, not on leaving this
      // one's middle: the menu opens over the held card's lower half, so the
      // way to it is across the card itself.
      // Looked for through the dimmed page the menu lays over the others.
      const holder = document.elementsFromPoint(event.clientX, event.clientY)
        .map((el) => el.closest('.item-holder')).find(Boolean);
      if (!holder || holder.contains(card)) return;
      startDragSelect(card, item);
    }
    state.dragSelect.x = event.clientX;
    state.dragSelect.y = event.clientY;
    dragSelectTo(event.clientX, event.clientY);
  };
  const onEnd = (event) => {
    if (event.pointerId !== pointerId) return;
    document.removeEventListener('pointermove', onMove);
    document.removeEventListener('pointerup', onEnd);
    document.removeEventListener('pointercancel', onEnd);
    if (state.dragSelect) {
      cancelAnimationFrame(state.dragSelect.frame);
      state.dragSelect = null;
      document.body.classList.remove('drag-selecting');
    }
  };
  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup', onEnd);
  document.addEventListener('pointercancel', onEnd);
}

function startDragSelect(card, item) {
  closeItemMenu();
  state.suppressClick = true; // the lift at the end is not a tap
  if (!state.selecting) setSelecting(true);
  // The bar waits for the lift: over the bottom of the list it would hide
  // the cards the finger is heading for.
  document.body.classList.add('drag-selecting');
  const key = selectionKey(item);
  state.dragSelect = {
    from: key,
    before: new Set(state.selected.keys()), // what was selected already stays
    x: 0, y: 0, frame: 0,
  };
  state.selected.set(key, item);
  card.classList.add('selected');
  resetSelectBar();
  if (navigator.vibrate) navigator.vibrate(8);
  const tick = () => {
    const d = state.dragSelect;
    if (!d) return;
    // The bottom 120px of the screen (tab bar included) scroll down, the
    // band under the header and pills scrolls up; faster nearer the edge.
    const bottomEdge = window.innerHeight - 120;
    const topEdge = listBounds().top + 80;
    let speed = 0;
    if (d.y > bottomEdge) speed = Math.min(1, (d.y - bottomEdge) / 120) * 26;
    else if (d.y && d.y < topEdge) speed = -Math.min(1, (topEdge - d.y) / 80) * 26;
    if (speed) {
      window.scrollBy(0, speed);
      dragSelectTo(d.x, d.y);
    }
    d.frame = requestAnimationFrame(tick);
  };
  state.dragSelect.frame = requestAnimationFrame(tick);
}

// Everything between the held card and the one under the finger, in the
// order they are shown.
// Where the list shows: under the header and its pills, above the tab bar.
function listBounds() {
  let top = 0;
  for (const el of [document.querySelector('#app > header'), $('music-tabs'), $('subtabs')]) {
    if (el && !el.classList.contains('hidden') && el.getClientRects().length) top = Math.max(top, el.getBoundingClientRect().bottom);
  }
  const tabs = $('tabs');
  const bottom = tabs && !tabs.classList.contains('hidden') && tabs.getClientRects().length
    ? Math.min(window.innerHeight, tabs.getBoundingClientRect().top) : window.innerHeight;
  return { top, bottom };
}

function dragSelectTo(x, y0) {
  const d = state.dragSelect;
  if (!d) return;
  // A finger over the pills or the tab bar - where it goes to scroll - means
  // the item at that edge of the list, so the selection follows the scroll.
  const { top, bottom } = listBounds();
  const y = Math.min(Math.max(y0, top + 8), bottom - 8);
  // Between two rows is no card: the nearest one above or below it counts.
  let target = null;
  for (const dy of [0, 16, -16, 32, -32]) {
    const yy = Math.min(Math.max(y + dy, top + 1), bottom - 1);
    const under = document.elementFromPoint(x, yy);
    const holder = under && under.closest('#results .item-holder');
    target = holder && holder.querySelector('.item');
    if (target) break;
  }
  if (!target) return;
  const cards = [...$('results').querySelectorAll('.item')];
  const a = cards.findIndex((c) => c.dataset.key === d.from);
  const b = cards.indexOf(target);
  if (a < 0 || b < 0) return;
  const [lo, hi] = a < b ? [a, b] : [b, a];
  const byKey = new Map((state.items || []).map((it) => [selectionKey(it), it]));
  cards.forEach((c, i) => {
    const key = c.dataset.key;
    const on = (i >= lo && i <= hi) || d.before.has(key);
    const it = byKey.get(key);
    if (on && it) state.selected.set(key, it);
    else if (!on) state.selected.delete(key);
    c.classList.toggle('selected', state.selected.has(key));
  });
  resetSelectBar();
}

// From the moment a hold registers until the finger lifts, the finger is
// the hold's: no scrolling by hand, no pull-to-refresh, no sideways swipe to
// the next pill - only the menu, or selecting and the scrolling it does itself.
function holdStarted() {
  state.holding = true;
  document.documentElement.classList.add('holding');
  const end = () => {
    state.holding = false;
    document.documentElement.classList.remove('holding');
    document.removeEventListener('touchend', end);
    document.removeEventListener('touchcancel', end);
    document.removeEventListener('pointerup', end);
  };
  document.addEventListener('touchend', end);
  document.addEventListener('touchcancel', end);
  document.addEventListener('pointerup', end);
}

document.addEventListener('touchmove', (event) => {
  if ((state.dragSelect || state.holding) && event.cancelable) event.preventDefault();
}, { passive: false });

/* ------------------------------------------------ authors and series */

// Books by author and by series, across the ebook and audiobook shelves: an
// author's page has their series, each in order, and then their other books;
// a series' page has its books in order. The server groups them (sort-form
// and reading-form names alike); typing narrows the list of authors or series.

function groupCover(group) {
  return group.cover ? artUrl(group.cover.sourceId, group.cover.artId) : '';
}

// A card for an author or a series: round for a person, square for books.
function bookGroupCard(group, round, subtitle, open) {
  const holder = document.createElement('div');
  holder.className = 'item-holder';
  const card = document.createElement('button');
  card.type = 'button';
  card.className = round ? 'item artist-card' : 'item';
  const meta = document.createElement('div');
  meta.className = 'meta';
  const name = document.createElement('span');
  name.className = 'title';
  name.textContent = group.name;
  const sub = document.createElement('span');
  sub.className = 'sub';
  sub.textContent = subtitle;
  meta.append(name, sub);
  card.append(coverArt(groupCover(group), group.name, round), meta);
  card.addEventListener('click', open);
  holder.append(card);
  return holder;
}

const bookCount = (n) => `${n} book${n === 1 ? '' : 's'}`;

async function showBookBrowse(seq) {
  const view = $('music-view');
  const authors = state.kind === 'authors';
  $('status').textContent = '';
  if (!view.children.length) showSkeleton(view, 'grid', authors);
  const params = new URLSearchParams({ q: state.query });
  const { ok, body } = await api(`/api/books/${authors ? 'authors' : 'series'}?${params}`);
  if (seq !== state.searchSeq) return;
  const list = (ok && body && (authors ? body.authors : body.series)) || [];
  const grid = document.createElement('div');
  grid.className = authors ? 'grid browse-grid artist-grid' : 'grid browse-grid';
  grid.append(...list.map((g) => (authors
    ? bookGroupCard(g, true, bookCount(g.count), () => showAuthor(g.key))
    : bookGroupCard(g, false, [(g.authors || []).join(', '), bookCount(g.count)].filter(Boolean).join(' \u00b7 '),
      () => showSeries(g.key)))));
  view.replaceChildren(grid);
  $('status').textContent = list.length ? ''
    : (state.query ? `No ${authors ? 'author' : 'series'} matches.`
      : (authors ? 'No authors yet.' : 'No series yet. Books show here when their details name a series.'));
}

function bookSection(title, children) {
  const section = document.createElement('section');
  section.className = 'book-section';
  const h = document.createElement('h2');
  h.textContent = title;
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid';
  grid.append(...children);
  section.append(h, grid);
  return section;
}

function pageHead(title, subtitle) {
  const head = document.createElement('div');
  head.className = 'book-page-head';
  const h = document.createElement('h1');
  h.textContent = title;
  const sub = document.createElement('p');
  sub.className = 'muted';
  sub.textContent = subtitle;
  head.append(h, sub);
  return head;
}

async function showAuthor(key) {
  const seq = ++state.searchSeq;
  const view = $('music-view');
  startLoading(view);
  const { ok, body } = await api(`/api/books/authors?${new URLSearchParams({ key })}`);
  if (seq !== state.searchSeq) return;
  $('status').textContent = ok && body ? '' : 'Could not load that author.';
  if (!ok || !body) {
    view.replaceChildren(backButton('Authors', () => runSearch()));
    return;
  }
  const parts = [backButton('Authors', () => runSearch()), pageHead(body.name, bookCount(body.count))];
  const series = body.series || [];
  if (series.length) {
    parts.push(bookSection('Series', series.map((g) => bookGroupCard(g, false, bookCount(g.count),
      () => showSeries(g.key, body.name)))));
  }
  const books = body.books || [];
  if (books.length) {
    state.items = books;
    parts.push(bookSection(series.length ? 'Other books' : 'Books', books.map(renderItem)));
  }
  view.replaceChildren(...parts);
  window.scrollTo(0, 0);
}

async function showSeries(key, fromAuthor) {
  const seq = ++state.searchSeq;
  const view = $('music-view');
  startLoading(view);
  const { ok, body } = await api(`/api/books/series?${new URLSearchParams({ key })}`);
  if (seq !== state.searchSeq) return;
  $('status').textContent = ok && body ? '' : 'Could not load that series.';
  if (!ok || !body) {
    view.replaceChildren(backButton('Series', () => runSearch()));
    return;
  }
  const books = body.books || [];
  state.items = books;
  // Back to wherever it was opened from: an author's page, or the list.
  const back = fromAuthor && state.kind === 'authors'
    ? backButton(fromAuthor, () => showAuthor(nameKeyOf(fromAuthor)))
    : backButton(state.kind === 'authors' ? 'Authors' : 'Series', () => runSearch());
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid';
  grid.append(...books.map(renderItem));
  view.replaceChildren(back, pageHead(body.name, [(body.authors || []).join(', '), bookCount(body.count)]
    .filter(Boolean).join(' \u00b7 ')), grid);
  window.scrollTo(0, 0);
}

// The server's key for a name: lower-case letters and digits of its reading form.
function nameKeyOf(name) {
  return name.toLowerCase().replace(/[^\p{L}\p{N}]/gu, '');
}

// startLoading swaps a view about to show another page for ghost content
// of a page, so the page being left is not mistaken for the one on its way.
// The next list page is then a fresh one, whatever runSearch last drew.
function startLoading(view) {
  enterDetailPage();
  showSkeleton(view, 'page');
  $('status').textContent = '';
  state.shownPage = null;
}

// enterDetailPage: an album, artist, playlist, author or series page, opened
// from a list - or from Home, where the Continue row was showing and used to
// stay above the album. Such a page is only itself; the lists' extras go.
function enterDetailPage() {
  state.detailPage = true;
  show($('continue'), false);
  show($('home-view'), false);
  closeItemMenu();
  if (state.selecting) setSelecting(false);
}

// Ghost content: gray shapes where covers and titles will be, with a slow
// shimmer across them, so a page on its way looks like it is coming rather
// than stuck. Shaped like what arrives - a grid of covers, Home's rows, or a
// page with its heading - and replaced wholesale by the real thing.
function skeletonCards(n, round) {
  return Array.from({ length: n }, () => {
    const card = document.createElement('div');
    card.className = 'item-holder skeleton';
    card.setAttribute('aria-hidden', 'true');
    const cover = document.createElement('div');
    cover.className = round ? 'sk-cover round' : 'sk-cover';
    const line = document.createElement('div');
    line.className = 'sk-line';
    const short = document.createElement('div');
    short.className = 'sk-line short';
    card.append(cover, line, short);
    return card;
  });
}

function showSkeleton(view, shape, round) {
  const grid = () => {
    const g = document.createElement('div');
    g.className = 'grid browse-grid';
    g.append(...skeletonCards(6, round));
    return g;
  };
  if (shape === 'home') {
    view.replaceChildren(...[0, 1, 2].map(() => {
      const row = document.createElement('div');
      row.className = 'sk-row skeleton';
      row.setAttribute('aria-hidden', 'true');
      const heading = document.createElement('div');
      heading.className = 'sk-line sk-heading';
      const strip = document.createElement('div');
      strip.className = 'sk-strip';
      strip.append(...skeletonCards(3));
      row.append(heading, strip);
      return row;
    }));
  } else if (shape === 'page') {
    const head = document.createElement('div');
    head.className = 'sk-page-head skeleton';
    head.setAttribute('aria-hidden', 'true');
    const title = document.createElement('div');
    title.className = 'sk-line sk-title';
    const sub = document.createElement('div');
    sub.className = 'sk-line short';
    head.append(title, sub);
    view.replaceChildren(head, grid());
  } else if (view.classList.contains('grid')) {
    view.replaceChildren(...skeletonCards(8, round));
  } else {
    view.replaceChildren(grid());
  }
}

/* ------------------------------------------------- people and places */

// Photos by who is in them and where they were taken. The photo server does
// the recognizing; these pages show what it found, in SoundStorm's own
// style. Somebody it found but nobody has named yet can be named here, for
// the whole household.
async function showPhotoBrowse(seq) {
  const view = $('music-view');
  const people = state.kind === 'people';
  $('status').textContent = '';
  if (!view.children.length) showSkeleton(view, 'grid', people);
  const { ok, body } = await api(`/api/photos/${people ? 'people' : 'places'}`);
  if (seq !== state.searchSeq) return;
  const words = state.query.toLowerCase().split(/\s+/).filter(Boolean);
  const list = ((ok && body && (people ? body.people : body.places)) || [])
    .filter((g) => words.every((w) => `${g.name} ${g.subtitle || ''}`.toLowerCase().includes(w)));
  const grid = document.createElement('div');
  grid.className = people ? 'grid browse-grid artist-grid' : 'grid browse-grid';
  grid.append(...list.map((g) => bookGroupCard(
    { name: g.name || (people ? 'Add a name' : ''), cover: { sourceId: g.sourceId, artId: g.artId } },
    people, g.subtitle || '', () => showPhotoGroup(g),
  )));
  if (people) {
    for (const [i, g] of list.entries()) {
      if (!g.name) grid.children[i].querySelector('.title').classList.add('unnamed');
    }
  }
  view.replaceChildren(grid);
  $('status').textContent = list.length ? ''
    : (state.query ? `No ${people ? 'one' : 'place'} matches.`
      : (people ? 'Nobody found in your photos yet. People appear once the photos have been looked through.'
        : 'No places yet. Photos show here when they know where they were taken.'));
}

async function showPhotoGroup(group) {
  const seq = ++state.searchSeq;
  const view = $('music-view');
  const people = state.kind === 'people';
  startLoading(view);
  const { ok, body } = await api(`/api/photos/${people ? 'people' : 'places'}?${new URLSearchParams({ id: group.id })}`);
  if (seq !== state.searchSeq) return;
  if (!ok || !body) {
    view.replaceChildren(backButton(people ? 'People' : 'Places', () => runSearch()));
    $('status').textContent = 'Could not load those photos.';
    return;
  }
  const items = body.items || [];
  state.items = items;
  $('status').textContent = '';
  const head = pageHead(group.name || (people ? 'Unnamed' : ''), [group.subtitle, `${items.length} photo${items.length === 1 ? '' : 's'}`]
    .filter(Boolean).join(' \u00b7 '));
  if (people) head.append(nameButton(group, head));
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid';
  grid.append(...items.map(renderItem));
  view.replaceChildren(backButton(people ? 'People' : 'Places', () => runSearch()), head, grid);
  window.scrollTo(0, 0);
}

// Name, or rename, somebody the photo server found: a box in place of the
// heading, Enter to keep, Escape to leave it.
function nameButton(group, head) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'ghost small name-person';
  button.textContent = group.name ? 'Rename' : 'Add a name';
  button.addEventListener('click', () => {
    const h = head.querySelector('h1');
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'name-person-input';
    input.maxLength = 100;
    input.value = group.name || '';
    input.placeholder = 'Their name';
    input.setAttribute('aria-label', 'Name');
    h.replaceWith(input);
    button.hidden = true;
    input.focus();
    let finished = false;
    const done = async (keep) => {
      // Once: Enter, then the box losing focus as it goes, must not both save.
      if (finished) return;
      finished = true;
      if (keep && input.value.trim() !== (group.name || '')) {
        const name = input.value.trim();
        const { ok, body } = await api(`/api/photos/people?${new URLSearchParams({ id: group.id })}`, {
          method: 'PUT', body: JSON.stringify({ name }),
        });
        if (!ok) showToast((body && body.error) || 'Could not change the name.');
        else group.name = name;
      }
      h.textContent = group.name || 'Unnamed';
      input.replaceWith(h);
      button.textContent = group.name ? 'Rename' : 'Add a name';
      button.hidden = false;
    };
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') done(true);
      if (event.key === 'Escape') done(false);
    });
    input.addEventListener('blur', () => { if (input.isConnected) done(true); });
  });
  return button;
}

/* ------------------------------------------------------------ going back */

// The phone's back gesture (and a browser's back button) steps back through
// the app before it leaves it: a menu, then Now Playing, a book, a film or a
// photo, then an album or other page back to its list, then a search, then
// any tab back to Home - and only from Home with nothing open does back
// leave. The app never changed the address, so back used to have nothing to
// go back to but the app itself.
//
// One history entry is kept armed while anything is open that back should
// close; back pops it, the topmost thing closes, and it is armed again if
// anything is still open. Whatever closes by other means (the x, a tap
// outside) takes the entry away again, so back never has to be pressed twice.
const backState = { armed: false, ignore: 0 };

function shown(id) {
  const el = $(id);
  return Boolean(el) && !el.classList.contains('hidden');
}

function backTarget() {
  if (shown('item-menu')) return () => closeItemMenu();
  if (state.selecting) return () => setSelecting(false);
  if (shown('photo-overlay')) return () => closePhoto();
  if (shown('reader-overlay')) return () => window.soundstormReader && window.soundstormReader.close();
  if (shown('video-overlay')) return () => closeVideo();
  if (shown('now-playing')) return () => closeNowPlaying();
  if (shown('recap-overlay')) return () => closeRecap();
  if (state.tab === 'settings') return () => selectTab('home');
  if (state.detailPage) return () => runSearch();
  if ($('search-input').value.trim()) return () => { $('search-input').value = ''; runSearch(); };
  if (state.tab && state.tab !== 'home') return () => selectTab('home');
  return null;
}

function syncBack() {
  const wanted = Boolean(backTarget());
  if (wanted && !backState.armed) {
    history.pushState({ soundstorm: 'back' }, '');
    backState.armed = true;
  } else if (!wanted && backState.armed) {
    backState.armed = false;
    backState.ignore += 1;
    history.back();
  }
}

window.addEventListener('popstate', async () => {
  if (backState.ignore > 0) {
    backState.ignore -= 1;
    return;
  }
  backState.armed = false;
  const close = backTarget();
  if (close) await close();
  syncBack();
});

// Screens opening and closing are seen as they happen - the overlays showing
// or hiding - so nothing that opens one has to remember to arm back.
(function watchForBack() {
  let queued = false;
  const later = () => {
    if (queued) return;
    queued = true;
    // After the event that caused it has been fully handled: a click's own
    // handler (opening an album, say) runs after a listener on the document.
    setTimeout(() => { queued = false; syncBack(); }, 0);
  };
  const observer = new MutationObserver(later);
  for (const id of ['item-menu', 'photo-overlay', 'reader-overlay', 'video-overlay', 'now-playing', 'recap-overlay', 'account', 'music-view', 'home-view']) {
    if ($(id)) observer.observe($(id), { attributes: true, attributeFilter: ['class'] });
  }
  observer.observe($('tabs'), { attributes: true, subtree: true, attributeFilter: ['class'] });
  $('search-input').addEventListener('input', later);
  document.addEventListener('click', later, true);
})();

/* ------------------------------------------------------------ television */

// A show's page: its episodes, season by season, with where this person is -
// a bar along an episode part-watched, a tick on one finished - and one
// button for what to watch now: carry on, or the next one, or the first.
async function showShow(series) {
  const seq = ++state.searchSeq;
  const view = $('music-view');
  show($('results'), false);
  show($('results-bar'), true);
  show(view, true);
  startLoading(view);
  const { ok, body } = await api(`/api/tv/show?${new URLSearchParams({ source: series.sourceId, id: series.id })}`);
  if (seq !== state.searchSeq) return;
  if (!ok || !body) {
    view.replaceChildren(backButton('TV', () => runSearch()));
    $('status').textContent = 'Could not load that show.';
    return;
  }
  const episodes = body.episodes || [];
  const progress = body.progress || {};
  state.showEpisodes = episodes;
  state.items = episodes;
  $('status').textContent = episodes.length ? '' : 'No episodes yet.';
  const seasons = [...new Set(episodes.map((e) => e.extra.season || '0'))];
  const next = nextToWatch(episodes, progress);

  const head = document.createElement('div');
  head.className = 'show-head';
  const poster = coverArt(artUrl(series.sourceId, series.artId), series.title);
  poster.classList.add('show-poster');
  const text = document.createElement('div');
  text.className = 'show-text';
  const title = document.createElement('h1');
  title.textContent = series.title;
  const meta = document.createElement('p');
  meta.className = 'muted';
  meta.textContent = [series.year, `${seasons.length} season${seasons.length === 1 ? '' : 's'}`]
    .filter(Boolean).join(' \u00b7 ');
  text.append(title, meta);
  if (series.extra && series.extra.overview) {
    const about = document.createElement('p');
    about.className = 'show-about';
    about.textContent = series.extra.overview;
    text.append(about);
  }
  if (next) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'play-main';
    const part = progress[next.id] > 0.005 && progress[next.id] < 0.93;
    button.textContent = `\u25B6  ${part ? 'Resume' : 'Play'} ${episodeLabel(next)}`;
    button.addEventListener('click', () => playVideo(next));
    text.append(button);
  }
  head.append(poster, text);

  const list = document.createElement('ol');
  list.className = 'episode-list';
  const showSeason = (season) => {
    list.replaceChildren(...episodes.filter((e) => (e.extra.season || '0') === season)
      .map((e) => episodeRow(e, progress[e.id] || 0)));
    for (const b of pills.querySelectorAll('button')) b.classList.toggle('active', b.dataset.season === season);
  };
  const pills = document.createElement('div');
  pills.className = 'show-seasons';
  if (seasons.length > 1) {
    for (const season of seasons) {
      const b = document.createElement('button');
      b.type = 'button';
      b.dataset.season = season;
      b.textContent = season === '0' ? 'Specials' : `Season ${season}`;
      b.addEventListener('click', () => showSeason(season));
      pills.append(b);
    }
  }
  showSeason(next ? (next.extra.season || '0') : seasons[0]);
  view.replaceChildren(backButton('TV', () => runSearch()), head, pills, list);
  window.scrollTo(0, 0);
}

const episodeLabel = (e) => (e.extra.season && e.extra.number ? `S${e.extra.season} E${e.extra.number}` : e.title);

// What to watch now: one part-watched, else the one after the last finished,
// else the first.
function nextToWatch(episodes, progress) {
  const part = episodes.find((e) => progress[e.id] > 0.005 && progress[e.id] < 0.93);
  if (part) return part;
  let lastDone = -1;
  episodes.forEach((e, i) => { if (progress[e.id] >= 0.93) lastDone = i; });
  return episodes[lastDone + 1] || episodes[0] || null;
}

function episodeRow(episode, fraction) {
  const li = document.createElement('li');
  const row = document.createElement('button');
  row.type = 'button';
  row.className = 'episode-row';
  const thumb = document.createElement('div');
  thumb.className = 'episode-thumb';
  if (episode.artId) {
    const img = document.createElement('img');
    img.src = artUrl(episode.sourceId, episode.artId);
    img.alt = '';
    img.loading = 'lazy';
    img.decoding = 'async';
    thumb.append(img);
  }
  if (fraction > 0.005 && fraction < 0.93) {
    const bar = document.createElement('span');
    bar.className = 'progress';
    const fill = document.createElement('span');
    fill.style.width = `${Math.round(fraction * 100)}%`;
    bar.append(fill);
    thumb.append(bar);
  }
  const text = document.createElement('div');
  text.className = 'episode-text';
  const name = document.createElement('strong');
  name.textContent = `${episode.extra.number ? `${episode.extra.number}. ` : ''}${episode.title}`;
  const meta = document.createElement('span');
  meta.className = 'muted';
  const minutes = Math.round((episode.durationSeconds || 0) / 60);
  meta.textContent = [minutes ? `${minutes} min` : '', fraction >= 0.93 ? 'Watched' : ''].filter(Boolean).join(' \u00b7 ');
  text.append(name, meta);
  if (episode.extra.overview) {
    const about = document.createElement('p');
    about.textContent = episode.extra.overview;
    text.append(about);
  }
  row.append(thumb, text);
  if (fraction >= 0.93) {
    const done = icon('check');
    done.classList.add('episode-done');
    row.append(done);
  }
  row.addEventListener('click', () => playVideo(episode));
  li.append(row);
  return li;
}

// When an episode ends, the next one - from the show's page if it was opened
// there, else asked of the server - after a short countdown that Play now
// skips and Cancel stops.
let upNextTimer = 0;

async function offerNextEpisode(episode) {
  let next = null;
  const list = state.showEpisodes || [];
  const at = list.findIndex((e) => e.id === episode.id && e.sourceId === episode.sourceId);
  if (at >= 0) next = list[at + 1] || null;
  else {
    const { ok, body } = await api(`/api/tv/next?${new URLSearchParams({ source: episode.sourceId, id: episode.id })}`);
    if (ok && body && body.found) next = body.episode;
  }
  if (!next || !state.watching || state.watching.item.id !== episode.id) return;
  $('up-next-title').textContent = `${episodeLabel(next)} \u00b7 ${next.title}`;
  show($('up-next'), true);
  const seconds = 8;
  const started = Date.now();
  const fill = $('up-next-fill');
  clearInterval(upNextTimer);
  upNextTimer = setInterval(() => {
    const k = Math.min(1, (Date.now() - started) / (seconds * 1000));
    fill.style.width = `${Math.round(k * 100)}%`;
    if (k >= 1) playVideo(next);
  }, 100);
  $('up-next-play').onclick = () => playVideo(next);
}

function hideUpNext() {
  clearInterval(upNextTimer);
  show($('up-next'), false);
}

$('up-next-cancel').addEventListener('click', hideUpNext);

// The audio language, when a file has more than one: choosing another plays
// on from the same moment in it.
function attachAudioChoice(item, tracks, chosen) {
  const select = $('audio-select');
  select.replaceChildren(...tracks.map((t) => {
    const option = document.createElement('option');
    option.value = String(t.index);
    option.textContent = t.label;
    return option;
  }));
  const current = chosen !== undefined ? chosen : (tracks.find((t) => t.default) || tracks[0] || {}).index;
  if (current !== undefined) select.value = String(current);
  select.onchange = async () => {
    await saveWatchPosition(true);
    playVideo(item, { audio: Number(select.value) });
  };
  show($('audio-picker'), tracks.length > 1);
}

/* ------------------------------------------------ choosing the categories */

// The + at the end of a row of categories: tap it for the ones put away, and
// one tapped comes back; hold a category and drag it onto the + to put it
// away. Genres start put away. Kept on the account, like the order.
const isPill = (b) => !b.classList.contains('hidden') && !b.classList.contains('pill-off') && !b.classList.contains('pill-add');

function hiddenPills(row) {
  const saved = state.prefs && state.prefs.hiddenPills && state.prefs.hiddenPills[row];
  return Array.isArray(saved) ? saved : (DEFAULT_HIDDEN[row] || []);
}

function setHiddenPills(row, keys) {
  state.prefs = state.prefs || {};
  state.prefs.hiddenPills = { ...(state.prefs.hiddenPills || {}), [row]: keys };
  savePrefs({ hiddenPills: { [row]: keys } });
}

// What the + offers for a row: its categories put away, that this account has.
function pillsToAdd(row) {
  const put = hiddenPills(row);
  if (row === 'music') {
    return [...document.querySelectorAll('#music-tabs [data-view]')]
      .filter((b) => put.includes(b.dataset.view) && !(state.offline && b.classList.contains('hidden')))
      .map((b) => ({ key: b.dataset.view, label: b.textContent }));
  }
  return (TABS[row] || []).filter((o) => put.includes(o.kind) && shelfAvailable(o.kind))
    .map((o) => ({ key: o.kind, label: o.label }));
}

function addPillButton() {
  const b = document.createElement('button');
  b.type = 'button';
  b.className = 'pill-add';
  b.setAttribute('aria-label', 'Add a category');
  b.append(icon('plus'));
  b.addEventListener('click', (event) => {
    event.stopPropagation();
    openPillPicker(state.tab, b);
  });
  return b;
}

function openPillPicker(row, anchor) {
  const menu = $('item-menu');
  if (state.menuFor === 'pills' && !menu.classList.contains('hidden')) { closeItemMenu(); return; }
  state.menuFor = 'pills';
  state.menuAnchor = anchor;
  const head = document.createElement('div');
  head.className = 'menu-head';
  const title = document.createElement('strong');
  title.textContent = 'Add a category';
  head.append(title);
  const choices = pillsToAdd(row);
  const entries = choices.map((c) => menuItem('plus', c.label, (event) => {
    event.stopPropagation();
    closeItemMenu();
    setHiddenPills(row, hiddenPills(row).filter((k) => k !== c.key));
    if (row === 'music') {
      markMusicTabs();
      const pill = document.querySelector(`#music-tabs [data-view="${c.key}"]`);
      if (pill) pill.click();
    } else {
      renderTabs();
      selectKind(c.key);
    }
  }));
  if (!entries.length) {
    const none = document.createElement('p');
    none.className = 'menu-confirm';
    none.textContent = 'Every category is showing. To put one away, hold it and drag it onto the +.';
    entries.push(none);
  }
  menu.replaceChildren(head, ...entries);
  placeMenu(menu, anchor);
}

// A category dropped on the +: put away, unless it is the last one showing.
function putPillAway(row, key) {
  const rowKey = row.id === 'music-tabs' ? 'music' : state.tab;
  if (!key || pillsIn(row).length <= 1) return;
  setHiddenPills(rowKey, [...new Set([...hiddenPills(rowKey), key])]);
  const wasOn = row.querySelector('button.active');
  const leaving = wasOn && (wasOn.dataset.view || wasOn.dataset.kind) === key;
  if (rowKey === 'music') {
    markMusicTabs();
    if (leaving) {
      const first = [...row.querySelectorAll('[data-view]')].find(isPill);
      if (first) first.click();
    }
  } else {
    renderTabs();
    if (leaving) {
      const first = tabShelves(rowKey).find((o) => !o.inMusicTabs);
      if (first) selectKind(first.kind);
    }
  }
  showToast('Put away. Tap + to bring it back.');
}

function pillsIn(row) {
  return [...row.querySelectorAll('button')].filter(isPill);
}

// Music's + is part of the page; the tabs' are made with their rows.
(function musicPlus() {
  const plus = document.querySelector('#music-tabs .pill-add');
  if (!plus) return;
  plus.append(icon('plus'));
  plus.addEventListener('click', (event) => {
    event.stopPropagation();
    openPillPicker('music', plus);
  });
})();

/* ------------------------------------------------------------------ genres */

// A tab's genres, most common first, each opening what is in it - across the
// tab's shelves, as the server groups them.
const genreNoun = (kind, n) => {
  const word = kind === 'genres-music' ? 'song' : kind === 'genres-books' ? 'book' : 'title';
  return `${n} ${word}${n === 1 ? '' : 's'}`;
};

async function showGenres(seq) {
  const view = $('music-view');
  const kind = state.kind;
  $('status').textContent = '';
  if (!view.children.length) showSkeleton(view, 'grid');
  const { ok, body } = await api(`/api/genres?${new URLSearchParams({ kinds: GENRE_KINDS[kind].join(',') })}`);
  if (seq !== state.searchSeq) return;
  const words = state.query.toLowerCase().split(/\s+/).filter(Boolean);
  const list = ((ok && body && body.genres) || []).filter((g) => words.every((w) => g.name.toLowerCase().includes(w)));
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid';
  grid.append(...list.map((g) => bookGroupCard(g, false, genreNoun(kind, g.count), () => showGenre(g, kind))));
  view.replaceChildren(grid);
  $('status').textContent = list.length ? ''
    : (state.query ? 'No genre matches.' : 'No genres yet. They come from what each file says it is.');
}

async function showGenre(genre, kind) {
  const seq = ++state.searchSeq;
  const view = $('music-view');
  startLoading(view);
  const { ok, body } = await api(`/api/genres?${new URLSearchParams({ kinds: GENRE_KINDS[kind].join(','), name: genre.name })}`);
  if (seq !== state.searchSeq) return;
  if (!ok || !body) {
    view.replaceChildren(backButton('Genres', () => runSearch()));
    $('status').textContent = 'Could not load that genre.';
    return;
  }
  const items = body.items || [];
  state.items = items;
  $('status').textContent = '';
  const parts = [backButton('Genres', () => runSearch()), pageHead(genre.name, genreNoun(kind, items.length))];
  if (kind === 'genres-music' && items.length) parts.push(playButtons(async () => items));
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid';
  grid.append(...items.map(renderItem));
  parts.push(grid);
  view.replaceChildren(...parts);
  window.scrollTo(0, 0);
}

/* --------------------------------------------------------------- scrobbling */

// Scrobbling is each person's own: their ListenBrainz account, their token.
state.scrobbling = false;

async function refreshScrobbling() {
  const { ok, body } = await api('/api/scrobble');
  const available = ok && body && body.available;
  show($('scrobble-block'), Boolean(available));
  if (!available) return;
  state.scrobbling = Boolean(body.connected && !body.problem);
  show($('scrobble-off'), !body.connected);
  show($('scrobble-on'), Boolean(body.connected));
  if (body.connected) {
    const waiting = body.pending ? ` ${body.pending} play${body.pending === 1 ? '' : 's'} waiting to send.` : '';
    $('scrobble-who').textContent = `Sending your plays to ListenBrainz as ${body.userName}.${waiting}`;
    if (body.problem) note($('scrobble-note'), body.problem, true);
  }
}

$('scrobble-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const token = $('scrobble-token').value.trim();
  if (!token) return;
  const button = event.target.querySelector('button');
  button.disabled = true;
  const { ok, body } = await api('/api/scrobble', { method: 'PUT', body: JSON.stringify({ token }) });
  button.disabled = false;
  if (!ok) {
    note($('scrobble-note'), (body && body.error) || 'Could not connect.', true);
    return;
  }
  $('scrobble-token').value = '';
  note($('scrobble-note'), `Connected. Songs you play from now on appear on ListenBrainz as ${body.userName}'s.`, false);
  refreshScrobbling();
});

$('scrobble-disconnect').addEventListener('click', async () => {
  const { ok } = await api('/api/scrobble', { method: 'DELETE' });
  if (ok) {
    note($('scrobble-note'), 'Disconnected. Nothing more is sent.', false);
    refreshScrobbling();
  }
});

/* ------------------------------------------------------------ year in music */

// A recap of somebody's listening, tapped through like a story: this year so
// far, any earlier year, or all time. Private to them, any day of the year.
const recap = { data: null, slides: [], index: 0, period: 'year', year: 0 };

function recapBanner() {
  const year = new Date().getFullYear();
  const card = document.createElement('button');
  card.type = 'button';
  card.className = 'recap-banner';
  const title = document.createElement('strong');
  title.textContent = `Your ${year} in music`;
  const sub = document.createElement('span');
  sub.textContent = 'Top songs, artists and how you listened, so far';
  card.append(title, sub);
  card.addEventListener('click', () => openRecap('year', year));
  return card;
}

async function openRecap(period, year) {
  recap.period = period;
  recap.year = year;
  const params = new URLSearchParams({ period, tz: String(-new Date().getTimezoneOffset()) });
  if (period === 'year' && year) params.set('year', String(year));
  const { ok, body } = await api(`/api/recap?${params}`);
  if (!ok || !body) {
    showToast('Your recap could not be made just now.');
    return;
  }
  recap.data = body;
  recap.slides = recapSlides(body);
  recap.index = 0;
  show($('recap-overlay'), true);
  document.body.classList.add('recap-open');
  drawRecap();
}

function closeRecap() {
  show($('recap-overlay'), false);
  document.body.classList.remove('recap-open');
}

function recapEl(tag, className, text) {
  const el = document.createElement(tag);
  if (className) el.className = className;
  if (text !== undefined) el.textContent = text;
  return el;
}

function recapPeriodLabel(d) {
  if (d.period === 'all') return 'All time';
  return d.year === new Date().getFullYear() ? `Your ${d.year} so far` : `Your ${d.year}`;
}

// The switch between this year, earlier years and all time.
function recapPeriods(d) {
  const row = recapEl('div', 'recap-periods');
  const years = (d.years && d.years.length ? d.years : [new Date().getFullYear()]);
  const choices = years.map((y) => ({ period: 'year', year: y, label: String(y) }));
  choices.push({ period: 'all', year: 0, label: 'All time' });
  for (const c of choices) {
    const b = recapEl('button', 'chip', c.label);
    b.type = 'button';
    const on = c.period === d.period && (c.period === 'all' || c.year === d.year);
    b.classList.toggle('active', on);
    b.setAttribute('aria-pressed', String(on));
    b.addEventListener('click', (event) => {
      event.stopPropagation();
      if (!on) openRecap(c.period, c.year);
    });
    row.append(b);
  }
  return row;
}

function recapCover(item, round) {
  const art = item.artId ? artUrl(item.sourceId, item.artId) : '';
  return coverArt(art, item.name, round);
}

function recapList(items, withArt) {
  const list = recapEl('ol', 'recap-list');
  items.forEach((it, i) => {
    const row = recapEl('li');
    row.append(recapEl('span', 'recap-rank', String(i + 1)));
    if (withArt) row.append(recapCover(it, false));
    const text = recapEl('span', 'recap-text');
    text.append(recapEl('strong', '', it.name));
    const bits = [it.sub, `${it.plays} play${it.plays === 1 ? '' : 's'}`].filter(Boolean);
    text.append(recapEl('span', '', bits.join(' · ')));
    row.append(text);
    list.append(row);
  });
  return list;
}

function playRecapSongs(d) {
  const songs = (d.topSongs || []).map((s) => s.item).filter(Boolean);
  if (!songs.length) return;
  closeRecap();
  playQueue(songs, 0);
}

function recapPlayButton(d) {
  const songs = (d.topSongs || []).filter((s) => s.item);
  if (!songs.length) return null;
  const b = recapEl('button', 'play-main recap-play', `\u25B6  Play your top ${songs.length}`);
  b.type = 'button';
  b.addEventListener('click', (event) => {
    event.stopPropagation();
    playRecapSongs(d);
  });
  return b;
}

const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const WEEKDAYS = ['Sundays', 'Mondays', 'Tuesdays', 'Wednesdays', 'Thursdays', 'Fridays', 'Saturdays'];

function peakIndex(values) {
  let best = 0;
  values.forEach((v, i) => { if (v > values[best]) best = i; });
  return best;
}

// When somebody listens most, in words.
function listenerKind(hours) {
  const h = peakIndex(hours);
  if (h < 5) return ['A night owl', 'Your music comes out after midnight.'];
  if (h < 10) return ['An early bird', 'You start the day with music.'];
  if (h < 17) return ['A daytime listener', 'Music keeps your day going.'];
  if (h < 22) return ['An evening listener', 'You wind down with music.'];
  return ['A late-night listener', 'Your music plays late into the night.'];
}

function shortDate(iso) {
  const d = new Date(`${iso}T12:00:00`);
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// Each slide is a function that fills a box. Slides with nothing to say are
// left out, so a new listener sees a short recap rather than empty ones.
function recapSlides(d) {
  const slides = [];
  slides.push((box) => {
    box.classList.add('recap-intro');
    box.append(recapEl('span', 'recap-kicker', recapPeriodLabel(d)));
    if (!d.plays) {
      box.append(recapEl('h2', '', 'Nothing yet'),
        recapEl('p', 'recap-lede', d.period === 'all'
          ? 'Play some music and this fills in.'
          : 'Songs you play from now on count toward this year. All time has what you have played so far.'));
    } else {
      box.append(recapEl('span', 'recap-big', d.minutes.toLocaleString()),
        recapEl('span', 'recap-unit', `minute${d.minutes === 1 ? '' : 's'} of music`),
        recapEl('p', 'recap-lede', `${d.plays.toLocaleString()} plays · ${d.songs.toLocaleString()} songs · ${d.artists.toLocaleString()} artists`));
      if (d.since && d.period === 'year' && new Date(d.since).getMonth() > 0) {
        box.append(recapEl('p', 'recap-small', `Counting since ${new Date(d.since).toLocaleDateString(undefined, { month: 'long', day: 'numeric' })}, when SoundStorm started keeping track.`));
      }
    }
    box.append(recapPeriods(d));
  });
  if (!d.plays) return slides;

  const artists = d.topArtists || [];
  if (artists.length) {
    slides.push((box) => {
      const top = artists[0];
      box.append(recapEl('span', 'recap-kicker', 'Your top artist'));
      const art = recapCover(top, true);
      art.classList.add('recap-hero');
      box.append(art, recapEl('h2', '', top.name),
        recapEl('p', 'recap-lede', `${top.plays.toLocaleString()} plays · ${top.minutes.toLocaleString()} minutes`));
      if (artists.length > 1) box.append(recapList(artists.slice(1).map((a) => ({ ...a, sub: '' })), false));
    });
  }
  const songs = d.topSongs || [];
  if (songs.length) {
    slides.push((box) => {
      box.append(recapEl('span', 'recap-kicker', 'Your top songs'), recapList(songs.slice(0, 5), true));
      const play = recapPlayButton(d);
      if (play) box.append(play);
    });
  }
  const albums = d.topAlbums || [];
  if (albums.length) {
    slides.push((box) => {
      box.append(recapEl('span', 'recap-kicker', 'Your top albums'), recapList(albums, true));
    });
  }
  if (d.hours && d.months) {
    slides.push((box) => {
      const [kind, line] = listenerKind(d.hours);
      box.append(recapEl('span', 'recap-kicker', 'How you listened'), recapEl('h2', '', kind), recapEl('p', 'recap-lede', line));
      const chart = recapEl('div', 'recap-chart');
      const peak = Math.max(1, ...d.months);
      d.months.forEach((n, i) => {
        const col = recapEl('div', 'recap-col');
        const bar = recapEl('span', 'recap-bar');
        bar.style.height = `${Math.round((n / peak) * 100)}%`;
        bar.title = `${MONTHS[i]}: ${n} plays`;
        col.append(bar, recapEl('span', 'recap-col-label', MONTHS[i][0]));
        chart.append(col);
      });
      box.append(chart,
        recapEl('p', 'recap-small', `Your biggest month was ${MONTHS[peakIndex(d.months)]}, and you listened most on ${WEEKDAYS[peakIndex(d.weekdays)]}.`));
    });
  }
  if (d.streak || d.newArtists || d.first) {
    slides.push((box) => {
      box.append(recapEl('span', 'recap-kicker', 'Milestones'));
      const facts = recapEl('div', 'recap-facts');
      const fact = (big, line) => {
        const f = recapEl('div', 'recap-fact');
        f.append(recapEl('strong', '', big), recapEl('span', '', line));
        facts.append(f);
      };
      if (d.streak && d.streak.days > 1) {
        fact(`${d.streak.days} days`, `in a row with music, ${shortDate(d.streak.from)} to ${shortDate(d.streak.to)}`);
      }
      if (d.newArtists) {
        fact(`${d.newArtists} new`, `artist${d.newArtists === 1 ? '' : 's'} you had never played before${d.newTop ? `, most of all ${d.newTop.name}` : ''}`);
      }
      if (d.first) {
        const when = d.firstAt ? new Date(d.firstAt).toLocaleDateString(undefined, { month: 'long', day: 'numeric' }) : '';
        fact(d.first.name, `by ${d.first.sub}, your first song of the year${when ? `, on ${when}` : ''}`);
      }
      box.append(facts);
    });
  }
  if ((d.moods && d.moods.length) || (d.topGenres && d.topGenres.length)) {
    slides.push((box) => {
      box.append(recapEl('span', 'recap-kicker', 'How it sounded'));
      if (d.moods && d.moods.length) {
        box.append(recapEl('h2', '', `Mostly ${d.moods[0].title.toLowerCase()}`));
        const bars = recapEl('div', 'recap-moods');
        for (const m of d.moods) {
          const row = recapEl('div', 'recap-mood');
          const fill = recapEl('span', 'recap-mood-fill');
          fill.style.width = `${m.share}%`;
          row.append(recapEl('span', 'recap-mood-name', m.title), fill, recapEl('span', 'recap-mood-share', `${m.share}%`));
          bars.append(row);
        }
        box.append(bars);
      }
      if (d.topGenres && d.topGenres.length) {
        box.append(recapEl('p', 'recap-small', `Top genres: ${d.topGenres.map((g) => g.name).join(', ')}.`));
      }
    });
  }
  slides.push((box) => {
    box.classList.add('recap-intro');
    box.append(recapEl('span', 'recap-kicker', recapPeriodLabel(d)),
      recapEl('h2', '', artists.length ? `${artists[0].name}, ${songs.length ? `"${songs[0].name}"` : ''}`.replace(/, $/, '') : 'Thanks for listening'),
      recapEl('p', 'recap-lede', d.period === 'year' && d.year === new Date().getFullYear()
        ? 'Your year so far. It keeps counting.' : 'That was your music.'));
    const play = recapPlayButton(d);
    if (play) box.append(play);
    box.append(recapPeriods(d));
  });
  return slides;
}

function drawRecap() {
  const bars = $('recap-bars');
  bars.replaceChildren(...recap.slides.map((_, i) => {
    const b = recapEl('span', 'recap-bar-seg');
    b.classList.toggle('done', i <= recap.index);
    return b;
  }));
  const box = $('recap-slide');
  box.className = 'recap-slide';
  box.replaceChildren();
  recap.slides[recap.index](box);
}

function stepRecap(by) {
  const next = recap.index + by;
  if (next < 0) return;
  if (next >= recap.slides.length) {
    closeRecap();
    return;
  }
  recap.index = next;
  drawRecap();
}

$('recap-close').append(icon('close'));
$('recap-close').addEventListener('click', (event) => {
  event.stopPropagation();
  closeRecap();
});
// A tap on the left third goes back, anywhere else forward - as a story does.
$('recap-slide').addEventListener('click', (event) => {
  if (event.target.closest('button, a')) return;
  stepRecap(event.clientX < window.innerWidth / 3 ? -1 : 1);
});
document.addEventListener('keydown', (event) => {
  if (!shown('recap-overlay')) return;
  if (event.key === 'ArrowRight' || event.key === ' ') { event.preventDefault(); stepRecap(1); }
  if (event.key === 'ArrowLeft') { event.preventDefault(); stepRecap(-1); }
  if (event.key === 'Escape') closeRecap();
});

/* ------------------------------------------------------ rearranging Up next */

// Hold a song in Up next - as long as a card's hold - and it lifts; drag it
// and the others slide aside to show where it will go; let go and it is
// there. Moving before the hold completes is a scroll, and a quick tap still
// plays the song. Near the top or bottom of the list it scrolls.
(() => {
  const list = $('np-queue');
  const EDGE = 48;
  let hold = null;
  let drag = null;
  let swallowClick = false;

  const rowsOf = () => [...list.children];
  const cancelHold = () => {
    if (hold) clearTimeout(hold.timer);
    hold = null;
  };

  const place = () => {
    if (!drag) return;
    const dy = drag.y - drag.startY + (list.scrollTop - drag.startScroll);
    drag.li.style.transform = `translateY(${dy}px)`;
    const to = Math.max(0, Math.min(drag.rows.length - 1, drag.from + Math.round(dy / drag.step)));
    drag.to = to;
    drag.rows.forEach((row, i) => {
      if (row === drag.li) return;
      let shift = 0;
      if (drag.from < i && i <= to) shift = -drag.step;
      else if (to <= i && i < drag.from) shift = drag.step;
      row.style.transform = shift ? `translateY(${shift}px)` : '';
    });
  };

  // Near an edge of the list, it scrolls, faster the nearer.
  const edgeScroll = () => {
    if (!drag) return;
    const r = list.getBoundingClientRect();
    let by = 0;
    if (drag.y < r.top + EDGE) by = -Math.ceil((r.top + EDGE - drag.y) / 6);
    else if (drag.y > r.bottom - EDGE) by = Math.ceil((drag.y - (r.bottom - EDGE)) / 6);
    if (by) {
      list.scrollTop += by;
      place();
    }
    drag.frame = requestAnimationFrame(edgeScroll);
  };

  const start = () => {
    const li = hold.li;
    const rows = rowsOf();
    const from = rows.indexOf(li);
    if (from < 0) return;
    const gap = parseFloat(getComputedStyle(list).rowGap) || 0;
    drag = { li, rows, from, to: from, startY: hold.y, y: hold.y, startScroll: list.scrollTop,
      step: li.getBoundingClientRect().height + gap };
    hold = null;
    queueDrag.active = true;
    list.classList.add('reordering');
    li.classList.add('dragging');
    if (navigator.vibrate) navigator.vibrate(10);
    drag.frame = requestAnimationFrame(edgeScroll);
  };

  const finish = (commit) => {
    const d = drag;
    drag = null;
    queueDrag.active = false;
    cancelAnimationFrame(d.frame);
    list.classList.remove('reordering');
    d.li.classList.remove('dragging');
    for (const row of d.rows) row.style.transform = '';
    swallowClick = true;
    setTimeout(() => { swallowClick = false; }, 400);
    const q = audio.queue;
    if (commit && q && d.to !== d.from) queueMove(q.index + 1 + d.from, q.index + 1 + d.to);
    else renderNowPlaying();
  };

  list.addEventListener('pointerdown', (event) => {
    if (event.button !== 0 || drag || event.target.closest('.np-remove')) return;
    const li = event.target.closest('#np-queue > li');
    if (!li) return;
    cancelHold();
    hold = { li, x: event.clientX, y: event.clientY, timer: setTimeout(start, HOLD_MS) };
  });
  list.addEventListener('pointermove', (event) => {
    if (hold && Math.hypot(event.clientX - hold.x, event.clientY - hold.y) > HOLD_SLOP) cancelHold();
    if (!drag) return;
    drag.y = event.clientY;
    place();
  });
  const end = (commit) => () => {
    cancelHold();
    if (drag) finish(commit);
  };
  list.addEventListener('pointerup', end(true));
  list.addEventListener('pointercancel', end(false));
  // Once a song is lifted the finger moves it, not the list: the browser
  // must not take the gesture for a scroll.
  list.addEventListener('touchmove', (event) => {
    if (drag && event.cancelable) event.preventDefault();
  }, { passive: false });
  // The lift at the end of a hold, or of a drag, is not a tap on the song.
  list.addEventListener('click', (event) => {
    if (!swallowClick) return;
    swallowClick = false;
    event.stopPropagation();
    event.preventDefault();
  }, true);
  list.addEventListener('contextmenu', (event) => {
    if (hold || drag) event.preventDefault();
  });
})();

/* ------------------------------------------------------ Now Playing's menu */

// Holding the big cover opens its menu: the player's options first - songs
// like this, shuffle, repeat, the sleep timer, Up next - then the song's own,
// as on any card. They used to be buttons over the cover. A tap still turns
// it into a record, and a swipe still changes song; right-click opens it too.
const npHold = { at: 0 };

function openNowPlayingMenu(anchor = $('np-cover')) {
  const item = audio.item;
  if (!item) return;
  const menu = $('item-menu');
  state.menuFor = item;
  state.menuAnchor = anchor;
  state.menuOpts = { nowPlaying: true };
  renderMainMenu(item, state.menuOpts);
  placeMenu(menu, state.menuAnchor);
}

function playerMenuItems(item, opts) {
  const again = () => renderMainMenu(item, opts);
  const out = [];
  if (item.kind === 'music') {
    out.push(menuItem('radio', 'Songs like this', () => {
      closeItemMenu();
      playLikeThis();
    }));
    out.push(menuItem('shuffle', 'Shuffle', () => {
      setShuffle(!audio.shuffle);
      again();
    }, { detail: audio.shuffle ? 'On' : 'Off' }));
    out.push(menuItem('repeat', 'Repeat', () => {
      cycleRepeat();
      again();
    }, { detail: { off: 'Off', all: 'All', one: 'This song' }[audio.repeat] || 'Off' }));
  }
  const left = sleep.atSongEnd ? 'After this song'
    : sleep.until ? `${Math.max(1, Math.round((sleep.until - Date.now()) / 60000))} min` : 'Off';
  out.push(menuItem('moon', 'Sleep timer', (event) => {
    event.stopPropagation();
    renderSleepMenu(item, opts);
  }, { chevron: true, detail: left }));
  if (item.kind === 'music') {
    const open = !$('now-playing').classList.contains('hidden');
    out.push(menuItem('queue', open && audio.showQueue ? 'Hide up next' : 'Up next', () => {
      closeItemMenu();
      // From the mini-player, Up next is Now Playing opened on the queue.
      if (!open) {
        openNowPlaying();
        audio.showQueue = true;
      } else {
        audio.showQueue = !audio.showQueue;
      }
      renderLyrics();
    }));
  }
  return out;
}

// The sleep timer's choices, as a second page of the menu.
function renderSleepMenu(item, opts) {
  const menu = $('item-menu');
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const label = document.createElement('span');
  label.textContent = 'Sleep timer';
  back.append(label);
  back.addEventListener('click', (event) => {
    event.stopPropagation();
    renderMainMenu(item, opts);
  });
  const choose = (value, message) => () => {
    setSleep(value);
    closeItemMenu();
    showToast(message);
  };
  const rows = [
    menuItem('moon', 'In 15 minutes', choose('15', 'Stops in 15 minutes.')),
    menuItem('moon', 'In 30 minutes', choose('30', 'Stops in 30 minutes.')),
    menuItem('moon', 'In 45 minutes', choose('45', 'Stops in 45 minutes.')),
    menuItem('moon', 'In 1 hour', choose('60', 'Stops in an hour.')),
    menuItem('moon', item.kind === 'audiobook' ? 'At the end of this chapter' : 'At the end of this song',
      choose('song', item.kind === 'audiobook' ? 'Stops after this chapter.' : 'Stops after this song.')),
  ];
  if (sleep.until || sleep.atSongEnd) rows.push(menuItem('close', 'Turn off', choose(null, 'Sleep timer off.')));
  menu.replaceChildren(back, ...rows);
  placeMenu(menu, state.menuAnchor);
}

// Anywhere on the screen, not only the cover: a hold on the background,
// the title or the lyrics opens the menu too. Not on what already answers a
// touch - play, the header's buttons, the timeline, Up next (a hold there
// moves a song), a menu.
const NP_HOLD_SKIP = 'input, a, #np-queue, .np-bar button, .np-controls button, #np-speed-wrap, #item-menu';
(() => {
  const panel = $('now-playing');
  $('np-cover').draggable = false;
  let timer = null;
  let x = 0;
  let y = 0;
  const cancel = () => {
    clearTimeout(timer);
    timer = null;
  };
  panel.addEventListener('pointerdown', (event) => {
    if (event.button !== 0 || event.target.closest(NP_HOLD_SKIP)) return;
    x = event.clientX;
    y = event.clientY;
    cancel();
    timer = setTimeout(() => {
      timer = null;
      npHold.at = performance.now();
      state.heldAt = npHold.at;
      if (navigator.vibrate) navigator.vibrate(10);
      holdStarted();
      openNowPlayingMenu();
      followMenuFinger(event.pointerId, x, y);
    }, HOLD_MS);
  });
  panel.addEventListener('pointermove', (event) => {
    if (timer && Math.hypot(event.clientX - x, event.clientY - y) > HOLD_SLOP) cancel();
  });
  for (const type of ['pointerup', 'pointercancel']) panel.addEventListener(type, cancel);
  panel.addEventListener('contextmenu', (event) => {
    if (event.target.closest(NP_HOLD_SKIP)) return;
    event.preventDefault();
    cancel();
    if (state.holding) return; // a long press Android also reports; answered above
    npHold.at = performance.now();
    openNowPlayingMenu();
  });
})();

/* ------------------------------------------- press, slide and release */

// A menu opened by holding can be used without lifting the finger: slide it
// over the options and the one under it lights; lift on one and it is
// chosen; slide off the menu and nothing is lit, and lifting there closes
// the menu. Lifting without having moved leaves the menu open to tap, as it
// always was. Nothing is lit until the finger moves, so whatever happened to
// be under it when the menu appeared is not taken for a choice.
const menuFinger = { id: null, entered: false };

function fingerOverMenu(x, y) {
  const el = document.elementFromPoint(x, y);
  return Boolean(el && el.closest('#item-menu'));
}

function followMenuFinger(pointerId, x0, y0) {
  menuFinger.id = pointerId;
  menuFinger.entered = false;
  let moved = false;
  let lit = null;
  const light = (el) => {
    if (lit === el) return;
    if (lit) lit.classList.remove('finger');
    lit = el;
    if (lit) lit.classList.add('finger');
  };
  const onMove = (event) => {
    if (event.pointerId !== pointerId) return;
    if (!moved && Math.hypot(event.clientX - x0, event.clientY - y0) < HOLD_SLOP) return;
    moved = true;
    const menu = $('item-menu');
    if (menu.classList.contains('hidden') || state.dragSelect) {
      light(null);
      return;
    }
    const el = document.elementFromPoint(event.clientX, event.clientY);
    const inMenu = el && el.closest('#item-menu');
    if (inMenu) menuFinger.entered = true;
    // Only the menu's own buttons light; the header and a text box do not.
    light(inMenu ? el.closest('#item-menu button:not([type="submit"])') : null);
  };
  const onEnd = (event) => {
    if (event.pointerId !== pointerId) return;
    document.removeEventListener('pointermove', onMove);
    document.removeEventListener('pointerup', onEnd);
    document.removeEventListener('pointercancel', onEnd);
    menuFinger.id = null;
    menuFinger.entered = false;
    const chosen = event.type === 'pointerup' ? lit : null;
    light(null);
    if (!moved) {
      // Lifted where it was held: the menu stays open to tap, and the
      // lift itself touches nothing under it - a lyric line, say.
      state.swallowClickUntil = performance.now() + 500;
      return;
    }
    if (state.dragSelect || $('item-menu').classList.contains('hidden')) return;
    if (chosen) chosen.click();
    else closeItemMenu();
    // The browser's own click for this lift, if it sends one, is not a
    // second choice or a click outside. A slide usually sends none, so the
    // card's own "swallow the lift" is let go too, or it would eat the next
    // real tap.
    state.swallowClickUntil = performance.now() + 500;
    setTimeout(() => { state.suppressClick = false; }, 500);
  };
  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup', onEnd);
  document.addEventListener('pointercancel', onEnd);
}

window.addEventListener('click', (event) => {
  if (performance.now() > (state.swallowClickUntil || 0)) return;
  state.swallowClickUntil = 0;
  state.suppressClick = false;
  event.stopPropagation();
  event.preventDefault();
}, true);

// Holding the mini-player opens the same menu as holding Now Playing, from
// wherever the music is. Not on its buttons, which answer a touch already.
(() => {
  const dock = $('audio-dock');
  let timer = null;
  let x = 0;
  let y = 0;
  const skip = (el) => el.closest('.dock-buttons, input, #audio-close');
  const cancel = () => {
    clearTimeout(timer);
    timer = null;
  };
  dock.addEventListener('pointerdown', (event) => {
    if (event.button !== 0 || skip(event.target)) return;
    x = event.clientX;
    y = event.clientY;
    cancel();
    timer = setTimeout(() => {
      timer = null;
      state.heldAt = performance.now();
      if (navigator.vibrate) navigator.vibrate(10);
      holdStarted();
      openNowPlayingMenu(dock);
      followMenuFinger(event.pointerId, x, y);
    }, HOLD_MS);
  });
  dock.addEventListener('pointermove', (event) => {
    if (timer && Math.hypot(event.clientX - x, event.clientY - y) > HOLD_SLOP) cancel();
  });
  for (const type of ['pointerup', 'pointercancel']) dock.addEventListener(type, cancel);
  dock.addEventListener('contextmenu', (event) => {
    if (skip(event.target)) return;
    event.preventDefault();
    cancel();
    if (!state.holding) openNowPlayingMenu(dock);
  });
})();
