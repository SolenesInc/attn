import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { scenarioSkipReason } from './matrixDigest.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
export const durationsPath = path.join(HARNESS_DIR, 'scenario-durations.json');

export function readRecordedDurations(file = durationsPath) {
  return JSON.parse(fs.readFileSync(file, 'utf8')).seconds;
}

export function parseShardSelector(raw) {
  const match = /^([0-9]+)\/([0-9]+)$/.exec(String(raw).trim());
  if (!match) {
    throw new Error(`Invalid --shard value: ${raw} (expected <index>/<count>, e.g. 2/4)`);
  }
  const index = Number(match[1]);
  const count = Number(match[2]);
  if (index < 1 || index > count) {
    throw new Error(`Invalid --shard value: ${raw} (index must be between 1 and ${count})`);
  }
  return { index, count };
}

// Longest-processing-time bin packing. Scenarios are indivisible, so the
// slowest one is the floor on any shard: 91.6s of a 761.5s matrix (run 34060183460).
export function planShards(scenarios, durationSeconds, shardCount, platform = process.platform, env = process.env) {
  if (!Number.isInteger(shardCount) || shardCount < 1) {
    throw new Error(`Invalid shard count: ${shardCount} (expected a positive integer)`);
  }
  if (shardCount > scenarios.length) {
    throw new Error(`Shard count ${shardCount} exceeds the ${scenarios.length} scenarios to run`);
  }
  const missing = scenarios
    .filter((scenario) => !scenarioSkipReason(scenario, platform, env))
    .filter((scenario) => typeof durationSeconds[scenario.id] !== 'number')
    .map((scenario) => scenario.id);
  if (missing.length > 0) {
    throw new Error([
      `No recorded duration for ${missing.join(', ')}, so the shards cannot be balanced.`,
      `Add each id to ${path.relative(process.cwd(), durationsPath)} with the seconds a green`,
      'matrix-digest.txt reports for it, and update the receipt block in that file.',
    ].join('\n'));
  }

  const shards = Array.from({ length: shardCount }, (_, offset) => ({ index: offset + 1, seconds: 0, ids: [] }));
  const weight = (scenario) => (scenarioSkipReason(scenario, platform, env) ? 0 : durationSeconds[scenario.id]);
  // Ties broken by id so the same catalog always plans to the same shards.
  const ordered = [...scenarios].sort((a, b) => (weight(b) - weight(a)) || a.id.localeCompare(b.id));
  for (const scenario of ordered) {
    const lightest = shards.reduce((a, b) => (b.seconds < a.seconds ? b : a));
    lightest.seconds += weight(scenario);
    lightest.ids.push(scenario.id);
  }
  return shards;
}

export function selectShard(scenarios, durationSeconds, { index, count }, platform = process.platform, env = process.env) {
  const shard = planShards(scenarios, durationSeconds, count, platform, env)[index - 1];
  const owned = new Set(shard.ids);
  return { ...shard, scenarios: scenarios.filter((scenario) => owned.has(scenario.id)) };
}

function describeShardCoverage(shardResults, shardCount) {
  const problems = [];
  const seen = new Map();
  for (const shard of shardResults) {
    if (shard.shard?.count !== shardCount) {
      problems.push(`${shard.source}: reports ${shard.shard?.count ?? 'no'} shards, expected ${shardCount}`);
    }
    const index = shard.shard?.index;
    if (seen.has(index)) {
      problems.push(`shard ${index} reported twice: ${seen.get(index)} and ${shard.source}`);
      continue;
    }
    seen.set(index, shard.source);
  }
  for (let index = 1; index <= shardCount; index += 1) {
    if (!seen.has(index)) {
      problems.push(`shard ${index}/${shardCount} reported no results; its job did not finish the matrix`);
    }
  }
  return problems;
}

// The aggregate is the gate, so it re-derives what should have run instead of
// trusting the shards: a scenario silently dropped by a bad plan reds it.
export function aggregateShards({ shardResults, expectedIds, shardCount }) {
  const problems = describeShardCoverage(shardResults, shardCount);
  const byId = new Map();
  const duplicated = [];
  for (const shard of shardResults) {
    for (const result of shard.results || []) {
      if (byId.has(result.id)) {
        duplicated.push(result.id);
        continue;
      }
      byId.set(result.id, { ...result, shard: shard.shard?.index ?? null });
    }
  }
  for (const id of [...new Set(duplicated)].sort()) {
    problems.push(`${id} ran in more than one shard`);
  }
  for (const id of expectedIds) {
    if (!byId.has(id)) {
      problems.push(`${id} ran in no shard`);
    }
  }
  for (const id of [...byId.keys()].sort()) {
    if (!expectedIds.includes(id)) {
      problems.push(`${id} is not in the matrix catalog but a shard reported it`);
    }
  }

  const results = expectedIds.filter((id) => byId.has(id)).map((id) => byId.get(id));
  const failed = results.filter((result) => result.code !== 0);
  return {
    ok: problems.length === 0 && failed.length === 0,
    problems,
    results,
    failed,
  };
}
