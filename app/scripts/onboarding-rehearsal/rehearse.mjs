#!/usr/bin/env node
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ensureFreshWorld } from '../real-app-harness/freshWorld.mjs';
import {
  appLocalDataDirForInstance,
  bundleIdentifierForInstance,
  dataDirForInstance,
  defaultAppPathForInstance,
} from '../real-app-harness/harnessInstance.mjs';
import { appDaemonInTree } from '../real-app-harness/platform.mjs';

const INSTANCE = 'onboarding';
const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const ROOT = path.join(os.homedir(), 'attn-onboarding-rehearsal');
const WORLDS = path.join(ROOT, 'worlds');
const NOTEBOOK = '/Users/Shared/attn-onboarding-notebook';
const WORK_NOTEBOOK = `${NOTEBOOK}-work`;
const TOOL_HOME = path.join(ROOT, 'tool-home');
const CLAUDE_HOME = path.join(TOOL_HOME, '.claude');
const CODEX_HOME = path.join(TOOL_HOME, '.codex');
const CLAUDE_LOGIN_STATE = path.join(ROOT, 'claude-login-state.json');
const LAST_RUNS = path.join(ROOT, 'last-runs.json');
const APP_PATH = defaultAppPathForInstance(INSTANCE);
const ATTN = appDaemonInTree(APP_PATH);
const HARNESS_ENV = /^(ATTN_|CLAUDE|CODEX_|ANTHROPIC_)/;
const UNAVAILABLE_HARNESS = path.join(ROOT, 'harness-not-installed');

const SCENARIOS = [
  {
    name: 'new-claude',
    title: 'New install on Claude Code',
    expect: 'full path',
    world: 'empty',
    try: 'Pick Claude Code in the Chief slot.',
  },
  {
    name: 'new-codex',
    title: 'New install on Codex',
    expect: 'full path',
    world: 'empty',
    try: 'Pick Codex in the Chief slot.',
  },
  {
    name: 'no-harness',
    title: 'No harness installed',
    expect: 'the slot says so and how to fix it; no Chief',
    world: 'empty',
    hide: ['claude', 'codex'],
    try: 'Open the Chief slot.',
  },
  {
    name: 'one-harness',
    title: 'Only one harness available',
    expect: 'full path; the Chief asks whether you expect another',
    world: 'empty',
    hide: ['codex'],
    try: 'Pick Claude Code in the Chief slot.',
  },
  {
    name: 'used',
    title: 'Used attn before, never had a Chief',
    expect: 'tour',
    world: 'used',
    try: 'Pick a harness in the Chief slot.',
  },
  {
    name: 'upgrader',
    title: "Upgrader whose profile has today's Chief session",
    expect: 'tour, prefilled, names the old Chief',
    world: 'upgrader',
    try: 'Wake the Chief from its slot.',
  },
  {
    name: 'second-profile',
    title: 'Second profile on a used install',
    expect: 'tour',
    world: 'used',
    prepare: secondProfile,
    profile: 'Work',
    try: 'Switch to the Work profile, then pick a harness in its Chief slot.',
  },
  {
    name: 'roles-set',
    title: 'Delegation roles already set up',
    expect: 'skips the roles step',
    world: 'empty',
    prepare: (attn) => attn('delegate', 'roles', 'install', '-m', 'Rehearsal: roles already set up'),
    try: 'Pick a harness in the Chief slot.',
  },
  {
    name: 'analytics-on',
    title: 'Analytics sharing already on',
    expect: "says so and skips to what's off",
    world: 'empty',
    prepare: (attn) => {
      attn('settings', 'set', 'analytics.enabled', 'true');
      attn('settings', 'set', 'analytics.sharing_enabled', 'true');
    },
    try: 'Pick a harness in the Chief slot, then skip ahead to helping the maintainers.',
  },
  {
    name: 'resume',
    title: 'Resuming a half-finished onboarding after /clear or sleep',
    expect: "carries on from what's left",
    world: 'empty',
    try: 'Pick a harness, get past the name step, then run /clear, or put the Chief to sleep and wake it.',
  },
  {
    name: 'again',
    title: 'Starting over with `attn onboarding again`',
    expect: 'full path',
    world: 'empty',
    prepare: finishedThenAgain,
    try: 'Wake the Chief from its slot.',
  },
];

const WORLD_BUILDERS = {
  empty: async () => {},
  used: async (attn, wsctl) => {
    addSession(wsctl, 'shell');
  },
  upgrader: async (attn, wsctl) => {
    wsctl('set-chief', '--id', addSession(wsctl, 'claude'));
  },
};

function secondProfile(attn, wsctl) {
  attn('crew', 'set', 'chief', '--agent', 'claude', '--model', 'sonnet');
  attn('onboarding', 'done');
  wsctl('request', '--json', JSON.stringify({ cmd: 'profile_create', request_id: 'rehearsal-work', name: 'Work' }), '--event', 'profile_action_result');
  attn('settings', 'set', 'notebook.root', WORK_NOTEBOOK, '--profile', 'Work');
  attn('crew', 'move-home', 'chief', '--profile', 'Work');
}

function finishedThenAgain(attn) {
  attn('crew', 'set', 'chief', '--agent', 'claude', '--model', 'sonnet');
  attn('crew', 'rename', 'chief', 'Juniper');
  attn('onboarding', 'done');
  attn('onboarding', 'again');
}

function addSession(wsctl, agent) {
  const output = wsctl('add-session', '--cwd', ROOT, '--agent', agent, '--label', 'earlier work');
  const id = output.match(/id=(\S+)/)?.[1];
  if (!id) throw new Error(`add-session did not name a session: ${output}`);
  return id;
}

function cleanEnv(extra = {}) {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !HARNESS_ENV.test(key)));
  return {
    ...env,
    ATTN_INSTANCE: INSTANCE,
    ATTN_WRAPPER_PATH: ATTN,
    ATTN_TOOL_HOME: TOOL_HOME,
    ATTN_AUTOMATION: '1',
    ATTN_HARNESS_SKILL_SYNC: '1',
    CLAUDE_CONFIG_DIR: CLAUDE_HOME,
    CODEX_HOME,
    ...extra,
  };
}

function hiddenHarnessEnv(hide = []) {
  return Object.fromEntries(hide.map((harness) => [`ATTN_${harness.toUpperCase()}_EXECUTABLE`, UNAVAILABLE_HARNESS]));
}

function runner(env) {
  const run = (command, args) => execFileSync(command, args, { cwd: REPO, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
  return {
    attn: (...args) => run(ATTN, args),
    wsctl: (...args) => run('go', ['run', './scripts/wsctl', ...args]),
  };
}

function reportStep(label, started) {
  console.log(`  ${label} (${((Date.now() - started) / 1000).toFixed(1)}s)`);
}

function make(target) {
  const started = Date.now();
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('ATTN_')));
  const result = spawnSync('make', [target, `INSTANCE=${INSTANCE}`], { cwd: REPO, env, stdio: 'inherit' });
  if (result.status !== 0) throw new Error(`make ${target} INSTANCE=${INSTANCE} exited ${result.status}`);
  reportStep(`make ${target}`, started);
}

function scrubInstance() {
  const home = os.homedir();
  const bundleId = bundleIdentifierForInstance(INSTANCE);
  for (const dir of [
    dataDirForInstance(INSTANCE),
    appLocalDataDirForInstance(INSTANCE),
    NOTEBOOK,
    WORK_NOTEBOOK,
    path.join(home, 'Library', 'WebKit', bundleId),
    path.join(home, 'Library', 'Caches', bundleId),
  ]) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function resetHarnessHomes() {
  const codexAuth = path.join(CODEX_HOME, 'auth.json');
  const keptAuth = fs.existsSync(codexAuth) ? fs.readFileSync(codexAuth) : null;
  fs.rmSync(TOOL_HOME, { recursive: true, force: true });
  fs.mkdirSync(CLAUDE_HOME, { recursive: true });
  fs.mkdirSync(CODEX_HOME, { recursive: true });
  if (fs.existsSync(CLAUDE_LOGIN_STATE)) fs.copyFileSync(CLAUDE_LOGIN_STATE, path.join(CLAUDE_HOME, '.claude.json'));
  if (keptAuth) fs.writeFileSync(codexAuth, keptAuth, { mode: 0o600 });
}

function neededLogins(scenario) {
  const hidden = new Set(scenario.hide ?? []);
  const missing = [];
  if (!hidden.has('claude') && !fs.existsSync(CLAUDE_LOGIN_STATE)) missing.push('claude');
  if (!hidden.has('codex') && !fs.existsSync(path.join(CODEX_HOME, 'auth.json'))) missing.push('codex');
  return missing;
}

function fakeHarness() {
  const file = path.join(ROOT, 'fake-harness.sh');
  fs.writeFileSync(file, '#!/bin/sh\nexit 0\n', { mode: 0o755 });
  return file;
}

async function ensureWorld(name, { force }) {
  const stored = path.join(WORLDS, name);
  if (fs.existsSync(stored) && !force) return stored;
  const started = Date.now();
  const build = WORLD_BUILDERS[name];
  if (!build) throw new Error(`no world named ${name}`);
  await ensureFreshWorld({ instance: INSTANCE, appPath: APP_PATH, log: () => {} });
  scrubInstance();
  resetHarnessHomes();
  const env = cleanEnv({ ATTN_CLAUDE_EXECUTABLE: fakeHarness() });
  const { attn, wsctl } = runner(env);
  attn('daemon', 'ensure');
  await build(attn, wsctl);
  await ensureFreshWorld({ instance: INSTANCE, appPath: APP_PATH, log: () => {} });
  fs.rmSync(stored, { recursive: true, force: true });
  fs.mkdirSync(WORLDS, { recursive: true });
  fs.cpSync(dataDirForInstance(INSTANCE), stored, {
    recursive: true,
    filter: (source) => !/\.(sock|pid)$/.test(source) && !source.includes(`${path.sep}workers${path.sep}`) && !source.includes(`${path.sep}pty-hosts${path.sep}`),
  });
  reportStep(`built the ${name} world`, started);
  return stored;
}

async function runScenario(scenario, { rebuild, rebuildApp, freshWorld }) {
  const started = Date.now();
  fs.mkdirSync(ROOT, { recursive: true });
  if (rebuildApp || !fs.existsSync(ATTN)) make('install');
  else if (rebuild) make('install-daemon');

  const world = await ensureWorld(scenario.world, { force: freshWorld });
  await ensureFreshWorld({ instance: INSTANCE, appPath: APP_PATH, log: () => {} });
  scrubInstance();
  resetHarnessHomes();
  fs.cpSync(world, dataDirForInstance(INSTANCE), { recursive: true });

  const env = cleanEnv(hiddenHarnessEnv(scenario.hide));
  const { attn, wsctl } = runner(env);
  attn('daemon', 'ensure');
  attn('settings', 'set', 'notebook.root', NOTEBOOK);
  await scenario.prepare?.(attn, wsctl);

  const launchEnv = Object.entries(env).filter(([key]) => HARNESS_ENV.test(key)).flatMap(([key, value]) => ['--env', `${key}=${value}`]);
  execFileSync('open', ['-a', APP_PATH, ...launchEnv], { env });

  recordRun(scenario.name);
  reportStep(`${scenario.name} ready`, started);
  console.log(`\n${scenario.title}\nExpected: ${scenario.expect}\nTry: ${scenario.try}\n`);
  console.log(`attn onboarding: ${attn('onboarding', ...(scenario.profile ? ['--profile', scenario.profile] : [])).trim()}`);
  for (const harness of neededLogins(scenario)) {
    console.log(`\n${harness} is not logged in here, so its Chief can't talk. Run once: node app/scripts/onboarding-rehearsal/rehearse.mjs login ${harness}`);
  }
}

function readLastRuns() {
  try {
    return JSON.parse(fs.readFileSync(LAST_RUNS, 'utf8'));
  } catch {
    return {};
  }
}

function recordRun(name) {
  fs.writeFileSync(LAST_RUNS, `${JSON.stringify({ ...readLastRuns(), [name]: new Date().toISOString() }, null, 2)}\n`);
}

function listScenarios() {
  const lastRuns = readLastRuns();
  const width = Math.max(...SCENARIOS.map(({ name }) => name.length));
  for (const scenario of SCENARIOS) {
    const last = lastRuns[scenario.name]?.slice(0, 16).replace('T', ' ') ?? 'never';
    console.log(`${scenario.name.padEnd(width)}  ${last.padEnd(16)}  ${scenario.title} -> ${scenario.expect}`);
  }
  console.log(`\nRun one: node app/scripts/onboarding-rehearsal/rehearse.mjs <scenario> [--rebuild | --rebuild-app] [--fresh-world]`);
}

function login(harness) {
  fs.mkdirSync(CLAUDE_HOME, { recursive: true });
  fs.mkdirSync(CODEX_HOME, { recursive: true });
  const command = { claude: ['claude', ['auth', 'login']], codex: ['codex', ['login']] }[harness];
  if (!command) throw new Error('login takes claude or codex');
  const result = spawnSync(command[0], command[1], { env: cleanEnv(), stdio: 'inherit' });
  if (result.status !== 0) throw new Error(`${command[0]} login exited ${result.status}`);
  if (harness === 'claude') {
    const state = JSON.parse(fs.readFileSync(path.join(CLAUDE_HOME, '.claude.json'), 'utf8'));
    fs.writeFileSync(CLAUDE_LOGIN_STATE, `${JSON.stringify({ ...state, hasCompletedOnboarding: true, theme: 'dark' }, null, 2)}\n`, { mode: 0o600 });
  }
  console.log(`${harness} is logged in under ${TOOL_HOME}.`);
}

async function main() {
  const args = process.argv.slice(2);
  const flags = new Set(args.filter((arg) => arg.startsWith('--')));
  const [command, operand] = args.filter((arg) => !arg.startsWith('--'));
  if (!command) return listScenarios();
  if (command === 'login') return login(operand);
  const scenario = SCENARIOS.find(({ name }) => name === command);
  if (!scenario) throw new Error(`no scenario named ${command}; run with no arguments to list them`);
  await runScenario(scenario, {
    rebuild: flags.has('--rebuild'),
    rebuildApp: flags.has('--rebuild-app'),
    freshWorld: flags.has('--fresh-world'),
  });
}

main().catch((error) => {
  console.error(error.message);
  process.exit(1);
});
