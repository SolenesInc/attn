import path from 'node:path';

export const VERDICTS = { ask: 'WAITING', done: 'DONE' };
export const SETTLED_STATE = { WAITING: 'waiting_input', DONE: 'idle' };

export function turn() {
  const steps = [];
  let current = {};
  let title = null;
  let verdict = null;
  let held = false;
  const close = () => {
    steps.push(current);
    current = {};
  };
  const builder = {
    title(text) {
      title = text;
      return builder;
    },
    say(text) {
      if (current.say != null) close();
      current.say = text;
      return builder;
    },
    read(file) {
      current.tool = { kind: 'read', file };
      close();
      return builder;
    },
    edit(file, { from, to }) {
      current.tool = { kind: 'edit', file, from, to };
      close();
      return builder;
    },
    run(command) {
      current.tool = { kind: 'run', command };
      close();
      return builder;
    },
    ask(text) {
      return builder.end(text, VERDICTS.ask);
    },
    done(text) {
      return builder.end(text, VERDICTS.done);
    },
    hold() {
      if (current.say != null) {
        throw new Error('hold() right after say() would end the turn first; put a read(), edit() or run() between them');
      }
      held = true;
      steps.push({ held: true });
      return builder;
    },
    end(text, endVerdict) {
      if (current.say != null) close();
      steps.push({ say: text });
      verdict = endVerdict;
      return builder;
    },
    build() {
      if (current.say != null) close();
      if (!held && !verdict) throw new Error('a turn ends with ask(), done() or hold()');
      return { steps, title, verdict, held, finalText: verdict ? steps.at(-1).say : null };
    },
  };
  return builder;
}

function codexPatch(file, from, to) {
  const lines = (text, mark) => text.split('\n').map((line) => `${mark}${line}`);
  return ['*** Begin Patch', `*** Update File: ${file}`, '@@', ...lines(from, '-'), ...lines(to, '+'), '*** End Patch', ''].join('\n');
}

function toolCall(dialect, tool, cwd, id) {
  const codex = dialect === 'openaiResponses';
  switch (tool.kind) {
    case 'read':
      return codex
        ? { id, name: 'exec_command', args: { cmd: `cat ${tool.file}` } }
        : { id, name: 'Read', args: { file_path: path.join(cwd, tool.file) } };
    case 'edit':
      return codex
        ? { id, name: 'apply_patch', args: codexPatch(tool.file, tool.from, tool.to) }
        : { id, name: 'Edit', args: { file_path: path.join(cwd, tool.file), old_string: tool.from, new_string: tool.to } };
    case 'run':
      return codex
        ? { id, name: 'exec_command', args: { cmd: tool.command } }
        : { id, name: 'Bash', args: { command: tool.command, description: tool.command } };
    default:
      throw new Error(`unknown tool step ${tool.kind}`);
  }
}

function titleFrom(prompt) {
  const words = prompt.replace(/[^\w\s-]/g, '').split(/\s+/).filter(Boolean).slice(0, 4);
  return words.map((word, index) => (index === 0 ? word[0].toUpperCase() + word.slice(1) : word.toLowerCase())).join(' ');
}

function words(text) {
  return (text.match(/[A-Za-z]{3,}/g) ?? []).join(' ');
}

function isAutoModeReview(request) {
  return request.tools.length === 0 && request.systemPrompt.includes('<severity>N</severity>');
}

function isAttnClassifier(request) {
  return request.tools.includes('StructuredOutput') || request.userAgent.startsWith('codex_exec');
}

export function createTurnRouter({ thinkMs }) {
  const turns = [];
  let nextToolId = 0;

  const scripted = (request) => turns.findLast((entry) => request.prompt === entry.prompt);
  const mentioned = (request) => {
    const haystack = JSON.stringify(request.body);
    return turns.findLast((entry) => haystack.includes(entry.prompt));
  };

  const classify = (request) => {
    const haystack = words(JSON.stringify(request.body));
    const answered = turns.findLast((entry) => entry.spec.finalText && haystack.includes(words(entry.spec.finalText.split('\n')[0])));
    const verdict = answered?.spec.verdict ?? VERDICTS.done;
    if (request.dialect === 'anthropicMessages') {
      return { tool: { id: `toolu_demo_${nextToolId++}`, name: 'StructuredOutput', args: { verdict } } };
    }
    return { say: verdict };
  };

  const route = async (request) => {
    if (isAttnClassifier(request)) return classify(request);
    if (isAutoModeReview(request)) return { say: '<severity>5</severity>' };
    if (request.tools.length === 0) {
      const entry = mentioned(request);
      return { say: entry?.spec.title ?? (entry ? titleFrom(entry.prompt) : 'Demo session') };
    }
    const entry = scripted(request);
    if (!entry) {
      return { say: `The demo has no scripted reply for "${request.prompt.slice(0, 120)}". Register it with session.prompt(text, turn).` };
    }
    const index = request.toolResults.length;
    const step = entry.spec.steps[index];
    if (!step) {
      return { say: `The scripted turn for "${entry.prompt}" has ${entry.spec.steps.length} steps; the agent asked for step ${index + 1}.` };
    }
    if (step.held) {
      await new Promise(() => {});
    }
    const answer = { holdMs: thinkMs };
    if (step.say != null) answer.say = step.say;
    if (step.tool) answer.tool = toolCall(request.dialect, step.tool, entry.cwd, `toolu_demo_${nextToolId++}`);
    return answer;
  };

  return {
    route,
    expect(prompt, builder, { cwd }) {
      const spec = builder.build();
      turns.push({ prompt, spec, cwd });
      return spec;
    },
  };
}
