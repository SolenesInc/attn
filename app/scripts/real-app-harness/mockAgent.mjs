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

export function mockAgentTitle(name, state) {
  const label = name || 'mock agent';
  return state === 'working' ? `\u2838 ${label} working` : `${label} ready`;
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

export const HEADLESS_TASKS_SETTING = 'headless_tasks.enabled';

async function setSettingAndWait(client, observer, key, value, description) {
  await client.request('set_setting', { key, value });
  await observer.waitFor(() => (observer.getSetting(key) === value ? true : null), description, 20_000);
}

export async function configureMockAgent({ client, observer, runner, agent = 'codex' }) {
  const key = `${agent}_executable`;
  const previous = observer.getSetting(key) || '';
  await setSettingAndWait(client, observer, key, executablePath, `${key} to point at the shared mock agent`);

  const previousHeadless = observer.getSetting(HEADLESS_TASKS_SETTING) || 'true';
  await setSettingAndWait(client, observer, HEADLESS_TASKS_SETTING, 'false', 'headless tasks to be off');

  let restored = false;
  const restore = async () => {
    if (restored) return;
    restored = true;
    await client.request('set_setting', { key, value: previous });
    await client.request('set_setting', { key: HEADLESS_TASKS_SETTING, value: previousHeadless });
  };
  runner?.registerCleanup(`restore_${key}`, restore);
  return { executablePath, previous, previousHeadless, restore };
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

async function runMockAgent() {
  const cwd = process.cwd();
  const configPath = path.join(cwd, MOCK_AGENT_CONFIG);
  const config = validateMockAgentConfig(JSON.parse(fs.readFileSync(configPath, 'utf8')));
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
  const setTitle = (value) => process.stdout.write(`\u001b]0;${value}\u0007`);
  const prompt = () => {
    setTitle(mockAgentTitle(config.name, 'ready'));
    process.stdout.write('\n› ');
  };
  const reply = (text) => {
    appendMessage('assistant', text);
    process.stdout.write(`\n• ${text.replaceAll('\n', '\n  ')}\n`);
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
    setTitle(mockAgentTitle(config.name, 'working'));
    requireAttn(['_hook-state', 'working', 'user_prompt_submit'], JSON.stringify({ prompt: input }));
    const workingSince = Date.now();
    const actions = selectMockAgentActions(config, input);
    for (const action of actions) await runAction(action);
    await delay(Math.max(0, (config.minimumWorkingMs ?? 1_500) - (Date.now() - workingSince)));
    stop(markerStateForActions(actions));
    prompt();
  };

  process.stdout.write(`${config.banner || 'OpenAI Codex mock agent'}\n`);
  prompt();
  let turns = Promise.resolve();
  const parseInput = createMockAgentInputParser((input) => {
    turns = turns.then(() => answer(input)).catch((error) => {
      process.stdout.write(`\n• mock agent error: ${error.message}\n`);
      try { stop('idle'); } catch { /* the daemon may be closing */ }
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
