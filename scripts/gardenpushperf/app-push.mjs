import { createRequire } from 'node:module';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, '../..');
const { chromium } = createRequire(path.join(repo, 'app/package.json'))('@playwright/test');
const [url, seed, runs = '20'] = process.argv.slice(2);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
await page.addInitScript(() => {
  window.__pushes = [];
  const isPush = (data) => typeof data === 'string' && /"garden_seeds_(updated|changed)"/.test(data.slice(0, 120));
  const measure = (handler) => function (event) {
    if (!isPush(event.data)) return handler.call(this, event);
    const start = performance.now();
    const result = handler.call(this, event);
    const handled = performance.now();
    requestAnimationFrame(() => setTimeout(() => {
      const framed = performance.now();
      setTimeout(() => {
        const parseStart = performance.now();
        JSON.parse(event.data);
        window.__pushes.push({
          bytes: event.data.length, handler: handled - start, frame: framed - start,
          parse: performance.now() - parseStart,
        });
      }, 50);
    }));
    return result;
  };
  const Native = window.WebSocket;
  window.WebSocket = class extends Native {
    addEventListener(type, handler, options) {
      super.addEventListener(type, type === 'message' ? measure(handler) : handler, options);
    }
    set onmessage(handler) { super.onmessage = handler && measure(handler); }
    get onmessage() { return super.onmessage; }
  };
});
await page.goto(url);
await page.waitForFunction(() => document.body.innerText.length > 200, null, { timeout: 30000 });
await page.waitForTimeout(3000);
await page.evaluate(() => { window.__pushes = []; });
const profiler = process.env.PROFILE ? await page.context().newCDPSession(page) : null;
if (profiler) { await profiler.send('Profiler.enable'); await profiler.send('Profiler.start'); }

const drive = spawnSync('go', ['run', './scripts/gardenpushperf', '-seed', seed, '-n', runs, '-moves', 'move'], { cwd: repo, encoding: 'utf8' });
if (drive.status !== 0) throw new Error(drive.stderr);
process.stdout.write(drive.stdout);
await page.waitForTimeout(1500);

const pushes = await page.evaluate(() => window.__pushes);
if (profiler) {
  const { profile } = await profiler.send('Profiler.stop');
  const self = new Map();
  const interval = (profile.endTime - profile.startTime) / profile.samples.length / 1000;
  const byId = new Map(profile.nodes.map((n) => [n.id, n]));
  const parent = new Map();
  for (const n of profile.nodes) for (const c of n.children ?? []) parent.set(c, n.id);
  const total = new Map();
  for (const id of profile.samples) {
    const seen = new Set();
    for (let at = id; at !== undefined; at = parent.get(at)) {
      const f = byId.get(at).callFrame;
      const name = `${f.functionName || '(anon)'} ${path.basename(f.url)}:${f.lineNumber}`;
      if (at === id) self.set(name, (self.get(name) ?? 0) + interval);
      if (!seen.has(name)) { seen.add(name); total.set(name, (total.get(name) ?? 0) + interval); }
    }
  }
  const top = (m, n) => [...m].sort((a, b) => b[1] - a[1]).slice(0, n).map(([k, v]) => `  ${v.toFixed(0).padStart(6)}ms ${k}`).join('\n');
  console.log(`self:\n${top(self, 25)}\ninclusive:\n${top(total, 60)}`);
}
await browser.close();
const stat = (key) => {
  const xs = pushes.map((p) => p[key]).sort((a, b) => a - b);
  return `median ${xs[xs.length >> 1].toFixed(1)}ms (min ${xs[0].toFixed(1)}, max ${xs[xs.length - 1].toFixed(1)})`;
};
console.log(`app x${pushes.length}: bytes ${pushes[0]?.bytes}; handler ${stat('handler')}; message-to-frame ${stat('frame')}; JSON.parse ${stat('parse')}`);
