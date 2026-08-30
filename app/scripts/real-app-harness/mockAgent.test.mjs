import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  captureFromPrompt,
  createMockAgentInputParser,
  interpolateCaptures,
  markerStateForActions,
  parseMockAgentArgv,
  mockAgentFlavor,
  mockAgentSplash,
  mockAgentTitle,
  mockPinnedAgents,
  MOCK_AGENT_AGENTS,
  MOCK_AGENT_CONFIG,
  MOCK_AGENT_EXECUTABLE,
  readMockAgentConfig,
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

  it('rests under the glyph claude reports being at its prompt with', () => {
    expect(mockAgentFlavor('claude').resting).toBe('\u2733 ');
    expect(mockAgentFlavor('codex').resting).toBe('');
    expect(mockAgentTitle('queue mock', 'ready', mockAgentFlavor('claude').resting))
      .toBe('\u2733 queue mock ready');
  });

  it('stands in for the agent attn launched it as', () => {
    expect(MOCK_AGENT_AGENTS).toEqual(['claude', 'codex']);
    expect(mockAgentFlavor('claude').prompt).toBe('\u276f ');
    expect(mockAgentFlavor('codex').header).toContain('OpenAI Codex');
    expect(mockAgentFlavor('')).toBe(mockAgentFlavor('codex'));
  });

  it('draws a splash whose every row fills the pane and names the header once', () => {
    const lines = mockAgentSplash({ header: 'OpenAI Codex mock agent', cwd: '/tmp/session', cols: 64 });

    expect(lines).toHaveLength(5);
    for (const line of lines) expect(line.trimEnd()).toHaveLength(63);
    expect(lines.join('\n').match(/OpenAI Codex/g)).toHaveLength(1);
    expect(lines[0].endsWith('\u256e')).toBe(true);
    expect(lines.at(-1).endsWith('\u256f')).toBe(true);
  });

  it('keeps the splash inside a narrow pane', () => {
    for (const line of mockAgentSplash({ header: 'Claude Code mock agent', cwd: '/tmp/x', cols: 28 })) {
      expect(line.length).toBe(27);
    }
  });

  it('is a silent agent when the session directory holds no fixture', () => {
    const config = readMockAgentConfig(tmpDir);

    expect(config.turns).toEqual([]);
    expect(selectMockAgentActions(config, 'anything at all')).toEqual([]);
  });

  it('reads the brief and the resume flags each agent hands it on argv', () => {
    const claude = parseMockAgentArgv([
      '--session-id', 'sess-7', '--settings', '/tmp/settings.json',
      '--disallowed-tools', 'AttnPeerSend', '--model', 'claude-haiku-4-5',
      '--dangerously-skip-permissions', '--', 'Plant a seed and report back',
    ]);
    expect(claude.initialPrompt).toBe('Plant a seed and report back');
    expect(claude.sessionId).toBe('sess-7');
    expect(claude.resumeSessionId).toBe('');

    const resumed = parseMockAgentArgv(['--model', 'x', '-r', 'abc-123', '--', 'carry on']);
    expect(resumed.resumeSessionId).toBe('abc-123');
    expect(resumed.resumePicker).toBe(false);

    const codex = parseMockAgentArgv(['-c', 'k="v"', 'resume', 'rollout-9', '-C', '/tmp/work', '--', 'brief here']);
    expect(codex.resumeSessionId).toBe('rollout-9');
    expect(codex.initialPrompt).toBe('brief here');

    expect(parseMockAgentArgv(['resume', '-C', '/tmp/work']).resumePicker).toBe(true);
    expect(parseMockAgentArgv([])).toEqual({
      argv: [], sessionId: '', resumeSessionId: '', resumePicker: false, initialPrompt: '',
    });
  });

  it('captures a value out of the prompt and fills it into later arguments', () => {
    const action = { type: 'capture', from: 'prompt', pattern: '(s-[a-z0-9]{6})', name: 'seed' };
    expect(captureFromPrompt(action, 'your work is seed `s-627pnh` in the garden')).toBe('s-627pnh');
    expect(() => captureFromPrompt(action, 'no seed here')).toThrow('found no');

    const captures = new Map([['seed', 's-627pnh']]);
    expect(interpolateCaptures('{{seed}}', captures)).toBe('s-627pnh');
    expect(interpolateCaptures('note on {{seed}} now', captures)).toBe('note on s-627pnh now');
    expect(() => interpolateCaptures('{{ticket}}', captures)).toThrow('nothing captured under ticket');
  });

  it('rejects a capture or an exec a scenario cannot run', () => {
    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [{ includes: 'hi', actions: [{ type: 'capture', pattern: 's-.*' }] }],
    })).toThrow('capture needs a name');

    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [{ includes: 'hi', actions: [{ type: 'capture', name: 'seed', from: 'transcript', pattern: 's-.*' }] }],
    })).toThrow('want one of prompt');

    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [{ includes: 'hi', actions: [{ type: 'capture', name: 'seed', pattern: '(' }] }],
    })).toThrow('does not compile');

    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [{ includes: 'hi', actions: [{ type: 'exec', args: ['status'] }] }],
    })).toThrow('exec needs a cmd');
  });

  it('names only the agents whose executable is pinned at the mock', () => {
    expect(mockPinnedAgents({
      ATTN_CLAUDE_EXECUTABLE: MOCK_AGENT_EXECUTABLE,
      ATTN_CODEX_EXECUTABLE: '/opt/homebrew/bin/codex',
    })).toEqual(['claude']);
    expect(mockPinnedAgents({})).toEqual([]);
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

  it('paints a full-width splash and answers nothing when no fixture was written', async () => {
    const ledger = path.join(tmpDir, 'attn-calls.jsonl');
    const wrapper = path.join(tmpDir, 'fake-attn.mjs');
    fs.writeFileSync(wrapper, FAKE_ATTN, { mode: 0o755 });
    fs.writeFileSync(ledger, '');

    child = spawn(process.execPath, [MOCK_AGENT_EXECUTABLE], {
      cwd: tmpDir,
      env: {
        ...process.env,
        ATTN_WRAPPER_PATH: wrapper,
        MOCK_AGENT_LEDGER: ledger,
        ATTN_SESSION_ID: 'sess-splash',
        ATTN_AGENT: 'claude',
      },
      stdio: ['pipe', 'pipe', 'pipe'],
    });

    let stdout = '';
    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (chunk) => { stdout += chunk; });

    await waitFor(() => stdout.includes('\u276f ') || null, 'the mock to paint its prompt');
    expect(stdout).toContain('Claude Code mock agent');
    expect(stdout).toContain('\u256d');
    expect(stdout.split('\n').some((line) => line.trimEnd().length >= 70)).toBe(true);

    const calls = () => fs.readFileSync(ledger, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));
    child.stdin.write('nothing matches this\r');
    await waitFor(() => calls().find((call) => call.args[0] === '_hook-stop'), 'the silent turn to close');
    expect(stdout).not.toContain('\u2022 ');
  });

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

  function startMock(argv, ledger) {
    const wrapper = path.join(tmpDir, 'fake-attn.mjs');
    fs.writeFileSync(wrapper, FAKE_ATTN, { mode: 0o755 });
    fs.writeFileSync(ledger, '');
    return spawn(process.execPath, [MOCK_AGENT_EXECUTABLE, ...argv], {
      cwd: tmpDir,
      env: { ...process.env, ATTN_WRAPPER_PATH: wrapper, MOCK_AGENT_LEDGER: ledger, ATTN_SESSION_ID: 'sess-argv' },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
  }

  it('takes the brief off argv as its first turn and fills a captured value into the attn call', async () => {
    const ledger = path.join(tmpDir, 'attn-calls.jsonl');
    const brief = 'Live proof only. Your work is seed `s-627pnh`; ring a note about it.';
    writeMockAgentFixture(tmpDir, {
      name: 'capture mock',
      minimumWorkingMs: 0,
      turns: [{
        includes: 'ring a note',
        actions: [
          { type: 'capture', from: 'prompt', pattern: '(s-[a-z0-9]{6})', name: 'seed' },
          { type: 'attn', args: ['seed', 'note', '{{seed}}', '-m', 'RING7', '--ring'] },
        ],
      }],
    });

    child = startMock(['-c', 'k="v"', 'resume', 'rollout-42', '-C', tmpDir, '--', brief], ledger);

    const calls = () => fs.readFileSync(ledger, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));
    const note = await waitFor(
      () => calls().find((call) => call.args[0] === 'seed' && call.args[1] === 'note'),
      'the mock to act on the brief it was launched with',
    );
    expect(note.args).toEqual(['seed', 'note', 's-627pnh', '-m', 'RING7', '--ring']);
    await waitFor(() => calls().find((call) => call.args[0] === '_hook-stop'), 'the argv turn to close');

    const sessionStart = calls().find((call) => call.args[0] === '_hook-session-start');
    const meta = JSON.parse(fs.readFileSync(JSON.parse(sessionStart.input).transcript_path, 'utf8').split('\n')[0]);
    expect(meta.payload.launch.resumeSessionId).toBe('rollout-42');
    expect(meta.payload.launch.argv).toContain('resume');
  });

  it('runs a command, paints its output, and fails the turn only when the failure is not allowed', async () => {
    const ledger = path.join(tmpDir, 'attn-calls.jsonl');
    writeMockAgentFixture(tmpDir, {
      name: 'exec mock',
      minimumWorkingMs: 0,
      turns: [
        {
          includes: 'remove the worktree',
          actions: [{ type: 'exec', cmd: process.execPath, args: ['-e', 'console.log("worktree removed")'], state: 'idle' }],
        },
        {
          includes: 'already gone',
          actions: [{
            type: 'exec',
            cmd: process.execPath,
            args: ['-e', 'console.error("no such worktree"); process.exit(3)'],
            allowFailure: true,
          }],
        },
        {
          includes: 'must not fail',
          actions: [{ type: 'exec', cmd: process.execPath, args: ['-e', 'process.exit(4)'] }],
        },
      ],
    });

    child = startMock([], ledger);
    let stdout = '';
    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (chunk) => { stdout += chunk; });

    const calls = () => fs.readFileSync(ledger, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));
    const stops = () => calls().filter((call) => call.args[0] === '_hook-stop').length;

    child.stdin.write('remove the worktree\r');
    await waitFor(() => stdout.includes('worktree removed') || null, 'the command output in the pane');
    await waitFor(() => stops() >= 1 || null, 'the exec turn to close');

    child.stdin.write('the worktree is already gone\r');
    await waitFor(() => stdout.includes('no such worktree') || null, 'the tolerated failure in the pane');
    expect(stdout).toContain('exited 3');
    expect(stdout).not.toContain('mock agent error');

    child.stdin.write('this one must not fail\r');
    await waitFor(() => (stdout.includes('mock agent error') ? stdout : null), 'the refused failure to break the turn');
    expect(stdout).toContain('exited 4');
    await waitFor(() => stops() >= 3 || null, 'every turn to close its own stop hook');

    const transcriptPath = JSON.parse(calls().find((call) => call.args[0] === '_hook-session-start').input).transcript_path;
    expect(fs.readFileSync(transcriptPath, 'utf8')).toContain('worktree removed');
  });
});
