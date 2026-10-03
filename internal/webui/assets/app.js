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

// The app runs only as the page itself. A book's chapter is a same-origin
// document in a frame, and one that loaded this script (or framed the app)
// could drive the signed-in account's controls with a tap - so in a frame it
// stops here. See also the reader, which takes scripts out of books.
if (window.top !== window.self) throw new Error('SoundStorm does not run inside a frame');

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
  // Held to choosing a new password: the server answers nothing else, so the
  // screen for it comes up wherever this happened.
  if (resp.status === 403 && body && body.mustRenew) showRenew();
  return { ok: resp.ok, status: resp.status, body };
}

// Who's listening? The people kept on this device (profiles.go on the
// server): a tile each, and switching to one needs their PIN if they set one,
// and the owner's PIN or password always. "Someone else" signs in, ticked to
// be kept.
let profilesShown = null;
const AVATAR_HUES = [210, 340, 28, 140, 265, 190, 5, 95];
function showProfiles(list) {
  profilesShown = list;
  for (const id of ['boot', 'gate', 'app', 'renew']) show($(id), false);
  show($('tabs'), false);
  show($('profiles'), true);
  // From Settings' Switch person there is a way back.
  show($('profiles-cancel'), Boolean(state.me));
  show($('profiles-secret'), false);
  show($('profiles-other'), true);
  const holder = $('profiles-list');
  holder.replaceChildren();
  for (const person of list.people) {
    const tile = document.createElement('button');
    tile.type = 'button';
    tile.className = 'profile-tile';
    tile.dataset.id = person.id;
    const avatar = document.createElement('span');
    avatar.className = 'profile-avatar';
    let hash = 0;
    for (const c of person.name) hash = (hash * 31 + c.charCodeAt(0)) >>> 0;
    avatar.style.background = `hsl(${AVATAR_HUES[hash % AVATAR_HUES.length]} 55% 42%)`;
    avatar.textContent = person.name.slice(0, 1).toUpperCase();
    const name = document.createElement('span');
    name.textContent = person.name;
    const lock = document.createElement('span');
    lock.className = 'profile-lock';
    lock.textContent = person.needs === 'pin' ? 'PIN' : person.needs === 'password' ? 'Password' : '';
    tile.append(avatar, name, lock);
    tile.addEventListener('click', () => pickProfile(person));
    holder.append(tile);
  }
  holder.firstChild?.focus();
}
let profilePicked = null;
function pickProfile(person) {
  profilePicked = person;
  if (!person.needs) {
    switchProfile(person, {});
    return;
  }
  // The rest stand aside while this person's PIN or password is asked for.
  for (const tile of $('profiles-list').children) show(tile, tile.dataset.id === person.id);
  show($('profiles-other'), false);
  show($('profiles-secret'), true);
  show($('profiles-error'), false);
  const input = $('profiles-secret-input');
  input.value = '';
  $('profiles-secret-label').textContent = person.needs === 'pin' ? `${person.name}'s PIN` : `${person.name}'s password`;
  input.inputMode = person.needs === 'pin' ? 'numeric' : 'text';
  // A password typed with a TV's remote is a chore: the phone can sign the
  // TV in instead (the owner's asking), with the same code and QR code as
  // the sign-in screen's.
  show($('profiles-phone'), TV);
  if (TV) $('profiles-phone').focus();
  else input.focus();
}
async function switchProfile(person, secret) {
  const { ok, status, body } = await api('/api/profiles/switch', {
    method: 'POST', body: JSON.stringify({ id: person.id, ...secret }),
  });
  if (ok && body && body.signedIn) {
    show($('profiles'), false);
    // Whoever was here before has nothing of theirs left on screen.
    if (state.me && state.me.id !== body.user.id) {
      location.reload();
      return;
    }
    showApp(body.user);
    // Music the app's player kept going while the page was made again is
    // taken over, as an ordinary opening does (a review: a TV with people
    // kept on it never did).
    setTimeout(adoptNativePlayback, 0);
    return;
  }
  const error = $('profiles-error');
  error.textContent = status === 403 ? (person.needs === 'pin' ? 'That PIN is not right.' : 'That password is not right.')
    : (body && body.error) || 'Could not switch.';
  show(error, true);
  if (!$('profiles-secret').classList.contains('hidden')) $('profiles-secret-input').select();
}
$('profiles-secret').addEventListener('submit', (event) => {
  event.preventDefault();
  if (!profilePicked) return;
  const value = $('profiles-secret-input').value;
  switchProfile(profilePicked, profilePicked.needs === 'pin' ? { pin: value } : { password: value });
});
$('profiles-secret-back').addEventListener('click', () => showProfiles(profilesShown));
$('profiles-phone').addEventListener('click', async () => {
  show($('profiles'), false);
  showGate(true, false);
  startLink();
});
$('profiles-cancel').addEventListener('click', () => {
  show($('profiles'), false);
  show($('app'), true);
  show($('tabs'), true);
});
$('profiles-other').addEventListener('click', async () => {
  show($('profiles'), false);
  const { body } = await api('/api/session');
  showGate(true, body && body.setupCodeRequired);
  $('gate-keep').checked = true;
});

// Settings, On this device: who is kept here, keeping or taking yourself off,
// switching, and your PIN.
async function refreshDeviceCard() {
  const me = state.me;
  if (!me) return;
  const { ok, body } = await api('/api/profiles');
  const people = (ok && body && body.people) || [];
  const kept = people.some((p) => p.id === me.id);
  const others = people.filter((p) => p.id !== me.id).map((p) => p.name);
  $('device-people').textContent = kept
    ? (others.length ? `You and ${others.join(', ')} can switch between each other here.` : 'You are kept on this device: it can switch back to you.')
    : (others.length ? `${others.join(', ')} ${others.length === 1 ? 'is' : 'are'} kept on this device.` : 'Nobody is kept on this device. Keep yourself on it to switch between people here, as on a shared TV.');
  show($('device-switch'), people.length > (kept ? 1 : 0));
  show($('device-keep'), !kept);
  show($('device-unkeep'), kept);
  $('pin-about').textContent = me.owner
    ? 'Asked when someone switches to you on a shared device. As the owner, switching to you always needs your PIN, or your password if you have none.'
    : 'Asked when someone switches to you on a shared device. Without one, anybody at a device you are kept on can switch to you.';
}
$('device-switch').addEventListener('click', async () => {
  const { ok, body } = await api('/api/profiles');
  if (ok && body) showProfiles(body);
});
$('device-keep').addEventListener('click', async () => {
  await api('/api/profiles/keep', { method: 'POST', body: '{}' });
  refreshDeviceCard();
});
$('device-unkeep').addEventListener('click', async () => {
  if (state.me) await api(`/api/profiles/${encodeURIComponent(state.me.id)}`, { method: 'DELETE' });
  refreshDeviceCard();
});
$('pin-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const pin = $('pin-new').value;
  const { ok, body } = await api('/api/account/pin', {
    method: 'PUT', body: JSON.stringify({ password: $('pin-password').value, pin }),
  });
  if (ok) {
    $('pin-password').value = '';
    $('pin-new').value = '';
    note($('pin-note'), pin ? 'PIN saved.' : 'PIN removed.');
    refreshDeviceCard();
  } else {
    note($('pin-note'), (body && body.error) || 'Could not save it.', true);
  }
});

// showRenew asks for a new password before anything else.
function showRenew() {
  if (!$('renew').classList.contains('hidden')) return;
  // Everything that sits over the app goes too, or the form opened behind
  // Now Playing with the music going on (a review).
  if (window.soundstormReader && shown('reader-overlay')) window.soundstormReader.close();
  if (shown('now-playing')) closeNowPlaying();
  closeVideo();
  if (audio.item) stopAudio();
  for (const id of ['boot', 'gate', 'app', 'profiles', 'tabs']) show($(id), false);
  show($('renew'), true);
  $('renew-current').focus();
}
$('renew-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const submit = $('renew-submit');
  submit.disabled = true;
  const { ok, body } = await api('/api/account/password', {
    method: 'POST',
    body: JSON.stringify({ current: $('renew-current').value, password: $('renew-new').value }),
  });
  submit.disabled = false;
  if (!ok) {
    $('renew-error').textContent = (body && body.error) || 'Could not change it.';
    show($('renew-error'), true);
    return;
  }
  $('renew-current').value = '';
  $('renew-new').value = '';
  location.reload();
});
$('renew-signout').addEventListener('click', async () => {
  await api('/api/logout', { method: 'POST' });
  location.reload();
});

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

// The TV app (android/, on Google TV, Android TV and Fire TV) is this same
// page, told by its user agent (SoundStormTV/). A TV has no touch and no
// mouse: everything is the remote's arrows, OK and Back. So the arrows move a
// focus between whatever can be pressed, OK presses it, OK held (or the
// remote's menu button) opens the menu a hold or a right-click opens, and
// Back is the history entry the page already keeps armed (backTarget). The
// layout is sized for across a room (html.tv in style.css). ?tv=1 turns it
// on in a browser, to try it at a desk; ?tv=0 turns it off.
const TV = (() => {
  if (/SoundStormTV\//.test(navigator.userAgent)) return true;
  const q = new URLSearchParams(location.search).get('tv');
  if (q !== null) sessionStorage.setItem('soundstorm.tv', q === '0' ? '' : '1');
  return sessionStorage.getItem('soundstorm.tv') === '1';
})();

// The Android app plays songs from the server with Android's own media
// player (PageScript's stand-in for the audio element): see NativeAudio.
const NATIVE_AUDIO = Boolean(window.soundstormApp && window.soundstormApp.nativeAudio);
// Controlling a TV's music from this phone's Now Playing (remoteLayer, near
// the end): declared here, as code that runs while the page loads asks it.
// Which device what is played goes to (Controlling, near the end).
const CONTROL = { target: null, away: null, st: null, timer: 0 };
const RA = { on: false, target: null, st: {}, stAt: 0, pwr: false, key: '', src: '', volume: 1, rate: 1,
  seekTo: null, seekAt: 0, ticker: 0, poll: 0, lastPlaying: false };

// A touch screen - not a TV, whose web view reports a coarse pointer too.
const touchScreen = () => !TV && matchMedia('(pointer: coarse)').matches;

// A person's own covers (Change cover in a song's or album's menu) win over
// the library's: a song's own first, then its album's. See withOverride.
const artPath = (item) => {
  const songKey = item.kind === 'music' ? `song:${item.sourceId}/${item.id}` : '';
  const orig = item.artId ? `/api/art/${encodeURIComponent(item.sourceId)}/${escapeId(item.artId)}` : '';
  return withOverride(orig, songKey, item.artId ? artKeyFor(item.sourceId, item.artId) : '');
};
// Covers come card-sized from the server (400px) unless asked for more, so
// opening the app does not download every album's full picture ahead of the
// song. The few big ones - Now Playing, the lock screen - ask for a size. A
// person's own cover (/api/myart) is theirs at its one size.
const bigArt = (path, px = 800) => {
  if (!path || !path.startsWith('/api/art/')) return path;
  const at = path.indexOf('#');
  const [base, frag] = at < 0 ? [path, ''] : [path.slice(0, at), path.slice(at)];
  return `${base}?size=${px}${frag}`;
};
const streamPath = (item) =>
  `/api/stream/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`;

// Streaming quality is this device's own choice - a phone on mobile data and
// the computer on the Wi-Fi want different things - so it lives here, not in
// the account. "auto" lowers it only where the browser says the connection is
// mobile data or the data saver is on, which Android's Chrome can tell and an
// iPhone cannot (there it streams the original). Downloads always keep the
// original: streamPath, not playPath.
const QUALITY_KEY = 'soundstorm.quality';
// Audiobooks are mastered loud - a voice, compressed to fill phone speakers -
// and films leave their dialogue quiet for the loud moments, so at one volume
// a book came out far louder than a film (the owner's report). A book plays
// quieter by this much, per device (Playback on this device), 8dB unless
// changed - about a film's dialogue.
const BOOK_GAIN_KEY = 'soundstorm.bookGain';
// Film quality, per device like the rest (Playback on this device): smart is
// the original picture at home and a 20 Mbit copy away from home, decided by
// the server from the address used (httpapi/filmquality.go).
const FILM_QUALITY_KEY = 'soundstorm.filmQuality';
function filmQuality() {
  return localStorage.getItem(FILM_QUALITY_KEY) || 'smart';
}
function bookGainDb() {
  const v = Number(localStorage.getItem(BOOK_GAIN_KEY));
  return localStorage.getItem(BOOK_GAIN_KEY) === null || !Number.isFinite(v) ? -8 : Math.max(-24, Math.min(0, v));
}

// slowLink: set for the rest of the session once a song sat unable to start
// at full quality (see startStream). Original quality then streams at 128
// kbps instead: a quarter of the data, and an MP3 has none of the ~600KB of
// header an iTunes M4A must deliver before its first note.
let slowLink = false;
// Known before the first song where possible: an away-from-home address found
// slow is remembered on this device for six hours, and otherwise the link is
// timed when the app opens there (probeLink). Otherwise the first song of every
// session paid for finding out - starting at full quality sends a megabyte
// that a slow link then has to drain before the quieter copy can arrive
// (measured: 20 seconds at 0.3 Mbps).
const SLOW_KEY = 'soundstorm.slowlink';
const SLOW_FOR_MS = 6 * 3600 * 1000;
const awayHost = /\.net\.soundstorm\.dev$|\.ts\.net$/i.test(location.hostname);
try {
  const kept = JSON.parse(localStorage.getItem(SLOW_KEY) || 'null');
  if (kept && kept.host === location.host && kept.until > Date.now()) slowLink = true;
} catch { /* nothing kept */ }
function rememberSlowLink() {
  slowLink = true;
  try { localStorage.setItem(SLOW_KEY, JSON.stringify({ host: location.host, until: Date.now() + SLOW_FOR_MS })); } catch { /* not kept */ }
}
// probeLink times 128KB from the server, once a session, only away from home:
// under 1.2 Mbps (about four seconds for an M4A's header) songs start at 128
// kbps from the first one. Costs about three seconds on a link that slow.
let probed = false;
async function probeLink() {
  if (probed || !awayHost || slowLink || localStorage.getItem(QUALITY_KEY) === 'original') return;
  probed = true;
  try {
    const t0 = performance.now();
    const resp = await fetch('/api/probe?b=131072', { cache: 'no-store' });
    const got = (await resp.arrayBuffer()).byteLength;
    const mbps = (got * 8) / ((performance.now() - t0) / 1000) / 1e6;
    if (resp.ok && got > 0 && mbps < 1.2) rememberSlowLink();
  } catch { /* offline: nothing to learn */ }
}

// While on the lower quality, the link is timed again every fifteen minutes
// (the same 128KB as probeLink, in the background, while the page is looked
// at): over 3 Mbps - ten times what full quality needs - and the next song is
// at full quality again, and the slow note is forgotten. A short dip in the
// Wi-Fi should not cost the rest of the evening. Nothing stalls to find out,
// and a song already playing is never changed.
const RECHECK_MS = 15 * 60 * 1000;
setInterval(async () => {
  if (!slowLink || document.hidden || state.offline) return;
  try {
    const t0 = performance.now();
    const resp = await fetch('/api/probe?b=131072', { cache: 'no-store' });
    const got = (await resp.arrayBuffer()).byteLength;
    const mbps = (got * 8) / ((performance.now() - t0) / 1000) / 1e6;
    if (resp.ok && got > 0 && mbps > 3) {
      slowLink = false;
      try { localStorage.removeItem(SLOW_KEY); } catch { /* not kept */ }
    }
  } catch { /* offline: nothing to learn */ }
}, RECHECK_MS);

function streamingKbps() {
  // "smart", the default, is original unless the link is found slow;
  // "original" is a promise - never lowered, whatever the connection (the
  // owner's asking, who would rather wait than hear less).
  const choice = localStorage.getItem(QUALITY_KEY) || 'smart';
  if (choice === 'original' || choice === 'smart') return slowLink && choice === 'smart' ? 128 : 0;
  if (slowLink && choice === 'auto') return 128;
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
    : 'Create the account for this server: you will be its owner. Choose a password of at least 12 characters - a few unrelated words make a good one.';
  $('gate-submit').textContent = hasAccount ? 'Sign in' : 'Create account';
  $('gate-form').dataset.mode = hasAccount ? 'login' : 'signup';
  // Asked for only when the address did not carry it, which is the unusual
  // case: somebody opened the page by hand instead of from setup.
  $('gate-setup-code').value = setupFromAddress;
  show($('gate-setup'), !hasAccount && setupCodeRequired && !setupFromAddress);
  // A TV may be signed in from a phone instead (tvlink.go on the server).
  show($('gate-phone'), TV && hasAccount);
  // Keeping somebody on a device is for signing in, not for the first
  // account; ticked already on a TV, which is shared by its nature.
  show($('gate-keep-row'), hasAccount);
  $('gate-keep').checked = TV;
  // On a TV the phone is the easy way in, so it is where the remote starts
  // (the owner's asking); elsewhere, typing.
  if (TV && hasAccount) $('gate-phone').focus();
  else $('gate-username').focus();
}

// Signing a TV in from a phone: the TV asks for a code and shows it, with a
// QR code of the address that allows it; a phone signed in allows it, and
// the TV, asking every three seconds, is signed in as that phone's person.
// A code lasts ten minutes; one that runs out is replaced.
let linkWait = null;
async function startLink() {
  clearInterval(linkWait);
  const { ok, body } = await api('/api/link', { method: 'POST', body: '{}' });
  if (!ok || !body || !body.id) {
    $('gate-error').textContent = (body && body.error) || 'Could not get a code; try again.';
    show($('gate-error'), true);
    stopLink();
    return;
  }
  $('gate-form').classList.add('linking');
  show($('gate-link'), true);
  show($('gate-error'), false);
  $('gate-link-code').textContent = body.code;
  $('gate-link-qr').src = `/api/link/${encodeURIComponent(body.id)}/qr.png`;
  $('gate-link-cancel').focus();
  // One poll at a time, and none for a code already replaced or cancelled
  // (two slow polls both found it run out and started two new codes). A
  // variable of its own: the interval's id is a number, and setting a
  // property on it threw, so every poll failed and an allowed TV sat on its
  // code (the owner's report).
  let busy = false;
  const mine = setInterval(async () => {
    if (busy) return;
    busy = true;
    const r = await api(`/api/link/${encodeURIComponent(body.id)}`);
    busy = false;
    if (linkWait !== mine) return;
    if (r.ok && r.body && r.body.signedIn) {
      stopLink();
      showApp(r.body.user);
    } else if (r.status === 404) {
      startLink(); // run out: a new code
    } else if (r.status === 403) {
      stopLink();
      $('gate-error').textContent = (r.body && r.body.error) || 'That sign-in was not allowed.';
      show($('gate-error'), true);
    }
  }, 3000);
  linkWait = mine;
}
function stopLink() {
  clearInterval(linkWait);
  linkWait = null;
  $('gate-form').classList.remove('linking');
  show($('gate-link'), false);
}
$('gate-phone').addEventListener('click', startLink);
$('gate-link-cancel').addEventListener('click', () => {
  stopLink();
  if (TV) $('gate-phone').focus();
  else $('gate-username').focus();
});

// On the phone: a TV's code, scanned (the QR opens /?link=CODE) or typed in
// Settings, is looked up and shown - what is asking, and that it will be
// signed in as this person - before it is allowed.
const linkFromAddress = new URLSearchParams(location.search).get('link') || '';
let linkAsking = '';
async function askLink(code) {
  const { ok, body } = await api(`/api/link/code/${encodeURIComponent(code)}`);
  if (!ok) {
    showToast((body && body.error) || 'No TV is showing that code.');
    return false;
  }
  linkAsking = body.code;
  $('link-ask-text').textContent = `${body.device} showing ${body.code} will be signed in as ${body.as}. `
    + 'Allow it only if it is a TV in front of you, signing in now.';
  show($('link-ask'), true);
  $('link-ask-yes').focus();
  return true;
}
async function answerLink(approve) {
  const code = linkAsking;
  linkAsking = '';
  show($('link-ask'), false);
  if (!code) return;
  const { ok, body } = await api(`/api/link/code/${encodeURIComponent(code)}`, {
    method: 'POST', body: JSON.stringify({ approve }),
  });
  if (!ok) showToast((body && body.error) || 'Could not answer the TV.');
  else if (approve) showToast('Allowed. The TV is signing in now.');
}
$('link-ask-yes').addEventListener('click', () => answerLink(true));
$('link-ask-no').addEventListener('click', () => answerLink(false));
$('link-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  if (await askLink($('link-code').value)) $('link-code').value = '';
});
// Scanned: asked once signed in here, and the code taken out of the address.
// The Android app hands a TV's code from a soundstorm:// link to the page
// already open.
// It says whether it took it: not signed in yet, the app loads the page with
// the code in its address instead, asked once signed in.
// Scanning the TV's code with the camera, in the Android app.
if (window.soundstormApp && window.soundstormApp.scanCode && !TV) {
  show($('link-scan'), true);
  $('link-scan').addEventListener('click', () => window.soundstormApp.scanCode());
}
window.__soundstormScanned = (what) => {
  showToast(what === 'not-ours'
    ? "That is not a TV's sign-in code. Point the camera at the QR code on the TV."
    : 'The scanner could not start. Type the code on the TV instead.');
};
window.__soundstormLink = (code) => {
  if (!state.me) return false;
  askLink(String(code));
  return true;
};

// In a phone's browser, the camera's way in: the app is where this person is
// signed in, so it is offered - Android by an intent (not installed, the
// browser simply stays here), an iPhone by the app's soundstorm:// link
// (ios/ AppLinks.swift; iOS asks first, and offers nothing without the app).
const phoneApp = /Android/i.test(navigator.userAgent) ? 'android' : /iPhone|iPad/.test(navigator.userAgent) ? 'ios' : '';
if (linkFromAddress && phoneApp && !window.soundstormApp) {
  const code = linkFromAddress.replace(/[^A-Za-z0-9]/g, '').toUpperCase();
  const plain = `soundstorm://link?server=${encodeURIComponent(location.origin)}&code=${code}`;
  const link = phoneApp === 'ios' ? plain : `intent://link?server=${encodeURIComponent(location.origin)}&code=${code}`
    + `#Intent;scheme=soundstorm;package=dev.soundstorm.app;S.browser_fallback_url=${encodeURIComponent(location.href)};end`;
  const bar = document.createElement('a');
  bar.className = 'open-in-app';
  bar.href = link;
  bar.textContent = 'Open in the SoundStorm app';
  document.body.append(bar);
}

function askLinkFromAddress() {
  if (!linkFromAddress) return;
  const params = new URLSearchParams(location.search);
  params.delete('link');
  const rest = params.toString();
  history.replaceState(null, '', location.pathname + (rest ? `?${rest}` : '') + location.hash);
  askLink(linkFromAddress);
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
      // Profiles: "keep me on this device", to be switched to later.
      ...(mode === 'login' && $('gate-keep').checked ? { keep: true } : {}),
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
  if (body && body.pending) {
    waitForApproval(body.pending, Boolean(body.code));
    return;
  }
  showApp(body && body.user);
});

// The right password on a device the account has never signed in on, with
// approval turned on: this device waits, asking every few seconds, until a
// device already signed in (or the owner) approves it - or the setup code
// from the server's .env does.
let approvalWait = null;
function waitForApproval(id, codeAllowed) {
  show($('gate-submit'), false);
  show($('gate-wait'), true);
  show($('gate-wait-code'), codeAllowed);
  const done = (message) => {
    clearInterval(approvalWait);
    approvalWait = null;
    show($('gate-wait'), false);
    show($('gate-submit'), true);
    if (message) {
      $('gate-error').textContent = message;
      show($('gate-error'), true);
    }
  };
  const settle = (ok, body, status) => {
    if (ok && body && body.signedIn) {
      done('');
      showApp(body.user);
    } else if (!ok && status !== 0 && body && body.error) {
      done(body.error);
    }
  };
  let mine = null;
  let busy = false;
  const ask = async () => {
    if (busy) return;
    busy = true;
    const r = await api(`/api/login/pending/${encodeURIComponent(id)}`);
    busy = false;
    // Cancelled while asking: not signed in after all.
    if (approvalWait !== mine) return;
    settle(r.ok, r.body, r.offline ? 0 : 1);
  };
  clearInterval(approvalWait);
  mine = setInterval(ask, 3000);
  approvalWait = mine;
  $('gate-wait-cancel').onclick = () => done('');
  $('gate-wait-code-use').onclick = async () => {
    const r = await api(`/api/login/pending/${encodeURIComponent(id)}`, {
      method: 'POST', body: JSON.stringify({ setupCode: $('gate-wait-code-input').value }),
    });
    if (!r.ok && r.body && r.body.error && /setup code/.test(r.body.error)) {
      $('gate-error').textContent = r.body.error;
      show($('gate-error'), true);
      return;
    }
    settle(r.ok, r.body, 1);
  };
}

// On devices already signed in: a new one asking to sign in to this account
// (or any account, for the owner) is offered for approval.
let deviceAsking = null;
async function checkDeviceRequests() {
  if (!state.me || document.hidden || deviceAsking) return;
  if (state.approveNewDevices === undefined) {
    // Known from the session answer; asked once for a member, whose
    // Settings never reads it.
    const r = await api('/api/session');
    state.approveNewDevices = Boolean(r.ok && r.body && r.body.approveNewDevices);
  }
  if (!state.approveNewDevices) return;
  const { ok, body } = await api('/api/devices/pending');
  const next = ok && body && Array.isArray(body.pending) ? body.pending[0] : null;
  if (!next) return;
  deviceAsking = next.id;
  const whose = next.user === state.me.name ? 'your account' : `${next.user}'s account`;
  $('device-ask-text').textContent = `${next.device} wants to sign in to ${whose}. `
    + 'Allow it only if you, or they, are signing in on it right now.';
  show($('device-ask'), true);
  // A remote can only reach what has the focus (a review: on a TV the
  // question sat there unanswerable).
  $('device-ask-yes').focus();
}
async function answerDevice(approve) {
  const id = deviceAsking;
  show($('device-ask'), false);
  const r = id ? await api(`/api/devices/pending/${encodeURIComponent(id)}`, { method: 'POST', body: JSON.stringify({ approve }) }) : { ok: true };
  deviceAsking = null;
  if (!r.ok) showToast((r.body && r.body.error) || 'Could not answer that sign-in.');
  else if (approve) showToast('Allowed. That device is signed in now.');
  setTimeout(checkDeviceRequests, 500);
}
$('device-ask-yes').addEventListener('click', () => answerDevice(true));
$('device-ask-no').addEventListener('click', () => answerDevice(false));
setInterval(checkDeviceRequests, 8000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) checkDeviceRequests(); });

$('logout').addEventListener('click', async () => {
  setAccountOpen(false);
  stopAudio();
  closeVideo();
  // Downloads are the signed-in person's; a shared device keeps nothing of
  // theirs after they sign out - nor goes on backing up their photos.
  if (BACKUP_APP) window.soundstormApp.backup('set', { enabled: false });
  await clearDownloads();
  await api('/api/logout', { method: 'POST' });
  if (state.setupTimer) clearInterval(state.setupTimer);
  clearTimeout(state.remotePoll);
  location.reload();
});

// Inside the phone apps (ios/, android/), which set window.soundstormApp, the
// app rather than the address bar decides which server this is - so Account
// offers the way back to its connect screen. Signing in stays per server.
if (window.soundstormApp) {
  show($('change-server'), true);
  $('change-server').addEventListener('click', () => {
    window.webkit.messageHandlers.soundstorm.postMessage({ type: 'changeServer' });
  });
}

/* -------------------------------------------------------------- app shell */

async function showApp(me) {
  state.me = me || null;
  // This page is a device to play on, from this person's phone (players.go).
  setTimeout(playerLoop, 1500);
  // Shown again (a switch back to the same person): one setup poll, not two.
  if (state.setupTimer) { clearInterval(state.setupTimer); state.setupTimer = null; }
  if (me && me.mustRenew) {
    showRenew();
    return;
  }
  // Downloads and kept places belong to whoever signed in on this device. If
  // somebody else signs in - the last person was signed out elsewhere, or
  // removed, without pressing Sign out here - theirs go first, or the next
  // person saw them, shelves their account cannot see included (a review).
  try {
    const owner = localStorage.getItem('soundstorm-owner');
    // Downloads nobody is recorded as owning are as good as somebody else's.
    if (me && me.id && ((owner && owner !== me.id) || (!owner && hasDownloads()))) {
      if (BACKUP_APP) window.soundstormApp.backup('set', { enabled: false });
      await clearDownloads();
    }
    if (me && me.id) localStorage.setItem('soundstorm-owner', me.id);
    localStorage.removeItem('soundstorm-locked');
  } catch {
    // no storage: nothing kept to hand on
  }
  show($('boot'), false);
  show($('gate'), false);
  show($('app'), true);
  // Away from home, time the link before anything plays (see slowLink).
  probeLink();
  // The search box is not focused on opening: on a phone that pops the
  // keyboard over the library, and it drew a highlight round the box.
  applyLibraryTabs();
  renderTabs();
  renderAccount();
  await loadFavoriteKeys();
  await Promise.all([loadPrefs(), loadMyArt()]);
  setTimeout(prepareDownloads, 20000);
  if (/\.soundstorm\.dev$/.test(location.hostname)) keepShell();
  refreshPairs();
  maybeShowHoldTip();
  askLinkFromAddress();
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
  refreshMyPhotos();
  refreshDeviceCard();
  if (BACKUP_APP) window.soundstormApp.backup('status');
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
  if (ok && body && typeof body.approveNewDevices === 'boolean') {
    state.approveNewDevices = body.approveNewDevices;
    $('new-devices-toggle').checked = body.approveNewDevices;
  }
  // Read-along's setting rides on the same answer: owner only, and only where
  // read-along is set up.
  const discovery = ok && body && typeof body.onlineDiscovery === 'boolean';
  show($('discovery-block'), discovery);
  if (discovery) $('discovery-toggle').checked = body.onlineDiscovery;
  const along = ok && body && typeof body.autoReadAlong === 'boolean';
  show($('readalong-block'), along);
  if (along) $('readalong-toggle').checked = body.autoReadAlong;
}

$('renew-everyone').addEventListener('click', async () => {
  if (!window.confirm('Ask everyone, you included, to choose a new password before they can use SoundStorm again?')) return;
  const { ok, body } = await api('/api/users/new-passwords', { method: 'POST' });
  if (!ok) {
    note($('renew-everyone-note'), (body && body.error) || 'Could not ask.', true);
    return;
  }
  // The owner is asked too, at once.
  showRenew();
});

$('new-devices-toggle').addEventListener('change', async (event) => {
  const enabled = event.target.checked;
  const { ok, body } = await api('/api/settings/new-devices', { method: 'PUT', body: JSON.stringify({ enabled }) });
  if (!ok) {
    event.target.checked = !enabled;
    note($('new-devices-note'), (body && body.error) || 'Could not change it.', true);
    return;
  }
  state.approveNewDevices = enabled;
  note($('new-devices-note'), enabled
    ? 'On. A device that has never signed in waits for approval, which shows on devices already signed in.'
    : 'Off. The right password is enough on any device.', false);
});

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
  if (state.photoLimitDefault === undefined) await loadPhotoLimitDefault();
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
    if (!person.owner) li.append(personPhotos(person));

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

// The photo space choices, in GB; -1 is no limit.
const PHOTO_LIMITS = [10, 25, 50, 100, 250, 500, 1000, 2000];
function limitLabel(gb) {
  if (gb < 0) return 'No limit';
  return gb >= 1000 ? `${gb / 1000} TB` : `${gb} GB`;
}
function limitOptions(select, current, defaultGB) {
  const opts = [];
  if (defaultGB !== undefined) opts.push(['default', `Default (${limitLabel(defaultGB)})`]);
  for (const gb of PHOTO_LIMITS) opts.push([String(gb), limitLabel(gb)]);
  opts.push(['-1', 'No limit']);
  select.replaceChildren(...opts.map(([v, label]) => {
    const o = document.createElement('option');
    o.value = v;
    o.textContent = label;
    return o;
  }));
  select.value = current;
  if (select.value !== current) {
    // A limit set some other way (the API): shown as it is.
    const o = document.createElement('option');
    o.value = current;
    o.textContent = limitLabel(Number(current));
    select.append(o);
    select.value = current;
  }
}

// personPhotos is one member's photo space: how much they use, and their
// limit, which the owner can change here.
function personPhotos(person) {
  const row = document.createElement('div');
  row.className = 'person-photos';
  const used = document.createElement('span');
  const limit = person.photoLimitBytes;
  used.textContent = `Photos: ${formatBytes(person.photoUsedBytes || 0)}`
    + (limit ? ` of ${limitLabel(Math.round(limit / 2 ** 30))}` : '');
  const select = document.createElement('select');
  select.setAttribute('aria-label', `Photo space for ${person.name}`);
  const current = person.photoLimitDefault ? 'default' : String(limit ? Math.round(limit / 2 ** 30) : -1);
  limitOptions(select, current, state.photoLimitDefault);
  select.addEventListener('change', async () => {
    const gb = select.value === 'default' ? null : Number(select.value);
    const { ok, body } = await api(`/api/users/${encodeURIComponent(person.id)}/photo-limit`,
      { method: 'PUT', body: JSON.stringify({ gb }) });
    note($('people-note'), ok ? `${person.name}'s photo space saved.` : ((body && body.error) || 'Could not save it.'), !ok);
    if (ok) loadPeople();
  });
  row.append(used, select);
  return row;
}

// The household default, at the top of People.
async function loadPhotoLimitDefault() {
  const { ok, body } = await api('/api/photos/limit-default');
  if (!ok || !body) return;
  state.photoLimitDefault = body.gb;
  const select = $('photo-limit-default');
  limitOptions(select, String(body.gb));
  select.onchange = async () => {
    const gb = Number(select.value);
    const { ok: saved } = await api('/api/photos/limit-default', { method: 'PUT', body: JSON.stringify({ gb }) });
    note($('people-note'), saved ? 'Saved. People on the default have the new space.' : 'Could not save it.', !saved);
    if (saved) {
      state.photoLimitDefault = gb;
      loadPeople();
    }
  };
}

// "Your photos": where this person's photos go and how much of their space
// they use. Only for an account with the picture shelf.
async function refreshMyPhotos() {
  const me = state.me;
  const allowed = (me && me.libraries) || [];
  if (!me || !allowed.includes('picture')) {
    show($('my-photos-block'), false);
    return;
  }
  const { ok, body } = await api('/api/photos/usage');
  if (!ok || !body) return;
  const usedText = body.usedBytes !== null && body.usedBytes !== undefined ? formatBytes(body.usedBytes) : null;
  if (body.limitBytes) {
    const k = Math.min(1, (body.usedBytes || 0) / body.limitBytes);
    $('my-photos-usage').textContent = `${usedText} of ${limitLabel(Math.round(body.limitBytes / 2 ** 30))} used.`
      + (k >= 0.9 ? ' Nearly full: ask the owner for more space, or remove some.' : '');
    $('my-photos-bar').style.width = `${Math.round(k * 100)}%`;
    $('my-photos-bar').classList.toggle('full', k >= 0.9);
  } else {
    $('my-photos-usage').textContent = me.owner ? 'Your photos have no limit.' : `${usedText || 'Nothing'} used, no limit.`;
    $('my-photos-bar').style.width = '0';
  }
  show($('my-photos-bar').parentElement, Boolean(body.limitBytes));
  // Said plainly: a member's photos are on somebody else's server.
  $('my-photos-where').textContent = me.owner
    ? `Photos you add or back up from a phone go to ${body.folder}, in folders by year and month. You see everyone's photos; each person sees only their own.`
    : `Photos you add or back up from your phone are kept in ${body.folder} on this server, in folders by year and month. The owner of the server can see them; nobody else can.`;
  // Dropping sorts by date; folders of one's own are kept only by putting
  // them in the folder on the server's drive directly.
  $('my-photos-folders').textContent = me.owner
    ? `Photos dropped here or brought in are sorted by when they were taken, and one already here is skipped, so there is no harm in adding the same ones twice. To keep folders of your own, copy them straight into ${body.folder} (or anywhere in the pictures folder) on the server's drive: they appear in Photos as they are, and are left alone - a photo that is also dropped here later is sorted by date as well, so it shows twice.`
    : `Photos dropped here or brought in are sorted by when they were taken, and one already here is skipped, so there is no harm in adding the same ones twice. To keep folders of your own, ask whoever looks after the server to copy them straight into ${body.folder} on its drive: they appear in Photos as they are, and are left alone - a photo that is also dropped here later is sorted by date as well, so it shows twice.`;
  show($('my-photos-block'), true);
}

// Phone photo backup, in the Android app (android/ PhotoBackup): the camera
// roll sent to the person's own folder, in the background. The page decides
// nothing about it; it asks the app and shows what the app says.
const BACKUP_APP = Boolean(window.soundstormApp && window.soundstormApp.photoBackup && window.soundstormApp.backup);
window.__soundstormBackup = (status) => {
  state.backup = status;
  renderBackup();
  maybeAskBackup();
};
function backupSet(options) {
  // Changing it in Settings is an answer too: not asked again after.
  try { localStorage.setItem('soundstorm.backupAsked', '1'); } catch { /* no storage */ }
  // The account it is for goes with it: the phone sends it with every photo,
  // and the server refuses photos meant for somebody else - on a shared
  // phone the next person's cookie would otherwise take the first person's
  // photos (a security review).
  if (BACKUP_APP) window.soundstormApp.backup('set', { ...options, account: (state.me && state.me.id) || '' });
}
function hasPictures() {
  return Boolean(state.me && (state.me.libraries || []).includes('picture'));
}

function renderBackup() {
  const st = state.backup;
  show($('backup-controls'), Boolean(BACKUP_APP && st && hasPictures()));
  if (!st) return;
  $('backup-on').checked = st.enabled;
  $('backup-wifi').checked = st.wifiOnly;
  $('backup-videos').checked = st.videos;
  $('backup-charging').checked = st.charging;
  show($('backup-options'), st.enabled);
  let text = '';
  if (st.enabled && !st.permission) text = 'SoundStorm needs permission to read your photos: allow it in the phone\'s settings.';
  else if (st.enabled && st.problem) text = st.problem;
  else if (st.enabled && st.total) {
    text = st.done >= st.total
      ? `All ${st.total.toLocaleString()} photos and videos are backed up.`
      : `${st.done.toLocaleString()} of ${st.total.toLocaleString()} backed up${st.running ? ', sending now' : ''}.`;
    if (st.running && st.sendingSize > 0) {
      const mb = (b) => Math.round(b / 1e6).toLocaleString();
      text += ` Sending a ${st.sendingVideo ? 'video' : 'photo'}: ${mb(st.sendingSent)} of ${mb(st.sendingSize)} MB.`;
    }
    if (st.done < st.total && !st.running) text += st.wifiOnly ? ' It carries on when the phone is on Wi-Fi.' : ' It carries on in the background.';
  } else if (st.enabled) text = 'Starting...';
  $('backup-status').textContent = text;
  show($('backup-status'), Boolean(text));
  // While it sends and somebody is looking, ask again every two seconds.
  clearTimeout(renderBackup.poll);
  if (BACKUP_APP && st.enabled && st.running) {
    renderBackup.poll = setTimeout(() => {
      if (!document.hidden && $('backup-controls').offsetParent) window.soundstormApp.backup('status');
      else renderBackup();
    }, 2000);
  }
}

// Asked once, after signing in, on a phone with the app and Pictures.
function maybeAskBackup() {
  const st = state.backup;
  // Asked unless backup is on, or this person has answered on this device -
  // the page's own record, cleared with the downloads when the person changes.
  // Not the app's "decided": switching backup off for the last person marks
  // that too, and the next person was never asked.
  if (!BACKUP_APP || !st || st.enabled || !hasPictures() || localStorage.getItem('soundstorm.backupAsked')) return;
  $('backup-ask-text').textContent = state.me && state.me.owner
    ? 'New photos and videos are sent to your server in the background, on Wi-Fi, into your own folder. Photos already on the server are not sent again. You can change this in Settings.'
    : 'New photos and videos are sent to this server in the background, on Wi-Fi, into your own folder. Photos already on the server are not sent again. The owner of the server can see them; nobody else can. You can change this in Settings.';
  show($('backup-ask'), true);
}
function answerBackup(on) {
  localStorage.setItem('soundstorm.backupAsked', '1');
  show($('backup-ask'), false);
  backupSet({ enabled: on, wifiOnly: true, videos: true, charging: false });
}
$('backup-ask-yes').addEventListener('click', () => answerBackup(true));
$('backup-ask-later').addEventListener('click', () => answerBackup(false));
$('backup-on').addEventListener('change', (e) => backupSet({ enabled: e.target.checked }));
$('backup-wifi').addEventListener('change', (e) => backupSet({ wifiOnly: e.target.checked }));
$('backup-videos').addEventListener('change', (e) => backupSet({ videos: e.target.checked }));
$('backup-charging').addEventListener('change', (e) => backupSet({ charging: e.target.checked }));
// While Settings is open the numbers move; asked every few seconds.
setInterval(() => {
  if (BACKUP_APP && !$('my-photos-block').classList.contains('hidden') && document.visibilityState === 'visible') {
    window.soundstormApp.backup('status');
  }
}, 4000);

// zipHoldsPhotos looks inside a zip without unpacking it: the central
// directory at its end lists every file. A Google Takeout or iCloud download
// says so in its folder names; any other zip counts if most of what it holds
// is photos and videos. Zip64 (anything over 4GB, as Takeout's are) keeps
// the directory's place in a second record.
async function zipHoldsPhotos(file) {
  try {
    const tailSize = Math.min(file.size, 128 << 10);
    const tail = new DataView(await file.slice(file.size - tailSize).arrayBuffer());
    let eocd = -1;
    for (let i = tail.byteLength - 22; i >= 0; i--) {
      if (tail.getUint32(i, true) === 0x06054b50) { eocd = i; break; }
    }
    if (eocd < 0) return false;
    let cdSize = tail.getUint32(eocd + 12, true);
    let cdStart = tail.getUint32(eocd + 16, true);
    if ((cdSize === 0xffffffff || cdStart === 0xffffffff) && eocd >= 20 && tail.getUint32(eocd - 20, true) === 0x07064b50) {
      const at = Number(tail.getBigUint64(eocd - 20 + 8, true));
      const z64 = new DataView(await file.slice(at, at + 56).arrayBuffer());
      if (z64.getUint32(0, true) !== 0x06064b50) return false;
      cdSize = Number(z64.getBigUint64(40, true));
      cdStart = Number(z64.getBigUint64(48, true));
    }
    // The first 4MB of the directory: thousands of names, enough to tell.
    const cd = new DataView(await file.slice(cdStart, cdStart + Math.min(cdSize, 4 << 20)).arrayBuffer());
    const names = [];
    const text = new TextDecoder();
    for (let i = 0; i + 46 <= cd.byteLength && cd.getUint32(i, true) === 0x02014b50;) {
      const n = cd.getUint16(i + 28, true), extra = cd.getUint16(i + 30, true), comment = cd.getUint16(i + 32, true);
      if (i + 46 + n > cd.byteLength) break;
      names.push(text.decode(new Uint8Array(cd.buffer, cd.byteOffset + i + 46, n)));
      i += 46 + n + extra + comment;
    }
    // A download from a service that keeps photos, by its folders: Google,
    // Apple, Facebook, Instagram, Snapchat, Flickr, Telegram, WhatsApp.
    if (names.some((n) => /(^|\/)(Google Photos|iCloud Photos|your_facebook_activity|your_instagram_activity|memories|ChatExport[^/]*)\//i.test(n)
      || /Photo Details[^/]*\.csv$|memories_history\.json$|(^|\/)photo_\d{6,}\.json$|WhatsApp Chat/i.test(n))) return true;
    const files = names.filter((n) => !n.endsWith('/'));
    const media = files.filter((n) => /\.(jpe?g|png|heic|heif|webp|gif|tiff?|avif|dng|cr2|cr3|nef|arw|raf|orf|rw2|mov|mp4|m4v|3gp)$/i.test(n));
    // Mostly photos, or plenty of them among records and pages.
    return files.length > 0 && (media.length / files.length >= 0.6 || (media.length >= 20 && media.length / files.length >= 0.3));
  } catch {
    return false;
  }
}

// Bringing a photo library in: each zip sent in 8MB pieces, carrying on from
// where the server has it if the connection drops (or the same zip is chosen
// again after a reload); the server then sorts it into the person's folder.
const IMPORT_CHUNK = 8 << 20;
const importSending = new Map(); // job id -> {sent, size}

async function importPhotos(files) {
  openImport();
  const usage = await api('/api/photos/usage');
  const left = usage.ok && usage.body && usage.body.limitBytes ? usage.body.limitBytes - (usage.body.usedBytes || 0) : Infinity;
  const total = files.reduce((n, f) => n + f.size, 0);
  if (total > left && !window.confirm(`These downloads are ${formatBytes(total)} and you have about ${formatBytes(Math.max(0, left))} of photo space left. `
    + 'Photos you already have do not count, but it may stop part way. Bring them in anyway?')) return;
  for (const file of files) sendImport(file);
}

async function sendImport(file) {
  const start = await api('/api/photos/import', { method: 'POST', body: JSON.stringify({ name: file.name, size: file.size }) });
  if (!start.ok || !start.body) {
    showToast((start.body && start.body.error) || `Could not start ${file.name}.`);
    return;
  }
  const id = start.body.id;
  let offset = start.body.received || 0;
  let wait = 1000;
  importSending.set(id, { sent: offset, size: file.size });
  refreshImports();
  while (offset < file.size) {
    const piece = await blobOf([file.slice(offset, Math.min(file.size, offset + IMPORT_CHUNK))]);
    let answer = null;
    try {
      const resp = await fetch(`/api/photos/import/${id}?offset=${offset}`, {
        method: 'PUT', credentials: 'same-origin', body: piece,
        headers: { 'Content-Type': 'application/octet-stream' },
      });
      answer = { status: resp.status, body: await resp.json().catch(() => null) };
    } catch {
      answer = null;
    }
    if (answer && (answer.status === 200 || answer.status === 409) && answer.body && typeof answer.body.received === 'number') {
      offset = answer.body.received;
      wait = 1000;
      if (answer.body.state && answer.body.state !== 'uploading') break;
    } else if (answer && answer.status >= 400 && answer.status < 500 && answer.status !== 409) {
      showToast((answer.body && answer.body.error) || `${file.name} was refused.`);
      break;
    } else {
      // The connection: try again, waiting longer each time, up to a minute.
      await new Promise((ok) => setTimeout(ok, wait));
      wait = Math.min(60000, wait * 2);
    }
    importSending.set(id, { sent: offset, size: file.size });
    renderImportProgress(id);
  }
  importSending.delete(id);
  refreshImports();
}

function openImport() {
  show($('photo-import'), true);
  refreshImports();
}

let importsTimer = 0;
async function refreshImports() {
  clearTimeout(importsTimer);
  const { ok, body } = await api('/api/photos/import');
  if (!ok || !body) return;
  const list = $('import-list');
  list.replaceChildren(...(body.imports || []).map(importRow));
  const busy = (body.imports || []).some((j) => ['uploading', 'queued', 'sorting'].includes(j.state));
  if (busy && !$('photo-import').classList.contains('hidden')) importsTimer = setTimeout(refreshImports, 3000);
  if (!busy) refreshMyPhotos();
}

function importRow(j) {
  const li = document.createElement('li');
  li.dataset.id = j.id;
  const name = document.createElement('span');
  name.className = 'import-name';
  name.textContent = j.name;
  const row = document.createElement('div');
  row.className = 'import-row';
  const text = document.createElement('span');
  text.className = 'import-text';
  text.textContent = importText(j);
  row.append(text);
  {
    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'ghost';
    remove.textContent = ['uploading', 'queued', 'sorting'].includes(j.state) ? 'Stop' : 'Clear';
    remove.addEventListener('click', async () => {
      await api(`/api/photos/import/${j.id}`, { method: 'DELETE' });
      refreshImports();
    });
    row.append(remove);
  }
  li.append(name, row);
  return li;
}

function importText(j) {
  const p = j.progress || {};
  const from = { google: 'Google Photos', apple: 'iCloud', facebook: 'Facebook', instagram: 'Instagram', snapchat: 'Snapchat', flickr: 'Flickr' }[p.source] || '';
  const counts = () => [`${(p.added || 0).toLocaleString()} added`, p.duplicates ? `${p.duplicates.toLocaleString()} already here` : '',
    p.improved ? `${p.improved.toLocaleString()} of those gave a photo you had a better date or place` : '', p.failed ? `${p.failed} could not be read` : '']
    .filter(Boolean).join(', ');
  switch (j.state) {
    case 'uploading': {
      const sending = importSending.get(j.id);
      const sent = sending ? sending.sent : j.received;
      return sending
        ? `Sending: ${formatBytes(sent)} of ${formatBytes(j.size)} (${Math.floor((sent / j.size) * 100)}%)`
        : `Half sent (${formatBytes(sent)} of ${formatBytes(j.size)}). Choose this zip again to carry on.`;
    }
    case 'queued': return 'Sent. Waiting its turn to be sorted.';
    case 'sorting': return `Sorting${from ? ` (${from})` : ''}: ${(p.done || 0).toLocaleString()} of ${(p.total || 0).toLocaleString()}. ${counts()}.`;
    case 'done': return `Done${from ? ` (${from})` : ''}: ${counts()}.`;
    case 'stopped': return p.problem ? `Stopped: ${p.problem}. ${counts()}.` : `Stopped. ${counts()}.`;
    default: return `Could not be brought in: ${p.problem || 'unreadable'}.`;
  }
}

function renderImportProgress(id) {
  const li = $('import-list').querySelector(`li[data-id="${id}"] .import-text`);
  const sending = importSending.get(id);
  if (li && sending) li.textContent = `Sending: ${formatBytes(sending.sent)} of ${formatBytes(sending.size)} (${Math.floor((sending.sent / sending.size) * 100)}%)`;
}

$('import-open').addEventListener('click', () => {
  const open = $('photo-import').classList.contains('hidden');
  show($('photo-import'), open);
  if (open) refreshImports();
});
$('import-files').addEventListener('change', (e) => {
  const files = [...e.target.files];
  e.target.value = '';
  if (files.length) importPhotos(files);
});

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
    + 'reading and listening positions are deleted. Their photos stay in '
    + `${person.photoFolder || 'their folder'}, for you to keep or delete. Your media is untouched.`)) {
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
$('film-quality-select').value = filmQuality();
$('film-quality-select').addEventListener('change', (event) => {
  localStorage.setItem(FILM_QUALITY_KEY, event.target.value);
  note($('playback-note'), 'Saved. It applies from the next film you start.', false);
});
$('book-gain-select').value = String(bookGainDb());
$('book-gain-select').addEventListener('change', (event) => {
  localStorage.setItem(BOOK_GAIN_KEY, event.target.value);
  if (audio.item && audio.item.kind === 'audiobook') applyLevel(audio.item);
  note($('playback-note'), 'Saved.', false);
});
$('quality-select').value = localStorage.getItem(QUALITY_KEY) || 'smart';
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

// In the iPhone app the app picks (FileUploads.swift): an iPhone's web view
// keeps the files its own picker gives to itself, and the app has to have
// them to send after it is left. What it picked comes back as AppFiles,
// read through the app.
$('choose-files').addEventListener('click', () => {
  if (window.soundstormApp && window.soundstormApp.pickFiles) window.soundstormApp.pickFiles();
  else $('file-picker').click();
});
window.__soundstormPicked = (list) => {
  const files = (list || []).map((f) => new AppFile(f));
  if (files.length) intake({ items: [], files });
};

// A file the iPhone app holds: name, size and date as a File's, and pieces
// of it read through the app - what the page reads is a few megabytes at
// most (a sample to spot a copy, the ends to name a different one, a zip's
// table of contents, an import's piece). blobOf turns them into real Blobs
// where one is needed.
const appReads = new Map();
let appReadNext = 1;
window.__soundstormFileData = (req, b64) => {
  const done = appReads.get(req);
  appReads.delete(req);
  if (!done) return;
  if (b64 === null) { done(null); return; }
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  done(bytes.buffer);
};
function appRead(id, from, to) {
  return new Promise((resolve, reject) => {
    const req = appReadNext++;
    appReads.set(req, (buf) => (buf ? resolve(buf) : reject(new Error('the app could not read the file'))));
    window.soundstormApp.readFile(req, id, from, to);
  });
}
class AppSlice {
  constructor(id, from, to) { this.appId = id; this.from = from; this.to = to; this.size = Math.max(0, to - from); }
  async arrayBuffer() {
    const out = new Uint8Array(this.size);
    for (let at = this.from; at < this.to; at += 8 << 20) {
      out.set(new Uint8Array(await appRead(this.appId, at, Math.min(this.to, at + (8 << 20)))), at - this.from);
    }
    return out.buffer;
  }
  slice(a = 0, b = this.size) {
    const from = this.from + Math.max(0, a < 0 ? this.size + a : a);
    const to = this.from + Math.min(this.size, b < 0 ? this.size + b : b);
    return new AppSlice(this.appId, from, Math.max(from, to));
  }
}
class AppFile extends AppSlice {
  constructor(f) {
    super(f.id, 0, Number(f.size) || 0);
    this.name = f.name;
    this.lastModified = Number(f.lastModified) || Date.now();
    this.type = '';
  }
}
async function blobOf(parts) {
  return new Blob(await Promise.all(parts.map((p) => (p instanceof AppSlice ? p.arrayBuffer() : p))));
}

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
  showHome(home);
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
    img.fetchPriority = 'low';
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

function play(item, options = {}) {
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
      // Either opens Now Playing, full screen, when started (an audiobook
      // too, at the owner's asking) - but not under a Read Along book, which
      // opens over it.
      playAudio(item);
      if ((item.kind === 'music' || item.kind === 'audiobook') && !options.underBook) openNowPlaying();
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
  // The music plays on while photos are looked at, as it does over a book
  // (the owner's asking); a clip is a film and stops it (playVideo).
  closeVideo();
  photoShown = item;
  // Controlling a TV: the photo shows there too, and stepping here steps it.
  if (CONTROL.target && !TV) controlSend({ type: 'play', item });

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
  stopLive();
  show($('photo-live'), Boolean(item.extra && item.extra.live) && !state.offline);

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

// A Live Photo's moving part, played over the still by the LIVE button and
// gone again when it ends. Quiet while music plays, which it does not stop.
function playLive() {
  if (!photoShown || !(photoShown.extra && photoShown.extra.live)) return;
  const v = $('photo-live-video');
  v.src = `/api/stream/${encodeURIComponent(photoShown.sourceId)}/${escapeId(photoShown.id + '@live')}`;
  v.muted = Boolean(audio.item) && !$('audio-player').paused;
  show(v, true);
  $('photo-live').classList.add('on');
  v.play().catch(stopLive);
}
function stopLive() {
  const v = $('photo-live-video');
  v.pause();
  v.removeAttribute('src');
  v.load();
  show(v, false);
  $('photo-live').classList.remove('on');
}
$('photo-live').addEventListener('click', (event) => {
  event.stopPropagation();
  if ($('photo-live-video').classList.contains('hidden')) playLive();
  else stopLive();
});
$('photo-live-video').addEventListener('ended', stopLive);

function closePhoto() {
  stopLive();
  if (photoShown && CONTROL.target && !TV) controlSend({ type: 'control', action: 'closephoto' });
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
  saveBookFile(item);
}

// saveBookFile hands the book's file to the browser to save. It shared a name
// with downloadBook further down (which keeps a book in the offline cache),
// and the later one won for every caller: the reader's Download button did
// nothing anyone could see (a review).
function saveBookFile(item) {
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
window.soundstormDownloadBook = saveBookFile;

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
  // Controlling a TV: the film plays there, and this phone is its remote.
  if (CONTROL.target && !TV) {
    if (RA.on) dropMirror();
    controlSend({ type: 'play', item });
    openRemote(CONTROL.target);
    return;
  }
  stopAudio();
  detachHls();
  hideUpNext();
  // Which play this is: closing the film, or starting another, while this
  // one waits on the server makes its answer stale - it used to start the
  // film playing behind a closed player, or the older of two (a review).
  const token = state.videoToken = (state.videoToken || 0) + 1;
  const current = () => token === state.videoToken;
  const player = $('video-player');
  // The last film's source goes first, or a saved place arriving early was
  // applied to the film going out, and the new one started at 0:00.
  player.removeAttribute('src');
  player.load();
  startWatching(item);

  $('video-caption').textContent = [item.title, subtitleFor(item)]
    .filter(Boolean)
    .join(' — ');
  show($('video-overlay'), true);
  show($('video-cast'), !TV && !state.offline && !isDownloaded(item));

  // Downloaded: from the device, connection or not - a kept copy, with no
  // quality to choose.
  show($('quality-picker'), false);
  if (isDownloaded(item) && (await playKeptVideo(item, player))) return;
  if (!current()) return;

  // Ask before building a player: the answer decides which one to build.
  // The quality is this device's setting unless one was picked in the
  // player, which holds until the player closes.
  const vq = state.videoQuality || filmQuality();
  state.videoAudio = options.audio;
  attachQualityChoice(item, vq);
  const audioQuery = `?vq=${vq}` + (options.audio !== undefined ? `&audio=${options.audio}` : '');
  const { ok, body } = await api(
    `/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}${audioQuery}`);
  if (!current()) return;
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
    if (!current()) return;
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
  state.videoToken = (state.videoToken || 0) + 1;
  // A quality picked in the player was for this sitting only.
  state.videoQuality = null;
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
  // Controlling a TV: the song or book plays there, this page its remote.
  if (CONTROL.target && !RA.on && item && (item.kind === 'music' || item.kind === 'audiobook')) {
    controlAttach(CONTROL.target);
  }
  audio.counted = false;
  if (item && item.kind === 'music' && state.scrobbling && !state.offline && !RA.on) {
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
  audio.chapters = [];
  audio.captionChapter = -1;
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
  saveNativeQueue();

  // A song starts now: a round trip before the first note is felt, and nothing
  // about a four minute track needs the answer. An audiobook waits, because it
  // may be resuming into chapter twelve, and starting chapter one first would
  // play a second of the wrong thing before correcting itself.
  if (item.kind !== 'audiobook') {
    const handoff = takeCrossfade(item);
    const preloaded = handoff ? null : takePreloaded(item);
    // A new song clears what the native player had queued after the last,
    // and in the app the songs after it are handed over a few seconds in, not
    // thirty seconds from its end: if Android ends the page early in a song,
    // the player still has them.
    audio.nativeQueued = '';
    if (NATIVE_AUDIO && item.kind === 'music') setTimeout(() => { if (audio.item === item) preloadNext(); }, 4000);
    if (!handoff && !preloaded) stopPreloading();
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
      startStream(item);
    }
  }
  applyLevel(item);

  loadPlayback(item);
}

// startStream plays a song from the server, and watches that it starts.
//
// Reported from a phone away from home, over a slow link (measured: 0.3 Mbps
// from the house's upload): songs sat at 0:00 for a minute or more. An iTunes
// M4A carries ~600KB before its first note - the index, the album art and
// padding - and at that speed that is 15-20 seconds before anything else is
// fetched. So a song still unable to play after six seconds is started again
// at 128 kbps, and every song after it for the rest of the session, with a
// word to say why. Not for a song already at a lower quality, a downloaded
// one, or a blob in memory, which never wait on the network.
const SLOW_START_MS = 6000;
function startStream(item) {
  startAt(playPath(item), 0);
  if (item.kind !== 'music' || slowLink || streamingKbps() !== 0 || localStorage.getItem(QUALITY_KEY) === 'original') return;
  const player = $('audio-player');
  setTimeout(() => {
    if (audio.item !== item || slowLink || player.paused) return;
    if (player.readyState >= 2) return; // it has started, or can
    rememberSlowLink();
    showToast('Slow connection - streaming at a lower quality for now.');
    // From where it is, not the top: a jump made while it was starting
    // was thrown away.
    startAt(playPath(item), player.currentTime || 0);
  }, SLOW_START_MS);
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

  // The table of contents: the book's own chapters (marks inside one file as
  // well as one file each), else its files when there are several.
  audio.chapters = Array.isArray(info.chapters) && info.chapters.length > 1 ? info.chapters
    : audio.tracks.length > 1 ? audio.tracks.map((t) => ({ title: t.title, startSeconds: t.startSeconds || 0 })) : [];
  renderDockButtons();
  audio.captionChapter = -1;
  updateTrackCaption();
  updateMediaSession();
  renderNowPlaying();

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

  // Taking over a song the app's player has paused leaves it paused.
  const begin = () => {
    if (audio.adoptPaused) { audio.adoptPaused = false; return; }
    player.play().catch(() => {});
  };
  // An earlier jump still waiting for its file is let go: started again
  // before the metadata came, it fired on the next file and moved that to
  // the old place (a review).
  if (audio.pendingSeek) player.removeEventListener('loadedmetadata', audio.pendingSeek);
  audio.pendingSeek = null;
  if (offset > 0) {
    audio.pendingSeek = () => {
      audio.pendingSeek = null;
      // Landing exactly on the end would fire 'ended' and skip the chapter.
      player.currentTime = Math.min(offset, Math.max(0, player.duration - 1));
      begin();
    };
    player.addEventListener('loadedmetadata', audio.pendingSeek, { once: true });
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
  // A book's chapter list, when it has one, names the place better than its
  // files do (a single m4b is one file and many chapters).
  if (updateChapterCaption()) return;
  const track = audio.tracks[audio.index];
  if (!track) return;
  $('audio-sub').textContent =
    `${audio.index + 1} of ${audio.tracks.length} · ${track.title}`;
}

// updateChapterCaption: the mini-player's second line (and the lock screen's
// title) is the chapter being listened to - "3 of 12 · its title" - kept up
// as it plays from one chapter into the next. True when a book has chapters.
function updateChapterCaption() {
  const list = audio.chapters || [];
  if (!audio.item || audio.item.kind === 'music' || list.length < 2) return false;
  const i = chapterAt(elapsed());
  if (i === audio.captionChapter) return true;
  audio.captionChapter = i;
  const title = (list[i] && list[i].title) || `Chapter ${i + 1}`;
  $('audio-sub').textContent = `${i + 1} of ${list.length} \u00B7 ${title}`;
  updateMediaSession();
  return true;
}
$('audio-player').addEventListener('timeupdate', () => updateChapterCaption());

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
  // Playing on a TV: the TV keeps the place.
  if (RA.on) return;

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
    // Should the native player have moved on regardless, it stops.
    if (NATIVE_AUDIO) $('audio-player').pause();
    sleep.atSongEnd = false;
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
  audio.chapters = [];
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
  $('intake-review').replaceChildren();
  filesToggle(false);
  $('intake-title').textContent = 'Reading what you dropped…';

  let dropped = await collectFiles(dataTransfer);
  if (!dropped.length) {
    $('intake-title').textContent = 'Nothing usable was dropped.';
    return;
  }
  // A zip is looked inside (its table of contents, at its end - a moment
  // even for 50GB): a photo download from Google or Apple, or a zip that is
  // mostly photos and videos, is brought in to the person's own folder;
  // any other zip is not unpacked, and says so. Everything else goes on as
  // usual, in the same drop.
  const zips = dropped.filter((d) => /\.zip$/i.test(d.file.name));
  if (zips.length) {
    dropped = dropped.filter((d) => !/\.zip$/i.test(d.file.name));
    const photos = [];
    const other = [];
    for (const z of zips) ((await zipHoldsPhotos(z.file)) && hasPictures() ? photos : other).push(z.file);
    if (photos.length) importPhotos(photos);
    if (other.length) {
      showToast(other.length === 1
        ? `${other[0].name} is not a photo download, and SoundStorm does not unpack other zips: unzip it and drop what is inside.`
        : `${other.length} zips are not photo downloads, and SoundStorm does not unpack other zips: unzip them and drop what is inside.`);
    }
    if (!dropped.length) {
      show($('intake'), false);
      return;
    }
  }

  const paths = dropped.map((d) => d.path);
  const choices = {};
  // What the review remembers between plans: the questions asked (they stay
  // on screen, answered, to change), what was left out, and the choices made
  // about names already taken.
  const review = { choices, asked: new Map(), excluded: new Set(), open: new Set(), memo: new Map(), skipFiles: new Set() };

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

    // One screen (the owner's design): what SoundStorm sorted itself, and
    // what it could not tell - grouped where alike, answered for all or one
    // by one, any part changed or left out - and nothing moves until Add.
    const questions = body.questions || [];
    for (const q of questions) if (!review.asked.has(q.group)) review.asked.set(q.group, q);
    const files = body.files || [];
    $('intake-title').textContent = 'Checking what is already there\u2026';
    await checkTaken(files, dropped);
    for (const p of files) {
      const kept = review.memo.get(p.path);
      if (kept && p.conflict) Object.assign(p, kept);
    }
    let decision;
    do {
      decision = await reviewPlan(files, review);
      // Choices about names already taken outlive the plan made again.
      if (decision) for (const p of files) if (p.conflict) review.memo.set(p.path, { conflict: p.conflict, asName: p.asName });
    } while (decision && decision.redraw);
    if (decision === null) {
      $('intake-title').textContent = 'Canceled \u2014 nothing was added.';
      $('intake-review').replaceChildren();
      return;
    }
    if (decision.replan) continue;
    if (decision.change) {
      choices[decision.change.group] = decision.change.kind;
      continue;
    }
    // Left out: not sent.
    for (const p of files) if (review.excluded.has(p.group || p.path) || review.skipFiles.has(p.path)) { p.skipped = true; p.reason = 'not added'; }
    await sendFiles(files, dropped);
    return;
  }
}

// What a shelf is called in the drop panel.
const shelfName = (kind) => (kind === 'picture' ? 'Photos' : LIBRARY_NAMES[kind] || kind);
// The shelves this account may add to: the hidden chips say which it has.
function shelvesAllowed() {
  return Object.keys(LIBRARY_NAMES).filter((k) => {
    const chip = document.querySelector(`#filters .chip[data-kind="${k}"]`);
    return chip && !chip.classList.contains('hidden');
  });
}
// A part's name: the folder, or the last few of a deep one.
function partLabel(group) {
  // One file given its own shelf: its name.
  if (group.startsWith('file:')) return group.slice(5).split('/').pop();
  const segs = group.split('/');
  return segs.length > 2 ? `…/${segs.slice(-2).join('/')}` : group;
}

// The plan in parts: each part of the drop (a folder decided on its own, or a
// loose file), where it is going and how many files.
// A taken name, before anything is sent (library/conflict.go): the server
// says which planned files would land on a name already used, and what is
// there; an exact copy (same length and Sample) is skipped here, never sent,
// and a different file with the name is a conflict for the review to ask
// about - keep both, skip, or (the owner) replace.
const SAMPLE_CHUNK = 1 << 20;
const sampleCache = new WeakMap();
async function fileSample(file) {
  if (sampleCache.has(file)) return sampleCache.get(file);
  const size = file.size;
  const head = new Uint8Array(8);
  new DataView(head.buffer).setBigUint64(0, BigInt(size));
  const parts = [head];
  if (size <= 3 * SAMPLE_CHUNK) parts.push(file);
  else for (const from of [0, Math.floor(size / 2) - SAMPLE_CHUNK / 2, size - SAMPLE_CHUNK]) parts.push(file.slice(from, from + SAMPLE_CHUNK));
  const bytes = new Uint8Array(await (await blobOf(parts)).arrayBuffer());
  const digest = window.crypto && crypto.subtle ? new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)) : sha256(bytes);
  const hex = [...digest].map((b) => b.toString(16).padStart(2, '0')).join('');
  sampleCache.set(file, hex);
  return hex;
}
// SHA-256 for a page on plain http, where the browser offers no crypto.subtle.
function sha256(data) {
  const K = new Uint32Array([0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
    0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb,
    0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f,
    0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2]);
  const H = new Uint32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);
  const len = data.length;
  const padded = new Uint8Array(((len + 9 + 63) >> 6) << 6);
  padded.set(data);
  padded[len] = 0x80;
  const view = new DataView(padded.buffer);
  view.setUint32(padded.length - 4, len * 8);
  view.setUint32(padded.length - 8, Math.floor(len / 0x20000000));
  const w = new Uint32Array(64);
  const r = (x, n) => (x >>> n) | (x << (32 - n));
  for (let off = 0; off < padded.length; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(off + i * 4);
    for (let i = 16; i < 64; i++) {
      const s0 = r(w[i - 15], 7) ^ r(w[i - 15], 18) ^ (w[i - 15] >>> 3);
      const s1 = r(w[i - 2], 17) ^ r(w[i - 2], 19) ^ (w[i - 2] >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
    }
    let [a, b, c, d, e, f, g, h] = H;
    for (let i = 0; i < 64; i++) {
      const t1 = (h + (r(e, 6) ^ r(e, 11) ^ r(e, 25)) + ((e & f) ^ (~e & g)) + K[i] + w[i]) >>> 0;
      const t2 = ((r(a, 2) ^ r(a, 13) ^ r(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))) >>> 0;
      h = g; g = f; f = e; e = (d + t1) >>> 0; d = c; c = b; b = a; a = (t1 + t2) >>> 0;
    }
    H[0] += a; H[1] += b; H[2] += c; H[3] += d; H[4] += e; H[5] += f; H[6] += g; H[7] += h;
  }
  const out = new Uint8Array(32);
  const ov = new DataView(out.buffer);
  H.forEach((v, i) => ov.setUint32(i * 4, v));
  return out;
}
async function checkTaken(files, dropped) {
  const asked = [];
  files.forEach((p, i) => {
    if (!p.skipped && !p.waiting && p.kind && dropped[i]) asked.push({ p, file: dropped[i].file });
  });
  if (!asked.length) return;
  const { ok, body } = await api('/api/upload/check', {
    method: 'POST',
    body: JSON.stringify({ files: asked.map(({ p, file }) => ({ dest: p.dest || '', kind: p.kind, size: file.size })) }),
  });
  if (!ok || !body || !Array.isArray(body.files)) return;
  for (let i = 0; i < asked.length; i++) {
    const { p, file } = asked[i];
    const there = body.files[i] || {};
    if (!there.taken) continue;
    if (p.kind === 'picture') {
      // A photo: only an exact copy anywhere in the person's folders counts.
      if ((there.samples || []).length && there.samples.includes(await fileSample(file))) {
        p.skipped = true;
        p.reason = 'already in your photos';
      }
      continue;
    }
    if (there.size === file.size && there.sample && there.sample === (await fileSample(file))) {
      p.skipped = true;
      p.reason = 'already in your library';
      continue;
    }
    if (!p.conflict) p.conflict = 'keep';
    if (p.asName === undefined) p.asName = await suggestName(p, file);
  }
}

// The name a different file of a taken name would get - for what makes it
// different: its picture size, quality, length or narrator - worked out by
// the server from the file's two ends, shown in the question to keep or
// change (library/distinct.go).
async function suggestName(p, file) {
  const MB = 1 << 20;
  const head = file.slice(0, Math.min(file.size, MB));
  const tail = file.slice(Math.max(head.size, file.size - MB));
  const q = new URLSearchParams({ kind: p.kind, dest: p.dest || '', size: String(file.size), head: String(head.size) });
  try {
    const resp = await fetch(`/api/upload/describe?${q}`, {
      method: 'POST', credentials: 'same-origin', body: await blobOf([head, tail]),
    });
    if (!resp.ok) return '';
    const body = await resp.json();
    return (body && body.name) || '';
  } catch {
    return '';
  }
}

function planParts(files) {
  const parts = new Map();
  let skipped = 0;
  for (const p of files) {
    if (p.skipped || p.conflict === 'skip') { skipped++; continue; }
    const key = p.group || p.path;
    if (!parts.has(key)) parts.set(key, { group: key, kinds: {}, count: 0 });
    const part = parts.get(key);
    part.count++;
    part.kinds[p.kind] = (part.kinds[p.kind] || 0) + 1;
  }
  for (const part of parts.values()) {
    part.kind = Object.keys(part.kinds).sort((a, b) => part.kinds[b] - part.kinds[a])[0];
  }
  return { parts: [...parts.values()], skipped };
}

function filesToggle(on) {
  const button = $('intake-files-toggle');
  show(button, on);
  show($('intake-list'), false);
  button.textContent = 'Show every file';
}
$('intake-files-toggle').addEventListener('click', () => {
  const list = $('intake-list');
  const open = list.classList.contains('hidden');
  show(list, open);
  $('intake-files-toggle').textContent = open ? 'Hide the files' : 'Show every file';
});

function intakeLine(label, rest, extra) {
  const li = document.createElement('li');
  const name = document.createElement('strong');
  name.textContent = label;
  name.title = label;
  const text = document.createElement('span');
  text.className = 'intake-part-dest';
  text.textContent = rest;
  li.append(name, text);
  if (extra) li.append(extra);
  return li;
}

// reviewPlan shows where each part is going and waits: Add sends the files,
// Change re-plans one part to another shelf, Cancel stops.
function reviewPlan(files, review) {
  return new Promise((resolve) => {
    const host = $('intake-review');
    host.replaceChildren();
    $('intake-questions').replaceChildren();
    $('intake-list').replaceChildren();
    files.forEach(renderIntakeRow);
    const done = (d) => { host.replaceChildren(); resolve(d); };
    const groupOf = (p) => p.group || p.path;
    const excluded = review.excluded;
    const asked = review.asked;

    // What will go: not skipped, not waiting on a choice, not left out.
    const going = files.filter((p) => !p.skipped && !p.waiting && p.conflict !== 'skip' && !excluded.has(groupOf(p)) && !review.skipFiles.has(p.path));
    const adding = going.length;
    const outstanding = [...asked.keys()].filter((g) => !review.choices[g] && !excluded.has(g));

    // Needs your choice: what SoundStorm could not tell, alike ones together.
    if (asked.size) host.append(askedBox(files, review, done));

    // What else name already taken.
    const conflicts = files.filter((p) => p.conflict && !excluded.has(groupOf(p)));
    if (conflicts.length) host.append(conflictChoice(conflicts, () => done({ redraw: true })));

    // Ready: what SoundStorm sorted itself, each part to change or leave out.
    const ready = files.filter((p) => !asked.has(groupOf(p)) && !excluded.has(groupOf(p)));
    const { parts, skipped } = planParts(ready);
    const list = document.createElement('ul');
    list.className = 'intake-parts';
    for (const part of parts) {
      const change = document.createElement('button');
      change.type = 'button';
      change.className = 'ghost small';
      change.textContent = 'Change';
      const li = intakeLine(partLabel(part.group),
        `\u2192 ${shelfName(part.kind)} \u00B7 ${part.count} file${part.count === 1 ? '' : 's'}`, change);
      change.addEventListener('click', () => {
        const options = document.createElement('span');
        options.className = 'question-options';
        for (const kind of shelvesAllowed().filter((k) => k !== part.kind)) {
          const b = document.createElement('button');
          b.type = 'button';
          b.textContent = shelfName(kind);
          b.addEventListener('click', () => done({ change: { group: part.group, kind } }));
          options.append(b);
        }
        if (part.group.startsWith('file:')) {
          const back = document.createElement('button');
          back.type = 'button';
          back.className = 'ghost';
          back.textContent = 'Back with its folder';
          back.addEventListener('click', () => { delete review.choices[part.group]; done({ replan: true }); });
          options.append(back);
        }
        const leave = document.createElement('button');
        leave.type = 'button';
        leave.className = 'ghost';
        leave.textContent = "Don't add";
        leave.addEventListener('click', () => { excluded.add(part.group); done({ redraw: true }); });
        options.append(leave);
        change.replaceWith(options);
        options.querySelector('button')?.focus();
      });
      // The files in it, each with a choice of its own - for the rare one
      // that does not belong with the rest.
      if (part.count > 1 || !part.group.startsWith('file:')) {
        const [peek, names] = fileChoices(files.filter((p) => groupOf(p) === part.group), review, done, part.kind);
        li.append(peek, names);
      }
      list.append(li);
    }
    // Copies already there (checkTaken) said as such; anything else skipped
    // keeps its reason under every file.
    const there = ready.filter((p) => p.skipped && /^already in your/.test(p.reason || '')).length;
    if (there) list.append(intakeLine(`${there} already in your library`, 'not sent again'));
    if (skipped - there > 0) list.append(intakeLine(`${skipped - there} skipped`, 'see every file for why'));
    if (list.children.length) {
      if (asked.size) host.append(intakeHeading('Ready'));
      host.append(list);
    }

    // Left out: each to put back.
    const left = [...excluded];
    if (left.length) {
      host.append(intakeHeading('Not adding'));
      const ul = document.createElement('ul');
      ul.className = 'intake-parts intake-left';
      for (const g of left) {
        const back = document.createElement('button');
        back.type = 'button';
        back.className = 'ghost small';
        back.textContent = 'Add back';
        back.addEventListener('click', () => { excluded.delete(g); done(asked.has(g) ? { replan: true } : { redraw: true }); });
        const n = files.filter((p) => groupOf(p) === g).length;
        ul.append(intakeLine(partLabel(g), `${n} file${n === 1 ? '' : 's'} left out`, back));
      }
      host.append(ul);
    }
    if (review.skipFiles.size) {
      if (!left.length) host.append(intakeHeading('Not adding'));
      const ul = document.createElement('ul');
      ul.className = 'intake-parts intake-left';
      for (const path of review.skipFiles) {
        const back = document.createElement('button');
        back.type = 'button';
        back.className = 'ghost small';
        back.textContent = 'Add back';
        back.addEventListener('click', () => { review.skipFiles.delete(path); done({ redraw: true }); });
        ul.append(intakeLine(path.split('/').pop(), 'left out', back));
      }
      host.append(ul);
    }

    $('intake-title').textContent = outstanding.length
      ? `${outstanding.length === 1 ? 'One thing needs' : `${outstanding.length} things need`} your choice before adding`
      : adding ? `Ready to add ${adding} file${adding === 1 ? '' : 's'} \u2014 check where they go`
        : 'Nothing here will be added.';

    const actions = document.createElement('div');
    actions.className = 'intake-actions';
    const add = document.createElement('button');
    add.type = 'button';
    add.textContent = outstanding.length ? `Choose for ${outstanding.length === 1 ? 'the one' : `the ${outstanding.length}`} above first`
      : `Add ${adding} file${adding === 1 ? '' : 's'}`;
    add.disabled = Boolean(outstanding.length) || !adding;
    add.addEventListener('click', () => done('send'));
    actions.append(add);
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'ghost';
    cancel.textContent = 'Cancel';
    cancel.addEventListener('click', () => resolve(null));
    actions.append(cancel);
    host.append(actions);
    filesToggle(files.length > 0);
  });
}

function intakeHeading(text) {
  const h = document.createElement('h3');
  h.className = 'intake-heading';
  h.textContent = text;
  return h;
}

// What SoundStorm could not tell, alike questions together: a choice for all
// of them, or Choose each opening a line per folder with its own choice and
// its files to look at; a choice made stays here to change until Add.
const ASK_TITLES = {
  'audiobook,music': (n) => (n === 1 ? 'Music or an audiobook?' : `${n} folders of MP3s \u2014 music or audiobooks?`),
  'tv,video': (n) => (n === 1 ? 'A film or TV?' : `${n} folders of videos without episode numbers \u2014 films or TV?`),
  'document,ebook': (n) => (n === 1 ? 'A book or a document?' : `${n} PDFs \u2014 books or documents?`),
};
function askedBox(files, review, done) {
  const box = document.createElement('div');
  box.className = 'intake-asked';
  box.append(intakeHeading('Needs your choice'));
  const clusters = new Map();
  for (const q of review.asked.values()) {
    const key = [...q.options].sort().join(',');
    if (!clusters.has(key)) clusters.set(key, []);
    clusters.get(key).push(q);
  }
  const groupOf = (p) => p.group || p.path;
  const filesOf = (g) => files.filter((p) => groupOf(p) === g);
  const choiceOf = (g) => (review.excluded.has(g) ? 'skip' : review.choices[g] || '');
  for (const [key, qs] of clusters) {
    const options = qs[0].options;
    const card = document.createElement('div');
    card.className = 'intake-ask';
    const title = document.createElement('p');
    const strong = document.createElement('strong');
    strong.textContent = (ASK_TITLES[key] || ((n) => `${n} to sort \u2014 ${options.map(shelfName).join(' or ')}?`))(qs.length);
    title.append(strong);
    if (qs.length === 1) {
      const n = filesOf(qs[0].group).length || qs[0].count;
      title.append(document.createTextNode(` ${partLabel(qs[0].group)} \u00B7 ${n} file${n === 1 ? '' : 's'}`));
    } else {
      title.append(document.createTextNode(' ' + qs.slice(0, 3).map((q) => partLabel(q.group).split('/').pop()).join(', ') + (qs.length > 3 ? ', \u2026' : '')));
    }
    card.append(title);
    // The same choice for all of them; "mixed" when they differ.
    const chosen = new Set(qs.map((q) => choiceOf(q.group)));
    const all = chosen.size === 1 ? [...chosen][0] : 'mixed';
    const row = document.createElement('div');
    row.className = 'intake-conflict-choice';
    const choose = (value) => {
      for (const q of qs) setAsked(review, q.group, value);
      done({ replan: true });
    };
    for (const kind of options) {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = all === kind ? '' : 'ghost';
      b.setAttribute('aria-pressed', String(all === kind));
      b.textContent = shelfName(kind);
      b.addEventListener('click', () => choose(kind));
      row.append(b);
    }
    const skip = document.createElement('button');
    skip.type = 'button';
    skip.className = all === 'skip' ? '' : 'ghost';
    skip.textContent = qs.length === 1 ? "Don't add" : "Don't add these";
    skip.addEventListener('click', () => choose('skip'));
    row.append(skip);
    card.append(row);
    if (all === 'mixed') {
      const note = document.createElement('p');
      note.className = 'muted small-print';
      note.textContent = 'Mixed \u2014 chosen folder by folder below.';
      card.append(note);
    }
    // One by one, each with its files to look at.
    const open = qs.length === 1 || review.open.has(key);
    if (qs.length > 1) {
      const each = document.createElement('button');
      each.type = 'button';
      each.className = 'ghost small';
      each.textContent = open ? 'Hide the list' : 'Choose each\u2026';
      each.addEventListener('click', () => {
        if (review.open.has(key)) review.open.delete(key);
        else review.open.add(key);
        done({ redraw: true });
      });
      card.append(each);
    }
    if (open) {
      const list = document.createElement('ul');
      list.className = 'intake-ask-list';
      for (const q of qs) {
        const li = document.createElement('li');
        const name = document.createElement('span');
        const its = filesOf(q.group);
        // The folder's own name: a long path was cut off just where folders differ.
        name.textContent = `${partLabel(q.group).split('/').pop()} \u00B7 ${its.length || q.count} file${(its.length || q.count) === 1 ? '' : 's'}`;
        name.title = partLabel(q.group);
        const select = document.createElement('select');
        const none = document.createElement('option');
        none.value = '';
        none.textContent = 'Choose\u2026';
        select.append(none);
        for (const kind of options) {
          const o = document.createElement('option');
          o.value = kind;
          o.textContent = shelfName(kind);
          select.append(o);
        }
        const skipOne = document.createElement('option');
        skipOne.value = 'skip';
        skipOne.textContent = "Don't add";
        select.append(skipOne);
        select.value = choiceOf(q.group);
        select.addEventListener('change', () => {
          setAsked(review, q.group, select.value);
          done({ replan: true });
        });
        const [peek, names] = fileChoices(its, review, done, review.choices[q.group]);
        // A question about one folder has its choice above: here only its files.
        if (qs.length === 1) li.append(peek, names);
        else li.append(name, select, peek, names);
        if (qs.length === 1) li.className = 'intake-ask-one';
        list.append(li);
      }
      card.append(list);
    }
    box.append(card);
  }
  return box;
}
// fileChoices is a part's Files button and its list: every file, each with
// a choice of its own - the shelf the rest of the folder goes to (the
// usual), another shelf, or left out. A file given another shelf leaves the folder (its companions with
// it: library.Plan) and shows as its own line, with a way back.
function fileChoices(its, review, done, restKind) {
  const peek = document.createElement('button');
  peek.type = 'button';
  peek.className = 'ghost small intake-peek';
  peek.textContent = `Files (${its.length})`;
  const names = document.createElement('ul');
  names.className = 'intake-peek-list';
  names.hidden = !its.some((p) => review.peeked && review.peeked.has(p.group || p.path));
  for (const p of its.slice(0, 200)) {
    const f = document.createElement('li');
    const name = document.createElement('span');
    name.textContent = p.path.split('/').slice(-2).join('/');
    name.title = p.path;
    f.append(name);
    if (!p.skipped || review.skipFiles.has(p.path)) {
      const select = document.createElement('select');
      select.setAttribute('aria-label', `Where ${p.path.split('/').pop()} goes`);
      const add = (value, label) => {
        const o = document.createElement('option');
        o.value = value;
        o.textContent = label;
        select.append(o);
      };
      // Said as the shelf the rest of the folder goes to: "with the folder"
      // read as staying in that folder in the library (the owner's point).
      add('', restKind ? `Same as the rest (${shelfName(restKind)})` : 'Same as the rest');
      for (const kind of shelvesAllowed()) if (kind !== restKind) add(kind, shelfName(kind));
      add('skip', "Don't add");
      select.value = review.skipFiles.has(p.path) ? 'skip' : review.choices['file:' + p.path] || '';
      select.addEventListener('change', () => {
        review.peeked = review.peeked || new Set();
        review.peeked.add(p.group || p.path);
        const key = 'file:' + p.path;
        if (select.value === 'skip') {
          review.skipFiles.add(p.path);
          done({ redraw: true });
          return;
        }
        review.skipFiles.delete(p.path);
        if (select.value) review.choices[key] = select.value;
        else delete review.choices[key];
        done({ replan: true });
      });
      f.append(select);
    } else {
      const why = document.createElement('span');
      why.className = 'muted';
      why.textContent = p.reason || 'skipped';
      f.append(why);
    }
    names.append(f);
  }
  if (its.length > 200) {
    const more = document.createElement('li');
    more.textContent = `\u2026and ${its.length - 200} more`;
    names.append(more);
  }
  peek.addEventListener('click', () => {
    names.hidden = !names.hidden;
    review.peeked = review.peeked || new Set();
    const g = its[0] && (its[0].group || its[0].path);
    if (names.hidden) review.peeked.delete(g);
    else review.peeked.add(g);
  });
  return [peek, names];
}

// setAsked records one answer: a shelf, left out, or not chosen yet.
function setAsked(review, group, value) {
  if (value === 'skip') {
    review.excluded.add(group);
    delete review.choices[group];
  } else {
    review.excluded.delete(group);
    if (value) review.choices[group] = value;
    else delete review.choices[group];
  }
}

// The question for files whose name is taken by a different file: one
// choice for all of them, or one each (the owner's design, after Windows'
// copy dialog). Keep both is chosen until changed - nothing is lost.
function conflictChoice(conflicts, changed) {
  const box = document.createElement('div');
  box.className = 'intake-conflicts';
  const text = document.createElement('p');
  const strong = document.createElement('strong');
  strong.textContent = conflicts.length === 1
    ? 'A file has the same name as something already in your library, but it is different.'
    : `${conflicts.length} files have the same name as something already in your library, but they are different.`;
  text.append(strong);
  const options = [['keep', 'Keep both'], ['skip', 'Skip']];
  if (state.me && state.me.owner) options.push(['replace', 'Replace']);
  const all = conflicts.every((p) => p.conflict === conflicts[0].conflict) ? conflicts[0].conflict : '';
  const row = document.createElement('div');
  row.className = 'intake-conflict-choice';
  for (const [value, label] of options) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = value === all ? '' : 'ghost';
    b.textContent = label;
    b.setAttribute('aria-pressed', String(value === all));
    b.addEventListener('click', () => {
      conflicts.forEach((p) => { p.conflict = value; });
      changed();
    });
    row.append(b);
  }
  const note = document.createElement('p');
  note.className = 'muted small-print';
  note.textContent = all === 'replace' ? 'The ones there now go to the bin for 30 days.'
    : all === 'keep' ? 'The new one is named for what makes it different - its quality, length or narrator - which you can change under Choose for each file.'
      : all === 'skip' ? 'What you have stays as it is.' : 'Chosen file by file.';
  const each = document.createElement('button');
  each.type = 'button';
  each.className = 'ghost small';
  each.textContent = 'Choose for each file\u2026';
  const list = document.createElement('ul');
  list.className = 'intake-conflict-list';
  list.hidden = !conflictChoice.open;
  each.addEventListener('click', () => {
    conflictChoice.open = !conflictChoice.open;
    list.hidden = !conflictChoice.open;
  });
  for (const p of conflicts) {
    const li = document.createElement('li');
    const name = document.createElement('span');
    name.textContent = (p.path || '').split('/').pop();
    name.title = p.dest || p.path;
    const select = document.createElement('select');
    for (const [value, label] of options) {
      const o = document.createElement('option');
      o.value = value;
      o.textContent = label;
      o.selected = p.conflict === value;
      select.append(o);
    }
    select.addEventListener('change', () => {
      p.conflict = select.value;
      changed();
    });
    li.append(name, select);
    // Kept both: the new one's name, as suggested, to change if wanted.
    if (p.conflict === 'keep') {
      const as = document.createElement('input');
      as.type = 'text';
      as.className = 'intake-conflict-name';
      as.value = p.asName || '';
      as.placeholder = 'Name for the new one';
      as.setAttribute('aria-label', 'Name for the new one');
      as.addEventListener('input', () => { p.asName = as.value; });
      li.append(as);
    }
    list.append(li);
  }
  box.append(text, row, note, each, list);
  return box;
}

// After: where each part really landed - which can differ from the plan, as
// a home video dropped with the films goes to the photos - and what did not.
function renderLanded(results) {
  const FOLDERS = { music: 'music', movies: 'video', tv: 'tv', audiobooks: 'audiobook', ebooks: 'ebook', documents: 'document', pictures: 'picture' };
  const parts = new Map();
  for (const { item, result } of results) {
    const key = item.group || item.path;
    if (!parts.has(key)) parts.set(key, { landed: {}, skipped: 0, failed: 0 });
    const part = parts.get(key);
    if (result.ok) {
      const kind = FOLDERS[(result.dest || '').split('/')[0]] || item.kind;
      part.landed[kind] = (part.landed[kind] || 0) + 1;
    } else if (result.skipped) part.skipped++;
    else part.failed++;
  }
  const list = document.createElement('ul');
  list.className = 'intake-parts';
  for (const [group, part] of parts) {
    const bits = Object.entries(part.landed).map(([k, n]) => `${shelfName(k)} ${n}`);
    if (part.skipped) bits.push(`${part.skipped} already there`);
    if (part.failed) bits.push(`${part.failed} failed`);
    const li = intakeLine(partLabel(group), `→ ${bits.join(' · ')}`);
    if (part.failed) li.classList.add('intake-failed');
    list.append(li);
  }
  const note = document.createElement('p');
  note.className = 'muted small-print';
  note.textContent = state.me && state.me.owner
    ? 'Something in the wrong place? Hold it (or right-click it) and choose Move to.'
    : 'Something in the wrong place? Whoever runs the server can move it.';
  $('intake-review').replaceChildren(list, note);
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

// Files going up: Hide puts the sheet away with a chip saying how far it
// has got (a tap brings it back); Stop lets the file being sent go and sends
// no more - what has arrived stays.
const INTAKE = { sending: false, stop: false, xhr: null, chipTimer: 0 };
function intakeChip(text, linger) {
  const chip = $('intake-chip');
  chip.textContent = text;
  clearTimeout(INTAKE.chipTimer);
  const on = Boolean(text) && !shown('intake');
  show(chip, on);
  if (on && linger) INTAKE.chipTimer = setTimeout(() => show(chip, false), linger);
}
$('intake-chip').addEventListener('click', () => {
  show($('intake'), true);
  show($('intake-chip'), false);
});
$('intake-stop').addEventListener('click', () => {
  if (INTAKE.native) {
    $('intake-stop').disabled = true;
    $('intake-stop').textContent = 'Stopping\u2026';
    appUploads('stop', {});
    return;
  }
  INTAKE.stop = true;
  $('intake-stop').disabled = true;
  $('intake-stop').textContent = 'Stopping\u2026';
  if (INTAKE.xhr) INTAKE.xhr.abort();
});

// In the Android app, files added are sent by the app, in the background
// (Uploads.kt): the page plans, hands over the list, and shows how it goes.
const APP_UPLOADS = Boolean(window.soundstormApp && window.soundstormApp.uploads) && !TV;
const appUploadWaiters = {};
function appUploads(cmd, data, ev) {
  return new Promise((resolve) => {
    if (ev) (appUploadWaiters[ev] = appUploadWaiters[ev] || []).push(resolve);
    window.soundstormApp.uploads(cmd, data);
    if (!ev) resolve(null);
    else setTimeout(() => resolve(null), 4000);
  });
}
window.__soundstormUploads = (msg) => {
  if (!msg) return;
  if (msg.ev === 'status' && msg.options) renderUploadOptions(msg.options);
  const list = appUploadWaiters[msg.ev] || [];
  appUploadWaiters[msg.ev] = [];
  list.forEach((r) => r(msg));
};
function renderUploadOptions(o) {
  $('uploads-wifi').checked = Boolean(o.wifiOnly);
  $('uploads-charging').checked = Boolean(o.charging);
}
if (APP_UPLOADS) {
  show($('uploads-options'), true);
  for (const id of ['uploads-wifi', 'uploads-charging']) {
    $(id).addEventListener('change', () => {
      appUploads('set', { wifiOnly: $('uploads-wifi').checked, charging: $('uploads-charging').checked });
    });
  }
}

// Following the app's uploads: the sheet and its chip from the app's status,
// once a second, until nothing is waiting - then where each part landed.
let appFollowing = false;
async function followAppUploads() {
  if (appFollowing) return;
  appFollowing = true;
  INTAKE.sending = true;
  INTAKE.native = true;
  INTAKE.stop = false;
  $('intake-close').textContent = 'Hide';
  $('intake-stop').disabled = false;
  $('intake-stop').textContent = 'Stop';
  show($('intake-sending'), true);
  show($('intake-bar'), true);
  let st = null;
  try {
    for (;;) {
      st = await appUploads('status', {}, 'status');
      if (!st) { await pause(1500); continue; }
      if (!st.waiting) break;
      const name = st.current || '';
      const of = `${Math.min(st.done + 1, st.total)} of ${st.total}`;
      const opts = st.options || {};
      let title = `Adding ${of}${name ? ` — ${name}` : ''}`;
      if (!st.running) {
        title = opts.wifiOnly && opts.charging ? `Waiting for Wi-Fi and charging — ${st.waiting} to add`
          : opts.wifiOnly ? `Waiting for Wi-Fi — ${st.waiting} to add`
            : opts.charging ? `Waiting to charge — ${st.waiting} to add`
              : `Waiting to send — ${st.waiting} to add`;
      }
      $('intake-title').textContent = title;
      const note = $('intake-note');
      note.textContent = st.problem ? st.problem : 'This keeps going if you leave the app.';
      show(note, true);
      intakeChip(st.running ? `Adding ${of}\u2026` : `Waiting \u2014 ${st.waiting} to add`);
      const bytes = st.totalBytes || 0;
      setIntakeProgress(bytes ? (st.doneBytes + (st.currentSent || 0)) / bytes : st.done / Math.max(1, st.total));
      await pause(document.hidden ? 5000 : 1000);
    }
  } finally {
    appFollowing = false;
    INTAKE.sending = false;
    INTAKE.native = false;
    $('intake-close').textContent = 'Close';
    show($('intake-sending'), false);
    show($('intake-bar'), false);
    show($('intake-note'), false);
  }
  const jobs = (st && st.jobs) || [];
  const results = jobs.map((j) => ({
    item: { path: j.path, group: j.group || j.path, kind: j.kind },
    result: j.state === 'done' ? { ok: true, dest: j.dest }
      : j.state === 'skipped' ? { ok: false, skipped: true, error: j.error }
        : j.state === 'stopped' ? { ok: false, stopped: true }
          : { ok: false, error: j.error || 'failed' },
  }));
  results.forEach(({ item, result }) => markIntakeRow(item.path, result));
  renderLanded(results.filter((r) => !r.result.stopped));
  const added = results.filter((r) => r.result.ok).length;
  const skipped = results.filter((r) => r.result.skipped).length;
  const stopped = results.filter((r) => r.result.stopped).length;
  const failed = results.length - added - skipped - stopped;
  const text = summary(added, skipped, failed, stopped);
  $('intake-title').textContent = text;
  intakeChip(text.split('.')[0], 8000);
  appUploads('seen', {});
  loadLibrary();
}
// Opened again while the app is still sending (or finished unseen): the
// chip, and the sheet behind it.
function resumeAppUploads() {
  if (!APP_UPLOADS) return;
  appUploads('status', {}, 'status').then((st) => {
    if (!st || !st.total) return;
    if (st.waiting) {
      $('intake-list').replaceChildren();
      $('intake-questions').replaceChildren();
      $('intake-review').replaceChildren();
      filesToggle(false);
      followAppUploads();
    } else if (!st.seen) {
      const added = st.jobs.filter((j) => j.state === 'done').length;
      intakeChip(`Added ${added} file${added === 1 ? '' : 's'}`, 8000);
      appUploads('seen', {});
    }
  });
}

async function sendFiles(plan, dropped) {
  $('intake-list').replaceChildren();

  const queue = [];
  plan.forEach((placement, index) => {
    renderIntakeRow(placement);
    if (!placement.skipped && !placement.waiting && placement.conflict !== 'skip') {
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
  // In the app: sent by the app, carrying on after it is left. Files it
  // cannot read (not from its own picker) are sent here, as before.
  if (APP_UPLOADS) {
    const jobs = queue.map((item) => ({
      // id: a file the iPhone app picked and holds (FileUploads.swift).
      id: item.file.appId || '',
      name: item.file.name, size: item.file.size, path: item.path, kind: item.kind, group: item.group || '',
      conflict: item.conflict === 'keep' || item.conflict === 'replace' ? item.conflict : '',
      as: item.conflict === 'keep' && item.asName ? item.asName : '',
      taken: (item.kind === 'picture' || item.kind === 'video') && item.file.lastModified ? item.file.lastModified : 0,
    }));
    const r = await appUploads('add', { server: location.origin, jobs }, 'added');
    if (r && r.added) {
      followAppUploads();
      return;
    }
  }
  show($('intake-bar'), true);
  INTAKE.sending = true;
  INTAKE.stop = false;
  $('intake-close').textContent = 'Hide';
  $('intake-stop').disabled = false;
  $('intake-stop').textContent = 'Stop';
  show($('intake-sending'), true);

  let done = 0;
  let sent = 0;
  let declined = 0; // already there, or the same recording is
  let stopped = 0;
  const results = [];
  try {
    for (const item of queue) {
      if (INTAKE.stop) { stopped += 1; markIntakeRow(item.path, { ok: false, stopped: true }); continue; }
      $('intake-title').textContent =
        `Adding ${done + 1} of ${queue.length} — ${item.file.name}`;
      intakeChip(`Adding ${done + 1} of ${queue.length}\u2026`);
      const result = await uploadOne(item, (loaded) => {
        setIntakeProgress(total ? (sent + loaded) / total : 0);
      });
      if (INTAKE.stop && !result.ok && !result.skipped) {
        // The one going up when Stop was pressed: let go, not failed.
        stopped += 1;
        markIntakeRow(item.path, { ok: false, stopped: true });
        continue;
      }
      sent += item.file.size;
      done += 1;
      setIntakeProgress(total ? sent / total : 1);
      if (!result.ok && result.skipped) declined += 1;
      markIntakeRow(item.path, result);
      results.push({ item, result });
    }
  } finally {
    INTAKE.sending = false;
    INTAKE.xhr = null;
    $('intake-close').textContent = 'Close';
    show($('intake-sending'), false);
  }
  renderLanded(results);

  const failed = document.querySelectorAll('#intake-list .intake-failed').length;
  // Skipped at planning, and skipped by the server as already there.
  const skipped = plan.filter((p) => p.skipped).length + declined;
  $('intake-title').textContent = summary(done - failed - declined, skipped, failed, stopped);
  intakeChip(summary(done - failed - declined, skipped, failed, stopped).split('.')[0], 8000);
  show($('intake-bar'), false);

  // Counts on the folder guide have moved, and a scan is probably running.
  loadLibrary();
}

function summary(added, skipped, failed, stopped = 0) {
  const parts = [`Added ${added} file${added === 1 ? '' : 's'}`];
  if (skipped) parts.push(`${skipped} skipped`);
  if (failed) parts.push(`${failed} failed`);
  if (stopped) parts.push(`${stopped} not sent (stopped)`);
  return parts.join(' · ') + '. It may take a minute to appear in search.';
}

// uploadOne sends one file. XMLHttpRequest rather than fetch, only because
// fetch cannot report upload progress, and a four gigabyte film with no
// progress bar looks like a hang.
function uploadOne(item, onProgress) {
  return new Promise((resolve) => {
    const params = new URLSearchParams({ path: item.path, kind: item.kind });
    // A taken name, as the person chose: keep both, or replace.
    if (item.conflict === 'keep' || item.conflict === 'replace') params.set('conflict', item.conflict);
    if (item.conflict === 'keep' && item.asName) params.set('as', item.asName);
    // A photo or video is sorted into the person's folder by when it was
    // taken; the file's own date (on a camera's card, when it was taken) is
    // what the server falls back on when the photo carries none inside it.
    // A film too: one a phone filmed goes to the photos by its date (homevideos.go).
    if ((item.kind === 'picture' || item.kind === 'video') && item.file && item.file.lastModified) params.set('taken', String(item.file.lastModified));
    const request = new XMLHttpRequest();
    INTAKE.xhr = request;
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
  li.classList.add(result.skipped || result.stopped ? 'intake-skipped' : 'intake-failed');
  dest.textContent = result.stopped ? 'not sent - stopped' : result.error;
}

$('intake-close').addEventListener('click', () => {
  show($('intake'), false);
  // While files go up this is Hide: they carry on, and a chip says how far.
  if (INTAKE.sending) {
    intakeChip($('intake-chip').textContent || 'Adding files\u2026');
    return;
  }
  show($('intake-chip'), false);
  $('intake-questions').replaceChildren();
  $('intake-review').replaceChildren();
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
    state.training = Boolean(body.training);
    show($('player-report'), state.training && Boolean(window.soundstormApp && window.soundstormApp.playerLog));
    // A TV asks "Who's listening?" every time it opens, when anybody is kept
    // on it - the owner's choice: a shared screen should not just carry on as
    // whoever used it last.
    // Just taken over from somebody's phone (players.go): that person is
    // who it is, with what they sent waiting - not asked who is listening.
    let switched = false;
    try { switched = sessionStorage.getItem('soundstorm-switched') === '1'; sessionStorage.removeItem('soundstorm-switched'); } catch { /* none */ }
    if (TV && body.hasAccount && !(switched && body.signedIn)) {
      const p = await api('/api/profiles');
      if (p.ok && p.body && p.body.people.length) {
        showProfiles(p.body);
        return;
      }
    }
    if (body.signedIn) {
      state.approveNewDevices = Boolean(body.approveNewDevices);
      showApp(body.user);
      setTimeout(adoptNativePlayback, 0);
    }
    else {
      // Signed out from elsewhere (a password reset, the account removed):
      // what is downloaded is no longer to be opened offline without a
      // password (a security review).
      try { if (hasDownloads()) localStorage.setItem('soundstorm-locked', '1'); } catch { /* no storage */ }
      showGate(body.hasAccount, body.setupCodeRequired);
    }
    return;
  }
  forgetSetupCodeInAddress();

  if (offline && hasDownloads() && !localStorage.getItem('soundstorm-locked')) {
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
    let added = 0;
    let there = 0;
    for (const it of items) {
      const { ok, status, body } = await api(`/api/playlists/${encodeURIComponent(id)}/items`, {
        method: 'POST', body: JSON.stringify({ source: it.sourceId, id: it.id }),
      });
      // Already in it: a playlist holds each song once, so it is skipped.
      if (status === 409) { there++; continue; }
      if (!ok) { failed = (body && body.error) || 'Could not add them all.'; break; }
      added++;
    }
    if (failed) {
      note.textContent = failed;
      show(note, true);
      return;
    }
    setSelecting(false);
    const s = (n) => (n === 1 ? '' : 's');
    showToast(there
      ? (added ? `Added ${added} song${s(added)}; ${there} ${there === 1 ? 'was' : 'were'} already in it.` : 'Those are all in it already.')
      : `Added ${added} song${s(added)} to the playlist.`);
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
  // Thirty seconds back and on, for an audiobook: a turning arrow round the
  // number.
  back30: '<path d="M5 13a7 7 0 1 0 2-5M4 4v4.5h4.5"/><text x="12.5" y="16" font-size="7.5" font-weight="700" text-anchor="middle" fill="currentColor" stroke="none" font-family="system-ui, sans-serif">30</text>',
  fwd30: '<path d="M19 13a7 7 0 1 1-2-5M20 4v4.5h-4.5"/><text x="11.5" y="16" font-size="7.5" font-weight="700" text-anchor="middle" fill="currentColor" stroke="none" font-family="system-ui, sans-serif">30</text>',
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
  sparkle: '<path d="M12 3v4M12 17v4M3 12h4M17 12h4M6.3 6.3l2.5 2.5M15.2 15.2l2.5 2.5M6.3 17.7l2.5-2.5M15.2 8.8l2.5-2.5"/>',
  image: '<rect x="4" y="5" width="16" height="14" rx="2"/><circle cx="9.5" cy="10" r="1.6"/><path d="M5 17l4.5-4.5 3 3 2.5-2.5L19 17"/>',
  album: '<circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="2.5"/>',
  upload: '<path d="M12 16V5M7 10l5-5 5 5M5 20h14"/>',
  move: '<path d="M4 7h6l2 2h8v9H4zM12 13.5h5M15 11l2.5 2.5L15 16"/>',
  device: '<rect x="7" y="2.5" width="10" height="19" rx="2.2"/><path d="M11 18.5h2"/>',
  cast: '<path d="M3 17.5a3.5 3.5 0 0 1 3.5 3.5M3 13.5A7.5 7.5 0 0 1 10.5 21M3 9.5A11.5 11.5 0 0 1 14.5 21M7 4h12a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3"/>',
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
    const asks = !downloaded && isVideoItem(item) && !(item.extra && item.extra.type === 'video');
    entries.push(menuItem('download', downloaded ? 'Remove download' : 'Download', async (event) => {
      if (asks) {
        // Redrawn in place: stopped here, or the click reaches the page from
        // a button no longer in the menu and closes it.
        event.stopPropagation();
      } else {
        closeItemMenu();
      }
      if (downloaded) {
        await removeItemDownload(item);
        showToast(`${item.title} removed from this device.`);
      } else {
        // A film or episode asks which version to keep, and which language.
        if (isVideoItem(item) && !(item.extra && item.extra.type === 'video')) {
          renderDownloadMenu(item);
          return;
        }
        const big = item.extra && item.extra.type === 'video';
        if (big && !window.confirm(`Download "${item.title}"? A clip that needs converting takes a while.`)) return;
        await downloadWithToast(item.title, (progress) => downloadBook(item, progress));
      }
    }));
  }
  if (item.kind === 'music' && !state.offline) {
    entries.push(menuItem('image', 'Change cover', (event) => {
      event.stopPropagation();
      renderCoverMenu({ song: item }, () => renderMainMenu(item, opts));
    }, { chevron: true }));
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
    entries.push(menuItem('move', 'Move to…', (event) => {
      event.stopPropagation();
      renderMoveMenu(item);
    }, { chevron: true }));
    entries.push(menuItem('trash', 'Delete from library', (event) => {
      event.stopPropagation();
      renderDeleteMenu(item);
    }, { className: 'menu-danger' }));
  }
  menu.replaceChildren(...entries, note);
}

// Move to: another shelf for something filed in the wrong one - a film that
// was a home video, a music folder that was an audiobook. The owner's, as
// deleting is. The server files it by the new shelf's own rule.
function renderMoveMenu(item) {
  const menu = $('item-menu');
  const note = menuNote();
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const backLabel = document.createElement('span');
  backLabel.textContent = 'Move to';
  back.append(backLabel);
  back.addEventListener('click', (event) => {
    event.stopPropagation();
    renderMainMenu(item, state.menuOpts);
  });
  const here = item.kind;
  const rows = shelvesAllowed().filter((k) => k !== here).map((kind) => menuItem('move', shelfName(kind), async (event) => {
    event.stopPropagation();
    note.textContent = `Moving to ${shelfName(kind)}…`;
    const { ok, body } = await api('/api/move', {
      method: 'POST',
      body: JSON.stringify({ items: [{ source: item.sourceId, id: item.id, title: item.title }], to: kind }),
    });
    if (!ok || !body) {
      note.textContent = (body && body.error) || 'Could not move it.';
      return;
    }
    closeItemMenu();
    const key = selectionKey(item);
    for (const el of document.querySelectorAll(`[data-key="${CSS.escape(key)}"]`)) {
      (el.closest('.item-holder') || el.closest('li') || el).remove();
    }
    showToast(body.stayed
      ? `Moved ${body.moved} file${body.moved === 1 ? '' : 's'} to ${shelfName(kind)}; ${body.stayed} stayed where ${body.stayed === 1 ? 'it was' : 'they were'}.`
      : `"${item.title}" moved to ${shelfName(kind)}. It shows there once the shelf has looked.`);
  }));
  menu.replaceChildren(back, ...rows, note);
}

// renderPlaylistMenu is the second page: which playlist, or a new one.
// Downloading a film: which version to keep, then - when the file has more
// than one - which language. The sizes are worked out from the length.
function renderDownloadMenu(item, vq) {
  const menu = $('item-menu');
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const backLabel = document.createElement('span');
  backLabel.textContent = vq ? 'Which language' : 'Download';
  back.append(backLabel);
  back.addEventListener('click', (event) => {
    event.stopPropagation();
    if (vq) renderDownloadMenu(item);
    else renderMainMenu(item, state.menuOpts);
  });
  const list = document.createElement('div');
  list.className = 'menu-scroll';
  const start = (quality, audio) => {
    closeItemMenu();
    downloadWithToast(item.title, (progress) => downloadBook(item, progress, null, { vq: quality, audio }));
  };
  if (!vq) {
    const hours = (Number(item.durationSeconds) || 0) / 3600;
    const gb = (mbps) => {
      const v = mbps * 3600 * hours / 8 / 1000;
      return `${v >= 10 ? Math.round(v) : Math.max(0.1, Math.round(v * 10) / 10)} GB`;
    };
    // Short names, the numbers in the detail: a phone's menu cut "Standard
    // (20 Mbps)" off.
    const choices = [
      ['standard', 'Standard', hours ? `20 Mbps, up to ${gb(20)}` : '20 Mbps, up to 9 GB an hour'],
      ['saver', 'Data saver', hours ? `720p, about ${gb(4.3)}` : '720p, about 2 GB an hour'],
      ['original', 'Original', 'the whole picture, largest'],
    ];
    list.append(...choices.map(([q, label, detail]) => menuItem('download', label, async (event) => {
      event.stopPropagation();
      // Ask which audio tracks the file has; with one, no question.
      const { ok, body } = await api(`/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}?vq=${q}`);
      const tracks = (ok && body && body.audio) || [];
      if (tracks.length < 2) start(q);
      else renderDownloadMenu(item, { q, tracks });
    }, { detail })));
  } else {
    // The default track needs no asking for: a film kept as it is keeps it.
    list.append(...vq.tracks.map((t) => menuItem('download', t.label, (event) => {
      event.stopPropagation();
      start(vq.q, t.default ? undefined : t.index);
    }, { detail: t.default ? 'main' : '', className: 'menu-wrap' })));
  }
  menu.replaceChildren(back, list);
  placeMenu(menu, state.menuAnchor);
}

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
    else {
      closeItemMenu();
      showToast(`Added to ${(lists.find((l) => l.id === id) || {}).name || 'the playlist'}.`);
    }
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
state.sheetMenus = Boolean(window.matchMedia && touchScreen());

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
    'photo-videos': 'videos', 'photo-live': 'Live Photos',
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

// The Playlists page: a card for each, A to Z, like every other shelf - a
// collage of its covers, its name and how many songs. Tap to open it; the
// shuffle button on its cover plays it shuffled straight away; a hold offers
// play, shuffle and delete. New and Import are two small buttons above the
// cards - they were cards of their own at the start of the grid, the size of
// a playlist, and got in the way (the owner's asking).
async function showPlaylists(report) {
  const view = $('playlists-view');
  delete view.dataset.open;
  const { ok, body } = await api('/api/playlists');
  const all = (ok && body && body.playlists) || [];
  // Typing narrows the playlists by name, as it narrows every other page.
  const words = (state.query || '').toLowerCase().split(/\s+/).filter(Boolean);
  const lists = all.filter((list) => words.every((w) => list.name.toLowerCase().includes(w)));
  view.replaceChildren();

  const form = document.createElement('form');
  form.className = 'playlist-new-form hidden';
  const input = document.createElement('input');
  input.placeholder = 'Name the new playlist';
  input.maxLength = 100;
  input.setAttribute('aria-label', 'New playlist name');
  const create = document.createElement('button');
  create.type = 'submit';
  create.textContent = 'Create';
  form.append(input, create);
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const name = input.value.trim();
    if (!name) return;
    const made = await api('/api/playlists', { method: 'POST', body: JSON.stringify({ name }) });
    if (made.ok && made.body && made.body.id) showPlaylist(made.body.id);
    else showToast((made.body && made.body.error) || 'Could not make the playlist.');
  });
  const actions = document.createElement('div');
  actions.className = 'playlist-actions';
  const make = document.createElement('button');
  make.type = 'button';
  make.className = 'playlist-action';
  make.append(icon('plus'));
  const makeLabel = document.createElement('span');
  makeLabel.textContent = 'New playlist';
  make.append(makeLabel);
  make.addEventListener('click', () => {
    show(form, form.classList.contains('hidden'));
    if (!form.classList.contains('hidden')) input.focus();
  });
  actions.append(make, importPlaylistButton());
  view.append(actions, form);
  if (report) view.append(report);

  const grid = document.createElement('div');
  grid.className = 'grid browse-grid mix-grid playlist-grid';
  for (const list of lists) grid.append(playlistCard(list));
  view.append(grid);
  if (!lists.length) {
    const empty = document.createElement('p');
    empty.className = 'muted playlist-empty';
    empty.textContent = all.length
      ? 'No playlists match.'
      : `No playlists yet. Make one here, or ${MENU_HOW.toLowerCase()} any song and choose Add to playlist.`;
    view.append(empty);
  }
}

async function playlistSongs(id) {
  const got = await api(`/api/playlists/${encodeURIComponent(id)}`);
  return (got.ok && got.body && got.body.items) || [];
}

async function playPlaylist(id, shuffle) {
  const songs = await playlistSongs(id);
  if (!songs.length) {
    showToast('That playlist is empty.');
    return;
  }
  playQueue(shuffle ? shuffled(songs) : songs, 0);
}

// A playlist's own picture (Change picture in its menu), kept like a song's
// own cover (collections/art.go), in place of the collage of its songs.
const playlistArt = (id) => (state.myArt && state.myArt[`playlist:${id}`]) || '';

function playlistCover(list) {
  const wrap = document.createElement('div');
  wrap.className = 'art-wrap mix-cover';
  const mine = playlistArt(list.id);
  if (mine) {
    const img = document.createElement('img');
    img.src = mine;
    img.alt = '';
    img.decoding = 'async';
    wrap.append(img);
    return wrap;
  }
  const art = (list.covers || []).slice(0, 4);
  if (art.length >= 4) wrap.classList.add('collage');
  for (const c of art.length >= 4 ? art : art.slice(0, 1)) {
    const img = document.createElement('img');
    img.src = artUrl(c.sourceId, c.artId);
    img.alt = '';
    img.loading = 'lazy';
    img.fetchPriority = 'low';
    img.decoding = 'async';
    wrap.append(img);
  }
  if (!art.length) wrap.append(noCover());
  return wrap;
}

function playlistCard(list) {
  const holder = document.createElement('div');
  holder.className = 'item-holder';
  const card = document.createElement('button');
  card.type = 'button';
  card.className = 'item mix-card playlist-card';
  const meta = document.createElement('div');
  meta.className = 'meta';
  const title = document.createElement('span');
  title.className = 'title';
  title.textContent = list.name;
  const sub = document.createElement('span');
  sub.className = 'sub';
  sub.textContent = `${list.count} song${list.count === 1 ? '' : 's'}`;
  meta.append(title, sub);
  card.append(playlistCover(list), meta);
  card.addEventListener('click', (event) => {
    if (state.suppressClick) {
      state.suppressClick = false;
      event.stopPropagation();
      return;
    }
    showPlaylist(list.id);
  });
  // Shuffle it from here, as a mix plays from its card.
  const shuffle = document.createElement('button');
  shuffle.type = 'button';
  shuffle.className = 'playlist-shuffle';
  shuffle.setAttribute('aria-label', `Shuffle ${list.name}`);
  shuffle.title = 'Shuffle';
  shuffle.append(icon('shuffle'));
  shuffle.disabled = !list.count;
  shuffle.addEventListener('click', (event) => {
    event.stopPropagation();
    playPlaylist(list.id, true);
  });
  holder.append(card, shuffle);
  attachHoldMenu(card, () => openPlaylistMenu(list, card.querySelector('.art-wrap') || card));
  return holder;
}

// A hold (or right-click) that opens a menu, for cards that are not items -
// playlists - with the same timing, slop and press-slide-release as the rest.
function attachHoldMenu(card, open) {
  let timer = null;
  let x = 0;
  let y = 0;
  const cancel = () => {
    clearTimeout(timer);
    timer = null;
    card.classList.remove('pressing');
  };
  card.addEventListener('pointerdown', (event) => {
    if (event.pointerType === 'mouse' || event.button !== 0) return;
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
    if (!state.holding) open();
  });
}

function openPlaylistMenu(list, anchor) {
  const menu = $('item-menu');
  state.menuFor = list;
  state.menuAnchor = anchor;
  state.menuOpts = {};
  const head = document.createElement('div');
  head.className = 'menu-head';
  const t = document.createElement('strong');
  t.textContent = list.name;
  const sub = document.createElement('span');
  sub.textContent = `Playlist \u00b7 ${list.count} song${list.count === 1 ? '' : 's'}`;
  head.append(t, sub);
  const entries = [head];
  if (list.count) {
    entries.push(
      menuItem('play', 'Play', () => { closeItemMenu(); playPlaylist(list.id, false); }),
      menuItem('shuffle', 'Shuffle', () => { closeItemMenu(); playPlaylist(list.id, true); }),
    );
  }
  if (!state.offline) {
    entries.push(menuItem('image', 'Change picture', (event) => {
      event.stopPropagation();
      renderPlaylistPictureMenu(list, () => openPlaylistMenu(list, anchor));
    }, { chevron: true }));
  }
  entries.push(menuItem('trash', 'Delete playlist', (event) => {
    // Two taps, not a browser dialog: the first asks, the second deletes.
    event.stopPropagation();
    const b = event.currentTarget;
    if (!b.classList.contains('confirming')) {
      b.classList.add('confirming');
      b.querySelector('.menu-label').textContent = 'Tap again to delete';
      return;
    }
    closeItemMenu();
    api(`/api/playlists/${encodeURIComponent(list.id)}`, { method: 'DELETE' }).then(() => showPlaylists());
  }, { className: 'menu-danger' }));
  menu.replaceChildren(...entries);
  placeMenu(menu, anchor);
}

const PLAYLIST_SORTS = [
  ['title', 'A to Z'],
  ['artist', 'Artist'],
  ['added', 'Recently added'],
  ['custom', 'Custom order'],
];

async function showPlaylist(id) {
  const seq = ++state.searchSeq;
  const view = $('playlists-view');
  view.dataset.open = id;
  startLoading(view);
  const { ok, body } = await api(`/api/playlists/${encodeURIComponent(id)}`);
  // Left for another page while this one loaded: its answer is not wanted.
  if (seq !== state.searchSeq) return;
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
  const cover = coverArt(playlistArt(id) || (first.artId ? artUrl(first.sourceId, first.artId) : ''), body.name);
  cover.classList.add('album-cover');
  // Holding the cover (or right-clicking it) changes the playlist's picture.
  if (!state.offline) {
    const list = { id, name: body.name, count: songs.length };
    attachHoldMenu(cover, () => {
      state.menuFor = list;
      state.menuAnchor = cover;
      state.menuOpts = {};
      renderPlaylistPictureMenu(list, null);
    });
  }
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
  // How its songs are ordered: A to Z unless chosen otherwise, and played in
  // the order shown. Custom order is the hand-made one, dragged by handles.
  const sortBy = body.sort || 'title';
  const sortButton = document.createElement('button');
  sortButton.type = 'button';
  sortButton.className = 'ghost small playlist-sort';
  sortButton.append(document.createTextNode(`Sort: ${(PLAYLIST_SORTS.find(([k]) => k === sortBy) || PLAYLIST_SORTS[0])[1]}`), icon('down'));
  sortButton.addEventListener('click', (event) => {
    event.stopPropagation();
    const menu = $('item-menu');
    state.menuFor = 'playlist-sort';
    state.menuAnchor = sortButton;
    state.menuOpts = {};
    menu.replaceChildren(...PLAYLIST_SORTS.map(([key, label]) => menuItem(key === sortBy ? 'check' : 'playlist', label, async () => {
      closeItemMenu();
      if (key === sortBy) return;
      await api(path, { method: 'PATCH', body: JSON.stringify({ sort: key }) });
      showPlaylist(id);
    })));
    placeMenu(menu, sortButton);
  });
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
  // Its order and deleting it, on a line of their own under the play row.
  const tools = document.createElement('div');
  tools.className = 'playlist-tools';
  tools.append(sortButton, remove);
  text.append(kind, title, facts, buttons, tools);
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
    thumb.fetchPriority = 'low';
    thumb.src = artPath(song) || NO_COVER;
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

    li.append(play, drop);
    // Moving a song by hand is what Custom order is; in the others the
    // order is worked out, so there is nothing to drag.
    if (sortBy === 'custom') {
      const handle = document.createElement('button');
      handle.type = 'button';
      handle.className = 'np-icon playlist-handle';
      handle.setAttribute('aria-label', `Move ${song.title}`);
      handle.append(icon('grip'));
      attachReorder(handle, li, list, async (from, to) => {
        await api(`${path}/move`, { method: 'POST', body: JSON.stringify({ from: songs[from].position, to: songs[to].position }) });
        showPlaylist(id);
      });
      li.append(handle);
    }
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
  const path = bigArt(artPath(item), 600);
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
  const chapters = item.kind !== 'music' && (audio.chapters || []).length > 1 ? audio.chapters : null;
  const track = chapters ? chapters[chapterAt(elapsed())] : audio.tracks.length > 1 ? audio.tracks[audio.index] : null;
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
  // An audiobook gets the lock screen's seek buttons, not next and previous:
  // there is no next song, and a chapter is too far to jump by accident.
  const book = item.kind !== 'music';
  const hasNext = !book && ((audio.queue && audio.queue.index + 1 < audio.queue.items.length)
    || audio.index + 1 < audio.tracks.length);
  const hasPrev = !book && ((audio.queue && audio.queue.index > 0) || audio.index > 0);
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
  } else if (RA.on && !audio.queue && !raBook()) {
    // A song the TV was playing when this phone picked it up: its queue is
    // the TV's, so the TV moves on.
    raSend({ type: 'control', action: 'next' });
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
  } else if (RA.on && !audio.queue && !raBook()) {
    raSend({ type: 'control', action: 'prev' });
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
  if (!artId) return '';
  return withOverride(`/api/art/${encodeURIComponent(sourceId)}/${escapeId(artId)}`, '', artKeyFor(sourceId, artId));
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
    img.fetchPriority = 'low';
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
  if (!state.offline) {
    entries.push(menuItem('image', 'Change cover', (event) => {
      event.stopPropagation();
      renderCoverMenu({ album, songs }, () => openAlbumMenu(album, anchor));
    }, { chevron: true }));
  }
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
  const seq = ++state.searchSeq;
  const view = $('music-view');
  startLoading(view);
  const { ok, body } = await api(`/api/music/albums/${encodeURIComponent(sourceId)}/${escapeId(id)}`);
  if (seq !== state.searchSeq) return;
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
  const seq = ++state.searchSeq;
  const view = $('music-view');
  startLoading(view);
  const { ok, body } = await api(`/api/music/artists/${encodeURIComponent(sourceId)}/${escapeId(id)}`);
  if (seq !== state.searchSeq) return;
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
      // Only a Wikipedia page, whatever the answer said (a review).
      more.href = /^https:\/\/[a-z-]+\.wikipedia\.org\//.test(body.bioUrl) ? body.bioUrl : 'https://en.wikipedia.org/';
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
  renderLyrics();
  show($('now-playing'), true);
  document.body.classList.add('np-open');
  renderNowPlaying();
}

function closeNowPlaying() {
  closeLooks();
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

document.querySelector('#now-playing .np-head').addEventListener('click', () => {
  // On a phone the title is the way back from Up next to the lyrics. Only
  // from Up next: in the lyrics it used to run the same layout again as a
  // view transition, and the title flickered as if it had been pressed.
  if (audio.npMode !== 'queue' || !matchMedia('(max-width: 760px)').matches) return;
  npTransition(() => {
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
  // An audiobook swipes to its chapters (the owner's asking, once a book had
  // a chapter list to go by): left to the next, right to the one before -
  // the same cover slides in.
  if ((by > 0 ? nextChapterStart() : prevChapterStart()) !== null) return { item: audio.item };
  return null;
}

// Where the next chapter starts on the book's timeline, or null in the last
// (or a book without chapters).
function nextChapterStart() {
  const list = audio.chapters || [];
  if (!audio.item || audio.item.kind === 'music' || list.length < 2) return null;
  const i = chapterAt(elapsed());
  return i + 1 < list.length ? list[i + 1].startSeconds : null;
}

// Where a swipe right goes: the start of this chapter, if it is more than
// five seconds in - as "previous" restarts a song - else the chapter before.
// Null at the very start of the first.
function prevChapterStart() {
  const list = audio.chapters || [];
  if (!audio.item || audio.item.kind === 'music' || list.length < 2) return null;
  const now = elapsed();
  const i = chapterAt(now);
  if (now - (list[i].startSeconds || 0) > 5) return list[i].startSeconds || 0;
  return i > 0 ? list[i - 1].startSeconds : null;
}

// A swipe changes song outright: back means the song before, not the start
// of this one, since the previous cover is what slid in.
function stepTrack(by) {
  if (audio.item && audio.item.kind !== 'music') {
    const next = by > 0 ? nextChapterStart() : prevChapterStart();
    if (next !== null) {
      goToBook(next);
      const list = audio.chapters;
      const title = list[chapterAt(next + 0.5)] && list[chapterAt(next + 0.5)].title;
      if (title) showToast(title, '', null, 1600);
    }
    return;
  }
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
  const artOf = (n) => (n && bigArt(artPath(n.item))) || NO_COVER;

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
      put(coverDeco(), d, ms);
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
        img.fetchPriority = 'low';
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
      // The record or visualizer over it rode the swipe too, and comes back
      // with it; left behind, it sat off to the side for the next song.
      put(coverDeco(), 0, 0);
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
  let fromTopEdge = false;
  let axis = null;   // 'y' closes, 'x' changes song
  let dx = 0;
  panel.addEventListener('touchstart', (event) => {
    const t = event.touches[0];
    armed = event.touches.length === 1 && !npSwipe.busy && !event.target.closest('input') && !event.target.closest('#np-looks, #np-chapters');
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
    // A pull down from the screen's top edge is Android's - the notifications
    // and quick settings - not a close. Reported as Now Playing closing every
    // time somebody reached for them. Only a thin band: the title just below
    // it is where people grab to pull Now Playing away.
    const safeTop = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--safe-top')) || 0;
    fromTopEdge = startY < Math.max(56, safeTop + 32);
  }, { passive: true });
  panel.addEventListener('touchmove', (event) => {
    if (!armed) return;
    // A hold put up the menu or the icons: the finger is theirs now.
    if (shown('item-menu') || shown('np-hold-layer')) {
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
      } else if (d > 0 && fromTopEdge) {
        armed = false; // the system's pull-down, not ours
        return;
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
$('dock-prev').addEventListener('click', () => $('np-prev').click());

// Previous and next for music; thirty seconds back and on for an audiobook,
// on Now Playing and the mini-player alike.
function renderSkipButtons(music) {
  if (music === audio.skipMusic) return;
  audio.skipMusic = music;
  for (const [id, song, book, songLabel, bookLabel] of [
    ['np-prev', 'prev', 'back30', 'Previous', 'Back 30 seconds'],
    ['np-next', 'skip', 'fwd30', 'Next', 'Forward 30 seconds'],
    ['dock-prev', 'prev', 'back30', 'Previous', 'Back 30 seconds'],
    ['dock-next', 'skip', 'fwd30', 'Next', 'Forward 30 seconds'],
  ]) {
    // A song's arrows are solid; the thirty-second ones are drawn in line.
    setIcon($(id), music ? song : book, music);
    $(id).setAttribute('aria-label', music ? songLabel : bookLabel);
  }
}
$('dock-next').addEventListener('click', () => $('np-next').click());

function renderDockButtons() {
  const q = audio.queue;
  const book = Boolean(audio.item && audio.item.kind !== 'music');
  // An audiobook's are thirty seconds back and on, always there - and on a
  // phone the card shows its forward one (dock-book), where a song's next
  // gives way to the swipe.
  renderSkipButtons(!book);
  document.body.classList.toggle('dock-book', book);
  const more = book || (q
    ? q.index + 1 < q.items.length || audio.repeat !== 'off'
    : audio.index + 1 < audio.tracks.length);
  $('dock-next').disabled = !more;
}

// Where the song is, on the card: the line along its bottom edge, and on a
// computer the seek bar and the times.
function renderDockProgress() {
  const player = $('audio-player');
  // An audiobook's is the chapter's, as in Now Playing.
  const span = bookSpan();
  const length = span ? span.end - span.start : Number.isFinite(player.duration) ? player.duration : audio.duration || 0;
  const at = span ? Math.max(0, span.now - span.start) : player.currentTime || 0;
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
  const span = bookSpan();
  if (span) {
    $('dock-time').textContent = formatDuration((Number($('dock-seek').value) / 1000) * (span.end - span.start)) || '0:00';
  } else if (Number.isFinite(player.duration)) {
    $('dock-time').textContent = formatDuration((Number($('dock-seek').value) / 1000) * player.duration) || '0:00';
  }
});
$('dock-seek').addEventListener('change', () => {
  const player = $('audio-player');
  const span = bookSpan();
  if (span) goToBook(span.start + Math.min(0.999, Number($('dock-seek').value) / 1000) * (span.end - span.start));
  else if (Number.isFinite(player.duration)) player.currentTime = (Number($('dock-seek').value) / 1000) * player.duration;
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
  show($('np-looks-btn'), item.kind === 'music');
  show($('np-cast'), !TV && !state.offline);
  // An audiobook keeps the plain player it had before the looks: its buttons
  // and timeline showing, its cover, no visualizer. Everything that hides
  // until a hold is for music (np-music).
  const music = item.kind === 'music';
  $('now-playing').classList.toggle('np-music', music);
  show($('np-chapters-btn'), !music && (audio.chapters || []).length > 1);
  renderSkipButtons(music);
  playOrb.start();
  renderSpeed();
  const art = artPath(item);
  // The big cover asks for a big picture; offline, or where the server
  // cannot size it, it falls back to the card's.
  const big = bigArt(art);
  const cover = $('np-cover');
  if (cover.getAttribute('src') !== (big || NO_COVER)) cover.src = big || NO_COVER;
  cover.onerror = () => { cover.onerror = () => { cover.onerror = null; cover.src = NO_COVER; }; cover.src = art || NO_COVER; };
  const thumb = $('np-thumb');
  thumb.src = art || NO_COVER;
  thumb.onerror = () => { thumb.onerror = null; thumb.src = NO_COVER; };
  setBackdrop(art);
  tintStatusBar(art);
  setNpLine($('np-title'), item.title);
  setNpLine($('np-sub'), [(item.creators || []).join(', '), (item.extra && item.extra.album) || item.subtitle]
    .filter(Boolean).join(' \u2014 '));

  const player = $('audio-player');
  setIcon($('np-play'), player.paused ? 'play' : 'pause', true);
  applyCoverStyle();
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
  // An audiobook's is thirty seconds on, always there.
  $('np-next').disabled = item.kind === 'music' && (!q || (q.index + 1 >= q.items.length && audio.repeat === 'off'));

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
    art.fetchPriority = 'low';
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

// bookSpan: for an audiobook with chapters, the chapter being listened to on
// the whole book's timeline - its start and end, its number - and the book's
// length. Null for music, or a book of one chapter, which keep the one
// timeline they always had.
function bookSpan() {
  const item = audio.item;
  const list = audio.chapters || [];
  if (!item || item.kind === 'music' || list.length < 2) return null;
  const player = $('audio-player');
  const total = audio.duration || (audio.tracks.length > 1 ? 0 : (Number.isFinite(player.duration) ? player.duration : 0));
  if (!total) return null;
  const now = elapsed();
  const i = chapterAt(now);
  const start = list[i].startSeconds || 0;
  const end = i + 1 < list.length ? list[i + 1].startSeconds : total;
  if (!(end > start)) return null;
  return { i, count: list.length, start, end, total, now };
}

// Anywhere in an audiobook, by the whole book's timeline: a file of its own
// when it is in another, or a seek within this one.
function goToBook(to) {
  const item = audio.item;
  if (!item) return;
  if (audio.tracks.length > 1 && trackContaining(to) !== audio.index) {
    savePosition();
    seekTo(to, item);
  } else {
    $('audio-player').currentTime = to - (audio.tracks.length > 1 ? audio.tracks[audio.index].startSeconds || 0 : 0);
  }
}

function syncNowPlayingTime() {
  if ($('now-playing').classList.contains('hidden')) return;
  const player = $('audio-player');
  // An audiobook's timeline is its chapter's, and a thinner bar under it the
  // whole book's, which is shown but cannot be dragged (the owner's asking:
  // a timeline across a ten-hour book moves a chapter at the slightest touch).
  const span = bookSpan();
  show($('np-book'), Boolean(span));
  if (span) {
    const len = span.end - span.start;
    const into = Math.max(0, Math.min(len, span.now - span.start));
    if (!state.seeking) $('np-seek').value = String(Math.round((into / len) * 1000));
    $('np-time').textContent = formatDuration(into) || '0:00';
    $('np-length').textContent = formatDuration(len) || '0:00';
    $('np-book-fill').style.width = `${Math.min(100, (span.now / span.total) * 100).toFixed(2)}%`;
    const title = (audio.chapters[span.i] && audio.chapters[span.i].title) || `Chapter ${span.i + 1}`;
    $('np-book-chapter').textContent = `${span.i + 1} of ${span.count} \u00B7 ${title}`;
    $('np-book-left').textContent = `${formatDuration(Math.max(0, span.total - span.now))} left`;
    return;
  }
  const length = Number.isFinite(player.duration) ? player.duration : 0;
  if (!state.seeking) {
    $('np-seek').value = length ? String(Math.round((player.currentTime / length) * 1000)) : '0';
  }
  $('np-time').textContent = formatDuration(player.currentTime) || '0:00';
  $('np-length').textContent = formatDuration(length) || '0:00';
}

// A tap anywhere on the mini-player opens Now Playing - the cover, the title,
// the blurred card around them - except on what answers a tap of its own: the
// buttons, the seek and volume sliders, the chapter list. It used to be the
// cover and the title only, and the rest of the card did nothing.
$('audio-dock').addEventListener('click', (event) => {
  if (event.target.closest('button:not(#audio-open), input, label, #audio-tracks, a')) return;
  // The lift that ends a hold (which opened the menu) is not a tap.
  if (performance.now() - (state.heldAt || 0) < 700) return;
  openNowPlaying();
});
$('audio-meta').addEventListener('keydown', (event) => {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    openNowPlaying();
  }
});
$('np-close').addEventListener('click', closeNowPlaying);
setIcon($('np-exit'), 'close');
setIcon($('np-looks-btn'), 'sparkle');
setIcon($('np-cast'), 'cast');
// Play on another device, from Now Playing and the film player: the device
// picker, where choosing a TV moves what is playing here to it.
$('np-cast').addEventListener('click', (event) => {
  event.stopPropagation();
  openControlSheet();
});
$('video-cast').addEventListener('click', (event) => {
  event.stopPropagation();
  openControlSheet();
});

// The Looks sheet: every look, grouped, picked right in Now Playing. Choosing
// one closes it; so does a tap anywhere else. Its taps and scrolls are its
// own: Now Playing's swipe and hold stand down inside it.
function openLooks() {
  renderLooks();
  show($('np-looks'), true);
  $('np-looks-btn').setAttribute('aria-expanded', 'true');
}
function closeLooks() {
  show($('np-looks'), false);
  $('np-looks-btn').setAttribute('aria-expanded', 'false');
}
// vizLead: how far ahead of the player's clock the visuals run on this device,
// in seconds - positive shows each moment sooner. Reported on a phone's own
// speaker as the lightning landing late, while in Chrome on a computer it
// struck on every beat: the gap between the clock a page reads and the sound
// is the device's, and nothing a page can ask reports it. So it is set by eye,
// in the Looks sheet, and kept per device.
const VIZ_LEAD_KEY = 'soundstorm-viz-lead';
function vizLead() {
  const v = Number(localStorage.getItem(VIZ_LEAD_KEY));
  return Number.isFinite(v) ? Math.max(-0.5, Math.min(1, v)) : 0;
}
function vizLeadLabel(v) {
  if (Math.abs(v) < 0.001) return 'On time';
  return `${Math.abs(v).toFixed(1)} s ${v > 0 ? 'sooner' : 'later'}`;
}
// The Looks sheet's timing row: Sooner and Later step by a tenth of a second,
// and the sheet stays open so the change can be judged by eye.
function looksTiming() {
  const row = document.createElement('div');
  row.className = 'np-looks-timing';
  const label = document.createElement('span');
  label.className = 'np-looks-timing-name';
  label.textContent = 'Timing';
  const value = document.createElement('span');
  value.className = 'np-looks-timing-value';
  value.textContent = vizLeadLabel(vizLead());
  const step = (by, name) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'np-look';
    b.textContent = name;
    b.addEventListener('click', (event) => {
      event.stopPropagation();
      const next = Math.round((vizLead() + by) * 10) / 10;
      localStorage.setItem(VIZ_LEAD_KEY, String(Math.max(-0.5, Math.min(1, next))));
      value.textContent = vizLeadLabel(vizLead());
    });
    return b;
  };
  row.append(label, step(-0.1, 'Later'), value, step(0.1, 'Sooner'));
  const hint = document.createElement('p');
  hint.className = 'np-looks-hint';
  hint.textContent = 'If the visuals land after the beat, choose Sooner.';
  // Whether the looks are following this song's own beats, and if not why:
  // otherwise nobody can tell a song heard from one moving to its tempo.
  const heard = document.createElement('p');
  heard.className = 'np-looks-hint';
  const show = () => {
    if (!heard.isConnected && heard.dataset.shown) return;
    heard.dataset.shown = '1';
    const key = audio.item && selectionKey(audio.item);
    const followed = viz.heard && key && viz.heard.key === key;
    heard.textContent = !key ? '' : followed
      ? `Following this song's beats (${viz.heard.beats.length} found).`
      : `Following its tempo only: ${hearState.get(key) || 'not listened to yet'}.`;
    setTimeout(show, 1000);
  };
  show();
  return [row, hint, heard];
}
function renderLooks() {
  const body = $('np-looks-body');
  if (!body) return;
  const current = coverStyle();
  body.replaceChildren(...COVER_GROUPS.flatMap((group) => {
    const h = document.createElement('p');
    h.className = 'np-looks-group';
    h.textContent = group.name;
    const grid = document.createElement('div');
    grid.className = 'np-looks-grid';
    grid.append(...group.styles.map((style) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = `np-look${style === current ? ' on' : ''}`;
      // Names only, so they fit three to a row on a phone; the current one
      // is ticked.
      if (style === current) b.append(icon('check'));
      const label = document.createElement('span');
      label.textContent = COVER_STYLE_NAMES[style];
      b.append(label);
      b.addEventListener('click', (event) => {
        event.stopPropagation();
        state.prefs = state.prefs || {};
        state.prefs.coverStyle = style;
        applyCoverStyle();
        savePrefs({ coverStyle: style });
        closeLooks();
      });
      return b;
    }));
    return [h, grid];
  }), ...looksTiming(), ...looksTraining());
}
$('np-looks-btn').addEventListener('click', (event) => {
  event.stopPropagation();
  if ($('np-looks').classList.contains('hidden')) openLooks();
  else closeLooks();
});
document.addEventListener('click', (event) => {
  if ($('np-looks').classList.contains('hidden')) return;
  if (event.target.closest && (event.target.closest('#np-looks') || event.target.closest('#np-looks-btn'))) return;
  closeLooks();
});
// The Chapters sheet: an audiobook's table of contents, the chapter being
// listened to marked, a tap going there. Audiobookshelf's chapter list names
// the marks inside one file as well as one file per chapter, so a single m4b
// is navigable too - it never was before.
function chapterAt(seconds) {
  const list = audio.chapters || [];
  let at = 0;
  for (let i = 0; i < list.length; i++) if (list[i].startSeconds <= seconds + 0.5) at = i;
  return at;
}
function openChapters() {
  const list = $('np-chapters-list');
  const current = chapterAt(elapsed());
  list.replaceChildren(...(audio.chapters || []).map((c, i) => {
    const li = document.createElement('li');
    const b = document.createElement('button');
    b.type = 'button';
    b.className = `np-chapter${i === current ? ' on' : ''}`;
    const name = document.createElement('span');
    name.className = 'np-chapter-name';
    name.textContent = c.title || `Chapter ${i + 1}`;
    const time = document.createElement('span');
    time.className = 'np-chapter-time';
    time.textContent = formatDuration(c.startSeconds) || '0:00';
    b.append(name, time);
    b.addEventListener('click', (event) => {
      event.stopPropagation();
      savePosition();
      seekTo(c.startSeconds, audio.item);
      closeChapters();
    });
    li.append(b);
    return li;
  }));
  show($('np-chapters'), true);
  const on = list.querySelector('.on');
  if (on) {
    on.scrollIntoView({ block: 'center' });
    // On a TV (and with a keyboard) the remote's highlight starts on the
    // chapter playing, not on the button that opened the list (the owner's
    // asking); a touch screen just sees it marked.
    if (!touchScreen()) on.focus({ preventScroll: true });
  }
}
function closeChapters() {
  show($('np-chapters'), false);
}
$('np-chapters-btn').addEventListener('click', (event) => {
  event.stopPropagation();
  if ($('np-chapters').classList.contains('hidden')) openChapters();
  else closeChapters();
});
document.addEventListener('click', (event) => {
  if ($('np-chapters').classList.contains('hidden')) return;
  if (event.target.closest && (event.target.closest('#np-chapters') || event.target.closest('#np-chapters-btn'))) return;
  closeChapters();
});
$('np-exit').addEventListener('click', stopAudio);
$('np-play').addEventListener('click', () => {
  const player = $('audio-player');
  if (player.paused) player.play().catch(() => {});
  else player.pause();
});
// Thirty seconds either way in an audiobook, across its files.
function bookSkip(by) {
  const item = audio.item;
  if (!item) return;
  goToBook(Math.max(0, Math.min(elapsed() + by, (audio.duration || Infinity) - 1)));
}
$('np-prev').addEventListener('click', () => {
  if (audio.item && audio.item.kind !== 'music') bookSkip(-30);
  else mediaPrevious();
});
$('np-next').addEventListener('click', () => {
  if (audio.item && audio.item.kind !== 'music') { bookSkip(30); return; }
  const q = audio.queue;
  if (q && q.index + 1 >= q.items.length && audio.repeat !== 'off') playQueueAt(0);
  else mediaNext();
});
$('np-seek').addEventListener('input', () => {
  state.seeking = true;
  const player = $('audio-player');
  const span = bookSpan();
  if (span) {
    $('np-time').textContent = formatDuration((Number($('np-seek').value) / 1000) * (span.end - span.start)) || '0:00';
  } else if (Number.isFinite(player.duration)) {
    $('np-time').textContent = formatDuration((Number($('np-seek').value) / 1000) * player.duration) || '0:00';
  }
});
$('np-seek').addEventListener('change', () => {
  const player = $('audio-player');
  const span = bookSpan();
  // Within the chapter, for an audiobook - never past its end into the next.
  if (span) goToBook(span.start + Math.min(0.999, Number($('np-seek').value) / 1000) * (span.end - span.start));
  else if (Number.isFinite(player.duration)) player.currentTime = (Number($('np-seek').value) / 1000) * player.duration;
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
audio.preloadAbort = null;

// A preload that has not finished when its song is wanted is stopped, not
// raced. Reported as a long wait when a song ended by itself, on a phone away
// from home: at 1.5 times a song's bitrate (the server's pace, pace.go) a
// whole song takes minutes to arrive, the preload starts thirty seconds
// before the end, so at the end the song was streamed a second time while the
// preload went on fetching it - two copies of one song on a link that
// carries one. A skip, which never had a preload running, started faster
// than a song ending on its own.
function stopPreloading() {
  if (audio.preloadAbort) audio.preloadAbort.abort();
  audio.preloadAbort = null;
  audio.preloading = null;
}

function upcomingItem() {
  const q = audio.queue;
  if (!q) return null;
  if (audio.repeat === 'one') return q.items[q.index];
  if (q.index + 1 < q.items.length) return q.items[q.index + 1];
  if (audio.repeat === 'all' && q.items.length) return q.items[0];
  return null;
}

// In the Android app the page remembers its queue on the device as each
// song starts, and a page made again after Android ended the last one (the
// app in the background, its memory reclaimed) asks the player what is
// playing and takes it over: the same song, where it has got to, playing or
// paused, with the queue around it - rather than an empty Now Playing over
// music that is still going.
const NATIVE_QUEUE_KEY = 'soundstorm-native-queue';
function saveNativeQueue() {
  if (!NATIVE_AUDIO || !audio.item || audio.item.kind !== 'music') return;
  try {
    const q = audio.queue;
    const items = q ? q.items : [audio.item];
    const index = q ? q.index : 0;
    const from = Math.max(0, index - 20);
    localStorage.setItem(NATIVE_QUEUE_KEY, JSON.stringify({
      items: items.slice(from, index + 200), index: index - from, queued: Boolean(q), repeat: audio.repeat || null,
    }));
  } catch { /* nothing to take over later */ }
}

async function adoptNativePlayback() {
  const app = window.soundstormApp;
  if (!NATIVE_AUDIO || !app || !app.nativeState || !app.askState || audio.item) return;
  app.askState();
  await new Promise((r) => setTimeout(r, 600));
  const now = app.nativeState();
  if (!now || !now.url || audio.item) return;
  let saved = null;
  try { saved = JSON.parse(localStorage.getItem(NATIVE_QUEUE_KEY) || 'null'); } catch { saved = null; }
  if (!saved || !Array.isArray(saved.items)) return;
  const index = saved.items.findIndex((it) => it && it.kind === 'music' && new URL(playPath(it), location.href).href === now.url);
  if (index < 0) return;
  if (saved.repeat) audio.repeat = saved.repeat;
  if (saved.queued) audio.queue = { items: saved.items, index, original: null };
  audio.adoptPaused = !now.pwr;
  playAudio(saved.items[index], Boolean(saved.queued));
}

// nativeUpcoming is the songs after this one for the Android app's player,
// in the order they will play, up to ten: each one's address, and its title,
// artist, album and cover for the lock screen. It stops at a song the player
// cannot take (a download, or something other than music), which the page
// plays itself.
function nativeUpcoming() {
  const q = audio.queue;
  if (!q || !q.items.length) return [];
  const order = [];
  if (audio.repeat === 'one') order.push(q.items[q.index]);
  else {
    for (let k = 1; k <= 10; k++) {
      let i = q.index + k;
      if (i >= q.items.length) {
        if (audio.repeat !== 'all') break;
        i %= q.items.length;
      }
      order.push(q.items[i]);
    }
  }
  const out = [];
  for (const item of order) {
    if (!item || item.kind !== 'music' || isDownloaded(item)) break;
    const art = (mediaArtwork(item)[0] || {}).src;
    out.push({
      url: new URL(playPath(item), location.href).href,
      title: item.title || '',
      artist: (item.creators || []).join(', ') || item.subtitle || '',
      album: (item.extra && item.extra.album) || '',
      art: art ? new URL(art, location.href).href : '',
    });
  }
  return out;
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
  // The TV fetches its own songs.
  if (RA.on) return;
  const next = upcomingItem();
  // Where songs play natively (the Android app), the native player is handed
  // the next song instead, and moves into it by itself: gapless, and without
  // ever stopping between songs, which is what kept music going with the
  // screen off. A downloaded song plays in the page, so is not handed over.
  if (NATIVE_AUDIO) {
    // "After this song": nothing handed over to play on into.
    if (sleep.atSongEnd) return;
    const upcoming = nativeUpcoming();
    const url = upcoming.length ? upcoming[0].url : '';
    const sig = upcoming.map((u) => u.url).join('|');
    if (audio.nativeQueued !== sig) {
      audio.nativeQueued = sig;
      // Several songs ahead, with what each is, where the app can take them
      // (0.12): it plays on through them if Android ends the page in the
      // background. Older apps take the next song alone.
      if (window.soundstormApp.queueUpcoming) window.soundstormApp.queueUpcoming(upcoming);
      else window.soundstormApp.queueNext(url);
      // No copy of the next song reaches the page to be heard from, so it is
      // heard now, while this one plays out: otherwise the animations would
      // follow only the tempo for its first twenty seconds.
      if (url) hearAhead(next);
    }
    return;
  }
  if (!next || next.kind !== 'music' || isDownloaded(next)) return;
  // On a link already found slow a whole song cannot arrive in thirty
  // seconds; trying would only spend somebody's mobile data.
  if (slowLink) return;
  const key = selectionKey(next);
  if ((audio.preloaded && audio.preloaded.key === key) || audio.preloading === key) return;
  audio.preloading = key;
  const ctl = new AbortController();
  audio.preloadAbort = ctl;
  try {
    const resp = await fetch(playPath(next), { credentials: 'same-origin', signal: ctl.signal, cache: 'no-store' });
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
    if (audio.preloadAbort === ctl) audio.preloadAbort = null;
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
  const level = item && item.kind === 'music' ? levelFor(item)
    : item && item.kind === 'audiobook' ? 10 ** (bookGainDb() / 20) : 1;
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
      img.fetchPriority = 'low';
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
  // Playing on a TV: the TV counts it.
  if (RA.on) return;
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
  // Three ways the screen can be laid out:
  //   queue  - Up next in the middle, a small cover beside the title;
  //   lyrics - the lyrics in the middle, big; on a phone the title centred at
  //            the top with no cover by it, on a computer the cover beside;
  //   cover  - an audiobook: the big cover and nothing else.
  // A phone's default used to be a fourth, the strip - the big cover with a
  // few small lines of lyrics under the title, tapped to grow. The owner
  // asked for the big lyrics only.
  //
  // A song keeps the lyrics layout whether or not it has lyrics, and while
  // they load: everything sits exactly where it would with them, and the
  // space is simply empty when there are none. Switching layout on the
  // answer made every song change jump once its lyrics arrived, and jump
  // back for a song without.
  const music = Boolean(audio.item && audio.item.kind === 'music');
  const mode = audio.showQueue ? 'queue' : !music ? 'cover' : 'lyrics';
  audio.npMode = mode;
  const showing = mode === 'lyrics' || mode === 'strip';
  show($('np-lyrics'), showing);
  show($('np-next-block'), mode === 'queue');
  // The panel layout - title at the top, the middle for lyrics or the queue,
  // controls at the bottom - whenever the middle is not the cover.
  $('now-playing').classList.toggle('panel-on', mode === 'lyrics' || mode === 'queue');
  $('now-playing').classList.toggle('strip-on', mode === 'strip');
  $('now-playing').classList.toggle('lyrics-on', mode === 'lyrics');
  const box = $('np-lyrics');
  box.replaceChildren();
  if (!has) {
    // No words for a song without lyrics - the space is left empty, with the
    // title where it always is (the owner's call; "No lyrics for this song"
    // used to sit there).
    box.classList.remove('unsynced');
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
        // On a touch screen a tap is for the next look (below), so a line
        // is not a place to jump to there; the timeline in the hold is.
        if (touchScreen()) return;
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
    // The line being sung sits at the middle of the screen - where the
    // visualizers are centred - not of the box, which starts under the
    // title. Measured against the box itself: offsetTop counts from the
    // nearest positioned ancestor, which is not the box, and parked the line
    // near the top.
    const top = box.getBoundingClientRect().top;
    const offset = current.getBoundingClientRect().top - top + box.scrollTop;
    const np = $('now-playing').getBoundingClientRect();
    const middle = np.top + np.height / 2 - top;
    box.scrollTo({ top: offset + current.clientHeight / 2 - middle, behavior: force ? 'auto' : 'smooth' });
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

// A kept file's blob takes the type it was stored with; one stored as a page
// (HTML, XML, SVG, script) would open as a same-origin document in the reader's
// frame, outside the server's sandbox (a security review). Those are made
// plain bytes; songs, films, pictures and PDFs keep theirs.
async function safeBlob(resp) {
  const blob = await resp.blob();
  return /html|xml|svg|javascript|ecmascript/i.test(blob.type) ? new Blob([blob], { type: 'application/octet-stream' }) : blob;
}

async function offlineURL(item) {
  try {
    const cache = await caches.open(OFFLINE_CACHE);
    const resp = await cache.match(streamPath(item));
    return resp ? URL.createObjectURL(await safeBlob(resp)) : '';
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
  // Hear them now, while online, so the visualizer is ready offline.
  prepareDownloads();
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
      await cache.delete(heardURL(kept));
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
    localStorage.removeItem('soundstorm-owner');
    localStorage.removeItem('soundstorm.backupAsked');
    // Places kept for downloaded books are this person's too.
    for (const key of Object.keys(localStorage)) {
      if (key.startsWith('soundstorm-pos:') || key.startsWith('soundstorm-read:')) localStorage.removeItem(key);
    }
    await caches.delete(OFFLINE_CACHE);
    await caches.delete(OFFLINE_SHELL);
    // What was heard in songs says what this person played.
    localStorage.removeItem(HEARD_ORDER_KEY);
    await caches.delete(HEARD_CACHE);
    // And the queue kept for the phone's own player: their songs, in order.
    localStorage.removeItem(NATIVE_QUEUE_KEY);
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
  const wasAtSongEnd = sleep.atSongEnd;
  sleep.until = 0;
  sleep.atSongEnd = false;
  if (choice === 'song') {
    sleep.atSongEnd = true;
    // In the Android app the native player holds the next songs and moves
    // into them by itself: they are taken back, or "after this song" played
    // on into the next (a review).
    if (NATIVE_AUDIO) {
      audio.nativeQueued = '';
      window.soundstormApp.queueNext('');
    }
  } else if (Number(choice) > 0) {
    sleep.until = Date.now() + Number(choice) * 60000;
    sleep.tick = setInterval(sleepTick, 1000);
  }
  // Turned off before the song ended: the next songs are handed over again.
  if (NATIVE_AUDIO && wasAtSongEnd && !sleep.atSongEnd) {
    audio.nativeQueued = '';
    preloadNext();
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
    const h = Math.floor(secs / 3600);
    const m = Math.floor((secs % 3600) / 60);
    const sec = String(secs % 60).padStart(2, '0');
    label.textContent = h ? `Stops in ${h}:${String(m).padStart(2, '0')}:${sec}` : `Stops in ${m}:${sec}`;
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
  // Stepped by a timer, not animation frames: a phone with its screen off
  // draws no frames, so a fade driven by them never got past its first step
  // and the music played on - reported as the sleep timer not working, which
  // is the one time the screen is always off. A page playing sound keeps its
  // timers running.
  const fade = setInterval(() => {
    const t = Math.min(1, (performance.now() - began) / SLEEP_FADE_MS);
    player.volume = start * (1 - t);
    if (t < 1 && !player.paused) return;
    clearInterval(fade);
    player.pause();
    sleep.fading = false;
    // Back to the listener's own level for next time. The fade's own
    // volumechange events are still queued; released only after they have
    // been seen, so none of them is taken for the listener's choice.
    if (audio.item) applyLevel(audio.item);
    else player.volume = start;
    setTimeout(() => { audio.fading = false; }, 0);
  }, 100);
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
  if (RA.on) return 0;
  // Not where songs play natively (the Android app): the fade-in is a second
  // audio element in the page, beside a native player it cannot blend with.
  if (NATIVE_AUDIO) return 0;
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

// A song asked for but not yet sounding - starting, or waiting on the
// network mid-song - wears a turning ring round its play button, in Now
// Playing and on the mini-player, so a slow start reads as loading rather
// than as nothing happening. Shown only after a moment, so a song that starts
// at once never flickers one.
{
  const player = $('audio-player');
  let timer = 0;
  const set = (on) => {
    clearTimeout(timer);
    timer = 0;
    if (on) {
      timer = setTimeout(() => {
        for (const id of ['np-play', 'dock-play']) $(id).classList.add('loading');
      }, 350);
    } else {
      for (const id of ['np-play', 'dock-play']) $(id).classList.remove('loading');
    }
  };
  player.addEventListener('waiting', () => { if (!player.paused) set(true); });
  player.addEventListener('play', () => { if (player.readyState < 3) set(true); });
  for (const ev of ['playing', 'pause', 'ended', 'error', 'emptied']) player.addEventListener(ev, () => set(false));
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

// A playback report (the developer's own install, the Android app): the
// phone's player log and what the page thinks, kept on the server.
window.__soundstormPlayerLog = async (log) => {
  const player = $('audio-player');
  const page = {
    song: audio.item ? `${audio.item.title} (${audio.item.sourceId}/${audio.item.id})` : null,
    pageVolume: player.volume, level: audio.level, userVolume: audio.userVolume,
    paused: player.paused, at: player.currentTime, duration: player.duration,
    fading: Boolean(audio.fading), sleep: Boolean(sleep.fading), slowLink: Boolean(slowLink),
    hidden: document.hidden,
  };
  const { ok } = await api('/api/diagnostics/playback', { method: 'POST', body: JSON.stringify({ log, page }) });
  showToast(ok ? 'Report sent. Thank you.' : 'Could not send the report.');
};
$('player-report-send').addEventListener('click', () => window.soundstormApp.playerLog());

$('crossfade-select').value = String(crossfadeSeconds());
$('crossfade-select').disabled = !canSetVolume;
show($('crossfade-unavailable'), !canSetVolume);
$('crossfade-select').addEventListener('change', (event) => {
  localStorage.setItem(FADE_KEY, event.target.value);
  note($('playback-note'), 'Saved.', false);
});

// Keep the screen on: never, while Now Playing is open (to watch a
// visualizer, or a phone on a stand), or whenever the app is on screen. Per
// device, like the rest of this card. A screen wake lock, which the browser
// drops whenever the page is hidden, so it is asked for again on coming back.
// Only on a secure address; the reader holds its own while reading along.
const AWAKE_KEY = 'soundstorm-screen-awake';
const awake = { lock: null, asking: false };
function awakeMode() {
  try { return localStorage.getItem(AWAKE_KEY) || 'off'; } catch { return 'off'; }
}
async function updateAwake() {
  if (!('wakeLock' in navigator)) return;
  const want = wantAwake();
  if (want && !awake.lock && !awake.asking) {
    awake.asking = true;
    try {
      awake.lock = await navigator.wakeLock.request('screen');
      awake.lock.addEventListener('release', () => { awake.lock = null; });
    } catch { /* refused: battery saver, or not allowed here */ }
    awake.asking = false;
    // Things may have changed while it was being asked for.
    if (awake.lock && !wantAwake()) { awake.lock.release().catch(() => {}); awake.lock = null; }
  } else if (!want && awake.lock) {
    awake.lock.release().catch(() => {});
    awake.lock = null;
  }
}
function wantAwake() {
  const mode = awakeMode();
  return document.visibilityState === 'visible'
    && (mode === 'app' || (mode === 'np' && !$('now-playing').classList.contains('hidden')));
}
$('awake-select').value = awakeMode();
$('awake-select').disabled = !('wakeLock' in navigator);
show($('awake-unavailable'), !('wakeLock' in navigator));
$('awake-select').addEventListener('change', (event) => {
  try { localStorage.setItem(AWAKE_KEY, event.target.value); } catch { /* this session only */ }
  note($('playback-note'), 'Saved.', false);
  updateAwake();
});
document.addEventListener('visibilitychange', updateAwake);
new MutationObserver(updateAwake).observe($('now-playing'), { attributes: true, attributeFilter: ['class'] });
updateAwake();

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
    { kind: 'places', label: 'Places' }, { kind: 'photo-videos', label: 'Videos' },
    { kind: 'photo-live', label: 'Live photos' }, { kind: 'fav-photos', label: 'Favorites' }],
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
const BOOK_BROWSE = new Set(['authors', 'series', 'people', 'places', 'photo-videos', 'photo-live', 'genres-music', 'genres-watch', 'genres-books']);
// Each tab's genres are of these shelves.
const GENRE_KINDS = { 'genres-music': ['music'], 'genres-watch': ['video', 'tv'], 'genres-books': ['audiobook', 'ebook'] };
// A category the + button starts with put away: nobody's tab changes until they add it.
const DEFAULT_HIDDEN = { music: ['genres'], watch: ['genres-watch'], books: ['genres-books'] };
const PHOTO_BROWSE = new Set(['people', 'places', 'photo-videos', 'photo-live']);
// The Photos tab's kinds of picture, asked of the photo server by type.
const PHOTO_TYPES = { 'photo-videos': 'video', 'photo-live': 'live' };
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
  if (kind === 'people' || kind === 'places' || PHOTO_TYPES[kind]) return shelfAvailable('picture');
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

// Now Playing's cover has four looks, a tap moving to the next: the cover,
// a spinning disc, a record (the cover as its label, the song's name round
// it), and moving with the music (pulsing at the song's tempo). Kept on the
// account, so it stays the way somebody left it, on every device.
// The looks in three groups: the cover itself, visualizers where the cover
// is, and visualizers filling the whole screen. The visualizers all follow the
// song itself (see listenTo and viz).
const COVER_GROUPS = [
  // Lyrics is a look of its own (the owner's asking): on a phone the title
  // is always at the top and everything under it is the chosen look - the
  // lyrics, the cover centred on the screen, or a visualizer - one at a time.
  { name: 'Lyrics and covers', styles: ['lyrics', 'square', 'spin', 'vinyl'] },
  // Every visualizer fills the screen, centred on it (the owner's asking:
  // the six that drew where the cover is had nowhere to draw once a phone's
  // Now Playing became the lyrics alone).
  { name: 'Visualizers', styles: ['pulse', 'bars', 'warp', 'waves', 'kaleido', 'fireworks',
    'flow', 'storm', 'synthwave', 'galaxy', 'aurora', 'lava', 'analysis'] },
];
const COVER_STYLES = COVER_GROUPS.flatMap((g) => g.styles);
const COVER_STYLE_NAMES = {
  lyrics: 'Lyrics', square: 'Cover', spin: 'Spinning disc', vinyl: 'Record', pulse: 'Orb', bars: 'Spectrum',
  warp: 'Warp', waves: 'Waves', kaleido: 'Kaleidoscope', fireworks: 'Fireworks',
  flow: 'Flow', storm: 'Storm', synthwave: 'Synthwave', galaxy: 'Galaxy', aurora: 'Aurora', lava: 'Lava',
  analysis: 'Analysis',
};
const FULL_STYLES = COVER_GROUPS[1].styles;
const EX_COVER_VIZ = ['pulse', 'bars', 'warp', 'waves', 'kaleido', 'fireworks'];
const VIZ_STYLES = FULL_STYLES;
function coverStyle() {
  const p = state.prefs || {};
  // An audiobook is always its plain cover: the looks are for music.
  if (audio.item && audio.item.kind !== 'music') return 'square';
  if (COVER_STYLES.includes(p.coverStyle)) return p.coverStyle;
  return p.coverSpin ? 'spin' : 'lyrics';
}
function coverSpins() {
  return coverStyle() === 'spin';
}

$('np-cover').addEventListener('click', (event) => {
  // The end of a hold that opened the menu is not a tap, and must not reach
  // the page as a click outside the menu, which would close it.
  if (performance.now() - npHold.at < 700) {
    event.stopPropagation();
    return;
  }
});
// On a touch screen a double tap anywhere on Now Playing moves to the next
// look, in the Looks sheet's order and round again - lyrics, the covers, then
// every visualizer - while a hold still brings up the buttons. A single tap
// did it first, and changed the look by accident (the owner's asking): two
// taps within 350ms and 40px of each other. Not on the title (the owner
// asked for a tap there to do nothing), nor on anything that answers a tap
// of its own: Up next, the Looks sheet, a menu. The end of a hold or a swipe
// is not a tap.
let lookTap = null;
$('now-playing').addEventListener('click', (event) => {
  if (!touchScreen() || !audio.item) return;
  if (performance.now() - npHold.at < 700) return;
  if (npSwipe.busy || performance.now() - (npSwipe.draggedAt || 0) < 400) return;
  if (event.target.closest('.np-head, #np-queue, #np-next-block, #np-looks, #item-menu, input, a, .np-controls button, .np-bar button, .np-sleep-menu')) return;
  if (audio.npMode === 'queue' || audio.item.kind !== 'music') return;
  const now = performance.now();
  if (!lookTap || now - lookTap.at > 350 || Math.hypot(event.clientX - lookTap.x, event.clientY - lookTap.y) > 40) {
    lookTap = { at: now, x: event.clientX, y: event.clientY };
    return;
  }
  lookTap = null;
  const at = COVER_STYLES.indexOf(coverStyle());
  const next = COVER_STYLES[(at + 1) % COVER_STYLES.length];
  state.prefs = state.prefs || {};
  state.prefs.coverStyle = next;
  applyCoverStyle();
  showToast(COVER_STYLE_NAMES[next], '', null, 1400);
  savePrefs({ coverStyle: next });
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

  // One tap to music, right as the app opens: shuffle everything, and the
  // other ways to just start something.
  const quick = homeQuickPlay(favs);
  $('home-quick').replaceChildren(...(quick ? [quick] : []));
  showHome(true);

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

// homeQuickPlay: big buttons at the top of Home that start music with one
// tap, no browsing - Shuffle all (every song, endless, never repeating until
// the library has been through), your favorites shuffled, Library radio
// (everything, leaning to what you love), and what is newest. Only where the
// account has music.
function homeQuickPlay(favs) {
  if (!shelfAvailable('music')) return null;
  const hasFavs = ((favs && favs.ok && favs.body && favs.body.items) || []).some((it) => it.kind === 'music');
  const actions = [
    // Named for music, since Home holds every shelf: "Shuffle all" alone read
    // as though it might mean films and books too.
    { icon: 'shuffle', label: 'Shuffle all music', run: () => startRadio({ mode: 'shuffle' }) },
    hasFavs && { icon: 'heart', label: 'Favorite songs', run: () => playMix('favorites') },
    { icon: 'radio', label: 'Music radio', run: () => startRadio({ mode: 'library' }) },
    { icon: 'sparkle', label: 'New music', run: () => playMix('recently-added') },
  ].filter(Boolean);
  const row = document.createElement('div');
  row.className = 'home-quick';
  row.append(...actions.map((a, i) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = `home-quick-btn${i === 0 ? ' primary' : ''}`;
    b.append(icon(a.icon, a.icon === 'heart'));
    const label = document.createElement('span');
    label.textContent = a.label;
    b.append(label);
    b.addEventListener('click', async () => {
      if (b.disabled) return;
      b.disabled = true;
      try { await a.run(); } finally { b.disabled = false; }
    });
    return b;
  }));
  return row;
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
    showHome(false);
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
      // The moving cover's glow: the whole cover's colour, brightened.
      const all = g.getImageData(0, 0, 16, 16).data;
      let rr = 0, gg = 0, bb = 0;
      for (let i = 0; i < all.length; i += 4) { rr += all[i]; gg += all[i + 1]; bb += all[i + 2]; }
      const k = all.length / 4;
      const top = Math.max(rr, gg, bb) / k || 1;
      const lift = Math.min(2.2, 235 / top);
      $('now-playing').style.setProperty('--np-glow',
        `rgb(${[rr, gg, bb].map((v) => Math.round(Math.min(255, (v / k) * lift))).join(', ')})`);
      viz.palette = coverPalette(all);
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
      play(pair.audiobook, { underBook: true });
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
  play(pair.audiobook, { underBook: true });
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
  // As the chapter list: on a TV (and with a keyboard) the highlight starts
  // on the speed chosen.
  const chosen = opening && menu.querySelector('.chosen');
  if (chosen && !touchScreen()) {
    chosen.focus({ preventScroll: true });
    chosen.scrollIntoView({ block: 'nearest' });
  }
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
async function downloadBook(item, onProgress = () => {}, shouldStop, opts = {}) {
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
    const kept = await keepVideo(cache, item, onProgress, shouldStop, opts);
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

async function keepVideo(cache, item, onProgress, shouldStop, opts = {}) {
  // The version chosen when downloading (renderDownloadMenu); standard when
  // nobody chose - a whole shelf's Download all - since the original picture
  // of a Blu-ray would be tens of gigabytes on the device.
  const q = opts.vq || 'standard';
  const audioQuery = opts.audio !== undefined ? `&audio=${opts.audio}` : '';
  const { ok, body } = await api(`/api/playback/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}?vq=${q}${audioQuery}`);
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
  showHome(kind === '' && !words.length);
  show($('results-bar'), !(kind === '' && !words.length));
  show($('results'), !(kind === '' && !words.length));
  const kept = (k) => Object.values(state.downloads.items)
    .filter((it) => it && it.kind === k && it.sourceId !== 'storyteller' && matches(it));

  // Home: the same strips as online, one for each kind that is downloaded.
  if (kind === '' && !words.length) {
    // The quick buttons start music from the server, which is not there.
    $('home-quick').replaceChildren();
    showHome(true);
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
    await cache.delete(heardURL(kept));
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
// Home and its quick buttons, which sit above the Continue row rather than in
// Home's own section: shown and hidden together.
function showHome(on) {
  show($('home-view'), on);
  show($('home-quick'), on && $('home-quick').childElementCount > 0);
}

function enterDetailPage() {
  state.detailPage = true;
  show($('continue'), false);
  showHome(false);
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
  if (PHOTO_TYPES[state.kind]) {
    await showPhotoType(seq);
    return;
  }
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

// Videos and Live photos: every clip, or every Live Photo, newest first, as
// a grid like the camera roll's. A Live Photo opens in the viewer, whose LIVE
// button plays its moving part; a clip plays as a film.
async function showPhotoType(seq) {
  const view = $('music-view');
  const type = PHOTO_TYPES[state.kind];
  $('status').textContent = '';
  if (!view.children.length) showSkeleton(view, 'grid');
  const { ok, body } = await api(`/api/photos/of?${new URLSearchParams({ type })}`);
  if (seq !== state.searchSeq) return;
  if (!ok || !body) {
    view.replaceChildren();
    $('status').textContent = 'Could not load those.';
    return;
  }
  const words = state.query.toLowerCase().split(/\s+/).filter(Boolean);
  const items = (body.items || []).filter((it) => words.every((w) =>
    `${it.title} ${it.subtitle || ''} ${(it.extra && it.extra.place) || ''}`.toLowerCase().includes(w)));
  state.items = items;
  const grid = document.createElement('div');
  grid.className = 'grid browse-grid';
  grid.append(...items.map(renderItem));
  view.replaceChildren(grid);
  $('status').textContent = items.length ? ''
    : (state.query ? 'Nothing matches.'
      : (type === 'video' ? 'No videos in your photos yet.'
        : 'No Live Photos yet. iPhone Live Photos and Android motion photos show here.'));
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
    img.fetchPriority = 'low';
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

// The film's quality, in the player: another choice plays on from the same
// moment, at the same audio track, and holds - across episodes too - until
// the player is closed, when the device's setting applies again.
function attachQualityChoice(item, vq) {
  const select = $('video-quality-select');
  select.value = vq;
  select.onchange = async () => {
    state.videoQuality = select.value;
    await saveWatchPosition(true);
    playVideo(item, state.videoAudio !== undefined ? { audio: state.videoAudio } : {});
  };
  show($('quality-picker'), true);
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
  if (!state.offline) {
    out.push(menuItem('cast', 'Play on another device', (event) => {
      event.stopPropagation();
      closeItemMenu();
      openControlSheet();
    }, { chevron: true }));
  }
  if (item.kind === 'music') {
    out.push(menuItem('image', 'Cover look', () => {
      closeItemMenu();
      if ($('now-playing').classList.contains('hidden')) openNowPlaying();
      openLooks();
    }, { detail: COVER_STYLE_NAMES[coverStyle()] }));
  }
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
    menuItem('moon', 'Custom time', (event) => {
      event.stopPropagation();
      renderSleepCustom(item, opts);
    }),
    menuItem('moon', item.kind === 'audiobook' ? 'At the end of this chapter' : 'At the end of this song',
      choose('song', item.kind === 'audiobook' ? 'Stops after this chapter.' : 'Stops after this song.')),
  ];
  if (sleep.until || sleep.atSongEnd) rows.push(menuItem('close', 'Turn off', choose(null, 'Sleep timer off.')));
  menu.replaceChildren(back, ...rows);
  placeMenu(menu, state.menuAnchor);
}

// A sleep timer of any length: hours and minutes, typed or stepped, and
// Start. The minutes are remembered on this device for next time.
const SLEEP_CUSTOM_KEY = 'soundstorm-sleep-custom';
function renderSleepCustom(item, opts) {
  const menu = $('item-menu');
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const label = document.createElement('span');
  label.textContent = 'Custom time';
  back.append(label);
  back.addEventListener('click', (event) => {
    event.stopPropagation();
    renderSleepMenu(item, opts);
  });
  let total = Math.max(1, Math.min(720, Number(localStorage.getItem(SLEEP_CUSTOM_KEY)) || 90));
  const box = document.createElement('div');
  box.className = 'sleep-custom';
  const field = (unit, max) => {
    const wrap = document.createElement('label');
    wrap.className = 'sleep-field';
    const input = document.createElement('input');
    input.type = 'number';
    input.inputMode = 'numeric';
    input.min = '0';
    input.max = String(max);
    input.setAttribute('aria-label', unit);
    const name = document.createElement('span');
    name.textContent = unit;
    wrap.append(input, name);
    return { wrap, input };
  };
  const hours = field('hours', 12);
  const mins = field('minutes', 59);
  const show = () => {
    hours.input.value = String(Math.floor(total / 60));
    mins.input.value = String(total % 60);
  };
  const read = () => {
    const h = Math.max(0, Math.min(12, Math.floor(Number(hours.input.value) || 0)));
    const m = Math.max(0, Math.min(59, Math.floor(Number(mins.input.value) || 0)));
    total = Math.max(1, Math.min(720, h * 60 + m));
  };
  for (const f of [hours, mins]) {
    f.input.addEventListener('click', (event) => event.stopPropagation());
    f.input.addEventListener('change', () => { read(); show(); });
  }
  const step = (by, text) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'sleep-step';
    b.textContent = text;
    b.addEventListener('click', (event) => {
      event.stopPropagation();
      read();
      total = Math.max(5, Math.min(720, total + by));
      show();
    });
    return b;
  };
  const steps = document.createElement('div');
  steps.className = 'sleep-steps';
  steps.append(step(-15, '-15'), step(-5, '-5'), step(5, '+5'), step(15, '+15'));
  const fields = document.createElement('div');
  fields.className = 'sleep-fields';
  fields.append(hours.wrap, mins.wrap);
  const start = document.createElement('button');
  start.type = 'button';
  start.className = 'sleep-start';
  start.textContent = 'Start';
  const go = (event) => {
    if (event) event.stopPropagation();
    read();
    localStorage.setItem(SLEEP_CUSTOM_KEY, String(total));
    setSleep(String(total));
    closeItemMenu();
    const h = Math.floor(total / 60);
    const m = total % 60;
    showToast(`Stops in ${[h ? `${h} hour${h === 1 ? '' : 's'}` : '', m ? `${m} minute${m === 1 ? '' : 's'}` : ''].filter(Boolean).join(' ')}.`);
  };
  start.addEventListener('click', go);
  for (const f of [hours, mins]) {
    f.input.addEventListener('keydown', (event) => { if (event.key === 'Enter') go(event); });
  }
  box.append(fields, steps, start);
  show();
  menu.replaceChildren(back, box);
  placeMenu(menu, state.menuAnchor);
}

// Anywhere on the screen, not only the cover: a hold on the background,
// the title or the lyrics opens the menu too. Not on what already answers a
// touch - play, the header's buttons, the timeline, Up next (a hold there
// moves a song), a menu.
const NP_HOLD_SKIP = 'input, a, #np-queue, .np-bar button, .np-controls button, #np-speed-wrap, #item-menu, #np-looks, #np-chapters';
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
      showHoldIcons(event.pointerId, x, y);
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

/* ------------------------------------------ Now Playing's options, on hold */

// Holding anywhere on Now Playing shows its options as icons over the cover,
// where buttons used to sit, for as long as the finger stays down: slide
// onto one and it grows and says what it is; let go on it and it is done;
// let go anywhere else and they are gone. An icon counts only once the
// finger has moved onto it, so the one that appears under a still thumb is
// not chosen by lifting it. With the cover hidden (a phone's lyrics or Up
// next) they appear in a square in the middle of the screen.
const HOLD_SPOTS = ['tl', 'tm', 'tr', 'ml', 'c', 'mr', 'bl', 'bm', 'br'];

function holdIconList(item) {
  const music = item.kind === 'music';
  const faved = state.favorites.has(selectionKey(item));
  const downloaded = isDownloaded(item);
  const menuAt = (render) => {
    state.menuFor = item;
    state.menuAnchor = $('np-cover').offsetWidth ? $('np-cover') : $('np-title');
    state.menuOpts = { nowPlaying: true };
    render();
    placeMenu($('item-menu'), state.menuAnchor);
  };
  // Each name says what letting go on it will do, not how things are now:
  // "Turn shuffle off" while it is on.
  const repeatNext = { off: 'Repeat all', all: 'Repeat this song', one: 'Turn repeat off' }[audio.repeat] || 'Repeat all';
  const list = [
    { spot: 'tl', icon: 'info', label: 'Show info', run: () => menuAt(() => renderInfoMenu(item)) },
    { spot: 'tm', icon: 'moon', label: sleep.until || sleep.atSongEnd ? 'Change the sleep timer' : 'Set a sleep timer',
      on: Boolean(sleep.until || sleep.atSongEnd), run: () => menuAt(() => renderSleepMenu(item, { nowPlaying: true })) },
    { spot: 'tr', icon: 'heart', label: faved ? 'Remove from favorites' : 'Add to favorites', filled: faved, heart: true,
      run: async () => {
        const problem = await setFavorite(item, !faved);
        showToast(problem || (faved ? 'Removed from favorites.' : 'Added to favorites.'));
      } },
    { spot: 'bl', icon: 'download', label: downloaded ? 'Remove from this device' : 'Download to this device', on: downloaded,
      run: async () => {
        if (downloaded) {
          await removeItemDownload(item);
          showToast(`${item.title} removed from this device.`);
        } else if (music) {
          await downloadWithToast(item.title, (progress) => download({ id: `song:${selectionKey(item)}`, type: 'song',
            title: item.title, subtitle: (item.creators || []).join(', '), sourceId: item.sourceId, artId: item.artId },
          [item], (k, total) => progress(k / total)));
        } else {
          await downloadWithToast(item.title, (progress) => downloadBook(item, progress));
        }
      } },
  ];
  if (music) {
    list.push(
      { spot: 'ml', icon: 'shuffle', label: audio.shuffle ? 'Turn shuffle off' : 'Turn shuffle on', on: audio.shuffle,
        run: () => {
          setShuffle(!audio.shuffle);
          showToast(`Shuffle ${audio.shuffle ? 'on' : 'off'}.`);
        } },
      // A plain plus: Up next's list icon is too like the playlist one.
      { spot: 'c', icon: 'plus', label: 'Add to playlist',
        run: async () => {
          const { ok, body } = await api('/api/playlists');
          menuAt(() => renderPlaylistMenu(item, (ok && body && body.playlists) || []));
        } },
      { spot: 'mr', icon: 'repeat', label: repeatNext, on: audio.repeat !== 'off', one: audio.repeat === 'one',
        run: () => {
          cycleRepeat();
          showToast(`Repeat ${{ off: 'off', all: 'all', one: 'this song' }[audio.repeat] || 'off'}.`);
        } },
      { spot: 'bm', icon: 'queue', label: audio.showQueue ? 'Hide up next' : 'Show up next', on: audio.showQueue,
        run: () => {
          audio.showQueue = !audio.showQueue;
          renderLyrics();
        } },
      { spot: 'br', icon: 'radio', label: 'Play songs like this next', run: () => playLikeThis() },
    );
  }
  return list;
}

// holdButtonList: the Now Playing buttons that a touch screen keeps
// invisible until a hold, with what letting go on each will do.
function holdButtonList() {
  const book = Boolean(audio.item && audio.item.kind !== 'music');
  return [
    { id: 'np-close', label: 'Close Now Playing' },
    { id: 'np-chapters-btn', label: 'Chapters', icon: 'queue' },
    { id: 'np-speed', label: 'Playback speed' },
    { id: 'np-cast', label: 'Play on another device', icon: 'cast' },
    { id: 'np-looks-btn', label: 'Cover looks' },
    { id: 'np-exit', label: 'Stop and close' },
    { id: 'np-prev', label: book ? 'Back 30 seconds' : 'Previous' },
    { id: 'np-next', label: book ? 'Forward 30 seconds' : 'Next' },
  ];
}
function showHoldIcons(pointerId, x0, y0) {
  const item = audio.item;
  if (!item) return;
  const layer = $('np-hold-layer');
  const box = $('np-hold-icons');
  const caption = $('np-hold-caption');
  // Over the cover where it shows; otherwise a square in the middle.
  const cover = $('np-cover');
  let r = null;
  if (cover.offsetWidth) {
    // Its own size about its centre, not its bounding box: a spinning record
    // is rotated, and the box around a turned square is up to 1.4 times as
    // wide - which drew the veil as a dark ring around the disc.
    const b = cover.getBoundingClientRect();
    const w = cover.offsetWidth;
    const h = cover.offsetHeight;
    r = { left: b.left + b.width / 2 - w / 2, top: b.top + b.height / 2 - h / 2, width: w, height: h };
  }
  if (!r) {
    const side = Math.min(window.innerWidth * 0.8, window.innerHeight * 0.6, 360);
    r = { left: (window.innerWidth - side) / 2, top: (window.innerHeight - side) / 2, width: side, height: side };
  }
  Object.assign(layer.style, { left: `${r.left}px`, top: `${r.top}px`, width: `${r.width}px`, height: `${r.height}px` });
  layer.classList.toggle('round', $('now-playing').classList.contains('round') && cover.offsetWidth > 0);
  // Over a visualizer a dark square hides the animation: each icon gets a
  // small dark glow of its own instead.
  layer.classList.toggle('viz', $('now-playing').classList.contains('cover-viz'));
  const icons = holdIconList(item);
  box.replaceChildren(...icons.map((it) => {
    const b = document.createElement('span');
    b.className = `np-hold-icon at-${it.spot}${it.on ? ' on' : ''}${it.heart && it.filled ? ' faved' : ''}${it.one ? ' one' : ''}`;
    b.append(icon(it.icon, it.filled));
    b.soundstormHold = it;
    return b;
  }));
  // On a touch screen the screen's own buttons - close, looks, stop, play,
  // and speed for an audiobook - are invisible (asked for so nothing sits on
  // the screen but the music), and appear here while the finger is down,
  // each where it always is, chosen the same way as the rest.
  const extra = $('np-hold-extra');
  const coarse = touchScreen();
  extra.replaceChildren(...(coarse ? holdButtonList() : []).flatMap((it) => {
    const el = $(it.id);
    const b = el && el.getBoundingClientRect();
    if (!b || !b.width || el.closest('.hidden')) return [];
    const s = document.createElement('span');
    s.className = 'np-hold-icon np-hold-button';
    const svg = el.querySelector('svg');
    if (it.icon) s.append(icon(it.icon));
    else if (svg) s.append(svg.cloneNode(true));
    else s.textContent = el.textContent;
    s.style.left = `${b.left + b.width / 2}px`;
    s.style.top = `${b.top + b.height / 2}px`;
    // Its own click, so the button behaves exactly as tapped - past the rule
    // that swallows the click the lift makes.
    s.soundstormHold = { label: it.label, run: () => {
      const until = state.swallowClickUntil;
      state.swallowClickUntil = 0;
      el.click();
      state.swallowClickUntil = until;
    } };
    return [s];
  }));
  caption.textContent = '';
  show(layer, true);
  show(extra, extra.children.length > 0);
  // The caption names the icon under the finger where the title was.
  $('now-playing').classList.add('hold-icons');
  const t = $('np-title').getBoundingClientRect();
  Object.assign(caption.style, { position: 'fixed', left: `${t.left}px`, width: `${t.width}px`,
    top: `${t.top}px`, bottom: 'auto', lineHeight: `${t.height}px` });

  let moved = false;
  let lit = null;
  const light = (el) => {
    if (lit === el) return;
    if (lit) lit.classList.remove('lit');
    lit = el;
    if (lit) lit.classList.add('lit');
    caption.textContent = lit ? lit.soundstormHold.label : '';
  };
  // Found by where the finger is over each icon's own circle, with a little
  // room around it, rather than by what is on top there.
  const iconAt = (x, y) => [...box.children, ...extra.children].find((el) => {
    const b = el.getBoundingClientRect();
    const cx = b.left + b.width / 2;
    const cy = b.top + b.height / 2;
    return Math.hypot(x - cx, y - cy) <= b.width * 0.75;
  }) || null;
  // The timeline is hidden on a touch screen too, and shows while holding:
  // sliding onto it (anywhere in a band around it) moves the position under
  // the finger, and letting go there seeks.
  let seekTo = null;
  const player = $('audio-player');
  const seekAt = (x, y) => {
    if (!coarse || !Number.isFinite(player.duration)) return null;
    const b = $('np-seek').getBoundingClientRect();
    if (!b.width || y < b.top - 36 || y > b.bottom + 36) return null;
    return Math.max(0, Math.min(1, (x - b.left) / b.width));
  };
  const onMove = (event) => {
    if (event.pointerId !== pointerId) return;
    if (!moved && Math.hypot(event.clientX - x0, event.clientY - y0) < HOLD_SLOP) return;
    moved = true;
    seekTo = seekAt(event.clientX, event.clientY);
    if (seekTo !== null) {
      light(null);
      state.seeking = true;
      $('np-seek').value = String(Math.round(seekTo * 1000));
      const at = formatDuration(seekTo * player.duration) || '0:00';
      $('np-time').textContent = at;
      caption.textContent = `Play from ${at}`;
      return;
    }
    if (state.seeking) { state.seeking = false; syncNowPlayingTime(); }
    light(iconAt(event.clientX, event.clientY));
  };
  const onEnd = (event) => {
    if (event.pointerId !== pointerId) return;
    document.removeEventListener('pointermove', onMove);
    document.removeEventListener('pointerup', onEnd);
    document.removeEventListener('pointercancel', onEnd);
    // Whatever is under the finger when it lifts is chosen, moved or not: a
    // thumb that came down on play and stayed there means play (the owner's
    // asking - it used to take moving off and back on).
    const chosen = event.type === 'pointerup' && lit ? lit.soundstormHold : null;
    if (event.type === 'pointerup' && seekTo !== null) player.currentTime = seekTo * player.duration;
    state.seeking = false;
    light(null);
    show(layer, false);
    show(extra, false);
    $('now-playing').classList.remove('hold-icons');
    // The lift is not a tap on whatever is under it.
    state.swallowClickUntil = performance.now() + 500;
    setTimeout(() => { state.suppressClick = false; }, 500);
    if (chosen) chosen.run();
  };
  // Lit at once if the finger is already on one as they appear.
  light(iconAt(x0, y0));
  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup', onEnd);
  document.addEventListener('pointercancel', onEnd);
}

/* ------------------------------------------------------ importing playlists */

// Playlists from another player - Plexamp by way of Plex, iTunes, Jellyfin,
// anything that saves M3U. The server finds each song on the music shelf
// (by where its file sat, then by artist, title and length) and says which it
// could not; nothing is guessed. Each file becomes one playlist, in its order.
function importPlaylistButton() {
  const holder = document.createElement('span');
  holder.className = 'playlist-import-holder';
  const card = document.createElement('button');
  card.type = 'button';
  card.className = 'playlist-action';
  card.append(icon('upload'));
  const t = document.createElement('span');
  t.textContent = 'Import';
  card.append(t);
  card.title = 'Import playlists from Plex or a file';
  const file = document.createElement('input');
  file.type = 'file';
  file.accept = '.m3u,.m3u8,audio/x-mpegurl,audio/mpegurl,application/vnd.apple.mpegurl';
  file.multiple = true;
  file.hidden = true;
  file.addEventListener('change', () => {
    const files = [...file.files];
    file.value = '';
    if (files.length) importPlaylists(files);
  });
  card.addEventListener('click', (event) => {
    event.stopPropagation();
    openImportMenu(card, file);
  });
  holder.append(card, file);
  return holder;
}

// Where playlists can come from: Plex (what Plexamp plays from) or an M3U
// file from any player.
function openImportMenu(anchor, file) {
  const menu = $('item-menu');
  state.menuFor = 'import';
  state.menuAnchor = anchor;
  state.menuOpts = {};
  const head = document.createElement('div');
  head.className = 'menu-head';
  const t = document.createElement('strong');
  t.textContent = 'Import playlists';
  const sub = document.createElement('span');
  sub.textContent = 'Songs are matched to your library';
  head.append(t, sub);
  menu.replaceChildren(head,
    menuItem('upload', 'From Plex or Plexamp', (event) => {
      closeItemMenu();
      startPlexImport(event);
    }),
    menuItem('playlist', 'From a file (M3U)', () => {
      closeItemMenu();
      file.click();
    }));
  placeMenu(menu, anchor);
}

// readPlaylistText decodes a playlist file: .m3u8 is UTF-8 by definition; a
// plain .m3u is UTF-8 from anything recent and Windows-1252 from older
// players (iTunes on Windows), so UTF-8 is tried strictly first.
async function readPlaylistText(file) {
  const bytes = await file.arrayBuffer();
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  } catch {
    return new TextDecoder('windows-1252').decode(bytes);
  }
}

async function importPlaylists(files) {
  showToast(files.length > 1 ? `Importing ${files.length} playlists...` : `Importing ${files[0].name}...`, '', null, 60000);
  const results = [];
  for (const f of files) {
    const name = f.name.replace(/\.m3u8?$/i, '');
    let text;
    try {
      text = await readPlaylistText(f);
    } catch {
      results.push({ name, error: 'Could not read the file.' });
      continue;
    }
    const { ok, body } = await api('/api/playlists/import', { method: 'POST', body: JSON.stringify({ name, m3u: text }) });
    if (ok && body && body.playlist) {
      results.push({ name: body.playlist.name, id: body.playlist.id, added: body.added, total: body.total,
        missing: body.missing || [], missingCount: body.missingCount || 0 });
    } else {
      results.push({ name, error: (body && body.error) || 'Could not import it.', missing: (body && body.missing) || [] });
    }
  }
  const done = results.filter((r) => r.id);
  if (files.length === 1 && done.length === 1 && !done[0].missingCount) {
    showToast(`Imported ${done[0].name}: all ${done[0].added} songs.`);
    showPlaylist(done[0].id);
    return;
  }
  showToast(done.length
    ? `Imported ${done.length} of ${results.length} playlist${results.length === 1 ? '' : 's'}.`
    : 'Nothing was imported.');
  if (!$('playlists-view').classList.contains('hidden')) showPlaylists(importReport(results));
}

// importReport says, per file, how many songs came across and which did not,
// above the playlists until the page is left.
function importReport(results) {
  const box = document.createElement('div');
  box.className = 'import-report';
  for (const r of results) {
    const row = document.createElement('details');
    row.className = 'import-row';
    const head = document.createElement('summary');
    if (r.id) {
      head.textContent = r.missingCount
        ? `${r.name}: ${r.added} of ${r.total} songs. ${r.missingCount} not in your library.`
        : `${r.name}: all ${r.added} songs.`;
    } else {
      head.textContent = `${r.name}: ${r.error}`;
    }
    row.append(head);
    if (r.missing && r.missing.length) {
      const list = document.createElement('ul');
      for (const m of r.missing) {
        const li = document.createElement('li');
        li.textContent = m;
        list.append(li);
      }
      const more = (r.missingCount || r.missing.length) - r.missing.length;
      if (more > 0) {
        const li = document.createElement('li');
        li.className = 'muted';
        li.textContent = `and ${more} more`;
        list.append(li);
      }
      row.append(list);
    } else {
      row.classList.add('plain');
    }
    box.append(row);
  }
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'import-report-close';
  close.setAttribute('aria-label', 'Dismiss');
  close.append(icon('close'));
  close.addEventListener('click', () => box.remove());
  box.append(close);
  return box;
}

/* ------------------------------------------------------ importing from Plex */

// Sign in on Plex's own page (a new tab; SoundStorm never sees the password),
// then pick playlists. The server holds the Plex token for the import and
// forgets it after; the page only ever sees names. See internal/plex.
const plexImport = { timer: null, panel: null, servers: [], server: '', lists: [] };

function plexPanel() {
  let panel = plexImport.panel;
  if (!panel || !panel.isConnected) {
    panel = document.createElement('section');
    panel.className = 'plex-panel';
    panel.setAttribute('aria-live', 'polite');
    plexImport.panel = panel;
    const view = $('playlists-view');
    view.insertBefore(panel, view.querySelector('.playlist-grid'));
  }
  return panel;
}

function plexPanelHead(title) {
  const head = document.createElement('div');
  head.className = 'plex-head';
  const h = document.createElement('h2');
  h.textContent = title;
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'import-report-close';
  close.setAttribute('aria-label', 'Cancel');
  close.append(icon('close'));
  close.addEventListener('click', stopPlexImport);
  head.append(h, close);
  return head;
}

function stopPlexImport() {
  clearInterval(plexImport.timer);
  plexImport.timer = null;
  document.removeEventListener('visibilitychange', plexVisible);
  if (plexImport.panel) plexImport.panel.remove();
  plexImport.panel = null;
  api('/api/plex', { method: 'DELETE' });
}

function plexVisible() {
  if (document.visibilityState === 'visible') checkPlex();
}

async function startPlexImport() {
  // The tab has to open in the tap itself or the browser blocks it; it is
  // pointed at Plex once the server has a sign-in code.
  const tab = window.open('', '_blank');
  if (tab) tab.opener = null;
  const { ok, body } = await api('/api/plex/signin', { method: 'POST', body: '{}' });
  if (!ok || !body || !body.authUrl) {
    if (tab) tab.close();
    showToast((body && body.error) || 'Could not start the Plex sign-in.');
    return;
  }
  if (tab) tab.location = body.authUrl;
  const panel = plexPanel();
  const p = document.createElement('p');
  p.textContent = 'Sign in to Plex in the tab that opened, then come back here.';
  const again = document.createElement('a');
  again.href = body.authUrl;
  again.target = '_blank';
  again.rel = 'noopener noreferrer';
  again.textContent = tab ? 'Open the Plex sign-in again' : 'Open the Plex sign-in';
  panel.replaceChildren(plexPanelHead('Import from Plex'), p, again);
  clearInterval(plexImport.timer);
  plexImport.timer = setInterval(checkPlex, 2500);
  document.addEventListener('visibilitychange', plexVisible);
}

async function checkPlex() {
  if (!plexImport.panel || !plexImport.panel.isConnected) {
    clearInterval(plexImport.timer);
    return;
  }
  const { ok, body } = await api('/api/plex/status');
  if (!ok || !body) return;
  if (body.state === 'waiting') return;
  clearInterval(plexImport.timer);
  plexImport.timer = null;
  document.removeEventListener('visibilitychange', plexVisible);
  if (body.state !== 'ready') {
    const p = document.createElement('p');
    p.textContent = body.state === 'expired' ? 'The Plex sign-in ran out of time. Close this and try again.' : 'The Plex sign-in stopped. Close this and try again.';
    plexPanel().replaceChildren(plexPanelHead('Import from Plex'), p);
    return;
  }
  plexImport.servers = body.servers || [];
  if (!plexImport.servers.length) {
    const p = document.createElement('p');
    p.textContent = 'Signed in, but this Plex account has no server with music.';
    plexPanel().replaceChildren(plexPanelHead('Import from Plex'), p);
    return;
  }
  const own = plexImport.servers.find((s) => s.owned) || plexImport.servers[0];
  loadPlexPlaylists(own.id);
}

async function loadPlexPlaylists(serverId) {
  plexImport.server = serverId;
  const panel = plexPanel();
  const wait = document.createElement('p');
  wait.textContent = 'Finding your Plex server...';
  panel.replaceChildren(plexPanelHead('Import from Plex'), plexServerPicker(), wait);
  const { ok, body } = await api(`/api/plex/playlists?server=${encodeURIComponent(serverId)}`);
  if (!plexImport.panel || plexImport.server !== serverId) return;
  if (!ok || !body) {
    wait.textContent = (body && body.error) || 'That Plex server did not answer.';
    return;
  }
  plexImport.lists = body.playlists || [];
  if (!plexImport.lists.length) {
    wait.textContent = 'No music playlists on this server.';
    return;
  }
  renderPlexPlaylists();
}

function plexServerPicker() {
  const wrap = document.createElement('div');
  if (plexImport.servers.length < 2) return wrap;
  const select = document.createElement('select');
  select.className = 'plex-server';
  select.setAttribute('aria-label', 'Plex server');
  for (const s of plexImport.servers) {
    const o = document.createElement('option');
    o.value = s.id;
    o.textContent = s.name;
    o.selected = s.id === plexImport.server;
    select.append(o);
  }
  select.addEventListener('change', () => loadPlexPlaylists(select.value));
  wrap.append(select);
  return wrap;
}

function renderPlexPlaylists() {
  const panel = plexPanel();
  const list = document.createElement('div');
  list.className = 'plex-lists';
  const boxes = [];
  for (const pl of plexImport.lists) {
    const row = document.createElement('label');
    row.className = 'plex-list';
    const box = document.createElement('input');
    box.type = 'checkbox';
    box.checked = true;
    box.soundstormList = pl;
    boxes.push(box);
    const name = document.createElement('span');
    name.className = 'plex-list-name';
    name.textContent = pl.title;
    const count = document.createElement('span');
    count.className = 'plex-list-count';
    count.textContent = `${pl.count} song${pl.count === 1 ? '' : 's'}${pl.smart ? ', smart' : ''}`;
    row.append(box, name, count);
    list.append(row);
  }
  const go = document.createElement('button');
  go.type = 'button';
  go.className = 'plex-go';
  const label = () => {
    const n = boxes.filter((b) => b.checked).length;
    go.textContent = n ? `Import ${n} playlist${n === 1 ? '' : 's'}` : 'Choose playlists';
    go.disabled = !n;
  };
  for (const b of boxes) b.addEventListener('change', label);
  label();
  go.addEventListener('click', async () => {
    const chosen = boxes.filter((b) => b.checked).map((b) => ({ id: b.soundstormList.id, title: b.soundstormList.title }));
    go.disabled = true;
    go.textContent = `Importing ${chosen.length}...`;
    const results = [];
    // Fifty at a time, the most one request takes.
    for (let i = 0; i < chosen.length; i += 50) {
      const { ok, body } = await api('/api/plex/import', { method: 'POST',
        body: JSON.stringify({ server: plexImport.server, playlists: chosen.slice(i, i + 50) }) });
      if (ok && body && body.results) results.push(...body.results);
      else for (const c of chosen.slice(i, i + 50)) results.push({ name: c.title, error: (body && body.error) || 'Could not import it.' });
    }
    const done = results.filter((r) => r.id).length;
    stopPlexImport();
    showToast(done ? `Imported ${done} of ${results.length} playlist${results.length === 1 ? '' : 's'} from Plex.` : 'Nothing was imported.');
    showPlaylists(importReport(results));
  });
  const note = document.createElement('p');
  note.className = 'muted plex-note';
  note.textContent = 'Each becomes a playlist here. Songs not in your library are listed afterwards.';
  panel.replaceChildren(plexPanelHead('Import from Plex'), plexServerPicker(), list, note, go);
}

/* ------------------------------------------------------------ own covers */

// Somebody's own picture for a song or an album, shown to them alone: kept on
// the server per person (collections/art.go), keyed by what it replaces.
state.myArt = {};

async function loadMyArt() {
  const { ok, body } = await api('/api/myart');
  state.myArt = (ok && body && body.art) || {};
}

// An album cover's id carries a version after an underscore (Navidrome's
// al-<id>_<hash>), which changes when the file does; the person's choice
// should not.
function artKeyFor(sourceId, artId) {
  return `art:${sourceId}/${String(artId).replace(/^((?:al|mf|ar|pl)-[^_]+)_.*$/, '$1')}`;
}

// withOverride is the person's picture when they have one, carrying the
// original address and the song it was for after a #, so it can be put back
// (repaintCovers) without knowing which card an image belongs to. A fragment
// is never sent to the server.
function withOverride(orig, songKey, artKey) {
  const mine = state.myArt && ((songKey && state.myArt[songKey]) || (artKey && state.myArt[artKey]));
  if (!mine) return orig;
  return `${mine}#o=${encodeURIComponent(orig)}${songKey ? `&s=${encodeURIComponent(songKey)}` : ''}`;
}

// repaintCovers points every cover on the page at the person's current
// choice, after one changes.
function repaintCovers() {
  for (const img of document.querySelectorAll('img')) {
    const src = img.getAttribute('src') || '';
    let orig = '';
    let song = '';
    const at = src.indexOf('#o=');
    if (at >= 0) {
      const q = new URLSearchParams(src.slice(at + 1));
      orig = q.get('o') || '';
      song = q.get('s') || '';
    } else if (src.startsWith('/api/art/')) {
      orig = src;
    } else {
      continue;
    }
    const m = orig.replace(/\?size=\d+$/, '').match(/^\/api\/art\/([^/]+)\/(.+)$/);
    const artKey = m ? artKeyFor(decodeURIComponent(m[1]), m[2].split('/').map(decodeURIComponent).join('/')) : '';
    const next = withOverride(orig, song, artKey) || NO_COVER;
    if (next !== src) img.src = next;
  }
  if (audio.item) {
    const art = artPath(audio.item) || NO_COVER;
    $('audio-art').src = art;
    $('dock-backdrop').src = art;
    updateMediaSession();
    renderNowPlaying();
    renderQueue();
  }
}

// pickCoverImage asks for a picture and hands back a square JPEG of it, at
// most 1000px: covers are square, and a phone photo is several megabytes.
function pickCoverImage() {
  return new Promise((resolve) => {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = 'image/*';
    input.addEventListener('cancel', () => resolve(null), { once: true });
    input.addEventListener('change', async () => {
      const file = input.files && input.files[0];
      if (!file) { resolve(null); return; }
      try {
        const bmp = await createImageBitmap(file);
        const side = Math.min(bmp.width, bmp.height);
        const out = Math.min(1000, side);
        const c = document.createElement('canvas');
        c.width = out;
        c.height = out;
        c.getContext('2d').drawImage(bmp, (bmp.width - side) / 2, (bmp.height - side) / 2, side, side, 0, 0, out, out);
        c.toBlob((blob) => resolve(blob), 'image/jpeg', 0.9);
      } catch {
        showToast("That picture can't be opened here. Try a JPEG or PNG.");
        resolve(null);
      }
    }, { once: true });
    input.click();
  });
}

// The keys a choice replaces: a song alone, or an album - its cover and every
// song's, since a song with its own embedded picture has its own cover id.
async function coverKeys(target, scope) {
  if (scope === 'song') return { keys: [`song:${target.song.sourceId}/${target.song.id}`], songs: [target.song] };
  let songs = [];
  if (target.songs) {
    songs = await target.songs();
  } else {
    const s = target.song;
    const album = (s.extra && s.extra.album) || '';
    const artist = (s.creators || [])[0] || '';
    const { ok, body } = await api(`/api/search?q=${encodeURIComponent(album)}&kind=music&limit=200`);
    songs = ((ok && body && body.items) || []).filter((it) =>
      it.sourceId === s.sourceId && ((it.extra && it.extra.album) || '') === album && ((it.creators || [])[0] || '') === artist);
    if (!songs.some((it) => it.id === s.id)) songs.push(s);
  }
  const keys = new Set();
  const albumArt = target.album && target.album.artId;
  if (albumArt) keys.add(artKeyFor(target.album.sourceId, albumArt));
  for (const it of songs) if (it.artId) keys.add(artKeyFor(it.sourceId, it.artId));
  return { keys: [...keys], songs };
}

async function setCover(target, scope) {
  const blob = await pickCoverImage();
  if (!blob) return;
  closeItemMenu();
  const { keys, songs } = await coverKeys(target, scope);
  if (!keys.length) { showToast('That has no cover to replace.'); return; }
  const query = keys.map((k) => `key=${encodeURIComponent(k)}`).join('&');
  let res;
  try {
    res = await fetch(`/api/myart?${query}`, { method: 'PUT', body: blob, headers: { 'Content-Type': 'image/jpeg' } });
  } catch {
    showToast('Could not save the cover.');
    return;
  }
  const body = await res.json().catch(() => null);
  if (!res.ok || !body) { showToast((body && body.error) || 'Could not save the cover.'); return; }
  state.myArt = body.art || {};
  // A new album cover replaces any single song's own from before, or those
  // songs would keep the old choice.
  if (scope === 'album') {
    const stale = songs.map((it) => `song:${it.sourceId}/${it.id}`).filter((k) => state.myArt[k]);
    if (stale.length) await removeCovers(stale);
  }
  repaintCovers();
  showToast(scope === 'song' ? 'Cover changed for this song. Only you see it.' : 'Cover changed for the album. Only you see it.');
}

async function removeCovers(keys) {
  const query = keys.map((k) => `key=${encodeURIComponent(k)}`).join('&');
  const { ok, body } = await api(`/api/myart?${query}`, { method: 'DELETE' });
  if (ok && body) state.myArt = body.art || {};
  return ok;
}

// The menu page: this song or the whole album, and the original back.
function renderCoverMenu(target, backTo) {
  const menu = $('item-menu');
  const back = document.createElement('button');
  back.type = 'button';
  back.className = 'menu-back';
  back.append(icon('back'));
  const label = document.createElement('span');
  label.textContent = 'Change cover';
  back.append(label);
  back.addEventListener('click', (event) => {
    event.stopPropagation();
    backTo();
  });
  const note = document.createElement('p');
  note.className = 'menu-note';
  note.textContent = 'Pick a picture. Only you will see it.';
  const rows = [];
  const s = target.song;
  const songKey = s ? `song:${s.sourceId}/${s.id}` : '';
  const albumKey = target.album ? artKeyFor(target.album.sourceId, target.album.artId)
    : (s && s.artId ? artKeyFor(s.sourceId, s.artId) : '');
  if (s) rows.push(menuItem('image', 'For this song', () => setCover(target, 'song')));
  rows.push(menuItem('album', s ? 'For the whole album' : 'Choose a picture', () => setCover(target, 'album')));
  const mine = (songKey && state.myArt[songKey]) || (albumKey && state.myArt[albumKey]);
  if (mine) {
    rows.push(menuItem('close', 'Use the original cover', async () => {
      closeItemMenu();
      const { keys } = await coverKeys(target, 'album');
      if (songKey) keys.push(songKey);
      if (await removeCovers(keys)) {
        repaintCovers();
        showToast('The original cover is back.');
      }
    }));
  }
  menu.replaceChildren(back, note, ...rows);
  placeMenu(menu, state.menuAnchor);
}

// A playlist's picture: choose one, or go back to the collage of its songs.
// backTo is the playlist's menu, or null when the page's cover opened this.
function renderPlaylistPictureMenu(list, backTo) {
  const menu = $('item-menu');
  const rows = [];
  if (backTo) {
    const back = document.createElement('button');
    back.type = 'button';
    back.className = 'menu-back';
    back.append(icon('back'));
    const label = document.createElement('span');
    label.textContent = 'Change picture';
    back.append(label);
    back.addEventListener('click', (event) => {
      event.stopPropagation();
      backTo();
    });
    rows.push(back);
  } else {
    const head = document.createElement('div');
    head.className = 'menu-head';
    const t = document.createElement('strong');
    t.textContent = list.name;
    const sub = document.createElement('span');
    sub.textContent = 'Playlist picture';
    head.append(t, sub);
    rows.push(head);
  }
  const note = document.createElement('p');
  note.className = 'menu-note';
  note.textContent = 'Pick a picture for this playlist. Only you will see it.';
  rows.push(note);
  const key = `key=${encodeURIComponent(`playlist:${list.id}`)}`;
  rows.push(menuItem('image', 'Choose a picture', async () => {
    const blob = await pickCoverImage();
    if (!blob) return;
    closeItemMenu();
    let res;
    try {
      res = await fetch(`/api/myart?${key}`, { method: 'PUT', body: blob, headers: { 'Content-Type': 'image/jpeg' } });
    } catch {
      showToast('Could not save the picture.');
      return;
    }
    const body = await res.json().catch(() => null);
    if (!res.ok || !body) { showToast((body && body.error) || 'Could not save the picture.'); return; }
    state.myArt = body.art || {};
    refreshPlaylistView();
    showToast('Playlist picture changed. Only you see it.');
  }));
  if (playlistArt(list.id)) {
    rows.push(menuItem('close', 'Use the song covers', async () => {
      closeItemMenu();
      const { ok, body } = await api(`/api/myart?${key}`, { method: 'DELETE' });
      if (!ok) { showToast('Could not change the picture.'); return; }
      state.myArt = (body && body.art) || {};
      refreshPlaylistView();
      showToast('The song covers are back.');
    }));
  }
  menu.replaceChildren(...rows);
  placeMenu(menu, state.menuAnchor);
}

// Draws the playlists page again - the grid, or the playlist open on it.
function refreshPlaylistView() {
  const view = $('playlists-view');
  if (view.classList.contains('hidden')) return;
  if (view.dataset.open) showPlaylist(view.dataset.open);
  else showPlaylists();
}

/* ---------------------------------------------------- the cover's looks */

// The record and the moving cover are drawn in a layer laid exactly over the
// cover image, which stays where it is (the swipe, the hold icons and the
// cover morph all work on it), and moves with it.
function coverDeco() {
  let deco = $('np-deco');
  if (deco) return deco;
  deco = document.createElement('div');
  deco.id = 'np-deco';
  deco.className = 'np-deco';
  deco.setAttribute('aria-hidden', 'true');
  const vinyl = document.createElement('div');
  vinyl.className = 'np-vinyl';
  const disc = document.createElement('div');
  disc.className = 'np-vinyl-disc';
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 100 100');
  svg.setAttribute('class', 'np-vinyl-text');
  const path = document.createElementNS(ns, 'path');
  path.setAttribute('id', 'np-vinyl-path');
  path.setAttribute('d', 'M50,50 m-31,0 a31,31 0 1,1 62,0 a31,31 0 1,1 -62,0');
  path.setAttribute('fill', 'none');
  const text = document.createElementNS(ns, 'text');
  const tp = document.createElementNS(ns, 'textPath');
  tp.setAttribute('href', '#np-vinyl-path');
  tp.setAttribute('id', 'np-vinyl-words');
  text.append(tp);
  svg.append(path, text);
  const labelImg = document.createElement('div');
  labelImg.className = 'np-vinyl-label';
  const hole = document.createElement('div');
  hole.className = 'np-vinyl-hole';
  disc.append(svg, labelImg, hole);
  const sheen = document.createElement('div');
  sheen.className = 'np-vinyl-sheen';
  vinyl.append(disc, sheen);
  // Two canvases: one redrawn whole each frame (the orb), one fading slowly
  // so the particles leave trails.
  const viz = document.createElement('canvas');
  viz.className = 'np-viz';
  const trails = document.createElement('canvas');
  trails.className = 'np-viz np-viz-trails';
  deco.append(viz, trails, vinyl);
  document.querySelector('.np-cover-wrap').append(deco);
  return deco;
}

function applyCoverStyle() {
  const np = $('now-playing');
  const style = coverStyle();
  for (const s of COVER_STYLES) np.classList.toggle(`cover-${s}`, s === style);
  np.classList.toggle('spin', style === 'spin');
  np.classList.toggle('round', style === 'spin' || style === 'vinyl');
  np.classList.toggle('cover-viz', VIZ_STYLES.includes(style));
  np.classList.toggle('cover-full', FULL_STYLES.includes(style));
  renderCoverDeco();
  renderLooks();
  // The song's beats, bass and loudness - asked for here, not only while the
  // cover is on screen as it once was: every visualizer is full screen now
  // and a phone never shows the cover under them, so the analysis stopped
  // being asked for at all and they fell back to a slow pulse of no song in
  // particular. For any look, not only the visualizers: the play button
  // moves in time with the song on all of them (playOrb).
  if (audio.item && audio.item.kind === 'music') keepTime(audio.item);
  if (VIZ_STYLES.includes(style)) viz.start();
  else viz.stop();
}

// Lays the layer over the cover and fills it for this song.
function renderCoverDeco() {
  const style = coverStyle();
  const deco = coverDeco();
  const cover = $('np-cover');
  const on = (style === 'vinyl' || VIZ_STYLES.includes(style)) && cover.offsetWidth > 0 && audio.item;
  deco.classList.toggle('shown', Boolean(on));
  if (!on) return;
  Object.assign(deco.style, {
    left: `${cover.offsetLeft}px`, top: `${cover.offsetTop}px`,
    width: `${cover.offsetWidth}px`, height: `${cover.offsetHeight}px`,
  });
  const item = audio.item;
  const key = selectionKey(item);
  if (style === 'vinyl') {
    deco.querySelector('.np-vinyl-label').style.backgroundImage = `url("${(artPath(item) || NO_COVER).replace(/"/g, '%22')}")`;
    if (deco.dataset.words !== key) {
      deco.dataset.words = key;
      const parts = [item.title, (item.creators || []).join(', '), (item.extra && item.extra.album) || '']
        .filter(Boolean).map((p) => p.toUpperCase());
      // Whole repeats only, so no name is cut off where the ring closes.
      let unit = `${parts.join('  •  ')}  •  `;
      if (unit.length > 84) unit = `${unit.slice(0, 80).trimEnd()}…  •  `;
      const words = unit.repeat(Math.max(1, Math.floor(84 / unit.length)));
      const tp = $('np-vinyl-words');
      tp.textContent = words;
      tp.setAttribute('textLength', '192');
      tp.setAttribute('lengthAdjust', 'spacingAndGlyphs');
    }
  }
}

// The moving cover keeps the song's tempo, from the sound analysis, and moves
// more for a song that is more energetic than most of the library. Not the
// beats themselves - hearing those means routing the music through the page's
// audio processing, which stops it when an iPhone locks - but its pace, lined
// up with where the song has got to. A song not yet analysed moves slowly.
const soundOf = {};
// The lookups still on their way, so a second keepTime for the same song waits
// for the answer rather than reading "not known" meanwhile - it used to, and
// started hearing the song with no tempo to lean on, which for Thunder (170)
// found 112.
const soundAsking = new Map();
async function keepTime(item, attempt = 0) {
  const key = selectionKey(item);
  if (!(key in soundOf)) {
    if (!soundAsking.has(key)) {
      soundAsking.set(key, (async () => {
        let found = null;
        // A downloaded song kept its tempo and energy with what was heard in
        // it, so offline needs no answer from the server.
        const kept = await loadHeard(item);
        if (kept && kept.sound) {
          found = kept.sound;
        } else if (item.kind === 'music') {
          const { ok, body } = await api(`/api/music/sound?id=${encodeURIComponent(item.id)}`);
          found = ok && body && body.known ? body : null;
        }
        soundOf[key] = found;
      })().catch(() => { soundOf[key] = null; }).finally(() => soundAsking.delete(key)));
    }
    await soundAsking.get(key);
  }
  if (audio.item !== item) return;
  const sound = soundOf[key];
  viz.beat = 1.6;
  viz.energy = 0.3;
  if (sound && sound.tempo > 0) {
    // Very slow or fast tempos are felt at double or half.
    let bpm = sound.tempo;
    while (bpm < 70) bpm *= 2;
    while (bpm > 150) bpm /= 2;
    viz.beat = 60 / bpm;
    viz.energy = Math.max(0, Math.min(1, sound.energy || 0));
  }
  // The song's own beats and loudness, worked out on the device; until they
  // arrive the tempo alone keeps time.
  listenTo(item, sound && sound.tempo > 0 ? 60 / viz.beat : 0).then((heard) => {
    if (heard && audio.item === item) { viz.heard = { key, ...heard }; viz.bigAt = undefined; }
    // Not heard: asked again while the song plays, after 10, 20 and 40s.
    if (!heard && audio.item === item && attempt < 3) {
      setTimeout(() => { if (audio.item === item && !(viz.heard && viz.heard.key === key)) keepTime(item, attempt + 1); }, 10000 * 2 ** attempt);
    }
  }).catch(() => {});
}

/* ------------------------------------------ listening for the visualizer */

// The visualizer follows the song itself, not only its tempo: where every
// beat falls, which one starts each bar, how loud it is from moment to
// moment, and the kick and snare hits. A copy of the song is decoded on the
// side - an OfflineAudioContext, which plays nothing - so the music keeps
// playing through the ordinary audio element and nothing changes about the
// lock screen (routing playback itself through Web Audio is what stops it
// when an iPhone locks). Once per song, kept for the last few.
const HEARD_KEEP = 6;
const heardSongs = new Map();
// What happened to each song's listening, for the Looks sheet.
const hearState = new Map();
// The shape and method of a hearing, as internal/beats.Version counts it.
const HEARD_VERSION = 8;

// playbackSettled: resolves once the song playing has music buffered well
// ahead (or plays from memory), so hearing it - a second download of the
// whole song - never competes with its start. On a slow link the two together
// held songs at 0:00 for a minute. False if the song changed meanwhile.
function playbackSettled(item) {
  return new Promise((resolve) => {
    const player = $('audio-player');
    const began = Date.now();
    const check = () => {
      if (audio.item !== item) return resolve(false);
      const src = player.currentSrc || '';
      const end = player.buffered.length ? player.buffered.end(player.buffered.length - 1) : 0;
      const ahead = end - player.currentTime;
      const done = Number.isFinite(player.duration) && end >= player.duration - 1;
      // Only a link found slow waits for twenty seconds in hand: on any
      // other, a 3MB copy beside a song that is playing costs nothing.
      const playing = player.readyState >= 3 && !player.paused;
      if (src.startsWith('blob:') || (playing && (!slowLink || ahead >= 20 || done)) || Date.now() - began > 120000) {
        return resolve(true);
      }
      setTimeout(check, 1000);
    };
    check();
  });
}

// hearAhead hears the song coming next before it starts (the Android app,
// where the page has no copy of it), so listenTo finds it already heard.
async function hearAhead(item) {
  const key = selectionKey(item);
  if (item.kind !== 'music' || isDownloaded(item) || heardSongs.has(key)) return;
  if (!(key in soundOf)) {
    const { ok, body } = await api(`/api/music/sound?id=${encodeURIComponent(item.id)}`);
    if (!(key in soundOf)) soundOf[key] = ok && body && body.known ? body : null;
  }
  if (heardSongs.has(key)) return;
  const kept = await loadHeard(item) || await serverHeard(item);
  if (kept) { heardSongs.set(key, Promise.resolve(kept)); return; }
  const sound = soundOf[key];
  let tempo = 0;
  if (sound && sound.tempo > 0) {
    tempo = sound.tempo;
    while (tempo < 70) tempo *= 2;
    while (tempo > 150) tempo /= 2;
  }
  const job = hearSong(item, tempo).catch((err) => { hearState.set(key, `could not listen (${(err && err.message) || err})`); return null; });
  heardSongs.set(key, job);
  while (heardSongs.size > HEARD_KEEP) heardSongs.delete(heardSongs.keys().next().value);
  // Failed: tried again the ordinary way when it plays.
  job.then((heard) => {
    if (heard) rememberHeard(item, heard, sound);
    else if (heardSongs.get(key) === job) heardSongs.delete(key);
  });
}

function listenTo(item, tempo) {
  const key = selectionKey(item);
  if (heardSongs.has(key)) return heardSongs.get(key);
  const job = (async () => {
    const kept = await loadHeard(item);
    if (kept) return kept;
    const fromServer = await serverHeard(item);
    if (fromServer) {
      if (isDownloaded(item)) saveHeard(item, fromServer, fromServer.sound);
      return fromServer;
    }
    // Not while the song is still getting started over the network.
    if (!isDownloaded(item)) hearState.set(key, 'waiting for the song to buffer');
    if (!isDownloaded(item) && !(await playbackSettled(item))) {
      heardSongs.delete(key); // heard next time it plays
      return null;
    }
    const heard = await hearSong(item, tempo);
    if (heard && isDownloaded(item)) saveHeard(item, heard, soundOf[key] || null);
    else if (heard) rememberHeard(item, heard, soundOf[key] || null);
    return heard;
  })().catch((err) => { hearState.set(key, `could not listen (${(err && err.message) || err})`); return null; });
  heardSongs.set(key, job);
  while (heardSongs.size > HEARD_KEEP) heardSongs.delete(heardSongs.keys().next().value);
  // Nothing heard is not kept: it was kept once, and a song whose analysis
  // failed (the server restarting, a dropped connection) stayed "not analysed
  // yet" to its end. keepTime tries again.
  job.then((heard) => { if (!heard && heardSongs.get(key) === job) heardSongs.delete(key); });
  return job;
}

// What was heard in a downloaded song is kept beside it on the device (the
// downloads cache, under a /__heard/ address nothing ever fetches), with the
// analysis's tempo and energy, so offline the visualizer is in time from the
// first beat and needs nothing from the server. Loudness and the two bands
// are kept as bytes (0 to 255): about 45KB for a four-minute song. Removing
// the download removes it; signing out clears it with the rest.
function heardURL(item) {
  return `/__heard/${encodeURIComponent(item.sourceId)}/${escapeId(item.id)}`;
}

const toB64 = (bytes) => {
  let out = '';
  for (let i = 0; i < bytes.length; i += 0x8000) out += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(out);
};
const fromB64 = (text) => Uint8Array.from(atob(text), (c) => c.charCodeAt(0));
const toBytes = (floats) => Uint8Array.from(floats, (v) => Math.round(Math.max(0, Math.min(1, v)) * 255));
const fromBytes = (bytes) => Float32Array.from(bytes, (v) => v / 255);

// Songs not downloaded keep what was heard in them too, in a cache of their
// own (the last HEARD_REMEMBER), so a song played before follows its beats
// from the first second. The order is kept in localStorage, oldest first.
const HEARD_CACHE = 'soundstorm-heard-v1';
const HEARD_ORDER_KEY = 'soundstorm-heard-order';
const HEARD_REMEMBER = 500;

async function rememberHeard(item, heard, sound) {
  if (!window.caches) return;
  try {
    const url = heardURL(item);
    const cache = await caches.open(HEARD_CACHE);
    await cache.put(url, heardResponse(heard, sound));
    let order = [];
    try { order = JSON.parse(localStorage.getItem(HEARD_ORDER_KEY) || '[]'); } catch { order = []; }
    order = order.filter((u) => u !== url);
    order.push(url);
    while (order.length > HEARD_REMEMBER) await cache.delete(order.shift());
    localStorage.setItem(HEARD_ORDER_KEY, JSON.stringify(order));
  } catch { /* heard again next time */ }
}

function heardResponse(heard, sound) {
  const kept = {
    v: HEARD_VERSION, fps: heard.fps, down: heard.down,
    loud: toB64(toBytes(heard.loud)), low: toB64(toBytes(heard.low)), high: toB64(toBytes(heard.high)),
    beats: toB64(new Uint8Array(Float32Array.from(heard.beats).buffer)),
    sound: sound && sound.tempo > 0 ? { tempo: sound.tempo, energy: sound.energy || 0 } : null,
  };
  return new Response(JSON.stringify(kept), { headers: { 'Content-Type': 'application/json' } });
}

async function saveHeard(item, heard, sound) {
  if (!window.caches) return;
  try {
    const cache = await caches.open(OFFLINE_CACHE);
    await cache.put(heardURL(item), heardResponse(heard, sound));
  } catch { /* heard again next time */ }
}

async function loadHeard(item) {
  if (!window.caches) return null;
  try {
    const resp = await caches.match(heardURL(item), { cacheName: isDownloaded(item) ? OFFLINE_CACHE : HEARD_CACHE });
    if (!resp) return null;
    return parseHeard(await resp.json());
  } catch {
    return null;
  }
}

// parseHeard reads a kept hearing, the device's or the server's (the same
// shape: internal/beats writes what saveHeard does).
function parseHeard(k) {
  if (!k || k.v !== HEARD_VERSION) return null; // from before the hits were on a fixed scale: heard again
  const beats = Float64Array.from(new Float32Array(fromB64(k.beats).buffer));
  return { fps: k.fps, down: k.down, loud: fromBytes(fromB64(k.loud)), low: fromBytes(fromB64(k.low)), high: fromBytes(fromB64(k.high)), beats, sound: k.sound };
}

// serverHeard is what the server heard in the song: it hears every song
// ahead of time (internal/beats), so this is there from the first second,
// about 45KB, where hearing it here means downloading the song again.
// Null when the server has not (the phone then hears it itself).
async function serverHeard(item) {
  if (item.kind !== 'music' || state.offline) return null;
  hearState.set(selectionKey(item), 'asking the server');
  try {
    // The version in the address: the server lets a browser keep an answer
    // for an hour, and one kept from before a version change would be
    // refused here without the server ever being asked again.
    const { ok, body } = await api(`/api/music/beats?source=${encodeURIComponent(item.sourceId)}&id=${encodeURIComponent(item.id)}&v=${HEARD_VERSION}`);
    return ok ? parseHeard(body) : null;
  } catch {
    return null;
  }
}

// prepareDownloads hears every downloaded song not heard yet, one at a time,
// resting between songs and waiting while the app is not on screen, so it is
// never in the way. Run after a download and a little after opening.
let preparing = false;
async function prepareDownloads() {
  if (preparing || !window.caches || !(window.OfflineAudioContext || window.webkitOfflineAudioContext)) return;
  preparing = true;
  const rest = (ms) => new Promise((r) => setTimeout(r, ms));
  try {
    const cache = await caches.open(OFFLINE_CACHE);
    for (const item of Object.values(state.downloads.items)) {
      if (!item || item.kind !== 'music' || !isDownloaded(item)) continue;
      if (await loadHeard(item)) continue;
      while (document.hidden) await rest(3000);
      const key = selectionKey(item);
      if (!(key in soundOf) && !state.offline) {
        const { ok, body } = await api(`/api/music/sound?id=${encodeURIComponent(item.id)}`);
        soundOf[key] = ok && body && body.known ? body : null;
      }
      const sound = soundOf[key];
      let tempo = 0;
      if (sound && sound.tempo > 0) {
        tempo = sound.tempo;
        while (tempo < 70) tempo *= 2;
        while (tempo > 150) tempo /= 2;
      }
      const heard = await serverHeard(item) || await hearSong(item, tempo).catch(() => null);
      if (heard) await saveHeard(item, heard, sound);
      await rest(1200);
    }
  } finally {
    preparing = false;
  }
}

// songBytes is the song to listen to: the whole file already in memory for
// gapless playback, or a downloaded copy, or else a small 96 kbps copy.
async function songBytes(item) {
  const player = $('audio-player');
  if (audio.item === item && (player.currentSrc || '').startsWith('blob:')) {
    return (await fetch(player.currentSrc)).arrayBuffer();
  }
  // A downloaded song is read from the device: no network, and it works
  // offline.
  if (window.caches && isDownloaded(item)) {
    const kept = await caches.match(streamPath(item), { cacheName: OFFLINE_CACHE, ignoreSearch: true });
    if (kept) return kept.arrayBuffer();
  }
  // Sent at full speed (listen=1 skips the away-from-home pace): about 3MB,
  // there in seconds. On a link found slow it keeps the pace.
  const res = await fetch(`${streamPath(item)}?kbps=96${slowLink ? '' : '&listen=1'}`);
  if (res.ok) return res.arrayBuffer();
  throw new Error('no audio to listen to');
}

async function hearSong(item, tempo) {
  const Offline = window.OfflineAudioContext || window.webkitOfflineAudioContext;
  const key = selectionKey(item);
  if (!Offline || item.kind !== 'music') { hearState.set(key, 'this browser cannot listen'); return null; }
  hearState.set(key, 'downloading a copy to listen to');
  const bytes = await songBytes(item);
  hearState.set(key, `decoding it (${Math.round(bytes.byteLength / 1024)} KB)`);
  // 44100 a second, as the server hears it: the sharp highs are above 7kHz,
  // which 11025 cannot hold. Lower rates only where a browser refuses it.
  let ctx = null;
  let SR = 0;
  for (const rate of [44100, 22050, 11025]) {
    try { ctx = new Offline(1, rate, rate); SR = rate; break; } catch { /* next */ }
  }
  if (!ctx) { hearState.set(key, 'no audio context'); return null; }
  const HOP = 256 * Math.round(SR / 11025); // about 23ms a frame, whatever the rate
  const pcm = await new Promise((resolve, reject) => {
    const p = ctx.decodeAudioData(bytes, resolve, reject);
    if (p && p.then) p.then(resolve, reject);
  });
  const left = pcm.getChannelData(0);
  const right = pcm.numberOfChannels > 1 ? pcm.getChannelData(1) : null;
  const fps = SR / HOP;
  const frames = Math.floor(pcm.length / HOP);
  if (frames < fps * 5) { hearState.set(key, `too short (${pcm.duration.toFixed(1)} s decoded)`); return null; }
  hearState.set(key, 'working out the beats');
  const loud = new Float32Array(frames);
  const low = new Float32Array(frames);
  const high = new Float32Array(frames);
  // Two one-pole filters split the low end (kick, bass) from the top (snare,
  // hats): cheap, and enough to tell them apart.
  const kLow = 1 - Math.exp((-2 * Math.PI * 150) / SR);
  const kHigh = 1 - Math.exp((-2 * Math.PI * 2500) / SR);
  // The sharp highs for the looks: above 7kHz through four one-pole
  // high-passes, where hats and a snare's crack are (and a voice's "s").
  const kHat = 1 - Math.exp((-2 * Math.PI * Math.min(7000, 0.4 * SR)) / SR);
  const hatSt = new Float64Array(4);
  const hat = new Float32Array(frames);
  let lp = 0, hpState = 0;
  for (let f = 0; f < frames; f++) {
    let sx = 0, sl = 0, sh = 0, st = 0;
    for (let i = f * HOP, end = i + HOP; i < end; i++) {
      const x = right ? (left[i] + right[i]) * 0.5 : left[i];
      lp += kLow * (x - lp);
      hpState += kHigh * (x - hpState);
      const hp = x - hpState;
      sx += x * x; sl += lp * lp; sh += hp * hp;
      let y = x;
      for (let p = 0; p < 4; p++) { hatSt[p] += kHat * (y - hatSt[p]); y -= hatSt[p]; }
      st += y * y;
    }
    hat[f] = 10 * Math.log10(st / HOP + 1e-10);
    loud[f] = 10 * Math.log10(sx / HOP + 1e-10);
    low[f] = 10 * Math.log10(sl / HOP + 1e-10);
    high[f] = 10 * Math.log10(sh / HOP + 1e-10);
  }
  // Onsets: how sharply each band got louder.
  const lowOn = new Float32Array(frames);
  const highOn = new Float32Array(frames);
  const onset = new Float32Array(frames);
  for (let f = 1; f < frames; f++) {
    lowOn[f] = Math.max(0, low[f] - low[f - 1]);
    highOn[f] = Math.max(0, high[f] - high[f - 1]);
    onset[f] = lowOn[f] + 0.6 * highOn[f] + 0.4 * Math.max(0, loud[f] - loud[f - 1]);
  }
  // Loudness as 0 to 1 within this song: quiet verse to its loudest chorus.
  const scale = (arr, lo, hi) => {
    const sorted = Float32Array.from(arr).sort();
    const a = sorted[Math.floor(sorted.length * lo)];
    const b = sorted[Math.floor(sorted.length * hi)];
    const out = new Float32Array(arr.length);
    for (let i = 0; i < arr.length; i++) out[i] = Math.max(0, Math.min(1, (arr[i] - a) / (b - a || 1)));
    return out;
  };
  // How loud a part of the song feels is its energy over about half a second,
  // not one 23ms slice - between the hits a slice is near silence in a loud
  // chorus as in a quiet verse.
  const win = Math.max(1, Math.round(fps * 0.25));
  const energy = Float64Array.from(loud, (db) => 10 ** (db / 10));
  const felt = new Float32Array(frames);
  let run = 0; // reused below for the bass
  for (let f = 0; f < frames + win; f++) {
    if (f < frames) run += energy[f];
    if (f - 2 * win - 1 >= 0) run -= energy[f - 2 * win - 1];
    const c = f - win;
    if (c >= 0 && c < frames) {
      const n = Math.min(frames - 1, c + win) - Math.max(0, c - win) + 1;
      felt[c] = 10 * Math.log10(run / n + 1e-10);
    }
  }
  const loudN = scale(felt, 0.05, 0.97);
  // Scaled within the song, for choosing each bar's first beat below.
  const lowOnN = scale(lowOn, 0.5, 0.995);
  // What the looks see is on a fixed scale (internal/beats says why): a
  // bass jump of 4dB starts to count, 10dB is a full hit.
  const fixed = (arr, lo, span) => Float32Array.from(arr, (v) => Math.max(0, Math.min(1, (v - lo) / span)));
  const kicks = fixed(lowOn, 4, 6);
  const hatOn = new Float32Array(frames);
  // A rise from the quietest of the three frames before (internal/beats
  // says why): 6dB starts to count, 16dB is a full hit.
  for (let f = 1; f < frames; f++) {
    let lo = hat[f - 1];
    for (let k = Math.max(0, f - 3); k < f - 1; k++) lo = Math.min(lo, hat[k]);
    hatOn[f] = Math.max(0, hat[f] - lo);
  }
  const highs = fixed(hatOn, 6, 10);
  // And heard: in full within 10dB of the song's loud highs (95th
  // percentile), fading out over the 4dB below.
  const loudHats = Float32Array.from(hat).sort();
  const top = loudHats[Math.floor(loudHats.length * 0.95)];
  for (let f = 0; f < frames; f++) highs[f] *= Math.max(0, Math.min(1, (hat[f] - (top - 14)) / 4));

  // The tempo: the lag at which the onsets repeat best, between 70 and 170
  // beats a minute, leaning towards the analysis's own tempo when there is one.
  let mean = 0;
  for (let f = 0; f < frames; f++) mean += onset[f];
  mean /= frames;
  let sd = 0;
  for (let f = 0; f < frames; f++) sd += (onset[f] - mean) ** 2;
  sd = Math.sqrt(sd / frames) || 1;
  const on = new Float32Array(frames);
  for (let f = 0; f < frames; f++) on[f] = (onset[f] - mean) / sd;
  // Lags are tried in tenths of a frame: at 23ms frames a whole-frame lag
  // is too coarse for a fast song - Thunder's 170 bpm falls between two, the
  // error adds up over every beat of the song, and a wrong tempo (112) won.
  const prior = tempo > 0 ? tempo : 120;
  let bestLag = (60 / prior) * fps;
  let best = -Infinity;
  for (let lag = (60 / 180) * fps; lag <= (60 / 70) * fps; lag += 0.1) {
    let sum = 0;
    const whole = Math.floor(lag);
    const part = lag - whole;
    for (let f = whole + 1; f < frames; f++) sum += on[f] * (on[f - whole] * (1 - part) + on[f - whole - 1] * part);
    sum /= frames - whole - 1;
    const octaves = Math.log2((60 * fps) / lag / prior);
    const weighted = sum * Math.exp(-0.5 * (octaves / (tempo > 0 ? 0.25 : 0.9)) ** 2);
    if (weighted > best) { best = weighted; bestLag = lag; }
  }
  const period = bestLag;

  // The beats themselves: dynamic programming (Ellis, 2007) - every frame's
  // best chain of beats ending there, rewarding onsets and punishing gaps
  // that stray from the period, so it follows a tempo that wanders.
  const score = new Float32Array(frames);
  const back = new Int32Array(frames).fill(-1);
  const tight = 100;
  for (let i = 0; i < frames; i++) {
    let bestPrev = -Infinity;
    let bj = -1;
    const from = i - Math.round(period * 2);
    const to = i - Math.round(period / 2);
    for (let j = Math.max(0, from); j <= to; j++) {
      const gap = Math.log((i - j) / period);
      const v = score[j] - tight * gap * gap;
      if (v > bestPrev) { bestPrev = v; bj = j; }
    }
    score[i] = on[i] + (bj >= 0 ? bestPrev : 0);
    back[i] = bj;
  }
  let end = frames - 1;
  for (let i = Math.max(0, frames - period); i < frames; i++) if (score[i] > score[end]) end = i;
  const beatFrames = [];
  for (let i = end; i >= 0; i = back[i]) beatFrames.push(i);
  beatFrames.reverse();
  const beats = Float64Array.from(beatFrames, (f) => f / fps);
  // Which beat starts each bar: the one of four where the kick lands hardest.
  const hit = [0, 0, 0, 0];
  beatFrames.forEach((f, k) => { hit[k % 4] += lowOnN[f] + lowOnN[Math.min(frames - 1, f + 1)]; });
  const down = hit.indexOf(Math.max(...hit));

  return { fps, loud: loudN, low: kicks, high: highs, beats, down };
}

// coverPalette picks up to three colours from a cover's 16x16 pixels: the
// strongest hues, brightened to glow on the dark screen. A grey cover gives
// greys and white.
function coverPalette(px) {
  const bins = Array.from({ length: 12 }, () => ({ w: 0, r: 0, g: 0, b: 0 }));
  for (let i = 0; i < px.length; i += 4) {
    const r = px[i] / 255, g = px[i + 1] / 255, b = px[i + 2] / 255;
    const max = Math.max(r, g, b), min = Math.min(r, g, b);
    const sat = max ? (max - min) / max : 0;
    let h = 0;
    if (max !== min) {
      if (max === r) h = ((g - b) / (max - min)) % 6;
      else if (max === g) h = (b - r) / (max - min) + 2;
      else h = (r - g) / (max - min) + 4;
    }
    const bin = bins[Math.floor(((h * 60 + 360) % 360) / 30)];
    const w = 0.15 + sat * max;
    bin.w += w; bin.r += px[i] * w; bin.g += px[i + 1] * w; bin.b += px[i + 2] * w;
  }
  const top = bins.filter((x) => x.w > 0).sort((x, y) => y.w - x.w).slice(0, 3);
  const out = top.map((x) => {
    const c = [x.r / x.w, x.g / x.w, x.b / x.w];
    const lift = Math.min(3, 230 / (Math.max(...c) || 1));
    return c.map((v) => Math.round(Math.min(255, v * lift)));
  });
  while (out.length < 3) out.push(out.length ? out[0].map((v) => Math.round(v + (255 - v) * 0.5)) : [255, 255, 255]);
  return out;
}

// The visualizer, in place of the cover: a glowing orb in the cover's colours
// that morphs and swells with the music, light rays and shock rings, and a
// vortex of particles thrown outward on every beat, leaving trails. It keeps
// the song's tempo and energy (keepTime), working everything out from the
// song's position each frame, so a seek needs no telling. It settles to still
// on pause and stops drawing when not on screen.
const TAU = Math.PI * 2;
// Each layer of the orb has its own set of waves, fixed so it looks the same
// every time: harmonic, how fast it travels, and where it starts.
const ORB_LAYERS = Array.from({ length: 6 }, (_, l) => ({
  spin: (l % 2 ? -1 : 1) * (0.05 + l * 0.025),
  waves: [2, 3, 5, 7].map((k, j) => ({ k: k + (l % 3), speed: (0.4 + ((l * 3 + j * 5) % 7) * 0.13) * (j % 2 ? -1 : 1), ph: l * 1.3 + j * 2.1 })),
}));
// How many particles, rain drops and stars the looks draw: half on a TV,
// whose processor does this drawing - a projector's is phone-class or less,
// and it has a 1080p screen to fill.
const VIZ_DENSITY = TV ? 0.5 : 1;
const viz = {
  beat: 1.6, energy: 0.3, palette: [[255, 255, 255], [200, 220, 255], [255, 210, 230]],
  raf: 0, level: 0, lastBeat: -1, lastT: 0, dots: [],
  start() {
    if (!this.raf) this.raf = requestAnimationFrame((t) => this.frame(t));
  },
  stop() {
    cancelAnimationFrame(this.raf);
    this.raf = 0;
  },
  // fit sizes a canvas to how it is shown, at up to twice the pixels - on a
  // TV at a little over half: a projector's chip drawing a full 1080p screen
  // of these was reported as super laggy, and from across a room the softer
  // picture does not show.
  fit(canvas) {
    const dpr = TV ? 0.6 : Math.min(window.devicePixelRatio || 1, 2);
    const w = Math.round(canvas.clientWidth * dpr);
    const h = Math.round(canvas.clientHeight * dpr);
    if (w && h && (canvas.width !== w || canvas.height !== h)) { canvas.width = w; canvas.height = h; }
    return dpr;
  },
  // keepClearOfPlay: the full-screen animation leaves a soft round space
  // where music's play orb sits (the owner's asking - the two drawn over each
  // other were a muddle): after each frame, a circle round the orb is cut out
  // of both canvases, solid in the middle and fading at its edge.
  keepClearOfPlay(back, front) {
    const np = $('now-playing');
    const btn = $('np-play');
    // No clearing while a TV has faded the buttons away (tv-idle).
    if (!np.classList.contains('np-music') || !btn.offsetWidth || np.classList.contains('tv-idle')) return;
    const c = back.getBoundingClientRect();
    const b = btn.getBoundingClientRect();
    if (!c.width) return;
    const k = back.width / c.width;
    const x = (b.left + b.width / 2 - c.left) * k;
    const y = (b.top + b.height / 2 - c.top) * k;
    // Clear space out to 1.1 of the button's width, clear only close in and
    // eased the whole way out - a solid circle with a short edge read as a
    // hole cut in the animation.
    const r0 = b.width * 0.22 * k;
    const r1 = b.width * 1.1 * k;
    for (const ctx of [back.getContext('2d'), front.getContext('2d')]) {
      const hole = ctx.createRadialGradient(x, y, r0, x, y, r1);
      hole.addColorStop(0, 'rgba(0, 0, 0, 1)');
      hole.addColorStop(0.3, 'rgba(0, 0, 0, 0.9)');
      hole.addColorStop(0.55, 'rgba(0, 0, 0, 0.6)');
      hole.addColorStop(0.8, 'rgba(0, 0, 0, 0.25)');
      hole.addColorStop(1, 'rgba(0, 0, 0, 0)');
      ctx.save();
      ctx.globalCompositeOperation = 'destination-out';
      ctx.fillStyle = 'rgba(0, 0, 0, 1)';
      ctx.beginPath();
      ctx.arc(x, y, r0, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = hole;
      ctx.fillRect(x - r1, y - r1, r1 * 2, r1 * 2);
      ctx.restore();
    }
  },
  frame(now) {
    this.raf = 0;
    const np = $('now-playing');
    const deco = $('np-deco');
    // Flow fills the whole of Now Playing, on its own canvases; the others
    // draw over the cover.
    const flowing = FULL_STYLES.includes(coverStyle());
    const back = flowing ? document.querySelector('#np-stage .np-stage-back') : deco && deco.querySelector('.np-viz:not(.np-viz-trails)');
    const front = flowing ? document.querySelector('#np-stage .np-stage-front') : deco && deco.querySelector('.np-viz-trails');
    if (!back || np.classList.contains('hidden') || !np.classList.contains('cover-viz') || (!flowing && !deco.classList.contains('shown'))) {
      this.stop();
      return;
    }
    // And at 30 frames a second on a TV, every other one skipped.
    if (TV && this.lastT && now - this.lastT < 28) {
      this.raf = requestAnimationFrame((ts) => this.frame(ts));
      return;
    }
    const player = $('audio-player');
    const playing = !player.paused;
    const dt = Math.min(0.1, (now - (this.lastT || now)) / 1000);
    this.lastT = now;
    this.level += ((playing ? 1 : 0) - this.level) * Math.min(1, dt * (playing ? 5 : 1.5));
    const dpr = this.fit(back);
    this.fit(front);
    const w = back.width, h = back.height;
    if (!w || !h) { this.raf = requestAnimationFrame((ts) => this.frame(ts)); return; }

    // The song's moment as it is heard: the player's clock plus this device's
    // own timing adjustment (vizLead), for a phone whose speaker runs behind
    // or ahead of the clock the page can read - or, while the Analysis look
    // is dragged, the moment the finger has reached.
    const t = (this.scrubAt !== undefined ? this.scrubAt : (player.currentTime || 0)) + vizLead();
    const e = this.energy;
    const lv = this.level;
    let beat = this.beat;
    let phase = (t % beat) / beat;
    let beatNo = Math.floor(t / beat);
    let downbeat = beatNo % 4 === 0;
    // Without the song's own analysis, only a soft pulse, a little more at
    // the start of each bar.
    let kick = Math.exp(-phase * 5) * lv * (downbeat ? 0.45 : 0.15);
    let snare = (beatNo % 2 === 1 ? Math.exp(-phase * 6) * 0.15 : 0) * lv;
    let loudness = 0.6;
    let novelty = 0; // how much this beat stands out from the last few
    const heard = this.heard && audio.item && this.heard.key === selectionKey(audio.item) ? this.heard : null;
    if (heard) {
      // The beat the song is on, from the list of beats found in it.
      const bs = heard.beats;
      let lo = 0, hi = bs.length - 1;
      while (lo < hi) { const mid = (lo + hi + 1) >> 1; if (bs[mid] <= t) lo = mid; else hi = mid - 1; }
      if (bs.length > 1 && bs[lo] <= t) {
        const next = lo + 1 < bs.length ? bs[lo + 1] : bs[lo] + (bs[lo] - bs[Math.max(0, lo - 1)] || beat);
        beat = next - bs[lo];
        phase = Math.min(1, (t - bs[lo]) / beat);
        beatNo = lo;
        downbeat = (lo - heard.down) % 4 === 0;
      }
      // Loudness and the hits, where the song actually is, a little smoothed
      // on the way down so a hit is seen rather than flickered.
      const fi = Math.max(0, Math.min(heard.loud.length - 1, Math.floor(t * heard.fps)));
      const fall = Math.exp(-dt * 7);
      this.kickEnv = Math.max((this.kickEnv || 0) * fall, heard.low[fi]);
      this.snareEnv = Math.max((this.snareEnv || 0) * fall, heard.high[fi]);
      this.loudEnv = (this.loudEnv || heard.loud[fi]) + (heard.loud[fi] - (this.loudEnv || 0)) * Math.min(1, dt * 6);
      loudness = this.loudEnv;
      // A hit is a jump in level, as big in a soft passage as a loud one, so
      // it is scaled by how loud the song is there.
      const hitScale = 0.25 + 0.75 * loudness;
      // What stands out, not every beat. A kick on every beat of a whole song
      // pumped everything relentlessly (asked for as getting rid of the
      // "consistent overwhelming tempo beats"), so each hit is measured
      // against the typical hit of the last two bars: a steady beat settles
      // into a soft pulse, and an accent, a fill or a drop after a quiet bit
      // is what hits hard. A surge in loudness counts the same way.
      if (beatNo !== this.peakBeat) {
        this.peakBeat = beatNo;
        const ahead = Math.min(heard.low.length - 1, fi + 2);
        let kp = 0, sp = 0;
        for (let i = fi; i <= ahead; i++) { kp = Math.max(kp, heard.low[i]); sp = Math.max(sp, heard.high[i]); }
        const mean = (a) => (a.length ? a.reduce((x, y) => x + y, 0) / a.length : 0.5);
        this.kickPeaks = this.kickPeaks || [];
        this.snarePeaks = this.snarePeaks || [];
        // Judged only against a few beats of this part of the song: right
        // after a seek the last bars were somewhere else entirely.
        this.beatNovelty = this.kickPeaks.length >= 4 ? Math.max(0, Math.min(1, (kp - mean(this.kickPeaks) * 1.05) / 0.35)) : 0;
        this.kickPeaks.push(kp);
        this.snarePeaks.push(sp);
        if (this.kickPeaks.length > 8) this.kickPeaks.shift();
        if (this.snarePeaks.length > 8) this.snarePeaks.shift();
      }
      const typicalK = this.kickPeaks && this.kickPeaks.length ? this.kickPeaks.reduce((x, y) => x + y, 0) / this.kickPeaks.length : 0.5;
      const typicalS = this.snarePeaks && this.snarePeaks.length ? this.snarePeaks.reduce((x, y) => x + y, 0) / this.snarePeaks.length : 0.5;
      this.loudAvg = (this.loudAvg === undefined ? loudness : this.loudAvg) + (loudness - (this.loudAvg || 0)) * Math.min(1, dt / 4);
      const surge = Math.max(0, Math.min(1, (loudness - this.loudAvg) / 0.25));
      const kickNew = Math.max(0, this.kickEnv - typicalK * 0.95);
      const snareNew = Math.max(0, this.snareEnv - typicalS * 0.95);
      kick = Math.min(1, 0.3 * this.kickEnv + 1.3 * kickNew + 0.5 * surge) * lv * hitScale;
      snare = Math.min(1, 0.3 * this.snareEnv + 1.3 * snareNew) * lv * hitScale;
      novelty = Math.max(this.beatNovelty || 0, surge);
    }
    // The lightning moments: every sharp high reaching 60% on the fixed
    // scale (a snare's crack, a cymbal), however many there are - the owner's
    // choice, after a limit of one every two seconds was offered and turned
    // down. A hit is counted once as it rises past the mark, not on every
    // frame it stays over it. The first after six quiet seconds is marked
    // (firstDrop: Storm's double strike, Fireworks' finale), and a glow fades
    // after each (dropEnv). Before, it was the first beat of each bar in the
    // song's loudest 30%, and before that runs of bass booms. Every frame
    // since the last is looked at, so a hit one frame long is not missed when
    // frames are skipped (a TV draws at 30 a second).
    let drop = false;
    let firstDrop = false;
    let dropPower = 0;
    let dropLoud = 0;
    let dropBass = 0;
    if (heard) {
      const fi = Math.max(0, Math.min(heard.high.length - 1, Math.floor(t * heard.fps)));
      if (this.bigAt !== undefined && t < this.bigAt) this.bigAt = undefined; // a seek back
      const jumped = this.strikeFrame === undefined || fi < this.strikeFrame || fi - this.strikeFrame > 10;
      let rose = false;
      let power = 0;
      for (let k = jumped ? fi : this.strikeFrame + 1; k <= fi; k++) {
        if (!strikesAt(heard, k)) continue;
        rose = true;
        // How hard it hits: the peak of the sharp high, which can come a
        // frame or two after it crosses the line (heard is the whole song).
        for (let j = k; j < Math.min(heard.high.length, k + 4); j++) {
          power = Math.max(power, heard.high[j]);
          dropLoud = Math.max(dropLoud, heard.loud[j] || 0);
          dropBass = Math.max(dropBass, heard.low[j] || 0);
        }
      }
      this.strikeFrame = fi;
      if (playing && rose) {
        drop = true;
        dropPower = power;
        firstDrop = this.bigAt === undefined || t - this.bigAt > 6;
        this.bigAt = t;
      }
    }
    const sinceBig = t - (this.bigAt === undefined ? -99 : this.bigAt);
    const dropEnv = sinceBig >= 0 && sinceBig < 0.9 ? 1 - sinceBig / 0.9 : 0;
    if (drop) this.beatNovelty = 1;
    this.kickNow = kick; // for a look from outside: how hard this moment hits
    this.noveltyNow = novelty;
    const bar = (t / (beat * 4)) % 1;
    // How hard everything moves: the song's energy, and how loud it is right
    // now - a quiet verse calms it, the chorus lets it go.
    const drive = (0.25 + 0.9 * loudness) * (0.6 + 0.7 * e);
    // And how bright it all is: dim in a quiet passage, blazing when loud.
    const bright = 0.35 + 0.65 * loudness;
    const cx = w / 2, cy = h / 2;
    // The size everything is drawn to. The screen's canvas scales to its
    // shorter side: the scenes made full screen from the start at 0.55 of
    // it, and the six first drawn where the cover was (sized to it, their
    // canvas 180% of the cover) at 0.85, so they fill the screen now rather
    // than keeping the cover's size in its middle.
    const size = flowing ? Math.min(w, h) * (EX_COVER_VIZ.includes(coverStyle()) ? 0.85 : 0.55) : w / 1.8;
    const R0 = size * 0.3;
    const pal = this.palette;
    const rgba = vizColor;
    // A clock that runs with the music and idles slowly without it.
    this.clock = (this.clock || 0) + dt * (0.25 + 0.75 * lv) * (0.5 + 0.5 * e + 0.6 * loudness);
    const ck = this.clock;

    const style = coverStyle();
    if (style !== 'pulse') {
      const scene = VIZ_SCENES[style];
      const g = back.getContext('2d');
      const f = front.getContext('2d');
      const anyBeat = playing && beatNo !== this.sceneBeat;
      if (anyBeat) this.sceneBeat = beatNo;
      // Big moments only: a beat that stands out, or the first of a bar -
      // not all four.
      const newBeat = anyBeat && (novelty > 0.4 || downbeat);
      if (this.sceneFor !== style) {
        // A fresh start for each scene: nothing left over from the last.
        this.sceneFor = style;
        this.scene = {};
        f.clearRect(0, 0, w, h);
      }
      scene(this.scene, {
        g, f, w, h, cx, cy, size, dpr, pal, rgba, t, dt, ck, phase, beatNo, downbeat, newBeat, novelty, drop, firstDrop, dropPower, dropLoud, dropBass, dropEnv,
        kick, snare, loud: loudness, lv, e, drive, bright, playing,
      });
      if (flowing) this.keepClearOfPlay(back, front);
      if (playing || this.level > 0.01 || this.scrubAt !== undefined) this.raf = requestAnimationFrame((ts) => this.frame(ts));
      return;
    }
    this.sceneFor = 'pulse';

    // ---- the orb, redrawn whole
    const g = back.getContext('2d');
    g.clearRect(0, 0, w, h);
    g.globalCompositeOperation = 'lighter';
    // A nebula behind it, in the cover's colours.
    for (let i = 0; i < 3; i++) {
      const ang = ck * 0.3 + i * 2.1;
      const x = cx + Math.cos(ang) * R0 * 0.9;
      const y = cy + Math.sin(ang * 1.2) * R0 * 0.9;
      const r = R0 * (2.2 + 0.5 * kick * drive);
      const grad = g.createRadialGradient(x, y, 0, x, y, r);
      grad.addColorStop(0, rgba(pal[i], 0.22 * (0.4 + 0.6 * lv) * bright));
      grad.addColorStop(1, rgba(pal[i], 0));
      g.fillStyle = grad;
      g.fillRect(0, 0, w, h);
    }
    // Light rays turning slowly, brighter on the beat.
    g.save();
    g.translate(cx, cy);
    g.rotate(ck * 0.12);
    const rays = 18;
    for (let i = 0; i < rays; i++) {
      const a0 = (i / rays) * TAU;
      const len = R0 * (2.4 + 0.8 * Math.sin(ck * 1.3 + i * 1.9) + 1.2 * kick * drive);
      const grad = g.createLinearGradient(0, 0, Math.cos(a0) * len, Math.sin(a0) * len);
      grad.addColorStop(0, rgba(pal[i % 3], 0.16 * (0.3 + kick)));
      grad.addColorStop(1, rgba(pal[i % 3], 0));
      g.fillStyle = grad;
      g.beginPath();
      g.moveTo(0, 0);
      g.arc(0, 0, len, a0 - 0.05, a0 + 0.05);
      g.closePath();
      g.fill();
    }
    g.restore();
    // The orb: six layers, each a closed shape whose edge is a sum of waves
    // travelling round it. Added together they glow white where they overlap.
    // The beat swells them; the snare sharpens the edges into spikes.
    const pts = 120;
    ORB_LAYERS.forEach((layer, l) => {
      const R = R0 * (0.72 + l * 0.085) * (1 + 0.22 * kick * drive);
      const amp = 0.05 + 0.06 * e + 0.1 * kick * drive;
      const spike = 0.07 * snare * drive;
      g.beginPath();
      for (let p = 0; p <= pts; p++) {
        const th = (p / pts) * TAU;
        let r = 1;
        for (const wv of layer.waves) r += (amp / layer.waves.length) * 2 * Math.sin(wv.k * th + wv.speed * ck * 2 + wv.ph);
        r += spike * Math.sin(13 * th + ck * 6 + l);
        const a = th + layer.spin * ck * 2;
        const x = cx + Math.cos(a) * R * r;
        const y = cy + Math.sin(a) * R * r;
        if (p) g.lineTo(x, y); else g.moveTo(x, y);
      }
      g.closePath();
      const grad = g.createRadialGradient(cx, cy, R * 0.15, cx, cy, R * 1.25);
      grad.addColorStop(0, rgba(pal[l % 3], 0.05 * bright));
      grad.addColorStop(0.6, rgba(pal[l % 3], (0.2 + 0.1 * lv) * bright));
      grad.addColorStop(1, rgba(pal[l % 3], 0.04 * bright));
      g.fillStyle = grad;
      g.fill();
    });
    // A bright core that flashes on the kick.
    const core = g.createRadialGradient(cx, cy, 0, cx, cy, R0 * (0.9 + 0.5 * kick));
    core.addColorStop(0, vizColor(VIZ_WHITE, 0.1 + 0.1 * bright + 0.4 * kick));
    core.addColorStop(1, 'rgba(255, 255, 255, 0)');
    g.fillStyle = core;
    g.fillRect(0, 0, w, h);
    // Thin rings round it, rippling fast.
    g.lineWidth = 1.4 * dpr;
    for (let i = 0; i < 3; i++) {
      const R = R0 * (1.35 + i * 0.16) * (1 + 0.12 * kick * drive);
      g.strokeStyle = rgba(pal[(i + 1) % 3], (0.35 + 0.35 * lv) * bright);
      g.beginPath();
      for (let p = 0; p <= pts; p++) {
        const th = (p / pts) * TAU;
        const r = 1 + (0.015 + 0.05 * snare * drive) * Math.sin((9 + i * 3) * th - ck * (3 + i)) + 0.02 * Math.sin(4 * th + ck * 1.7 + i);
        const x = cx + Math.cos(th) * R * r;
        const y = cy + Math.sin(th) * R * r;
        if (p) g.lineTo(x, y); else g.moveTo(x, y);
      }
      g.closePath();
      g.stroke();
    }
    // A white shock ring and a flash, for a drop only.
    if (dropEnv > 0 && lv > 0.05) {
      const p = 1 - dropEnv;
      g.lineWidth = (2 + 6 * dropEnv) * dpr;
      g.strokeStyle = vizColor(VIZ_WHITE, dropEnv * 0.7 * lv);
      g.beginPath();
      g.arc(cx, cy, R0 * (1.2 + p * 2.6), 0, TAU);
      g.stroke();
      const flash = g.createRadialGradient(cx, cy, 0, cx, cy, R0 * 3);
      flash.addColorStop(0, vizColor(VIZ_WHITE, dropEnv * dropEnv * 0.45));
      flash.addColorStop(1, 'rgba(255, 255, 255, 0)');
      g.fillStyle = flash;
      g.fillRect(0, 0, w, h);
    }
    g.globalCompositeOperation = 'source-over';

    // ---- the vortex, on the canvas that fades rather than clears
    const f = front.getContext('2d');
    f.globalCompositeOperation = 'destination-out';
    f.fillStyle = `rgba(0, 0, 0, ${playing ? 0.2 : 0.35})`;
    f.fillRect(0, 0, w, h);
    f.globalCompositeOperation = 'lighter';
    if (!this.dots.length) {
      for (let i = 0; i < Math.round(240 * VIZ_DENSITY); i++) {
        const home = 1.25 + Math.random() * 1.3;
        this.dots.push({ a: Math.random() * TAU, r: home, home, v: 0, spin: 0.25 + Math.random() * 0.6,
          size: 0.8 + Math.random() * 1.8, c: i % 3 });
      }
    }
    // Every beat throws them outward; a spring brings them back.
    if (playing && beatNo !== this.lastBeat && (novelty > 0.4 || downbeat)) {
      this.lastBeat = beatNo;
      const push = (0.4 + 0.9 * e) * (0.6 + 1.4 * novelty) * (0.3 + 0.9 * (this.loudEnv === undefined ? 0.6 : this.loudEnv));
      for (const d of this.dots) d.v += push * (0.4 + Math.random() * 0.8);
    }
    for (const d of this.dots) {
      d.v += ((d.home - d.r) * 9 - d.v * 3.2) * dt;
      d.r += d.v * dt;
      d.a += (d.spin / d.r) * dt * (0.6 + 1.4 * lv) * (1 + kick);
      const rr = R0 * d.r;
      d.px = d.x === undefined ? cx + Math.cos(d.a) * rr : d.x;
      d.py = d.y === undefined ? cy + Math.sin(d.a) * rr * 0.92 : d.y;
      d.x = cx + Math.cos(d.a) * rr;
      d.y = cy + Math.sin(d.a) * rr * 0.92;
    }
    // A streak from where each was, so the trails are smooth lines rather
    // than a string of dots one frame apart - drawn in six batches (three
    // colours, two weights), not a stroke per particle, which with a colour
    // each was the other half of the garbage that made a phone skip.
    const streak = (0.55 + 0.4 * lv) * (0.45 + 0.55 * bright);
    f.lineCap = 'round';
    for (let c = 0; c < 3; c++) {
      for (let big = 0; big < 2; big++) {
        f.beginPath();
        for (const d of this.dots) {
          if (d.c !== c || (d.size >= 1.7) !== (big === 1)) continue;
          f.moveTo(d.px, d.py);
          f.lineTo(d.x, d.y);
        }
        f.strokeStyle = rgba(pal[c], streak);
        f.lineWidth = (big ? 2.2 : 1.2) * dpr * (1 + kick * 0.6);
        f.stroke();
      }
    }
    f.globalCompositeOperation = 'source-over';

    // On until paused and settled.
    if (flowing) this.keepClearOfPlay(back, front);
    if (playing || this.level > 0.01) this.raf = requestAnimationFrame((ts) => this.frame(ts));
  },
};
$('audio-player').addEventListener('play', () => {
  if ($('now-playing').classList.contains('cover-viz')) viz.start();
  playOrb.start();
});
// A jump in the song starts the "what stands out" comparison afresh.
$('audio-player').addEventListener('seeked', () => {
  viz.bigAt = undefined;
  viz.strikeFrame = undefined;
  viz.kickPeaks = [];
  viz.snarePeaks = [];
  viz.loudAvg = undefined;
  viz.beatNovelty = 0;
});

// vizColor: a colour string for the visualizers without making a new one on
// every call. They ask for thousands a second - a colour and a see-through
// amount per particle, per frame - and building each as a fresh string was
// garbage enough for a phone to stop and collect it every few seconds, seen
// as the animation skipping. The amount is rounded to one of 64 steps, which
// the eye cannot tell apart, and each colour keeps its 65 strings. Keyed by
// the colour's values, so any [r, g, b] works, the palette's or a literal.
const vizColors = new Map();
function vizColor(c, a) {
  const key = (c[0] << 16) | (c[1] << 8) | c[2];
  let row = vizColors.get(key);
  if (!row) {
    if (vizColors.size > 256) vizColors.clear();
    row = new Array(65);
    vizColors.set(key, row);
  }
  const i = a <= 0 ? 0 : a >= 1 ? 64 : Math.round(a * 64);
  return row[i] || (row[i] = `rgba(${c[0]}, ${c[1]}, ${c[2]}, ${i / 64})`);
}
const VIZ_WHITE = [255, 255, 255];
// Lightning: a sharp high rising past this, landing this close to a beat or
// to halfway between two. 60% and the in-between beats are from the owner's
// taps (soundstorm train-looks rules): over ten songs it matched them best of
// the versions tried; 80% on the beat alone had been set by eye.
const STRIKE_AT = 0.6;
const STRIKE_ON_BEAT = 0.07;
// strikesAt says whether a sharp high rising at frame fi is lightning: past
// STRIKE_AT, and on one of the beats found. Asked for as what makes Thunder's
// hits on 2 and 4 right for it and little else: looked at afresh, those land
// within a few milliseconds of a beat, and nearly every other strike fell
// between beats (100-350ms off - fills, the vocal's "th" and "s"). Measured
// over the song: 68 strikes a minute to 34, the hits on 2 and 4 kept (72% to
// 71%), strikes anywhere else 38 a minute to 4. A loud top as well was tried
// and changed nothing more.
function strikesAt(heard, fi) {
  if (!(heard.high[fi] >= STRIKE_AT && (fi === 0 || heard.high[fi - 1] < STRIKE_AT))) return false;
  const at = fi / heard.fps;
  const bs = heard.beats;
  let lo = 0, hi = bs.length - 1;
  while (lo < hi) { const mid = (lo + hi + 1) >> 1; if (bs[mid] <= at) lo = mid; else hi = mid - 1; }
  if (Math.abs(at - bs[lo]) <= STRIKE_ON_BEAT) return true;
  if (lo + 1 >= bs.length) return false;
  // The next beat, or halfway to it (the owner's taps had both).
  return Math.abs(bs[lo + 1] - at) <= STRIKE_ON_BEAT || Math.abs((bs[lo] + bs[lo + 1]) / 2 - at) <= STRIKE_ON_BEAT;
}

// playOrb: Now Playing's play button for music, drawn rather than an icon -
// the owner found a play triangle boring and asked for something round-ish
// that moves, and then that it keep time with the analysed music. Four
// glowing layers in the cover's colours, each a closed shape whose edge is a
// sum of travelling waves, turning against each other: on every beat it
// swells (more on the bar's first, and on a beat the kick lands on), a kick
// makes the edges ripple, and the song's loudness sets how much it moves.
// Paused, it stops where it is and dims - the movement is the "playing".
// Its own small loop, not the visualizer's, so it lives on Lyrics and the
// covers too; it reads what was heard in the song itself (viz.heard), and
// only the tempo until that arrives.
const playOrb = {
  raf: 0,
  clock: 0,
  env: 0,
  swell: 0,
  last: 0,
  beatNo: -1,
  canvas() {
    let c = $('np-play').querySelector('.np-play-orb');
    if (!c) {
      c = document.createElement('canvas');
      c.className = 'np-play-orb';
      c.setAttribute('aria-hidden', 'true');
      $('np-play').append(c);
    }
    return c;
  },
  start() {
    if (!this.raf) this.raf = requestAnimationFrame((ts) => this.frame(ts));
  },
  frame(ts) {
    this.raf = 0;
    const np = $('now-playing');
    if (np.classList.contains('hidden') || !np.classList.contains('np-music') || document.hidden) return;
    const canvas = this.canvas();
    const dpr = TV ? 1 : Math.min(window.devicePixelRatio || 1, 2);
    const cw = Math.round(canvas.clientWidth * dpr);
    const ch = Math.round(canvas.clientHeight * dpr);
    if (!cw || !ch) return;
    if (canvas.width !== cw || canvas.height !== ch) { canvas.width = cw; canvas.height = ch; }
    const player = $('audio-player');
    const playing = !player.paused;
    const dt = Math.min(0.1, this.last ? (ts - this.last) / 1000 : 0.016);
    this.last = ts;

    // Where the song is in its rhythm: the beat, how far through it, whether
    // it is a bar's first, and what the kick and loudness are right now.
    const t = (player.currentTime || 0) + vizLead();
    const heard = viz.heard && audio.item && viz.heard.key === selectionKey(audio.item) ? viz.heard : null;
    let beatNo;
    let phase;
    let downbeat;
    let kick = 0;
    let loud = 0.6;
    if (heard && heard.beats && heard.beats.length > 1) {
      const bs = heard.beats;
      let lo = 0, hi = bs.length - 1;
      while (lo < hi) { const mid = (lo + hi + 1) >> 1; if (bs[mid] <= t) lo = mid; else hi = mid - 1; }
      const len = (bs[Math.min(bs.length - 1, lo + 1)] - bs[lo]) || viz.beat;
      beatNo = lo;
      phase = Math.max(0, Math.min(1, (t - bs[lo]) / len));
      downbeat = (lo - heard.down) % 4 === 0;
      const fi = Math.max(0, Math.min(heard.low.length - 1, Math.floor(t * heard.fps)));
      kick = heard.low[fi] || 0;
      loud = heard.loud[fi] === undefined ? 0.6 : heard.loud[fi];
    } else {
      beatNo = Math.floor(t / viz.beat);
      phase = (t % viz.beat) / viz.beat;
      downbeat = beatNo % 4 === 0;
    }
    if (playing) {
      // A new beat swells it; the kick is held a moment and let go softly.
      if (beatNo !== this.beatNo) {
        this.beatNo = beatNo;
        this.swell = Math.max(this.swell, (downbeat ? 1 : 0.55) * (0.5 + 0.5 * loud));
      }
      this.env = Math.max(this.env * Math.exp(-dt * 7), kick);
      this.swell *= Math.exp(-dt * 5.5);
      // Loudness eased over a fraction of a second, so it breathes with the
      // song rather than flickering frame to frame.
      this.loud = this.loud === undefined ? loud : this.loud + (loud - this.loud) * Math.min(1, dt * 4);
      this.clock += dt * (0.3 + 1.4 * this.loud);
    }
    const lvl = this.loud === undefined ? loud : this.loud;
    const g = canvas.getContext('2d');
    g.clearRect(0, 0, cw, ch);
    g.globalCompositeOperation = 'lighter';
    // Dimmer than it first was, so it sits under the main animation rather
    // than competing with it (the owner's asking): brighter in a loud part,
    // quiet in a soft one, and dimmer still when paused.
    g.globalAlpha = playing ? 0.35 + 0.4 * lvl : 0.3;
    const cx = cw / 2;
    const cy = ch / 2;
    // The button is the middle 60% of the canvas; the glow spills round it.
    // Its size follows the loudness as much as the beat.
    const R = cw * 0.3 * (0.78 + 0.3 * lvl + 0.14 * this.swell + 0.06 * this.env);
    const pal = viz.palette;
    const ck = this.clock;
    const pts = 72;
    const TAU2 = Math.PI * 2;
    // Crisp glowing lines rather than filled haze (filled layers read as
    // cloudy; the owner asked for something more magical): each wavy layer
    // drawn twice, a wide faint stroke for its glow and a fine bright one
    // over it, like neon, with only a breath of colour inside.
    for (let l = 0; l < 4; l++) {
      const layer = ORB_LAYERS[l];
      const amp = 0.03 + 0.14 * lvl + 0.12 * this.env;
      g.beginPath();
      for (let p = 0; p <= pts; p++) {
        const th = (p / pts) * TAU2;
        let r = 0.8 + l * 0.08;
        for (const wv of layer.waves) r += (amp / layer.waves.length) * 2 * Math.sin(wv.k * th + wv.speed * ck * 2.4 + wv.ph);
        const a = th + layer.spin * ck * 4;
        const x = cx + Math.cos(a) * R * r;
        const y = cy + Math.sin(a) * R * r;
        if (p) g.lineTo(x, y); else g.moveTo(x, y);
      }
      g.closePath();
      const c = pal[l % 3];
      g.fillStyle = vizColor(c, 0.04 + 0.04 * this.swell);
      g.fill();
      g.strokeStyle = vizColor(c, 0.12 + 0.12 * this.swell);
      g.lineWidth = 5 * dpr;
      g.stroke();
      g.strokeStyle = vizColor(c, 0.6 + 0.35 * this.swell);
      g.lineWidth = 1.2 * dpr;
      g.stroke();
    }
    // Sparkles circling it: each twinkles on its own, the ring turns with the
    // music, and a beat makes them all flare and drift outward a little.
    if (!this.sparks) {
      this.sparks = Array.from({ length: 16 }, (_, i) => ({
        a: (i / 16) * TAU2 + Math.random() * 0.4, r: 1.05 + Math.random() * 0.45,
        tw: Math.random() * TAU2, sp: 0.6 + Math.random() * 0.9, c: i % 3,
      }));
    }
    for (const sk of this.sparks) {
      const a = sk.a + ck * 0.5 * sk.sp;
      const rr = R * (sk.r + 0.18 * this.swell);
      const x = cx + Math.cos(a) * rr;
      const y = cy + Math.sin(a) * rr;
      const twinkle = Math.max(0, Math.sin(ck * 3.2 * sk.sp + sk.tw));
      const bright = twinkle * twinkle * (0.5 + 0.5 * lvl) + 0.5 * this.swell;
      if (bright < 0.04) continue;
      const size = (0.8 + 1.4 * bright) * dpr;
      g.fillStyle = vizColor(VIZ_WHITE, Math.min(1, bright));
      g.fillRect(x - size / 2, y - size / 2, size, size);
      // A soft cross of light on the brightest ones.
      if (bright > 0.45) {
        g.fillStyle = vizColor(pal[sk.c], Math.min(1, bright) * 0.5);
        g.fillRect(x - size * 2.5, y - size * 0.2, size * 5, size * 0.4);
        g.fillRect(x - size * 0.2, y - size * 2.5, size * 0.4, size * 5);
      }
    }
    // A small bright heart that flashes with the beat.
    const core = g.createRadialGradient(cx, cy, 0, cx, cy, R * (0.35 + 0.2 * this.swell));
    core.addColorStop(0, vizColor(VIZ_WHITE, 0.25 + 0.2 * lvl + 0.35 * this.swell));
    core.addColorStop(1, vizColor(VIZ_WHITE, 0));
    g.fillStyle = core;
    g.fillRect(0, 0, cw, ch);
    g.globalAlpha = 1;
    g.globalCompositeOperation = 'source-over';
    // Moving only while it plays; a pause leaves this last, dimmed frame.
    if (playing) this.raf = requestAnimationFrame((next) => this.frame(next));
    else this.last = 0;
  },
};
$('audio-player').addEventListener('pause', () => playOrb.start());
document.addEventListener('visibilitychange', () => { if (!document.hidden) playOrb.start(); });

// Flow's scene, full screen: particles swirling through a moving current round
// the middle of the screen, trailing light; faster and more turbulent as it
// gets louder, a pulse out from the middle on every beat and a shock wave
// across the screen on the first beat of each bar.
function flowScene(st, m) {
  const { g, f, w, h, cx, cy, dpr, pal, rgba, dt, ck, kick, snare, loud, lv, e, newBeat, downbeat, bright, playing } = m;
  const S = Math.min(w, h);
  g.clearRect(0, 0, w, h);
  f.globalCompositeOperation = 'destination-out';
  // Fast enough that old trails really go: slower left a grey haze, the
  // last faint trace of every trail never quite reaching nothing.
  f.fillStyle = `rgba(0, 0, 0, ${playing ? 0.1 : 0.22})`;
  f.fillRect(0, 0, w, h);
  const spawn = (anywhere) => {
    const c = Math.floor(Math.random() * 3);
    if (anywhere || Math.random() < 0.35) return { x: Math.random() * w, y: Math.random() * h, vx: 0, vy: 0, c, life: 0.4 + Math.random() };
    const a = Math.random() * Math.PI * 2;
    const r = Math.random() * S * 0.12;
    return { x: cx + Math.cos(a) * r, y: cy + Math.sin(a) * r, vx: 0, vy: 0, c, life: 0.6 + Math.random() };
  };
  if (!st.p) { st.p = Array.from({ length: Math.round(520 * VIZ_DENSITY) }, () => spawn(true)); st.pulses = []; }
  if (newBeat || m.drop) {
    st.pulses.push({ r: S * 0.05, life: 1, big: m.drop });
    const push = S * (0.15 + 0.5 * loud) * (m.drop ? 2.4 : 1) * (0.6 + 0.6 * e);
    for (const p of st.p) {
      const dx = p.x - cx, dy = p.y - cy;
      const d = Math.hypot(dx, dy) || 1;
      const near = Math.exp(-d / (S * 0.6));
      p.vx += (dx / d) * push * near;
      p.vy += (dy / d) * push * near;
    }
  }
  // A glow in the middle, and the pulses going out from it.
  g.globalCompositeOperation = 'lighter';
  const core = g.createRadialGradient(cx, cy, 0, cx, cy, S * (0.3 + 0.25 * kick));
  core.addColorStop(0, rgba(pal[0], (0.1 + 0.3 * kick) * (0.4 + 0.6 * bright)));
  core.addColorStop(1, rgba(pal[0], 0));
  g.fillStyle = core;
  g.fillRect(0, 0, w, h);
  st.pulses = st.pulses.filter((pl) => {
    pl.r += dt * S * (0.8 + 0.7 * loud);
    pl.life -= dt * 0.7;
    if (pl.life <= 0) return false;
    g.strokeStyle = rgba(pl.big ? [255, 255, 255] : pal[1], pl.life * (pl.big ? 0.45 : 0.22) * (0.4 + 0.6 * bright));
    g.lineWidth = (pl.big ? 4 : 2) * dpr;
    g.beginPath();
    g.arc(cx, cy, pl.r, 0, Math.PI * 2);
    g.stroke();
    return true;
  });
  g.globalCompositeOperation = 'source-over';
  // The current: a swirl round the middle mixed with a field of waves that
  // tightens as it gets louder.
  const speed = S * (0.04 + (0.1 + 0.35 * loud) * lv * (0.6 + 0.6 * e) + 0.25 * kick);
  // A gentle current, so trails flow in curves rather than zigzags.
  const k = ((1.1 + 1.4 * loud) / S) * Math.PI;
  const sw = 0.45;
  f.globalCompositeOperation = 'lighter';
  f.lineCap = 'round';
  f.lineWidth = (0.9 + 1.2 * snare) * dpr;
  for (const p of st.p) {
    const dx = p.x - cx, dy = p.y - cy;
    const swirl = Math.atan2(dy, dx) + Math.PI / 2;
    const n = Math.sin(p.x * k + ck * 0.7) + Math.cos(p.y * k * 1.3 - ck * 0.5) + Math.sin((p.x + p.y) * k * 0.7 + ck * 1.1);
    const na = n * Math.PI * 0.4;
    const fx = Math.cos(na) * (1 - sw) + Math.cos(swirl) * sw;
    const fy = Math.sin(na) * (1 - sw) + Math.sin(swirl) * sw;
    p.vx *= 1 - dt * 2.2;
    p.vy *= 1 - dt * 2.2;
    const nx = p.x + (fx * speed + p.vx) * dt;
    const ny = p.y + (fy * speed + p.vy) * dt;
    f.strokeStyle = rgba(pal[p.c], (0.35 + 0.45 * lv) * (0.4 + 0.6 * bright));
    f.beginPath();
    f.moveTo(p.x, p.y);
    f.lineTo(nx, ny);
    f.stroke();
    p.x = nx;
    p.y = ny;
    p.life -= dt * 0.22;
    if (p.life <= 0 || p.x < -20 || p.y < -20 || p.x > w + 20 || p.y > h + 20) Object.assign(p, spawn(false));
  }
  f.globalCompositeOperation = 'source-over';
}

// The other visualizers. Each is given the moment of the music (m: the beat's
// phase, which beat, a new beat, the kick and snare, how loud, how energetic,
// the cover's colours) and two canvases: g, cleared for it each frame, and f,
// which it may fade itself for trails. st is its own state, fresh each time it
// is chosen. They draw round (cx, cy), the cover's middle; size is the
// cover's width, and the canvases are 180% of it.
// Storm's lightning. A soft round sprite, drawn once per colour and stretched
// into clouds and glows, rather than a gradient made every frame.
function stormSprite(rgb, a0, a1) {
  const c = document.createElement('canvas');
  c.width = c.height = 64;
  const x = c.getContext('2d');
  const gr = x.createRadialGradient(32, 32, 0, 32, 32, 32);
  gr.addColorStop(0, `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${a0})`);
  gr.addColorStop(0.45, `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${a1})`);
  gr.addColorStop(1, `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, 0)`);
  x.fillStyle = gr;
  x.fillRect(0, 0, 64, 64);
  return c;
}
// A puff of cloud: several soft blobs bumped along the top, flatter
// underneath. The same shape is drawn twice, dark for the cloud and pale for
// it lit, so a flash lights up the cloud's own shape.
function stormPuffShape() {
  const blobs = [];
  const n = 7 + Math.floor(Math.random() * 5);
  for (let i = 0; i < n; i++) {
    const bx = 0.18 + Math.random() * 0.64;
    blobs.push([bx, 0.62 - Math.sin(Math.PI * bx) * (0.12 + Math.random() * 0.2), 0.09 + Math.random() * 0.07]);
  }
  return blobs;
}
// Drawn into the middle of a canvas with 32px to spare all round, so no blob
// is cut off square at an edge (STORM_PAD is that margin as a share).
const STORM_PAD = 32 / 192;
function stormPuff(shape, rgb, a) {
  const c = document.createElement('canvas');
  c.width = 256;
  c.height = 160;
  const x = c.getContext('2d');
  for (const [bx, by, r] of shape) {
    const gr = x.createRadialGradient(32 + bx * 192, 32 + by * 96, 0, 32 + bx * 192, 32 + by * 96, r * 192);
    gr.addColorStop(0, `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${a})`);
    gr.addColorStop(0.55, `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${a * 0.6})`);
    gr.addColorStop(1, `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, 0)`);
    x.fillStyle = gr;
    x.fillRect(0, 0, 256, 160);
  }
  return c;
}
function stormDrawPuff(g, img, c, w, h, R) {
  const pw = c.rx * R * 2;
  const ph = c.rx * R;
  g.drawImage(img, c.x * w - pw / 2 - pw * STORM_PAD, c.y * h - ph / 2 - ph * (32 / 96), pw * (256 / 192), ph * (160 / 96));
}
// Where a strike lights the clouds, so no two light them alike: a close bolt
// a wide glow round where it leaves the clouds and a patch or two beside it;
// a far one less; sheet lightning one to three patches anywhere in the deck,
// each a moment after the last, so the light rolls across the sky.
function stormLights(tier, x, w, h) {
  const out = [];
  const add = (lx, ly, r, k, delay) => out.push({ x: lx, y: ly, r, k, delay });
  if (tier === 2) {
    add(x, h * 0.08, w * (0.55 + Math.random() * 0.25), 0.8, 0);
    for (let i = 0; i < 1 + Math.floor(Math.random() * 2); i++) add(x + (Math.random() - 0.5) * w * 0.8, h * (0.04 + Math.random() * 0.25), w * (0.25 + Math.random() * 0.2), 0.5 + Math.random() * 0.4, Math.random() * 0.08);
  } else if (tier === 1) {
    add(x, h * 0.07, w * (0.4 + Math.random() * 0.2), 0.8, 0);
    if (Math.random() < 0.6) add(x + (Math.random() - 0.5) * w * 0.6, h * (0.04 + Math.random() * 0.2), w * (0.2 + Math.random() * 0.2), 0.5, Math.random() * 0.1);
  } else {
    let lx = w * Math.random();
    const dir = Math.random() < 0.5 ? -1 : 1;
    const n = 1 + Math.floor(Math.random() * 3);
    for (let i = 0; i < n; i++) {
      add(lx, h * (0.04 + Math.random() * 0.26), w * (0.35 + Math.random() * 0.3), 0.6 + Math.random() * 0.4, i * (0.08 + Math.random() * 0.12));
      lx += dir * w * (0.2 + Math.random() * 0.25);
    }
  }
  return out;
}

// One strike, sized by how hard the sound hit (s, 0 to 1): a weak one is
// sheet lightning, only the clouds lit from inside; a middling one a thin
// bolt far off, ending in the sky; a strong one a thick bolt close by, down
// to the ground with branches. Each is one flash fading out - a flicker of
// return strokes was tried and the owner preferred the single flash.
function stormBolt(w, h, s, age) {
  const tier = s < 0.3 ? 0 : s < 0.7 ? 1 : 2;
  const x0 = w * (0.15 + Math.random() * 0.7);
  const bo = { tier, s, age, x: x0, life: tier ? 1.4 : 1.1, branches: [], scale: 1, landed: false, lights: stormLights(tier, x0, w, h) };
  if (!tier) return bo;
  bo.scale = tier === 2 ? 1 + 0.6 * (s - 0.7) / 0.3 : 0.45 + 0.3 * (s - 0.3) / 0.4;
  const bottom = tier === 2 ? h * (0.9 + Math.random() * 0.06) : h * (0.36 + Math.random() * 0.2);
  // Not every bolt falls straight down (the owner's asking): half crawl
  // across the sky, just under and in and out of the clouds, never reaching
  // the ground; a quarter lean, landing well to one side of where they left
  // the cloud; the rest come down more or less straight.
  const r = Math.random();
  const crawl = r < 0.5;
  const lean = !crawl && r < 0.75;
  const side = Math.random() < 0.5 ? -1 : 1;
  let sx = x0;
  let sy = -h * 0.02;
  let tx;
  let ty;
  if (crawl) {
    sx = w * (0.1 + Math.random() * 0.8);
    sy = h * (0.16 + Math.random() * 0.2);
    tx = Math.min(w * 1.05, Math.max(-w * 0.05, sx + side * w * (0.45 + Math.random() * 0.45)));
    ty = sy + (Math.random() - 0.3) * h * 0.12;
  } else {
    tx = lean ? sx + side * w * (0.25 + Math.random() * 0.3) : sx + (Math.random() - 0.5) * w * 0.15;
    tx = Math.min(w * 0.95, Math.max(w * 0.05, tx));
    ty = bottom;
  }
  bo.crawl = crawl;
  if (crawl) return stormSpider(bo, tier, w, h);
  // The shape is lightning's, jagged at every scale: a few big kinks first
  // (waypoints along the way, each pushed aside), then each stretch between
  // them broken by midpoint displacement - halved again and again, each
  // middle pushed aside by a share of its own length - so a stretch is as
  // zigzag up close as the whole bolt is from afar. A falling bolt starts
  // above the screen, hidden in the cloud.
  const main = [sx, sy];
  const kinks = 4 + Math.floor(Math.random() * 3);
  let px = sx;
  let py = sy;
  for (let k = 1; k <= kinks; k++) {
    const f = k / kinks;
    const last = k === kinks;
    const nx = sx + (tx - sx) * f + (last ? 0 : (Math.random() - 0.5) * w * (crawl ? 0.05 : 0.12));
    const ny = sy + (ty - sy) * f + (last ? 0 : (Math.random() - 0.5) * h * (crawl ? 0.07 : 0.04));
    stormJag(main, px, py, nx, ny, 0.42, 4);
    px = nx;
    py = ny;
  }
  bo.main = main;
  bo.ex = px;
  bo.ey = py;
  // A crawler lights the clouds all along its way, the light rolling with
  // it, in place of one patch where a bolt leaves the cloud.
  if (crawl) {
    bo.lights = [];
    for (let k = 0; k < 4; k++) {
      const f = k / 3;
      bo.lights.push({ x: sx + (tx - sx) * f, y: Math.min(h * 0.2, sy + (ty - sy) * f), r: w * (0.3 + Math.random() * 0.2), k: tier === 2 ? 0.9 : 0.65, delay: f * 0.12 });
    }
  }
  // Branches: few, short, angled down and forking, gathered near the start
  // where the bolt leaves the cloud, and shorter the further along they
  // start (a crawler's anywhere along it). A close bolt has one strong
  // branch, nearly as bright as the bolt itself.
  const n = main.length / 2;
  const count = tier === 2 ? 3 + Math.floor(Math.random() * 3 + s * 2) : 1 + Math.floor(Math.random() * 2);
  for (let b = 0; b < count; b++) {
    const t = crawl ? 0.1 + Math.random() * 0.8 : Math.random() * Math.random() * 0.7;
    const at = Math.max(2, Math.floor(t * n)) * 2;
    const strong = tier === 2 && b === 0;
    const len = h * (strong ? 0.22 + Math.random() * 0.12 : 0.07 + Math.random() * 0.12) * (crawl ? 0.6 : 1 - t * 0.8) * (tier === 2 ? 1 : 0.6);
    // A leaning bolt's branches mostly lean its way.
    const bs = lean && Math.random() < 0.7 ? side : Math.random() < 0.5 ? -1 : 1;
    stormBranch(bo.branches, main[at], main[at + 1], len, bs, strong ? 0.75 : 0.35 + Math.random() * 0.15, strong ? 2 : 1);
  }
  return bo;
}
// Lightning in the clouds spreads rather than aims (the owner's asking: the
// ground strikes are right, the cloud ones were too much one line going
// somewhere): from a point under the clouds two or three arms wander off in
// different directions, mostly sideways, and each branches again and again,
// up as well as down, the branches forking in turn.
function stormSpider(bo, tier, w, h) {
  const ox = w * (0.15 + Math.random() * 0.7);
  const oy = h * (0.18 + Math.random() * 0.17);
  const arms = 2 + (Math.random() < 0.45 ? 1 : 0);
  const first = (Math.random() < 0.5 ? 0 : Math.PI) + (Math.random() - 0.5) * 0.8;
  const paths = [];
  bo.lights = [{ x: ox, y: Math.min(h * 0.2, oy), r: w * (0.35 + Math.random() * 0.2), k: tier === 2 ? 0.95 : 0.7, delay: 0 }];
  for (let a = 0; a < arms; a++) {
    // The second arm roughly the other way; a third anywhere off to a side.
    const ang = a === 0 ? first : a === 1 ? first + Math.PI + (Math.random() - 0.5) * 0.9 : first + (Math.random() < 0.5 ? 1 : -1) * (1.2 + Math.random() * 0.6);
    const len = w * (a === 0 ? 0.35 + Math.random() * 0.25 : 0.18 + Math.random() * 0.22);
    const pts = [ox, oy];
    const kinks = 3 + Math.floor(Math.random() * 3);
    let px = ox;
    let py = oy;
    let dir = ang;
    for (let k = 1; k <= kinks; k++) {
      // Each stretch turns a little from the last, so an arm wanders.
      dir += (Math.random() - 0.5) * 0.9;
      const step = len / kinks;
      const nx = px + Math.cos(dir) * step;
      const ny = Math.max(h * 0.08, Math.min(h * 0.5, py + Math.sin(dir) * step * 0.7));
      stormJag(pts, px, py, nx, ny, 0.45, 4);
      px = nx;
      py = ny;
    }
    paths.push(pts);
    bo.lights.push({ x: px, y: Math.min(h * 0.2, py), r: w * (0.25 + Math.random() * 0.2), k: tier === 2 ? 0.8 : 0.6, delay: 0.05 + Math.random() * 0.1 });
    // Branches all along the arm, either side of it.
    const count = (tier === 2 ? 4 : 3) + Math.floor(Math.random() * 3);
    for (let b = 0; b < count; b++) {
      const at = Math.max(1, Math.floor((0.1 + Math.random() * 0.85) * (pts.length / 2 - 1))) * 2;
      const bang = ang + (Math.random() < 0.5 ? 1 : -1) * (0.5 + Math.random() * 0.9);
      stormBranchAt(bo.branches, pts[at], pts[at + 1], h * (0.05 + Math.random() * 0.1), bang, 0.3 + Math.random() * 0.2, 2);
    }
  }
  // The longest arm is the bolt's main channel; the others are drawn as
  // heavy branches.
  bo.main = paths[0];
  for (let a = 1; a < paths.length; a++) bo.branches.push({ pts: paths[a], w: 0.7 });
  bo.ex = ox;
  bo.ey = oy;
  return bo;
}
// A branch going off at an angle (radians, 0 is to the right), jagged,
// forking up to forks times further out, each fork turned further away.
function stormBranchAt(out, x, y, len, ang, wgt, forks) {
  const ex = x + Math.cos(ang) * len;
  const ey = y + Math.sin(ang) * len;
  const pts = [x, y];
  stormJag(pts, x, y, ex, ey, 0.5, 3);
  out.push({ pts, w: wgt });
  for (let f = 0; f < forks; f++) {
    if (Math.random() > 0.6) continue;
    const at = Math.floor((0.3 + Math.random() * 0.4) * (pts.length / 2)) * 2;
    stormBranchAt(out, pts[at], pts[at + 1], len * (0.4 + Math.random() * 0.3), ang + (Math.random() < 0.5 ? 1 : -1) * (0.35 + Math.random() * 0.45), wgt * 0.65, forks - 1);
  }
}
// Midpoint displacement from (x1, y1) to (x2, y2), adding the points after
// the first: the middle pushed sideways by up to rough times the length, and
// each half done the same, depth times.
function stormJag(out, x1, y1, x2, y2, rough, depth) {
  if (depth === 0) {
    out.push(x2, y2);
    return;
  }
  const dx = x2 - x1;
  const dy = y2 - y1;
  const len = Math.hypot(dx, dy) || 1;
  const off = (Math.random() - 0.5) * len * rough;
  const mx = (x1 + x2) / 2 - (dy / len) * off;
  const my = (y1 + y2) / 2 + (dx / len) * off;
  stormJag(out, x1, y1, mx, my, rough, depth - 1);
  stormJag(out, mx, my, x2, y2, rough, depth - 1);
}
// A branch: out to one side at 20-55 degrees from straight down, jagged like
// the bolt, perhaps forking once more further out, thinner and shorter.
function stormBranch(out, x, y, len, side, wgt, forks) {
  const ang = (20 + Math.random() * 35) * Math.PI / 180;
  const ex = x + Math.sin(ang) * side * len;
  const ey = y + Math.cos(ang) * len;
  const pts = [x, y];
  stormJag(pts, x, y, ex, ey, 0.45, 4);
  out.push({ pts, w: wgt });
  for (let f = 0; f < forks; f++) {
    if (Math.random() > 0.55) continue;
    const at = Math.floor((0.2 + Math.random() * 0.4) * (pts.length / 2)) * 2;
    stormBranch(out, pts[at], pts[at + 1], len * (0.35 + Math.random() * 0.25), Math.random() < 0.7 ? side : -side, wgt * 0.6, 0);
  }
}
// How lit a strike is at its age: one flash fading out in about 0.4s (sheet
// lightning glows a little longer in the clouds).
function stormLight(bo) {
  return Math.max(0, 1 - bo.age * (bo.tier ? 2.6 : 1.8));
}
function stormPath(g, pts, from, to) {
  g.moveTo(pts[from], pts[from + 1]);
  for (let i = from + 2; i <= to; i += 2) g.lineTo(pts[i], pts[i + 1]);
}

// The other full-screen scenes. Like Flow they draw over the whole of Now
// Playing, round the middle of the screen (cx, cy), with S its shorter side.
const FULL_SCENES = {
  // Storm: rain in the cover's colours, faster when loud, under dark clouds;
  // lightning on every strong sharp high (m.drop, see viz.frame), sized by how
  // hard it hit (m.dropPower) - sheet lightning in the clouds, a bolt far off,
  // or a bolt close by with a flash over everything, its light on the clouds
  // and the rain, and the rain splashing up where it lands.
  storm(st, m) {
    const { g, f, w, h, dpr, pal, rgba, dt, ck, kick, loud, lv, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    if (!st.drops) {
      // Spread wider than the screen to the left, which the wind blows
      // across, so the rain covers all of it.
      // Drops start far enough left that the strongest wind (it blows a drop
      // about a quarter of the screen's height sideways on the way down)
      // still carries rain into the bottom left corner - on a tall phone the
      // old margin of a third of the width left that corner dry. The count
      // grows with the span, so the rain is as thick as it was.
      // Each drop has a depth (z): far ones thin, dim, slow and short, landing
      // higher up the screen; near ones thick, bright, fast and long, landing
      // at the bottom. k is its place in the heaviness: in a quiet part only
      // the drops under it fall.
      const span = w * 1.1 + h * 0.34;
      st.drops = Array.from({ length: Math.round(480 * VIZ_DENSITY * span / (w * 1.35)) }, () => {
        const z = Math.random();
        return { x: -(h * 0.3 + w * 0.05) + Math.random() * span, y: Math.random() * h, z, layer: z < 0.45 ? 0 : z < 0.8 ? 1 : 2, s: 0.5 + z * 1.2, c: Math.floor(Math.random() * 3), k: Math.random(), ground: h * (0.85 + 0.12 * z + Math.random() * 0.02), off: false };
      });
      st.mist = Array.from({ length: 6 }, () => ({ x: Math.random() * 1.4 - 0.2, y: 0.86 + Math.random() * 0.11, rx: 0.35 + Math.random() * 0.3, ry: 0.06 + Math.random() * 0.05, sp: 0.5 + Math.random(), ph: Math.random() * 6 }));
      st.bolts = [];
      st.splash = [];
      st.sparks = [];
      // A deck of cloud over the whole top of the screen, there all the
      // time: puffs in rows, thickest at the top, the lower (nearer) ones
      // bigger and drifting faster, drawn back to front.
      st.shapes = Array.from({ length: 5 }, stormPuffShape);
      st.clouds = [];
      // Five rows, the first right along the top edge, so the very top of the
      // screen is cloud too (with four, starting above it, the top showed
      // through dark).
      for (let row = 0; row < 5; row++) {
        const n = 9 + row * 2;
        for (let i = 0; i < n; i++) {
          st.clouds.push({
            x: ((i + Math.random() * 0.8) / n) * 1.5 - 0.25,
            y: -0.02 + row * 0.07 + Math.random() * 0.04,
            rx: 0.16 + row * 0.03 + Math.random() * 0.1,
            sp: 0.5 + row * 0.35 + Math.random() * 0.3,
            shape: Math.floor(Math.random() * 5),
            thin: 0.6 + Math.random() * 0.7, // how much light comes through
            lit: 0,
          });
        }
      }
    }
    // The light takes a touch of the cover's colour.
    if (st.tintFor !== pal[0]) {
      st.tintFor = pal[0];
      const mix = (a, b, k) => a.map((v, i) => Math.round(v + (b[i] - v) * k));
      st.core = mix([238, 242, 255], pal[0], 0.1);
      st.tint = mix([180, 198, 255], pal[0], 0.35);
      st.glow = stormSprite(mix([200, 212, 255], pal[0], 0.3), 1, 0.35);
      // The clouds a dark slate with a little of the cover in them, and their
      // lit side pale.
      // Some puffs a shade lighter than others, so the deck has texture
      // between flashes.
      st.dark = st.shapes.map((sh, i) => stormPuff(sh, mix(i % 2 ? [34, 38, 52] : [58, 62, 80], pal[0], 0.12), 0.75));
      st.lit = st.shapes.map((sh) => stormPuff(sh, mix([185, 195, 235], pal[0], 0.25), 0.7));
      st.mistImg = stormSprite(mix([150, 160, 185], pal[0], 0.15), 1, 0.45);
    }
    if (m.drop) {
      // How big a strike is, from how loud the song is at that moment and
      // whether a bass hit comes with it - the sharp high itself tops out on
      // most strikes, so it cannot tell them apart. Measured over 300 of the
      // owner's songs: big close bolts 12% (were 79%, at the owner's asking
      // for fewer), bolts far off or across the clouds 37%, the clouds lit
      // alone 51%.
      const ld = m.dropLoud || 0;
      const bass = m.dropBass || 0;
      const s = ld >= 0.85 && bass >= 0.3 ? 0.7 + 0.3 * Math.min(1, bass)
        : ld >= 0.82 ? 0.3 + 0.39 * Math.min(1, (ld - 0.82) / 0.18)
        : 0.29 * Math.min(1, ld / 0.82);
      // A double strike for the first after a quiet spell, the second a
      // little after and a little weaker.
      for (let k = 0; k < (m.firstDrop ? 2 : 1); k++) st.bolts.push(stormBolt(w, h, k ? Math.max(0, s - 0.25) : s, -0.14 * k));
      if (st.bolts.length > 6) st.bolts.splice(0, st.bolts.length - 6);
    }
    // How lit everything is this frame.
    let sky = 0;
    let rainLit = 0;
    for (const bo of st.bolts) {
      bo.age += dt;
      bo.I = bo.age < 0 ? 0 : stormLight(bo);
      sky = Math.max(sky, bo.I * (bo.tier === 2 ? 1 : bo.tier ? 0.25 : 0.2));
      rainLit = Math.max(rainLit, bo.I * (bo.tier === 2 ? 1 : bo.tier ? 0.5 : 0.3));
    }
    const shine = 0.5 + 0.5 * bright;
    if (sky > 0.01) {
      g.fillStyle = rgba(st.tint, sky * sky * 0.4 * shine);
      g.fillRect(0, 0, w, h);
    }
    // The clouds: a dark band over the top of the screen with the deck of
    // puffs in it, always there; then a broad glow round each place a strike
    // lights, and each puff lit by its own shape, by how near it is and how
    // thin it is.
    if (st.bandFor !== h) {
      st.bandFor = h;
      st.band = g.createLinearGradient(0, 0, 0, h * 0.5);
      // Solid at the very top, so nothing behind shows through there between
      // the puffs (on a phone it did, at 97%).
      st.band.addColorStop(0, 'rgb(34, 38, 52)');
      st.band.addColorStop(0.22, 'rgba(30, 34, 47, 0.97)');
      st.band.addColorStop(0.5, 'rgba(16, 18, 28, 0.7)');
      st.band.addColorStop(1, 'rgba(10, 12, 20, 0)');
    }
    g.fillStyle = st.band;
    g.fillRect(0, 0, w, h * 0.5);
    const R = Math.max(w, h * 0.7);
    // Gusts: now and then, and sometimes with a big strike, the wind picks up
    // for a few seconds, slanting the rain hard (mostly with the wind, now and
    // then against it), then dies back.
    st.gustT = (st.gustT === undefined ? 8 + Math.random() * 10 : st.gustT) - dt;
    if (st.gustT <= 0 || (m.drop && (m.dropPower || 0) > 0.85 && !(st.gustHold > 0) && Math.random() < 0.35)) {
      st.gustHold = 2 + Math.random() * 2;
      st.gustAim = (0.18 + Math.random() * 0.17) * (Math.random() < 0.8 ? 1 : -0.6);
      st.gustT = 10 + Math.random() * 15;
    }
    st.gustHold = Math.max(0, (st.gustHold || 0) - dt);
    const gustTo = st.gustHold > 0 ? st.gustAim : 0;
    st.gust = (st.gust || 0) + (gustTo - (st.gust || 0)) * Math.min(1, dt * (gustTo ? 1.8 : 0.7));
    const wind = Math.sin(ck * 0.3) * 0.15 + 0.12 + st.gust;
    for (const c of st.clouds) {
      c.x += dt * 0.006 * c.sp * (0.5 + wind * 3);
      if (c.x > 1.35) c.x -= 1.7;
      c.lit = 0;
      g.globalAlpha = 0.9;
      stormDrawPuff(g, st.dark[c.shape], c, w, h, R);
    }
    g.globalCompositeOperation = 'lighter';
    for (const bo of st.bolts) {
      if (bo.age < 0) continue;
      for (const l of bo.lights) {
        const I = Math.max(0, 1 - (bo.age - l.delay) * (bo.tier ? 2.6 : 1.8)) * (bo.age >= l.delay ? 1 : 0) * l.k;
        l.I = I;
        if (I < 0.01) continue;
        g.globalAlpha = Math.min(1, I * 0.18 * shine);
        g.drawImage(st.glow, l.x - l.r * 1.3, l.y - l.r * 0.6, l.r * 2.6, l.r * 1.2);
        for (const c of st.clouds) {
          const dx = (c.x * w - l.x) / l.r;
          const dy = (c.y * h - l.y) / (l.r * 0.6);
          c.lit += I * c.thin * Math.exp(-(dx * dx + dy * dy));
        }
      }
    }
    // The lit puffs are painted over, not added: overlapping puffs (four or
    // five deep in places) added together burned the whole deck white;
    // painted, they build only to the lit cloud's own pale colour, its shape
    // still showing.
    g.globalCompositeOperation = 'source-over';
    for (const c of st.clouds) {
      if (c.lit < 0.01) continue;
      g.globalAlpha = Math.min(0.85, c.lit * 0.85) * shine;
      stormDrawPuff(g, st.lit[c.shape], c, w, h, R);
    }
    g.globalCompositeOperation = 'lighter';
    g.globalAlpha = 1;
    // Rain, slanted with a wind that drifts. Its speed eases towards what
    // the music asks rather than jumping on every kick - reported as the
    // animation skipping every few seconds - and it is drawn in six batches
    // (three colours, two weights), not a stroke and a new colour string per
    // drop: 420 of those a frame was garbage enough for a phone to stop and
    // collect it every few seconds, which is the other half of the skip.
    // In a flash it lights up: brighter, and a white streak over every drop.
    const target = 0.28 + 0.5 * loud * lv + 0.15 * kick;
    st.fallRate = st.fallRate === undefined ? target : st.fallRate + (target - st.fallRate) * Math.min(1, dt * 2.5);
    // Heavier with the music: a drizzle in a quiet part, a downpour in a loud
    // one - more drops, not only faster ones - eased over a couple of seconds.
    const heavyTo = 0.3 + 0.7 * Math.min(1, loud * lv * 1.15);
    st.heavy = st.heavy === undefined ? heavyTo : st.heavy + (heavyTo - st.heavy) * Math.min(1, dt * 0.6);
    const fall = h * st.fallRate * dt;
    const alpha = Math.min(1, (0.35 + 0.35 * lv) * (0.5 + 0.5 * bright) * (1 + 1.5 * rainLit));
    const minLen = h * 0.015;
    const LAYER_W = [0.8, 1.3, 2.1];
    const LAYER_A = [0.45, 0.75, 1];
    const LAYER_L = [0.6, 1, 1.5];
    g.lineCap = 'round';
    // Nine batches, three depths by three colours, far first.
    for (let layer = 0; layer < 3; layer++) {
      const len = LAYER_L[layer];
      for (let c = 0; c < 3; c++) {
        g.beginPath();
        for (const d of st.drops) {
          if (d.off || d.layer !== layer || d.c !== c) continue;
          const step = fall * d.s;
          g.moveTo(d.x, d.y);
          g.lineTo(d.x + step * wind * 2.2 * len, d.y + Math.max(step * 2.2, minLen) * len);
        }
        g.strokeStyle = rgba(pal[c], alpha * LAYER_A[layer]);
        g.lineWidth = LAYER_W[layer] * dpr;
        g.stroke();
      }
    }
    if (rainLit > 0.04) {
      g.beginPath();
      for (const d of st.drops) {
        if (d.off) continue;
        const step = fall * d.s;
        const len = LAYER_L[d.layer];
        g.moveTo(d.x, d.y);
        g.lineTo(d.x + step * wind * 2.2 * len, d.y + Math.max(step * 2.2, minLen) * len);
      }
      g.strokeStyle = rgba(st.core, Math.min(0.7, rainLit * 0.75));
      g.lineWidth = dpr;
      g.stroke();
    }
    // A drop starts where the wind now will carry it across the screen by
    // the time it lands, so no corner goes dry, a gust included.
    const drift = h * wind * 1.1;
    const from = Math.min(0, -drift) - w * 0.05;
    const across = w * 1.1 + Math.abs(drift);
    for (const d of st.drops) {
      if (d.off) {
        if (d.k > st.heavy) continue;
        d.off = false;
        d.y = -h * 0.05 * Math.random();
        d.x = from + Math.random() * across;
        continue;
      }
      const step = fall * d.s;
      d.y += step;
      d.x += step * wind;
      if (d.y > d.ground) {
        if (Math.random() < 0.3 && st.splash.length < 80) st.splash.push({ x: d.x, y: d.ground + Math.random() * h * 0.015, life: 1, c: d.c, big: 0.4 + 0.8 * d.z });
        d.off = d.k > st.heavy;
        d.y = -h * 0.05 * Math.random();
        d.x = from + Math.random() * across;
      }
    }
    // Mist along the ground where the rain lands, thicker in a downpour and
    // lit by the flash.
    g.globalAlpha = 1;
    for (const ms of st.mist) {
      ms.x += dt * 0.01 * ms.sp * (0.4 + wind * 3);
      if (ms.x > 1.4) ms.x -= 1.8;
      if (ms.x < -0.4) ms.x += 1.8;
      g.globalAlpha = Math.min(1, (0.06 + 0.1 * st.heavy + 0.3 * rainLit) * shine * (0.8 + 0.2 * Math.sin(ck * 0.5 + ms.ph)));
      g.drawImage(st.mistImg, ms.x * w - ms.rx * w, ms.y * h - ms.ry * h, ms.rx * w * 2, ms.ry * h * 2);
    }
    g.globalAlpha = 1;
    // Splashes, sparks and bolts are aged in place, the finished ones dropped
    // from the same array, rather than a new array filtered out every frame.
    g.lineWidth = dpr;
    let keep = 0;
    for (const sp of st.splash) {
      sp.life -= dt * (sp.bolt ? 1.4 : 2.5);
      if (sp.life <= 0) continue;
      st.splash[keep++] = sp;
      g.strokeStyle = sp.bolt ? rgba(st.core, sp.life * 0.6 * shine) : rgba(pal[sp.c], sp.life * 0.5 * bright);
      g.beginPath();
      g.ellipse(sp.x, sp.y, ((1 - sp.life) * 14 * dpr + 2) * sp.big, ((1 - sp.life) * 4 * dpr + 1) * sp.big, 0, 0, Math.PI * 2);
      g.stroke();
    }
    st.splash.length = keep;
    keep = 0;
    g.lineWidth = 1.4 * dpr;
    for (const sk of st.sparks) {
      sk.life -= dt * 1.6;
      if (sk.life <= 0) continue;
      st.sparks[keep++] = sk;
      sk.vy += h * 1.6 * dt;
      sk.x += sk.vx * dt;
      sk.y += sk.vy * dt;
      g.strokeStyle = rgba(st.core, sk.life * 0.8 * shine);
      g.beginPath();
      g.moveTo(sk.x, sk.y);
      g.lineTo(sk.x - sk.vx * 0.03, sk.y - sk.vy * 0.03);
      g.stroke();
    }
    st.sparks.length = keep;
    // The bolts: a white core thick at the top and thinning towards the end; the
    // branches dim faster than the main channel; and after the flashes a
    // faint image of the channel lingers, as it does on the eye.
    const drawBolt = (bo) => {
      const I = bo.I;
      const Ib = I * I;
      const sc = bo.scale * dpr;
      const main = bo.main;
      // One faint glow round the bolt, between the two it had (a wide band and
      // a narrower bright one, which read as a ghost of the bolt) and none,
      // the owner's call.
      if (I > 0.01) {
        g.strokeStyle = rgba(st.tint, 0.14 * I);
        g.lineWidth = 7 * sc;
        g.beginPath();
        stormPath(g, main, 0, main.length - 2);
        g.stroke();
        if (Ib > 0.01) {
          g.strokeStyle = rgba(st.tint, 0.14 * Ib);
          g.lineWidth = 4 * sc;
          g.beginPath();
          for (const b of bo.branches) stormPath(g, b.pts, 0, b.pts.length - 2);
          g.stroke();
        }
      }
      if (I > 0.01) {
        const n = main.length / 2;
        g.strokeStyle = rgba(st.core, I);
        for (let q = 0; q < 4; q++) {
          const a = Math.floor((n - 1) * q / 4) * 2;
          const z = Math.floor((n - 1) * (q + 1) / 4) * 2;
          if (z <= a) continue;
          g.lineWidth = (3.4 - 0.75 * q) * sc;
          g.beginPath();
          stormPath(g, main, a, z);
          g.stroke();
        }
        if (Ib > 0.01) {
          g.strokeStyle = rgba(st.core, Ib);
          // Each branch thins and fades towards its tip, in three stretches.
          for (const b of bo.branches) {
            const bn = b.pts.length / 2;
            for (let q = 0; q < 3; q++) {
              const a = Math.floor((bn - 1) * q / 3) * 2;
              const z = Math.floor((bn - 1) * (q + 1) / 3) * 2;
              if (z <= a) continue;
              g.strokeStyle = rgba(st.core, Ib * (1 - q * 0.28));
              g.lineWidth = 2.6 * b.w * sc * (1 - q * 0.28);
              g.beginPath();
              stormPath(g, b.pts, a, z);
              g.stroke();
            }
          }
        }
      }
      const after = 0.22 * (1 - bo.age / bo.life);
      if (after > 0.01) {
        g.strokeStyle = rgba(st.tint, after);
        g.lineWidth = 1.6 * sc;
        g.beginPath();
        stormPath(g, main, 0, main.length - 2);
        g.stroke();
      }
      if (bo.tier === 2 && !bo.crawl) {
        // Where it lands: a glow on the ground, and the first stroke throws
        // up a burst of spray and a ring of splashes.
        if (I > 0.01) {
          g.globalAlpha = Math.min(1, I * 0.75 * shine);
          g.drawImage(st.glow, bo.ex - w * 0.3, bo.ey - h * 0.07, w * 0.6, h * 0.14);
          g.globalAlpha = 1;
        }
        if (!bo.landed) {
          bo.landed = true;
          for (let k = 0; k < 26 && st.sparks.length < 90; k++) st.sparks.push({ x: bo.ex, y: bo.ey, vx: (Math.random() - 0.5) * w * 0.5, vy: -(0.15 + Math.random() * 0.45) * h, life: 0.6 + Math.random() * 0.4 });
          for (let k = 0; k < 5 && st.splash.length < 90; k++) st.splash.push({ x: bo.ex + (Math.random() - 0.5) * w * 0.12, y: bo.ey + Math.random() * h * 0.02, life: 1, c: 0, big: 2.5 + Math.random() * 2, bolt: true });
        }
      }
    };
    keep = 0;
    for (const bo of st.bolts) {
      if (bo.age >= bo.life) continue;
      st.bolts[keep++] = bo;
      if (bo.tier && bo.age >= 0 && !bo.crawl) drawBolt(bo);
    }
    st.bolts.length = keep;
    g.globalCompositeOperation = 'source-over';
    // The upper clouds again, over the bolts (and the rain), so a bolt comes
    // out of a cloud lit by it rather than from the edge of the screen - the
    // owner's asking - and the rain falls from under the cloud.
    // And the top edge solid over the rain and bolts too, so nothing is seen
    // falling from above the clouds.
    if (st.capFor !== h) {
      st.capFor = h;
      st.cap = g.createLinearGradient(0, 0, 0, h * 0.16);
      st.cap.addColorStop(0, 'rgb(34, 38, 52)');
      st.cap.addColorStop(0.4, 'rgba(34, 38, 52, 0.9)');
      st.cap.addColorStop(1, 'rgba(34, 38, 52, 0)');
    }
    g.fillStyle = st.cap;
    g.fillRect(0, 0, w, h * 0.16);
    for (const c of st.clouds) {
      if (c.y > 0.2) continue;
      g.globalAlpha = 0.9;
      stormDrawPuff(g, st.dark[c.shape], c, w, h, R);
      if (c.lit < 0.01) continue;
      g.globalAlpha = Math.min(0.85, c.lit * 0.85) * shine;
      stormDrawPuff(g, st.lit[c.shape], c, w, h, R);
    }
    g.globalAlpha = 1;
    // A crawler goes across the clouds, so it is drawn over them (under
    // them, on a TV's wide clouds it was hidden).
    g.globalCompositeOperation = 'lighter';
    for (const bo of st.bolts) if (bo.crawl && bo.tier && bo.age >= 0) drawBolt(bo);
    g.globalCompositeOperation = 'source-over';
  },

  // Synthwave: a neon grid racing towards you under a striped sunset sun that
  // pulses on the kick, with mountains on the horizon.
  synthwave(st, m) {
    const { g, f, w, h, dpr, pal, rgba, dt, ck, kick, loud, lv, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    const S = Math.min(w, h);
    const hz = h * 0.46;
    if (!st.stars) {
      st.stars = Array.from({ length: 90 }, () => ({ x: Math.random(), y: Math.random() * 0.42, b: Math.random() }));
      st.scroll = 0;
      const ridge = (n, rough) => Array.from({ length: n + 1 }, (_, i) => (0.35 + 0.65 * Math.abs(Math.sin(i * 1.7 + rough)) * (0.5 + 0.5 * Math.sin(i * 0.6 + rough * 2))));
      st.far = ridge(18, 1.3);
      st.near = ridge(12, 4.1);
    }
    for (const s of st.stars) {
      g.fillStyle = vizColor(VIZ_WHITE, (0.25 + 0.5 * s.b * (0.5 + 0.5 * Math.sin(ck * 2 + s.x * 40))) * bright);
      g.fillRect(s.x * w, s.y * h, 1.5 * dpr, 1.5 * dpr);
    }
    // The sun, striped across its lower half.
    const r = S * 0.24 * (1 + 0.06 * kick);
    const sy = hz - r * 0.35;
    const sun = g.createLinearGradient(0, sy - r, 0, sy + r);
    sun.addColorStop(0, rgba(pal[0], 0.95));
    sun.addColorStop(1, rgba(pal[1], 0.95));
    g.save();
    g.beginPath();
    g.arc(w / 2, sy, r, 0, Math.PI * 2);
    g.clip();
    g.fillStyle = sun;
    g.fillRect(w / 2 - r, sy - r, r * 2, r * 2);
    g.globalCompositeOperation = 'destination-out';
    for (let i = 0; i < 7; i++) {
      const t = ((i / 7) + ck * 0.05) % 1;
      const y = sy + r * t;
      g.fillRect(w / 2 - r, y, r * 2, r * 0.02 + r * 0.09 * t);
    }
    g.restore();
    const glow = g.createRadialGradient(w / 2, sy, r * 0.8, w / 2, sy, r * 2.2);
    glow.addColorStop(0, rgba(pal[0], (0.25 + 0.3 * kick) * bright));
    glow.addColorStop(1, rgba(pal[0], 0));
    g.fillStyle = glow;
    g.fillRect(0, 0, w, hz);
    // Mountains, far then near.
    for (const [ridge, height, alpha] of [[st.far, 0.13, 0.75], [st.near, 0.09, 0.92]]) {
      g.fillStyle = `rgba(12, 6, 24, ${alpha})`;
      g.beginPath();
      g.moveTo(0, hz);
      ridge.forEach((v, i) => g.lineTo((i / (ridge.length - 1)) * w, hz - v * h * height));
      g.lineTo(w, hz);
      g.closePath();
      g.fill();
    }
    // The floor.
    g.fillStyle = 'rgba(10, 4, 20, 0.85)';
    g.fillRect(0, hz, w, h - hz);
    st.scroll = (st.scroll + dt * (0.25 + 1.1 * loud * lv + 0.6 * kick)) % 1;
    const line = rgba(pal[2], (0.55 + 0.35 * kick) * (0.5 + 0.5 * bright));
    g.strokeStyle = line;
    g.lineWidth = 1.5 * dpr;
    for (let i = -14; i <= 14; i++) {
      g.beginPath();
      g.moveTo(w / 2 + i * w * 0.04, hz);
      g.lineTo(w / 2 + i * w * 0.5, h);
      g.stroke();
    }
    for (let k = 0; k < 14; k++) {
      const t = ((k / 14) + st.scroll) % 1;
      const y = hz + (h - hz) * t * t;
      g.lineWidth = (0.6 + 2 * t) * dpr;
      g.beginPath();
      g.moveTo(0, y);
      g.lineTo(w, y);
      g.stroke();
    }
    // A glow along the horizon.
    const hg = g.createLinearGradient(0, hz - h * 0.03, 0, hz + h * 0.05);
    hg.addColorStop(0, rgba(pal[2], 0));
    hg.addColorStop(0.5, rgba(pal[2], 0.5 * bright));
    hg.addColorStop(1, rgba(pal[2], 0));
    g.fillStyle = hg;
    g.fillRect(0, hz - h * 0.03, w, h * 0.08);
  },

  // Galaxy: a spiral galaxy turning round the middle of the screen, inner stars
  // faster than outer, arms winding tighter when loud, the core flashing on
  // the kick and a ripple on each bar.
  galaxy(st, m) {
    const { g, f, w, h, cx, cy, dpr, pal, rgba, dt, ck, kick, loud, lv, newBeat, downbeat, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    const S = Math.min(w, h);
    if (!st.pts) {
      st.pts = Array.from({ length: Math.round(900 * VIZ_DENSITY) }, (_, i) => ({ r: Math.sqrt(Math.random()), arm: i % 3, off: (Math.random() - 0.5) * 0.6, c: i % 3, b: Math.random() }));
      st.bg = Array.from({ length: 120 }, () => ({ x: Math.random(), y: Math.random(), b: Math.random() }));
      st.turn = 0;
      st.ripples = [];
    }
    for (const s of st.bg) {
      g.fillStyle = vizColor(VIZ_WHITE, 0.15 + 0.35 * s.b);
      g.fillRect(s.x * w, s.y * h, dpr, dpr);
    }
    st.turn += dt * (0.12 + 0.5 * loud * lv);
    const R = S * 0.46 * (1 + 0.06 * kick);
    const wind = 3.2 + 1.8 * loud;
    g.globalCompositeOperation = 'lighter';
    const core = g.createRadialGradient(cx, cy, 0, cx, cy, R * (0.35 + 0.2 * kick));
    core.addColorStop(0, `rgba(255, 245, 235, ${(0.35 + 0.5 * kick) * (0.5 + 0.5 * bright)})`);
    core.addColorStop(1, rgba(pal[0], 0));
    g.fillStyle = core;
    g.fillRect(0, 0, w, h);
    for (const p of st.pts) {
      const a = p.arm * ((Math.PI * 2) / 3) + p.r * wind + st.turn / (0.25 + p.r) + p.off / (0.4 + p.r);
      const x = cx + Math.cos(a) * p.r * R;
      const y = cy + Math.sin(a) * p.r * R * 0.8;
      const sz = (0.8 + 1.6 * p.b) * dpr;
      g.fillStyle = rgba(pal[p.c], (0.35 + 0.55 * p.b) * (1 - p.r * 0.45) * (0.45 + 0.55 * bright));
      g.fillRect(x, y, sz, sz);
    }
    if (m.drop) st.ripples.push({ r: R * 0.2, life: 1 });
    st.ripples = st.ripples.filter((rp) => {
      rp.r += dt * S * 0.7;
      rp.life -= dt * 0.9;
      if (rp.life <= 0) return false;
      g.strokeStyle = rgba(pal[1], rp.life * 0.4 * bright);
      g.lineWidth = 2 * dpr;
      g.beginPath();
      g.ellipse(cx, cy, rp.r, rp.r * 0.8, 0, 0, Math.PI * 2);
      g.stroke();
      return true;
    });
    g.globalCompositeOperation = 'source-over';
  },

  // Aurora: curtains of northern lights swaying across a starry sky, flaring
  // on the beat and brighter when loud.
  aurora(st, m) {
    const { g, f, w, h, dpr, pal, rgba, ck, kick, snare, loud, lv, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    if (!st.stars) st.stars = Array.from({ length: 140 }, () => ({ x: Math.random(), y: Math.random() * 0.8, b: Math.random(), t: Math.random() * 6 }));
    for (const s of st.stars) {
      g.fillStyle = vizColor(VIZ_WHITE, (0.2 + 0.5 * s.b * (0.5 + 0.5 * Math.sin(ck * 1.5 + s.t))) * bright);
      g.fillRect(s.x * w, s.y * h, 1.4 * dpr, 1.4 * dpr);
    }
    g.globalCompositeOperation = 'lighter';
    const steps = 60;
    for (let c = 0; c < 3; c++) {
      const base = h * (0.16 + c * 0.07);
      const depth = h * (0.22 + 0.22 * loud * lv + 0.08 * kick) * (1 - c * 0.15);
      const topAt = (u) => base + h * 0.05 * Math.sin(u * 6 + ck * (0.5 + c * 0.2) + c) + h * 0.03 * Math.sin(u * 13 - ck * 0.8 + c * 2);
      const color = pal[c];
      const glow = (0.28 + 0.4 * kick + 0.12 * snare) * (0.35 + 0.65 * bright);
      const grad = g.createLinearGradient(0, base - h * 0.08, 0, base + depth + h * 0.08);
      grad.addColorStop(0, rgba(color, 0));
      grad.addColorStop(0.25, rgba(color, glow * 0.9));
      grad.addColorStop(0.7, rgba(color, glow * 0.35));
      grad.addColorStop(1, rgba(color, 0));
      g.fillStyle = grad;
      g.beginPath();
      for (let i = 0; i <= steps; i++) {
        const u = i / steps;
        const x = u * w;
        const y = topAt(u);
        if (i) g.lineTo(x, y); else g.moveTo(x, y);
      }
      for (let i = steps; i >= 0; i--) {
        const u = i / steps;
        g.lineTo(u * w, topAt(u) + depth * (0.75 + 0.25 * Math.sin(u * 9 + ck + c)));
      }
      g.closePath();
      g.fill();
      // The folds: faint rays hanging from the top edge.
      g.lineWidth = 2 * dpr;
      for (let i = 0; i <= 50; i++) {
        const u = i / 50;
        const x = u * w;
        const y = topAt(u);
        const a = glow * 0.35 * (0.5 + 0.5 * Math.sin(u * 40 + ck * 2 + c * 3));
        g.strokeStyle = rgba(color, a);
        g.beginPath();
        g.moveTo(x, y);
        g.lineTo(x, y + depth * 0.8);
        g.stroke();
      }
    }
    g.globalCompositeOperation = 'source-over';
  },

  // Lava: big soft glowing blobs rising and wobbling like a lava lamp,
  // bouncing on the kick, with small bubbles popping on the snare.
  lava(st, m) {
    const { g, f, w, h, dpr, pal, rgba, dt, ck, kick, snare, loud, lv, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    const S = Math.min(w, h);
    if (!st.blobs) {
      st.blobs = Array.from({ length: 9 }, (_, i) => ({ x: Math.random() * w, y: Math.random() * h, r: S * (0.12 + Math.random() * 0.14), v: 0.3 + Math.random() * 0.7, ph: Math.random() * 6, c: i % 3 }));
      st.bubbles = [];
      st.snare = 0;
    }
    g.globalCompositeOperation = 'lighter';
    const rise = h * (0.03 + 0.12 * loud * lv) * dt;
    for (const b of st.blobs) {
      b.y -= rise * b.v;
      if (b.y < -b.r * 1.5) { b.y = h + b.r * 1.5; b.x = Math.random() * w; }
      const x = b.x + Math.sin(ck * 0.4 * b.v + b.ph) * S * 0.08;
      const r = b.r * (1 + 0.08 * Math.sin(ck * 1.3 + b.ph) + 0.18 * kick);
      const grad = g.createRadialGradient(x, b.y, 0, x, b.y, r);
      grad.addColorStop(0, rgba(pal[b.c], 0.75 * (0.45 + 0.55 * bright)));
      grad.addColorStop(0.55, rgba(pal[b.c], 0.45 * (0.45 + 0.55 * bright)));
      grad.addColorStop(1, rgba(pal[b.c], 0));
      g.fillStyle = grad;
      g.beginPath();
      g.arc(x, b.y, r, 0, Math.PI * 2);
      g.fill();
    }
    if (snare - st.snare > 0.3 && lv > 0.3) {
      for (let i = 0; i < 6; i++) st.bubbles.push({ x: Math.random() * w, y: h * (0.6 + Math.random() * 0.4), r: (2 + Math.random() * 5) * dpr, life: 1, c: Math.floor(Math.random() * 3) });
    }
    st.snare = snare;
    st.bubbles = st.bubbles.filter((bb) => {
      bb.life -= dt * 0.9;
      bb.y -= h * 0.25 * dt;
      if (bb.life <= 0) return false;
      g.strokeStyle = rgba(pal[bb.c], bb.life * 0.8 * bright);
      g.lineWidth = 1.2 * dpr;
      g.beginPath();
      g.arc(bb.x, bb.y, bb.r * (1 + (1 - bb.life) * 0.6), 0, Math.PI * 2);
      g.stroke();
      return true;
    });
    g.globalCompositeOperation = 'source-over';
  },
};

// Analysis: what the song's analysis found, laid out as a timeline moving
// past the moment playing (the line a third of the way across) - two
// seconds heard, four to come, all of it known ahead. Top to bottom: the
// beat of the bar, the bar and the tempo; the beats, each bar's first
// marked, and the lightning moments (a bar's first beat in the song's
// loudest 30%); how loud each moment is, the loud parts lit; the kick (bass
// hits) and the snare and hats (high hits); and what stood out as it played
// - the one reading made live rather than read ahead. Asked for by the owner
// to see what the analysis gives the looks.
function analysisScene(st, m) {
  const { g, f, w, h, dpr, pal, rgba, t, phase, novelty, playing } = m;
  f.clearRect(0, 0, w, h);
  g.clearRect(0, 0, w, h);
  const heard = viz.heard && audio.item && viz.heard.key === selectionKey(audio.item) ? viz.heard : null;
  const u = dpr;
  const font = (px, weight) => `${weight || 600} ${Math.round(px * u)}px system-ui, -apple-system, "Segoe UI", sans-serif`;
  const left = 16 * u;
  const right = w - 16 * u;
  const top = h * 0.2;
  // Clear of the title above and the play button below.
  const bottom = h * (TV ? 0.74 : 0.75);
  const past = 2;
  const ahead = 4;
  const pps = (right - left) / (past + ahead);
  const px = left + past * pps;
  const X = (tt) => px + (tt - t) * pps;
  // A veil so the words read over any cover.
  g.fillStyle = 'rgba(0, 0, 0, 0.35)';
  g.fillRect(0, top - 12 * u, w, bottom - top + 24 * u);
  g.textBaseline = 'alphabetic';
  if (!heard) {
    g.fillStyle = rgba(VIZ_WHITE, 0.85);
    g.font = font(16);
    g.textAlign = 'center';
    g.fillText('Not analysed yet', w / 2, (top + bottom) / 2 - 10 * u);
    g.fillStyle = rgba(VIZ_WHITE, 0.55);
    g.font = font(12, 500);
    g.fillText('Following the tempo only until the analysis arrives', w / 2, (top + bottom) / 2 + 14 * u);
    return;
  }
  const bs = heard.beats;
  const fps = heard.fps;
  if (heard.loudTop === undefined) {
    const sorted = Array.from(heard.loud).sort((x, y) => x - y);
    heard.loudTop = sorted[Math.floor(sorted.length * 0.7)];
  }
  // The beat playing now, and the tempo from its length.
  let lo = 0, hi = bs.length - 1;
  while (lo < hi) { const mid = (lo + hi + 1) >> 1; if (bs[mid] <= t) lo = mid; else hi = mid - 1; }
  const nxt = Math.min(bs.length - 1, lo + 1);
  const len = (nxt > lo ? bs[nxt] - bs[lo] : bs[lo] - bs[Math.max(0, lo - 1)]) || 0.5;
  const inBar = (i) => (((i - heard.down) % 4) + 4) % 4;
  const barOf = (i) => Math.floor((i - heard.down) / 4) + 1;
  const label = (text, x, y, px2, weight, alpha, align) => {
    g.textAlign = align || 'left';
    g.fillStyle = rgba(VIZ_WHITE, alpha);
    g.font = font(px2, weight);
    g.fillText(text, x, y);
  };

  // ---- the readout: the beat of the bar, the bar, the tempo
  const rTop = top;
  label('BEAT', left, rTop + 12 * u, 11, 600, 0.55);
  const nowIn = bs[lo] <= t ? inBar(lo) : -1;
  for (let k = 0; k < 4; k++) {
    const cxk = left + 12 * u + k * 26 * u;
    const cyk = rTop + 34 * u;
    const on = k === nowIn;
    const r = (k === 0 ? 9 : 7) * u * (on ? 1 + 0.35 * Math.exp(-phase * 5) : 1);
    g.beginPath();
    g.arc(cxk, cyk, r, 0, Math.PI * 2);
    g.fillStyle = on ? rgba(k === 0 ? VIZ_WHITE : pal[0], 0.95) : rgba(VIZ_WHITE, 0.14);
    g.fill();
    g.textAlign = 'center';
    g.fillStyle = on ? 'rgba(0, 0, 0, 0.8)' : rgba(VIZ_WHITE, 0.5);
    g.font = font(10, 700);
    g.fillText(String(k + 1), cxk, cyk + 3.5 * u);
  }
  label('BAR', w / 2 + 10 * u, rTop + 12 * u, 11, 600, 0.55, 'center');
  label(bs[lo] <= t && lo >= heard.down ? String(barOf(lo)) : '-', w / 2 + 10 * u, rTop + 40 * u, 22, 700, 0.95, 'center');
  label('TEMPO', right, rTop + 12 * u, 11, 600, 0.55, 'right');
  label(`${Math.round(60 / len)} bpm`, right, rTop + 40 * u, 22, 700, 0.95, 'right');

  // ---- the lanes
  const gridTop = rTop + 62 * u;
  const lanesTop = gridTop + 26 * u;
  const gap = 8 * u;
  const lanes = ['Loudness', 'Bass hits', 'Sharp highs  (drums, "s" sounds, strums)', 'Stood out  (as it played)'];
  const laneH = (bottom - lanesTop - gap * (lanes.length - 1)) / lanes.length;
  const laneY = (i) => lanesTop + i * (laneH + gap);
  for (let i = 0; i < lanes.length; i++) {
    g.fillStyle = 'rgba(255, 255, 255, 0.05)';
    g.fillRect(left, laneY(i), right - left, laneH);
  }
  // What has been heard, a shade lighter than what is to come.
  g.fillStyle = 'rgba(255, 255, 255, 0.05)';
  g.fillRect(left, lanesTop, px - left, bottom - lanesTop);
  const f0 = Math.max(0, Math.floor((t - past) * fps));
  const f1 = Math.min(heard.loud.length - 1, Math.ceil((t + ahead) * fps));
  const fx = (fi) => X(fi / fps);

  // Loudness: the whole curve, and the loud parts - over the line - lit.
  {
    const y0 = laneY(0), y1 = y0 + laneH;
    const yv = (v) => y1 - v * laneH * 0.92;
    const curve = () => {
      g.beginPath();
      g.moveTo(fx(f0), y1);
      for (let fi = f0; fi <= f1; fi++) g.lineTo(fx(fi), yv(heard.loud[fi]));
      g.lineTo(fx(f1), y1);
      g.closePath();
    };
    curve();
    g.fillStyle = rgba(pal[0], 0.35);
    g.fill();
    const cut = yv(heard.loudTop);
    g.save();
    g.beginPath();
    g.rect(left, y0, right - left, cut - y0);
    g.clip();
    curve();
    g.fillStyle = rgba(pal[0], 0.95);
    g.fill();
    g.restore();
    g.setLineDash([4 * u, 4 * u]);
    g.strokeStyle = rgba(VIZ_WHITE, 0.45);
    g.lineWidth = 1 * u;
    g.beginPath();
    g.moveTo(left, cut);
    g.lineTo(right, cut);
    g.stroke();
    g.setLineDash([]);
    label('loud part', right - 4 * u, cut - 3 * u, 10, 600, 0.6, 'right');
  }
  // The kick and the snare: every hit, as tall as it is sharp.
  const hits = (arr, lane, c) => {
    const y1 = laneY(lane) + laneH;
    const bw = Math.max(1.5 * u, pps / fps - 1 * u);
    g.beginPath();
    for (let fi = f0; fi <= f1; fi++) {
      const v = arr[fi];
      if (v < 0.04) continue;
      g.rect(fx(fi), y1 - v * laneH * 0.92, bw, v * laneH * 0.92);
    }
    g.fillStyle = rgba(c, 0.85);
    g.fill();
  };
  hits(heard.low, 1, pal[1]);
  hits(heard.high, 2, pal[2]);
  // Stood out: kept as it plays, since it compares each beat with the last
  // few - what is to come has not been judged yet.
  {
    if (!st.hist || (st.hist.length && t < st.hist[st.hist.length - 1].t - 0.5)) st.hist = [];
    if (playing && viz.scrubAt === undefined) st.hist.push({ t, n: novelty });
    while (st.hist.length && st.hist[0].t < t - past - 0.2) st.hist.shift();
    const y0 = laneY(3), y1 = y0 + laneH;
    g.save();
    g.beginPath();
    g.rect(left, y0, right - left, laneH);
    g.clip();
    g.beginPath();
    g.moveTo(X(st.hist.length ? st.hist[0].t : t), y1);
    for (const p of st.hist) g.lineTo(X(p.t), y1 - p.n * laneH * 0.92);
    g.lineTo(X(t), y1);
    g.closePath();
    g.fillStyle = rgba(VIZ_WHITE, 0.55);
    g.fill();
    g.restore();
    label('judged as it plays', px + 8 * u, y1 - 6 * u, 10, 500, 0.3);
  }

  // ---- the beats across everything: each bar's first bright and numbered,
  // and the lightning moments marked with a bolt.
  const b0 = Math.max(0, lo - Math.ceil(past / len) - 2);
  for (let i = b0; i < bs.length; i++) {
    const x = X(bs[i]);
    if (x > right) break;
    if (x < left) continue;
    const k = inBar(i);
    const first = k === 0 && i >= heard.down;
    g.strokeStyle = rgba(VIZ_WHITE, first ? 0.5 : 0.16);
    g.lineWidth = (first ? 2 : 1) * u;
    g.beginPath();
    g.moveTo(x, gridTop + 16 * u);
    g.lineTo(x, bottom);
    g.stroke();
    label(String(k + 1), x, gridTop + 12 * u, first ? 11 : 9, first ? 700 : 500, first ? 0.95 : 0.45, 'center');
  }
  // The lightning: a bolt, and a line down through the sharp highs, at every
  // strong sharp high on a beat (strikesAt) - the looks' own rule, so these
  // are exactly where Storm strikes, Fireworks has its finale and the others
  // make their biggest move.
  const f0s = Math.max(1, Math.floor((t - past) * fps));
  for (let fi = f0s; fi <= f1; fi++) {
    if (!strikesAt(heard, fi)) continue;
    const x = fx(fi);
    if (x < left || x > right) continue;
    g.strokeStyle = 'rgba(255, 220, 90, 0.55)';
    g.lineWidth = 2 * u;
    g.beginPath();
    g.moveTo(x, gridTop + 16 * u);
    g.lineTo(x, laneY(2) + laneH);
    g.stroke();
    const bx = x - 1 * u, by = gridTop + 1 * u;
    g.beginPath();
    g.moveTo(bx + 3 * u, by);
    g.lineTo(bx - 1 * u, by + 7 * u);
    g.lineTo(bx + 2 * u, by + 7 * u);
    g.lineTo(bx - 2 * u, by + 14 * u);
    g.lineTo(bx + 5 * u, by + 5 * u);
    g.lineTo(bx + 2 * u, by + 5 * u);
    g.closePath();
    g.fillStyle = 'rgba(255, 220, 90, 0.95)';
    g.fill();
  }

  // What was recorded for training, on the developer's install: each tap a
  // cyan mark and line, and the intensity a cyan line over the loudness.
  if (training.data && training.key === selectionKey(audio.item)) {
    g.strokeStyle = 'rgba(80, 220, 255, 0.8)';
    g.fillStyle = 'rgba(80, 220, 255, 0.95)';
    g.lineWidth = 2 * u;
    for (const at of training.data.taps) {
      const x = X(at);
      if (x < left || x > right) continue;
      g.beginPath();
      g.moveTo(x, gridTop + 18 * u);
      g.lineTo(x, bottom);
      g.stroke();
      g.beginPath();
      g.arc(x, gridTop + 20 * u, 4 * u, 0, Math.PI * 2);
      g.fill();
    }
    const iv = training.data.intensity;
    if (iv.length) {
      const y0 = laneY(0), y1 = y0 + laneH;
      g.beginPath();
      let pen = false;
      for (let i = 0; i < iv.length; i++) {
        const x = X(iv[i][0]);
        if (x < left - 20 || x > right + 20) { pen = false; continue; }
        const y = y1 - iv[i][1] * laneH * 0.92;
        if (pen && i > 0 && iv[i][0] - iv[i - 1][0] < 0.5) g.lineTo(x, y); else g.moveTo(x, y);
        pen = true;
      }
      g.stroke();
    }
  }

  // ---- the moment playing: a line, a dot on each lane where it is now,
  // and the lanes' names and readings.
  const glow = g.createLinearGradient(px - 10 * u, 0, px + 10 * u, 0);
  glow.addColorStop(0, rgba(VIZ_WHITE, 0));
  glow.addColorStop(0.5, rgba(VIZ_WHITE, 0.18));
  glow.addColorStop(1, rgba(VIZ_WHITE, 0));
  g.fillStyle = glow;
  g.fillRect(px - 10 * u, gridTop, 20 * u, bottom - gridTop);
  g.fillStyle = rgba(VIZ_WHITE, 0.95);
  g.fillRect(px - 1 * u, gridTop, 2 * u, bottom - gridTop);
  if (viz.scrubAt !== undefined) {
    const at = Math.max(0, viz.scrubAt);
    const text = `Play from ${Math.floor(at / 60)}:${String(Math.floor(at % 60)).padStart(2, '0')}`;
    g.font = font(12, 700);
    const tw = g.measureText(text).width + 16 * u;
    const bx = Math.max(left, Math.min(right - tw, px - tw / 2));
    g.fillStyle = 'rgba(255, 255, 255, 0.95)';
    g.fillRect(bx, gridTop - 26 * u, tw, 20 * u);
    g.textAlign = 'center';
    g.fillStyle = 'rgba(0, 0, 0, 0.9)';
    g.fillText(text, bx + tw / 2, gridTop - 12 * u);
  }
  const fiNow = Math.max(0, Math.min(heard.loud.length - 1, Math.floor(t * fps)));
  const nowVals = [heard.loud[fiNow], heard.low[fiNow], heard.high[fiNow], novelty];
  for (let i = 0; i < lanes.length; i++) {
    const y0 = laneY(i);
    const v = Math.max(0, Math.min(1, nowVals[i] || 0));
    label(lanes[i], left + 6 * u, y0 + 14 * u, 11, 600, 0.8);
    g.font = font(11, 600);
    const nameW = g.measureText(lanes[i]).width;
    label(`${Math.round(v * 100)}%`, left + 12 * u + nameW, y0 + 14 * u, 11, 700, 0.95);
    g.beginPath();
    g.arc(px, y0 + laneH - v * laneH * 0.92, 4 * u, 0, Math.PI * 2);
    g.fillStyle = rgba(VIZ_WHITE, 1);
    g.fill();
  }
}


// Dragging the Analysis look moves through the song, the owner's asking:
// the timeline follows the finger (drag left for later, right for earlier)
// and letting go plays from there. A touch that starts on the timeline is
// the look's alone - Now Playing's swipe (which changes song) and hold
// (which brings up the buttons) listen on the whole screen, so they are
// kept from it here, in the capture phase, before they hear it. A tap there
// still reaches the page as a click, so a double tap still changes the look.
const analysisScrub = (() => {
  let drag = null;
  const PAST = 2, AHEAD = 4, MARGIN = 16; // as analysisScene draws it
  const bounds = () => {
    const r = $('np-stage').getBoundingClientRect();
    return { r, top: r.top + r.height * 0.2, bottom: r.top + r.height * (TV ? 0.74 : 0.75) };
  };
  const owns = (x, y, target) => {
    if (training.mode) return false;
    const np = $('now-playing');
    if (np.classList.contains('hidden') || coverStyle() !== 'analysis' || !audio.item || audio.item.kind !== 'music') return false;
    if (audio.npMode === 'queue' || !(viz.heard && viz.heard.key === selectionKey(audio.item))) return false;
    if (target.closest('#np-looks, #item-menu, .np-hold-layer, #np-hold-extra, .np-controls, button, input, a')) return false;
    const b = bounds();
    return y >= b.top && y <= b.bottom && x >= b.r.left && x <= b.r.right;
  };
  const player = () => $('audio-player');
  const timeAt = (x) => {
    const b = bounds();
    const pps = (b.r.width - 2 * MARGIN) / (PAST + AHEAD);
    const dur = Number.isFinite(player().duration) ? player().duration : Infinity;
    return Math.max(0, Math.min(dur - 0.5, drag.t0 - (x - drag.x0) / pps));
  };
  const stop = (e) => { e.stopPropagation(); };
  document.addEventListener('pointerdown', (e) => {
    if (e.button !== 0 || !owns(e.clientX, e.clientY, e.target)) return;
    drag = { id: e.pointerId, x0: e.clientX, y0: e.clientY, t0: player().currentTime || 0, moved: false };
    stop(e);
  }, true);
  document.addEventListener('pointermove', (e) => {
    if (!drag || e.pointerId !== drag.id) return;
    stop(e);
    if (!drag.moved && Math.hypot(e.clientX - drag.x0, e.clientY - drag.y0) < 8) return;
    if (!drag.moved) {
      drag.moved = true;
      // Moving from the start keeps the timeline from jumping by the slop.
      drag.x0 = e.clientX;
    }
    viz.scrubAt = timeAt(e.clientX);
    viz.start();
  }, true);
  const end = (e) => {
    if (!drag || e.pointerId !== drag.id) return;
    stop(e);
    if (drag.moved && e.type === 'pointerup') {
      player().currentTime = timeAt(e.clientX);
      // The end of a drag is not a tap (for the double tap).
      npSwipe.draggedAt = performance.now();
    }
    viz.scrubAt = undefined;
    drag = null;
  };
  document.addEventListener('pointerup', end, true);
  document.addEventListener('pointercancel', end, true);
  // A touch's own events, for the swipe that listens to those: while the
  // look has the finger, they stop here, and the page does not scroll.
  for (const type of ['touchstart', 'touchmove', 'touchend', 'touchcancel']) {
    document.addEventListener(type, (e) => {
      const tt = e.changedTouches[0];
      const mine = drag || (type === 'touchstart' && tt && owns(tt.clientX, tt.clientY, e.target));
      if (!mine) return;
      e.stopPropagation();
      if (type === 'touchmove' && e.cancelable) e.preventDefault();
    }, { capture: true, passive: false });
  }
  return { get active() { return Boolean(drag && drag.moved); } };
})();


// A view screen scrolled anyway - an older web view without overflow: clip
// (style.css) - is put straight back: none of them scrolls.
document.addEventListener('scroll', (e) => {
  const el = e.target;
  if (el && el.matches && el.matches('.now-playing, .now-playing .np-inner, .photo-viewer, .reader, .overlay') && (el.scrollTop || el.scrollLeft)) {
    el.scrollTop = 0;
    el.scrollLeft = 0;
  }
}, true);

// Training the looks, on SoundStorm's developer's install alone (the server
// says so in the session: SOUNDSTORM_TRAINING; see httpapi/training.go). In
// Now Playing, either tap where a big moment - lightning - should be, or hold
// and slide up and down for how intense the music should feel; what is
// recorded is kept per song on that server, and a training script learns
// from it, for every install. Nobody else ever sees any of this. While it
// records, Now Playing's own gestures (swipe, hold, double tap, dragging the
// Analysis look) stand down, so every touch is a recording.
const training = {
  mode: null,        // 'taps' or 'intensity'
  key: '',           // the song the data is for
  item: null,        // and the song itself, to save against
  data: null,        // { taps: [], intensity: [] }
  dirty: false,
  saveTimer: 0,
  watch: 0,
  slide: null,       // { id, last } while a finger is held in intensity mode
};

function trainingSongKey() {
  return audio.item && audio.item.kind === 'music' ? selectionKey(audio.item) : '';
}

function trainingQuery(item) {
  return `source=${encodeURIComponent(item.sourceId)}&id=${encodeURIComponent(item.id)}`;
}

async function trainingLoad() {
  const item = audio.item;
  const key = trainingSongKey();
  training.key = key;
  training.item = key ? item : null;
  training.data = null;
  if (!key) return;
  const { ok, body } = await api(`/api/training/song?${trainingQuery(item)}`);
  if (training.key !== key) return;
  training.data = ok && body ? { taps: body.taps || [], intensity: body.intensity || [] } : { taps: [], intensity: [] };
  trainingBar();
}

async function trainingSave() {
  clearTimeout(training.saveTimer);
  if (!training.dirty || !training.data || !training.item) return;
  training.dirty = false;
  const data = training.data;
  const item = training.item;
  data.taps.sort((a, b) => a - b);
  data.intensity.sort((a, b) => a[0] - b[0]);
  const { ok } = await api(`/api/training/song?${trainingQuery(item)}`, {
    method: 'PUT',
    body: JSON.stringify({ taps: data.taps, intensity: data.intensity, lead: vizLead(), device: navigator.userAgent }),
  });
  if (!ok) { training.dirty = true; showToast('Could not save the recording; trying again', 'error'); trainingSaveSoon(); }
}

function trainingSaveSoon() {
  clearTimeout(training.saveTimer);
  training.saveTimer = setTimeout(trainingSave, 2000);
}

function trainingStart(mode) {
  training.mode = mode;
  if (trainingSongKey() !== training.key || !training.data) trainingLoad();
  // A new song is loaded as it starts; what was recorded for the last is
  // saved first.
  clearInterval(training.watch);
  training.watch = setInterval(async () => {
    if (trainingSongKey() === training.key || training.switching) return;
    training.switching = true;
    try {
      await trainingSave(); // against the song it was recorded for
      await trainingLoad();
    } finally {
      training.switching = false;
    }
  }, 700);
  trainingBar();
}

async function trainingStop() {
  await trainingSave();
  training.mode = null;
  clearInterval(training.watch);
  trainingBar();
}

// The bar across Now Playing while recording: what is being recorded, how
// much, and Undo, Clear and Done.
function trainingBar() {
  let bar = $('np-train');
  if (!training.mode) { if (bar) bar.remove(); return; }
  if (!bar) {
    bar = document.createElement('div');
    bar.id = 'np-train';
    $('now-playing').append(bar);
  }
  const d = training.data;
  const what = training.mode === 'taps'
    ? `Big moments · ${d ? d.taps.length : '…'}`
    : `Intensity · ${d ? Math.round(d.intensity.length / 10) : '…'}s`;
  const text = document.createElement('span');
  text.textContent = what;
  const button = (name, fn) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = name;
    b.addEventListener('click', (e) => { e.stopPropagation(); fn(); });
    return b;
  };
  bar.replaceChildren(text,
    button('Undo', () => {
      if (!training.data) return;
      if (training.mode === 'taps') training.data.taps.pop();
      else {
        // The last held stretch: back to the last gap of over half a second.
        const iv = training.data.intensity;
        let i = iv.length - 1;
        while (i > 0 && iv[i][0] - iv[i - 1][0] < 0.5) i--;
        iv.length = Math.max(0, i);
      }
      training.dirty = true;
      trainingSaveSoon();
      trainingBar();
    }),
    button('Clear', () => {
      if (!training.data || !confirm(`Clear this song's ${training.mode === 'taps' ? 'taps' : 'intensity'}?`)) return;
      if (training.mode === 'taps') training.data.taps = []; else training.data.intensity = [];
      training.dirty = true;
      trainingSave();
      trainingBar();
    }),
    button('Done', trainingStop));
}

// Where a touch on Now Playing is a recording: not on the bar, a sheet or a
// menu, nor on any button (play, Looks), which still work.
function trainingTouch(target) {
  return training.mode && training.data && !$('now-playing').classList.contains('hidden')
    && !target.closest('#np-train, #np-looks, #item-menu, .np-controls, button, a, input');
}

function trainingFlash(x, y, cls) {
  const dot = document.createElement('div');
  dot.className = cls;
  dot.style.left = `${x}px`;
  dot.style.top = `${y}px`;
  $('now-playing').append(dot);
  setTimeout(() => dot.remove(), 600);
}

function trainingIntensityAt(y) {
  const r = $('now-playing').getBoundingClientRect();
  return Math.max(0, Math.min(1, 1 - (y - r.top - r.height * 0.15) / (r.height * 0.7)));
}

document.addEventListener('pointerdown', (e) => {
  if (!trainingTouch(e.target)) return;
  e.stopPropagation();
  const t = ($('audio-player').currentTime || 0) + vizLead();
  if (training.mode === 'taps') {
    training.data.taps.push(+t.toFixed(3));
    if (navigator.vibrate) navigator.vibrate(8);
    trainingFlash(e.clientX, e.clientY, 'train-tap');
  } else {
    training.slide = { id: e.pointerId, y: e.clientY };
    training.data.intensity.push([+t.toFixed(2), +trainingIntensityAt(e.clientY).toFixed(3)]);
    let line = $('np-train-level');
    if (!line) { line = document.createElement('div'); line.id = 'np-train-level'; $('now-playing').append(line); }
    line.style.top = `${e.clientY}px`;
    line.dataset.v = `${Math.round(trainingIntensityAt(e.clientY) * 100)}%`;
  }
  training.dirty = true;
  trainingSaveSoon();
  trainingBar();
}, true);
document.addEventListener('pointermove', (e) => {
  if (!training.slide || e.pointerId !== training.slide.id) return;
  e.stopPropagation();
  const line = $('np-train-level');
  if (line) { line.style.top = `${e.clientY}px`; line.dataset.v = `${Math.round(trainingIntensityAt(e.clientY) * 100)}%`; }
  training.slide.y = e.clientY;
}, true);
// While a finger is held, its height is sampled ten times a second.
setInterval(() => {
  if (!training.slide || training.slide.y === undefined || !training.data) return;
  const t = ($('audio-player').currentTime || 0) + vizLead();
  training.data.intensity.push([+t.toFixed(2), +trainingIntensityAt(training.slide.y).toFixed(3)]);
  training.dirty = true;
  if (training.data.intensity.length % 10 === 0) trainingBar();
}, 100);
for (const type of ['pointerup', 'pointercancel']) {
  document.addEventListener(type, (e) => {
    if (!training.slide || e.pointerId !== training.slide.id) return;
    e.stopPropagation();
    training.slide = null;
    const line = $('np-train-level');
    if (line) line.remove();
    trainingSaveSoon();
  }, true);
}
// A touch's own events, for Now Playing's swipe: stopped while recording,
// and the page does not scroll.
for (const type of ['touchstart', 'touchmove', 'touchend', 'touchcancel']) {
  document.addEventListener(type, (e) => {
    if (!(training.slide || (type === 'touchstart' && trainingTouch(e.target)))) return;
    e.stopPropagation();
    if (type === 'touchmove' && e.cancelable) e.preventDefault();
  }, { capture: true, passive: false });
}
// A click on Now Playing while recording is not a tap for the next look.
document.addEventListener('click', (e) => { if (trainingTouch(e.target)) e.stopPropagation(); }, true);
window.addEventListener('pagehide', () => { trainingSave(); });

// The Looks sheet's training section, on the developer's install alone.
function looksTraining() {
  if (!state.training || !audio.item || audio.item.kind !== 'music') return [];
  const h = document.createElement('p');
  h.className = 'np-looks-group';
  h.textContent = 'Training (only on this server)';
  const grid = document.createElement('div');
  grid.className = 'np-looks-grid';
  const add = (name, fn, on) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = `np-look${on ? ' on' : ''}`;
    b.textContent = name;
    b.addEventListener('click', (e) => { e.stopPropagation(); fn(); closeLooks(); });
    grid.append(b);
  };
  add('Tap big moments', () => trainingStart('taps'), training.mode === 'taps');
  add('Slide intensity', () => trainingStart('intensity'), training.mode === 'intensity');
  if (training.mode) add('Stop', trainingStop);
  return [h, grid];
}

const VIZ_SCENES = {
  flow: (st, m) => flowScene(st, m),
  analysis: (st, m) => analysisScene(st, m),
  ...FULL_SCENES,
  // Spectrum: a mirrored equalizer across the screen, bass in the middle
  // jumping with the kick, highs at the edges snapping with the snare, peak
  // caps falling slowly, and a reflection below.
  bars(st, m) {
    const { g, f, w, h, cx, cy, size, pal, rgba, dt, ck, kick, snare, loud, lv, drive, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    const half = 24;
    if (!st.v) { st.v = new Float32Array(half); st.peak = new Float32Array(half); }
    const span = size * 0.56; // each side: the whole fits a phone's width
    const base = cy + size * 0.18;
    const bw = span / half;
    // A glow under them, brighter when loud.
    const glow = g.createRadialGradient(cx, base, 0, cx, base, size * 0.9);
    glow.addColorStop(0, rgba(pal[0], 0.25 * bright * (0.4 + 0.6 * lv)));
    glow.addColorStop(1, rgba(pal[0], 0));
    g.fillStyle = glow;
    g.fillRect(0, 0, w, h);
    for (let i = 0; i < half; i++) {
      const fq = i / (half - 1);
      const low = Math.exp(-fq * 3.5);
      const high = fq ** 1.4;
      const wander = 0.5 + 0.5 * Math.sin(ck * (2.1 + (i % 5) * 0.37) + i * 1.9);
      const target = lv * (low * kick * 1.1 + high * snare * 0.9 + (0.15 + 0.55 * loud) * (0.35 + 0.65 * wander) * (1 - 0.4 * fq)) * drive;
      st.v[i] = target > st.v[i] ? st.v[i] + (target - st.v[i]) * 0.6 : Math.max(target, st.v[i] - dt * 1.6);
      st.peak[i] = Math.max(st.peak[i] - dt * 0.45, st.v[i]);
    }
    g.globalCompositeOperation = 'lighter';
    for (const side of [-1, 1]) {
      for (let i = 0; i < half; i++) {
        const x = cx + side * (i + 0.5) * bw - bw * 0.36;
        const bh = Math.max(size * 0.012, st.v[i] * size * 0.62);
        const c = pal[i % 3];
        const grad = g.createLinearGradient(0, base - bh, 0, base);
        grad.addColorStop(0, rgba(c, 0.95 * (0.5 + 0.5 * bright)));
        grad.addColorStop(1, rgba(pal[(i + 1) % 3], 0.55 * (0.5 + 0.5 * bright)));
        g.fillStyle = grad;
        g.fillRect(x, base - bh, bw * 0.72, bh);
        // The reflection, faint and squashed.
        g.fillStyle = rgba(c, 0.16 * bright);
        g.fillRect(x, base + size * 0.02, bw * 0.72, bh * 0.35);
        // The peak cap.
        g.fillStyle = vizColor(VIZ_WHITE, 0.5 + 0.4 * bright);
        g.fillRect(x, base - st.peak[i] * size * 0.62 - size * 0.02, bw * 0.72, size * 0.01);
      }
    }
    g.globalCompositeOperation = 'source-over';
  },

  // Warp: a hyperspace tunnel - stars streaking past, faster as it gets
  // louder and on every kick, and turning rings of a tunnel flashing on the
  // first beat of a bar.
  warp(st, m) {
    const { g, f, w, h, cx, cy, size, dpr, pal, rgba, dt, ck, kick, loud, lv, e, downbeat, phase, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    if (!st.stars) {
      st.stars = Array.from({ length: Math.round(320 * VIZ_DENSITY) }, () => ({ x: Math.random() * 2 - 1, y: Math.random() * 2 - 1, z: Math.random(), c: Math.floor(Math.random() * 3) }));
      st.rings = Array.from({ length: 9 }, (_, i) => i / 9);
    }
    const speed = (0.08 + (0.35 + 1.4 * loud) * lv * (0.6 + 0.6 * e) + 1.2 * kick + 2 * m.dropEnv) * dt;
    const focal = size * 0.32;
    // A flash in the middle on the kick.
    const core = g.createRadialGradient(cx, cy, 0, cx, cy, size * (0.35 + 0.3 * kick));
    core.addColorStop(0, vizColor(VIZ_WHITE, 0.08 + 0.45 * kick));
    core.addColorStop(1, 'rgba(255, 255, 255, 0)');
    g.fillStyle = core;
    g.fillRect(0, 0, w, h);
    g.globalCompositeOperation = 'lighter';
    // The tunnel: hexagons coming towards you, turning.
    for (let i = 0; i < st.rings.length; i++) {
      st.rings[i] -= speed * 0.35;
      if (st.rings[i] <= 0.05) st.rings[i] += 1;
      const z = st.rings[i];
      const r = focal * 0.9 / z;
      const turn = ck * 0.5 + i * 0.35;
      g.strokeStyle = rgba(pal[i % 3], Math.min(1, (1 - z) * 1.2) * (0.25 + 0.5 * bright));
      g.lineWidth = (1 + 4 * m.dropEnv + 2 * (1 - z)) * dpr;
      g.beginPath();
      for (let k = 0; k <= 6; k++) {
        const a = turn + (k / 6) * Math.PI * 2;
        const x = cx + Math.cos(a) * r;
        const y = cy + Math.sin(a) * r;
        if (k) g.lineTo(x, y); else g.moveTo(x, y);
      }
      g.stroke();
    }
    // The stars: each a streak from where it was a moment ago.
    g.lineCap = 'round';
    for (const s of st.stars) {
      const z0 = s.z;
      s.z -= speed;
      if (s.z <= 0.02) { s.x = Math.random() * 2 - 1; s.y = Math.random() * 2 - 1; s.z = 1; continue; }
      const x0 = cx + (s.x / z0) * focal, y0 = cy + (s.y / z0) * focal;
      const x1 = cx + (s.x / s.z) * focal, y1 = cy + (s.y / s.z) * focal;
      g.strokeStyle = rgba(pal[s.c], Math.min(1, (1 - s.z) * 1.4) * (0.4 + 0.6 * bright));
      g.lineWidth = (0.6 + 2.2 * (1 - s.z)) * dpr;
      g.beginPath();
      g.moveTo(x0, y0);
      g.lineTo(x1, y1);
      g.stroke();
    }
    g.globalCompositeOperation = 'source-over';
  },

  // Waves: glowing ribbons across the screen, each the space between two
  // travelling waves, so it twists. Loudness raises them, a kick bulges them,
  // the snare ripples them.
  waves(st, m) {
    const { g, f, w, h, cx, cy, size, pal, rgba, ck, kick, snare, loud, lv, drive, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    const x0 = cx - size * 0.9;
    const x1 = cx + size * 0.9;
    const steps = 90;
    g.globalCompositeOperation = 'lighter';
    for (let r = 0; r < 5; r++) {
      const amp = size * (0.05 + (0.1 + 0.22 * loud) * lv * drive * 0.8 + 0.16 * kick) * (1 - r * 0.11);
      const k = 2.2 + r * 0.7;
      const sp = (0.8 + r * 0.35) * (r % 2 ? -1 : 1);
      const ripple = 0.25 * snare;
      const wave = (u, ph) => {
        const env = Math.sin(Math.PI * u);
        return cy + amp * env * (Math.sin(k * u * Math.PI * 2 + ck * sp * 2 + ph + r) + ripple * Math.sin(u * 40 + ck * 9 + r));
      };
      // The edges' heights go into buffers kept from frame to frame: arrays
      // of points built fresh each frame were garbage enough to make a phone
      // stop and collect it.
      if (!st.top) { st.top = new Float32Array(steps + 1); st.bottom = new Float32Array(steps + 1); }
      const top = st.top;
      const bottom = st.bottom;
      const lift = 0.9 + 0.3 * Math.sin(ck + r);
      for (let p = 0; p <= steps; p++) {
        const u = p / steps;
        top[p] = wave(u, 0);
        bottom[p] = wave(u, lift);
      }
      const xAt = (p) => x0 + (x1 - x0) * (p / steps);
      const c = pal[r % 3];
      const grad = g.createLinearGradient(x0, 0, x1, 0);
      grad.addColorStop(0, rgba(c, 0));
      grad.addColorStop(0.5, rgba(c, (0.28 + 0.12 * lv) * (0.4 + 0.6 * bright)));
      grad.addColorStop(1, rgba(c, 0));
      g.fillStyle = grad;
      g.beginPath();
      g.moveTo(xAt(0), top[0]);
      for (let p = 1; p <= steps; p++) g.lineTo(xAt(p), top[p]);
      for (let p = steps; p >= 0; p--) g.lineTo(xAt(p), bottom[p]);
      g.closePath();
      g.fill();
      // A bright edge along the top.
      g.strokeStyle = rgba(c, 0.55 * (0.4 + 0.6 * bright));
      g.lineWidth = 1.5 * m.dpr;
      g.beginPath();
      g.moveTo(xAt(0), top[0]);
      for (let p = 1; p <= steps; p++) g.lineTo(xAt(p), top[p]);
      g.stroke();
    }
    g.globalCompositeOperation = 'source-over';
  },

  // Kaleidoscope: shapes in one wedge, mirrored ten ways. They swell on the
  // kick, turn on the snare, and the whole thing snaps round on each bar and
  // spins faster when it is loud.
  kaleido(st, m) {
    const { g, f, w, h, cx, cy, size, pal, rgba, dt, kick, snare, loud, lv, downbeat, phase, bright } = m;
    f.clearRect(0, 0, w, h);
    g.clearRect(0, 0, w, h);
    if (!st.shapes) {
      st.shapes = Array.from({ length: 8 }, (_, i) => ({ r: 0.15 + (i / 8) * 0.75, a: Math.random(), s: 0.04 + Math.random() * 0.07, kind: i % 3, c: i % 3, sp: 0.3 + Math.random() }));
      st.spin = 0;
    }
    st.spin += dt * (0.1 + 0.9 * loud * lv) + (downbeat ? dt * 3 * (1 - phase) : 0);
    const seg = (Math.PI * 2) / 10;
    const R = size * 0.62;
    g.save();
    g.translate(cx, cy);
    g.globalCompositeOperation = 'lighter';
    for (let k = 0; k < 10; k++) {
      for (const mirror of [1, -1]) {
        g.save();
        g.rotate(k * seg + st.spin);
        g.scale(1, mirror);
        for (const sh of st.shapes) {
          const a = (0.5 + 0.5 * Math.sin(st.spin * sh.sp * 3 + sh.a * 6)) * seg * 0.5;
          const r = R * (sh.r + 0.08 * Math.sin(st.spin * 2 + sh.a * 9) + 0.1 * kick);
          const x = Math.cos(a) * r;
          const y = Math.sin(a) * r;
          const sz = R * sh.s * (1 + 1.2 * kick) * (0.6 + 0.6 * loud);
          g.fillStyle = rgba(pal[sh.c], (0.35 + 0.3 * lv) * (0.4 + 0.6 * bright));
          g.beginPath();
          if (sh.kind === 0) {
            g.arc(x, y, sz, 0, Math.PI * 2);
          } else {
            const turn = st.spin * 2 + snare * 2;
            const n = sh.kind === 1 ? 3 : 4;
            for (let p = 0; p <= n; p++) {
              const t = turn + (p / n) * Math.PI * 2;
              if (p) g.lineTo(x + Math.cos(t) * sz, y + Math.sin(t) * sz);
              else g.moveTo(x + Math.cos(t) * sz, y + Math.sin(t) * sz);
            }
          }
          g.fill();
        }
        g.restore();
      }
    }
    // A star in the middle.
    const core = g.createRadialGradient(0, 0, 0, 0, 0, R * (0.25 + 0.2 * kick));
    core.addColorStop(0, vizColor(VIZ_WHITE, 0.25 + 0.5 * kick));
    core.addColorStop(1, 'rgba(255, 255, 255, 0)');
    g.fillStyle = core;
    g.beginPath();
    g.arc(0, 0, R * 0.5, 0, Math.PI * 2);
    g.fill();
    g.restore();
    g.globalCompositeOperation = 'source-over';
  },

  // Fireworks: a burst on every beat, bigger on the first of a bar, crackle
  // on the snare, falling with gravity and leaving trails.
  fireworks(st, m) {
    const { g, f, w, h, cx, cy, size, dpr, pal, rgba, dt, newBeat, downbeat, snare, loud, lv, e, bright, playing } = m;
    g.clearRect(0, 0, w, h);
    f.globalCompositeOperation = 'destination-out';
    f.fillStyle = `rgba(0, 0, 0, ${playing ? 0.14 : 0.3})`;
    f.fillRect(0, 0, w, h);
    if (!st.sparks) { st.sparks = []; st.flashes = []; st.snare = 0; }
    const burst = (x, y, n, power, c) => {
      for (let i = 0; i < n; i++) {
        const a = Math.random() * Math.PI * 2;
        const v = size * power * (0.35 + Math.random() * 0.75);
        st.sparks.push({ x, y, px: x, py: y, vx: Math.cos(a) * v, vy: Math.sin(a) * v, life: 1, fade: 0.55 + Math.random() * 0.45, c });
      }
      st.flashes.push({ x, y, life: 1, r: size * power * 0.6, c });
    };
    if (m.firstDrop) {
      // The drop: a great burst in the middle, and two more beside it.
      burst(cx, cy - size * 0.1, 180, 1.5, 0);
      burst(cx - size * 0.45, cy, 70, 0.9, 1);
      burst(cx + size * 0.45, cy, 70, 0.9, 2);
    } else if (m.drop) {
      // Each loud bar after it: one great burst.
      burst(cx + (Math.random() - 0.5) * size * 0.5, cy - size * 0.15, 110, 1.2, Math.floor(Math.random() * 3));
    } else if (newBeat) {
      const a = Math.random() * Math.PI * 2;
      const d = Math.random() * size * 0.45;
      const n = Math.round((24 + 60 * loud) * (downbeat ? 1.8 : 1) * (0.6 + 0.6 * e));
      burst(cx + Math.cos(a) * d, cy - size * 0.1 + Math.sin(a) * d * 0.7, n, (downbeat ? 1.1 : 0.75) * (0.6 + 0.6 * loud), Math.floor(Math.random() * 3));
    }
    if (snare - st.snare > 0.35 && lv > 0.3) {
      const a = Math.random() * Math.PI * 2;
      burst(cx + Math.cos(a) * size * 0.5, cy + Math.sin(a) * size * 0.4, 14, 0.35, Math.floor(Math.random() * 3));
    }
    st.snare = snare;
    if (st.sparks.length > 650) st.sparks.splice(0, st.sparks.length - 650);
    // Glows where the bursts went off.
    g.globalCompositeOperation = 'lighter';
    st.flashes = st.flashes.filter((fl) => {
      fl.life -= dt * 2.2;
      if (fl.life <= 0) return false;
      const grad = g.createRadialGradient(fl.x, fl.y, 0, fl.x, fl.y, fl.r);
      grad.addColorStop(0, rgba(pal[fl.c], 0.45 * fl.life * bright));
      grad.addColorStop(1, rgba(pal[fl.c], 0));
      g.fillStyle = grad;
      g.fillRect(fl.x - fl.r, fl.y - fl.r, fl.r * 2, fl.r * 2);
      return true;
    });
    g.globalCompositeOperation = 'source-over';
    // The sparks, as streaks on the fading canvas.
    const gravity = size * 0.45;
    f.globalCompositeOperation = 'lighter';
    f.lineCap = 'round';
    st.sparks = st.sparks.filter((p) => {
      p.life -= dt * p.fade;
      if (p.life <= 0) return false;
      p.px = p.x; p.py = p.y;
      p.vx *= 1 - dt * 1.4;
      p.vy = p.vy * (1 - dt * 1.4) + gravity * dt;
      p.x += p.vx * dt;
      p.y += p.vy * dt;
      f.strokeStyle = rgba(pal[p.c], Math.min(1, p.life * 1.3) * (0.5 + 0.5 * bright));
      f.lineWidth = (0.8 + 1.8 * p.life) * dpr;
      f.beginPath();
      f.moveTo(p.px, p.py);
      f.lineTo(p.x, p.y);
      f.stroke();
      return true;
    });
    f.globalCompositeOperation = 'source-over';
  },
};
window.addEventListener('resize', () => {
  if (!$('now-playing').classList.contains('hidden')) renderCoverDeco();
});

/* ------------------------------------------------------------- television */

/* ------------------------- Now Playing as the remote, for music on a TV */

// While this phone controls a TV's music, the page's own audio element
// stands in for the TV, as it stands in for Android's native player in the
// app (PageScript): play, pause, seek and the next song go to the TV, and
// its clock and its playing or pausing come back from the TV's reports as
// the element's own events. So the whole of Now Playing - lyrics in time,
// the looks and visualizers on the beat (the server's analysis of the song),
// Up next, swiping songs, the hold buttons, the lock screen - is the remote,
// and the phone streams nothing. Music only: an audiobook's clock spans its
// files, and films and photos have the compact remote.
const raKey = (it) => (it ? `${it.sourceId}/${it.id}` : '');
const raBook = () => Boolean(audio.item && audio.item.kind === 'audiobook');
// Where the TV is on the whole item's timeline (a book's, across its files).
function raBookTime() {
  if (RA.seekTo !== null && Date.now() - RA.seekAt < 3000) return RA.seekTo;
  const st = RA.st;
  const age = Math.max(0, Math.min(5000, Date.now() - RA.stAt));
  const t = (st.position || 0) + (st.playing ? (age / 1000) * (Number(st.rate) || 1) : 0);
  return st.duration ? Math.min(t, st.duration) : t;
}
// A book's file playing here starts where it does in the book: the element
// stands for that one file, as it does when the book plays on the phone.
const raTrackStart = () => (raBook() && audio.tracks.length > 1 ? (audio.tracks[audio.index].startSeconds || 0) : 0);
function raTime() {
  return Math.max(0, raBookTime() - raTrackStart());
}
function raDuration() {
  if (raBook()) {
    if (audio.tracks.length > 1) return audio.tracks[audio.index].durationSeconds || NaN;
    return RA.st.duration || audio.duration || NaN;
  }
  return RA.st.duration ? RA.st.duration : NaN;
}
function raSend(cmd) {
  if (!RA.target) return;
  api(`/api/players/${RA.target.id}/command`, { method: 'POST', body: JSON.stringify(cmd) });
}
(function remoteLayer() {
  const el = $('audio-player');
  const proto = HTMLMediaElement.prototype;
  const prev = (name) => Object.getOwnPropertyDescriptor(el, name) || Object.getOwnPropertyDescriptor(proto, name)
    || Object.getOwnPropertyDescriptor(Element.prototype, name);
  const fire = (type) => el.dispatchEvent(new Event(type));
  RA.fire = fire;
  RA.real = {};
  const prop = (name, get, set) => {
    const p = prev(name);
    RA.real[name] = p;
    Object.defineProperty(el, name, {
      configurable: true,
      get() { return RA.on ? get() : p.get.call(el); },
      set(v) { if (RA.on) { if (set) set(v); } else if (p.set) p.set.call(el, v); },
    });
  };
  const method = (name, remote) => {
    const p = prev(name);
    const f = p.value;
    RA.real[name] = f;
    Object.defineProperty(el, name, {
      configurable: true, writable: true,
      value(...args) { return RA.on ? remote(...args) : f.apply(el, args); },
    });
  };
  const ranges = (end) => ({ length: end > 0 ? 1 : 0, start: () => 0, end: () => end });
  prop('src', () => RA.src, (v) => raLoad(String(v)));
  prop('currentSrc', () => RA.src);
  prop('currentTime', raTime, (v) => {
    if (!Number.isFinite(Number(v))) return;
    // A book is sent its place in the whole book, which is what the TV's
    // book player goes to - and only once the TV's place is known, or the
    // phone's own resume would pull a book playing on the TV back.
    if (raBook() && !RA.known) return;
    const t = Math.max(0, Number(v)) + raTrackStart();
    RA.seekTo = t;
    RA.seekAt = Date.now();
    raSend({ type: 'control', action: 'seek', value: t });
    fire('seeking');
    setTimeout(() => { fire('seeked'); fire('timeupdate'); }, 0);
  });
  prop('duration', raDuration);
  prop('paused', () => !RA.pwr);
  prop('ended', () => false);
  prop('readyState', () => 4);
  prop('networkState', () => 2);
  prop('buffered', () => ranges(raDuration() || 0));
  prop('seekable', () => ranges(raDuration() || 0));
  prop('error', () => null);
  // Leveling's volume is the TV's own business; kept here, not sent.
  prop('volume', () => RA.volume, (v) => { RA.volume = Number(v); fire('volumechange'); });
  // A book's speed is the TV's to play at: a change here is sent.
  prop('playbackRate', () => RA.rate, (v) => {
    const rate = Number(v) || 1;
    if (rate !== RA.rate && raBook()) raSend({ type: 'control', action: 'rate', value: rate });
    RA.rate = rate;
  });
  prop('defaultPlaybackRate', () => RA.rate, () => {});
  method('play', () => {
    RA.pwr = true;
    fire('play');
    if (!RA.st.playing) raSend({ type: 'control', action: 'play' });
    if (RA.st.playing) fire('playing');
    return Promise.resolve();
  });
  method('pause', () => {
    if (RA.pwr) {
      RA.pwr = false;
      raSend({ type: 'control', action: 'pause' });
      setTimeout(() => fire('pause'), 0);
    }
  });
  method('load', () => {});
  method('removeAttribute', (name) => {
    // Stopping here lets the TV go: this phone stops being its remote.
    if (String(name).toLowerCase() === 'src') exitMirror(false);
    else RA.real.removeAttribute.call(el, name);
  });
})();

// A song set on the element: the TV is told to play it, unless it already
// is (it moved on by itself, or the song was sent with Play on).
function raLoad(url) {
  RA.src = url;
  const item = audio.item;
  const key = raKey(item);
  if (key && key !== RA.key) {
    RA.key = key;
    if (raKey(RA.st.item) !== key) {
      const q = audio.queue;
      const cmd = { type: 'play', item };
      if (q && q.items.length > 1) {
        const at = q.index;
        cmd.queue = q.items.slice(Math.max(0, at - 200), at + 300);
        cmd.index = Math.min(at, 200);
      }
      raSend(cmd);
      RA.sentAt = Date.now();
      RA.st = { ...RA.st, position: 0, playing: false, item: { sourceId: item.sourceId, id: item.id } };
      RA.stAt = Date.now();
    }
  } else if (key && raBook() && RA.known && audio.tracks.length > 1) {
    // Another file of the book chosen here (a chapter, a skip across files):
    // the TV goes to where it starts, unless it is already in it. A place
    // inside it follows as the page sets the time.
    if (trackContaining(raBookTime()) !== audio.index) {
      const t = audio.tracks[audio.index].startSeconds || 0;
      RA.seekTo = t;
      RA.seekAt = Date.now();
      raSend({ type: 'control', action: 'seek', value: t });
    }
  }
  RA.fire('emptied');
  RA.fire('loadstart');
  setTimeout(() => { RA.fire('durationchange'); RA.fire('loadedmetadata'); RA.fire('canplay'); }, 0);
}

async function raPoll() {
  if (!RA.on || !RA.target) return;
  const target = RA.target;
  const { ok, status, body } = await api(`/api/players/${target.id}`);
  if (!RA.on || RA.target !== target) return;
  if (!ok) {
    if (status === 404) {
      exitMirror(false);
      showToast(`${target.name} is not yours to control now.`);
    }
    return;
  }
  const st = body.state || {};
  const settled = Date.now() - (RA.sentAt || 0) > 5000;
  if (st.kind !== 'audio') {
    // The TV went on to a film or a photo, or stopped: this page lets go of
    // the music - quietly, the TV is not told anything.
    if (settled) dropMirror();
    return;
  }
  const was = RA.st;
  RA.st = st;
  RA.stAt = Date.now() - Math.max(0, Math.min(5000, st.at ? Date.now() - st.at : 0));
  if (RA.seekTo !== null && Date.now() - RA.seekAt > 2500) RA.seekTo = null;
  if (raKey(st.item) === RA.key) RA.known = true;
  // A book the TV has played on into its next file (or was sent elsewhere
  // in): this page follows, without telling the TV anything.
  if (raBook() && raKey(st.item) === RA.key && audio.tracks.length > 1 && RA.seekTo === null) {
    const at = trackContaining(raBookTime());
    if (at !== audio.index) {
      audio.index = at;
      RA.src = audio.tracks[at].url;
      renderTracks();
      updateMediaSession();
      RA.fire('durationchange');
    }
  }
  const tvKey = raKey(st.item);
  // The TV moved on to the next song by itself: here too.
  if (tvKey && tvKey !== RA.key) {
    const q = audio.queue;
    const next = q && q.items[q.index + 1];
    if (next && raKey(next) === tvKey) {
      RA.key = tvKey;
      RA.fire('ended');
      return;
    }
    // Something this page did not send - the TV's own remote, or a queue it
    // had before this phone picked it up: shown here as it is.
    if (settled && st.item && st.item.id) {
      raAdopt(st);
      return;
    }
  }
  if (st.duration !== was.duration) RA.fire('durationchange');
  if (st.playing && !RA.lastPlaying) {
    if (!RA.pwr) { RA.pwr = true; RA.fire('play'); }
    RA.fire('playing');
  } else if (!st.playing && RA.lastPlaying && RA.pwr) {
    // Paused on the TV itself, or by its remote.
    RA.pwr = false;
    RA.fire('pause');
  }
  RA.lastPlaying = Boolean(st.playing);
}

// Into the mirror: the TV plays, this phone's Now Playing is its remote.
function enterMirror(target, cmd) {
  const el = $('audio-player');
  // This phone's own player stops - it is the TV that plays now.
  if (!RA.on) {
    RA.real.pause.call(el);
    RA.real.removeAttribute.call(el, 'src');
    RA.real.load.call(el);
  }
  RA.on = true;
  RA.target = target;
  RA.key = raKey(cmd.item);
  const moved = raKey(audio.item) === RA.key;
  RA.st = { playing: true, position: Number(cmd.at) || 0, item: { sourceId: cmd.item.sourceId, id: cmd.item.id } };
  // A book moved from here carries on from where it was here; one sent from
  // its card waits to hear where the TV picked it up.
  if (moved && raBook()) RA.st.position = elapsed();
  RA.known = moved || cmd.item.kind !== 'audiobook';
  const realRate = RA.real.playbackRate && RA.real.playbackRate.get ? RA.real.playbackRate.get.call(el) : 1;
  RA.rate = moved && raBook() ? realRate || 1 : 1;
  RA.stAt = Date.now();
  RA.pwr = true;
  RA.sentAt = Date.now();
  RA.lastPlaying = false;
  clearInterval(RA.poll);
  clearInterval(RA.ticker);
  RA.poll = setInterval(raPoll, 1000);
  RA.ticker = setInterval(() => { if (RA.on && RA.pwr) RA.fire('timeupdate'); }, 250);
  if (moved) {
    // Moved from here: the song and its queue are already this page's.
    RA.src = 'remote:' + RA.key;
    RA.fire('playing');
  } else if (Array.isArray(cmd.queue) && cmd.queue.length) {
    playQueue(cmd.queue, Math.max(0, Math.min(cmd.queue.length - 1, Number(cmd.index) || 0)));
  } else {
    play(cmd.item);
  }
  renderWhere();
  raPoll();
}

// Out of it: this phone stops being the TV's remote (stop sends the TV
// stop too). Play here plays the song on from the TV's moment.
function exitMirror(stopTV) {
  if (!RA.on) return;
  if (stopTV) raSend({ type: 'stop' });
  RA.on = false;
  clearInterval(RA.poll);
  clearInterval(RA.ticker);
  RA.target = null;
  RA.key = '';
  RA.pwr = false;
  renderWhere();
}
function renderWhere() {
  const pill = $('np-where');
  show(pill, RA.on);
  if (RA.on) pill.textContent = `On ${RA.target.name}`;
  tellRemoteVolume();
}

// The phone's volume buttons turn the TV's volume while this phone controls
// it - in the Android app, which hears the buttons (a browser cannot).
function remoteVolumeTarget() {
  if (RA.on && RA.target) return { target: RA.target, st: RA.st };
  if (PLAYER.target) return { target: PLAYER.target, st: PLAYER.targetState || {} };
  if (CONTROL.target) return { target: CONTROL.target, st: CONTROL.st || {} };
  return null;
}
let remoteVolumeOn = false;
function tellRemoteVolume() {
  const on = Boolean(remoteVolumeTarget());
  if (on === remoteVolumeOn || !window.soundstormApp || !window.soundstormApp.remoteVolume) return;
  remoteVolumeOn = on;
  window.soundstormApp.remoteVolume(on);
}
const remoteVolumeKey = { value: null, at: 0 };
window.__soundstormVolumeKey = (dir) => {
  const t = remoteVolumeTarget();
  if (!t) return false;
  // Quick presses build on each other, not on a report a second old.
  const reported = typeof t.st.volume === 'number' ? t.st.volume : 1;
  const from = remoteVolumeKey.value !== null && Date.now() - remoteVolumeKey.at < 3000 ? remoteVolumeKey.value : reported;
  const value = Math.round(Math.max(0, Math.min(1, from + (dir > 0 ? 0.05 : -0.05))) * 100) / 100;
  remoteVolumeKey.value = value;
  remoteVolumeKey.at = Date.now();
  api(`/api/players/${t.target.id}/command`, { method: 'POST', body: JSON.stringify({ type: 'volume', value }) });
  if (t.st === RA.st) RA.st = { ...RA.st, volume: value };
  else if (PLAYER.targetState) { PLAYER.targetState = { ...PLAYER.targetState, volume: value }; holdRemote({ volume: value }); }
  if (shown('rc')) $('rc-volume').value = String(Math.round(value * 100));
  showVolumeBadge(t.target.name, value);
  return true;
};
let volumeBadgeTimer = 0;
function showVolumeBadge(name, value) {
  let badge = $('tv-volume-badge');
  if (!badge) {
    badge = document.createElement('div');
    badge.id = 'tv-volume-badge';
    badge.className = 'tv-volume-badge';
    badge.setAttribute('role', 'status');
    const label = document.createElement('span');
    const bar = document.createElement('div');
    bar.className = 'tv-volume-bar';
    bar.append(document.createElement('i'));
    badge.append(label, bar);
    document.body.append(badge);
  }
  badge.firstChild.textContent = `${name} volume ${Math.round(value * 100)}%`;
  badge.querySelector('i').style.width = `${Math.round(value * 100)}%`;
  badge.classList.add('on');
  clearTimeout(volumeBadgeTimer);
  volumeBadgeTimer = setTimeout(() => badge.classList.remove('on'), 1500);
}
// The label: the compact remote for the volume, Play here and Stop.
$('np-where').addEventListener('click', (event) => {
  event.stopPropagation();
  openControlSheet();
});

// Picked up what the TV plays (a song the TV moved to by itself, or what
// it was playing when this phone chose it): this page shows it, as a queue
// of its own the TV holds, and sends nothing.
function raAdopt(st) {
  const item = { ...st.item };
  RA.key = raKey(item);
  RA.st = st;
  RA.stAt = Date.now();
  RA.known = item.kind !== 'audiobook';
  audio.queue = null;
  audio.radio = null;
  playAudio(item, true);
  renderNowPlaying();
}

// Letting go of the TV's music on this page: the phone's player cleared,
// with no place saved (it would be this page's, not the TV's) and nothing
// sent to the TV, which plays on.
function dropMirror() {
  if (!RA.on) return;
  exitMirror(false);
  audio.resumable = false;
  stopAudio();
}

// Into the mirror without a song to send yet: what is played next goes to
// the TV (playAudio, controlling a TV).
function controlAttach(target) {
  const el = $('audio-player');
  RA.real.pause.call(el);
  RA.real.removeAttribute.call(el, 'src');
  RA.real.load.call(el);
  RA.on = true;
  RA.target = target;
  RA.key = '';
  RA.st = {};
  RA.stAt = Date.now();
  RA.known = false;
  RA.rate = 1;
  RA.pwr = true;
  RA.sentAt = Date.now();
  RA.lastPlaying = false;
  clearInterval(RA.poll);
  clearInterval(RA.ticker);
  RA.poll = setInterval(raPoll, 1000);
  RA.ticker = setInterval(() => { if (RA.on && RA.pwr) RA.fire('timeupdate'); }, 250);
  renderWhere();
}

/* --------------------------------- playing on another device, remotely */

// Every open SoundStorm page is a player (players.go on the server): it says
// hello, reports what it is playing, and waits for commands - a phone's Play
// on, pause, skip. A TV is shared, so anybody in the house may send
// something to it, which switches it to them (asking first when somebody
// else is using it); everything else only its own person's phones control.
const PLAYER = { id: '', polling: false, lastState: '', stateAt: 0, target: null, remoteTimer: 0 };
try {
  PLAYER.id = localStorage.getItem('soundstorm-player-id') || '';
  if (!/^[a-f0-9]{32}$/.test(PLAYER.id)) {
    PLAYER.id = [...crypto.getRandomValues(new Uint8Array(16))].map((b) => b.toString(16).padStart(2, '0')).join('');
    localStorage.setItem('soundstorm-player-id', PLAYER.id);
  }
} catch { /* no storage: not a player */ }

function deviceName() {
  try { return localStorage.getItem('soundstorm.deviceName') || ''; } catch { return ''; }
}
$('device-name').value = deviceName();
$('device-name').addEventListener('change', () => {
  try { localStorage.setItem('soundstorm.deviceName', $('device-name').value.trim()); } catch { /* full */ }
  playerHello();
});

function playerHello() {
  if (!PLAYER.id || !state.me) return Promise.resolve();
  return api('/api/players/hello', {
    method: 'POST', body: JSON.stringify({ id: PLAYER.id, name: deviceName() || (TV ? 'TV' : ''), tv: TV }),
  });
}

const pause = (ms) => new Promise((r) => setTimeout(r, ms));
async function playerLoop() {
  if (PLAYER.polling || !PLAYER.id) return;
  PLAYER.polling = true;
  try {
    await playerHello();
    if (!PLAYER.restored) { PLAYER.restored = true; restoreControl(); resumeAppUploads(); }
    // A phone in a pocket is not worth a connection; a TV always is.
    while (state.me && !state.offline && !PLAYER.leaving && (TV || !document.hidden)) {
      const r = await api(`/api/players/${PLAYER.id}/next`);
      if (!r.ok) {
        if (r.status === 404) await playerHello();
        await pause(r.offline ? 8000 : 2000);
        continue;
      }
      for (const c of (r.body && r.body.commands) || []) {
        try { await runPlayerCommand(c); } catch { /* one bad command, not the loop */ }
        if (PLAYER.leaving) return;
      }
      // What it did, straight away, so the phone's remote is not left
      // showing the old state (and flipping back) until the next report.
      setTimeout(() => reportPlayerState(true), 300);
    }
  } finally {
    PLAYER.polling = false;
  }
}
document.addEventListener('visibilitychange', () => { if (!document.hidden) playerLoop(); });

// What this device is playing, for the phones controlling it.
function playerState() {
  const a = $('audio-player');
  const v = $('video-player');
  const card = (it) => it && ({
    sourceId: it.sourceId, id: it.id, title: it.title, subtitle: subtitleFor(it), kind: it.kind,
    artId: it.artId, durationSeconds: it.durationSeconds,
    extra: it.extra && it.extra.type ? { type: it.extra.type } : undefined,
  });
  if (shown('video-overlay') && state.watching) {
    return {
      playing: !v.paused, kind: 'video', item: card(state.watching.item),
      position: v.currentTime || 0, duration: Number.isFinite(v.duration) ? v.duration : 0, volume: v.volume,
    };
  }
  if (audio.item) {
    const book = audio.item.kind === 'audiobook';
    return {
      playing: !a.paused, kind: 'audio', item: card(audio.item),
      position: book ? elapsed() : a.currentTime || 0,
      duration: book ? audio.duration || 0 : (Number.isFinite(a.duration) ? a.duration : 0),
      volume: audio.userVolume, rate: a.playbackRate || 1,
      queue: audio.queue ? { index: audio.queue.index, length: audio.queue.items.length } : null,
    };
  }
  if (photoShown) return { playing: false, kind: 'photo', item: card(photoShown) };
  return { playing: false, kind: null };
}
function reportPlayerState(force) {
  if (!PLAYER.polling || !state.me) return;
  const st = playerState();
  const sig = JSON.stringify({ ...st, position: Math.round((st.position || 0) / 5) });
  // On a change, and every half minute regardless: the phone's timeline
  // runs from the last report.
  if (!force && sig === PLAYER.lastState && Date.now() - PLAYER.stateAt < 30000) return;
  PLAYER.lastState = sig;
  PLAYER.stateAt = Date.now();
  api(`/api/players/${PLAYER.id}/state`, { method: 'POST', body: JSON.stringify({ ...st, at: Date.now() }) });
}
setInterval(() => reportPlayerState(false), 2000);

async function runPlayerCommand(c) {
  switch (c.type) {
    case 'switch': {
      // Taken over from somebody's phone: signed in as them, then made again.
      const r = await api('/api/players/switch', { method: 'POST', body: JSON.stringify({ code: c.code, id: PLAYER.id }) });
      if (r.ok) {
        // Nothing more is taken by this page: what was sent waits for the
        // page made again as the new person (it once took it and vanished).
        PLAYER.leaving = true;
        try { sessionStorage.setItem('soundstorm-switched', '1'); } catch { /* none */ }
        location.reload();
      }
      return;
    }
    case 'ask':
      askPlayerTakeOver(c);
      return;
    case 'claim':
      // A phone chose this device to control: nothing to do but be ready.
      return;
    case 'play':
      remotePlay(c);
      return;
    case 'control':
      remoteControl(c);
      return;
    case 'volume': {
      const v = Math.max(0, Math.min(1, Number(c.value)));
      if (shown('video-overlay')) $('video-player').volume = v;
      audio.userVolume = v;
      if (audio.item) applyLevel(audio.item);
      return;
    }
    case 'stop':
      closeVideo();
      if (audio.item) { savePosition(); stopAudio(); }
      if (photoShown) closePhoto();
      return;
  }
}

function remotePlay(c) {
  const item = c.item;
  if (!item || !item.sourceId || !item.id) return;
  if (c.from && state.me && c.from !== state.me.name) showToast(`Playing from ${c.from}'s phone`);
  else if (c.from) showToast('Playing from your phone');
  if (item.kind === 'music' && Array.isArray(c.queue) && c.queue.length) {
    const at = Math.max(0, Math.min(c.queue.length - 1, Number(c.index) || 0));
    playQueue(c.queue, at);
  } else if (item.kind === 'picture' && !(item.extra && item.extra.type === 'video')) {
    state.items = [item];
    showPhoto(item);
    return;
  } else {
    play(item);
  }
  // Moved from a phone mid-song: on from the same moment.
  const from = Number(c.at) || 0;
  if (from > 1 && item.kind === 'music') {
    const a = $('audio-player');
    a.addEventListener('loadedmetadata', () => { a.currentTime = from; }, { once: true });
  }
}

function remoteControl(c) {
  const v = $('video-player');
  const a = $('audio-player');
  const value = Number(c.value) || 0;
  if (c.action === 'closephoto') {
    if (photoShown) closePhoto();
    return;
  }
  if (shown('video-overlay')) {
    if (c.action === 'toggle') { if (v.paused) v.play().catch(() => {}); else v.pause(); }
    else if (c.action === 'pause') v.pause();
    else if (c.action === 'play') v.play().catch(() => {});
    else if (c.action === 'seek') v.currentTime = value;
    else if (c.action === 'skip') v.currentTime = Math.max(0, v.currentTime + value);
    return;
  }
  if (!audio.item) return;
  const book = audio.item.kind === 'audiobook';
  if (c.action === 'toggle') { if (a.paused) a.play().catch(() => {}); else a.pause(); }
  else if (c.action === 'pause') a.pause();
  else if (c.action === 'play') a.play().catch(() => {});
  else if (c.action === 'next') mediaNext();
  else if (c.action === 'prev') mediaPrevious();
  else if (c.action === 'seek') { if (book) goToBook(value); else a.currentTime = value; }
  else if (c.action === 'skip') { if (book) bookSkip(value); else a.currentTime = Math.max(0, a.currentTime + value); }
  else if (c.action === 'rate' && book && value >= 0.5 && value <= 3) {
    a.playbackRate = value;
    a.defaultPlaybackRate = value;
  }
}

// On a TV in use, somebody else asks to play: OK, No, or nothing (which
// the server takes for OK after fifteen seconds).
let playerAsking = null;
function askPlayerTakeOver(c) {
  playerAsking = c.ask;
  $('player-ask-title').textContent = `${c.from} wants to play something`;
  $('player-ask-text').textContent = `Let ${c.from} take over this TV? What is playing now is saved, so it can carry on later.`;
  show($('player-ask'), true);
  $('player-ask-yes').focus();
  setTimeout(() => { if (playerAsking === c.ask) { show($('player-ask'), false); playerAsking = null; } }, 15000);
}
async function answerPlayerAsk(allow) {
  const ask = playerAsking;
  playerAsking = null;
  show($('player-ask'), false);
  if (ask) await api(`/api/players/${PLAYER.id}/ask/${encodeURIComponent(ask)}`, { method: 'POST', body: JSON.stringify({ allow }) });
}
$('player-ask-yes').addEventListener('click', () => answerPlayerAsk(true));
$('player-ask-no').addEventListener('click', () => answerPlayerAsk(false));

// --- the phone's side: Play on, and the remote ---------------------------

// --- Which device this phone plays on --------------------------------------
//
// The owner's design (2026-10-02): one choice, always in reach - this phone,
// or a TV (or any other open device of this person's). With a TV chosen,
// everything played goes there and this phone is its remote: Now Playing for
// music and books, the remote sheet for a film, and a photo shows on both.
// Browsing stays on the phone. Choosing the phone again leaves the TV
// playing, and a chip says so, a tap choosing it again.
const CONTROL_KEY = 'soundstorm-control';

function renderControl() {
  const btn = $('ctl-btn');
  show(btn, !TV && !state.offline && Boolean(state.me));
  btn.classList.toggle('on', Boolean(CONTROL.target));
  const label = document.createElement('span');
  label.textContent = CONTROL.target ? CONTROL.target.name : '';
  btn.replaceChildren(icon('cast'), label);
  btn.title = CONTROL.target ? `Playing on ${CONTROL.target.name}` : 'Playing on this device';
  renderControlChip();
  tellRemoteVolume();
}

// What the device chosen (or the one left playing) is doing, every few
// seconds: picked up when it plays music or a book, shown on the chip.
async function watchControl() {
  const t = CONTROL.target || CONTROL.away;
  if (!t || !state.me || document.hidden) return;
  const { ok, status, body } = await api(`/api/players/${t.id}`);
  if (t !== (CONTROL.target || CONTROL.away)) return;
  if (!ok) {
    if (status === 404) {
      if (CONTROL.target === t) {
        showToast(`${t.name} is not open, or is not yours to control now.`);
        setControl(null);
        if (RA.on) dropMirror();
      } else {
        CONTROL.away = null;
      }
      renderControl();
    }
    return;
  }
  CONTROL.st = body.state || {};
  const st = CONTROL.st;
  if (CONTROL.target === t && !RA.on && st.kind === 'audio' && st.item && st.playing) {
    controlAttach(t);
    raAdopt(st);
  }
  renderControlChip();
}

function renderControlChip() {
  const chip = $('rc-chip');
  const st = CONTROL.st || {};
  let text = '';
  if (CONTROL.target && !RA.on && !shown('rc') && (st.kind === 'video' || st.kind === 'photo')) text = `On ${CONTROL.target.name}`;
  else if (!CONTROL.target && CONTROL.away && st.kind) text = `Playing on ${CONTROL.away.name}`;
  chip.textContent = text;
  show(chip, Boolean(text));
}

function setControl(target) {
  CONTROL.target = target;
  try {
    if (target) localStorage.setItem(CONTROL_KEY, JSON.stringify(target));
    else localStorage.removeItem(CONTROL_KEY);
  } catch { /* storage full */ }
  clearInterval(CONTROL.timer);
  if (target || CONTROL.away) CONTROL.timer = setInterval(watchControl, 3000);
  renderControl();
}

function controlSend(cmd) {
  const t = CONTROL.target;
  if (!t) return Promise.resolve(null);
  return api(`/api/players/${t.id}/command`, { method: 'POST', body: JSON.stringify(cmd) }).then((r) => {
    if (r.status === 404 || r.status === 403) {
      showToast(`${t.name} is not open, or is not yours to control now.`);
      setControl(null);
    }
    return r;
  });
}

// Taking a device: a TV somebody else is using asks them first (players.go).
async function claimDevice(p, note) {
  note.textContent = `Connecting to ${p.name}\u2026`;
  let r = await api(`/api/players/${p.id}/command`, { method: 'POST', body: JSON.stringify({ type: 'claim' }) });
  if (r.status === 202 && r.body && r.body.asking) {
    note.textContent = `Asking ${p.person || 'whoever is watching'} on ${p.name}\u2026`;
    const ask = r.body.asking;
    for (let i = 0; i < 30; i++) {
      await pause(1000);
      r = await api(`/api/players/${p.id}/ask/${encodeURIComponent(ask)}`);
      if (!r.ok || !r.body || !r.body.waiting) break;
    }
    if (r.ok && r.body && r.body.denied) {
      note.textContent = `${p.person || 'They'} said no. You can ask again in ${Math.round(r.body.retryIn / 60)} minutes.`;
      return false;
    }
    if (!(r.ok && r.body && r.body.allowed)) {
      note.textContent = (r.body && r.body.error) || 'That did not go through.';
      return false;
    }
  } else if (r.status === 429 && r.body && r.body.retryIn) {
    note.textContent = `${p.person || 'They'} said no. You can ask again in ${Math.max(1, Math.round(r.body.retryIn / 60))} minute${r.body.retryIn > 90 ? 's' : ''}.`;
    return false;
  } else if (!r.ok) {
    note.textContent = (r.body && r.body.error) || 'That did not go through.';
    return false;
  }
  return true;
}

// Choosing a device (null: this phone).
async function chooseDevice(p) {
  const note = $('ctl-note');
  if (!p) {
    const was = CONTROL.target;
    if (RA.on) dropMirror();
    forgetRemote();
    CONTROL.away = was;
    CONTROL.st = null;
    setControl(null);
    closeControlSheet();
    if (was) watchControl();
    return;
  }
  if (CONTROL.target && CONTROL.target.id === p.id) { closeControlSheet(); return; }
  if (RA.on) dropMirror();
  forgetRemote();
  if (!(await claimDevice(p, note))) return;
  const target = { id: p.id, name: p.name, tv: Boolean(p.tv) };
  CONTROL.away = null;
  CONTROL.st = null;
  setControl(target);
  closeControlSheet();
  // What plays here moves there, from the same moment.
  if (shown('video-overlay') && state.watching) {
    const item = state.watching.item;
    saveWatchPosition(true);
    closeVideo();
    controlSend({ type: 'play', item });
    openRemote(target);
    return;
  }
  // Paused here: left here, not started on the TV unasked.
  if (audio.item && $('audio-player').paused) stopAudio();
  if (audio.item && (audio.item.kind === 'music' || audio.item.kind === 'audiobook')) {
    const item = audio.item;
    const cmd = { type: 'play', item };
    const q = audio.queue;
    if (item.kind === 'music' && q && q.items.length > 1) {
      cmd.queue = q.items.slice(Math.max(0, q.index - 200), q.index + 300);
      cmd.index = Math.min(q.index, 200);
    }
    if (item.kind === 'music') cmd.at = $('audio-player').currentTime || 0;
    // A book's place is kept on the server: saved here, the TV carries on.
    else savePosition();
    await controlSend(cmd);
    enterMirror(target, cmd);
    return;
  }
  // Nothing playing here: whatever the TV plays shows here.
  watchControl();
}

async function openControlSheet() {
  const list = $('ctl-list');
  const note = $('ctl-note');
  note.textContent = 'Looking for devices\u2026';
  show($('ctl-backdrop'), true);
  show($('ctl-sheet'), true);
  const here = { id: '', name: touchScreen() ? 'This phone' : 'This computer' };
  const row = (p, icn, detail) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'ctl-row';
    const current = p.id ? Boolean(CONTROL.target && CONTROL.target.id === p.id) : !CONTROL.target;
    b.classList.toggle('on', current);
    const name = document.createElement('span');
    name.className = 'ctl-name';
    name.textContent = p.name;
    const sub = document.createElement('span');
    sub.className = 'ctl-detail muted';
    sub.textContent = detail || '';
    const text = document.createElement('span');
    text.className = 'ctl-text';
    text.append(name, sub);
    b.append(icon(icn), text);
    if (current) b.append(icon('check'));
    b.addEventListener('click', () => chooseDevice(p.id ? p : null));
    return b;
  };
  list.replaceChildren(row(here, 'device', ''));
  renderControlNow();
  const { ok, body } = await api(`/api/players?self=${encodeURIComponent(PLAYER.id)}`);
  const players = (ok && body && body.players) || [];
  list.replaceChildren(row(here, 'device', ''), ...players.map((p) => {
    const detail = p.mine ? (p.state && p.state.playing ? 'Playing' : '') : (p.busy ? `${p.person} is using it` : (p.person ? `${p.person}'s` : ''));
    return row(p, p.tv ? 'film' : 'headphones', detail);
  }));
  note.textContent = players.length ? 'What you play goes to the device chosen. Browsing stays on this phone.'
    : 'No other device is open. Open SoundStorm on the TV, or another computer, and it shows here.';
}
function closeControlSheet() {
  show($('ctl-sheet'), false);
  show($('ctl-backdrop'), false);
}
function renderControlNow() {
  show($('ctl-now'), Boolean(CONTROL.target));
  if (!CONTROL.target) return;
  $('ctl-stop-name').textContent = CONTROL.target.name;
  const st = (RA.on ? RA.st : CONTROL.st) || {};
  if (typeof st.volume === 'number') $('ctl-volume').value = String(Math.round(st.volume * 100));
}
$('ctl-btn').addEventListener('click', openControlSheet);
$('ctl-backdrop').addEventListener('click', closeControlSheet);
let ctlVolumeTimer = 0;
$('ctl-volume').addEventListener('input', () => {
  if (ctlVolumeTimer) return;
  ctlVolumeTimer = setTimeout(() => {
    ctlVolumeTimer = 0;
    const value = Number($('ctl-volume').value) / 100;
    if (RA.on) RA.st = { ...RA.st, volume: value };
    controlSend({ type: 'volume', value });
  }, 200);
});
$('ctl-stop').addEventListener('click', () => {
  controlSend({ type: 'stop' });
  forgetRemote();
  if (RA.on) dropMirror();
  closeControlSheet();
});
// The device chosen last time, if it is still open and still this
// person's to control.
async function restoreControl() {
  let saved = null;
  try { saved = JSON.parse(localStorage.getItem(CONTROL_KEY) || 'null'); } catch { /* none */ }
  if (TV || !saved || !saved.id) { renderControl(); return; }
  const { ok, body } = await api(`/api/players?self=${encodeURIComponent(PLAYER.id)}`);
  const p = ok && body && (body.players || []).find((x) => x.id === saved.id);
  if (p && (p.mine || !p.busy)) {
    setControl({ id: p.id, name: p.name, tv: Boolean(p.tv) });
    watchControl();
  } else {
    setControl(null);
  }
}

// The remote: what the device is playing, and its buttons.
function openRemote(target) {
  PLAYER.target = target;
  setTimeout(tellRemoteVolume, 0);
  $('rc-device').textContent = `Playing on ${target.name}`;
  show($('rc'), true);
  show($('rc-chip'), false);
  $('rc-note').textContent = '';
  refreshRemote();
  clearInterval(PLAYER.remoteTimer);
  PLAYER.remoteTimer = setInterval(refreshRemote, 1500);
}
function closeRemote() {
  show($('rc'), false);
  clearInterval(PLAYER.remoteTimer);
  PLAYER.target = null;
  setTimeout(tellRemoteVolume, 0);
  renderControlChip();
}
function forgetRemote() {
  PLAYER.target = null;
  setTimeout(tellRemoteVolume, 0);
  clearInterval(PLAYER.remoteTimer);
  show($('rc'), false);
  show($('rc-chip'), false);
}
let remoteSeeking = false;
// What a tap just did, shown at once and kept for a few seconds against the
// reports still on their way (they flipped the button back - "finicky").
const remoteHeld = { playing: null, position: null, volume: null, until: 0 };
function holdRemote(what) {
  Object.assign(remoteHeld, what, { until: Date.now() + 3000 });
}
const clock = (s) => {
  s = Math.max(0, Math.floor(s || 0));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
};
async function refreshRemote() {
  const target = PLAYER.target;
  if (!target) return;
  const { ok, status, body } = await api(`/api/players/${target.id}`);
  if (PLAYER.target !== target) return;
  if (!ok) {
    // Taken over by somebody else, or closed: the remote goes.
    if (status === 404) {
      forgetRemote();
      showToast(`${target.name} is not yours to control now.`);
    }
    return;
  }
  const st = { ...(body.state || {}) };
  if (Date.now() < remoteHeld.until) {
    if (remoteHeld.playing !== null) st.playing = remoteHeld.playing;
    if (remoteHeld.position !== null) { st.position = remoteHeld.position; st.at = remoteHeld.at; }
    if (remoteHeld.volume !== null) st.volume = remoteHeld.volume;
  } else {
    Object.assign(remoteHeld, { playing: null, position: null, volume: null });
  }
  const item = st.item;
  $('rc-title').textContent = item ? item.title : 'Nothing playing';
  $('rc-sub').textContent = item ? item.subtitle || '' : '';
  const art = $('rc-art');
  if (item && item.artId) art.src = `/api/art/${encodeURIComponent(item.sourceId)}/${escapeId(item.artId)}`;
  else art.src = NO_COVER;
  // The time runs on from the report, as the device's own clock does.
  const position = (st.position || 0) + (st.playing && st.at ? (Date.now() - st.at) / 1000 : 0);
  if (!remoteSeeking) {
    $('rc-seek').max = String(Math.max(1, Math.round(st.duration || 0)));
    $('rc-seek').value = String(Math.round(position));
  }
  $('rc-at').textContent = clock(position);
  $('rc-len').textContent = st.duration ? clock(st.duration) : '';
  $('rc-toggle').replaceChildren(icon(st.playing ? 'pause' : 'play'));
  if (typeof st.volume === 'number' && !remoteVolumeTouched()) $('rc-volume').value = String(Math.round(st.volume * 100));
  const audioish = st.kind === 'audio';
  show($('rc-prev'), audioish && !(item && item.kind === 'audiobook'));
  show($('rc-next'), audioish && !(item && item.kind === 'audiobook'));
  PLAYER.targetState = st;
}
function remoteSend(cmd) {
  if (!PLAYER.target) return;
  $('rc-note').textContent = '';
  api(`/api/players/${PLAYER.target.id}/command`, { method: 'POST', body: JSON.stringify(cmd) }).then((r) => {
    if (!r.ok) $('rc-note').textContent = (r.body && r.body.error) || 'That did not go through.';
    setTimeout(refreshRemote, 900);
  });
}
// The position the remote shows, run on from the last report.
function remotePosition() {
  const st = PLAYER.targetState || {};
  return (st.position || 0) + (st.playing && st.at ? (Date.now() - st.at) / 1000 : 0);
}
let volumeTouchedAt = 0;
const remoteVolumeTouched = () => Date.now() - volumeTouchedAt < 2500;
$('rc-prev').replaceChildren(icon('prev'));
$('rc-next').replaceChildren(icon('skip'));
$('rc-close').replaceChildren(icon('down'));
$('rc-close').addEventListener('click', closeRemote);
$('rc-chip').addEventListener('click', () => {
  if (CONTROL.target) openRemote(CONTROL.target);
  else if (CONTROL.away) chooseDevice(CONTROL.away);
});
$('rc-toggle').addEventListener('click', () => {
  const st = PLAYER.targetState || {};
  const playing = !st.playing;
  holdRemote({ playing, position: remotePosition(), at: Date.now() });
  $('rc-toggle').replaceChildren(icon(playing ? 'pause' : 'play'));
  PLAYER.targetState = { ...st, playing, position: remotePosition(), at: Date.now() };
  remoteSend({ type: 'control', action: playing ? 'play' : 'pause' });
});
$('rc-prev').addEventListener('click', () => remoteSend({ type: 'control', action: 'prev' }));
$('rc-next').addEventListener('click', () => remoteSend({ type: 'control', action: 'next' }));
for (const [id, by] of [['rc-back10', -10], ['rc-fwd10', 10]]) {
  $(id).addEventListener('click', () => {
    const to = Math.max(0, remotePosition() + by);
    holdRemote({ position: to, at: Date.now() });
    $('rc-seek').value = String(Math.round(to));
    $('rc-at').textContent = clock(to);
    remoteSend({ type: 'control', action: 'skip', value: by });
  });
}
$('rc-seek').addEventListener('input', () => { remoteSeeking = true; $('rc-at').textContent = clock(Number($('rc-seek').value)); });
$('rc-seek').addEventListener('change', () => {
  remoteSeeking = false;
  const to = Number($('rc-seek').value);
  holdRemote({ position: to, at: Date.now() });
  remoteSend({ type: 'control', action: 'seek', value: to });
});
// Volume as it is slid, a few times a second - it waited for the finger to
// lift, and a report arriving meanwhile pulled the slider back.
let volumeSendTimer = 0;
function sendVolume() {
  volumeTouchedAt = Date.now();
  const value = Number($('rc-volume').value) / 100;
  holdRemote({ volume: value });
  if (volumeSendTimer) return;
  volumeSendTimer = setTimeout(() => {
    volumeSendTimer = 0;
    remoteSend({ type: 'volume', value: Number($('rc-volume').value) / 100 });
  }, 200);
}
$('rc-volume').addEventListener('input', sendVolume);
$('rc-volume').addEventListener('change', sendVolume);
$('rc-stop').addEventListener('click', () => {
  remoteSend({ type: 'stop' });
  forgetRemote();
  if (RA.on) dropMirror();
});
// Play here: what the device is playing comes to this phone, and stops there.
$('rc-here').addEventListener('click', () => {
  const st = PLAYER.targetState;
  if (!st || !st.item) return;
  remoteSend({ type: 'stop' });
  const item = st.item;
  const at = (st.position || 0) + (st.playing && st.at ? (Date.now() - st.at) / 1000 : 0);
  forgetRemote();
  // Controlling the TV from Now Playing: its queue is already here; the
  // song carries on on this phone from the TV's moment.
  if (RA.on) {
    exitMirror(false);
    const q = audio.queue;
    if (q && raKey(q.items[q.index]) === raKey(item)) {
      playQueueAt(q.index);
      const a = $('audio-player');
      if (at > 1) a.addEventListener('loadedmetadata', () => { a.currentTime = at; }, { once: true });
      return;
    }
  }
  // A book's and a film's place were saved on the server as it stopped.
  setTimeout(() => {
    play(item);
    if (item.kind === 'music' && at > 1) {
      const a = $('audio-player');
      a.addEventListener('loadedmetadata', () => { a.currentTime = at; }, { once: true });
    }
  }, item.kind === 'music' ? 0 : 1200);
});

if (TV) {
  document.documentElement.classList.add('tv');
  tvRemote();
}

function tvRemote() {
  const FOCUSABLE = 'button, a[href], input:not([type="hidden"]), select, textarea, [tabindex]:not([tabindex="-1"])';
  // The top thing open is the only place the focus may go: a menu over Now
  // Playing, Now Playing over the library.
  // The questions over everything (a new device, a TV, phone backup) come
  // first: on a TV that may be the only device that can answer them.
  const LAYERS = ['player-ask', 'rc', 'device-ask', 'link-ask', 'backup-ask', 'item-menu', 'recap-overlay', 'video-overlay', 'reader-overlay', 'np-looks', 'now-playing'];
  const layer = () => {
    for (const id of LAYERS) if (shown(id)) return $(id);
    return document.body;
  };
  const usable = (el) => {
    if (el.disabled || el.closest('.hidden')) return false;
    const r = el.getBoundingClientRect();
    if (!r.width || !r.height) return false;
    const cs = getComputedStyle(el);
    return cs.visibility !== 'hidden' && cs.pointerEvents !== 'none' && Number(cs.opacity) > 0.05;
  };
  // Where the focus last was in each layer, so closing a menu or Now Playing
  // puts it back on the card it was opened from.
  const last = new WeakMap();
  // A text box reached with the arrows is only highlighted: focused for real,
  // it brings up the TV's on-screen keyboard at once, and the keyboard takes
  // the arrows. So it is read-only until OK is pressed on it.
  const locked = (el) => el && el.dataset && el.dataset.tvLock === '1';
  const unlock = (el) => {
    if (!locked(el)) return;
    delete el.dataset.tvLock;
    el.readOnly = false;
  };
  const focusOn = (el, dir) => {
    if (typing(el) && el.tagName !== 'SELECT' && !el.readOnly) {
      el.dataset.tvLock = '1';
      el.readOnly = true;
      el.addEventListener('blur', () => unlock(el), { once: true });
    }
    el.focus({ preventScroll: true });
    // Now Playing and a film fit the screen: moving about them never scrolls
    // (reported as Now Playing sliding up on reaching the timeline).
    const still = el.closest('#now-playing, #video-overlay');
    if (!still) el.scrollIntoView({ block: dir === 'left' || dir === 'right' ? 'nearest' : 'center', inline: 'nearest', behavior: 'smooth' });
    last.set(layer(), el);
  };
  const gap = (a1, a2, b1, b2) => Math.max(0, Math.max(a1, b1) - Math.min(a2, b2));
  // The nearest thing that way: the gap along the arrow, plus three times how
  // far off to the side it is, so a card straight below beats a nearer one
  // two columns over.
  const move = (dir) => {
    const root = layer();
    const cur = document.activeElement;
    const all = [...root.querySelectorAll(FOCUSABLE)].filter((el) => el !== cur && usable(el));
    if (!all.length) return;
    if (!cur || cur === document.body || !root.contains(cur) || !usable(cur)) {
      const back = last.get(root);
      if (back && root.contains(back) && usable(back)) { focusOn(back, dir); return; }
      // The page's first button - not the search box above it, nor the tabs
      // beside it (unless there is nothing else).
      const tabs = $('tabs');
      const inView = all.filter((el) => {
        const r = el.getBoundingClientRect();
        return r.bottom > 0 && r.top < innerHeight && !typing(el) && !tabs.contains(el);
      });
      const first = (inView.length ? inView : all).sort((a, b) => {
        const ra = a.getBoundingClientRect(); const rb = b.getBoundingClientRect();
        return (ra.top - rb.top) || (ra.left - rb.left);
      })[0];
      focusOn(first, dir);
      return;
    }
    const a = cur.getBoundingClientRect();
    const acx = (a.left + a.right) / 2;
    const acy = (a.top + a.bottom) / 2;
    let best = null;
    let bestScore = Infinity;
    for (const el of all) {
      if (el.contains(cur) || cur.contains(el)) continue;
      const b = el.getBoundingClientRect();
      const bcx = (b.left + b.right) / 2;
      const bcy = (b.top + b.bottom) / 2;
      let along;
      let aside;
      if (dir === 'right') {
        if (bcx <= acx + 1 || b.left < a.left) continue;
        along = Math.max(0, b.left - a.right); aside = gap(a.top, a.bottom, b.top, b.bottom);
      } else if (dir === 'left') {
        if (bcx >= acx - 1 || b.right > a.right) continue;
        along = Math.max(0, a.left - b.right); aside = gap(a.top, a.bottom, b.top, b.bottom);
      } else if (dir === 'down') {
        if (bcy <= acy + 1 || b.top < a.top) continue;
        along = Math.max(0, b.top - a.bottom); aside = gap(a.left, a.right, b.left, b.right);
      } else {
        if (bcy >= acy - 1 || b.bottom > a.bottom) continue;
        along = Math.max(0, a.top - b.bottom); aside = gap(a.left, a.right, b.left, b.right);
      }
      const offCentre = dir === 'left' || dir === 'right' ? Math.abs(bcy - acy) : Math.abs(bcx - acx);
      const score = along + aside * 3 + offCentre * 0.05;
      if (score < bestScore) { best = el; bestScore = score; }
    }
    if (!best) return;
    // Into the tabs from the page: onto the tab you are on, not whichever
    // happens to be level with the card.
    const tabs = $('tabs');
    if (tabs.contains(best) && !tabs.contains(cur)) {
      const current = tabs.querySelector('[aria-current="page"]');
      if (current && usable(current)) best = current;
    }
    focusOn(best, dir);
  };
  // What a hold or a right-click opens, for the thing in focus.
  const openMenuFor = (el) => {
    const r = el.getBoundingClientRect();
    el.dispatchEvent(new MouseEvent('contextmenu', {
      bubbles: true, cancelable: true, clientX: r.left + r.width / 2, clientY: r.top + r.height / 2,
    }));
    intoMenu();
  };
  // Into the menu once it is drawn (some menus fill in a moment later).
  const intoMenu = () => {
    const started = performance.now();
    const into = () => {
      if (shown('item-menu')) {
        if (!$('item-menu').contains(document.activeElement)) move('down');
      } else if (performance.now() - started < 400) {
        requestAnimationFrame(into);
      }
    };
    requestAnimationFrame(into);
  };
  // When a menu or Now Playing closes, the focus goes back to where it was
  // underneath at once - not only on the next arrow - so OK works straight
  // away. Nothing is focused while nothing has been (a first arrow does it).
  const restore = () => {
    const root = layer();
    const cur = document.activeElement;
    if (cur && cur !== document.body && root.contains(cur) && usable(cur)) return;
    const back = last.get(root);
    if (back && root.contains(back) && usable(back)) { back.focus({ preventScroll: true }); return; }
    // Opening: a film starts with the picture, where OK pauses and the
    // arrows skip. Now Playing starts with nothing in focus: OK plays and
    // pauses, left and right skip.
    const first = root.id === 'video-overlay' ? $('video-player') : null;
    if (first && usable(first)) first.focus({ preventScroll: true });
  };
  $('video-player').tabIndex = 0;

  // A film on a TV is the film, the whole screen: none of the browser's own
  // controls, whose small buttons (full screen, more options) a remote could
  // not reach - reported from the projector. The remote does the rest (OK,
  // left and right, Back), and a slim timeline with the name shows at the
  // foot when a key is pressed or the film is paused, fading after four
  // seconds (tv-bare), as Now Playing's buttons do.
  const film = $('video-player');
  film.controls = false;
  film.removeAttribute('controls');
  const bar = document.createElement('div');
  bar.id = 'tv-video-bar';
  const track = document.createElement('div');
  track.className = 'tvb-track';
  const fill = document.createElement('i');
  track.append(fill);
  const now = document.createElement('span');
  const end = document.createElement('span');
  const times = document.createElement('div');
  times.className = 'tvb-times';
  times.append(now, end);
  bar.append(track, times);
  $('video-overlay').append(bar);
  const drawBar = () => {
    const d = film.duration;
    const known = Number.isFinite(d) && d > 0;
    fill.style.width = known ? `${Math.min(100, (film.currentTime / d) * 100)}%` : '0';
    now.textContent = formatDuration(film.currentTime) || '0:00';
    end.textContent = known ? formatDuration(d) : '';
  };
  for (const ev of ['timeupdate', 'durationchange', 'seeked', 'loadedmetadata']) film.addEventListener(ev, drawBar);
  let bareTimer = 0;
  const overlay = $('video-overlay');
  const setBare = (on) => {
    // Only when it changes: the overlay's class is watched (the layers).
    if (overlay.classList.contains('tv-bare') !== on) overlay.classList.toggle('tv-bare', on);
  };
  const showFilmBar = () => {
    setBare(false);
    clearTimeout(bareTimer);
    bareTimer = setTimeout(function hide() {
      // Not while paused, nor while a picker has the focus.
      if (film.paused || overlay.querySelector('figcaption').contains(document.activeElement)) {
        bareTimer = setTimeout(hide, 4000);
        return;
      }
      setBare(true);
    }, 4000);
  };
  film.addEventListener('play', showFilmBar);
  film.addEventListener('pause', showFilmBar);
  document.addEventListener('keydown', () => { if (shown('video-overlay')) showFilmBar(); }, true);
  // Now Playing's buttons fade after a few seconds without the remote, the
  // music and its animation left alone on the screen (the owner's asking);
  // the next press brings them back and does nothing else, so nobody skips a
  // song by pressing to see the buttons. The title stays.
  let idleTimer = 0;
  const idleSoon = () => {
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      const np = $('now-playing');
      if (shown('now-playing') && !shown('item-menu') && !shown('np-looks') && !np.classList.contains('tv-idle')) np.classList.add('tv-idle');
    }, 4000);
  };
  const wake = () => {
    const np = $('now-playing');
    const was = np.classList.contains('tv-idle');
    // Only when it is there: removing an absent class still rewrites the
    // attribute, which the watcher below sees as a change and answers by
    // waking again - a loop that froze the app on opening Looks.
    if (was) np.classList.remove('tv-idle');
    if (shown('now-playing')) idleSoon(); else clearTimeout(idleTimer);
    return was;
  };
  const watch = new MutationObserver(() => {
    setTimeout(restore, 0);
    if (!shown('now-playing') || shown('item-menu') || shown('np-looks')) wake();
    else if (!$('now-playing').classList.contains('tv-idle')) idleSoon();
  });
  for (const id of LAYERS) if ($(id)) watch.observe($(id), { attributes: true, attributeFilter: ['class'] });
  const typing = (el) => Boolean(el && el.matches
    && el.matches('input:not([type="range"]):not([type="checkbox"]):not([type="radio"]):not([type="button"]), textarea, select'));
  const DIRS = { ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right' };
  let enterAt = 0;
  let held = false;
  let holdTimer = 0;
  document.addEventListener('keydown', (event) => {
    const t = event.target;
    // The chapter list and the speed menu (the owner's asking): up and down
    // move an option at a time, the list scrolling when the next is off it,
    // and nothing past the first or last; left or right, from anywhere on
    // it, closes it and gives the highlight back to the button that opened it.
    const list = shown('np-chapters') && t.closest && t.closest('#np-chapters') ? 'chapters'
      : shown('np-speed-menu') && t.closest && t.closest('#np-speed-menu') ? 'speed' : null;
    if (list) {
      const keys = ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'];
      if (keys.includes(event.key)) {
        event.preventDefault();
        wake();
        if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
          if (list === 'chapters') {
            closeChapters();
            $('np-chapters-btn').focus({ preventScroll: true });
          } else {
            show($('np-speed-menu'), false);
            $('np-speed').setAttribute('aria-expanded', 'false');
            $('np-speed').focus({ preventScroll: true });
          }
          return;
        }
        const all = [...(list === 'chapters' ? $('np-chapters-list') : $('np-speed-menu')).querySelectorAll('button')];
        const at = all.indexOf(t.closest('button'));
        const to = all[at + (event.key === 'ArrowDown' ? 1 : -1)];
        if (to) {
          to.focus({ preventScroll: true });
          to.scrollIntoView({ block: 'nearest' });
        }
        return;
      }
    }
    // Now Playing has no play, previous or next buttons on a TV (the owner's
    // asking). While its buttons are faded, or none of them has the focus,
    // OK plays and pauses and left and right skip - an audiobook's thirty
    // seconds, as those buttons do there. The remote's own previous and next
    // keys skip anywhere.
    const across = event.key === 'ArrowLeft' || event.key === 'ArrowRight';
    const npOnly = shown('now-playing') && !shown('item-menu') && !shown('np-looks');
    const faded = $('now-playing').classList.contains('tv-idle');
    const npFree = npOnly && (faded || !$('now-playing').contains(t));
    if (npFree && event.key === 'Enter' && audio.item) {
      event.preventDefault();
      if (!event.repeat) {
        const player = $('audio-player');
        if (player.paused) player.play().catch(() => {}); else player.pause();
      }
      return;
    }
    // From nothing in focus - faded or not - down goes straight to the
    // timeline and up to the arrow that puts Now Playing away to the mini
    // player, bringing the buttons back as it goes (the owner's asking).
    if (npFree && (event.key === 'ArrowDown' || event.key === 'ArrowUp')) {
      const to = event.key === 'ArrowDown' ? $('np-seek') : $('np-close');
      // Faded, it is see-through, which usable() refuses: here being on the
      // page is enough, as it is about to fade back in.
      if (to && !to.disabled && to.getClientRects().length) {
        event.preventDefault();
        wake();
        focusOn(to, 'down');
        return;
      }
    }
    // Up from the timeline is the arrow that puts Now Playing away, not
    // whichever top button is nearest (Looks); down from the top buttons is
    // the timeline.
    if (npOnly && !faded && event.key === 'ArrowUp' && t === $('np-seek')) {
      event.preventDefault();
      focusOn($('np-close'), 'up');
      wake();
      return;
    }
    if (npOnly && !faded && event.key === 'ArrowDown' && t.closest && t.closest('#now-playing .np-bar')) {
      event.preventDefault();
      focusOn($('np-seek'), 'down');
      wake();
      return;
    }
    // While the timeline shows (the buttons not faded), left and right move
    // through the song instead - ten seconds a press - and so they do on the
    // timeline itself (the owner's asking).
    if (npOnly && across && audio.item && !faded && (npFree || t === $('np-seek'))) {
      event.preventDefault();
      const by = event.key === 'ArrowRight' ? 10 : -10;
      if (audio.item.kind !== 'music') {
        bookSkip(by);
      } else {
        const player = $('audio-player');
        const end = Number.isFinite(player.duration) ? player.duration - 1 : Infinity;
        player.currentTime = Math.max(0, Math.min(player.currentTime + by, end));
      }
      wake();
      return;
    }
    if ((npFree && across) || event.key === 'MediaTrackNext' || event.key === 'MediaTrackPrevious') {
      if (audio.item) {
        event.preventDefault();
        const next = event.key === 'ArrowRight' || event.key === 'MediaTrackNext';
        // An audiobook goes by chapters here (the owner's asking): the next,
        // or back to the start of this one - the chapter before near its
        // start - as a swipe does. One without a chapter list keeps its
        // thirty seconds.
        if (audio.item.kind !== 'music' && (next ? nextChapterStart() : prevChapterStart()) !== null) {
          stepTrack(next ? 1 : -1);
          if (!faded) wake();
          return;
        }
        const btn = $(next ? 'np-next' : 'np-prev');
        if (!btn.disabled) btn.click();
        if (!faded) wake();
        return;
      }
    }
    // Any other press while Now Playing's buttons are faded only brings them back.
    if (shown('now-playing') && wake() && !event.key.startsWith('Media')) {
      event.preventDefault();
      return;
    }
    // The remote's own play/pause button: the film, else the music.
    if (event.key === 'MediaPlayPause' || event.key === 'MediaPlay' || event.key === 'MediaPause') {
      const media = shown('video-overlay') ? $('video-player') : $('audio-player');
      if (media.src) {
        event.preventDefault();
        if (event.key === 'MediaPause' || (event.key === 'MediaPlayPause' && !media.paused)) media.pause();
        else media.play().catch(() => {});
      }
      return;
    }
    // Up from a film's subtitle or audio picker is back to the film. The film
    // fills the screen, so it is never "above" anything, and the picker could
    // not be left at all (reported from the projector).
    if (event.key === 'ArrowUp' && shown('video-overlay') && t.closest && t.closest('#video-overlay figcaption')) {
      event.preventDefault();
      $('video-player').focus({ preventScroll: true });
      return;
    }
    // A film with the picture in focus: OK pauses and plays, left and right
    // skip ten seconds; up and down reach the close button and the pickers.
    if (t === $('video-player') && shown('video-overlay') && !shown('item-menu')) {
      const v = t;
      if (event.key === 'Enter') {
        event.preventDefault();
        if (!event.repeat) (v.paused ? v.play().catch(() => {}) : v.pause());
        return;
      }
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault();
        const to = v.currentTime + (event.key === 'ArrowRight' ? 10 : -10);
        v.currentTime = Math.max(0, Number.isFinite(v.duration) ? Math.min(to, v.duration - 1) : to);
        return;
      }
    }
    // OK on a highlighted text box: now it takes typing, keyboard and all.
    if (event.key === 'Enter' && locked(t)) {
      event.preventDefault();
      unlock(t);
      t.blur();
      t.focus();
      return;
    }
    // Down over a photo or a book pauses the music playing behind it, and
    // plays it again (the owner's asking); a message says which, as nothing
    // else on screen would.
    if (event.key === 'ArrowDown' && (photoShown || shown('reader-overlay')) && !shown('item-menu') && audio.item
        && !(t.closest && t.closest('.reader-bar'))) {
      event.preventDefault();
      const player = $('audio-player');
      if (player.paused) { player.play().catch(() => {}); showToast('Music playing'); }
      else { player.pause(); showToast('Music paused'); }
      return;
    }
    if (DIRS[event.key]) {
      // The photo viewer and the year in music step with the arrows themselves.
      if (event.defaultPrevented || photoShown || shown('recap-overlay')) return;
      const across = event.key === 'ArrowLeft' || event.key === 'ArrowRight';
      // In a book, left and right turn the page (reader.js listens for them);
      // up and down still reach its buttons.
      if (across && shown('reader-overlay') && !shown('item-menu')) return;
      // Left and right belong to a text box's cursor and a slider's thumb.
      // (Not a picker: on a TV the arrows leave it, and OK opens its list -
      // with the arrows kept, a film's audio picker could not be left.)
      if (across && ((typing(t) && !locked(t) && t.tagName !== 'SELECT') || (t.matches && t.matches('input[type="range"]')))) return;
      event.preventDefault();
      move(DIRS[event.key]);
      return;
    }
    // OK presses on letting go, so that holding it can open the menu
    // instead - timed, not counted in repeats, since remotes differ in
    // whether a held button repeats.
    if (event.key === 'Enter' && !typing(t) && t !== document.body) {
      event.preventDefault();
      if (event.repeat) return;
      enterAt = performance.now();
      held = false;
      clearTimeout(holdTimer);
      holdTimer = setTimeout(() => {
        if (enterAt) { held = true; openMenuFor(t); }
      }, HOLD_MS);
      return;
    }
    if (event.key === 'ContextMenu' && t !== document.body) {
      // A card opens its own menu for this key; the focus still goes into it.
      if (event.defaultPrevented) intoMenu();
      else { event.preventDefault(); openMenuFor(t); }
    }
  });
  // The mini player's place, on a TV: first in the side bar, the playing
  // song's cover, OK opening Now Playing. The mini player at the foot of the
  // screen is hidden there (style.css) - a remote reached it only by
  // scrolling to the end of a page, and it covered the bottom of every one
  // (the owner's report). The side bar is one press of left from anywhere.
  // Play and pause are the remote's own key, or OK in Now Playing.
  const npTab = document.createElement('button');
  npTab.type = 'button';
  npTab.className = 'tv-np-tab hidden';
  npTab.setAttribute('aria-label', 'Now Playing');
  const npArt = document.createElement('img');
  npArt.className = 'tv-np-art';
  npArt.alt = '';
  const npLabel = document.createElement('span');
  npLabel.textContent = 'Playing';
  npTab.append(npArt, npLabel);
  npTab.addEventListener('click', () => { if (audio.item) openNowPlaying(); });
  $('tabs').prepend(npTab);
  const syncNpTab = () => {
    show(npTab, Boolean(audio.item) && document.body.classList.contains('dock-open'));
    const src = $('audio-art').getAttribute('src') || NO_COVER;
    if (npArt.getAttribute('src') !== src) npArt.src = src;
  };
  // Watching the body (the dock shown or not) and the dock's cover, and
  // writing only to the tab's own image, so nothing watched changes.
  const npWatch = new MutationObserver(syncNpTab);
  npWatch.observe(document.body, { attributes: true, attributeFilter: ['class'] });
  npWatch.observe($('audio-art'), { attributes: true, attributeFilter: ['src'] });
  syncNpTab();
  document.addEventListener('keyup', (event) => {
    if (event.key !== 'Enter') return;
    clearTimeout(holdTimer);
    if (!enterAt) return;
    enterAt = 0;
    if (!held && !typing(event.target)) event.target.click();
  });
}
