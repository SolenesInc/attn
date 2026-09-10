#!/usr/bin/env node

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { scenarioCatalog } from './scenarioCatalog.mjs';

export function parseScenarioList(value, catalog = scenarioCatalog) {
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
  return ids;
}

export function planSoak({ scenarios, repeat }, catalog = scenarioCatalog) {
  const parsedRepeat = Number(repeat);
  if (!Number.isInteger(parsedRepeat) || parsedRepeat <= 0) {
    throw new Error(`Repeat must be a positive integer, got: ${repeat}`);
  }
  return {
    matrix: { scenario: parseScenarioList(scenarios, catalog) },
    repeat: parsedRepeat,
  };
}

function parseArgs(argv) {
  const args = [...argv];
  let scenarios = '';
  let repeat = '';
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--scenarios') {
      scenarios = args[++index] || '';
    } else if (arg === '--repeat') {
      repeat = args[++index] || '';
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  return { scenarios, repeat };
}

function main() {
  const plan = planSoak(parseArgs(process.argv.slice(2)));
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
