import { execFileSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export const repository = path.resolve(new URL('../../..', import.meta.url).pathname);
const mockAgent = path.join(repository, 'app/scripts/real-app-harness/mockAgent.mjs');
const realClaude = path.join(os.homedir(), '.claude');
const instancePrefix = 'wfe-';
const stubRoles = ['pathfinder', 'builder', 'reviewer', 'orchestrator', 'prototyper'];

export function preflight() {
  if (!fs.existsSync(path.join(repository, 'app/node_modules/ws'))) {
    throw new Error(`the stubs run app/scripts/real-app-harness/mockAgent.mjs, which needs app/node_modules: run pnpm --dir ${path.relative(process.cwd(), path.join(repository, 'app')) || '.'} install`);
  }
}

export function readAccessToken(minutes) {
  const { accessToken, expiresAt } = JSON.parse(fs.readFileSync(path.join(realClaude, '.credentials.json'), 'utf8')).claudeAiOauth;
  const left = (expiresAt - Date.now()) / 60_000;
  if (left < minutes) {
    throw new Error(`the machine's Claude access token expires in ${Math.max(0, Math.floor(left))} minutes, before this case's ${minutes}-minute limit; let a real Claude session renew it, then rerun`);
  }
  return accessToken;
}

function scrubbedEnv() {
  return Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    !/^(ATTN_|CLAUDE|CODEX_|ANTHROPIC_)/.test(key) || key === 'ATTN_SESSION_HOME' || key === 'ATTN_SESSION_BINARY'));
}

export function createWorld({ root, arm, accessToken }) {
  const instance = `${instancePrefix}${randomBytes(4).toString('hex')}`;
  const dir = path.join(root, instance);
  const toolHome = path.join(dir, 'toolhome');
  const repo = path.join(dir, 'repo');
  fs.mkdirSync(dir, { recursive: true });
  const env = {
    ...scrubbedEnv(),
    PATH: `${arm.bin}:${process.env.PATH}`,
    ATTN_INSTANCE: instance,
    ATTN_HEADLESS_TASKS: 'off',
    ATTN_PTY_BACKEND: 'worker',
    ATTN_AUTOMATION: '1',
    ATTN_HARNESS_SKILL_SYNC: '1',
    ATTN_TOOL_HOME: toolHome,
    CLAUDE_CONFIG_DIR: path.join(toolHome, '.claude'),
    ATTN_CODEX_EXECUTABLE: mockAgent,
  };
  const world = {
    instance, dir, toolHome, repo, env, arm, seeds: {},
    attn: (args, options = {}) => execFileSync(path.join(arm.bin, 'attn'), args, { env, encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'], ...options }),
    attnJson: (args) => JSON.parse(world.attn([...args, '--json'])),
    daemonEnv: { ...env, CLAUDE_CODE_OAUTH_TOKEN: accessToken },
  };
  const resolved = world.attnJson(['instance', 'resolve']);
  if (resolved.instance !== instance || resolved.dataDir !== path.join(os.homedir(), `.attn-${instance}`)) {
    throw new Error(`the runner's environment selects ${resolved.dataDir}, not instance ${instance}; refusing to start`);
  }
  return world;
}

function copyClaudeConfig(target, trustedFolder) {
  fs.mkdirSync(path.join(target, 'skills'), { recursive: true });
  for (const name of ['CLAUDE.md', 'agents', 'commands', 'output-styles']) {
    const source = path.join(realClaude, name);
    if (fs.existsSync(source)) fs.cpSync(source, path.join(target, name), { recursive: true, dereference: true });
  }
  for (const skill of fs.readdirSync(path.join(realClaude, 'skills'))) {
    if (skill !== 'attn-workflow') fs.cpSync(path.join(realClaude, 'skills', skill), path.join(target, 'skills', skill), { recursive: true, dereference: true });
  }
  const { hooks, statusLine, enabledPlugins, extraKnownMarketplaces, ...settings } = JSON.parse(fs.readFileSync(path.join(realClaude, 'settings.json'), 'utf8'));
  fs.writeFileSync(path.join(target, 'settings.json'), `${JSON.stringify(settings, null, 2)}\n`);
  const version = execFileSync('claude', ['--version'], { encoding: 'utf8' }).match(/\d+\.\d+\.\d+/)?.[0] ?? '';
  fs.writeFileSync(path.join(target, '.claude.json'), `${JSON.stringify({
    hasCompletedOnboarding: true,
    lastOnboardingVersion: version,
    lastReleaseNotesSeen: version,
    hasSeenAutoDefaultNotice: true,
    autoModeClassifierBillingNoticeAcknowledgedAt: Date.now(),
    projects: { [trustedFolder]: { hasTrustDialogAccepted: true, hasCompletedProjectOnboarding: true } },
  }, null, 2)}\n`, { mode: 0o600 });
}

export function startWorld(world, testCase) {
  fs.mkdirSync(world.repo);
  copyClaudeConfig(world.env.CLAUDE_CONFIG_DIR, fs.realpathSync(world.repo));
  world.attn(['daemon', 'ensure'], { env: world.daemonEnv });
  world.attn(['delegate', 'roles', 'install']);
  for (const role of stubRoles) {
    const real = role === testCase.role || testCase.level === 2;
    world.attn(['delegate', 'roles', 'set', role, '--agent', real ? 'claude' : 'codex', '--model', real ? testCase.model : 'default']);
  }
  world.attn(['delegate', 'roles', 'set', '--fallback', '--agent', 'codex', '--model', 'default']);
  const seedsFile = path.join(world.dir, 'seeds');
  execFileSync('bash', [path.join(testCase.dir, 'world.sh')], {
    cwd: world.repo,
    env: { ...world.env, PATH: `${world.arm.bin}:${process.env.PATH}`, EVAL_SEEDS: seedsFile },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  for (const line of fs.readFileSync(seedsFile, 'utf8').trim().split('\n')) {
    const [name, id] = line.split(/\s+/);
    world.seeds[name] = id;
  }
}

export function cleanWorld(world) {
  if (!world.instance.startsWith(instancePrefix)) throw new Error(`refusing to clean ${world.instance}`);
  let failure = '';
  try {
    world.attn(['instance', 'clean', world.instance]);
  } catch (error) {
    failure = `attn instance clean ${world.instance}: ${String(error.stderr || error.message).trim()}`;
  }
  fs.rmSync(path.join(os.homedir(), `attn-notebook-${world.instance}`), { recursive: true, force: true });
  if (failure) throw new Error(failure);
}
