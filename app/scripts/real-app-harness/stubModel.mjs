import http from 'node:http';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function textOf(content) {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return content == null ? '' : JSON.stringify(content);
  return content.map((part) => (typeof part === 'string' ? part : (part?.text ?? part?.content ?? ''))).join('');
}

function stringOrJSON(content) {
  return typeof content === 'string' ? content : JSON.stringify(content);
}

function sseWriter(res, { named }) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' });
  return (event, payload) => {
    if (named) res.write(`event: ${event}\n`);
    res.write(`data: ${typeof payload === 'string' ? payload : JSON.stringify(payload)}\n\n`);
  };
}

function pieces(text, pace) {
  if (!pace || !text) return [text];
  return text.match(new RegExp(`[\\s\\S]{1,${pace.chars}}`, 'g'));
}

async function streamText(text, pace, write) {
  for (const piece of pieces(text, pace)) {
    write(piece);
    if (pace) await delay(pace.ms);
  }
}

function isTypedPrompt(text) {
  const trimmed = text.trimStart();
  return trimmed.length > 0 && !trimmed.startsWith('<') && !trimmed.startsWith('[') && !trimmed.startsWith('# AGENTS.md');
}

const chatCompletions = {
  matches: (path) => path.endsWith('/chat/completions'),
  parse(body) {
    const messages = body.messages ?? [];
    const lastUser = messages.findLastIndex((m) => m.role === 'user');
    const prompts = messages.filter((m) => m.role === 'user').map((m) => textOf(m.content));
    return {
      tools: (body.tools ?? []).map((tool) => tool.function?.name ?? tool.name),
      systemPrompt: messages
        .filter((m) => m.role === 'system' || m.role === 'developer')
        .map((m) => stringOrJSON(m.content))
        .join('\n'),
      prompts,
      prompt: prompts.at(-1) ?? '',
      toolResults: messages.slice(lastUser + 1).filter((m) => m.role === 'tool').map((m) => textOf(m.content)),
      messages,
    };
  },
  async write(res, answer, { pace }) {
    const sse = sseWriter(res, { named: false });
    const chunk = (delta, finishReason = null) => ({
      id: 'attn-stub-model',
      object: 'chat.completion.chunk',
      created: 1,
      model: 'stub',
      choices: [{ index: 0, delta, finish_reason: finishReason }],
    });
    if (answer.say != null) {
      let first = true;
      await streamText(answer.say, pace, (text) => {
        sse(null, chunk(first ? { role: 'assistant', content: text } : { content: text }));
        first = false;
      });
    }
    if (answer.tool) {
      sse(null, chunk({ tool_calls: [{ index: 0, id: answer.tool.id, type: 'function', function: { name: answer.tool.name, arguments: '' } }] }));
      sse(null, chunk({ tool_calls: [{ index: 0, function: { arguments: stringOrJSON(answer.tool.args) } }] }));
      sse(null, { ...chunk({}, 'tool_calls'), usage: { prompt_tokens: 8, completion_tokens: 6, total_tokens: 14 } });
    } else {
      sse(null, { ...chunk({}, 'stop'), usage: { prompt_tokens: 8, completion_tokens: 4, total_tokens: 12 } });
    }
    sse(null, '[DONE]');
  },
};

function anthropicIsPrompt(message) {
  if (message.role !== 'user') return false;
  if (typeof message.content === 'string') return isTypedPrompt(message.content);
  return !message.content.some((part) => part.type === 'tool_result')
    && message.content.some((part) => part.type === 'text' && isTypedPrompt(part.text));
}

const anthropicMessages = {
  matches: (path) => path.endsWith('/v1/messages'),
  parse(body) {
    const messages = body.messages ?? [];
    const promptMessages = messages.filter(anthropicIsPrompt);
    const prompts = promptMessages.map((m) => (typeof m.content === 'string'
      ? m.content
      : m.content.filter((part) => part.type === 'text' && isTypedPrompt(part.text)).map((part) => part.text).join('\n')));
    const lastPrompt = messages.lastIndexOf(promptMessages.at(-1));
    const toolResults = messages.slice(lastPrompt + 1)
      .filter((m) => m.role === 'user' && Array.isArray(m.content))
      .flatMap((m) => m.content.filter((part) => part.type === 'tool_result').map((part) => textOf(part.content)));
    const system = [body.system, ...messages.filter((m) => m.role === 'system').map((m) => m.content)];
    return {
      tools: (body.tools ?? []).map((tool) => tool.name),
      systemPrompt: system.filter(Boolean).map(textOf).join('\n'),
      prompts,
      prompt: prompts.at(-1) ?? '',
      toolResults,
      messages,
    };
  },
  async write(res, answer, { pace, body }) {
    const stopSequence = (body.stop_sequences ?? []).find((sequence) => answer.say?.includes(sequence));
    const say = stopSequence ? answer.say.slice(0, answer.say.indexOf(stopSequence)) : answer.say;
    const stopReason = answer.tool ? 'tool_use' : (stopSequence ? 'stop_sequence' : 'end_turn');
    if (!body.stream) {
      const content = [
        ...(say != null ? [{ type: 'text', text: say }] : []),
        ...(answer.tool ? [{ type: 'tool_use', id: answer.tool.id, name: answer.tool.name, input: answer.tool.args }] : []),
      ];
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.write(JSON.stringify({
        id: `msg_stub_${Date.now()}`, type: 'message', role: 'assistant', model: body.model, content,
        stop_reason: stopReason, stop_sequence: stopSequence ?? null, usage: { input_tokens: 1200, output_tokens: 42 },
      }));
      return;
    }
    const sse = sseWriter(res, { named: true });
    const send = (type, payload) => sse(type, { type, ...payload });
    send('message_start', {
      message: {
        id: `msg_stub_${Date.now()}`, type: 'message', role: 'assistant', model: body.model, content: [],
        stop_reason: null, stop_sequence: null,
        usage: { input_tokens: 1200, output_tokens: 1, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 },
      },
    });
    let index = 0;
    if (say != null) {
      send('content_block_start', { index, content_block: { type: 'text', text: '' } });
      await streamText(say, pace, (text) => send('content_block_delta', { index, delta: { type: 'text_delta', text } }));
      send('content_block_stop', { index });
      index += 1;
    }
    if (answer.tool) {
      send('content_block_start', { index, content_block: { type: 'tool_use', id: answer.tool.id, name: answer.tool.name, input: {} } });
      send('content_block_delta', { index, delta: { type: 'input_json_delta', partial_json: JSON.stringify(answer.tool.args) } });
      send('content_block_stop', { index });
    }
    send('message_delta', { delta: { stop_reason: stopReason, stop_sequence: stopSequence ?? null }, usage: { output_tokens: 42 } });
    send('message_stop', {});
  },
};

const TOOL_OUTPUTS = new Set(['function_call_output', 'custom_tool_call_output']);

function responsesIsPrompt(item) {
  return item.type === 'message' && item.role === 'user' && isTypedPrompt(textOf(item.content));
}

const openaiResponses = {
  matches: (path) => path.endsWith('/responses'),
  parse(body) {
    const input = Array.isArray(body.input) ? body.input : [];
    const promptItems = input.filter(responsesIsPrompt);
    const prompts = promptItems.map((item) => textOf(item.content));
    const lastPrompt = input.lastIndexOf(promptItems.at(-1));
    return {
      tools: (body.tools ?? []).map((tool) => tool.name ?? tool.type),
      systemPrompt: [body.instructions, ...input.filter((item) => item.role === 'developer' || item.role === 'system').map((item) => textOf(item.content))]
        .filter(Boolean)
        .join('\n'),
      prompts,
      prompt: prompts.at(-1) ?? '',
      toolResults: input.slice(lastPrompt + 1).filter((item) => TOOL_OUTPUTS.has(item.type)).map((item) => textOf(item.output)),
      messages: input,
    };
  },
  async write(res, answer, { pace, body }) {
    const sse = sseWriter(res, { named: true });
    let sequence = 0;
    const send = (type, payload) => sse(type, { type, sequence_number: sequence++, ...payload });
    const response = { id: `resp_stub_${Date.now()}`, object: 'response', created_at: Math.floor(Date.now() / 1000), status: 'in_progress', model: body.model, output: [] };
    send('response.created', { response });
    const output = [];
    if (answer.say != null) {
      const outputIndex = output.length;
      const item = { id: `msg_stub_${Date.now()}`, type: 'message', role: 'assistant', status: 'in_progress', content: [] };
      send('response.output_item.added', { output_index: outputIndex, item });
      send('response.content_part.added', { item_id: item.id, output_index: outputIndex, content_index: 0, part: { type: 'output_text', text: '', annotations: [] } });
      await streamText(answer.say, pace, (delta) => send('response.output_text.delta', { item_id: item.id, output_index: outputIndex, content_index: 0, delta }));
      const done = { ...item, status: 'completed', content: [{ type: 'output_text', text: answer.say, annotations: [] }] };
      send('response.output_item.done', { output_index: outputIndex, item: done });
      output.push(done);
    }
    if (answer.tool) {
      const outputIndex = output.length;
      const item = typeof answer.tool.args === 'string'
        ? { id: `ctc_${answer.tool.id}`, type: 'custom_tool_call', status: 'completed', call_id: answer.tool.id, name: answer.tool.name, input: answer.tool.args }
        : { id: `fc_${answer.tool.id}`, type: 'function_call', status: 'completed', call_id: answer.tool.id, name: answer.tool.name, arguments: JSON.stringify(answer.tool.args) };
      send('response.output_item.added', { output_index: outputIndex, item });
      send('response.output_item.done', { output_index: outputIndex, item });
      output.push(item);
    }
    send('response.completed', {
      response: {
        ...response,
        status: 'completed',
        output,
        usage: { input_tokens: 1200, input_tokens_details: { cached_tokens: 0 }, output_tokens: 42, output_tokens_details: { reasoning_tokens: 0 }, total_tokens: 1242 },
      },
    });
  },
};

const DIALECTS = { chatCompletions, anthropicMessages, openaiResponses };

function refuseWebsocketSoClientsStreamOverHTTP(req, socket) {
  socket.end('HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n');
}

function sideRoute(req, res) {
  const path = req.url.split('?')[0];
  if (path.endsWith('/count_tokens')) {
    res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"input_tokens":1200}');
  } else if (path.endsWith('/models')) {
    res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"data":[],"models":[]}');
  } else {
    res.writeHead(200, { 'Content-Type': 'text/plain' }).end('STUB-OK\n');
  }
}

export function startStubModel(route, { pace = null, onError = () => {} } = {}) {
  const server = http.createServer((req, res) => {
    const path = req.url.split('?')[0];
    const [dialect, wire] = Object.entries(DIALECTS).find(([, candidate]) => candidate.matches(path)) ?? [];
    let raw = '';
    req.on('data', (piece) => { raw += piece; });
    req.on('end', async () => {
      if (!wire || req.method !== 'POST') {
        sideRoute(req, res);
        return;
      }
      let body;
      try {
        body = JSON.parse(raw);
      } catch (error) {
        res.writeHead(400).end(String(error));
        return;
      }
      const request = { dialect, path, model: body.model, userAgent: String(req.headers['user-agent'] ?? ''), body, ...wire.parse(body) };
      let answer;
      try {
        answer = await route(request);
      } catch (error) {
        onError(error, request);
        answer = { say: `stub model failed: ${error?.message ?? error}` };
      }
      if (answer.error) {
        res.writeHead(answer.status ?? 400, { 'Content-Type': 'application/json' })
          .end(JSON.stringify({ error: { message: answer.error, code: answer.error } }));
        return;
      }
      if (answer.holdMs) await delay(answer.holdMs);
      await wire.write(res, answer, { pace, body });
      res.end();
    });
  });
  server.on('upgrade', refuseWebsocketSoClientsStreamOverHTTP);

  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({
        port,
        origin: `http://127.0.0.1:${port}`,
        close: () => new Promise((done) => { server.closeAllConnections(); server.close(() => done()); }),
      });
    });
  });
}
