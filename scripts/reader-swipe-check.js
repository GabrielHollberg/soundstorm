// Does a swipe turn the page in the reader?
//
// The page-turn arrows are hidden on touch devices, and this is the evidence
// for that being safe. It is a script rather than a note because the claim is
// about third-party code we vendored - if foliate-js is ever updated, this is
// how to find out whether the answer changed before somebody discovers it with
// a book they cannot read.
//
// foliate-view's shadow root is mode: 'closed', so nothing can reach inside to
// check which page is showing. The only observable is the relocate event, and
// a relocate firing is NOT a page turn - it fires on resize too. The fraction
// it carries has to actually change, and change back.
//
// Needs playwright installed elsewhere; see scripts/mobile-check.js for the
// three lines that do it. Point it at a throwaway instance - it signs itself
// up - with a reflowable EPUB in the library whose title matches SWIPE_BOOK.
//
//   node <repo>/scripts/reader-swipe-check.js
//
// Exits non-zero unless every swipe, in both directions, moved the book.

const path = require('path');

let chromium;
try {
  chromium = require(require.resolve('playwright', {
    paths: [process.cwd(), path.join(process.cwd(), 'node_modules')],
  })).chromium;
} catch {
  console.error('playwright is not installed in ' + process.cwd() +
    ' - see the note at the top of scripts/mobile-check.js');
  process.exit(2);
}

const BASE = process.env.SHOT_BASE || 'http://localhost:8199';
// A fresh instance needs its setup code for the first sign-up: run it with
// SOUNDSTORM_SETUP_CODE set and pass the same value here as SETUP_CODE.
const SIGNUP_URL = BASE + '/?setup=' + encodeURIComponent(process.env.SETUP_CODE || '');
const USER = process.env.SHOT_USER || 'swipecheck';
const PASS = process.env.SHOT_PASS || 'swipecheckpassword';
const BOOK = process.env.SWIPE_BOOK || 'alice';
const ROUNDS = 3;

(async () => {
  const browser = await chromium.launch({ channel: 'chrome' });
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2, isMobile: true, hasTouch: true,
  });
  const page = await ctx.newPage();
  const cdp = await ctx.newCDPSession(page);

  await page.goto(SIGNUP_URL, { waitUntil: 'networkidle' });
  await page.fill('#gate-username', USER);
  await page.fill('#gate-password', PASS);
  await page.click('#gate-submit');
  await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });

  await page.fill('#search-input', BOOK);
  await page.press('#search-input', 'Enter');
  await page.waitForTimeout(2500);
  if (!await page.locator('#results .item, #results > *').count()) {
    console.error(`nothing found for "${BOOK}" - set SWIPE_BOOK to a book this instance has`);
    process.exit(1);
  }
  await page.locator('#results .item, #results > *').first().click();
  await page.waitForSelector('#reader-overlay:not(.hidden)', { timeout: 20000 });
  // The first page has to be painted before a page turn means anything.
  await page.waitForTimeout(6000);

  const fixed = await page.evaluate(() =>
    document.getElementById('reader-overlay').classList.contains('fixed-layout'));
  if (fixed) {
    console.error('that book is fixed-layout, which keeps its arrows - pick a reflowable one');
    process.exit(1);
  }

  await page.evaluate(() => {
    window.__f = null;
    document.querySelector('foliate-view')
      .addEventListener('relocate', e => { window.__f = e.detail && e.detail.fraction; });
  });
  const where = () => page.evaluate(() => window.__f);

  const box = await page.locator('#reader-host').boundingBox();
  const midY = box.y + box.height / 2;

  // A real gesture: down, ten moves, up. One jump is not a swipe and any
  // recogniser worth the name ignores it.
  async function swipe(forward) {
    const from = box.x + box.width * (forward ? 0.82 : 0.18);
    const to = box.x + box.width * (forward ? 0.18 : 0.82);
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchStart', touchPoints: [{ x: from, y: midY }],
    });
    for (let i = 1; i <= 10; i++) {
      await cdp.send('Input.dispatchTouchEvent', {
        type: 'touchMove',
        touchPoints: [{ x: from + (to - from) * (i / 10), y: midY }],
      });
      await page.waitForTimeout(16);
    }
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    await page.waitForTimeout(2000);
  }

  const fmt = f => (f === null ? 'null' : f.toFixed(5));
  let forwardOK = 0, backOK = 0;

  for (let r = 1; r <= ROUNDS; r++) {
    const before = await where();
    await swipe(true);
    const mid = await where();
    const fwd = before === null ? mid !== null : mid > before;
    if (fwd) forwardOK++;

    await swipe(false);
    const after = await where();
    const back = after < mid;
    if (back) backOK++;

    console.log(`round ${r}: ${fmt(before)} -swipe-> ${fmt(mid)} ${fwd ? 'FORWARD' : 'NO MOVE'}` +
      `  -back-> ${fmt(after)} ${back ? 'BACK' : 'NO MOVE'}`);
  }

  // The arrows should be gone here, because this context is a touch device
  // and the book is reflowable. Asserted rather than clicked: a hidden button
  // can still be clicked from JavaScript, so doing that would "pass" while
  // proving nothing about what a finger can reach.
  const arrowsHidden = await page.evaluate(() =>
    getComputedStyle(document.getElementById('reader-next')).display === 'none');
  console.log(`arrows : ${arrowsHidden ? 'hidden, as intended on touch' : 'STILL SHOWING'}`);

  // And they must come back for a mouse, which cannot swipe at all.
  const desktop = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const deskPage = await desktop.newPage();
  await deskPage.goto(BASE, { waitUntil: 'networkidle' });
  await deskPage.fill('#gate-username', USER);
  await deskPage.fill('#gate-password', PASS);
  await deskPage.click('#gate-submit');
  await deskPage.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
  await deskPage.fill('#search-input', BOOK);
  await deskPage.press('#search-input', 'Enter');
  await deskPage.waitForTimeout(2500);
  await deskPage.locator('#results .item, #results > *').first().click();
  await deskPage.waitForSelector('#reader-overlay:not(.hidden)', { timeout: 20000 });
  await deskPage.waitForTimeout(4000);
  const arrowsOnDesktop = await deskPage.evaluate(() =>
    getComputedStyle(document.getElementById('reader-next')).display !== 'none');
  console.log(`mouse  : arrows ${arrowsOnDesktop ? 'present, as they must be' : 'MISSING'}`);

  await browser.close();

  const ok = forwardOK === ROUNDS && backOK === ROUNDS && arrowsHidden && arrowsOnDesktop;
  console.log(`\nswipe forward ${forwardOK}/${ROUNDS}, back ${backOK}/${ROUNDS}`);
  console.log(ok
    ? 'swipe navigates reliably - hiding the arrows on touch is safe'
    : 'swipe is NOT reliable - the arrows must come back');
  process.exit(ok ? 0 : 1);
})().catch(e => { console.error(e); process.exit(1); });
