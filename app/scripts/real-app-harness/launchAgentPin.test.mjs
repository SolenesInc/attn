import { afterEach, describe, expect, it } from 'vitest';
import { launchFreshAppAndConnect, restoreHarnessSettings } from './common.mjs';
import { MOCK_AGENT_EXECUTABLE } from './mockAgent.mjs';

function fakeClient() {
  const calls = [];
  return {
    calls,
    settingWrites: () => calls.filter((call) => call.verb === 'set_setting').map((call) => call.payload),
    launchFreshApp: async () => {},
    waitForManifest: async () => {},
    waitForReady: async () => {},
    waitForFrontendResponsive: async () => {},
    request: async (verb, payload) => {
      calls.push({ verb, payload });
      return {};
    },
  };
}

function fakeObserver(settings = {}) {
  return {
    connect: async () => {},
    getSetting: (key) => settings[key] ?? '',
  };
}

function recordingWriter() {
  const written = [];
  const write = async (entries) => { written.push(...entries); };
  return { written, write };
}

const ENV_KEYS = ['ATTN_CLAUDE_EXECUTABLE', 'ATTN_CODEX_EXECUTABLE'];

function armTripwireEnv({ claude = MOCK_AGENT_EXECUTABLE, codex = MOCK_AGENT_EXECUTABLE } = {}) {
  if (claude) process.env.ATTN_CLAUDE_EXECUTABLE = claude;
  if (codex) process.env.ATTN_CODEX_EXECUTABLE = codex;
}

afterEach(async () => {
  for (const key of ENV_KEYS) delete process.env[key];
  await restoreHarnessSettings({ write: async () => {} });
});

describe('mock agent pinning', () => {
  it('points every agent the tripwire mocked at the mock without the scenario asking', async () => {
    armTripwireEnv();
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'claude_executable', value: MOCK_AGENT_EXECUTABLE },
      { key: 'codex_executable', value: MOCK_AGENT_EXECUTABLE },
    ]);
  });

  it('leaves the agent a scenario runs for real on its own executable', async () => {
    armTripwireEnv({ codex: '/opt/homebrew/bin/codex' });
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'claude_executable', value: MOCK_AGENT_EXECUTABLE },
    ]);
  });

  it('writes nothing for an agent already pinned at the mock', async () => {
    armTripwireEnv();
    const client = fakeClient();
    const observer = fakeObserver({
      claude_executable: MOCK_AGENT_EXECUTABLE,
      codex_executable: MOCK_AGENT_EXECUTABLE,
    });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([]);
    expect(await restoreHarnessSettings({ write: async () => {} })).toBe(0);
  });

  it('puts back the executables it found, blanks included', async () => {
    armTripwireEnv();
    const client = fakeClient();
    const observer = fakeObserver({ claude_executable: '/usr/local/bin/claude', codex_executable: '' });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });

    const writer = recordingWriter();
    expect(await restoreHarnessSettings({ write: writer.write })).toBe(2);
    expect(writer.written).toEqual([
      { key: 'claude_executable', value: '/usr/local/bin/claude' },
      { key: 'codex_executable', value: '' },
    ]);
  });

  it('restores what the run started with, not its own pin, after a relaunch', async () => {
    armTripwireEnv();
    const client = fakeClient();
    const observer = fakeObserver({ claude_executable: '/usr/local/bin/claude', codex_executable: '' });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
    const afterPin = fakeObserver({
      claude_executable: MOCK_AGENT_EXECUTABLE,
      codex_executable: MOCK_AGENT_EXECUTABLE,
    });
    await launchFreshAppAndConnect(client, afterPin, { sweepStaleSessions: false });

    const writer = recordingWriter();
    await restoreHarnessSettings({ write: writer.write });
    expect(writer.written).toEqual([
      { key: 'claude_executable', value: '/usr/local/bin/claude' },
      { key: 'codex_executable', value: '' },
    ]);
  });

  it('pins nothing when the scenario left every agent real', async () => {
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([]);
  });
});
