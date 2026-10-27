#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, relative, resolve } from 'node:path';

const archive = process.argv[2];
if (!archive || process.argv.length !== 3) throw new Error('usage: node restore.mjs /path/to/reviewed-pilot-archive.tar.gz');
const path = resolve(archive);
const entries = execFileSync('/usr/bin/tar', ['-tzf', path], { encoding: 'utf8' }).trim().split('\n');
const states = entries.filter(entry => entry.endsWith('/flow/_state.json'));
if (states.length !== 1) throw new Error(`archive needs exactly one flow/_state.json, found ${states.length}`);
const prefix = dirname(dirname(states[0]));
const root = resolve('/private/tmp', prefix);
if (relative('/private/tmp', root).startsWith('..') || root === '/private/tmp' ||
  entries.some(entry => {
    const target = resolve('/private/tmp', entry);
    return target !== root && relative(root, target).startsWith('..');
  }))
  throw new Error('archive entries must belong to one private flow root under /private/tmp');
const state = JSON.parse(execFileSync('/usr/bin/tar', ['-xOzf', path, states[0]], { encoding: 'utf8' }));
if (state.private_root !== root) throw new Error(`archive root ${root} differs from reviewed private_root ${state.private_root}`);
if (existsSync(root)) throw new Error(`refusing to overwrite ${root}; keep its existing results and configuration`);
execFileSync('/usr/bin/tar', ['-xzf', path, '-C', '/private/tmp'], { stdio: 'inherit' });
console.log(`Restored ${root}, including saved receipts. No new approval recorded and no model calls made.`);
execFileSync(process.execPath, ['scripts/evals/crew-memory/run.mjs', '--flow', root + '/flow', '--check-harness'], { stdio: 'inherit' });
