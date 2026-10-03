// Draws the web page's icons (ICONS in app.js) into the Apple TV app's image
// catalog, so both TV apps show exactly the same ones: each an SVG kept as a
// vector and drawn as a template, in the colour the app gives it.
//   node scripts/make-tv-icons.js
const fs = require('fs');
const path = require('path');
const root = path.join(__dirname, '..');
const app = fs.readFileSync(path.join(root, 'internal/webui/assets/app.js'), 'utf8');
const start = app.indexOf('const ICONS = {');
const end = app.indexOf('\n};', start) + 3;
const ICONS = new Function(app.slice(start, end).replace('const ICONS =', 'return') )();
const out = path.join(root, 'ios/SoundStormTV/Assets.xcassets/Icons');
fs.rmSync(out, { recursive: true, force: true });
fs.mkdirSync(out, { recursive: true });
fs.writeFileSync(path.join(out, 'Contents.json'), JSON.stringify({ info: { author: 'xcode', version: 1 }, properties: { 'provides-namespace': true } }, null, 2));
// Icons drawn filled on the page (.icon-more and the like) rather than stroked.
const filled = new Set(['more']);
let n = 0;
for (const [name, body] of Object.entries(ICONS)) {
  const paint = filled.has(name)
    ? 'fill="#000" stroke="none"'
    : 'fill="none" stroke="#000" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"';
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" ${paint}>${body}</svg>\n`;
  const dir = path.join(out, `${name}.imageset`);
  fs.mkdirSync(dir);
  fs.writeFileSync(path.join(dir, `${name}.svg`), svg);
  fs.writeFileSync(path.join(dir, 'Contents.json'), JSON.stringify({
    images: [{ filename: `${name}.svg`, idiom: 'universal' }],
    info: { author: 'xcode', version: 1 },
    properties: { 'preserves-vector-representation': true, 'template-rendering-intent': 'template' },
  }, null, 2));
  n++;
}
console.log(`${n} icons written to ${path.relative(root, out)}`);
