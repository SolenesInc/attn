import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';

function wsctl(world, args) {
  return execFileSync(world.wsctl, args, { env: world.env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
}

function recommendedAnswer(question) {
  if (question.kind === 'text') return { key: question.key, picked: [], text: 'Use your judgment.' };
  return { key: question.key, picked: [question.recommended || question.choices[0].label] };
}

export function openAsks(world, session) {
  const { asks, overseen } = world.attnJson(['ask', 'list', '--session', session]);
  return [...asks, ...overseen.map(entry => entry.ask)].filter(ask => !ask.reply);
}

export function answerAsks(world, session) {
  const answered = [];
  for (const ask of openAsks(world, session)) {
    const answers = ask.parts.filter(part => part.question).map(part => recommendedAnswer(part.question));
    const reply = JSON.parse(wsctl(world, ['request', '--event', 'ask_answer_result', '--json',
      JSON.stringify({ cmd: 'ask_answer', request_id: randomUUID(), ask_id: ask.id, answers })]).replace(/^\[wsctl[^\n]*\n/, ''));
    if (!reply.success) throw new Error(`ask_answer ${ask.id}: ${reply.error}`);
    answered.push({ id: ask.id, subject: ask.subject, answers });
  }
  return answered;
}

export function type(world, session, text) {
  wsctl(world, ['input', '--id', session, '--text', text]);
  wsctl(world, ['input', '--id', session, '--enter']);
}
