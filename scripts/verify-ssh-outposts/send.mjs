import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { parseArgs } from 'node:util';
import { world, quote, DaemonObserver } from './common.mjs';

const { values: o } = parseArgs({
  options: {
    macBin: { type: 'string' },
    instance: { type: 'string' },
    ssh: { type: 'string', default: 'ai-sandbox' },
    out: { type: 'string' },
    help: { type: 'boolean' },
  },
});
if (o.help || !o.macBin || !o.instance || !o.out) {
  console.log('node scripts/verify-ssh-outposts/send.mjs --macBin /tmp/attn-mac --instance send-proof --out /tmp/send-proof [--ssh ai-sandbox]');
  console.log('Pairs a sender on a fresh home instance, sends a file with attn send to a shell on a fresh outpost, and pulls it there with attn agent attachment.');
  process.exit(o.help ? 0 : 2);
}
o.scratch = `/tmp/attn-send-${o.instance}`;
const W = await world(o);
const { receipt, remote } = W;
let failed = false;
async function sendWithStdin(args, input) {
  const child = spawn(o.macBin, ['send', ...args], { env: W.env, stdio: ['pipe', 'pipe', 'pipe'] });
  let stdout = '', stderr = '';
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => { stderr += chunk; });
  child.stdin.end(input);
  const [code] = await once(child, 'exit');
  return { code, stdout, stderr };
}
try {
  await W.startOutpost();
  await W.startHome(path.join(o.out, 'notebook'));
  await W.pair();

  const sessionId = crypto.randomUUID();
  const spawned = W.observer.waitForMessage(m => m.event === 'spawn_result' && m.id === sessionId && m, 'shell spawn', 120_000);
  W.observer.send({ cmd: 'spawn_session', id: sessionId, host: W.outpost.id, agent: 'shell', cwd: W.R.root, profile_id: W.observer.profileId, placement: {}, label: 'send target', cols: 100, rows: 30 });
  if (!(await spawned).success) throw new Error('shell spawn on the outpost failed');
  const opened = { session_id: sessionId, terminal_id: W.observer.terminalOf(sessionId) };
  receipt('outpost shell opened', opened);

  const hostToken = path.join(W.macData, 'browser-host-token');
  if (!fs.existsSync(hostToken)) fs.writeFileSync(hostToken, crypto.randomBytes(32).toString('hex'), { mode: 0o600 });
  const app = new DaemonObserver({ connectTimeoutMs: 60_000, trustedApp: true });
  await app.connect();
  const pairing = spawn(o.macBin, ['send', 'pair', '--name', 'Verify', '--json'], { env: W.env, stdio: ['ignore', 'pipe', 'inherit'] });
  const lines = readline.createInterface({ input: pairing.stdout })[Symbol.asyncIterator]();
  const request = JSON.parse((await lines.next()).value);
  const shownPromise = app.waitForMessage(m => m.event === 'senders_updated' && m.requests.some(r => r.id === request.id) && m, 'the app sees the pairing request', 30_000);
  app.send({ cmd: 'senders_get' });
  const shown = await shownPromise;
  const seen = shown.requests.find(r => r.id === request.id);
  if (seen.code !== request.code) throw new Error(`the app shows code ${seen.code}; the program printed ${request.code}`);
  const decided = await app.requestResult({ cmd: 'sender_pair_decide', pairing_id: request.id, allow: true }, 'sender_pair_decide_result');
  if (!decided.success) throw new Error(`allow failed: ${decided.error}`);
  const { token, sender } = JSON.parse((await lines.next()).value);
  await once(pairing, 'exit');
  receipt('sender paired', { request, sender });
  await app.close();

  const file = path.join(o.out, 'data.csv');
  const rows = crypto.randomBytes(1_500_000).toString('hex').match(/.{1,32}/g).map((v, i) => `${i},${v}`);
  const bytes = Buffer.from(`month,total\n${rows.join('\n')}\n`);
  fs.writeFileSync(file, bytes);
  const digest = crypto.createHash('sha256').update(bytes).digest('hex');
  const sent = await sendWithStdin([opened.session_id, '--token-stdin', '--file', file, '-m', 'Totals look off in March', '--json'], `${token}\n`);
  if (sent.code !== 0) throw new Error(`attn send exited ${sent.code}: ${sent.stderr}`);
  const stdout = sent.stdout;
  const capture = JSON.parse(stdout);
  receipt('sent', { capture, bytes: bytes.length, sha256: digest });

  const out = `${W.R.root}/pulled.csv`;
  const pulled = await remote(`cd ${quote(W.R.root)} && ATTN_INSTANCE=${quote(o.instance)} ATTN_TERMINAL_ID=${quote(opened.terminal_id)} ATTN_INSIDE_APP=1 ${quote(W.R.binary)} agent attachment ${quote(capture.id)} ${quote(capture.attachments[0].id)} --out ${quote(out)} && sha256sum ${quote(out)}`);
  const remoteDigest = pulled.trim().split('\n').at(-1).split(/\s+/)[0];
  receipt('pulled on the outpost', { output: pulled, sha256: remoteDigest });
  if (remoteDigest !== digest) throw new Error(`outpost bytes differ: ${remoteDigest} != ${digest}`);

  const forged = await sendWithStdin([opened.session_id, '--token-stdin', '-m', 'approved'], 'not-a-paired-token\n');
  receipt('send with a token no sender holds', forged);
  if (forged.code === 0 || !forged.stderr.includes('attn send pair')) throw new Error('a send with an unknown token was not refused');
  receipt('verdict', { pass: true });
} catch (error) {
  failed = true;
  receipt('verdict', { pass: false, error: String(error?.stack ?? error) });
} finally {
  await W.cleanup();
}
process.exit(failed ? 1 : 0);
