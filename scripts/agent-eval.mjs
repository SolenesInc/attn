#!/usr/bin/env node
import { execFile, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { parseArgs } from 'node:util';

const { values: options } = parseArgs({
  options: {
    attn: { type: 'string' },
    harness: { type: 'string', default: 'claude' },
    instance: { type: 'string' },
    model: { type: 'string' },
    only: { type: 'string' },
    scenario: { type: 'string' },
    'tool-home': { type: 'string', default: path.join(os.homedir(), 'attn-onboarding-rehearsal', 'tool-home') },
    parallel: { type: 'string', default: '4' },
    runs: { type: 'string', default: '3' },
  },
});
if (!options.attn) {
  console.error(`usage: node scripts/agent-eval.mjs --attn <binary> [--harness claude|codex] [--model <id>] [--only <probe,...>] [--scenario plain|chief|review] [--runs <n>] [--tool-home <dir>] [--instance <name>]

Start real headless agents with attn's launch instructions and a plain request,
and check that each reaches the right attn command. Actions are recorded, not
run, so this proves what agents find, not that the commands work.

The review scenario runs its commands for real on the daemon of --instance,
which it starts with code_review_enabled on, and judges the review document
the agent opened with attn review check.`);
  process.exit(2);
}
const attn = path.resolve(options.attn);

const plain = ['garden_available=true', 'asks_enabled=true'];
const repository = path.resolve(new URL('..', import.meta.url).pathname);
const presentedChange = '3982a161b';

const probes = [
  { id: 'message-chief', ask: 'Tell the Chief that the deploy finished.', want: /^agent msg chief\b/ },
  { id: 'list-agents', ask: 'Which other agents are running right now?', want: /^agent list\b/ },
  { id: 'read-agent', ask: 'What is the agent called fern doing? Read its screen.', want: /^agent peek fern\b/ },
  { id: 'close-self', ask: 'Close yourself. There is nothing left to do.', want: /^agent close --self\b/, reads: 'wrap-up' },
  { id: 'park-seed', ask: 'Stop working on seed s-abc123 for now and let someone else pick it up later.', want: /^seed park s-abc123\b/ },
  { id: 'remind-seed', ask: 'Leave a note that whoever tends seed s-abc123 in two hours should check the deploy.', want: /^seed remind s-abc123\b/ },
  { id: 'can-close', ask: 'Can I close you?', want: /^skill --reference wrap-up\b/ },
  { id: 'open-shell', ask: 'Open me a shell in /tmp.', want: /^terminal open\b.*\/tmp/ },
  { id: 'move-terminal', ask: 'Move this terminal to desktop 3.', want: /^terminal move\b.*\b3\b/ },
  { id: 'show-markdown', ask: 'Show me plan.md in the app.', want: /^open\b.*plan\.md/ },
  { id: 'change-setting', ask: 'Set the attn setting asks_enabled to true.', want: /^settings set asks_enabled\b/ },
  { id: 'add-crew', ask: 'Add a permanent agent named Fern to my crew.', want: /^crew (create|add)\b.*[Ff]ern/ },
  { id: 'plant-seed', ask: 'Track this for later and do not start it: the login test is flaky.', want: /^seed plant\b/ },
  { id: 'ask-user', ask: 'Ask me, in the way that lets you keep working, whether to use Postgres or Redis.', want: /^ask\b/ },
  { id: 'queue', ask: 'A notification from attn started this run, and attn agent inbox says your stop will not show. You have a result I must review.', want: /^turn open\b/ },
  { id: 'journal', ask: 'Add to my journal that we chose Postgres.', want: /^journal append\b/ },
  { id: 'browser', ask: 'Open example.com in the in-app browser.', want: /^browser open\b.*example\.com/ },
  { id: 'callout', ask: 'Point at the command palette in the app so I can see it.', want: /^callout show\b/ },
  { id: 'automation', ask: 'Every morning at nine, have an agent summarize my pull requests for this folder. Set it up now without asking me anything.', want: /^automation (apply|validate)\b/ },
  { id: 'delegate', ask: 'I authorize you to delegate this: have another agent fix the flaky login test.', want: /^delegate\b/, reads: 'delegation' },
  { id: 'roles', ask: 'Change the model that my Builder role uses.', want: /^delegate roles\b/ },
  { id: 'guide', ask: 'How do crew members work in attn? Use the documentation.', want: /^guide\b/ },
  { id: 'rename-session', ask: 'Rename this session to login tests.', want: /^session rename\b/ },
  { id: 'chief-journal', scenario: 'chief', ask: 'Add to my journal that we chose Postgres.', want: /^journal append\b/ },
  { id: 'chief-crew', scenario: 'chief', ask: 'I want a permanent agent named Fern for research.', want: /^crew (create|add)\b.*[Ff]ern/ },
  { id: 'chief-roles', scenario: 'chief', ask: 'Change the model that my Builder role uses.', want: /^delegate roles\b/ },
  { id: 'chief-settings', scenario: 'chief', ask: 'What attn settings do I have?', want: /^settings list\b/ },
  { id: 'chief-automation', scenario: 'chief', ask: 'Every morning at nine, have an agent summarize my pull requests for this folder. Set it up now without asking me anything.', want: /^automation (apply|validate)\b/ },
  { id: 'present-change', scenario: 'review', ask: 'Present the change.', want: /^open\b/, reads: 'review', stage: stageChange, judge: judgeReview, timeout: 900_000 },
].filter(probe => !options.only || options.only.split(',').includes(probe.id))
  .filter(probe => !options.scenario || (probe.scenario ?? 'plain') === options.scenario);

if (!options.instance && probes.some(probe => probe.scenario === 'review')) {
  if (options.only || options.scenario) {
    console.error('the review scenario runs attn review on a daemon: pass --instance <name>, an instance this run may start and change');
    process.exit(2);
  }
  console.log('Skipping the review scenario: it needs --instance <name>.\n');
  probes.splice(0, probes.length, ...probes.filter(probe => probe.scenario !== 'review'));
}
const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), 'attn-agent-eval-'));
const launches = { plain: launchText(plain), chief: chiefLaunchText() };
if (probes.some(probe => probe.scenario === 'review')) {
  launches.review = launchText([...plain, 'code_review_enabled=true']);
  startReviewDaemon();
}

function isolatedEnv(extra) {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(ATTN_|CLAUDE|CODEX_)/.test(key)));
  return { ...env, ATTN_DATA_DIR: path.join(root, 'data'), ...extra };
}

function render(recipient, event, values) {
  return execFileSync(attn, ['prompts', 'render', recipient, event, ...values.flatMap(value => ['--set', value])], { env: isolatedEnv({}), encoding: 'utf8' });
}

function instanceEnv() {
  const { ATTN_DATA_DIR, ...env } = isolatedEnv({ ATTN_INSTANCE: options.instance });
  return env;
}

function startReviewDaemon() {
  execFileSync(attn, ['daemon', 'ensure'], { env: instanceEnv(), stdio: 'inherit' });
  execFileSync(attn, ['settings', 'set', 'code_review_enabled', 'true'], { env: instanceEnv(), stdio: 'inherit' });
}

function stageChange(dir) {
  const repo = path.join(dir, 'attn');
  execFileSync('git', ['clone', '--quiet', '--shared', '--no-checkout', repository, repo]);
  execFileSync('git', ['-C', repo, 'checkout', '--quiet', '--detach', `${presentedChange}~1`]);
  const diff = execFileSync('git', ['-C', repository, 'diff', `${presentedChange}~1`, presentedChange]);
  execFileSync('git', ['-C', repo, 'apply'], { input: diff });
  return repo;
}

function judgeReview(dir, cwd, commands) {
  const opened = commands.filter(line => /^open\b/.test(line)).at(-1);
  if (!opened) return 'never opened a review document';
  const document = path.resolve(cwd, opened.replace(/^open\s+/, '').split(/\s+--/)[0].replace(/^['"]|['"]$/g, ''));
  if (!fs.existsSync(document)) return `opened ${document}, which does not exist`;
  fs.copyFileSync(document, path.join(dir, 'review.md'));
  const presented = fs.readFileSync(document, 'utf8').match(/^repository:\s*(.+)$/m)?.[1].trim();
  if (presented !== cwd) return `the document presents ${presented ?? 'no repository'}, not this run's ${cwd}; another run may have written it, so pass --parallel 1`;
  try {
    execFileSync(attn, ['review', 'check', document], { cwd, env: instanceEnv(), encoding: 'utf8' });
    return '';
  } catch (error) {
    return `attn review check: ${String(error.stdout ?? error.message).trim().split('\n').slice(0, 3).join('; ')}`;
  }
}

function launchText(values) {
  return render('session', 'launch', values);
}

function chiefLaunchText() {
  const scenario = new URL('../internal/prompts/scenarios/crew-v2-chief-fresh.json', import.meta.url);
  const { values } = JSON.parse(fs.readFileSync(scenario, 'utf8'));
  const sets = [...Object.entries({ ...values, onboarding: 'false' }).map(([key, value]) => `${key}=${value}`), 'garden_holdings='];
  const priming = render('crew', 'priming', sets);
  return launchText([...plain, `crew_priming=${priming}`]);
}

function installRecorder(dir, scenario) {
  const bin = path.join(dir, 'bin');
  fs.mkdirSync(bin, { recursive: true });
  const recorder = path.join(bin, 'attn');
  const live = scenario === 'review' ? `  review*|open*) exec env -u ATTN_DATA_DIR ATTN_INSTANCE='${options.instance}' "${attn}" "$@" ;;\n` : '';
  fs.writeFileSync(recorder, `#!/bin/sh
printf '%s\\n' "$*" >> "${path.join(dir, 'commands.log')}"
case "$1 $*" in
  skill*|guide*|prompts*|*--help*|*" -h"*) exec "${attn}" "$@" ;;
${live}esac
echo ok
`, { mode: 0o755 });
  return bin;
}

function runAgent(cwd, bin, instructions, probe) {
  const home = options['tool-home'];
  const path_ = `${bin}:${process.env.PATH}`;
  const reads = probe.scenario === 'review' ? ',Read,Grep,Glob,Bash(git *)' : '';
  const [command, args, env] = options.harness === 'codex'
    ? ['codex', ['exec', '--json', '--skip-git-repo-check', '--sandbox', 'workspace-write', '-c', `developer_instructions=${JSON.stringify(instructions)}`,
        ...(probe.scenario === 'review' ? ['-c', 'sandbox_workspace_write.network_access=true'] : []),
        ...(options.model ? ['--model', options.model] : []), probe.ask], { CODEX_HOME: path.join(home, '.codex') }]
    : ['claude', ['-p', probe.ask, '--output-format', 'stream-json', '--verbose', '--append-system-prompt', instructions, '--setting-sources', '', '--strict-mcp-config',
        '--tools', `Bash,Write${reads.replace(',Bash(git *)', '')}`, '--allowedTools', `Bash(attn *),Write${reads}`,
        '--model', options.model ?? 'sonnet'], { CLAUDE_CONFIG_DIR: path.join(home, '.claude') }];
  const tmp = path.join(path.dirname(bin), 'tmp');
  fs.mkdirSync(tmp, { recursive: true });
  return new Promise(resolve => {
    const child = execFile(command, args, { cwd, env: isolatedEnv({ ...env, PATH: path_, TMPDIR: tmp, ATTN_WRAPPER_PATH: path.join(bin, 'attn') }), timeout: probe.timeout ?? 300_000, maxBuffer: 64 * 1024 * 1024 },
      (error, stdout, stderr) => resolve({ failure: error ? `${command} failed: ${stderr.slice(-300)}` : '', stdout }));
    child.stdin.end();
  });
}

async function runProbe(probe, run) {
  const dir = path.join(root, probe.id, String(run));
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, 'plan.md'), '# Plan\n');
  const cwd = probe.stage ? probe.stage(dir) : dir;
  const bin = installRecorder(dir, probe.scenario);
  const { failure: ran, stdout } = await runAgent(cwd, bin, launches[probe.scenario ?? 'plain'], probe);
  const log = path.join(dir, 'commands.log');
  const commands = fs.existsSync(log) ? fs.readFileSync(log, 'utf8').trim().split('\n').filter(Boolean) : [];
  const reached = commands.some(line => probe.want.test(line));
  const read = !probe.reads || commands.some(line => line.startsWith(`skill --reference ${probe.reads}`));
  fs.writeFileSync(path.join(dir, 'transcript.jsonl'), stdout);
  const failure = ran || (reached && probe.judge ? probe.judge(dir, cwd, commands) : '');
  return { id: probe.id, run, pass: reached && read && !failure, reached, read, commands, failure };
}

const runs = Number(options.runs);
const queue = probes.flatMap(probe => Array.from({ length: runs }, (_, run) => ({ probe, run: run + 1 })));
const results = [];
await Promise.all(Array.from({ length: Number(options.parallel) }, async () => {
  for (let next = queue.shift(); next; next = queue.shift()) results.push(await runProbe(next.probe, next.run));
}));

let short = 0;
for (const probe of probes) {
  const mine = results.filter(result => result.id === probe.id).sort((a, b) => a.run - b.run);
  const passed = mine.filter(result => result.pass).length;
  if (passed < runs) short++;
  const shown = mine.find(result => !result.pass) ?? mine[0];
  const why = shown.pass ? '' : shown.failure || (shown.reached ? 'skipped the reference' : 'never reached the command');
  console.log(`${passed === runs ? 'pass' : 'FAIL'} ${passed}/${runs}  ${probe.id.padEnd(18)} ${shown.commands.join(' | ').slice(0, 150)}${why ? `  <- run ${shown.run}: ${why}` : ''}`);
}
console.log(`\n${probes.length - short}/${probes.length} probes passed all ${runs} runs with ${options.harness}. Transcripts: ${root}`);
process.exit(short ? 1 : 0);
