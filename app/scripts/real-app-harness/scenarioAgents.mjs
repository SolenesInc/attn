import { writeMockAgentFixture } from './mockAgent.mjs';

export function writeQueueAgentFixture(cwd, { minimumWorkingMs, turns = [] } = {}) {
  return writeMockAgentFixture(cwd, {
    name: 'queue mock',
    ...(minimumWorkingMs === undefined ? {} : { minimumWorkingMs }),
    defaultActions: [{ type: 'reply', text: 'Which part should I start with?', state: 'waiting_input' }],
    turns,
  });
}
