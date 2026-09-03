#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createRunContext,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, profileCliEnv } from './harnessProfile.mjs';
import {
  preTrustClaudeFolder,
  ensureClaudePromptReadyViaPty,
  ensureCodexPromptReadyViaPty,
} from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const PROMPTS = {
  claude:
    'hey can you get someone going on a quick CHANGELOG audit? skim the last couple ' +
    'weeks of entries and flag any that read like they were written for maintainers ' +
    'instead of users, or that are just vague. have them list the worst ones with a ' +
    'suggested rewrite. stepping out, keep me posted',
  codex:
    'morning — can you hand a small thing off to someone: go through the README ' +
    'quickstart and check every command still exists in the CLI (nothing renamed or ' +
    'dropped). just want a list of anything stale. i\'m in meetings most of the day, ' +
    'ping me when there\'s something to look at',
};

const CHIEF_MODELS = {
  claude: 'opus',
  codex: 'gpt-5.5',
};

const NO_WATCH_PROMPTS = {
  claude:
    'hey can you get someone going on a quick CHANGELOG audit? skim the last couple ' +
    'weeks of entries and flag any that read like they were written for maintainers ' +
    'instead of users, or that are just vague. have them list the worst ones with a ' +
    "suggested rewrite. don't bother setting up any watch or monitor on it — just hand " +
    "it off and i'll check back myself. stepping out",
  codex:
    'morning — can you hand a small thing off to someone: go through the README ' +
    'quickstart and check every command still exists in the CLI (nothing renamed or ' +
    "dropped). just want a list of anything stale. don't set up any watch on it, i'll " +
    "circle back myself. i'm in meetings most of the day",
};

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  let agent = 'claude';
  let noWatch = false;
  const rest = [];
  for (let i = 0; i < args.length; i += 1) {
    if (args[i] === '--agent') agent = args[++i];
    else if (args[i] === '--no-watch') noWatch = true;
    else rest.push(args[i]);
  }
  const options = parseCommonArgs(rest);
  return { options, agent, noWatch, help: args.includes('--help') || args.includes('-h') };
}

function assert(condition, message) {
  if (!condition) throw new Error(`Assertion failed: ${message}`);
}

const delay = (ms) => new Promise((r) => setTimeout(r, ms));

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 500) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  return null;
}

function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    const env = profileCliEnv(profile);
    delete env.ATTN_SESSION_ID;
    delete env.ATTN_WRAPPER_PATH;
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env,
    });
    const brace = stdout.indexOf('{');
    return { stdout, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
  };
}

function shell(cmd) {
  try {
    return execFileSync('bash', ['-lc', cmd], { encoding: 'utf8' });
  } catch (error) {
    return error.stdout ? String(error.stdout) : '';
  }
}

// The agent's own launch process carries `ticket inbox --watch` inside its
// guidance blob; the greps below exclude it from a genuine watch invocation.
function watchProcessLines() {
  return shell(
    `ps -Awwo pid=,command= | grep -- 'ticket inbox --watch'` +
    ` | grep -v 'arm a harness Monitor' | grep -v 'append-system-prompt'` +
    ` | grep -v 'developer_instructions' | grep -v grep`,
  ).split('\n').map((l) => l.trim()).filter(Boolean);
}

const pidOf = (line) => line.split(/\s+/)[0];

function freshWatchProcesses(baselinePids) {
  return watchProcessLines().filter((l) => !baselinePids.has(pidOf(l))).join('\n');
}

function chiefGuidanceProcesses() {
  const marker = "Rely on attn's ticket nudges";
  return shell(`ps -Awwo pid=,command= | grep -- '${marker}' | grep -v grep`).trim();
}

async function setChiefOfStaff(client, sessionId, want) {
  const before = await client.request('chief_of_staff_get_state');
  const isChief = Boolean(before.sessions.find((s) => s.id === sessionId)?.chiefOfStaff);
  if (isChief === want) return;
  await client.request('chief_of_staff_open_actions', { sessionId });
  await client.request('chief_of_staff_toggle');
  const afterToggle = await client.request('chief_of_staff_get_state');
  if (want && afterToggle.transferPrompt) await client.request('chief_of_staff_confirm_transfer');
  const ok = await pollFor(
    async () => {
      const state = await client.request('chief_of_staff_get_state');
      return Boolean(state.sessions.find((s) => s.id === sessionId)?.chiefOfStaff) === want ? state : null;
    },
    `session ${sessionId} chief=${want}`,
    15_000,
  );
  assert(ok, `chief role set to ${want} for ${sessionId}`);
}

async function clearAnyChief(client) {
  const state = await client.request('chief_of_staff_get_state').catch(() => ({ sessions: [] }));
  const chief = (state.sessions || []).find((s) => s.chiefOfStaff);
  if (!chief) return null;
  await setChiefOfStaff(client, chief.id, false);
  return chief.id;
}

async function readChiefPane(client, chiefId) {
  const pane = await waitForFirstWorkspacePane(client, chiefId, `chief pane ${chiefId}`, 20_000);
  const res = await client.request('read_pane_text', { sessionId: chiefId, paneId: pane.paneId }, { timeoutMs: 20_000 }).catch(() => null);
  return { paneId: pane.paneId, text: res?.text || '' };
}

async function main() {
  const { options, agent, noWatch, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-chief-ticket-watch.mjs');
    console.log('\n  --agent claude|codex   which agent the chief runs as (default claude)');
    return;
  }
  assert(agent === 'claude' || agent === 'codex', `--agent must be claude or codex (got ${agent})`);

  const profile = currentHarnessProfile();
  if (!profile) throw new Error('this benchmark never runs against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);
  // `ticket list --json` prints an array; runAttn's own parse looks for an object.
  const ticketBoard = () => {
    try {
      const { stdout } = runAttn(['ticket', 'list', '--json']);
      return JSON.parse(stdout.slice(stdout.indexOf('[')));
    } catch {
      return [];
    }
  };

  const { runId, runDir, sessionDir } = createRunContext(options, `chief-watch-${agent}`);

  const repoDir = path.join(sessionDir, 'chief-repo');
  fs.mkdirSync(repoDir, { recursive: true });
  fs.writeFileSync(path.join(repoDir, 'CHANGELOG.md'),
    '# Changelog\n\n## [2026-06-28]\n- Refactored the FooManager to use the new BarAdapter interface.\n' +
    '- Fixed a bug.\n- Bumped internal protocol to v3 and migrated the store schema.\n\n' +
    '## [2026-06-27]\n- Users can now pin workspaces so they stay in the sidebar when empty.\n' +
    '- Various improvements.\n', 'utf8');
  fs.writeFileSync(path.join(repoDir, 'README.md'),
    '# demo\n\n## Quickstart\n\n```\nattn list\nattn delegate --brief "..."\nattn dispatch update\nattn ticket status ready_for_review\n```\n', 'utf8');
  execFileSync('git', ['init', '-q'], { cwd: repoDir });
  execFileSync('git', ['add', '-A'], { cwd: repoDir });
  execFileSync('git', ['commit', '-q', '-m', 'seed'], {
    cwd: repoDir,
    env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
  });
  if (agent === 'claude') preTrustClaudeFolder(repoDir);

  const ensureReady = agent === 'claude' ? ensureClaudePromptReadyViaPty : ensureCodexPromptReadyViaPty;
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let chiefId = null;
  let workerId = null;
  const evidence = { runId, profile, agent, steps: [] };
  const note = (m, extra) => { console.log(`[chief-watch] ${m}`); evidence.steps.push({ t: Date.now(), m, ...extra }); };
  const saveEvidence = (verdict) => {
    evidence.verdict = verdict;
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
  };
  const dumpPane = async (name) => {
    if (!chiefId) return;
    const { text } = await readChiefPane(client, chiefId).catch(() => ({ text: '' }));
    fs.writeFileSync(path.join(runDir, `${name}.txt`), text, 'utf8');
    return text;
  };

  console.log(`[chief-watch] profile=${profile} agent=${agent} runDir=${runDir} repo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    const leftover = await clearAnyChief(client);
    if (leftover) note(`demoted leftover chief from a prior run`, { leftover });

    await client.request('set_setting', { key: 'auto_approve_enabled', value: 'true' });
    note('enabled auto_approve_enabled for the benchmark');

    const chiefModel = CHIEF_MODELS[agent] || '';
    if (chiefModel) {
      await client.request('set_setting', { key: `chief_model_${agent}`, value: chiefModel });
      note(`pinned chief model: ${agent}=${chiefModel}`);
    }

    const created = await client.request('create_session', {
      cwd: repoDir,
      label: `chief-${runId.slice(-6)}`,
      agent,
      chief_of_staff: true,
    });
    chiefId = created.sessionId;
    await observer.waitForSession({ id: chiefId, timeoutMs: 30_000 });
    note(`chief session created (create-as-chief)`, { chiefId });
    await ensureReady(client, chiefId, 90_000);
    note(`chief agent booted to prompt`);

    const roleState = await pollFor(
      async () => {
        const state = await client.request('chief_of_staff_get_state').catch(() => null);
        return state?.sessions?.find((s) => s.id === chiefId)?.chiefOfStaff ? state : null;
      },
      'daemon to hold the chief role for the new session',
      20_000,
    );
    evidence.daemonHoldsRole = Boolean(roleState);
    if (!roleState) {
      await dumpPane('00-chief-no-role');
      saveEvidence('setup-failed-no-role');
      throw new Error('SETUP FAILED: the daemon did not assign the chief role to the new session (create-as-chief was skipped — likely a stale chief still holds the role; reset the profile and re-run).');
    }

    await pollFor(
      () => Boolean(chiefGuidanceProcesses()),
      'chief agent launched with ChiefGuidance',
      30_000,
    );
    const guidanceProc = chiefGuidanceProcesses();
    evidence.guidanceProcess = guidanceProc;
    if (!guidanceProc) {
      await dumpPane('00-chief-no-guidance');
      saveEvidence('setup-failed-no-guidance');
      throw new Error(`SETUP FAILED: ${agent} chief was not launched with its runtime-specific ChiefGuidance. The create-as-chief role-set did not reach the launch path.`);
    }
    note(`role + guidance verified at first launch`);
    const readyPane = (await dumpPane('01-chief-ready-with-guidance')) || '';

    if (chiefModel) {
      if (!new RegExp(chiefModel, 'i').test(readyPane)) {
        console.log('\n=== SETUP FAILED: chief model did not pin ===');
        console.log(readyPane.split('\n').slice(-12).join('\n'));
        saveEvidence('setup-model-not-pinned');
        throw new Error(`SETUP FAILED: chief was not launched on the pinned model "${chiefModel}" (status line never mentions it); the chief_model_${agent} setting did not reach --model.`);
      }
      note(`chief model pinned: ${chiefModel}`);
    }

    const baselineWatchPids = new Set(watchProcessLines().map(pidOf));

    const prompt = (noWatch ? NO_WATCH_PROMPTS : PROMPTS)[agent];
    const pane = await waitForFirstWorkspacePane(client, chiefId, `chief pane ${chiefId}`, 20_000);
    // Claude's TUI reads a fast burst ending in CR as a bracketed paste, so the
    // Enter has to be a separate write a beat later.
    await client.request('write_pane', { sessionId: chiefId, paneId: pane.paneId, text: prompt, submit: false });
    await delay(1_200);
    await client.request('write_pane', { sessionId: chiefId, paneId: pane.paneId, text: '\r', submit: false });
    note(`human prompt sent`, { prompt });

    const chiefState = () => observer.getSession(chiefId)?.state || 'unknown';
    evidence.stateBeforePrompt = chiefState();

    const started = await pollFor(
      () => (chiefState() === 'working' ? true : null),
      'chief to start working after the prompt',
      30_000,
      500,
    );
    if (!started) {
      await dumpPane('02-prompt-not-accepted');
      evidence.chiefState = chiefState();
      saveEvidence('prompt-not-accepted');
      console.log('\n=== HARNESS ISSUE: chief never started working after the prompt ===');
      console.log(`chief state: ${chiefState()} (expected to pass through "working")`);
      console.log('Setup/timing problem, not a behavioral finding. Re-run.');
      return;
    }
    note(`chief started working (prompt accepted)`);

    const baselineIds = new Set(ticketBoard().map((tk) => tk.id));
    note(`ticket baseline captured`, { existing: baselineIds.size });

    let delegation = null;
    let armedWatch = '';
    const observeUntil = Date.now() + 240_000;
    let snap = 0;
    while (Date.now() < observeUntil && !delegation) {
      const bound = ticketBoard().find((tk) => !baselineIds.has(tk.id) && tk.assignee && tk.assignee !== chiefId);
      if (bound) { delegation = bound; break; }
      const w = freshWatchProcesses(baselineWatchPids);
      if (w && !armedWatch) { armedWatch = w; note(`watch armed`, { processes: w.split('\n').length }); }
      if (snap % 6 === 0) await dumpPane(`02-observe-${String(snap).padStart(2, '0')}`);
      snap += 1;
      await delay(2_500);
    }
    if (!armedWatch) armedWatch = freshWatchProcesses(baselineWatchPids);
    evidence.armedWatch = armedWatch;
    evidence.delegated = Boolean(delegation);

    const chiefText = await dumpPane('03-chief-after-observe');

    if (!delegation) {
      const finalState = chiefState();
      evidence.chiefState = finalState;
      if (finalState === 'pending_approval') {
        note(`observe window elapsed with chief BLOCKED on approval`, { finalState });
        saveEvidence('blocked-on-approval');
        console.log('\n=== BLOCKED: chief is stuck on a permission-approval prompt ===');
        console.log('Not a behavioral finding — the chief never got to decide. A human (or');
        console.log('yolo mode) would unblock it. Decide the permission posture, then re-run.');
        console.log('--- chief pane (tail) ---');
        console.log(chiefText.split('\n').slice(-40).join('\n'));
        return;
      }
      if (finalState === 'working' || finalState === 'launching') {
        note(`observe window elapsed while chief STILL WORKING`);
        saveEvidence('inconclusive-still-working');
        console.log('\n=== INCONCLUSIVE: chief still working at the deadline ===');
        console.log('Not a finding — the chief never finished. Bump the window or re-run.');
        console.log('--- chief pane (tail) ---');
        console.log(chiefText.split('\n').slice(-40).join('\n'));
        return;
      }
      note(`chief FINISHED without delegating`, { finalState });
      saveEvidence('did-not-delegate');
      console.log('\n=== VERDICT: chief did NOT delegate ===');
      console.log(`chief final state: ${finalState}`);
      console.log(`armed watch: ${armedWatch ? 'YES' : 'no'}`);
      console.log('--- chief pane (tail) ---');
      console.log(chiefText.split('\n').slice(-40).join('\n'));
      console.log('\nStopping for discussion (not auto-deciding the next step).');
      return;
    }

    workerId = delegation.assignee;
    const ticketId = delegation.id;
    note(`DELEGATED`, { workerId, ticketId, armedWatch: Boolean(armedWatch) });
    await observer.waitForSession({ id: workerId, timeoutMs: 30_000 }).catch(() => {});

    if (noWatch) {
      const chiefEligible = () => chiefState() !== 'pending_approval';

      const eligible = await pollFor(() => (chiefEligible() ? true : null), 'chief to leave pending approval (no-watch)', 120_000, 1_500);
      if (!eligible) {
        await dumpPane('06-nowatch-pending-approval');
        note(`chief remained pending approval in the no-watch window`, { finalState: chiefState() });
        saveEvidence('nowatch-blocked-on-approval');
        console.log('\n=== INCONCLUSIVE: chief remained at an approval prompt ===');
        return;
      }
      await client.request('select_session', { sessionId: workerId });
      note(`selected worker so the chief's shared nudge countdown can run`);
      const strayWatch = freshWatchProcesses(baselineWatchPids);
      if (strayWatch) {
        note(`chief ARMED A WATCH despite the no-watch prompt`, { processes: strayWatch.split('\n').length });
      } else {
        note(`chief is nudge-eligible with no watch armed`);
      }

      runAttn(['ticket', 'status', 'ready_for_review', '--comment', 'Audit done — 3 entries flagged, rewrites in the report.', '--session', workerId]);
      note(`worker reported ready_for_review`);

      const nudgeUntil = Date.now() + 90_000;
      let nudged = null;
      let reactedNw = null;
      let nsnap = 0;
      while (Date.now() < nudgeUntil) {
        const text = await dumpPane(`06-nudge-${String(nsnap).padStart(2, '0')}`);
        if (!nudged && /New ticket activity/i.test(text)) { nudged = text; note(`shared daemon nudge delivered to chief`); }
        if (nudged && /ticket inbox|ready[ _]for[ _]review|in review|review the|audit/i.test(text.split('\n').slice(-25).join('\n'))) { reactedNw = text; break; }
        nsnap += 1;
        await delay(3_000);
      }
      evidence.sharedNudgeDelivered = Boolean(nudged);
      evidence.reacted = Boolean(reactedNw);
      evidence.strayWatch = Boolean(strayWatch);
      const finalText = await dumpPane('07-chief-final-nowatch');

      const verdict = !nudged
        ? (strayWatch ? 'nowatch-chief-self-armed-watch' : 'shared-nudge-not-delivered')
        : (reactedNw ? 'shared-nudge-and-reacted' : 'shared-nudge-no-reaction');
      saveEvidence(verdict);
      console.log(`\n=== VERDICT: ${verdict} ===`);
      console.log(`delegated: yes (worker=${workerId} ticket=${ticketId})`);
      console.log(`chief armed its own watch despite no-watch prompt: ${strayWatch ? 'YES' : 'no'}`);
      console.log(`shared daemon nudge delivered: ${nudged ? 'YES' : 'no'}`);
      console.log(`visible reaction to the nudge: ${reactedNw ? 'YES' : 'no'}`);
      console.log('--- chief pane (tail) ---');
      console.log(finalText.split('\n').slice(-45).join('\n'));
      return;
    }

    const chiefEligible = () => chiefState() !== 'pending_approval';
    const settled = await pollFor(() => {
      if (!armedWatch) armedWatch = freshWatchProcesses(baselineWatchPids);
      return chiefEligible() ? true : null;
    }, 'chief to leave pending approval', 120_000, 1_500);
    if (!settled) {
      await dumpPane('04-chief-pending-approval');
      evidence.armedWatch = armedWatch;
      evidence.chiefState = chiefState();
      saveEvidence('inconclusive-chief-pending-approval');
      console.log('\n=== INCONCLUSIVE: chief remained at an approval prompt ===');
      console.log(`chief state: ${chiefState()}`);
      console.log(`armed watch: ${armedWatch ? 'YES' : 'no'}`);
      return;
    }
    await client.request('select_session', { sessionId: workerId });
    note(`selected worker so the chief's shared nudge countdown can run`);

    await delay(2_000);
    runAttn(['ticket', 'status', 'ready_for_review', '--comment', 'Audit done — 3 entries flagged, rewrites in the report.', '--session', workerId]);
    note(`worker reported ready_for_review`);

    const reactUntil = Date.now() + 240_000;
    let reacted = null;
    let nudged = null;
    let rsnap = 0;
    while (Date.now() < reactUntil) {
      const text = await dumpPane(`04-react-${String(rsnap).padStart(2, '0')}`);
      const recent = text.split('\n').slice(-35).join('\n');
      if (!nudged && /New ticket activity/i.test(text)) {
        nudged = text;
        note(`shared daemon ticket nudge delivered to chief`);
      }
      const handledUpdate = /ticket inbox|ready[ _]for[ _]review|in review|review the report/i.test(recent);
      if (!reacted && handledUpdate && (armedWatch || nudged)) {
        reacted = text;
      }
      if (!armedWatch) {
        const w = freshWatchProcesses(baselineWatchPids);
        if (w) {
          armedWatch = w;
          note(`optional watch armed (post-delegation)`, { processes: w.split('\n').length });
        }
      }
      if (reacted) break;
      rsnap += 1;
      await delay(3_000);
    }
    evidence.armedWatch = armedWatch;
    evidence.nudged = Boolean(nudged);
    evidence.reacted = Boolean(reacted);
    const finalText = await dumpPane('05-chief-final');

    const verdict = reacted
      ? 'delegated-and-reacted'
      : (armedWatch ? 'watch-no-visible-reaction' : 'shared-nudge-not-delivered');
    saveEvidence(verdict);
    console.log(`\n=== VERDICT: ${evidence.verdict} ===`);
    console.log(`delegated: yes (worker=${workerId} ticket=${ticketId})`);
    console.log(`armed watch: ${armedWatch ? 'YES' : 'no'}`);
    console.log(`daemon nudge delivered: ${nudged ? 'YES' : 'no'}`);
    console.log(`visible reaction to the report: ${reacted ? 'YES' : 'no'}`);
    console.log('--- chief pane (tail) ---');
    console.log(finalText.split('\n').slice(-45).join('\n'));
  } catch (error) {
    saveEvidence(evidence.verdict || 'error');
    throw error;
  } finally {
    if (workerId) await client.request('close_session', { sessionId: workerId }).catch(() => {});
    if (chiefId) {
      await setChiefOfStaff(client, chiefId, false).catch(() => {});
      await client.request('close_session', { sessionId: chiefId }).catch(() => {});
    }
    await client.request('set_setting', { key: 'auto_approve_enabled', value: 'false' }).catch(() => {});
    if (CHIEF_MODELS[agent]) {
      await client.request('set_setting', { key: `chief_model_${agent}`, value: '' }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
    console.log(`[chief-watch] artifacts in ${runDir}`);
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
