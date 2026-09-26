import { describe, expect, it } from 'vitest';
import { computeWarmWorkspaceIds } from './terminalVirtualization';

const ABCDE = ['a', 'b', 'c', 'd', 'e'];

describe('computeWarmWorkspaceIds', () => {
  it.each<[string, string[], string[], string | null, number, string[], string[] | null]>([
    ['keeps everything live when virtualization is off', ['a', 'b', 'c'], ['a', 'b', 'c'], 'a', -1, [], null],
    ['keeps a lone active workspace', ['a'], ['a'], 'a', 3, [], ['a']],
    ['warms nothing unvisited, even within the budget', ['a', 'b', 'c', 'd'], ['a'], 'a', 3, [], ['a']],
    ['warms nothing on the dashboard before a workspace is selected', ['a', 'b'], [], null, 3, [], []],
    ['keeps only the active workspace at limit 0', ['a', 'b', 'c'], ['a', 'b', 'c'], 'a', 0, [], ['a']],
    ['keeps the active and the most recent up to the limit', ABCDE, ABCDE, 'a', 3, [], ['a', 'b', 'c', 'd']],
    ['keeps an active workspace missing from recents', ['z', 'b', 'c'], ['b', 'c'], 'z', 1, [], ['b', 'z']],
    ['counts an active workspace in recents once', ['a', 'b', 'c', 'd'], ['a', 'b', 'c'], 'a', 2, [], ['a', 'b', 'c']],
    ['fills the budget from recents with no active workspace', ['a', 'b', 'c'], ['a', 'b', 'c'], null, 1, [], ['a', 'b']],
    ['does not fill unused slots before recency is known', ABCDE, [], 'a', 2, [], ['a']],
    ['ignores stale active and recent ids', ['a', 'b', 'c', 'd'], ['x', 'b'], 'y', 1, [], ['b']],
    ['keeps a required visible workspace that is cold', ABCDE, ['a', 'b'], 'a', 1, ['e'], ['a', 'e']],
    ['lets required visible workspaces exceed the budget', ABCDE, ['a', 'b'], 'a', 1, ['d', 'e'], ['a', 'd', 'e']],
    ['ignores stale required ids', ['a', 'b', 'c', 'd'], ['a', 'b', 'c'], 'a', 1, ['missing'], ['a', 'b']],
  ])('%s', (_name, all, recents, active, limit, required, expected) => {
    const warm = computeWarmWorkspaceIds(all, recents, active, limit, required);

    expect(warm && [...warm].sort()).toEqual(expected);
    if (!warm) return;
    for (const id of [active, ...required]) {
      if (id && all.includes(id)) expect(warm.has(id)).toBe(true);
    }
    expect([...warm].every((id) => all.includes(id))).toBe(true);
    expect(warm.size).toBeLessThanOrEqual(limit + 1 + required.length);
  });
});
