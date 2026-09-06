#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { formatResultTable } from './matrixDigest.mjs';
import { resolveScenarios, scenarioCatalog } from './scenarioCatalog.mjs';
import { aggregateShards } from './shardPlan.mjs';

function parseArgs(argv) {
  let resultsDir = null;
  let shardCount = null;
  let out = null;
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--results-dir') {
      resultsDir = String(argv[++index] || '');
    } else if (arg === '--shard-count') {
      shardCount = Number(argv[++index]);
    } else if (arg === '--out') {
      out = String(argv[++index] || '');
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  if (!resultsDir) {
    throw new Error('--results-dir is required: the directory the shard artifacts were downloaded into');
  }
  if (!Number.isInteger(shardCount) || shardCount < 1) {
    throw new Error(`--shard-count must be a positive integer, got ${shardCount}`);
  }
  return { resultsDir, shardCount, out };
}

function findShardFiles(root) {
  const found = [];
  for (const entry of fs.readdirSync(root, { withFileTypes: true, recursive: true })) {
    if (entry.isFile() && entry.name === 'last-matrix.json') {
      found.push(path.join(entry.parentPath ?? entry.path, entry.name));
    }
  }
  return found.sort();
}

function main() {
  const { resultsDir, shardCount, out } = parseArgs(process.argv.slice(2));
  const files = findShardFiles(resultsDir);
  const shardResults = files.map((file) => ({
    ...JSON.parse(fs.readFileSync(file, 'utf8')),
    source: path.relative(resultsDir, file),
  }));
  const expectedIds = resolveScenarios([], scenarioCatalog).map((scenario) => scenario.id);
  const aggregate = aggregateShards({ shardResults, expectedIds, shardCount });

  for (const shard of [...shardResults].sort((a, b) => (a.shard?.index ?? 0) - (b.shard?.index ?? 0))) {
    const seconds = (shard.results || []).reduce((total, result) => total + (result.durationMs || 0), 0) / 1000;
    console.log(`shard ${shard.shard?.index ?? '?'}/${shard.shard?.count ?? '?'}: `
      + `${(shard.results || []).length} scenarios, ${seconds.toFixed(1)}s (${shard.source})`);
  }

  // The shard column also keeps ci-flake-report.sh honest: its serial-matrix
  // rule reads the shard jobs' own tables, and would count this one twice.
  const rows = formatResultTable(aggregate.results.map((result) => ({
    ...result,
    durationMs: result.durationMs ?? 0,
  }))).split('\n');
  const table = aggregate.results.map((result, index) => `s${result.shard}  ${rows[index]}`).join('\n');
  const failureSections = aggregate.failed
    .map((result) => `--- ${result.id} (shard ${result.shard}) ---\n${result.outputTail || '(no output captured)'}`)
    .join('\n\n');
  const problemSection = aggregate.problems.length > 0
    ? `\n\nShard coverage problems:\n${aggregate.problems.map((problem) => `  - ${problem}`).join('\n')}`
    : '';
  const digest = `${table}${problemSection}${failureSections ? `\n\n${failureSections}` : ''}\n`;
  console.log(`\n${digest}`);
  if (out) {
    fs.mkdirSync(path.dirname(out), { recursive: true });
    fs.writeFileSync(out, digest, 'utf8');
  }

  console.log(`\n${aggregate.results.length}/${expectedIds.length} catalog scenarios reported, `
    + `${aggregate.failed.length} failed, ${aggregate.problems.length} coverage problems.`);
  if (!aggregate.ok) {
    process.exitCode = 1;
  }
}

main();
