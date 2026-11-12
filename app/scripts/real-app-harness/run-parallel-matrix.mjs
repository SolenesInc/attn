#!/usr/bin/env node

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline';
import { fileURLToPath } from 'node:url';
import { emitVerdict, harnessArtifactsRoot, timestampSlug } from './common.mjs';
import { defaultAppPathForInstance, instanceCliEnv } from './harnessInstance.mjs';
import { formatShardDigest, selectFailedScenarios } from './matrixDigest.mjs';
import { resolveScenarios } from './scenarioCatalog.mjs';
import { appShellInTree } from './platform.mjs';
import { acquireScenarioLock, instanceScenarioLockPath } from './scenarioRunner.mjs';
import { aggregateShards } from './shardPlan.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const APP_DIR = path.resolve(HARNESS_DIR, '../..');
const SERIAL_MATRIX = path.join(HARNESS_DIR, 'run-serial-matrix.mjs');
const DEFAULT_WORKERS = 4;
const DEFAULT_INSTANCE_PREFIX = 'acc';
const SCREEN = '1600x1000x24';

function usage() {
  return `Usage:
  node scripts/real-app-harness/run-parallel-matrix.mjs [--workers N] [--instance-prefix acc]
      [--scenario <id>]... [--failed-only] [--fail-fast] [--timeout-ms <ms>] [--no-fresh-world]

Runs the acceptance matrix as N concurrent serial matrices on this Linux
machine. Worker k drives instance <prefix><k> on its own Xvfb display and runs
shard k/N of the selected scenarios, balanced by scenario-durations.json.
Defaults: ${DEFAULT_WORKERS} workers, instances ${DEFAULT_INSTANCE_PREFIX}1..${DEFAULT_INSTANCE_PREFIX}${DEFAULT_WORKERS}.

Install every worker's instance from this checkout first:
  for k in $(seq 1 N); do make install INSTANCE=<prefix>$k; done

The run holds each worker's instance for its whole length; a second run that
needs one of them waits for it. Each run writes to a new
<artifacts>/parallel-<time>/ folder, so earlier runs' evidence stays: worker k's
full output is in its w<k>/matrix.log. The combined result is
<artifacts>/last-matrix.json and <artifacts>/matrix-digest.txt.`;
}

function parseArgs(argv) {
  const args = argv[0] === '--' ? argv.slice(1) : [...argv];
  const options = {
    workers: DEFAULT_WORKERS,
    instancePrefix: DEFAULT_INSTANCE_PREFIX,
    selected: [],
    failedOnly: false,
    passThrough: [],
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--workers') {
      options.workers = Number(args[++index]);
    } else if (arg === '--instance-prefix') {
      options.instancePrefix = String(args[++index] || '');
    } else if (arg === '--scenario') {
      options.selected.push(String(args[++index] || '').trim());
    } else if (arg === '--failed-only') {
      options.failedOnly = true;
    } else if (arg === '--fail-fast' || arg === '--no-fresh-world') {
      options.passThrough.push(arg);
    } else if (arg === '--timeout-ms') {
      options.passThrough.push(arg, String(args[++index]));
    } else if (arg === '--help' || arg === '-h') {
      return { help: true };
    } else {
      throw new Error(`Unknown argument: ${arg}\n\n${usage()}`);
    }
  }
  if (!Number.isInteger(options.workers) || options.workers < 1) {
    throw new Error(`--workers must be a positive integer, got ${options.workers}`);
  }
  if (!options.instancePrefix) {
    throw new Error('--instance-prefix must name a non-production instance prefix, such as acc');
  }
  if (options.failedOnly && options.selected.length > 0) {
    throw new Error('--failed-only cannot be combined with --scenario');
  }
  return options;
}

function selectScenarioIds({ selected, failedOnly, instancePrefix }, artifactsRoot) {
  if (!failedOnly) {
    const app = appShellInTree(defaultAppPathForInstance(`${instancePrefix}1`));
    return resolveScenarios(selected, app).map((scenario) => scenario.id);
  }
  const lastMatrixPath = path.join(artifactsRoot, 'last-matrix.json');
  if (!fs.existsSync(lastMatrixPath)) {
    throw new Error(`--failed-only requires a previous parallel matrix run; missing ${lastMatrixPath}`);
  }
  const failedIds = selectFailedScenarios(JSON.parse(fs.readFileSync(lastMatrixPath, 'utf8')));
  if (failedIds.length === 0) {
    throw new Error(`--failed-only found no failed scenarios in ${lastMatrixPath}`);
  }
  console.log(`--failed-only selected: ${failedIds.join(', ')}`);
  return failedIds;
}

function startDisplay(logPath) {
  return new Promise((resolve, reject) => {
    const log = fs.openSync(logPath, 'a');
    const server = spawn('Xvfb', ['-displayfd', '3', '-noreset', '-nolisten', 'tcp', '-screen', '0', SCREEN], {
      stdio: ['ignore', log, log, 'pipe'],
    });
    fs.closeSync(log);
    let output = '';
    server.once('error', (error) => reject(new Error(`Xvfb could not start: ${error.message}`)));
    server.once('exit', (code, signal) => reject(new Error(`Xvfb exited before naming its display (code ${code}, signal ${signal}); see ${logPath}`)));
    server.stdio[3].on('data', (chunk) => {
      output += chunk.toString('utf8');
      const match = /^(\d+)\n/.exec(output);
      if (match) {
        server.stdio[3].destroy();
        resolve({ server, display: `:${match[1]}` });
      }
    });
  });
}

function runWorker({ index, count, instance, display, dir, scenarioIds, passThrough }) {
  const lockPath = instanceScenarioLockPath(instance);
  const args = [SERIAL_MATRIX, '--shard', `${index}/${count}`, ...passThrough];
  for (const id of scenarioIds) {
    args.push('--scenario', id);
  }
  const env = instanceCliEnv(instance, {
    ATTN_HARNESS_INSTANCE: instance,
    DISPLAY: display,
    ATTN_REAL_APP_ARTIFACTS_DIR: dir,
    ATTN_REAL_APP_SESSION_ROOT: path.join(dir, 'sessions'),
    ATTN_REAL_APP_SCENARIO_LOCK_PATH: `${lockPath}.children-${process.pid}`,
  });
  delete env.ATTN_TEST_RESULTS;
  const log = fs.createWriteStream(path.join(dir, 'matrix.log'));
  const child = spawn(process.execPath, args, { cwd: APP_DIR, env, stdio: ['ignore', 'pipe', 'pipe'] });
  const prefix = `[w${index} ${instance}]`;
  readline.createInterface({ input: child.stdout }).on('line', (line) => {
    log.write(`${line}\n`);
    if (line.startsWith('--- ') || line.startsWith('Shard ')) {
      console.log(`${prefix} ${line}`);
    }
  });
  readline.createInterface({ input: child.stderr }).on('line', (line) => log.write(`${line}\n`));
  const exited = new Promise((resolve) => {
    child.once('exit', (code, signal) => {
      log.end();
      resolve({ code, signal });
    });
  });
  return { child, exited };
}

function readShardResult(artifactsRoot, dir) {
  const file = path.join(dir, 'last-matrix.json');
  if (!fs.existsSync(file)) {
    return null;
  }
  return { ...JSON.parse(fs.readFileSync(file, 'utf8')), source: path.relative(artifactsRoot, file) };
}

function holdInstances(instances, runDir) {
  const releases = [];
  process.once('exit', () => releases.forEach((release) => release()));
  for (const instance of instances) {
    releases.push(acquireScenarioLock({
      scenarioId: 'PARALLEL-MATRIX',
      tier: 'matrix',
      runId: path.basename(runDir),
      runDir,
      appPath: null,
    }, instanceScenarioLockPath(instance)));
  }
}

async function main() {
  const startedAt = Date.now();
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log(usage());
    return;
  }
  if (process.platform !== 'linux') {
    throw new Error(`The parallel matrix gives each worker its own Xvfb display, which needs Linux; on ${process.platform} run real-app:serial-matrix.`);
  }
  const artifactsRoot = harnessArtifactsRoot();
  const scenarioIds = selectScenarioIds(options, artifactsRoot);
  const count = Math.min(options.workers, scenarioIds.length);
  if (count < options.workers) {
    console.log(`${scenarioIds.length} scenarios selected, so ${count} workers instead of ${options.workers}.`);
  }
  const runDir = path.join(artifactsRoot, `parallel-${timestampSlug()}`);
  const instances = Array.from({ length: count }, (_, offset) => `${options.instancePrefix}${offset + 1}`);
  holdInstances(instances, runDir);
  fs.mkdirSync(artifactsRoot, { recursive: true });
  fs.mkdirSync(runDir);
  console.log(`Run folder: ${runDir}`);

  const workers = [];
  let stopSignal = null;
  const stopAll = (signal) => {
    stopSignal ??= signal;
    for (const worker of workers) {
      worker.run?.child.kill(signal);
    }
  };
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    process.once(signal, () => stopAll(signal));
  }

  try {
    for (let index = 1; index <= count && !stopSignal; index += 1) {
      const instance = instances[index - 1];
      const dir = path.join(runDir, `w${index}`);
      fs.mkdirSync(dir);
      const worker = { index, instance, dir };
      workers.push(worker);
      Object.assign(worker, await startDisplay(path.join(dir, 'xvfb.log')));
      console.log(`worker ${index}: instance ${instance}, DISPLAY=${worker.display}, log ${path.join(dir, 'matrix.log')}`);
      worker.run = runWorker({ ...worker, count, scenarioIds, passThrough: options.passThrough });
    }
    await Promise.all(workers.map((worker) => worker.run.exited));
  } finally {
    stopAll('SIGTERM');
    await Promise.all(workers.map((worker) => worker.run?.exited));
    for (const worker of workers) {
      worker.server?.kill('SIGTERM');
    }
  }

  const shardResults = workers.map(({ dir }) => readShardResult(artifactsRoot, dir)).filter(Boolean);
  const aggregate = aggregateShards({ shardResults, expectedIds: scenarioIds, shardCount: count });
  const { shardLines, digest } = formatShardDigest(shardResults, aggregate);
  console.log(`\n${shardLines.join('\n')}\n\n${digest}`);
  fs.writeFileSync(path.join(artifactsRoot, 'matrix-digest.txt'), digest, 'utf8');
  const document = `${JSON.stringify({
    finishedAt: new Date().toISOString(),
    complete: aggregate.problems.length === 0,
    workers: count,
    results: aggregate.results,
  }, null, 2)}\n`;
  for (const target of [path.join(artifactsRoot, 'last-matrix.json'), process.env.ATTN_TEST_RESULTS].filter(Boolean)) {
    fs.writeFileSync(target, document, 'utf8');
  }

  const firstFailure = aggregate.failed[0]
    ? `${aggregate.failed[0].id} exit ${aggregate.failed[0].code}`
    : aggregate.problems[0] ?? null;
  emitVerdict({
    ok: aggregate.ok,
    scenarioId: 'parallel-matrix',
    runId: '',
    failureCount: aggregate.failed.length + aggregate.problems.length,
    firstFailure,
    artifactsDir: artifactsRoot,
    summaryPath: '',
    durationMs: Date.now() - startedAt,
  });
  if (!aggregate.ok) {
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
