import { describe, expect, it } from 'vitest';
import { resolveScenarios, scenarioCatalog } from './scenarioCatalog.mjs';
import {
  aggregateShards,
  parseShardSelector,
  planShards,
  readRecordedDurations,
  selectShard,
} from './shardPlan.mjs';

const LINUX_SKIP = { linux: 'no display for this one' };

function scenario(id, extra = {}) {
  return { id, label: id, command: ['node', `scenario-${id}.mjs`], ...extra };
}

describe('parseShardSelector', () => {
  it('reads an index and a count', () => {
    expect(parseShardSelector('2/4')).toEqual({ index: 2, count: 4 });
  });

  it('names the expected shape', () => {
    expect(() => parseShardSelector('2 of 4')).toThrow(/expected <index>\/<count>/);
    expect(() => parseShardSelector('0/4')).toThrow(/between 1 and 4/);
    expect(() => parseShardSelector('5/4')).toThrow(/between 1 and 4/);
  });
});

describe('planShards', () => {
  const durations = {
    long: 90, medium: 40, short: 10, tiny: 5,
  };
  const scenarios = [scenario('short'), scenario('long'), scenario('tiny'), scenario('medium')];

  it('packs the longest scenarios first so the shards even out', () => {
    const shards = planShards(scenarios, durations, 2, 'linux', {});
    expect(shards.map((shard) => shard.seconds)).toEqual([90, 55]);
    expect(shards.map((shard) => shard.ids)).toEqual([['long'], ['medium', 'short', 'tiny']]);
  });

  it('gives every scenario to exactly one shard', () => {
    const assigned = planShards(scenarios, durations, 3, 'linux', {}).flatMap((shard) => shard.ids);
    expect(assigned.sort()).toEqual(['long', 'medium', 'short', 'tiny']);
  });

  it('plans the same way every time', () => {
    const once = planShards(scenarios, durations, 3, 'linux', {});
    const twice = planShards([...scenarios].reverse(), durations, 3, 'linux', {});
    expect(twice).toEqual(once);
  });

  it('places a platform skip without asking for a duration', () => {
    const withSkip = [...scenarios, scenario('elsewhere', { skipOn: LINUX_SKIP })];
    const shards = planShards(withSkip, durations, 2, 'linux', {});
    expect(shards.flatMap((shard) => shard.ids)).toContain('elsewhere');
    expect(shards.reduce((total, shard) => total + shard.seconds, 0)).toBe(145);
  });

  it('names the scenarios with no recorded duration', () => {
    expect(() => planShards([...scenarios, scenario('brand-new')], durations, 2, 'linux', {}))
      .toThrow(/No recorded duration for brand-new/);
  });

  it('refuses more shards than there is work to spread', () => {
    expect(() => planShards(scenarios, durations, 9, 'linux', {}))
      .toThrow(/Shard count 9 exceeds the 4 scenarios/);
  });
});

describe('selectShard', () => {
  it('returns the scenarios that shard owns, in catalog order', () => {
    const durations = { a: 30, b: 20, c: 20 };
    const scenarios = [scenario('a'), scenario('b'), scenario('c')];
    const shard = selectShard(scenarios, durations, { index: 2, count: 2 }, 'linux', {});
    expect(shard.ids).toEqual(['b', 'c']);
    expect(shard.scenarios.map((entry) => entry.id)).toEqual(['b', 'c']);
    expect(shard.seconds).toBe(40);
  });
});

describe('the recorded durations file', () => {
  it('carries a duration for every matrix scenario Linux actually runs', () => {
    const scenarios = resolveScenarios([], scenarioCatalog);
    expect(() => planShards(scenarios, readRecordedDurations(), 4, 'linux', {})).not.toThrow();
  });
});

function shardResult(index, count, results, source = `shard-${index}/last-matrix.json`) {
  return { shard: { index, count }, results, source };
}

describe('aggregateShards', () => {
  const expectedIds = ['a', 'b', 'c'];

  it('accepts a run whose shards cover the catalog exactly once', () => {
    const aggregate = aggregateShards({
      shardResults: [
        shardResult(1, 2, [{ id: 'a', code: 0, durationMs: 1_000 }]),
        shardResult(2, 2, [{ id: 'b', code: 0, durationMs: 2_000 }, { id: 'c', code: 0, durationMs: 500 }]),
      ],
      expectedIds,
      shardCount: 2,
    });
    expect(aggregate.ok).toBe(true);
    expect(aggregate.results.map((result) => result.id)).toEqual(['a', 'b', 'c']);
    expect(aggregate.results.map((result) => result.shard)).toEqual([1, 2, 2]);
  });

  it('reds the aggregate when any shard reports a failure', () => {
    const aggregate = aggregateShards({
      shardResults: [
        shardResult(1, 2, [{ id: 'a', code: 0 }]),
        shardResult(2, 2, [{ id: 'b', code: 1, outputTail: 'boom' }, { id: 'c', code: 0 }]),
      ],
      expectedIds,
      shardCount: 2,
    });
    expect(aggregate.ok).toBe(false);
    expect(aggregate.failed.map((result) => result.id)).toEqual(['b']);
    expect(aggregate.problems).toEqual([]);
  });

  it('names a scenario no shard ran', () => {
    const aggregate = aggregateShards({
      shardResults: [
        shardResult(1, 2, [{ id: 'a', code: 0 }]),
        shardResult(2, 2, [{ id: 'b', code: 0 }]),
      ],
      expectedIds,
      shardCount: 2,
    });
    expect(aggregate.ok).toBe(false);
    expect(aggregate.problems).toEqual(['c ran in no shard']);
  });

  it('names a shard that reported nothing', () => {
    const aggregate = aggregateShards({
      shardResults: [shardResult(1, 2, [{ id: 'a', code: 0 }, { id: 'b', code: 0 }, { id: 'c', code: 0 }])],
      expectedIds,
      shardCount: 2,
    });
    expect(aggregate.ok).toBe(false);
    expect(aggregate.problems).toEqual(['shard 2/2 reported no results; its job did not finish the matrix']);
  });

  it('names a scenario two shards both ran', () => {
    const aggregate = aggregateShards({
      shardResults: [
        shardResult(1, 2, [{ id: 'a', code: 0 }, { id: 'c', code: 0 }]),
        shardResult(2, 2, [{ id: 'b', code: 0 }, { id: 'c', code: 0 }]),
      ],
      expectedIds,
      shardCount: 2,
    });
    expect(aggregate.ok).toBe(false);
    expect(aggregate.problems).toEqual(['c ran in more than one shard']);
  });

  it('names a shard planned against a different shard count', () => {
    const aggregate = aggregateShards({
      shardResults: [
        shardResult(1, 2, [{ id: 'a', code: 0 }]),
        shardResult(2, 3, [{ id: 'b', code: 0 }, { id: 'c', code: 0 }]),
      ],
      expectedIds,
      shardCount: 2,
    });
    expect(aggregate.ok).toBe(false);
    expect(aggregate.problems).toEqual(['shard-2/last-matrix.json: reports 3 shards, expected 2']);
  });

  it('names a result that is not in the catalog at all', () => {
    const aggregate = aggregateShards({
      shardResults: [
        shardResult(1, 2, [{ id: 'a', code: 0 }, { id: 'b', code: 0 }]),
        shardResult(2, 2, [{ id: 'c', code: 0 }, { id: 'ghost', code: 0 }]),
      ],
      expectedIds,
      shardCount: 2,
    });
    expect(aggregate.ok).toBe(false);
    expect(aggregate.problems).toEqual(['ghost is not in the matrix catalog but a shard reported it']);
  });
});
