#!/usr/bin/env node

import { readFileSync } from 'node:fs';
import { scenarioSkipReason } from './matrixDigest.mjs';
import { scenarioCatalog } from './scenarioCatalog.mjs';

const packageScripts = JSON.parse(readFileSync(new URL('../../package.json', import.meta.url), 'utf8')).scripts;

function scenarioFile(scenario) {
  const [program, ...args] = scenario.command;
  const words = program === 'pnpm' ? packageScripts[args[1]].split(' ') : scenario.command;
  return `app/${words[1]}`;
}

const changed = new Set(readFileSync(0, 'utf8').split('\n'));
const runnable = [];
for (const scenario of scenarioCatalog) {
  if (!changed.has(scenarioFile(scenario))) {
    continue;
  }
  const skipReason = scenarioSkipReason(scenario, 'linux', {});
  if (skipReason) {
    console.error(`land: scenario ${scenario.id} changed and does not run on Linux: ${skipReason}`);
  } else {
    runnable.push(scenario.id);
  }
}
console.log(runnable.join(','));
