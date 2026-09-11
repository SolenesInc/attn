#!/usr/bin/env node

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { scenarioSkipReason } from './matrixDigest.mjs';
import { scenarioCatalog } from './scenarioCatalog.mjs';

export function parseScenarioList(value, catalog = scenarioCatalog, env) {
  const ids = String(value).split(',').map((id) => id.trim());
  if (ids.length === 0 || ids.some((id) => !id)) {
    throw new Error('Scenario ids must be a comma-separated list with no empty entries.');
  }

  const known = new Set(catalog.map((scenario) => scenario.id));
  const unknown = ids.filter((id) => !known.has(id));
  if (unknown.length > 0) {
    throw new Error(`Unknown scenario id(s): ${unknown.join(', ')}\nKnown scenarios: ${[...known].join(', ')}`);
  }

  const duplicate = ids.find((id, index) => ids.indexOf(id) !== index);
  if (duplicate) {
    throw new Error(`Duplicate scenario id: ${duplicate}`);
  }

  const unavailable = [];
  for (const id of ids) {
    const scenario = catalog.find((entry) => entry.id === id);
    const rule = scenario.skipOn?.linux;
    const reason = typeof rule === 'object' && rule.unlessEnv && env === undefined
      ? null
      : scenarioSkipReason(scenario, 'linux', env || {});
    if (reason) {
      unavailable.push({ id, reason });
    }
  }
  if (unavailable.length > 0) {
    throw new Error(`Scenario(s) unavailable on the Linux soak runner:\n${unavailable
      .map(({ id, reason }) => `  ${id}: ${reason}`)
      .join('\n')}`);
  }
  return ids;
}

export function planSoak({ scenarios, repeat }, catalog = scenarioCatalog, env) {
  const parsedRepeat = Number(repeat);
  if (!Number.isInteger(parsedRepeat) || parsedRepeat <= 0) {
    throw new Error(`Repeat must be a positive integer, got: ${repeat}`);
  }
  return {
    matrix: { scenario: parseScenarioList(scenarios, catalog, env) },
    repeat: parsedRepeat,
  };
}

function parseArgs(argv) {
  const args = [...argv];
  let scenarios = '';
  let repeat = '';
  let checkRunnerEnv = false;
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--scenarios') {
      scenarios = args[++index] || '';
    } else if (arg === '--repeat') {
      repeat = args[++index] || '';
    } else if (arg === '--check-runner-env') {
      checkRunnerEnv = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  return { scenarios, repeat, checkRunnerEnv };
}

function main() {
  const options = parseArgs(process.argv.slice(2));
  const plan = planSoak(options, scenarioCatalog, options.checkRunnerEnv ? process.env : undefined);
  process.stdout.write(`matrix=${JSON.stringify(plan.matrix)}\nrepeat=${plan.repeat}\n`);
}

const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1]);
if (isMainModule) {
  try {
    main();
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exit(1);
  }
}
