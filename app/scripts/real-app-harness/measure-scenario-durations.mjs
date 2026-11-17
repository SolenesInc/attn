#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import { scenarioSkipReason } from './matrixDigest.mjs';
import { resolveScenarios } from './scenarioCatalog.mjs';
import { durationsPath } from './shardPlan.mjs';

const JOB = 'app-acceptance-shard';
const DEFAULT_RUNS = 50;

const USAGE = `Usage: node scripts/real-app-harness/measure-scenario-durations.mjs [--runs <count>]

Rewrites scenario-durations.json from CI's receipts in the test-history spool:
the median seconds of each scenario's passing first attempt over the newest
<count> CI runs of the ${JOB} job (default ${DEFAULT_RUNS}).
Run \`go tool test-history sync\` first so the spool holds CI's latest runs.`;

function parseArgs(argv) {
  let runs = DEFAULT_RUNS;
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--runs') {
      runs = Number(argv[i + 1]);
      i += 1;
    } else {
      throw new Error(`${USAGE}\n\nUnknown argument: ${argv[i]}`);
    }
  }
  if (!Number.isInteger(runs) || runs < 1) {
    throw new Error(`${USAGE}\n\n--runs takes a positive integer`);
  }
  return { runs };
}

function spoolDir() {
  const repoRoot = path.resolve(path.dirname(durationsPath), '../../..');
  return execFileSync('go', ['tool', 'test-history', 'spool'], { cwd: repoRoot, encoding: 'utf8' }).trim();
}

function* shardReceipts(spool) {
  const mirror = path.join(spool, 'mirror');
  for (const name of fs.readdirSync(mirror)) {
    const receipt = JSON.parse(zlib.zstdDecompressSync(fs.readFileSync(path.join(mirror, name))));
    if (receipt.runner === 'native' && receipt.source?.kind === 'ci' && receipt.source.job === JOB) {
      yield receipt;
    }
  }
}

function newestRuns(receipts, count) {
  const runs = new Map();
  for (const receipt of receipts) {
    const run = runs.get(receipt.source.run_id) ?? { id: receipt.source.run_id, startedAt: receipt.command.started_at, receipts: [] };
    if (receipt.command.started_at < run.startedAt) {
      run.startedAt = receipt.command.started_at;
    }
    run.receipts.push(receipt);
    runs.set(run.id, run);
  }
  return [...runs.values()].sort((a, b) => b.startedAt.localeCompare(a.startedAt)).slice(0, count);
}

function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
}

function passingFirstAttempts(runs) {
  const samples = new Map();
  for (const run of runs) {
    for (const receipt of run.receipts) {
      for (const result of receipt.results ?? []) {
        if (result.attempt !== 1 || result.outcome !== 'pass' || !result.unit.startsWith('native ')) continue;
        const id = result.unit.slice('native '.length);
        samples.set(id, [...(samples.get(id) ?? []), result.elapsed_ms / 1000]);
      }
    }
  }
  return samples;
}

function main() {
  const { runs: runCount } = parseArgs(process.argv.slice(2));
  const runs = newestRuns(shardReceipts(spoolDir()), runCount);
  if (runs.length < runCount) {
    throw new Error(`The spool holds ${runs.length} CI runs of ${JOB}, fewer than the ${runCount} asked for; run go tool test-history sync`);
  }
  const samples = passingFirstAttempts(runs);
  const ids = ['tauri', 'native'].flatMap((app) => resolveScenarios([], app))
    .filter((scenario) => !scenarioSkipReason(scenario, 'linux', {}))
    .map((scenario) => scenario.id)
    .sort();
  const unmeasured = ids.filter((id) => !samples.has(id));
  if (unmeasured.length > 0) {
    throw new Error(`No passing first attempt of ${unmeasured.join(', ')} in the newest ${runCount} CI runs of ${JOB}`);
  }

  const seconds = Object.fromEntries(ids.map((id) => [id, Math.round(median(samples.get(id)) * 10) / 10]));
  const durations = {
    receipt: {
      source: `median elapsed seconds of each scenario's passing first attempt in the test-history receipts of CI's ${JOB} job`,
      runs: runs.map((run) => `https://github.com/SolenesInc/attn/actions/runs/${run.id}`),
      from: runs.at(-1).startedAt,
      to: runs[0].startedAt,
      matrixSeconds: Math.round(Object.values(seconds).reduce((sum, value) => sum + value, 0) * 10) / 10,
      regenerate: 'go tool test-history sync && node app/scripts/real-app-harness/measure-scenario-durations.mjs',
    },
    seconds,
  };
  fs.writeFileSync(durationsPath, `${JSON.stringify(durations, null, 2)}\n`);
  console.log(`Wrote ${path.relative(process.cwd(), durationsPath)}: ${ids.length} scenarios, `
    + `${durations.receipt.matrixSeconds}s of work over ${runs.length} runs from ${durations.receipt.from} to ${durations.receipt.to}`);
}

try {
  main();
} catch (error) {
  console.error(error.message);
  process.exit(1);
}
