// Measure the UI at phone width, and screenshot it.
//
// This exists because "looks fine on my laptop" found none of what it found on
// the first run: a 531px-wide first screen on a 390px phone, one result card
// per screenful, a search box too narrow to show its own placeholder, and a
// service worker the Content-Security-Policy was refusing in silence. Layout
// bugs on a phone are measurable - a page either scrolls sideways or it does
// not - so they are measured here rather than judged from a screenshot.
//
// Playwright is not a project dependency and must not become one; the Go
// module has none and there is no package.json. Install it somewhere else and
// run this from that directory:
//
//   mkdir /tmp/ss-check && cd /tmp/ss-check && npm init -y
//   PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install playwright
//   node <repo>/scripts/mobile-check.js
//
// channel: 'chrome' drives the Chrome already on the machine, so no browser is
// downloaded. Point it at any SoundStorm with SHOT_BASE; it signs itself up,
// so give it one whose state you do not mind, not your own install.

const fs = require('fs');
const path = require('path');

// Resolved from the working directory rather than from here, because node
// resolves a bare require() against the *script's* directory - and this script
// lives in a repo that deliberately has no node_modules. Without this, the
// usage above fails with a bare MODULE_NOT_FOUND that says nothing about why.
let chromium;
try {
  chromium = require(require.resolve('playwright', {
    paths: [process.cwd(), path.join(process.cwd(), 'node_modules')],
  })).chromium;
} catch (err) {
  console.error(
    'playwright is not installed in ' + process.cwd() + '\n\n' +
    '  npm init -y\n' +
    '  PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install playwright\n\n' +
    'then run this again from that directory. It is deliberately not a\n' +
    'dependency of this project.');
  process.exit(2);
}

const BASE = process.env.SHOT_BASE || 'http://localhost:8199';
// A fresh instance needs its setup code for the first sign-up: run it with
// SOUNDSTORM_SETUP_CODE set and pass the same value here as SETUP_CODE.
const SIGNUP_URL = BASE + '/?setup=' + encodeURIComponent(process.env.SETUP_CODE || '');
const OUT = process.env.SHOT_OUT || path.join(process.cwd(), 'mobile-shots');
const USER = process.env.SHOT_USER || 'mobilecheck';
const PASS = process.env.SHOT_PASS || 'mobilecheckpassword';

const PHONE = { width: 390, height: 844 };  // iPhone 14/15
const SMALL = { width: 360, height: 780 };  // a common Android, and the floor

let failures = 0;

// check is the whole point of the script. A page that scrolls sideways is a
// bug you can prove, so it is asserted rather than looked at.
async function check(page, name) {
  fs.mkdirSync(OUT, { recursive: true });
  await page.screenshot({ path: path.join(OUT, `${name}.png`) });

  const report = await page.evaluate(() => {
    const doc = document.documentElement;
    // Something inside a deliberately side-scrolling container is meant to be
    // off screen - that is what the container is for. Without this, the filter
    // chips report a false positive on every single screen.
    const inScroller = el => {
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        const ov = getComputedStyle(p).overflowX;
        if (ov === 'auto' || ov === 'scroll') return true;
      }
      return false;
    };
    const wide = [];
    const small = [];
    for (const el of document.querySelectorAll('body *')) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      if (!inScroller(el) && r.right > doc.clientWidth + 1) {
        const id = el.id ? '#' + el.id : '';
        const cls = typeof el.className === 'string' && el.className
          ? '.' + el.className.trim().split(/\s+/).join('.') : '';
        wide.push(`${el.tagName.toLowerCase()}${id}${cls} right=${Math.round(r.right)}`);
      }
      if (el.matches('button, a, input, select, .chip') && r.height < 40) {
        small.push(`${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''} ${Math.round(r.width)}x${Math.round(r.height)}`);
      }
    }
    return {
      scrollW: doc.scrollWidth,
      clientW: doc.clientWidth,
      wide: [...new Set(wide)].slice(0, 6),
      small: [...new Set(small)].slice(0, 6),
    };
  });

  const scrolls = report.scrollW > report.clientW + 1;
  if (scrolls || report.wide.length) failures++;
  console.log(`  ${name.padEnd(24)} ${report.clientW}w  scrollW=${report.scrollW}  ` +
    (scrolls ? 'SCROLLS SIDEWAYS' : 'ok'));
  for (const w of report.wide) console.log(`      overflows: ${w}`);
  // Reported rather than failed: a 32px control is bad on a phone and fine
  // under a mouse, and this script cannot tell which one is holding it.
  for (const s of report.small) console.log(`      under 40px: ${s}`);
}

(async () => {
  const browser = await chromium.launch({ channel: 'chrome' });
  const ctx = await browser.newContext({
    viewport: PHONE, deviceScaleFactor: 2, isMobile: true, hasTouch: true,
  });
  const page = await ctx.newPage();
  // A CSP refusal appears here and nowhere else, which is how the inline
  // service-worker registration went unnoticed.
  page.on('console', m => {
    if (m.type() === 'error') { console.log('    [console] ' + m.text()); failures++; }
  });

  console.log(`\n=== ${PHONE.width}x${PHONE.height} ===`);
  await page.goto(SIGNUP_URL, { waitUntil: 'networkidle' });
  await page.waitForSelector('#gate:not(.hidden)', { timeout: 10000 });
  await check(page, 'signup');

  await page.fill('#gate-username', USER);
  await page.fill('#gate-password', PASS);
  await page.click('#gate-submit');
  await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
  await page.waitForTimeout(1200);
  await check(page, 'first-run');

  await page.fill('#search-input', 'the');
  await page.press('#search-input', 'Enter');
  await page.waitForTimeout(2500);
  await check(page, 'results');

  await page.click('#account-toggle');
  await page.waitForTimeout(400);
  await check(page, 'account');
  await page.click('#account-toggle');

  // The dock and video overlay need backends a bare instance has not got, so
  // their markup is filled in directly. The CSS under test is the real CSS.
  await page.evaluate(() => {
    document.getElementById('audio-dock').classList.remove('hidden');
    document.getElementById('audio-title').textContent = 'Chapter 07 - The Pit and the Pendulum';
    document.getElementById('audio-sub').textContent = 'Edgar Allan Poe';
    document.getElementById('audio-tracks-toggle').classList.remove('hidden');
  });
  await page.waitForTimeout(200);
  await check(page, 'dock');

  await page.evaluate(() => {
    document.getElementById('video-overlay').classList.remove('hidden');
    document.getElementById('video-caption').textContent = 'Big Buck Bunny (2008) - transcoding';
    document.getElementById('subtitle-picker').classList.remove('hidden');
  });
  await page.waitForTimeout(200);
  await check(page, 'video');
  await page.evaluate(() => document.getElementById('video-overlay').classList.add('hidden'));

  // The install surface, stated rather than assumed.
  const pwa = await page.evaluate(async () => ({
    manifest: !!document.querySelector('link[rel="manifest"]'),
    themeColor: !!document.querySelector('meta[name="theme-color"]'),
    appleCapable: !!document.querySelector('meta[name="apple-mobile-web-app-capable"]'),
    appleIcon: !!document.querySelector('link[rel="apple-touch-icon"]'),
    workers: (await navigator.serviceWorker?.getRegistrations?.() || []).length,
  }));
  console.log('\n  PWA:', JSON.stringify(pwa));
  // Read this honestly: it says the tags and the worker are right, and nothing
  // about whether a phone can install it.
  //
  // This runs against localhost, which browsers treat as a secure context
  // whatever the certificate. A real phone reaches SoundStorm by LAN address
  // over a certificate signed by an authority only that install knows about -
  // Chrome answers ERR_CERT_AUTHORITY_INVALID, treats the origin as having a
  // certificate error, and refuses to register a service worker there. So a
  // green line here sat happily alongside an Android that would not install.
  //
  // To test installability for real, point SHOT_BASE at the LAN or ts.net
  // address, from a browser that has not been told to ignore certificates.
  if (!pwa.manifest || !pwa.appleIcon) failures++;

  console.log(`\n=== ${SMALL.width}x${SMALL.height} ===`);
  await page.setViewportSize(SMALL);
  await page.waitForTimeout(400);
  await check(page, 'results-narrow');
  await check(page, 'dock-narrow');

  await browser.close();
  console.log(`\n${failures ? failures + ' problem(s)' : 'clean'} - shots in ${OUT}`);
  process.exit(failures ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });
