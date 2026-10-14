#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath, pathToFileURL } from 'node:url';

const DEMO_DIR = path.dirname(fileURLToPath(import.meta.url));
const SCENES_DIR = path.join(DEMO_DIR, 'scenes');
const scenes = fs.readdirSync(SCENES_DIR).filter((file) => file.endsWith('.mjs')).map((file) => file.slice(0, -4));

function usage() {
  console.log(`Usage: node app/scripts/demo-harness/demo.mjs <scene> [--out <dir>] [--instance <name>]

Records a scene against a demo build of a named instance (make install INSTANCE=demo DEMO=1).
Scenes: ${scenes.join(', ')}`);
}

const args = process.argv.slice(2);
let scene = null;
let outDir = null;
let instance = 'demo';
while (args.length > 0) {
  const arg = args.shift();
  if (arg === '--out') outDir = args.shift();
  else if (arg === '--instance') instance = args.shift();
  else if (arg === '--help' || arg === '-h') { usage(); process.exit(0); }
  else scene = arg;
}
if (!scenes.includes(scene)) {
  usage();
  process.exit(scene ? 1 : 0);
}
process.env.ATTN_HARNESS_INSTANCE = instance;
outDir = path.resolve(outDir ?? path.join(os.tmpdir(), 'attn-demo', scene));
fs.mkdirSync(outDir, { recursive: true });

const log = (message) => console.log(`[demo] ${message}`);
const { startWorld } = await import('./world.mjs');
const { createDemo } = await import('./scene.mjs');
const { default: spec } = await import(pathToFileURL(path.join(SCENES_DIR, `${scene}.mjs`)).href);

const world = await startWorld({ instance, repos: spec.repos, log });
try {
  await spec.run(createDemo({ world, outDir, log }));
  log(`wrote ${outDir}`);
} finally {
  await world.stop();
}
