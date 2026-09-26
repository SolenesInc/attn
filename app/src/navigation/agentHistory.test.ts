import { describe, expect, it } from 'vitest';
import {
  AGENT_HISTORY_LIMIT,
  createAgentHistory,
  moveAgentHistory,
  recordAgentVisit,
  reconcileAgentHistory,
} from './agentHistory';

function run(script: string[]) {
  let state = createAgentHistory();
  const live = new Set(script.filter((op) => /^\w/.test(op) && op !== 'sync'));
  const targets: (string | null)[] = [];
  for (const op of script) {
    if (op === '<' || op === '>' || op === '<home' || op === '>home') {
      const move = moveAgentHistory(state, op[0] === '<' ? 'back' : 'forward', live, op.endsWith('home'));
      state = move.state;
      targets.push(move.targetSessionId);
    } else if (op.startsWith('-')) {
      live.delete(op.slice(1));
    } else if (op === 'sync') {
      state = reconcileAgentHistory(state, live);
    } else {
      state = recordAgentVisit(state, op === '.' ? '' : op);
    }
  }
  return { entries: state.entries.join(' '), cursor: state.cursor, targets };
}

const MANY = Array.from({ length: AGENT_HISTORY_LIMIT + 1 }, (_, index) => `s${index}`);

describe('agent history', () => {
  it.each<[string, string[], string, number, (string | null)[]]>([
    ['starts empty', [], '', -1, []],
    ['records visits in order', ['a', 'b'], 'a b', 1, []],
    ['ignores an empty visit', ['a', '.'], 'a', 0, []],
    ['ignores a consecutive repeat', ['a', 'b', 'b'], 'a b', 1, []],
    ['records a non-consecutive repeat', ['a', 'b', 'a'], 'a b a', 2, []],
    ['moves back and forward without recording', ['a', 'b', 'c', '<', '>'], 'a b c', 2, ['b', 'c']],
    ['stays put going forward from the newest visit', ['a', 'b', 'c', '>'], 'a b c', 2, [null]],
    ['stays put going back from the oldest visit', ['a', '<'], 'a', 0, [null]],
    ['drops the forward branch when visiting after going back', ['a', 'b', 'c', '<', 'd'], 'a b d', 2, ['b']],
    ['keeps only the newest visits at the limit', MANY, MANY.slice(1).join(' '), AGENT_HISTORY_LIMIT - 1, []],
    ['resumes the current agent when going back from a non-session view', ['a', 'b', '<home'], 'a b', 1, ['b']],
    ['does nothing going forward from a non-session view', ['a', 'b', '>home'], 'a b', 1, [null]],
    ['keeps repeats and the cursor when a session closes', ['a', 'b', 'a', 'c', 'd', '<', '-b', 'sync'], 'a a c d', 2, ['c']],
    ['empties when every session closes', ['a', 'b', '-a', '-b', 'sync'], '', -1, []],
    ['skips a closed session while moving', ['a', 'b', 'c', '-b', '<'], 'a c', 0, ['a']],
    ['returns nothing when every session closed before moving', ['a', '-a', '<'], '', -1, [null]],
  ])('%s', (_name, script, entries, cursor, targets) => {
    expect(run(script)).toEqual({ entries, cursor, targets });
  });
});
