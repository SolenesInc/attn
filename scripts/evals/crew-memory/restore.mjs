#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

const archive = process.argv[2];
const root = '/private/tmp/crew-memory-eval-private';
if (!archive || process.argv.length !== 3) throw new Error('usage: node restore.mjs /path/to/reviewed-pilot-archive.tar.gz');
if (existsSync(root)) throw new Error(`refusing to overwrite ${root}; keep its existing results and configuration`);
execFileSync('/usr/bin/tar', ['-xzf', resolve(archive), '-C', '/private/tmp'], { stdio: 'inherit' });
console.log(`Restored ${root}, including saved receipts. No new approval recorded and no model calls made.`);
execFileSync(process.execPath, ['scripts/evals/crew-memory/run.mjs', '--flow', root + '/flow', '--check-harness'], { stdio: 'inherit' });
