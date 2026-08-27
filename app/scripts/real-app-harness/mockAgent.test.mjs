import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  configureMockAgent,
  createMockAgentInputParser,
  mockAgentTitle,
  MOCK_AGENT_CONFIG,
  selectMockAgentActions,
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

  it('points Codex at the shared executable and restores the prior setting once', async () => {
    const settings = { codex_executable: '/usr/local/bin/codex' };
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
    expect(cleanups[0].name).toBe('restore_codex_executable');

    await configured.restore();
    await cleanups[0].fn();
    expect(writes).toHaveLength(2);
    expect(writes[1]).toEqual({ key: 'codex_executable', value: '/usr/local/bin/codex' });
  });
});
