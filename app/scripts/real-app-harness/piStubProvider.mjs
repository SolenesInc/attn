// Agent and classifier are told apart by the system prompt: the classifier's
// opens with a line no coding agent's does.
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { execFileSync, spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { profileCliEnv, socketPathForProfile } from './harnessProfile.mjs';
import { queueDaemonSettingRestore } from './common.mjs';
import { appDaemonInTree } from './platform.mjs';

const CLASSIFIER_MARKER = 'You are a security monitor for an autonomous coding agent';

const PASS_ONE = 'This is pass 1';
const ALLOW_SEVERITY = 5;
const DENY_SEVERITY = 60;
const HARD_BLOCK_RULE = 'Data Exfiltration';

function severityAnswer(verdict, pass) {
  if (verdict.verdict === 'allow') return `<severity>${ALLOW_SEVERITY}</severity>`;
  if (pass === 1) return `<severity>${DENY_SEVERITY}</severity>`;
  const category = verdict.boundary === true ? HARD_BLOCK_RULE : (verdict.category ?? 'Destructive Operation');
  const thinking = verdict.reason ? `<thinking>${verdict.reason}</thinking>` : '';
  return `${thinking}<severity>${DENY_SEVERITY}</severity><category>${category}</category>`;
}

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

export const stubProviderName = 'attn-harness';
export const stubAgentModel = `${stubProviderName}/stub-agent`;
export const stubJudgeModel = `${stubProviderName}/stub-judge`;

function textOf(content) {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return content == null ? '' : JSON.stringify(content);
  return content.map((part) => (typeof part === 'string' ? part : (part?.text ?? ''))).join('');
}

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function sse(res, payload) {
  res.write(`data: ${JSON.stringify(payload)}\n\n`);
}

function chunk(delta, finishReason = null) {
  return {
    id: 'attn-harness-stub',
    object: 'chat.completion.chunk',
    created: 1,
    model: 'stub',
    choices: [{ index: 0, delta, finish_reason: finishReason }],
  };
}

function writeText(res, text) {
  sse(res, chunk({ role: 'assistant', content: text }));
  sse(res, { ...chunk({}, 'stop'), usage: { prompt_tokens: 8, completion_tokens: 4, total_tokens: 12 } });
}

function writeToolCall(res, { id, name, args }) {
  sse(res, chunk({
    tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: '' } }],
  }));
  sse(res, chunk({
    tool_calls: [{ index: 0, function: { arguments: JSON.stringify(args) } }],
  }));
  sse(res, { ...chunk({}, 'tool_calls'), usage: { prompt_tokens: 8, completion_tokens: 6, total_tokens: 14 } });
}

export function startPiStubProvider({ agent, judge }) {
  const calls = { agent: [], judge: [] };
  let held;
  const server = http.createServer((req, res) => {
    if (!req.url.endsWith('/chat/completions')) {
      res.writeHead(200, { 'Content-Type': 'text/plain' }).end('STUB-OK\n');
      return;
    }
    let raw = '';
    req.on('data', (piece) => { raw += piece; });
    req.on('end', async () => {
      let body;
      try {
        body = JSON.parse(raw);
      } catch (error) {
        res.writeHead(400).end(String(error));
        return;
      }
      const messages = body.messages ?? [];
      const systemPrompt = messages
        .filter((m) => m.role === 'system' || m.role === 'developer')
        .map((m) => (typeof m.content === 'string' ? m.content : JSON.stringify(m.content)))
        .join('\n');
      const role = systemPrompt.includes(CLASSIFIER_MARKER) ? 'judge' : 'agent';
      const lastUser = messages.findLastIndex((m) => m.role === 'user');
      const prompts = messages.filter((m) => m.role === 'user').map((m) => textOf(m.content));
      const toolResults = messages.slice(lastUser + 1).filter((m) => m.role === 'tool').map((m) => textOf(m.content));
      const request = { body, messages, systemPrompt, turn: calls[role].length, prompt: prompts.at(-1) ?? '', prompts, toolResults };
      calls[role].push(request);

      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      });
      try {
        if (role === 'judge') {
          const userText = messages
            .filter((m) => m.role === 'user')
            .map((m) => (typeof m.content === 'string' ? m.content : JSON.stringify(m.content)))
            .join('\n');
          const pass = userText.includes(PASS_ONE) ? 1 : 2;
          if (pass === 1) held = judge(request);
          const verdict = held ?? { verdict: 'deny', reason: 'the stub had no scripted verdict' };
          writeText(res, severityAnswer(verdict, pass));
        } else {
          const answer = await agent(request);
          if (answer.holdMs) await delay(answer.holdMs);
          if (answer.tool) {
            writeToolCall(res, {
              id: answer.tool.id ?? `call-${calls.agent.length}`,
              name: answer.tool.name,
              args: answer.tool.args,
            });
          } else {
            writeText(res, answer.text ?? '');
          }
        }
      } catch (error) {
        writeText(res, `stub provider failed: ${error?.message ?? error}`);
      }
      res.write('data: [DONE]\n\n');
      res.end();
    });
  });

  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({
        port,
        baseUrl: `http://127.0.0.1:${port}/v1`,
        calls,
        close: () => new Promise((done) => server.close(() => done())),
      });
    });
  });
}

export function writeStubAgentDir(dir, baseUrl) {
  fs.mkdirSync(dir, { recursive: true });
  const model = (id) => ({
    id,
    name: id,
    api: 'openai-completions',
    provider: stubProviderName,
    baseUrl,
    reasoning: false,
    input: ['text'],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 128000,
    maxTokens: 8192,
  });
  fs.writeFileSync(
    path.join(dir, 'models.json'),
    `${JSON.stringify({
      providers: {
        [stubProviderName]: {
          baseUrl,
          apiKey: 'attn-harness-stub',
          api: 'openai-completions',
          models: [model('stub-agent'), model('stub-judge')],
        },
      },
    }, null, 2)}\n`,
    'utf8',
  );
  return dir;
}

export function scriptedAgent(rules) {
  return (request) => {
    const rule = rules.find(({ when }) => (when instanceof RegExp ? when.test(request.prompt) : request.prompt.includes(when)));
    if (!rule) return { text: `the stub has no scripted reply for: ${request.prompt.slice(0, 200)}` };
    const tools = typeof rule.tools === 'function' ? rule.tools(request) : (rule.tools ?? []);
    const next = tools[request.toolResults.length];
    if (next) return { tool: next, holdMs: rule.holdMs };
    return { text: typeof rule.text === 'function' ? rule.text(request) : (rule.text ?? ''), holdMs: rule.holdMs };
  };
}

export const bash = (command) => ({ name: 'bash', args: { command } });

export function rememberedWord(request) {
  for (const prompt of [...request.prompts].reverse()) {
    const match = /Remember this word: (\w+)/.exec(prompt);
    if (match) return match[1];
  }
  return 'the stub was never told a word to remember';
}

export const allowEverything = () => ({ verdict: 'allow' });

// The profile's own bundled CLI, not a repo build: bundled plugins resolve
// relative to the app bundle, so any other daemon reports the pi driver missing.
export function resolveAttnBinary(appPath) {
  const candidates = [
    process.env.ATTN_HARNESS_BIN,
    appDaemonInTree(appPath),
    path.resolve(HARNESS_DIR, '../../../attn'),
  ].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error(`no attn binary found for ${appPath}`);
}

// pi reads its agent dir from the daemon's environment, so a daemon left over
// from an earlier run is restarted with the stub agent dir before the app connects.
export async function restartDaemonWithStubEnv({ appPath, profile, agentDir }) {
  const attnBin = resolveAttnBinary(appPath);
  const env = profileCliEnv(profile, { ATTN_SOCKET_PATH: socketPathForProfile(profile) });
  try {
    execFileSync(attnBin, ['daemon', 'stop'], { encoding: 'utf8', env });
  } catch {
  }
  const child = spawn(attnBin, ['daemon', 'ensure'], {
    env: { ...env, PI_CODING_AGENT_DIR: agentDir },
    detached: true,
    stdio: 'ignore',
  });
  child.unref();
  const deadline = Date.now() + 30_000;
  for (;;) {
    try {
      execFileSync(attnBin, ['automode', 'show', '--json'], { encoding: 'utf8', env, stdio: ['ignore', 'pipe', 'ignore'] });
      return;
    } catch {
      if (Date.now() > deadline) throw new Error('daemon did not come back up with the stub agent dir');
      await delay(500);
    }
  }
}

export async function startStubWorld({ scenario, appPath, profile, agent, judge = allowEverything }) {
  const stub = await startPiStubProvider({ agent, judge });
  const agentDir = writeStubAgentDir(path.join(os.tmpdir(), `attn-${scenario}-${process.pid}`), stub.baseUrl);
  const launchEnv = { PI_CODING_AGENT_DIR: agentDir };
  return {
    stub,
    agentDir,
    launchEnv,
    async launch({ client, observer, runner, launchApp, pinModelFor }) {
      await restartDaemonWithStubEnv({ appPath, profile, agentDir });
      await launchApp();
      const key = `default_model_${pinModelFor}`;
      queueDaemonSettingRestore(observer, key);
      await client.request('set_setting', { key, value: stubAgentModel });
      runner.log(`stub provider on ${stub.baseUrl}`, { agentDir, model: stubAgentModel });
    },
    async close() {
      await stub.close().catch(() => {});
      fs.rmSync(agentDir, { recursive: true, force: true });
    },
  };
}
