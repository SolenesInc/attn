import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { parseArgs } from 'node:util';
import { world, quote } from './common.mjs';

const { values: o } = parseArgs({
  options: {
    macBin: { type: 'string' },
    nextMacBin: { type: 'string' },
    instance: { type: 'string' },
    ssh: { type: 'string', default: 'ai-sandbox' },
    out: { type: 'string' },
    help: { type: 'boolean' },
  },
});
if (o.help) {
  console.log(
    'node scripts/verify-ssh-outposts/install.mjs --macBin /tmp/attn-home-a --nextMacBin /tmp/attn-home-b --instance fresh-install --out /tmp/install-proof [--ssh ai-sandbox]',
  );
  console.log(
    'Both home binaries must be source builds from this repository with distinct recorded fingerprints. Add obtains the Linux build itself. The named instance must have no files on either machine.',
  );
  process.exit(0);
}
for (const key of ['macBin', 'nextMacBin', 'instance', 'out'])
  if (!o[key]) throw new Error(`--${key} is required`);
o.scratch = `/tmp/attn-install-${o.instance}`;
const W = await world(o);
const { receipt, remote, attn } = W;
let removed = false;
const require = (value, reason) => {
  if (!value) throw new Error(reason);
};
const defaultSnapshot = () =>
  remote(
    `for p in "$HOME/.local/bin/attn" "$HOME/.local/bin/attn.previous" "$HOME/.attn"; do if test -e "$p"; then stat -c "%n %i %s %Y" "$p"; else printf "absent %s\\n" "$p"; fi; done; if test -d "$HOME/.attn"; then find "$HOME/.attn" -maxdepth 1 -printf "%p %i %s %T@\\n" | sort; fi; if test -f "$HOME/.attn/attn.pid"; then ps -p "$(cat "$HOME/.attn/attn.pid")" -o pid=,lstart=,args=; fi`,
  );
const listing = () =>
  remote(
    `for p in ${[W.R.binary, W.R.binary + '.previous', W.R.data, W.R.notebook].map(quote).join(' ')}; do if test -e "$p"; then stat -c "%n %F %s" "$p"; else printf "absent %s\\n" "$p"; fi; done`,
  );
let sending;
function watchPhases() {
  W.observer
    .waitForMessage(
      (m) => {
        if (m.event === 'outposts_updated')
          for (const row of m.outposts) {
            if (row.update?.phase === 'sending' && sending === undefined)
              sending = performance.now();
            if (row.update?.phase === 'verifying' && sending !== undefined) {
              receipt('uncompressed upload', {
                milliseconds: performance.now() - sending,
              });
              sending = undefined;
            }
            if (row.update?.phase === 'failed')
              receipt('install failed', { row });
          }
        return false;
      },
      'phase recording',
      3_600_000,
    )
    .catch(() => {});
}
async function shellCommand(id, command, marker) {
  const terminal = W.observer.terminalOf(id);
  let text = '';
  const done = W.observer.waitForMessage(
    (m) => {
      if (m.event !== 'pty_output' || m.id !== terminal) return false;
      text += Buffer.from(m.data, 'base64').toString();
      return text.includes(marker);
    },
    marker,
    60_000,
  );
  W.observer.send({ cmd: 'pty_input', id: terminal, data: command + '\r' });
  await done;
}
try {
  await W.startOutpost();
  const before = await listing();
  require(before
    .split('\n')
    .every((line) =>
      line.startsWith('absent '),
    ), `named instance has files before Add: ${before}`);
  receipt('listing before Add', { listing: before });
  const defaultBefore = await defaultSnapshot();
  receipt('default before', { snapshot: defaultBefore });
  const notebook = path.join(o.out, 'notebook');
  fs.mkdirSync(notebook, { recursive: true });
  const marker = `install-${crypto.randomUUID()}`;
  fs.writeFileSync(path.join(notebook, 'install-proof.md'), marker);
  await W.startHome(notebook);
  watchPhases();
  await W.pair();
  const resolved = JSON.parse(await W.rcli('instance', 'resolve', '--json'));
  require(resolved.dataDir ===
    W.R.data, `remote data differs: ${resolved.dataDir}`);
  await W.waitOutpost(
    (row) => row.id === W.outpost.id && row.notebook?.state === 'in_sync',
    'Notebook in sync',
  );
  require((await remote(`cat ${quote(W.R.notebook + '/install-proof.md')}`)) ===
    marker, 'Notebook content did not arrive');
  receipt('Add connected and in sync', {
    row: W.observer.outposts.find((r) => r.id === W.outpost.id),
    build: JSON.parse(await W.rcli('--build-info-json')),
  });
  const id = crypto.randomUUID();
  const spawned = W.observer.waitForMessage(
    (m) => m.event === 'spawn_result' && m.id === id && m,
    'shell spawn',
    60_000,
  );
  W.observer.send({
    cmd: 'spawn_session',
    id,
    host: W.outpost.id,
    agent: 'shell',
    cwd: o.scratch,
    profile_id: W.observer.profileId,
    placement: {},
    cols: 100,
    rows: 30,
  });
  require((await spawned).success, 'shell spawn failed');
  const terminal = W.observer.terminalOf(id);
  const attached = W.observer.waitForMessage(
    (m) => m.event === 'attach_result' && m.id === terminal && m,
    'shell attach',
    60_000,
  );
  W.observer.send({ cmd: 'attach_session', id: terminal });
  require((await attached).success, 'shell attach failed');
  await shellCommand(
    id,
    `python3 -c 'import os; print(os.getppid())' > ${quote(o.scratch + '/shell.pid')}; printf "before-%s\\n" ready`,
    'before-ready',
  );
  const shellBefore = await remote(`cat ${quote(o.scratch + '/shell.pid')}`);
  const oldHash = await remote(`sha256sum ${quote(W.R.binary)}`);
  await W.observer.close();
  W.home.kill('SIGTERM');
  await once(W.home, 'exit');
  o.macBin = o.nextMacBin;
  W.env.ATTN_HARNESS_BIN = o.macBin;
  process.env.ATTN_HARNESS_BIN = o.macBin;
  const log = fs.openSync(path.join(o.out, 'home.stderr'), 'a');
  W.home = spawn(o.macBin, ['daemon'], {
    env: { ...W.env, ATTN_DAEMON_READY_FD: '3' },
    stdio: ['ignore', log, log, 'pipe'],
  });
  fs.closeSync(log);
  const [ready] = await once(W.home.stdio[3], 'data');
  require(ready.toString().includes('ready'), 'second home not ready');
  const { DaemonObserver } =
    await import('../../app/scripts/real-app-harness/daemonObserver.mjs');
  W.observer = new DaemonObserver({ connectTimeoutMs: 120_000 });
  await W.observer.connect();
  const refused = await W.waitOutpost(
    (row) => row.id === W.outpost.id && row.state === 'refused',
    'reconnect refuses old build',
  );
  require((await remote(`sha256sum ${quote(W.R.binary)}`)) ===
    oldHash, 'reconnect changed install path');
  receipt('reconnect refused without installing', { row: refused });
  watchPhases();
  const start = performance.now();
  W.observer.send({ cmd: 'update_outpost', id: W.outpost.id });
  const updated = await W.waitOutpost(
    (row) =>
      row.id === W.outpost.id &&
      row.state === 'connected' &&
      row.update?.phase === 'done',
    'Update connected',
    300_000,
  );
  const homeInfo = JSON.parse((await attn('--build-info-json')).stdout);
  require(updated.build.includes(
    homeInfo.sourceFingerprint,
  ), 'Update did not match home build');
  const reattach = W.observer.waitForMessage(
    (m) => m.event === 'attach_result' && m.id === terminal && m,
    'reattach',
    60_000,
  );
  W.observer.send({ cmd: 'attach_session', id: terminal });
  require((await reattach).success, 'reattach failed');
  await shellCommand(
    id,
    `python3 -c 'import os; print(os.getppid())' > ${quote(o.scratch + '/shell.pid')}; printf "after-%s\\n" ready`,
    'after-ready',
  );
  const shellAfter = await remote(`cat ${quote(o.scratch + '/shell.pid')}`);
  require(shellBefore ===
    shellAfter, `shell PID changed ${shellBefore} -> ${shellAfter}`);
  receipt('Update kept shell', {
    milliseconds: performance.now() - start,
    shellBefore,
    shellAfter,
    row: updated,
  });
  const daemonPID = await remote(`cat ${quote(W.R.data + '/attn.pid')}`);
  W.observer.send({ cmd: 'remove_outpost', id: W.outpost.id });
  await W.observer.waitForMessage(
    (m) => m.event === 'outposts_updated' && m.outposts.length === 0,
    'Remove forgotten',
    60_000,
  );
  removed = true;
  const stopped = await remote(
    `if test -e /proc/${Number(daemonPID)}; then echo running; else echo stopped; fi`,
  );
  require(stopped === 'stopped', `removed daemon ${daemonPID} still running`);
  const after = await listing();
  require(!after
    .split('\n')
    .some((line) =>
      line.startsWith('absent '),
    ), `Remove discarded files: ${after}`);
  const defaultAfter = await defaultSnapshot();
  require(defaultAfter ===
    defaultBefore, 'default instance files or processes changed');
  receipt('Remove stopped daemon and retained files', {
    daemonPID,
    listing: after,
    defaultAfter,
  });
  receipt('PASS');
} finally {
  receipt('cleanup', { removed });
  await W.cleanup();
}
