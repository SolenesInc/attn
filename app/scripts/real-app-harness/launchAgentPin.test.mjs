import { afterEach, describe, expect, it } from 'vitest';
import { once } from 'node:events';
import { WebSocketServer } from 'ws';
import { launchFreshAppAndConnect, restoreHarnessSettings, writeDaemonSettings } from './common.mjs';
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
  it.each([false, true])('restores both settings from one coalesced snapshot (key omitted: %s)', async (omitKey) => {
    const server = new WebSocketServer({ host: '127.0.0.1', port: 0 });
    await once(server, 'listening');
    const entries = [{ key: 'claude_executable', value: '' }, { key: 'codex_executable', value: '/mock/codex' }];
    const received = [];
    server.on('connection', (socket) => socket.on('message', (raw) => {
      const message = JSON.parse(raw.toString());
      if (message.cmd !== 'set_setting') return;
      received.push({ key: message.key, value: message.value });
      if (received.length === entries.length) socket.send(JSON.stringify({
        event: 'settings_updated',
        ...(!omitKey && { changed_key: 'codex_executable' }),
        settings: Object.fromEntries(received.map(({ key, value }) => [key, value])),
      }));
    }));
    try {
      await writeDaemonSettings(entries, { wsUrl: `ws://127.0.0.1:${server.address().port}` });
      expect(received).toEqual(entries);
    } finally {
      for (const socket of server.clients) socket.terminate();
      await new Promise((resolve) => server.close(resolve));
    }
  });

  it('reports a refused setting instead of treating its changed key as acknowledgement', async () => {
    const server = new WebSocketServer({ host: '127.0.0.1', port: 0 });
    await once(server, 'listening');
    server.on('connection', (socket) => socket.on('message', (raw) => {
      const message = JSON.parse(raw.toString());
      if (message.cmd === 'set_setting') socket.send(JSON.stringify({
        event: 'settings_updated', changed_key: message.key, success: false, error: 'fixture refusal', settings: {},
      }));
    }));
    try {
      await expect(writeDaemonSettings([{ key: 'claude_executable', value: '' }], {
        wsUrl: `ws://127.0.0.1:${server.address().port}`,
      })).rejects.toThrow('fixture refusal');
    } finally {
      for (const socket of server.clients) socket.terminate();
      await new Promise((resolve) => server.close(resolve));
    }
  });

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

  it('pins a PATH command when the same session launches on a remote host', async () => {
    armTripwireEnv();
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), {
      sweepStaleSessions: false,
      agentExecutables: { codex: 'attn-harness-mock-agent' },
    });

    expect(client.settingWrites()).toEqual([
      { key: 'claude_executable', value: MOCK_AGENT_EXECUTABLE },
      { key: 'codex_executable', value: 'attn-harness-mock-agent' },
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
