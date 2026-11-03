#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { formatShardDigest } from './matrixDigest.mjs';
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

  const { shardLines, digest } = formatShardDigest(shardResults, aggregate);
  for (const line of shardLines) {
    console.log(line);
  }
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
