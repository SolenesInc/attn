import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  captureFromPrompt,
  claudeTranscriptPath,
  mockTranscriptPath,
  transcriptTurns,
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
  selectMockAgentTurn,
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

  it('can model an injected turn without emitting a prompt-submit hook', () => {
    const config = validateMockAgentConfig({
      version: 1,
      turns: [{ includes: '🔔', submitHook: false, actions: [] }],
    });

    expect(selectMockAgentTurn(config, '🔔 seed moved').submitHook).toBe(false);
    expect(selectMockAgentTurn(config, 'ordinary prompt').submitHook).toBeUndefined();
    expect(() => validateMockAgentConfig({
      version: 1,
      turns: [{ includes: '🔔', submitHook: 'no', actions: [] }],
    })).toThrow('submitHook must be a boolean');
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
      env: {
        ...process.env,
        ATTN_WRAPPER_PATH: wrapper,
        MOCK_AGENT_LEDGER: ledger,
        ATTN_SESSION_ID: 'sess-marker',
        ATTN_AGENT: 'codex',
      },
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
      env: {
        ...process.env,
        ATTN_WRAPPER_PATH: wrapper,
        MOCK_AGENT_LEDGER: ledger,
        ATTN_SESSION_ID: 'sess-argv',
        ATTN_AGENT: 'codex',
      },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
  }

  it('records an injected turn without reporting a prompt-submit hook', async () => {
    const ledger = path.join(tmpDir, 'attn-calls.jsonl');
    writeMockAgentFixture(tmpDir, {
      name: 'injected turn mock',
      minimumWorkingMs: 0,
      turns: [{
        includes: '🔔',
        submitHook: false,
        actions: [{ type: 'reply', text: 'read', state: 'idle' }],
      }],
    });
    child = startMock([], ledger);
    const calls = () => fs.readFileSync(ledger, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));

    child.stdin.write('🔔 s-abc123 moved: note\r');
    await waitFor(() => calls().find((call) => call.args[0] === '_hook-stop'), 'the injected turn to close');

    expect(calls().some((call) => call.args[0] === '_hook-state')).toBe(false);
  });

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

describe('mock agent conversations', () => {
  let child;
  let home;

  beforeEach(() => {
    home = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'mock-agent-home-')));
  });

  afterEach(() => {
    child?.kill('SIGKILL');
    child = null;
    fs.rmSync(home, { recursive: true, force: true });
  });

  const homeEnv = () => ({ ATTN_TOOL_HOME: home, CODEX_HOME: path.join(home, '.codex') });

  it('places a resumable transcript where each agent driver looks for it', () => {
    const env = homeEnv();
    expect(mockTranscriptPath({ agent: 'claude', cwd: '/tmp/work.dir', id: 'sess-1', resumable: true, env }))
      .toBe(path.join(home, '.claude', 'projects', '-tmp-work-dir', 'sess-1.jsonl'));

    const rollout = mockTranscriptPath({
      agent: 'codex',
      cwd: '/tmp/work',
      id: 'mock-9',
      resumable: true,
      startedAt: new Date('2026-08-30T14:05:06.789Z'),
      env,
    });
    expect(rollout).toBe(path.join(home, '.codex', 'sessions', '2026', '08', '30', 'rollout-2026-08-30T14-05-06-mock-9.jsonl'));

    expect(mockTranscriptPath({ agent: 'codex', cwd: '/tmp/work', id: 'mock-9', resumable: false, env }))
      .toBe('/tmp/work/.attn-mock-agent/rollout-mock-9.jsonl');
  });

  it('reads prior turns out of either transcript shape and leaves the state marker out', () => {
    const codex = [
      JSON.stringify({ type: 'session_meta', payload: { id: 'mock-1', cwd: '/tmp/work' } }),
      JSON.stringify({ type: 'response_item', payload: { type: 'message', role: 'user', content: [{ type: 'input_text', text: 'ping' }] } }),
      JSON.stringify({ type: 'response_item', payload: { type: 'message', role: 'assistant', content: [{ type: 'output_text', text: 'pong' }] } }),
      JSON.stringify({ type: 'response_item', payload: { type: 'message', role: 'assistant', content: [{ type: 'output_text', text: stateMarker('waiting_input') }] } }),
      'not json at all',
    ].join('\n');
    expect(transcriptTurns(codex)).toEqual([{ role: 'user', text: 'ping' }, { role: 'assistant', text: 'pong' }]);

    const claude = [
      JSON.stringify({ type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'ping' }] } }),
      JSON.stringify({ type: 'assistant', message: { id: 'msg_1', role: 'assistant', content: [{ type: 'text', text: 'pong' }] } }),
    ].join('\n');
    expect(transcriptTurns(claude)).toEqual([{ role: 'user', text: 'ping' }, { role: 'assistant', text: 'pong' }]);
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

  function startMock(argv, ledger, agent) {
    const wrapper = path.join(tmpDir, 'fake-attn.mjs');
    fs.writeFileSync(wrapper, FAKE_ATTN, { mode: 0o755 });
    if (!fs.existsSync(ledger)) fs.writeFileSync(ledger, '');
    const spawned = spawn(process.execPath, [MOCK_AGENT_EXECUTABLE, ...argv], {
      cwd: tmpDir,
      env: {
        ...process.env,
        ...homeEnv(),
        ATTN_WRAPPER_PATH: wrapper,
        MOCK_AGENT_LEDGER: ledger,
        ATTN_SESSION_ID: 'sess-resume',
        ATTN_AGENT: agent,
      },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    spawned.stdout.setEncoding('utf8');
    return spawned;
  }

  it('resumes a codex rollout from the sessions tree and repaints what was said before', async () => {
    const ledger = path.join(tmpDir, 'attn-calls.jsonl');
    writeMockAgentFixture(tmpDir, {
      name: 'resume mock',
      resumable: true,
      minimumWorkingMs: 0,
      turns: [{ includes: 'say the token', actions: [{ type: 'reply', text: 'TOKEN42' }] }],
    });
    const calls = () => fs.readFileSync(ledger, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));

    child = startMock([], ledger, 'codex');
    const start = await waitFor(() => calls().find((call) => call.args[0] === '_hook-session-start'), 'the session start hook');
    const { session_id: nativeId, transcript_path: rollout } = JSON.parse(start.input);
    expect(rollout.startsWith(path.join(home, '.codex', 'sessions'))).toBe(true);
    expect(rollout.endsWith(`${nativeId}.jsonl`)).toBe(true);

    child.stdin.write('say the token\r');
    await waitFor(() => calls().find((call) => call.args[0] === '_hook-stop'), 'the first turn to close');
    const recorded = fs.readFileSync(rollout, 'utf8');
    expect(JSON.parse(recorded.split('\n')[0])).toMatchObject({ type: 'session_meta', payload: { id: nativeId } });
    expect(recorded).toContain('"type":"token_count"');
    child.kill('SIGKILL');

    child = startMock(['resume', nativeId, '-C', tmpDir], ledger, 'codex');
    let painted = '';
    child.stdout.on('data', (chunk) => { painted += chunk; });
    await waitFor(() => (painted.includes('TOKEN42') ? painted : null), 'the resumed pane to repaint the earlier reply');
    expect(painted).toContain('say the token');
    expect(painted).not.toContain('attn:state=');

    const resumed = calls().filter((call) => call.args[0] === '_hook-session-start').at(-1);
    expect(JSON.parse(resumed.input)).toMatchObject({ session_id: nativeId, transcript_path: rollout });
    const metas = fs.readFileSync(rollout, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line))
      .filter((entry) => entry.type === 'session_meta');
    expect(metas.at(-1).payload.launch.resumeSessionId).toBe(nativeId);
  });

  it('starts a new codex conversation on /new and binds the successor rollout', async () => {
    const ledger = path.join(tmpDir, 'attn-calls.jsonl');
    writeMockAgentFixture(tmpDir, { name: 'new mock', resumable: true, minimumWorkingMs: 0, turns: [] });
    const calls = () => fs.readFileSync(ledger, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));
    const starts = () => calls().filter((call) => call.args[0] === '_hook-session-start').map((call) => JSON.parse(call.input));

    child = startMock([], ledger, 'codex');
    const first = await waitFor(() => starts()[0], 'the first conversation');
    child.stdin.write('/new\r');
    const second = await waitFor(() => (starts().length > 1 ? starts()[1] : null), 'the successor conversation');

    expect(second.session_id).not.toBe(first.session_id);
    expect(second.transcript_path).not.toBe(first.transcript_path);
    expect(fs.existsSync(first.transcript_path)).toBe(true);
    expect(fs.existsSync(second.transcript_path)).toBe(true);
  });

  it('writes a claude transcript under the session id attn dictated, and only once it takes a turn', async () => {
    const ledger = path.join(tmpDir, 'attn-calls.jsonl');
    writeMockAgentFixture(tmpDir, {
      name: 'claude resume mock',
      resumable: true,
      minimumWorkingMs: 0,
      turns: [
        { includes: 'hello', actions: [{ type: 'reply', text: 'hello back' }] },
        { includes: 'again', actions: [{ type: 'reply', text: 'still here' }] },
      ],
    });
    const calls = () => fs.readFileSync(ledger, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line));
    const stops = () => calls().filter((call) => call.args[0] === '_hook-stop');
    const assistantMessages = (file) => fs.readFileSync(file, 'utf8').split('\n').filter(Boolean)
      .map((line) => JSON.parse(line))
      .filter((entry) => entry.type === 'assistant')
      .map((entry) => entry.message);

    child = startMock(['--session-id', 'attn-sess-7'], ledger, 'claude');
    const start = await waitFor(() => calls().find((call) => call.args[0] === '_hook-session-start'), 'the session start hook');
    const { session_id: nativeId, transcript_path: transcript } = JSON.parse(start.input);
    expect(nativeId).toBe('attn-sess-7');
    expect(transcript).toBe(claudeTranscriptPath(fs.realpathSync(tmpDir), 'attn-sess-7', homeEnv()));
    expect(fs.existsSync(transcript)).toBe(false);

    child.stdin.write('hello\r');
    await waitFor(() => stops()[0], 'the first turn to close');
    const first = assistantMessages(transcript);
    expect(first[0].content[0].text).toBe('hello back');
    expect(first[0].usage.input_tokens).toBeGreaterThan(0);
    child.kill('SIGKILL');

    child = startMock(['-r', 'attn-sess-7'], ledger, 'claude');
    let painted = '';
    child.stdout.on('data', (chunk) => { painted += chunk; });
    await waitFor(() => (painted.includes('hello back') ? painted : null), 'the resumed pane to repaint the earlier reply');

    child.stdin.write('again\r');
    await waitFor(() => (stops().length > 1 ? stops()[1] : null), 'the turn after the resume to close');
    const answered = assistantMessages(transcript).filter((message) => !message.content[0].text.startsWith('<!-- attn:state='));
    expect(answered.map((message) => message.content[0].text)).toEqual(['hello back', 'still here']);
    // attn keys a claude cost observation on the message id and drops a repeated
    // usage value, so a resumed turn that reuses either overwrites the earlier one.
    expect(new Set(assistantMessages(transcript).map((message) => message.id)).size)
      .toBe(assistantMessages(transcript).length);
    expect(answered[1].usage.input_tokens).toBeGreaterThan(answered[0].usage.input_tokens);
  });
});
