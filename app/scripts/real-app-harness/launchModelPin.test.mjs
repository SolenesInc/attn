import { afterEach, describe, expect, it } from 'vitest';
import { launchFreshAppAndConnect, restoreHarnessSettings } from './common.mjs';

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

const ENV_KEYS = ['ATTN_HARNESS_LAUNCH_MODEL_CLAUDE', 'ATTN_HARNESS_LAUNCH_MODEL_CODEX'];

afterEach(async () => {
  for (const key of ENV_KEYS) delete process.env[key];
  await restoreHarnessSettings({ write: async () => {} });
});

describe('launch model pinning', () => {
  it('pins every agent to its cheap model and low effort without the scenario asking', async () => {
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'default_model_claude', value: 'haiku' },
      { key: 'default_effort_claude', value: 'low' },
      { key: 'default_model_codex', value: 'gpt-5.4-mini' },
      { key: 'default_effort_codex', value: 'low' },
    ]);
  });

  it('puts back the models and efforts it found, not blanks', async () => {
    const client = fakeClient();
    const observer = fakeObserver({
      default_model_claude: 'opus',
      default_effort_claude: 'high',
      default_model_codex: '',
      default_effort_codex: 'max',
    });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });

    const writer = recordingWriter();
    expect(await restoreHarnessSettings({ write: writer.write })).toBe(4);
    expect(writer.written).toEqual([
      { key: 'default_model_claude', value: 'opus' },
      { key: 'default_effort_claude', value: 'high' },
      { key: 'default_model_codex', value: '' },
      { key: 'default_effort_codex', value: 'max' },
    ]);
  });

  it('restores the recipe the run started with, not its own pin, after a relaunch', async () => {
    const client = fakeClient();
    const observer = fakeObserver({
      default_model_claude: 'sonnet',
      default_effort_claude: 'medium',
      default_model_codex: 'gpt-5.5',
      default_effort_codex: 'xhigh',
    });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
    const afterPin = fakeObserver({
      default_model_claude: 'haiku',
      default_effort_claude: 'low',
      default_model_codex: 'gpt-5.4-mini',
      default_effort_codex: 'low',
    });
    await launchFreshAppAndConnect(client, afterPin, { sweepStaleSessions: false });

    const writer = recordingWriter();
    await restoreHarnessSettings({ write: writer.write });
    expect(writer.written).toEqual([
      { key: 'default_model_claude', value: 'sonnet' },
      { key: 'default_effort_claude', value: 'medium' },
      { key: 'default_model_codex', value: 'gpt-5.5' },
      { key: 'default_effort_codex', value: 'xhigh' },
    ]);
  });

  it('lets one agent be overridden without unpinning the other', async () => {
    process.env.ATTN_HARNESS_LAUNCH_MODEL_CLAUDE = 'sonnet';
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'default_model_claude', value: 'sonnet' },
      { key: 'default_effort_claude', value: 'low' },
      { key: 'default_model_codex', value: 'gpt-5.4-mini' },
      { key: 'default_effort_codex', value: 'low' },
    ]);
  });

  it('leaves both settings alone only when inheriting is asked for by name', async () => {
    process.env.ATTN_HARNESS_LAUNCH_MODEL_CLAUDE = 'inherit';
    const client = fakeClient();
    await launchFreshAppAndConnect(client, fakeObserver(), { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'default_model_codex', value: 'gpt-5.4-mini' },
      { key: 'default_effort_codex', value: 'low' },
    ]);
  });

  it('repairs an incompatible effort when the model is already pinned', async () => {
    const client = fakeClient();
    const observer = fakeObserver({
      default_model_claude: 'haiku',
      default_effort_claude: 'low',
      default_model_codex: 'gpt-5.4-mini',
      default_effort_codex: 'max',
    });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([
      { key: 'default_effort_codex', value: 'low' },
    ]);
    const writer = recordingWriter();
    expect(await restoreHarnessSettings({ write: writer.write })).toBe(1);
    expect(writer.written).toEqual([
      { key: 'default_effort_codex', value: 'max' },
    ]);
  });

  it('writes nothing when both recipes are already what it wants', async () => {
    const client = fakeClient();
    const observer = fakeObserver({
      default_model_claude: 'haiku',
      default_effort_claude: 'low',
      default_model_codex: 'gpt-5.4-mini',
      default_effort_codex: 'low',
    });
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });

    expect(client.settingWrites()).toEqual([]);
    expect(await restoreHarnessSettings({ write: async () => {} })).toBe(0);
  });
});
