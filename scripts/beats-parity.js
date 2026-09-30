// Checks that the server's hearing (internal/beats) finds the same beats as
// the app's own (hearSong in app.js) for the same samples.
//
//   BEATS_PARITY_DIR=/tmp/parity go test ./internal/beats/ -run AppCheck
//   node scripts/beats-parity.js /tmp/parity
//
// hearSong is lifted out of app.js as it is and run under Node with a
// stand-in audio context that hands it the Go test's samples at 44100 a
// second (refusing the lower rates, as an older Safari does), so both sides
// see exactly the same audio.
const fs = require('fs');
const path = require('path');

const dir = process.argv[2];
const app = fs.readFileSync(path.join(__dirname, '..', 'internal/webui/assets/app.js'), 'utf8').split('\r').join('');
const start = app.indexOf('async function hearSong(');
const end = app.indexOf('\n}\n', start) + 3;
const raw = fs.readFileSync(path.join(dir, 'click.f32'));
const samples = new Float32Array(raw.buffer.slice(raw.byteOffset, raw.byteOffset + raw.length));
const goBytes = fs.readFileSync(path.join(dir, 'click.beats'));
const goBeats = new Float64Array(goBytes.buffer.slice(goBytes.byteOffset, goBytes.byteOffset + goBytes.length));

class OfflineAudioContext {
  constructor(ch, len, rate) {
    if (rate !== 44100) throw new Error('rate refused');
  }
  decodeAudioData(bytes, ok) {
    ok({ length: samples.length, duration: samples.length / 44100, numberOfChannels: 1, getChannelData: () => samples });
  }
}
const window = { OfflineAudioContext };
const hearState = new Map();
const selectionKey = () => 'k';
const songBytes = async () => new ArrayBuffer(0);
const hearSong = eval(`(${app.slice(start, end).replace('async function hearSong', 'async function')})`);

(async () => {
  const heard = await hearSong({ kind: 'music' }, 120);
  const js = Array.from(heard.beats);
  let worst = 0;
  const n = Math.min(js.length, goBeats.length);
  for (let i = 0; i < n; i++) worst = Math.max(worst, Math.abs(js[i] - goBeats[i]));
  // The hit lanes, frame by frame.
  const lane = (name) => {
    const b = fs.readFileSync(path.join(dir, `click.${name}`));
    const go = new Float32Array(b.buffer.slice(b.byteOffset, b.byteOffset + b.length));
    const app2 = heard[name];
    let d = go.length === app2.length ? 0 : Infinity;
    for (let i = 0; i < Math.min(go.length, app2.length); i++) d = Math.max(d, Math.abs(go[i] - app2[i]));
    return d;
  };
  const low = lane('low');
  const high = lane('high');
  console.log(JSON.stringify({ app: js.length, server: goBeats.length, worstDifferenceMs: +(worst * 1000).toFixed(3), down: heard.down, lowDifference: low, highDifference: high }));
  if (js.length !== goBeats.length || worst > 1e-6 || low > 1e-5 || high > 1e-5) process.exit(1);
})();
