#!/usr/bin/env node
import { execFile, spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline';
import { parseArgs, promisify } from 'node:util';
import { mainModel } from './lib/main-model.mjs';

const usage = `usage: node scripts/workflow-scorecard.mjs collect [--history <file>]
       node scripts/workflow-scorecard.mjs escapes [--since <date>] [--until <date>] [--history <file>]
       node scripts/workflow-scorecard.mjs [--since <date>] [--until <date>] [--by week|day|skill] [--host <name>] [--history <file>]

collect reads every closed Claude and Codex session in attn's session ledger
that the history file lacks, through attn session transcript, and appends one
line per session. A session whose transcript cannot be read, or shows no user
message, gets no line, so the next collect tries it again.

The scorecard prints, for sessions that started in [--since, --until), what
delegated Builders and Orchestrators loaded and read, how Orchestrators judged
checks, and what review on main found. --since defaults to seven days before
--until, which defaults to today. --by week or day prints one table per
period. --by skill prints one table per attn-workflow text the sessions loaded,
identified by a hash of the text, because a change reaches agents when each
host is reinstalled, not when it lands. --host takes home, a host name or the
start of a host id.

escapes prints, for notes written in [--since, --until), every finding line
review on main wrote, every rejected check on the seeds of Builders that ran
then, and every note of the Sheriff, which is where a red main traced to a
commit is recorded. The garden keeps a seed's notes for 30 days.

Nothing here calls a model.

The history file defaults to workflow-sessions.jsonl in the artifact folder of
the seed improve-attn-workflow-record-real-work (s-4z5bcq).`;

const historySeed = 's-4z5bcq';
const reviewMainSlug = 'review-main';
const sheriffSlug = 'sheriff';
const transcriptReaders = 6;
const gardenReaders = 8;
const reviewWindow = 80;
const sizeBounds = [40, 120];

const run = promisify(execFile);

async function attnJSON(...args) {
  const { stdout } = await run('attn', [...args, '--json'], { maxBuffer: 512 * 1024 * 1024 });
  return JSON.parse(stdout);
}

function streamTranscript(id, onEvent) {
  return new Promise((resolve, reject) => {
    const child = spawn('attn', ['session', 'transcript', id, '--json'], { stdio: ['ignore', 'pipe', 'pipe'] });
    let stderr = '';
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    readline.createInterface({ input: child.stdout }).on('line', (line) => {
      if (line.startsWith('{')) onEvent(JSON.parse(line));
    });
    child.on('error', reject);
    child.on('close', (code) => {
      const reason = stderr.split('\n').filter((line) => line.trim() && !line.startsWith('cursor: ')).join(' ');
      return code === 0 ? resolve() : reject(new Error(reason || `exit ${code}`));
    });
  });
}

async function pool(items, width, work) {
  const results = new Array(items.length);
  let next = 0;
  await Promise.all(Array.from({ length: Math.min(width, items.length) }, async () => {
    while (next < items.length) {
      const index = next++;
      results[index] = await work(items[index]);
    }
  }));
  return results;
}

async function defaultHistoryPath() {
  const shown = await attnJSON('seed', 'show', historySeed);
  return path.join(shown.artifact_dir, 'workflow-sessions.jsonl');
}

function wholeLines(file) {
  if (!fs.existsSync(file)) return '';
  const text = fs.readFileSync(file, 'utf8');
  return text.slice(0, text.lastIndexOf('\n') + 1);
}

function readHistory(file) {
  const lines = new Map();
  for (const line of wholeLines(file).split('\n').filter(Boolean).map((row) => JSON.parse(row))) {
    if (!lines.has(line.session)) lines.set(line.session, line);
  }
  return [...lines.values()];
}

async function ledger() {
  const rows = [];
  let before;
  do {
    const page = await attnJSON('session', 'list', '--all', '--limit', '1000', ...(before ? ['--before', before] : []));
    rows.push(...page.entries);
    before = page.next_before;
  } while (before);
  return rows;
}

async function thisHost(rows) {
  try {
    const [{ stdout: self }, status] = await Promise.all([run('attn', ['presence', '--session-id']), attnJSON('update', 'status')]);
    const row = rows.find((r) => r.id === self.trim());
    return row?.host && status.kind === 'outpost' ? { id: row.host, name: status.outpost } : null;
  } catch {
    return null;
  }
}

const shellTools = new Set(['Bash', 'exec', 'exec_command', 'shell', 'shell_command', 'local_shell']);
const readTools = new Set([...shellTools, 'Read']);
const ran = (name, text, pattern) => shellTools.has(name) && pattern.test(text);

const looks = {
  subagent: (name, text) => ['Agent', 'Task', 'Workflow'].includes(name) || /\bspawn_agent\b/.test(text),
  tests: (name, text) => ran(name, text, /go test|test-go\.sh|cargo (test|nextest)|pnpm [^|;&]*(test|e2e)|make (test|check)|scripts\/(vm|land_test|real-app)|wsctl|real-app-harness/),
  diff: (name, text) => ran(name, text, /git (show|diff|log -p)/),
  ci: (name, text) => ran(name, text, /gh (run|pr|api)|test-history/),
  seed: (name, text) => ran(name, text, /attn seed (show|notes)|seeds\/s-/),
};
const lookedAtTheWork = ['subagent', 'tests', 'diff'];
const skillFile = /(?<!skills\/attn\/)(references|principles)\/([a-z-]+)\.md/g;
const skillDir = new URL('../internal/prompts/content/skills/attn-workflow/', import.meta.url);
const skillFiles = Object.fromEntries(['references', 'principles'].map((kind) => [kind, new Set(fs.readdirSync(new URL(kind, skillDir)).map((name) => name.replace(/\.md$/, '')))]));

function skillBody(text) {
  if (text.startsWith('Base directory for this skill:')) {
    const [first, ...rest] = text.split('\n');
    return first.includes('/attn-workflow') ? rest.join('\n').replace(/\n+ARGUMENTS: [^\n]*$/, '').trim() : null;
  }
  let outputs = [text];
  try {
    const parsed = JSON.parse(text);
    if (Array.isArray(parsed)) outputs = parsed.map((item) => item?.text ?? '');
  } catch {}
  for (const output of outputs) {
    const start = output.indexOf('---\nname: attn-workflow\n');
    if (start === -1) continue;
    const end = output.indexOf('\n---\n', start + 4);
    return end === -1 ? null : output.slice(end + 5).trim();
  }
  return null;
}

const shortHash = (text) => createHash('sha256').update(text).digest('hex').slice(0, 12);

function sessionLine(row, hostNames) {
  const line = {
    session: row.id,
    host: row.host ?? 'home',
    ...(hostNames.get(row.host) ? { host_name: hostNames.get(row.host) } : {}),
    agent: row.agent,
    model: mainModel(row.usage),
    role: null,
    seed: null,
    started: null,
    closed: row.closed_at,
    tool_calls: 0,
    cost_usd: Math.round((row.usage?.cost_usd ?? 0) * 100) / 100,
    loaded_skill_at_call: null,
    skill_hash: null,
    references: [],
    principles: [],
    verdicts: [],
    seed_plants: 0,
    discovered_plants: 0,
    seed_withers: 0,
  };
  const references = new Set();
  const principles = new Set();
  let opening = null;
  let window = [];
  let awaitingSkill = false;
  const onEvent = (event) => {
    line.started ??= event.timestamp ?? null;
    if (awaitingSkill && (event.kind === 'user' || event.kind === 'tool_result')) {
      const body = skillBody(event.text ?? '');
      if (body) {
        line.skill_hash = shortHash(body);
        awaitingSkill = false;
      }
    }
    if (event.kind === 'user' && opening === null) {
      opening = event.text ?? '';
      line.role = opening.match(/\nRole: (\w+)\n/)?.[1] ?? null;
      line.seed = opening.match(/\(seed `(s-[a-z0-9]{6})`\)/)?.[1] ?? null;
    }
    if (event.kind !== 'tool_call') return;
    const name = event.tool_name ?? '';
    const text = event.text ?? '';
    line.tool_calls += 1;
    const reads = readTools.has(name);
    const loads = name === 'Skill' ? /attn-workflow/.test(text) : reads && /attn-workflow\/SKILL\.md/.test(text);
    if (loads) {
      line.loaded_skill_at_call ??= line.tool_calls;
      awaitingSkill ||= line.skill_hash === null;
    }
    for (const [, kind, file] of reads ? text.matchAll(skillFile) : []) {
      if (!skillFiles[kind].has(file)) continue;
      (kind === 'references' ? references : principles).add(file);
    }
    const plants = /(?:^|__)seed_plant$/.test(name) || (shellTools.has(name) && /\battn\s+seed\s+plant\b/.test(text) && !/\battn\s+seed\s+plant\b[^\n;|&]*--help/.test(text));
    if (plants) {
      line.seed_plants += 1;
      if (/--discovered-from\b|"discovered_from"\s*:/.test(text)) line.discovered_plants += 1;
    }
    if (/(?:^|__)seed_wither$/.test(name) || (shellTools.has(name) && /\battn\s+seed\s+wither\b/.test(text) && !/\battn\s+seed\s+wither\b[^\n;|&]*--help/.test(text))) {
      line.seed_withers += 1;
    }
    const verdicts = shellTools.has(name) ? [...text.matchAll(/attn seed check (confirm|reject) (s-[a-z0-9]{6})(?: (C\d+))?/g)] : [];
    if (verdicts.length) {
      const looked = Object.keys(looks).filter((kind) => window.some(([n, t]) => looks[kind](n, t)));
      for (const [, verdict, seed, check = null] of verdicts) line.verdicts.push({ seed, check, verdict, at_call: line.tool_calls, looked });
      window = [];
    } else {
      window.push([name, text]);
      if (window.length > reviewWindow) window.shift();
    }
  };
  const finish = () => {
    if (opening === null) throw new Error('the transcript shows no user message yet');
    return Object.assign(line, { references: [...references].sort(), principles: [...principles].sort() });
  };
  return { line, onEvent, finish };
}

function dropPartialLastLine(file) {
  if (fs.existsSync(file) && fs.statSync(file).size !== Buffer.byteLength(wholeLines(file))) fs.writeFileSync(file, wholeLines(file));
}

async function collect(historyFile) {
  dropPartialLastLine(historyFile);
  const history = readHistory(historyFile);
  const known = new Set(history.map((line) => line.session));
  const rows = await ledger();
  const here = await thisHost(rows);
  const hostNames = new Map(history.filter((l) => l.host_name).map((l) => [l.host, l.host_name]));
  if (here) hostNames.set(here.id, here.name);
  const wanted = rows.filter((r) => r.closed_at && ['claude', 'codex'].includes(r.agent) && !known.has(r.id));
  fs.mkdirSync(path.dirname(historyFile), { recursive: true });
  const failures = new Map();
  let appended = 0;
  await pool(wanted, transcriptReaders, async (row) => {
    const { onEvent, finish } = sessionLine(row, hostNames);
    let line;
    try {
      await streamTranscript(row.id, onEvent);
      line = finish();
    } catch (error) {
      failures.set(error.message, (failures.get(error.message) ?? 0) + 1);
      return;
    }
    fs.appendFileSync(historyFile, `${JSON.stringify(line)}\n`);
    appended += 1;
  });
  console.log(`${historyFile}\n${history.length} lines before, ${appended} appended; ${rows.length} ledger rows, ${wanted.length} closed sessions not yet in the file`);
  for (const [reason, count] of [...failures].sort((a, b) => b[1] - a[1])) console.log(`  ${count} not read, retried next run: ${reason}`);
}

function day(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) throw new Error(`not a date: ${value}`);
  return new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate()));
}

const addDays = (date, days) => new Date(date.getTime() + days * 86400000);
const shortDate = (date) => `${date.getUTCDate()} ${date.toLocaleString('en', { month: 'short', timeZone: 'UTC' })}`;

function periods(since, until, by) {
  if (!by) return [[since, until]];
  const step = { day: 1, week: 7 }[by];
  if (!step) throw new Error('--by takes week, day or skill');
  const out = [];
  for (let start = since; start < until; start = addDays(start, step)) out.push([start, new Date(Math.min(addDays(start, step), until))]);
  return out;
}

const percent = (part, whole) => (whole ? `${Math.round((100 * part) / whole)}%` : '-');
const median = (values) => {
  if (!values.length) return '-';
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return String(sorted.length % 2 ? sorted[middle] : Math.round((sorted[middle - 1] + sorted[middle]) / 2));
};

async function everyNote(seed, kind = 'note') {
  const sinceStart = await attnJSON('seed', 'notes', seed, '--since', '0', '--kind', kind);
  if (!sinceStart.expired) return { notes: sinceStart.notes.filter((n) => n.seed_id === seed), keptSince: null };
  const kept = await attnJSON('seed', 'notes', seed, '--since', String(sinceStart.expired.earliest), '--kind', kind);
  return { notes: kept.notes.filter((n) => n.seed_id === seed), keptSince: new Date(Date.now() - sinceStart.expired.retention_seconds * 1000) };
}

const writtenWithin = (since, until) => (n) => new Date(n.created_at) >= since && new Date(n.created_at) < until;

async function stepNotes(title, slug, since, until) {
  const search = await attnJSON('seed', 'search', title, '--limit', '1000');
  const seeds = search.hits.map((hit) => hit.seed).filter((seed) => seed.step_slug === slug);
  const found = { notes: [], keptSince: null };
  for (const seed of seeds) {
    const { notes, keptSince } = await everyNote(seed.id);
    found.notes.push(...notes.filter(writtenWithin(since, until)));
    if (keptSince && keptSince > since) found.keptSince = keptSince;
  }
  return found;
}

const findingLines = (body) => [...body.matchAll(/^finding: ([0-9a-f]{7,40}) (s-[a-z0-9]{6}|-) (P[01]) .*$/gm)];

async function gardenFacts(builderSeeds, since, until) {
  const notes = await pool([...builderSeeds], gardenReaders, async (seed) => {
    try {
      return { seed, notes: (await attnJSON('seed', 'notes', seed)).notes };
    } catch {
      return { seed, notes: null };
    }
  });
  const facts = { rejected: 0, rereported: 0, unread: 0 };
  for (const { notes: log } of notes) {
    if (!log) { facts.unread += 1; continue; }
    const reports = new Map();
    for (const note of log.filter((n) => n.kind === 'check')) {
      const [, check, move] = note.body.match(/^(C\d+) (reported|rejected)/) ?? [];
      if (move === 'reported') reports.set(check, (reports.get(check) ?? 0) + 1);
      if (move === 'rejected') facts.rejected += 1;
    }
    if ([...reports.values()].some((count) => count > 1)) facts.rereported += 1;
  }
  const review = await stepNotes('Review main', reviewMainSlug, since, until);
  const findings = review.notes.flatMap((note) => findingLines(note.body).map(([, sha, target, severity]) => ({ sha, target, severity })));
  facts.reviewNotes = review.notes.length;
  if (review.keptSince) facts.findingsKeptSince = review.keptSince;
  facts.findings = findings.length;
  facts.findingSeeds = new Set(findings.map((f) => f.target).filter((t) => t !== '-')).size;
  return facts;
}

async function escapes(history, since, until) {
  const dateOf = (n) => n.created_at.slice(0, 10);
  const indent = (body) => body.trim().split('\n').map((line) => `    ${line}`).join('\n');
  const builderSeeds = [...new Set(history
    .filter((l) => l.role === 'Builder' && l.seed && new Date(l.started) < until && new Date(l.closed ?? until) >= since)
    .map((l) => l.seed))];
  const [review, sheriff, checkLogs] = await Promise.all([
    stepNotes('Review main', reviewMainSlug, since, until),
    stepNotes('Sheriff', sheriffSlug, since, until),
    pool(builderSeeds, gardenReaders, async (seed) => {
      try {
        return (await everyNote(seed, 'check')).notes;
      } catch {
        return null;
      }
    }),
  ]);
  const unread = checkLogs.filter((log) => !log).length;
  const rejections = checkLogs.filter(Boolean).flat().filter((n) => /^C\d+ rejected/.test(n.body)).filter(writtenWithin(since, until));
  const findings = review.notes.flatMap((note) => findingLines(note.body).map(([line]) => `  ${dateOf(note)} ${line}  (review ${note.seed_id})`));

  console.log(`Escapes from ${since.toISOString().slice(0, 10)} to ${until.toISOString().slice(0, 10)}`);
  console.log(`\nfindings from review on main: ${findings.length}`);
  for (const line of findings) console.log(line);
  if (review.keptSince) console.log(`  the garden keeps review on main's notes only since ${review.keptSince.toISOString().slice(0, 10)}`);
  console.log(`\nrejected checks on ${builderSeeds.length} Builder seeds: ${rejections.length}`);
  for (const note of rejections) console.log(`  ${dateOf(note)} ${note.seed_id}\n${indent(note.body)}`);
  if (unread) console.log(`  ${unread} Builder seeds could not be read from the garden`);
  console.log(`\nSheriff notes, for a red main traced to a commit: ${sheriff.notes.length}`);
  for (const note of sheriff.notes) console.log(`  ${dateOf(note)} ${note.seed_id}\n${indent(note.body)}`);
  if (sheriff.keptSince) console.log(`  the garden keeps the Sheriff's notes only since ${sheriff.keptSince.toISOString().slice(0, 10)}`);
}

function matchesHost(line, wanted) {
  if (!wanted) return true;
  return line.host === wanted || line.host_name === wanted || line.host.startsWith(wanted);
}

function hostLabel(line, names) {
  return line.host === 'home' ? 'home' : names.get(line.host) ?? line.host.slice(0, 8);
}

const startedWithin = (since, until) => (l) => l.started && new Date(l.started) >= since && new Date(l.started) < until;

async function scorecard(inPeriod, title, since, until, names) {
  const builders = inPeriod.filter((l) => l.role === 'Builder');
  const orchestrators = inPeriod.filter((l) => l.role === 'Orchestrator');
  const garden = await gardenFacts(new Set(builders.map((l) => l.seed).filter(Boolean)), since, until);

  const loaded = (set) => set.filter((l) => l.loaded_skill_at_call !== null);
  const bySize = sizeBounds.reduce((buckets, bound, index) => {
    const low = sizeBounds[index - 1] ?? 0;
    buckets.push(builders.filter((l) => l.tool_calls >= low && l.tool_calls < bound));
    return buckets;
  }, []).concat([builders.filter((l) => l.tool_calls >= sizeBounds.at(-1))]);
  const openedPrinciple = (set) => percent(loaded(set).filter((l) => l.principles.length).length, loaded(set).length);
  const confirmations = new Map();
  for (const l of orchestrators) {
    const calls = Map.groupBy(l.verdicts, (v) => v.at_call);
    for (const [at, verdicts] of calls) {
      if (verdicts.every((v) => v.verdict === 'confirm')) confirmations.set(`${l.session}:${at}`, verdicts[0].looked);
    }
  }
  const afterLook = [...confirmations.values()].filter((looked) => looked.some((k) => lookedAtTheWork.includes(k))).length;
  const count = (set, key) => {
    const tally = new Map();
    for (const l of set) tally.set(key(l), (tally.get(key(l)) ?? 0) + 1);
    return [...tally].sort((a, b) => b[1] - a[1]).map(([k, n]) => `${k} ${n}`).join(' · ') || '-';
  };
  const cost = (set) => `$${Math.round(set.reduce((sum, l) => sum + l.cost_usd, 0))}`;
  const seedMoves = (set) => {
    const tracked = set.filter((l) => typeof l.seed_plants === 'number');
    if (!tracked.length) return '-';
    const planted = tracked.reduce((sum, l) => sum + l.seed_plants, 0);
    const withered = tracked.reduce((sum, l) => sum + (l.seed_withers ?? 0), 0);
    return `${planted} / ${withered}`;
  };
  const discoveredPlants = (set) => {
    const tracked = set.filter((l) => typeof l.discovered_plants === 'number');
    return tracked.length ? tracked.reduce((sum, l) => sum + l.discovered_plants, 0) : '-';
  };

  const rows = [
    [title, 'Builders', 'Orchestrators'],
    ['sessions', builders.length, orchestrators.length],
    ['loaded attn-workflow', percent(loaded(builders).length, builders.length), percent(loaded(orchestrators).length, orchestrators.length)],
    [`  under ${sizeBounds[0]} calls / ${sizeBounds[0]}-${sizeBounds[1] - 1} / ${sizeBounds[1]}+`, bySize.map((set) => percent(loaded(set).length, set.length)).join(' / '), ''],
    ['opened a principle file, of loaded', openedPrinciple(builders), openedPrinciple(orchestrators)],
    ['confirmations after a look', '', `${percent(afterLook, confirmations.size)} of ${confirmations.size}`],
    ['checks rejected', '', garden.rejected],
    ['seeds with a check reported twice', garden.rereported, ''],
    ['seeds planted / withered', seedMoves(builders), seedMoves(orchestrators)],
    ['  discovered-from planted', discoveredPlants(builders), discoveredPlants(orchestrators)],
    ['findings from review on main', `${garden.findingSeeds} seeds`, ''],
    ['median tool calls per session', median(builders.map((l) => l.tool_calls)), median(orchestrators.map((l) => l.tool_calls))],
    ['cost', cost(builders), cost(orchestrators)],
  ];
  const width = Math.max(...rows.map((r) => r[0].length)) + 2;
  for (const [label, a, b] of rows) console.log(`${label.padEnd(width)}${String(a).padStart(14)}${String(b).padStart(16)}`);
  console.log(`all sessions          ${inPeriod.length}: ${count(inPeriod, (l) => l.role ?? 'no role')}`);
  console.log(`  by host             ${count(inPeriod, (l) => hostLabel(l, names))}`);
  console.log(`  by harness          ${count(inPeriod, (l) => l.agent)}`);
  console.log(`  review on main      ${garden.reviewNotes} notes read for findings`);
  if (garden.unread) console.log(`  ${garden.unread} Builder seeds could not be read from the garden`);
  if (garden.findingsKeptSince) console.log(`  the garden keeps review on main's notes only since ${garden.findingsKeptSince.toISOString().slice(0, 10)}; findings before then are not counted`);
  console.log('');
}

const { values: options, positionals } = parseArgs({
  allowPositionals: true,
  options: {
    since: { type: 'string' },
    until: { type: 'string' },
    by: { type: 'string' },
    host: { type: 'string' },
    history: { type: 'string' },
    help: { type: 'boolean', short: 'h' },
  },
});
if (options.help || positionals.length > 1 || (positionals[0] && !['collect', 'escapes'].includes(positionals[0]))) {
  console.error(usage);
  process.exit(options.help ? 0 : 2);
}
const historyFile = options.history ?? await defaultHistoryPath();
if (positionals[0] === 'collect') {
  await collect(historyFile);
} else {
  const until = options.until ? day(options.until) : day(new Date());
  const since = options.since ? day(options.since) : addDays(until, -7);
  const history = readHistory(historyFile);
  const names = new Map(history.filter((l) => l.host_name).map((l) => [l.host, l.host_name]));
  const lines = history.filter((l) => matchesHost(l, options.host));
  const onHost = options.host ? ` on ${options.host}` : '';
  const span = (start, end) => `${shortDate(start)} to ${shortDate(addDays(end, -1))}`;
  if (positionals[0] === 'escapes') {
    await escapes(history, since, until);
  } else if (options.by === 'skill') {
    const bySkill = Map.groupBy(lines.filter(startedWithin(since, until)), (l) => l.skill_hash);
    const judged = [...bySkill].filter(([, group]) => group.some((l) => l.role === 'Builder' || l.role === 'Orchestrator'));
    for (const [hash, group] of judged.sort(([, a], [, b]) => a.map((l) => l.started).sort()[0].localeCompare(b.map((l) => l.started).sort()[0]))) {
      const started = group.map((l) => l.started).sort();
      const title = `${hash ? `skill ${hash}` : 'no skill text'}, ${started[0].slice(0, 10)} to ${started.at(-1).slice(0, 10)}${onHost}`;
      await scorecard(group, title, since, until, names);
    }
  } else {
    for (const [start, end] of periods(since, until, options.by)) {
      await scorecard(lines.filter(startedWithin(start, end)), `${span(start, end)}${onHost}`, start, end, names);
    }
  }
}
