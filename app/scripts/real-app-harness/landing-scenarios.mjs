#!/usr/bin/env node

import { existsSync, readFileSync } from 'node:fs';
import { scenarioSkipReason } from './matrixDigest.mjs';
import { scenarioCatalog } from './scenarioCatalog.mjs';

const packageScripts = JSON.parse(readFileSync(new URL('../../package.json', import.meta.url), 'utf8')).scripts;
const repositoryRoot = new URL('../../../', import.meta.url);

function scenarioFile(scenario) {
  const [program, ...args] = scenario.command;
  const words = program === 'pnpm' ? packageScripts[args[1]].split(' ') : scenario.command;
  return `app/${words[1]}`;
}

function changesProductCode(path) {
  return /^(app\/src\/|app\/src-tauri\/|internal\/|cmd\/|pty-host\/)/.test(path)
    && !/(_test\.go$|\.test\.tsx?$|\/testdata\/|\/test\/|^internal\/prompts\/content\/|\.md$|\.generated\.)/.test(path);
}

function scenariosListedBy(featureFile) {
  const url = new URL(featureFile, repositoryRoot);
  if (!existsSync(url)) {
    return [];
  }
  const scenarioLine = readFileSync(url, 'utf8').match(/^- Scenario: (.*)$/m)?.[1] ?? '';
  return [...scenarioLine.matchAll(/`([a-z0-9-]+)`/g)].map((match) => match[1]);
}

const changedPaths = readFileSync(0, 'utf8').split('\n').filter(Boolean);
const changedFiles = new Set(changedPaths);
const listed = new Set(
  changedPaths.some(changesProductCode)
    ? changedPaths.filter((path) => path.startsWith('.agents/skills/drive-attn/features/')).flatMap(scenariosListedBy)
    : [],
);

const runnable = [];
for (const scenario of scenarioCatalog) {
  const changed = changedFiles.has(scenarioFile(scenario));
  if (!changed && !listed.has(scenario.id)) {
    continue;
  }
  const skipReason = scenarioSkipReason(scenario, 'linux', {});
  if (!skipReason) {
    runnable.push(scenario.id);
  } else if (changed) {
    console.error(`land: scenario ${scenario.id} changed and does not run on Linux: ${skipReason}`);
  }
}
console.log(runnable.join(','));
