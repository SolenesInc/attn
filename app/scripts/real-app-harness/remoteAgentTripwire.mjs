import fs from 'node:fs';
import path from 'node:path';

import { tripwireDir } from './agentTripwire.mjs';
import {
  MOCK_AGENT_CONFIG,
  MOCK_AGENT_EXECUTABLE,
  validateMockAgentConfig,
} from './mockAgent.mjs';
import {
  listRemoteProcessesByHarnessRoot,
  runSSH,
} from './scenarioRemote.mjs';

export const REMOTE_MOCK_AGENT_COMMAND = 'attn-harness-mock-agent';
export const REMOTE_AGENT_FIXTURE_RELATIVE_ROOT = '.attn/real-app-harness/agent-fixtures';
const AGENT_BINARIES = ['claude', 'codex', 'copilot', 'pi'];

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

export function buildRemoteAgentTripwire({ remoteHome, remotePaths, scenarioId }) {
  const fixtureRoot = path.posix.join(remoteHome, REMOTE_AGENT_FIXTURE_RELATIVE_ROOT);
  const binDir = path.posix.join(fixtureRoot, 'bin');
  const ledgerPath = path.posix.join(remotePaths.remoteHarnessRoot, 'agent-tripwire.ledger');
  const marker = `${scenarioId}|${binDir}`;
  const executable = (name) => path.posix.join(binDir, name);
  return {
    scenarioId,
    fixtureRoot,
    binDir,
    ledgerPath,
    marker,
    mockAgentPath: executable(REMOTE_MOCK_AGENT_COMMAND),
    remoteHarnessRoot: remotePaths.remoteHarnessRoot,
    remoteHarnessBinary: remotePaths.remoteHarnessBinary,
    launchEnv: {
      ATTN_REMOTE_DATA_DIR: remotePaths.remoteHarnessRoot,
      ATTN_REMOTE_PATH_PREFIX: binDir,
      ATTN_REMOTE_HEADLESS_TASKS: 'off',
      ATTN_REMOTE_AGENT_TRIPWIRE: marker,
      ATTN_REMOTE_AGENT_TRIPWIRE_LEDGER: ledgerPath,
      ATTN_REMOTE_AGENT_TRIPWIRE_SCENARIO: scenarioId,
      ...Object.fromEntries(AGENT_BINARIES.map((name) => [
        `ATTN_REMOTE_${name.toUpperCase()}_EXECUTABLE`,
        executable(name),
      ])),
    },
  };
}

export function installLocalRemoteMockCommand({ dir = tripwireDir() } = {}) {
  fs.mkdirSync(dir, { recursive: true });
  const target = path.join(dir, REMOTE_MOCK_AGENT_COMMAND);
  fs.copyFileSync(MOCK_AGENT_EXECUTABLE, target);
  fs.chmodSync(target, 0o755);
  return REMOTE_MOCK_AGENT_COMMAND;
}

export async function armRemoteAgentTripwire({
  target,
  fixture,
  runner,
  remote = runSSH,
  installLocalMock = installLocalRemoteMockCommand,
}) {
  installLocalMock();
  const required = [fixture.mockAgentPath, ...AGENT_BINARIES.map((name) => path.posix.join(fixture.binDir, name))];
  const checks = required.map((file) => `test -x ${shellQuote(file)}`).join(' && ');
  await remote(
    target,
    `${checks} || { printf 'remote agent fixture missing; run pnpm --dir app run real-app:provision-remote\n' >&2; exit 1; }; ` +
      `mkdir -p ${shellQuote(fixture.remoteHarnessRoot)}; : > ${shellQuote(fixture.ledgerPath)}`,
  );
  const armed = {
    scenarioId: fixture.scenarioId,
    fixtureRoot: fixture.fixtureRoot,
    ledgerPath: fixture.ledgerPath,
    marker: fixture.marker,
  };
  runner?.writeJson('remote-agent-tripwire-armed.json', armed);
  return armed;
}

export async function writeRemoteMockAgentFixture({ target, cwd, config, remote = runSSH }) {
  const checked = validateMockAgentConfig({ version: 1, ...config });
  const encoded = Buffer.from(`${JSON.stringify(checked, null, 2)}\n`, 'utf8').toString('base64');
  const configPath = path.posix.join(cwd, MOCK_AGENT_CONFIG);
  await remote(
    target,
    `mkdir -p ${shellQuote(cwd)}; printf %s ${shellQuote(encoded)} | base64 -d > ${shellQuote(configPath)}`,
  );
  return configPath;
}

function parseEnvironment(raw) {
  return Object.fromEntries(String(raw || '')
    .split('\n')
    .filter(Boolean)
    .map((line) => {
      const separator = line.indexOf('=');
      return separator < 0 ? [line, ''] : [line.slice(0, separator), line.slice(separator + 1)];
    }));
}

export async function verifyRemoteDaemonTripwire({
  target,
  fixture,
  runner,
  remote = runSSH,
  listProcesses = listRemoteProcessesByHarnessRoot,
}) {
  const processes = await listProcesses(target, fixture.remoteHarnessRoot, 30_000);
  const daemons = processes.filter((processInfo) => {
    const cmdline = String(processInfo?.cmdline || '');
    return cmdline.includes(fixture.remoteHarnessBinary) && /\bdaemon\b/.test(cmdline);
  });
  if (daemons.length !== 1) {
    throw new Error(`remote agent tripwire: expected one harness daemon, found ${daemons.length}`);
  }
  const daemon = daemons[0];
  const raw = await remote(target, `tr '\\0' '\\n' < /proc/${daemon.pid}/environ`);
  const env = parseEnvironment(raw);
  const expected = {
    ATTN_HEADLESS_TASKS: 'off',
    ATTN_AGENT_TRIPWIRE: fixture.marker,
    ATTN_AGENT_TRIPWIRE_LEDGER: fixture.ledgerPath,
    ATTN_AGENT_TRIPWIRE_SCENARIO: fixture.scenarioId,
    ...Object.fromEntries(AGENT_BINARIES.map((name) => [
      `ATTN_${name.toUpperCase()}_EXECUTABLE`,
      path.posix.join(fixture.binDir, name),
    ])),
  };
  const faults = Object.entries(expected)
    .filter(([key, value]) => env[key] !== value)
    .map(([key, value]) => `${key} reads ${JSON.stringify(env[key] ?? null)}, want ${JSON.stringify(value)}`);
  if ((env.PATH || '').split(':')[0] !== fixture.binDir) {
    faults.push(`PATH starts with ${JSON.stringify((env.PATH || '').split(':')[0] || null)}, want ${JSON.stringify(fixture.binDir)}`);
  }
  const receipt = {
    ok: faults.length === 0,
    pid: daemon.pid,
    headlessTasks: env.ATTN_HEADLESS_TASKS || null,
    carriesMarker: env.ATTN_AGENT_TRIPWIRE === fixture.marker,
    pathPrefix: (env.PATH || '').split(':')[0] || null,
    faults,
  };
  runner?.writeJson('remote-agent-tripwire-receipt.json', receipt);
  if (faults.length > 0) {
    throw new Error(`remote agent tripwire: remote daemon is not armed\n  ${faults.join('\n  ')}`);
  }
  return receipt;
}

export async function collectRemoteAgentTripwire({
  target,
  fixture,
  runner,
  assertClean = true,
  remote = runSSH,
}) {
  const raw = await remote(target, `test -f ${shellQuote(fixture.ledgerPath)} && cat ${shellQuote(fixture.ledgerPath)}`);
  const lines = String(raw || '').split('\n').map((line) => line.trim()).filter(Boolean);
  runner?.writeText('remote-agent-tripwire.ledger', raw ? `${raw.trimEnd()}\n` : '');
  const result = {
    count: lines.length,
    ledgerPath: fixture.ledgerPath,
    lines,
  };
  if (assertClean && lines.length > 0) {
    throw new Error([
      `remote agent tripwire: ${lines.length} real agent exec(s) during ${fixture.scenarioId}; this run calls no model.`,
      `remote ledger: ${fixture.ledgerPath}`,
      ...lines.map((line) => `  ${line}`),
    ].join('\n'));
  }
  return result;
}
