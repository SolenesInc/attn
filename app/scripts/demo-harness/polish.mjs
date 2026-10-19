#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const DEMO_DIR = path.dirname(fileURLToPath(import.meta.url));
const COMPOSITION = path.join(DEMO_DIR, 'polish', 'index.html');
const HYPERFRAMES = 'hyperframes@0.8.140';
const FPS = 30;
const CANVAS_WIDTH = 1600;
const CAPTION_BAR = 120;
const AVIF = { crf: 40, preset: 4 };

function usage() {
  console.log(`Usage: node app/scripts/demo-harness/polish.mjs <clip.mp4> [--out <clip.avif>] [--keep]

Composes a raw demo clip and its <clip>.events.jsonl with the HyperFrames
composition in app/scripts/demo-harness/polish/, renders it locally, and
encodes the result as a looping animated AVIF for the guide.
--keep leaves the HyperFrames project and its MP4 render in place.`);
}

const args = process.argv.slice(2);
let clip = null;
let out = null;
let keep = false;
while (args.length > 0) {
  const arg = args.shift();
  if (arg === '--out') out = args.shift();
  else if (arg === '--keep') keep = true;
  else if (arg === '--help' || arg === '-h') { usage(); process.exit(0); }
  else clip = arg;
}
if (!clip) {
  usage();
  process.exit(1);
}
clip = path.resolve(clip);
const name = path.basename(clip, '.mp4');
const eventsFile = path.join(path.dirname(clip), `${name}.events.jsonl`);
out = path.resolve(out ?? path.join(path.dirname(clip), `${name}.avif`));

const run = (command, commandArgs, options = {}) => execFileSync(command, commandArgs, { stdio: 'inherit', ...options });
const seconds = (file) => Number(execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', file], { encoding: 'utf8' }));

const recorded = fs.readFileSync(eventsFile, 'utf8').trim().split('\n').map((line) => JSON.parse(line));
const geometry = recorded.find((event) => event.action === 'window');
if (!geometry) throw new Error(`${eventsFile} has no window event; record ${name} again with the current demo harness`);
const scale = CANVAS_WIDTH / geometry.width;
const stage = Math.round(((geometry.height - geometry.titlebar) * scale) / 2) * 2;
const canvas = { width: CANVAS_WIDTH, height: stage + CAPTION_BAR, stage, scale };

const segments = [{ start: 0, rate: 1 }];
for (const { t, rate } of recorded.filter((event) => event.action === 'speed')) segments.push({ start: Math.max(t, segments.at(-1).start), rate });
segments.forEach((segment, index) => { segment.end = segments[index + 1]?.start ?? seconds(clip); });
const retime = (t) => segments.reduce((sum, { start, end, rate }) => sum + Math.max(0, Math.min(t, end) - start) / rate, 0);
const events = segments.length > 1 ? recorded.map((event) => ({ ...event, t: Number(retime(event.t).toFixed(3)) })) : recorded;

const project = fs.mkdtempSync(path.join(os.tmpdir(), `attn-polish-${name}-`));
fs.mkdirSync(path.join(project, 'assets'));
const footage = path.join(project, 'assets', 'footage.mp4');
if (segments.length > 1) {
  const pieces = segments.filter(({ start, end }) => end > start);
  const filter = pieces.map(({ start, end, rate }, index) => `[0:v]trim=start=${start}:end=${end},setpts=(PTS-STARTPTS)/${rate}[v${index}]`).join(';')
    + `;${pieces.map((_, index) => `[v${index}]`).join('')}concat=n=${pieces.length}:v=1:a=0,fps=${FPS}[out]`;
  run('ffmpeg', ['-loglevel', 'error', '-y', '-i', clip, '-filter_complex', filter, '-map', '[out]', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '16', footage]);
} else {
  fs.copyFileSync(clip, footage);
}
const duration = Number(seconds(footage).toFixed(3));
const edit = { canvas, window: { width: geometry.width, height: geometry.height, titlebar: geometry.titlebar }, duration, events };
const untaggedRgb = 'scale=in_color_matrix=bt709:in_range=tv:out_range=pc,format=rgb24,setparams=color_primaries=unknown:color_trc=unknown:colorspace=unknown:range=unknown';
run('ffmpeg', ['-loglevel', 'error', '-y', '-i', footage, '-frames:v', '1', '-vf', untaggedRgb, path.join(project, 'assets', 'first.png')]);
const composition = fs.readFileSync(COMPOSITION, 'utf8')
  .replace(/data-duration="[\d.]+"/g, `data-duration="${duration}"`)
  .replace(/data-width="\d+" data-height="\d+"/, `data-width="${canvas.width}" data-height="${canvas.height}"`)
  .replace(/content="width=\d+, height=\d+"/, `content="width=${canvas.width}, height=${canvas.height}"`)
  .replace(/(<script id="edit" type="application\/json">)[\s\S]*?(<\/script>)/, (_, open, close) => `${open}${JSON.stringify(edit).replace(/</g, '\\u003c')}${close}`);
fs.writeFileSync(path.join(project, 'index.html'), composition);

const env = { ...process.env, HYPERFRAMES_NO_TELEMETRY: '1', HYPERFRAMES_NO_UPDATE_CHECK: '1', HYPERFRAMES_SKIP_SKILLS: '1' };
const rendered = path.join(project, `${name}.mp4`);
try {
  run('npx', ['--yes', HYPERFRAMES, 'check', project], { env });
  run('npx', ['--yes', HYPERFRAMES, 'render', project, '--quality', 'high', '--fps', String(FPS), '--output', rendered], { env });
  run('ffmpeg', ['-loglevel', 'error', '-y', '-i', rendered, '-c:v', 'libsvtav1', '-preset', String(AVIF.preset), '-crf', String(AVIF.crf), '-pix_fmt', 'yuv420p', '-loop', '0', out]);
} catch (error) {
  console.error(`[polish] failed; the HyperFrames project is still at ${project}`);
  throw error;
}

console.log(`[polish] ${out}: ${(fs.statSync(out).size / 1024).toFixed(0)} KiB, ${duration}s`);
if (keep) console.log(`[polish] project and render: ${project}`);
else fs.rmSync(project, { recursive: true, force: true });
