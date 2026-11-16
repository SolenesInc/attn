import { createRequire } from 'node:module';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, '../..');
const { chromium } = createRequire(path.join(repo, 'app/package.json'))('@playwright/test');
const [url, seed, title, notes = '20'] = process.argv.slice(2);
if (!url || !seed || !title) throw new Error('usage: app-review-pull.mjs <bundle url> <seed id> <seed title> [notes]');

const dataDir = spawnSync('attn', ['instance', 'resolve', '--field', 'dataDir'], { encoding: 'utf8' }).stdout.trim();
const daemonLog = path.join(dataDir, 'daemon.log');
const logLength = () => readFileSync(daemonLog).length;
const daemonReceived = (from, cmd) => readFileSync(daemonLog).subarray(from).toString()
  .split('\n').filter((line) => line.endsWith(`WebSocket parsed cmd: ${cmd}`)).length;

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
await page.addInitScript(() => {
  window.__sent = [];
  const send = WebSocket.prototype.send;
  WebSocket.prototype.send = function (data) {
    if (typeof data === 'string') window.__sent.push(JSON.parse(data).cmd);
    return send.call(this, data);
  };
});
await page.goto(url);
await page.waitForFunction(() => document.body.innerText.length > 200, null, { timeout: 30000 });
await page.waitForTimeout(3000);

if (await page.locator('.whats-new-overlay').isVisible()) {
  await page.keyboard.press('Escape');
  await page.locator('.whats-new-overlay').waitFor({ state: 'hidden' });
}
const opened = logLength();
await page.getByRole('button', { name: 'Show the garden' }).click();
const garden = page.getByRole('region', { name: 'The garden' });
await garden.getByRole('button', { name: new RegExp(`^${title}.`) }).first().click();
await page.waitForTimeout(3000);
const atOpen = daemonReceived(opened, 'seed_review_show');

const noted = logLength();
const sentBefore = await page.evaluate(() => window.__sent.length);
const drive = spawnSync('go', ['run', './scripts/gardenpushperf', '-seed', seed, '-n', notes, '-moves', 'note'], { cwd: repo, encoding: 'utf8' });
if (drive.status !== 0) throw new Error(drive.stderr);
process.stdout.write(drive.stdout);
await page.waitForTimeout(3000);
const sent = (await page.evaluate(() => window.__sent)).slice(sentBefore);
await browser.close();

const count = (cmd) => sent.filter((sentCmd) => sentCmd === cmd).length;
console.log(`opening the Garden: daemon received ${atOpen} seed_review_show`);
console.log(`${notes} notes with the Garden open on ${seed}: daemon received ${daemonReceived(noted, 'seed_review_show')} seed_review_show; app sent ${count('seed_review_show')} seed_review_show and ${count('seed_document_get')} seed_document_get`);
