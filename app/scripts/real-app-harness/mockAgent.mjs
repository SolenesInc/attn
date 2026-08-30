#!/usr/bin/env node

import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const MOCK_AGENT_CONFIG = '.attn-mock-agent.json';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const executablePath = fileURLToPath(import.meta.url);

function resolveFixturePath(cwd, value) {
  return path.isAbsolute(value) ? value : path.join(cwd, value);
}

// Mirrors internal/statemarker.States(); the two must not drift.
export const MOCK_AGENT_STATES = ['waiting_input', 'idle'];
const REPLYING_ACTIONS = ['reply', 'attn'];

// Header and prompt glyph are what scenarioAgents.mjs reads to call a pane
// ready, so an agent the mock stands in for must be recognisable by both.
const MOCK_AGENT_FLAVORS = {
  // harness_signals.go reads claude's resting title by its ✳; any other leading
  // rune is unclassified, and an unsettled heartbeat never reaches idle.
  claude: { header: 'Claude Code mock agent', prompt: '❯ ', resting: '✳ ' },
  codex: { header: 'OpenAI Codex mock agent', prompt: '› ', resting: '' },
};

export const MOCK_AGENT_AGENTS = Object.keys(MOCK_AGENT_FLAVORS);
export const MOCK_AGENT_EXECUTABLE = executablePath;

export function mockAgentExecutableVar(agent) {
  return `ATTN_${String(agent).toUpperCase()}_EXECUTABLE`;
}

export function mockPinnedAgents(env = process.env) {
  return MOCK_AGENT_AGENTS.filter((agent) => env[mockAgentExecutableVar(agent)] === MOCK_AGENT_EXECUTABLE);
}

export function mockAgentFlavor(agent) {
  return MOCK_AGENT_FLAVORS[String(agent || '').trim().toLowerCase()] || MOCK_AGENT_FLAVORS.codex;
}

// Every row is drawn to the pane's width so the harness's width, density and
// native-paint gates see an agent TUI rather than two words on a blank screen.
export function mockAgentSplash({ header, cwd = '', cols = 80 }) {
  const width = Math.max(24, Math.trunc(Number(cols) || 80)) - 1;
  const inner = width - 4;
  const row = (text) => `│ ${String(text).slice(0, inner).padEnd(inner)} │`;
  return [
    `╭${'─'.repeat(width - 2)}╮`,
    row(header),
    row(`no model is called; replies come from ${MOCK_AGENT_CONFIG}`),
    row(`cwd ${cwd}`),
    `╰${'─'.repeat(width - 2)}╯`,
  ];
}

export function stateMarker(state) {
  return `<!-- attn:state=${state} -->`;
}

export function markerStateForActions(actions) {
  const list = Array.isArray(actions) ? actions : [];
  const named = list.filter((action) => action?.state);
  if (named.length > 0) return named[named.length - 1].state;
  return list.some((action) => REPLYING_ACTIONS.includes(action?.type)) ? 'waiting_input' : 'idle';
}

function validateActions(actions, label) {
  if (!Array.isArray(actions)) throw new Error(`${label} must be an array`);
  for (const action of actions) {
    if (!action || typeof action.type !== 'string') throw new Error(`${label} has an action without a type`);
    if (!['reply', 'delay', 'touch', 'wait_for_file', 'attn'].includes(action.type)) {
      throw new Error(`${label} has unsupported action type ${JSON.stringify(action.type)}`);
    }
    if (action.state !== undefined && !MOCK_AGENT_STATES.includes(action.state)) {
      throw new Error(`${label} has state ${JSON.stringify(action.state)}, want one of ${MOCK_AGENT_STATES.join(', ')}`);
    }
  }
}

export function validateMockAgentConfig(config) {
  if (!config || config.version !== 1) throw new Error('mock agent config version must be 1');
  if (!Array.isArray(config.turns)) throw new Error('mock agent config turns must be an array');
  if (config.minimumWorkingMs !== undefined && (!Number.isFinite(config.minimumWorkingMs) || config.minimumWorkingMs < 0)) {
    throw new Error('mock agent minimumWorkingMs must be a non-negative number');
  }
  for (const [index, turn] of config.turns.entries()) {
    if (typeof turn?.includes !== 'string' || !turn.includes) {
      throw new Error(`mock agent turn ${index} needs a non-empty includes value`);
    }
    validateActions(turn.actions, `mock agent turn ${index}`);
  }
  validateActions(config.defaultActions ?? [], 'mock agent defaultActions');
  return config;
}

export function selectMockAgentActions(config, input) {
  const turn = config.turns.find((candidate) => input.includes(candidate.includes));
  return turn?.actions ?? config.defaultActions ?? [];
}

export function mockAgentTitle(name, state, resting = '') {
  const label = name || 'mock agent';
  return state === 'working' ? `\u2838 ${label} working` : `${resting}${label} ready`;
}

export function createMockAgentInputParser(onPrompt) {
  const markers = [
    { value: '\u001b[200~', paste: true },
    { value: '\u001b[201~', paste: false },
  ];
  let pending = '';
  let prompt = '';
  let inPaste = false;
  let lastWasCR = false;

  return (chunk) => {
    pending += chunk;
    while (pending) {
      const marker = markers.find((candidate) => pending.startsWith(candidate.value));
      if (marker) {
        pending = pending.slice(marker.value.length);
        inPaste = marker.paste;
        continue;
      }
      if (markers.some((candidate) => candidate.value.startsWith(pending))) return;

      const char = pending[0];
      pending = pending.slice(1);
      if (char === '\r' || char === '\n') {
        if (char === '\n' && lastWasCR) {
          lastWasCR = false;
          continue;
        }
        lastWasCR = char === '\r';
        if (inPaste) {
          prompt += '\n';
        } else {
          const submitted = prompt.trim();
          prompt = '';
          if (submitted) onPrompt(submitted);
        }
        continue;
      }
      lastWasCR = false;
      prompt += char;
    }
  };
}

export function writeMockAgentFixture(cwd, config) {
  const checked = validateMockAgentConfig({ version: 1, ...config });
  fs.mkdirSync(cwd, { recursive: true });
  const configPath = path.join(cwd, MOCK_AGENT_CONFIG);
  fs.writeFileSync(configPath, `${JSON.stringify(checked, null, 2)}\n`, 'utf8');
  return { configPath, executablePath };
}

function runAttn(args, input = '') {
  return spawnSync(process.env.ATTN_WRAPPER_PATH || 'attn', args, {
    encoding: 'utf8',
    env: process.env,
    input,
  });
}

function requireAttn(args, input = '') {
  const result = runAttn(args, input);
  if (result.error || result.status !== 0) {
    const detail = (result.stderr || result.stdout || result.error?.message || 'unknown error').trim();
    throw new Error(`attn ${args.join(' ')} failed: ${detail}`);
  }
  return result.stdout || '';
}

function transcriptRecord(type, payload) {
  return JSON.stringify({ timestamp: new Date().toISOString(), type, payload });
}

// Every scenario launches the mock, most without a fixture: an absent one is a
// silent agent, not a crashed session.
export function readMockAgentConfig(cwd) {
  try {
    return validateMockAgentConfig(JSON.parse(fs.readFileSync(path.join(cwd, MOCK_AGENT_CONFIG), 'utf8')));
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error;
    return validateMockAgentConfig({ version: 1, turns: [] });
  }
}

async function runMockAgent() {
  const cwd = process.cwd();
  const config = readMockAgentConfig(cwd);
  const nativeId = `mock-${randomUUID()}`;
  const transcriptDir = path.join(cwd, '.attn-mock-agent');
  const transcriptPath = path.join(transcriptDir, `rollout-${nativeId}.jsonl`);
  fs.mkdirSync(transcriptDir, { recursive: true });
  fs.writeFileSync(transcriptPath, `${transcriptRecord('session_meta', {
    id: nativeId,
    timestamp: new Date().toISOString(),
    cwd,
    source: 'cli',
  })}\n`, 'utf8');

  requireAttn(['_hook-session-start'], JSON.stringify({
    session_id: nativeId,
    transcript_path: transcriptPath,
    cwd,
  }));

  const appendMessage = (role, text) => {
    fs.appendFileSync(transcriptPath, `${transcriptRecord('response_item', {
      type: 'message',
      role,
      content: [{ type: role === 'assistant' ? 'output_text' : 'input_text', text }],
    })}\n`, 'utf8');
  };
  const flavor = mockAgentFlavor(config.agent || process.env.ATTN_AGENT);
  const header = config.banner || flavor.header;
  const blocks = [];
  let phase = 'ready';

  const setTitle = () => process.stdout.write(`\u001b]0;${mockAgentTitle(config.name, phase, flavor.resting)}\u0007`);
  const prompt = () => {
    setTitle();
    process.stdout.write(`\n${flavor.prompt}`);
  };
  const splash = () => mockAgentSplash({ header, cwd, cols: process.stdout.columns }).join('\n');
  // A resized pane has to come back at the new width, and the header exactly
  // once: the codex header-frame gate counts it over the whole screen.
  const repaint = () => {
    process.stdout.write(`\u001b[2J\u001b[H${[splash(), ...blocks].join('\n\n')}\n`);
    prompt();
  };
  const emit = (block) => {
    blocks.push(block);
    process.stdout.write(`\n${block}\n`);
  };
  const reply = (text) => {
    appendMessage('assistant', text);
    emit(`• ${text.replaceAll('\n', '\n  ')}`);
  };

  const runAction = async (action) => {
    if (action.type === 'reply') {
      reply(String(action.text ?? ''));
      return;
    }
    if (action.type === 'delay') {
      await delay(Number(action.ms) || 0);
      return;
    }
    if (action.type === 'touch') {
      const target = resolveFixturePath(cwd, String(action.path || ''));
      fs.mkdirSync(path.dirname(target), { recursive: true });
      fs.writeFileSync(target, `${action.content ?? 'ready'}\n`, 'utf8');
      return;
    }
    if (action.type === 'wait_for_file') {
      const target = resolveFixturePath(cwd, String(action.path || ''));
      const deadline = Date.now() + (Number(action.timeoutMs) || 120_000);
      while (!fs.existsSync(target)) {
        if (Date.now() >= deadline) throw new Error(`timed out waiting for ${target}`);
        await delay(Number(action.pollMs) || 25);
      }
      return;
    }
    if (action.type === 'attn') {
      const output = requireAttn((action.args || []).map(String), String(action.input || '')).trim();
      reply(output || String(action.emptyReply || 'done'));
    }
  };

  const stop = (state) => {
    appendMessage('assistant', stateMarker(state));
    requireAttn(['_hook-stop'], JSON.stringify({
      session_id: nativeId,
      transcript_path: transcriptPath,
      cwd,
    }));
  };

  const answer = async (raw) => {
    const input = raw.trim();
    if (!input) {
      prompt();
      return;
    }
    appendMessage('user', input);
    phase = 'working';
    setTitle();
    requireAttn(['_hook-state', 'working', 'user_prompt_submit'], JSON.stringify({ prompt: input }));
    const workingSince = Date.now();
    // A turn silent for StaleAfter (60s) settles under the daemon.
    const beating = setInterval(setTitle, 500);
    try {
      const actions = selectMockAgentActions(config, input);
      for (const action of actions) await runAction(action);
      await delay(Math.max(0, (config.minimumWorkingMs ?? 1_500) - (Date.now() - workingSince)));
      stop(markerStateForActions(actions));
    } finally {
      clearInterval(beating);
    }
    phase = 'ready';
    prompt();
  };

  repaint();
  process.stdout.on('resize', repaint);
  let turns = Promise.resolve();
  const parseInput = createMockAgentInputParser((input) => {
    turns = turns.then(() => answer(input)).catch((error) => {
      emit(`• mock agent error: ${error.message}`);
      try { stop('idle'); } catch { /* the daemon may be closing */ }
      phase = 'ready';
      prompt();
    });
  });
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', parseInput);
  process.stdin.resume();
}

if (process.argv[1] && path.resolve(process.argv[1]) === executablePath) {
  runMockAgent().catch((error) => {
    console.error(`mock agent failed: ${error instanceof Error ? error.message : String(error)}`);
    process.exitCode = 1;
  });
}
