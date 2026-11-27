import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';
import assert from 'node:assert/strict';
import { after, before, describe, it } from 'node:test';
import { run } from './providers.mjs';
import { syncSource } from './source.mjs';

const sourceModule = new URL('./source.mjs', import.meta.url).href;

const localGuest = { ssh: (command, options) => run('bash', ['-c', command], options) };

function makeRepo(directory) {
  const git = (...args) => run('git', ['-C', directory, ...args], { stdio: 'ignore' });
  fs.mkdirSync(directory, { recursive: true });
  git('init', '--quiet');
  fs.writeFileSync(path.join(directory, 'README'), 'runner\n');
  git('add', 'README');
  git('-c', 'user.name=t', '-c', 'user.email=t@example.com', 'commit', '--quiet', '-m', 'init');
}

function makeCheckout(runner, name, ageDays) {
  const dir = path.join(runner, name);
  fs.mkdirSync(path.join(dir, 'source'), { recursive: true });
  fs.writeFileSync(path.join(dir, 'source', 'build-cache'), 'x');
  fs.writeFileSync(path.join(dir, 'managed-source'), '');
  const time = new Date(Date.now() - ageDays * 86_400_000);
  for (const file of [path.join(dir, 'operation.lock'), `${dir}.turn`]) {
    fs.writeFileSync(file, '');
    fs.utimesSync(file, time, time);
  }
  fs.utimesSync(dir, time, time);
  return dir;
}

async function holdTurn(dir) {
  const holder = spawn('flock', [`${dir}.turn`, '-c', 'echo took; exec cat >/dev/null'], { stdio: ['pipe', 'pipe', 'inherit'] });
  for await (const line of readline.createInterface({ input: holder.stdout })) if (line === 'took') break;
  return holder;
}

function syncInChild(repo, root) {
  const script = `import { syncSource } from ${JSON.stringify(sourceModule)};
import { run } from ${JSON.stringify(new URL('./providers.mjs', import.meta.url).href)};
syncSource({ ssh: (command, options) => run('bash', ['-c', command], { ...options, stdio: ['pipe', 'ignore', 'inherit'] }) }, ${JSON.stringify(repo)}, ${JSON.stringify(root)});`;
  const child = spawn(process.execPath, ['--input-type=module', '-e', script], { stdio: ['ignore', 'ignore', 'pipe'] });
  let stderr = '';
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  return new Promise((resolve) => child.once('close', (code) => resolve({ code, stderr })));
}

async function workIn(dir) {
  const worker = spawn('cat', [], { cwd: dir, stdio: ['pipe', 'ignore', 'inherit'] });
  await new Promise((resolve, reject) => worker.once('spawn', resolve).once('error', reject));
  return worker;
}

describe('sync evicts idle runner checkouts', { skip: process.platform !== 'linux' && 'the runner guest is Linux' }, () => {
  let temp;
  let runner;
  let holders;
  const exists = (name) => fs.existsSync(path.join(runner, name));

  before(async () => {
    temp = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-runner-evict-'));
    runner = path.join(temp, 'attn-linux-runner');
    makeRepo(path.join(temp, 'repo'));
    makeCheckout(runner, 'idle', 8);
    makeCheckout(runner, 'recent', 1);
    fs.rmSync(path.join(makeCheckout(runner, 'self', 8), 'source'), { recursive: true });
    holders = [await holdTurn(makeCheckout(runner, 'turn-held', 8)), await workIn(path.join(makeCheckout(runner, 'worked-in', 8), 'source'))];
    fs.writeFileSync(path.join(runner, 'gone.turn'), '');
    fs.utimesSync(path.join(runner, 'gone.turn'), new Date(Date.now() - 8 * 86_400_000), new Date(Date.now() - 8 * 86_400_000));
    syncSource(localGuest, path.join(temp, 'repo'), path.join(runner, 'self'));
  });

  after(() => {
    for (const holder of holders ?? []) holder.stdin.end();
    fs.rmSync(temp, { recursive: true, force: true });
  });

  it('removes a checkout and its turn file once both are unused past the threshold', () => {
    assert.equal(exists('idle'), false);
    assert.equal(exists('idle.turn'), false);
    assert.equal(exists('gone.turn'), false);
    assert.deepEqual(fs.readdirSync(runner).filter((name) => name.startsWith('.evicting')), []);
  });

  it('keeps a recently used checkout', () => assert.equal(exists('recent'), true));

  it('keeps a checkout whose turn is held', () => assert.equal(exists('turn-held/source/build-cache'), true));

  it('keeps a checkout a process works in', () => assert.equal(exists('worked-in/source/build-cache'), true));

  it('keeps and syncs the checkout it syncs into, however long it sat unused', () => {
    assert.equal(fs.readFileSync(path.join(runner, 'self/source/README'), 'utf8'), 'runner\n');
  });
});

describe('concurrent syncs evict the same idle checkouts', { skip: process.platform !== 'linux' && 'the runner guest is Linux' }, () => {
  it('both succeed and leave no idle checkout behind', async () => {
    const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-runner-evict-'));
    try {
      const runner = path.join(temp, 'attn-linux-runner');
      const repo = path.join(temp, 'repo');
      makeRepo(repo);
      for (let i = 0; i < 40; i++) makeCheckout(runner, `idle-${i}`, 8);
      const results = await Promise.all(['a', 'b'].map((name) => syncInChild(repo, path.join(runner, name))));
      assert.deepEqual(results.map(({ code }) => code), [0, 0], results.map(({ stderr }) => stderr).join('\n'));
      assert.deepEqual(fs.readdirSync(runner).sort(), ['a', 'b']);
    } finally {
      fs.rmSync(temp, { recursive: true, force: true });
    }
  });
});
