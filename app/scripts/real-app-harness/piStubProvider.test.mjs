// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';
import { bash, rememberedWord, scriptedAgent, startPiStubProvider, stubAgentModel, waitForPiPreflight } from './piStubProvider.mjs';

describe('Pi startup preflight', () => {
  afterEach(() => vi.useRealTimers());
  const check = (name, status, summary = name) => ({ name, status, summary });
  const output = (...checks) => JSON.stringify({ status: checks.some((c) => c.status === 'fail') ? 'fail' : 'pass', checks });
  const startup = output(check('plugin.attn-pi', 'fail', 'daemon cannot launch pi: pi availability has not been checked'));
  const failedCommand = (stdout) => Object.assign(new Error('exit 1'), { stdout, stderr: 'profile banner' });

  it('keeps the failed report and waits for the cached startup health to settle', async () => {
    vi.useFakeTimers();
    const run = vi.fn().mockImplementationOnce(() => { throw failedCommand(startup); })
      .mockReturnValue(output(check('plugin.attn-pi', 'pass', 'pi is ready')));
    const receipts = [];
    const pending = waitForPiPreflight({ run, save: (attempts) => receipts.push(structuredClone(attempts)) });
    expect(run).toHaveBeenCalledTimes(1);
    expect(receipts[0][0]).toMatchObject({ stdout: startup, stderr: 'profile banner', report: { status: 'fail' } });
    await vi.advanceTimersByTimeAsync(250);
    await expect(pending).resolves.toMatchObject({ status: 'pass' });
    expect(receipts.at(-1)).toHaveLength(2);
  });

  it('waits for an agent that has not registered or received its first health check', async () => {
    vi.useFakeTimers();
    const run = vi.fn().mockImplementationOnce(() => { throw failedCommand(output(check('launch.agent', 'fail'))); })
      .mockReturnValueOnce(output(check('plugin.attn-pi', 'warn')))
      .mockReturnValue(output(check('plugin.attn-pi', 'pass')));
    const pending = waitForPiPreflight({ run, save: () => {} });
    await vi.advanceTimersByTimeAsync(500);
    await expect(pending).resolves.toMatchObject({ status: 'pass' });
    expect(run).toHaveBeenCalledTimes(3);
  });

  it.each(['tool.go', 'path.go_caches', 'routing.daemon', 'protocol.cli_app', 'plugin.attn-pi'])
    ('fails immediately for %s errors even during startup', async (name) => {
      const run = vi.fn(() => { throw failedCommand(output(...JSON.parse(startup).checks, check(name, 'fail', 'broken'))); });
      const save = vi.fn();
      await expect(waitForPiPreflight({ run, save })).rejects.toThrow(`${name}: broken`);
      expect(run).toHaveBeenCalledTimes(1);
      expect(save).toHaveBeenCalledOnce();
    });

  it('fails after the startup deadline with the last report saved', async () => {
    vi.useFakeTimers();
    const run = vi.fn(() => { throw failedCommand(startup); });
    const save = vi.fn();
    const pending = expect(waitForPiPreflight({ run, save, timeoutMs: 500 })).rejects.toThrow('availability has not been checked');
    await vi.advanceTimersByTimeAsync(500);
    await pending;
    expect(run).toHaveBeenCalledTimes(3);
    expect(save.mock.lastCall[0]).toHaveLength(3);
  });

  it('saves malformed output before failing', async () => {
    const save = vi.fn();
    await expect(waitForPiPreflight({ run: () => { throw failedCommand('not JSON'); }, save }))
      .rejects.toThrow('did not return a report');
    expect(save.mock.lastCall[0][0]).toMatchObject({ stdout: 'not JSON', stderr: 'profile banner' });
  });
});

async function ask(stub, messages) {
  const response = await fetch(`${stub.baseUrl}/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: stubAgentModel, messages, stream: true }),
  });
  const chunks = (await response.text())
    .split('\n\n')
    .filter((line) => line.startsWith('data: ') && !line.includes('[DONE]'))
    .map((line) => JSON.parse(line.slice('data: '.length)).choices[0].delta);
  const text = chunks.map((delta) => delta.content ?? '').join('');
  const call = chunks.find((delta) => delta.tool_calls)?.tool_calls[0];
  const args = chunks.flatMap((delta) => delta.tool_calls ?? []).map((entry) => entry.function.arguments).join('');
  return { text, tool: call ? { name: call.function.name, args: JSON.parse(args) } : null };
}

describe('scriptedAgent over the stub provider', () => {
  it('can require both judge passes and return a provider context limit', async () => {
    let oversized = false;
    const stub = await startPiStubProvider({ agent: () => ({ text: 'unused' }), judge: () => oversized ?
      { error: 'context_length_exceeded' } : { verdict: 'allow', forceIntent: true } });
    const messages = (pass) => [
      { role: 'system', content: 'You are a security monitor for an autonomous coding agent' },
      { role: 'user', content: `This is pass ${pass}` },
    ];
    try {
      expect((await ask(stub, messages(1))).text).toContain('<severity>60</severity>');
      expect((await ask(stub, messages(2))).text).toContain('<severity>5</severity>');
      oversized = true;
      const response = await fetch(`${stub.baseUrl}/chat/completions`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ messages: messages(1) }),
      });
      expect(response.status).toBe(400);
      expect(await response.json()).toEqual({ error: { message: 'context_length_exceeded', code: 'context_length_exceeded' } });
    } finally { await stub.close(); }
  });

  it('issues each scripted tool once, then the text, keyed on the latest prompt', async () => {
    const stub = await startPiStubProvider({
      agent: scriptedAgent([
        { when: 'sleep 45', tools: [bash('sleep 45')], text: 'done' },
        { when: 'alpha', text: 'alpha' },
        { when: 'What word did I ask you to remember', text: rememberedWord },
      ]),
      judge: () => ({ verdict: 'allow' }),
    });
    try {
      const system = { role: 'system', content: 'You are pi.' };
      expect(await ask(stub, [system, { role: 'user', content: 'Reply with exactly one word: alpha' }]))
        .toEqual({ text: 'alpha', tool: null });

      const hold = [system, { role: 'user', content: [{ type: 'text', text: 'Run the bash command `sleep 45`, then say done.' }] }];
      expect((await ask(stub, hold)).tool).toEqual({ name: 'bash', args: { command: 'sleep 45' } });
      const afterTool = [
        ...hold,
        { role: 'assistant', content: null, tool_calls: [{ id: 'c1', type: 'function', function: { name: 'bash', arguments: '{}' } }] },
        { role: 'tool', tool_call_id: 'c1', content: '' },
      ];
      expect(await ask(stub, afterTool)).toEqual({ text: 'done', tool: null });

      const remembered = [
        system,
        { role: 'user', content: 'Remember this word: marmalade. Reply with exactly one word: alpha' },
        { role: 'assistant', content: 'alpha' },
        { role: 'user', content: 'What word did I ask you to remember? Reply with only that word.' },
      ];
      expect((await ask(stub, remembered)).text).toBe('marmalade');
      expect((await ask(stub, [system, { role: 'user', content: 'something unscripted' }])).text)
        .toContain('no scripted reply');
      expect(stub.calls.agent.map((call) => call.toolResults.length)).toEqual([0, 0, 1, 0, 0]);
    } finally {
      await stub.close();
    }
  });
});
