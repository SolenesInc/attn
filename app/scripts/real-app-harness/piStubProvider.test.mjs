// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { bash, rememberedWord, scriptedAgent, startPiStubProvider, stubAgentModel } from './piStubProvider.mjs';

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
