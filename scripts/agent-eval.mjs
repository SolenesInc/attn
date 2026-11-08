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
    model: { type: 'string' },
    only: { type: 'string' },
    scenario: { type: 'string' },
    'tool-home': { type: 'string', default: path.join(os.homedir(), 'attn-onboarding-rehearsal', 'tool-home') },
    parallel: { type: 'string', default: '4' },
    runs: { type: 'string', default: '3' },
  },
});
if (!options.attn) {
  console.error(`usage: node scripts/agent-eval.mjs --attn <binary> [--harness claude|codex] [--model <id>] [--only <probe,...>] [--scenario plain|chief] [--runs <n>] [--tool-home <dir>]

Start real headless agents with attn's launch instructions and a plain request,
and check that each reaches the right attn command. Actions are recorded, not
run, so this proves what agents find, not that the commands work.`);
  process.exit(2);
}
const attn = path.resolve(options.attn);

const plain = ['garden_available=true', 'asks_enabled=true'];

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
].filter(probe => !options.only || options.only.split(',').includes(probe.id))
  .filter(probe => !options.scenario || (probe.scenario ?? 'plain') === options.scenario);

const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), 'attn-agent-eval-'));
const launches = { plain: launchText(plain), chief: chiefLaunchText() };

function isolatedEnv(extra) {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(ATTN_|CLAUDE|CODEX_)/.test(key)));
  return { ...env, ATTN_DATA_DIR: path.join(root, 'data'), ...extra };
}

function render(recipient, event, values) {
  return execFileSync(attn, ['prompts', 'render', recipient, event, ...values.flatMap(value => ['--set', value])], { env: isolatedEnv({}), encoding: 'utf8' });
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

function installRecorder(dir) {
  const bin = path.join(dir, 'bin');
  fs.mkdirSync(bin, { recursive: true });
  const recorder = path.join(bin, 'attn');
  fs.writeFileSync(recorder, `#!/bin/sh
printf '%s\\n' "$*" >> "${path.join(dir, 'commands.log')}"
case "$1 $*" in
  skill*|guide*|prompts*|*--help*|*" -h"*) exec "${attn}" "$@" ;;
esac
echo ok
`, { mode: 0o755 });
  return bin;
}

function runAgent(dir, bin, instructions, ask) {
  const home = options['tool-home'];
  const path_ = `${bin}:${process.env.PATH}`;
  const [command, args, env] = options.harness === 'codex'
    ? ['codex', ['exec', '--skip-git-repo-check', '--sandbox', 'workspace-write', '-c', `developer_instructions=${JSON.stringify(instructions)}`,
        ...(options.model ? ['--model', options.model] : []), ask], { CODEX_HOME: path.join(home, '.codex') }]
    : ['claude', ['-p', ask, '--append-system-prompt', instructions, '--setting-sources', '', '--strict-mcp-config', '--tools', 'Bash,Write', '--allowedTools', 'Bash(attn *),Write',
        '--model', options.model ?? 'sonnet'], { CLAUDE_CONFIG_DIR: path.join(home, '.claude') }];
  return new Promise(resolve => {
    const child = execFile(command, args, { cwd: dir, env: isolatedEnv({ ...env, PATH: path_, ATTN_WRAPPER_PATH: path.join(bin, 'attn') }), timeout: 300_000, maxBuffer: 16 * 1024 * 1024 },
      (error, stdout, stderr) => resolve({ failure: error ? `${command} failed: ${stderr.slice(-300)}` : '', stdout }));
    child.stdin.end();
  });
}

async function runProbe(probe, run) {
  const dir = path.join(root, probe.id, String(run));
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, 'plan.md'), '# Plan\n');
  const bin = installRecorder(dir);
  const { failure, stdout } = await runAgent(dir, bin, launches[probe.scenario ?? 'plain'], probe.ask);
  const log = path.join(dir, 'commands.log');
  const commands = fs.existsSync(log) ? fs.readFileSync(log, 'utf8').trim().split('\n').filter(Boolean) : [];
  const reached = commands.some(line => probe.want.test(line));
  const read = !probe.reads || commands.some(line => line.startsWith(`skill --reference ${probe.reads}`));
  fs.writeFileSync(path.join(dir, 'answer.txt'), stdout);
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
