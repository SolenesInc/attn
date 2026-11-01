import { createRequire } from 'node:module';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, '../..');
const { chromium } = createRequire(path.join(repo, 'app/package.json'))('@playwright/test');
const [url, vars, runs = '10'] = process.argv.slice(2);
if (!url || !vars) throw new Error('usage: app-session.mjs <app url> <daemon /debug/vars url> [bursts]');

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
await page.addInitScript(() => {
  window.__facts = 0;
  window.__commits = [];
  window.__recording = false;
  const name = (fiber) => {
    let type = fiber.type;
    if (type && type.$$typeof && type.type) type = type.type;
    if (type && type.render) type = type.render;
    return (type && (type.displayName || type.name)) || null;
  };
  const componentTags = new Set([0, 1, 11, 14, 15]);
  const performedWork = 1;
  window.__REACT_DEVTOOLS_GLOBAL_HOOK__ = {
    renderers: new Map(),
    supportsFiber: true,
    isDisabled: false,
    inject() { return 1; },
    checkDCE() {},
    onCommitFiberUnmount() {},
    onPostCommitFiberRoot() {},
    onScheduleFiberRoot() {},
    onCommitFiberRoot(_id, root) {
      if (!window.__recording) return;
      const rendered = [];
      const stack = [root.current];
      while (stack.length) {
        const fiber = stack.pop();
        if (componentTags.has(fiber.tag) && (fiber.flags & performedWork) !== 0) {
          rendered.push(name(fiber) ?? `tag${fiber.tag}`);
        }
        if (fiber.sibling) stack.push(fiber.sibling);
        if (fiber.child) stack.push(fiber.child);
      }
      window.__commits.push({ at: performance.now(), duration: root.current.actualDuration ?? null, rendered });
    },
  };
  const Native = window.WebSocket;
  const count = (handler) => function (event) {
    if (typeof event.data === 'string' && event.data.slice(0, 60).includes('"session_state_changed"')) window.__facts++;
    return handler.call(this, event);
  };
  window.WebSocket = class extends Native {
    addEventListener(type, handler, options) {
      super.addEventListener(type, type === 'message' ? count(handler) : handler, options);
    }
    set onmessage(handler) { super.onmessage = handler && count(handler); }
    get onmessage() { return super.onmessage; }
  };
});
await page.goto(url);
await page.waitForFunction(() => document.body.innerText.length > 200, null, { timeout: 60000 });
await page.waitForTimeout(3000);
await page.keyboard.press('Escape');
await page.waitForTimeout(3000);

const cdp = await page.context().newCDPSession(page);
await cdp.send('Performance.enable');
const metrics = async () => Object.fromEntries((await cdp.send('Performance.getMetrics')).metrics.map((m) => [m.name, m.value]));

const traced = async (work) => {
  const events = [];
  cdp.on('Tracing.dataCollected', ({ value }) => events.push(...value));
  const done = new Promise((resolve) => cdp.once('Tracing.tracingComplete', resolve));
  await cdp.send('Tracing.start', { categories: 'devtools.timeline', transferMode: 'ReportEvents' });
  await page.evaluate(() => { window.__facts = 0; window.__commits = []; window.__recording = true; });
  const before = await metrics();
  const started = Date.now();
  await work();
  await page.waitForTimeout(1500);
  const after = await metrics();
  const seconds = (Date.now() - started) / 1000;
  const state = await page.evaluate(() => { window.__recording = false; return { facts: window.__facts, commits: window.__commits }; });
  await cdp.send('Tracing.end');
  await done;
  cdp.removeAllListeners('Tracing.dataCollected');
  const counted = (name) => events.filter((e) => e.name === name && e.ph !== 'E').length;
  return {
    seconds, facts: state.facts, commits: state.commits,
    taskMs: (after.TaskDuration - before.TaskDuration) * 1000,
    scriptMs: (after.ScriptDuration - before.ScriptDuration) * 1000,
    layoutMs: (after.LayoutDuration - before.LayoutDuration) * 1000,
    styleMs: (after.RecalcStyleDuration - before.RecalcStyleDuration) * 1000,
    paints: counted('Paint'), layouts: counted('Layout'), styles: counted('UpdateLayoutTree'),
  };
};

const drive = () => new Promise((resolve, reject) => {
  const child = spawn('go', ['run', './scripts/sessionfactperf', '-vars', vars, '-n', runs], { cwd: repo, stdio: ['ignore', 'pipe', 'inherit'] });
  let out = '';
  child.stdout.on('data', (chunk) => { out += chunk; });
  child.on('exit', (code) => (code === 0 ? resolve(out) : reject(new Error(`driver exited ${code}\n${out}`))));
});

const report = (label, r) => {
  const per = (x) => (r.facts ? (x / r.facts).toFixed(2) : '-');
  console.log(`${label}: ${r.seconds.toFixed(0)}s, ${r.facts} session_state_changed, ${r.commits.length} React commits, ` +
    `main thread task ${r.taskMs.toFixed(0)}ms (script ${r.scriptMs.toFixed(0)}, style ${r.styleMs.toFixed(0)}, layout ${r.layoutMs.toFixed(0)}), ` +
    `${r.paints} paints, ${r.layouts} layouts, ${r.styles} style recalcs; per fact: task ${per(r.taskMs)}ms, script ${per(r.scriptMs)}ms`);
};

const idleSeconds = Number(process.env.IDLE_SECONDS ?? 20);
const idle = await traced(() => page.waitForTimeout(idleSeconds * 1000));
report('idle', idle);

let driverOut = '';
const load = await traced(async () => { driverOut = await drive(); });
process.stdout.write(driverOut);
report('facts', load);

const durations = load.commits.map((c) => c.duration).filter((d) => d !== null).sort((a, b) => a - b);
if (durations.length) {
  const at = (q) => durations[Math.min(durations.length - 1, Math.floor(q * durations.length))].toFixed(2);
  console.log(`React commit render duration (actualDuration, dev builds only), ms: p50 ${at(0.5)}, p90 ${at(0.9)}, p99 ${at(0.99)}, max ${durations.at(-1).toFixed(2)} over ${durations.length} commits`);
}
const renders = new Map();
for (const commit of load.commits) for (const name of commit.rendered) renders.set(name, (renders.get(name) ?? 0) + 1);
const top = [...renders].sort((a, b) => b[1] - a[1]).slice(0, Number(process.env.TOP ?? 40));
const totalRenders = [...renders.values()].reduce((a, b) => a + b, 0);
console.log(`component renders: ${totalRenders} over ${load.commits.length} commits (${(totalRenders / Math.max(1, load.commits.length)).toFixed(0)} per commit); most rendered:`);
for (const [name, n] of top) console.log(`  ${String(n).padStart(6)} ${name}`);
await browser.close();
