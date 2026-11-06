#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync } from 'node:fs';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { chromium } from 'playwright';

const USAGE = `Usage: pnpm --dir website verify [--url <base>] [--shots <dir>]

Without --url, builds the site and checks the build through astro preview.
With --url, checks the server already running there, such as the dev server.
--shots <dir> also saves screenshots at 1280px and 390px, in light and dark.`;

const { values: options } = parseArgs({
  options: {
    url: { type: 'string' },
    shots: { type: 'string' },
    help: { type: 'boolean', short: 'h' },
  },
});
if (options.help) {
  console.log(USAGE);
  process.exit(0);
}

const websiteRoot = resolve(import.meta.dirname, '..');
const repoRoot = resolve(websiteRoot, '..');
const GITHUB_BLOB = 'https://github.com/SolenesInc/attn/blob/main/';
const DESKTOP = { name: '1280', width: 1280, height: 900 };
const PHONE = { name: '390', width: 390, height: 844 };

const SCHEDULING_WINDOW_MS = 2500;
const SETTLE_MS = 300;

const problems = [];
const queueLog = [];

function guideFiles() {
  const inFolder = (folder) =>
    readdirSync(join(repoRoot, 'docs/guide', folder))
      .filter((name) => name.endsWith('.md'))
      .map((name) => ({
        file: `docs/guide/${folder}/${name}`,
        path: `/guide/${name.replace(/\.md$/, '')}`,
      }));
  return [
    { file: 'docs/guide/README.md', path: '/guide' },
    ...inFolder('tutorials'),
    ...inFolder('reference'),
  ];
}

function featurePaths() {
  return readdirSync(join(websiteRoot, 'src/content/features'))
    .filter((name) => name.endsWith('.md'))
    .map((name) => `/features/${name.replace(/\.md$/, '')}`);
}

function freePort() {
  return new Promise((done, fail) => {
    const probe = createServer();
    probe.once('error', fail);
    probe.listen(0, '127.0.0.1', () => {
      const { port } = probe.address();
      probe.close(() => done(port));
    });
  });
}

function astro(...args) {
  const run = spawnSync('pnpm', ['exec', 'astro', ...args], { cwd: websiteRoot, stdio: 'inherit' });
  if (run.status !== 0) throw new Error(`astro ${args.join(' ')} exited with status ${run.status}`);
}

async function startPreview() {
  astro('build');
  const port = await freePort();
  astro('preview', '--background', '--port', String(port), '--host', '127.0.0.1');
  return { base: `http://127.0.0.1:${port}`, stop: () => astro('preview', 'stop') };
}

function normalizePath(pathname) {
  return pathname.length > 1 ? pathname.replace(/\/$/, '') : pathname;
}

function watchPage(page, base, path) {
  const host = new URL(base).host;
  page.on('console', (message) => {
    if (message.type() === 'error') problems.push(`${path}: console error: ${message.text()}`);
  });
  page.on('pageerror', (error) => problems.push(`${path}: page error: ${error.message}`));
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (url.protocol.startsWith('http') && url.host !== host) {
      problems.push(`${path}: third-party request: ${request.url()}`);
    }
  });
  page.on('response', (response) => {
    if (response.status() >= 400) problems.push(`${path}: HTTP ${response.status()} for ${response.url()}`);
  });
}

function checkImages(page) {
  return page.evaluate(async () => {
    const images = [...document.images];
    for (const image of images) image.loading = 'eager';
    await Promise.all(images.map((image) => image.decode().catch(() => {})));
    return {
      total: images.length,
      broken: images.filter((image) => image.naturalWidth === 0).map((image) => image.getAttribute('src')),
    };
  });
}

function openProseDetails(page) {
  return page.evaluate(() => {
    for (const details of document.querySelectorAll('.prose details')) details.open = true;
  });
}

function sidewaysOverflow(page) {
  return page.evaluate(() => {
    for (const details of document.querySelectorAll('details')) details.open = true;
    const root = document.documentElement;
    return root.scrollWidth > root.clientWidth ? `${root.scrollWidth}px > ${root.clientWidth}px` : null;
  });
}

function readPage(page) {
  return page.evaluate(() => ({
    h1: document.querySelector('h1')?.textContent?.trim() ?? '',
    ids: [...document.querySelectorAll('[id]')].map((element) => element.id),
    links: [...document.querySelectorAll('a[href]')].map((link) => link.href),
    mainText: document.querySelector('main')?.innerText ?? '',
    slot: document.querySelector('.recording-slot .label')?.textContent ?? '',
    video: Boolean(document.querySelector('.recording video')),
    doing: [...document.querySelectorAll('.doing-item')].map((item) => ({
      title: item.querySelector('h3')?.textContent ?? '',
      guide: item.querySelector('h3 a')?.href ?? '',
    })),
  }));
}

async function checkView(browser, base, path, view, scheme) {
  const context = await browser.newContext({
    viewport: { width: view.width, height: view.height },
    colorScheme: scheme,
  });
  const page = await context.newPage();
  watchPage(page, base, `${path} [${view.name} ${scheme}]`);
  const response = await page.goto(base + path, { waitUntil: 'load' });
  let facts = { h1: '', ids: [], links: [], mainText: '', slot: '', video: false, doing: [] };
  if (!response?.ok()) {
    problems.push(`${path}: HTTP ${response?.status()} loading the page`);
  } else {
    facts = await readPage(page);
    if (!facts.h1) problems.push(`${path}: no h1 on the page`);
    const images = await checkImages(page);
    facts.images = images.total;
    for (const src of images.broken) problems.push(`${path}: image did not load: ${src}`);
    await openProseDetails(page);
    if (options.shots) {
      const name = path === '/' ? 'home' : path.slice(1).replaceAll('/', '-');
      await page.screenshot({
        path: join(options.shots, `${name}-${view.name}-${scheme}.png`),
        fullPage: true,
      });
    }
    const overflow = await sidewaysOverflow(page);
    if (overflow) problems.push(`${path}: page scrolls sideways at ${view.width}px: ${overflow}`);
  }
  await context.close();
  return facts;
}

function checkFeaturePage(path, facts) {
  const slug = path.split('/').pop();
  if (!facts.video && !facts.slot.endsWith(`: ${slug}`)) {
    problems.push(`${path}: no recording slot named ${slug}`);
  }
  if (!facts.links.some((href) => /^\/guide\/./.test(new URL(href).pathname))) {
    problems.push(`${path}: no link into the guide`);
  }
  const named = facts.mainText.match(/experimental|crew v2|launcher 2\.0/i);
  if (named) problems.push(`${path}: the page says "${named[0]}", which feature pages never say`);
}

function checkLandingPage(facts, features) {
  if (!facts.video && !facts.slot.endsWith(': queue-loop')) problems.push('/: no recording slot named queue-loop');
  if (!facts.mainText.includes('brew install --cask victorarias/attn/attn')) problems.push('/: no install command');
  const paths = facts.links.map((href) => normalizePath(new URL(href).pathname));
  for (const path of ['/guide', ...features]) {
    if (!paths.includes(path)) problems.push(`/: no link to ${path}`);
  }
  if (facts.doing.length === 0) problems.push('/: "What attn does" has no items');
  for (const { title, guide } of facts.doing) {
    if (!guide || !new URL(guide).pathname.startsWith('/guide/')) problems.push(`/: "${title}" does not link into the guide`);
  }
}

function countScheduling() {
  window.scheduling = { scheduled: 0, fired: 0 };
  for (const name of ['setTimeout', 'setInterval', 'requestAnimationFrame']) {
    const original = window[name];
    window[name] = (callback, ...rest) => {
      window.scheduling.scheduled += 1;
      return original.call(
        window,
        (...args) => {
          window.scheduling.fired += 1;
          callback(...args);
        },
        ...rest,
      );
    };
  }
}

async function schedulingOver(page) {
  const read = () =>
    page.evaluate(() => ({
      ...window.scheduling,
      animations: document.getAnimations().filter((animation) => animation.playState === 'running').length,
    }));
  const before = await read();
  await page.waitForTimeout(SCHEDULING_WINDOW_MS);
  const after = await read();
  return {
    scheduled: after.scheduled - before.scheduled,
    fired: after.fired - before.fired,
    animations: after.animations,
  };
}

function queueView(page) {
  return page.evaluate(() => ({
    pane: document.querySelector('.queue-pane.is-on')?.dataset.pane,
    waiting: [...document.querySelectorAll('[data-waiting] [data-row]')].map((row) => row.dataset.row),
    count: document.querySelector('[data-waiting-count]')?.textContent,
    action: document.querySelector('[data-action-label]')?.textContent,
    focused: document.activeElement?.hasAttribute('data-action') ?? false,
  }));
}

async function pressEnterAndExpect(page, label, pane) {
  await page.keyboard.press('Enter');
  const view = await queueView(page);
  queueLog.push(`${label}: Enter -> on ${view.pane}, ${view.count} waiting, action "${view.action}"`);
  if (view.pane !== pane) problems.push(`queue ${label}: Enter went to ${view.pane}, expected ${pane}`);
  return view;
}

function expectAllSettled(view, label) {
  if (view.pane !== 'Home' || view.count !== '0' || view.action !== 'Start over') {
    problems.push(`queue ${label}: did not end at Home with nobody waiting: ${JSON.stringify(view)}`);
  }
}

async function queuePage(browser, base, contextOptions = {}) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, ...contextOptions });
  const page = await context.newPage();
  await page.addInitScript(countScheduling);
  watchPage(page, base, '/ [queue]');
  await page.goto(base, { waitUntil: 'load' });
  return { context, page };
}

async function checkQueueKeyboardRun(browser, base) {
  const { context, page } = await queuePage(browser, base);
  queueLog.push('Keyboard run, normal motion, nothing focused:');
  await pressEnterAndExpect(page, 'motion', 'fix-unit-conversion');
  const first = await pressEnterAndExpect(page, 'motion', 'Home');
  if (first.action !== "Nobody's waiting") problems.push(`queue motion: Home offered "${first.action}" while an agent still works`);
  await page.waitForFunction(() => document.querySelector('.queue-pane.is-on')?.dataset.pane === 'Run the tests', null, {
    timeout: 15000,
    polling: 200,
  });
  queueLog.push('motion: Run the tests finished, and the ticked box took the page to it with no key pressed');
  expectAllSettled(await pressEnterAndExpect(page, 'motion', 'Home'), 'motion');
  await context.close();
}

async function checkQueueReducedMotion(browser, base) {
  const { context, page } = await queuePage(browser, base, { reducedMotion: 'reduce' });
  queueLog.push('Keyboard run, reduced motion, Tab to Settle:');
  for (let presses = 0; presses < 20 && !(await queueView(page)).focused; presses += 1) await page.keyboard.press('Tab');
  if (!(await queueView(page)).focused) problems.push('queue reduced motion: Tab never reached Settle');
  const start = await queueView(page);
  queueLog.push(`reduced motion: starts on ${start.pane}, ${start.count} waiting`);
  if (start.waiting.length !== 3) problems.push(`queue reduced motion: expected 3 waiting at rest, saw ${start.waiting.length}`);
  await pressEnterAndExpect(page, 'reduced motion', 'fix-unit-conversion');
  await pressEnterAndExpect(page, 'reduced motion', 'Run the tests');
  const end = await pressEnterAndExpect(page, 'reduced motion', 'Home');
  expectAllSettled(end, 'reduced motion');
  if (!end.focused) problems.push('queue reduced motion: Settle lost keyboard focus');
  const idle = await schedulingOver(page);
  const total = await page.evaluate(() => window.scheduling);
  queueLog.push(`reduced motion: over the whole visit, ${total.scheduled} frames or timers scheduled; ${idle.animations} animations running`);
  if (total.scheduled > 0 || idle.animations > 0) problems.push(`queue reduced motion: it scheduled work: ${JSON.stringify({ total, idle })}`);
  await context.close();
}

async function checkQueueStopsWhenUnseen(browser, base) {
  const { context, page } = await queuePage(browser, base);
  const seen = await schedulingOver(page);
  queueLog.push(`In view for ${SCHEDULING_WINDOW_MS}ms: ${JSON.stringify(seen)}`);
  if (seen.fired === 0) problems.push('queue in view: no timer fired, so the unseen measurements prove nothing');

  await page.evaluate(() => window.scrollTo({ top: document.body.scrollHeight, behavior: 'instant' }));
  await page.waitForTimeout(SETTLE_MS);
  const offScreen = await schedulingOver(page);
  queueLog.push(`Scrolled off-screen for ${SCHEDULING_WINDOW_MS}ms: ${JSON.stringify(offScreen)}`);
  if (offScreen.scheduled || offScreen.fired || offScreen.animations) problems.push(`queue off-screen: it kept working: ${JSON.stringify(offScreen)}`);

  await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
  await page.waitForTimeout(SETTLE_MS);
  const back = await schedulingOver(page);
  queueLog.push(`Back in view for ${SCHEDULING_WINDOW_MS}ms: ${JSON.stringify(back)}`);
  if (back.fired === 0) problems.push('queue back in view: the clock did not restart');

  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { value: true, configurable: true });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  const hidden = await schedulingOver(page);
  queueLog.push(`Tab hidden (simulated, because Playwright keeps pages visible) for ${SCHEDULING_WINDOW_MS}ms: ${JSON.stringify(hidden)}`);
  if (hidden.scheduled || hidden.fired || hidden.animations) problems.push(`queue hidden tab: it kept working: ${JSON.stringify(hidden)}`);
  await context.close();
}

async function checkThemeToggle(browser, base) {
  const context = await browser.newContext({ colorScheme: 'light' });
  const page = await context.newPage();
  await page.goto(`${base}/guide/queue`, { waitUntil: 'load' });
  const theme = () => page.evaluate(() => document.documentElement.dataset.theme);
  if ((await theme()) !== 'light') problems.push('theme: a light system preference did not give the light theme');
  await page.click('[data-theme-toggle]');
  if ((await theme()) !== 'dark') problems.push('theme: the toggle did not switch to dark');
  await page.reload({ waitUntil: 'load' });
  if ((await theme()) !== 'dark') problems.push('theme: the dark choice did not survive a reload');
  await context.close();
}

const views = options.shots
  ? [
      [DESKTOP, 'light'],
      [DESKTOP, 'dark'],
      [PHONE, 'light'],
      [PHONE, 'dark'],
    ]
  : [
      [DESKTOP, 'light'],
      [PHONE, 'dark'],
    ];

if (options.shots) mkdirSync(options.shots, { recursive: true });
const browser = await chromium.launch();
const preview = options.url ? null : await startPreview();
const base = (options.url ?? preview.base).replace(/\/$/, '');

const files = guideFiles();
const features = featurePaths();
const queue = ['/', '/features', ...features, ...files.map((entry) => entry.path)];
const visited = new Map();
const linksFrom = [];

try {
  while (queue.length > 0) {
    const path = queue.shift();
    if (visited.has(path)) continue;
    let facts;
    for (const [view, scheme] of views) {
      const seen = await checkView(browser, base, path, view, scheme);
      facts ??= seen;
    }
    visited.set(path, facts);
    for (const href of facts.links) {
      const url = new URL(href);
      if (url.origin === new URL(base).origin) {
        const target = normalizePath(url.pathname);
        if (!target.startsWith('/_astro') && !target.startsWith('/@')) queue.push(target);
        linksFrom.push({ from: path, target, hash: decodeURIComponent(url.hash.slice(1)), href });
      } else if (href.startsWith(GITHUB_BLOB)) {
        const repoPath = decodeURI(url.pathname.split('/blob/main/')[1]);
        if (!existsSync(join(repoRoot, repoPath))) {
          problems.push(`${path}: links to ${repoPath}, which is not in the repository`);
        }
      }
    }
    console.log(`checked ${path}: ${facts.images ?? 0} images, ${facts.links.length} links`);
  }

  for (const { from, target, hash, href } of linksFrom) {
    if (hash && !visited.get(target)?.ids.includes(hash)) {
      problems.push(`${from}: link ${href} has no anchor "${hash}" on ${target}`);
    }
  }

  const guideIndexLinks = new Set(
    linksFrom.filter((link) => link.from === '/guide').map((link) => link.target),
  );
  for (const { file, path } of files) {
    if (path !== '/guide' && !guideIndexLinks.has(path)) {
      problems.push(`${file}: no row in the tables of docs/guide/README.md, so the guide navigation misses ${path}`);
    }
  }

  for (const path of features) {
    checkFeaturePage(path, visited.get(path));
    if (!linksFrom.some((link) => link.from === '/features' && link.target === path)) {
      problems.push(`/features: no link to ${path}`);
    }
  }

  checkLandingPage(visited.get('/'), features);
  await checkQueueKeyboardRun(browser, base);
  await checkQueueReducedMotion(browser, base);
  await checkQueueStopsWhenUnseen(browser, base);
  await checkThemeToggle(browser, base);
} finally {
  await browser.close();
  preview?.stop();
}

console.log(`\nQueue miniature:\n  ${queueLog.join('\n  ')}`);
const unique = [...new Set(problems)];
if (unique.length > 0) {
  console.error(`\nFAIL: ${unique.length} problems on ${visited.size} pages at ${base}`);
  for (const problem of unique) console.error(`  ${problem}`);
  process.exit(1);
}
console.log(
  `\nPASS: ${visited.size} pages at ${base}, ${files.length} guide files, ` +
    `${features.length} feature pages, ${linksFrom.length} internal links, views ${views.map(([view, scheme]) => `${view.name}/${scheme}`).join(' ')}`,
);
if (options.shots) console.log(`Screenshots in ${resolve(options.shots)}`);
