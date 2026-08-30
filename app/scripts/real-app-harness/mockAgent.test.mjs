import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  configureMockAgent,
  createMockAgentInputParser,
  HEADLESS_TASKS_SETTING,
  markerStateForActions,
  mockAgentTitle,
  MOCK_AGENT_CONFIG,
  selectMockAgentActions,
  stateMarker,
  validateMockAgentConfig,
  writeMockAgentFixture,
} from './mockAgent.mjs';

let tmpDir;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mock-agent-test-'));
});

afterEach(() => {
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

describe('mock agent fixture', () => {
  it('writes one config beside the session and selects the first matching turn', () => {
    const config = {
      turns: [
        { includes: 'first', actions: [{ type: 'reply', text: 'one' }] },
        { includes: 'second', actions: [{ type: 'reply', text: 'two' }] },
      ],
      defaultActions: [{ type: 'reply', text: 'fallback' }],
    };
    const fixture = writeMockAgentFixture(tmpDir, config);
    const written = JSON.parse(fs.readFileSync(path.join(tmpDir, MOCK_AGENT_CONFIG), 'utf8'));

    expect(fixture.executablePath).toMatch(/mockAgent\.mjs$/);
    expect(fs.statSync(fixture.executablePath).mode & 0o111).not.toBe(0);
    expect(selectMockAgentActions(written, 'the second turn')).toEqual(config.turns[1].actions);
    expect(selectMockAgentActions(written, 'anything else')).toEqual(config.defaultActions);
  });

  it('rejects misspelled actions before a scenario launches', () => {
    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [{ includes: 'hello', actions: [{ type: 'repply', text: 'hello' }] }],
    })).toThrow('unsupported action type');

    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [],
      minimumWorkingMs: -1,
    })).toThrow('minimumWorkingMs must be a non-negative number');

    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [{ includes: 'hello', actions: [{ type: 'reply', text: 'hello', state: 'pending_approval' }] }],
    })).toThrow('want one of waiting_input, idle');

    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [{ includes: 'hello', actions: [{ type: 'reply', text: 'hello', state: 'parked' }] }],
    })).toThrow('want one of waiting_input, idle');
  });

  it('ends a turn in the state its actions name, waiting after a reply and idle when silent', () => {
    expect(markerStateForActions([{ type: 'reply', text: 'hi' }])).toBe('waiting_input');
    expect(markerStateForActions([{ type: 'touch', path: 'flag' }])).toBe('idle');
    expect(markerStateForActions([])).toBe('idle');
    expect(markerStateForActions([
      { type: 'reply', text: 'hi', state: 'waiting_input' },
      { type: 'reply', text: 'and one more', state: 'idle' },
    ])).toBe('idle');
  });

  it('submits a bracketed multiline paste as one prompt', () => {
    const prompts = [];
    const parse = createMockAgentInputParser((prompt) => prompts.push(prompt));

    parse('one line\r');
    parse('\u001b[20');
    parse('0~first pasted line\nsecond pasted line\u001b[201');
    parse('~\r');

    expect(prompts).toEqual([
      'one line',
      'first pasted line\nsecond pasted line',
    ]);
  });

  it('uses the Codex busy marker while a turn is running', () => {
    expect(mockAgentTitle('annotation mock', 'working')).toBe('\u2838 annotation mock working');
    expect(mockAgentTitle('annotation mock', 'ready')).toBe('annotation mock ready');
  });

  it('points Codex at the shared executable, turns headless tasks off, and restores both once', async () => {
    const settings = { codex_executable: '/usr/local/bin/codex', [HEADLESS_TASKS_SETTING]: 'true' };
    const writes = [];
    const cleanups = [];
    const client = {
      request: async (verb, payload) => {
        expect(verb).toBe('set_setting');
        writes.push(payload);
        settings[payload.key] = payload.value;
      },
    };
    const observer = {
      getSetting: (key) => settings[key] || '',
      waitFor: async (fn) => {
        expect(fn()).toBe(true);
      },
    };
    const runner = { registerCleanup: (name, fn) => cleanups.push({ name, fn }) };

    const configured = await configureMockAgent({ client, observer, runner });
    expect(writes[0]).toEqual({ key: 'codex_executable', value: configured.executablePath });
    expect(writes[1]).toEqual({ key: HEADLESS_TASKS_SETTING, value: 'false' });
    expect(cleanups[0].name).toBe('restore_codex_executable');

    await configured.restore();
    await cleanups[0].fn();
    expect(writes).toHaveLength(4);
    expect(writes[2]).toEqual({ key: 'codex_executable', value: '/usr/local/bin/codex' });
    expect(writes[3]).toEqual({ key: HEADLESS_TASKS_SETTING, value: 'true' });
  });
});

const FAKE_ATTN = `#!/usr/bin/env node
import fs from 'node:fs';
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
  fs.appendFileSync(process.env.MOCK_AGENT_LEDGER, JSON.stringify({ args: process.argv.slice(2), input }) + '\\n');
});
`;

describe('mock agent turns', () => {
  let child;

  afterEach(() => {
    child?.kill('SIGKILL');
    child = null;
  });

  async function waitFor(read, description, timeoutMs = 20_000) {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const value = read();
      if (value) return value;
      if (Date.now() >= deadline) throw new Error(`timed out waiting for ${description}`);
      await new Promise((resolve) => setTimeout(resolve, 25));
    }
  }

  it('closes a turn with the state marker and the real stop hook', async () => {
    const ledger = path.join(tmpDir, 'attn-calls.jsonl');
    const wrapper = path.join(tmpDir, 'fake-attn.mjs');
    fs.writeFileSync(wrapper, FAKE_ATTN, { mode: 0o755 });
    fs.writeFileSync(ledger, '');

    const { executablePath } = writeMockAgentFixture(tmpDir, {
      name: 'marker mock',
      minimumWorkingMs: 0,
      turns: [{ includes: 'wrap up', actions: [{ type: 'reply', text: 'wrapped up', state: 'idle' }] }],
      defaultActions: [{ type: 'reply', text: 'mock turn finished' }],
    });

    child = spawn(process.execPath, [executablePath], {
      cwd: tmpDir,
      env: { ...process.env, ATTN_WRAPPER_PATH: wrapper, MOCK_AGENT_LEDGER: ledger, ATTN_SESSION_ID: 'sess-marker' },
      stdio: ['pipe', 'pipe', 'pipe'],
    });

    const calls = () => fs.readFileSync(ledger, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));
    const sessionStart = await waitFor(
      () => calls().find((call) => call.args[0] === '_hook-session-start'),
      'the mock to report its session start',
    );
    const transcriptPath = JSON.parse(sessionStart.input).transcript_path;

    child.stdin.write('please wrap up');
    child.stdin.write('\r');

    const stop = await waitFor(() => calls().find((call) => call.args[0] === '_hook-stop'), 'the mock to run the stop hook');
    expect(calls().some((call) => call.args[0] === '_hook-state' && call.args[1] === 'working')).toBe(true);
    expect(calls().some((call) => call.args[0] === '_hook-state' && call.args[1] === 'idle')).toBe(false);
    expect(JSON.parse(stop.input).transcript_path).toBe(transcriptPath);

    const messages = fs.readFileSync(transcriptPath, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));
    const assistant = messages.filter((entry) => entry.payload?.role === 'assistant');
    expect(assistant.at(-1).payload.content[0].text).toBe(stateMarker('idle'));
    expect(assistant.at(-2).payload.content[0].text).toBe('wrapped up');
  });
});
