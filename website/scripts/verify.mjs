#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { chromium } from 'playwright';

const USAGE = `Usage: pnpm --dir website verify [--url <base>]

Without --url, builds the site and checks the build through astro preview.
With --url, checks the server already running there, such as the dev server.`;

const { values: options } = parseArgs({
  options: {
    url: { type: 'string' },
    help: { type: 'boolean', short: 'h' },
  },
});
if (options.help) {
  console.log(USAGE);
  process.exit(0);
}

const websiteRoot = resolve(import.meta.dirname, '..');
const distRoot = join(websiteRoot, 'dist');

const VIEWPORTS = [
  { name: '1440x900', width: 1440, height: 900 },
  { name: '390x844', width: 390, height: 844 },
];
const PAGES = [
  { path: '/', headline: 'Agents scale. Your attention doesn’t.', text: ['brew install --cask solenesinc/attn/attn', 'brew tap solenesinc/attn https://github.com/SolenesInc/attn'] },
  { path: '/guide/', headline: 'Coming soon', text: [] },
  { path: '/about/', headline: 'Why I built it', text: ['A note about this project', 'GPL-3.0', 'fork it'] },
];
const VIDEO_SECTIONS = ['queue', 'handoff', 'plan', 'room', 'keyboard'];
const QUIET_WINDOW_MS = 2500;
const VIDEO_WAIT_MS = 20000;

const problems = [];
const passes = [];

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

function filesUnder(folder) {
  return readdirSync(folder, { withFileTypes: true, recursive: true })
    .filter((entry) => entry.isFile())
    .map((entry) => join(entry.parentPath, entry.name));
}

function checkBuildOutput() {
  for (const { path } of PAGES) {
    if (!existsSync(join(distRoot, path, 'index.html'))) problems.push(`dist: no page for ${path}`);
  }
  if (existsSync(join(distRoot, 'features'))) problems.push('dist: /features was published');
  const guidePages = readdirSync(join(distRoot, 'guide'));
  if (guidePages.length !== 1) problems.push(`dist: /guide/ should hold only its index, found ${guidePages.join(', ')}`);
  for (const file of filesUnder(distRoot).filter((name) => /\.(html|css|js)$/.test(name))) {
    if (readFileSync(file, 'utf8').includes('/proto/')) problems.push(`dist: ${file} refers to /proto/`);
  }
}

function watchPage(page, base, label) {
  const host = new URL(base).host;
  page.on('console', (message) => {
    if (message.type() === 'error') problems.push(`${label}: console error: ${message.text()}`);
  });
  page.on('pageerror', (error) => problems.push(`${label}: page error: ${error.message}`));
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (url.protocol.startsWith('http') && url.host !== host) problems.push(`${label}: third-party request: ${request.url()}`);
  });
  page.on('response', (response) => {
    if (response.status() >= 400) problems.push(`${label}: HTTP ${response.status()} for ${response.url()}`);
  });
}

async function openPage(browser, base, path, viewport, label, contextOptions = {}) {
  const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height }, ...contextOptions });
  await context.addInitScript(countScheduledWork);
  const page = await context.newPage();
  watchPage(page, base, label);
  await page.goto(new URL(path, base).href, { waitUntil: 'load' });
  await page.evaluate(() => document.fonts.ready);
  return { context, page };
}

function countScheduledWork() {
  window.__fired = [];
  for (const name of ['requestAnimationFrame', 'setTimeout', 'setInterval']) {
    const original = window[name];
    window[name] = function (callback, ...rest) {
      const counted = typeof callback === 'function'
        ? function (...args) {
            window.__fired.push(name);
            return callback.apply(this, args);
          }
        : callback;
      return original.call(this, counted, ...rest);
    };
  }
}

async function checkContent(page, { path, headline, text }, label) {
  const facts = await page.evaluate(() => {
    const root = document.documentElement;
    const visibleSource = () => {
      const copy = document.body.cloneNode(true);
      for (const code of copy.querySelectorAll('script, style')) code.remove();
      return copy.textContent;
    };
    return {
      h1: document.querySelector('h1')?.textContent?.trim() ?? '',
      body: visibleSource(),
      overflow: root.scrollWidth > root.clientWidth ? `${root.scrollWidth}px > ${root.clientWidth}px` : null,
    };
  });
  if (facts.h1 !== headline) problems.push(`${label}: headline is "${facts.h1}", expected "${headline}"`);
  for (const wanted of text) {
    if (!facts.body.includes(wanted)) problems.push(`${label}: missing "${wanted}"`);
  }
  if (facts.overflow) problems.push(`${label}: sideways scroll, ${facts.overflow}`);
  passes.push(`${label}: content checked${path === '/' ? ', install command present' : ''}`);
}

async function checkLinks(page, base, { path }, checked) {
  const links = await page.evaluate(() =>
    [...document.querySelectorAll('a[href]')].map((link) => ({ href: link.href, hash: link.hash }))
  );
  const origin = new URL(base).origin;
  for (const { href, hash } of links) {
    const url = new URL(href);
    if (url.origin !== origin) continue;
    const route = url.pathname;
    if (!checked.has(route)) {
      const response = await fetch(new URL(route, base));
      checked.set(route, response.status);
    }
    if (checked.get(route) !== 200) problems.push(`${path}: link to ${route} returned ${checked.get(route)}`);
    if (hash && route === new URL(path, base).pathname) {
      const found = await page.evaluate((id) => Boolean(document.getElementById(id)), hash.slice(1));
      if (!found) problems.push(`${path}: link to ${hash} has no target`);
    }
  }
}

function waitForVideos(page, playing) {
  return page.waitForFunction(
    (name) => {
      const videos = [...document.querySelectorAll('video.as-media')];
      if (videos.length === 0) return false;
      return videos.every((video) => {
        const own = video.closest('.as-sec').dataset.section === name;
        return own ? !video.paused && video.currentTime > 0.1 : video.paused;
      });
    },
    playing,
    { timeout: VIDEO_WAIT_MS }
  );
}

function waitForAllPaused(page) {
  return page.waitForFunction(
    () => [...document.querySelectorAll('video.as-media')].every((video) => video.paused),
    undefined,
    { timeout: VIDEO_WAIT_MS }
  );
}

function scrollToSection(page, name) {
  return page.evaluate((id) => document.getElementById(id).scrollIntoView({ block: 'center' }), `as-${name}`);
}

async function checkVideos(browser, base, viewport) {
  const label = `/ videos ${viewport.name}`;
  const { context, page } = await openPage(browser, base, '/', viewport, label);
  const atTop = await page.evaluate(() => [...document.querySelectorAll('video.as-media')].filter((video) => !video.paused).length);
  if (atTop !== 0) problems.push(`${label}: ${atTop} video(s) playing under the hero`);
  for (const name of VIDEO_SECTIONS) {
    await scrollToSection(page, name);
    try {
      await waitForVideos(page, name);
      passes.push(`${label}: ${name} plays alone`);
    } catch {
      problems.push(`${label}: ${name} did not play alone within ${VIDEO_WAIT_MS}ms`);
    }
  }
  await page.evaluate(() => window.scrollTo(0, 0));
  try {
    await waitForAllPaused(page);
  } catch {
    problems.push(`${label}: videos kept playing after returning to the hero`);
  }
  await context.close();
}

async function scheduledDuringQuietWindow(page) {
  await page.evaluate(() => {
    window.__fired.length = 0;
  });
  await page.waitForTimeout(QUIET_WINDOW_MS);
  return page.evaluate(() => [...window.__fired]);
}

async function checkReducedMotion(browser, base, viewport, { path }) {
  const label = `${path} reduced motion ${viewport.name}`;
  const { context, page } = await openPage(browser, base, path, viewport, label, { reducedMotion: 'reduce' });
  const scheduled = await scheduledDuringQuietWindow(page);
  if (scheduled.length) problems.push(`${label}: ${scheduled.length} scheduled callback(s) ran while idle: ${[...new Set(scheduled)].join(', ')}`);
  if (path === '/') {
    await scrollToSection(page, 'plan');
    await page.waitForFunction(() => document.querySelector('#as-plan[data-focus]'));
    const playing = await page.evaluate(() => [...document.querySelectorAll('video.as-media')].filter((video) => !video.paused).length);
    if (playing) problems.push(`${label}: ${playing} video(s) playing`);
    const controls = await page.evaluate(() => [...document.querySelectorAll('video.as-media')].every((video) => video.controls));
    if (!controls) problems.push(`${label}: videos have no controls to start them`);
  }
  passes.push(`${label}: checked`);
  await context.close();
}

async function verify(base) {
  const browser = await chromium.launch();
  const linkStatus = new Map();
  try {
    for (const viewport of VIEWPORTS) {
      for (const entry of PAGES) {
        const label = `${entry.path} ${viewport.name}`;
        const { context, page } = await openPage(browser, base, entry.path, viewport, label);
        await checkContent(page, entry, label);
        await checkLinks(page, base, entry, linkStatus);
        await context.close();
        await checkReducedMotion(browser, base, viewport, entry);
      }
      await checkVideos(browser, base, viewport);
    }
  } finally {
    await browser.close();
  }
}

const preview = options.url ? { base: options.url.replace(/\/$/, ''), stop: () => {} } : await startPreview();
try {
  if (!options.url) checkBuildOutput();
  await verify(preview.base);
} finally {
  preview.stop();
}

for (const pass of passes) console.log(`ok  ${pass}`);
if (problems.length) {
  for (const problem of problems) console.error(`FAIL  ${problem}`);
  console.error(`\n${problems.length} problem(s)`);
  process.exit(1);
}
console.log(`\nAll checks passed against ${options.url ?? 'a production build'}.`);
