import path from 'node:path';
import process from 'node:process';
import { startEvidenceRecorderWindowRecording } from './evidenceRecordingClient.mjs';

function usage(message) {
  if (message) process.stderr.write(`record-window: ${message}\n`);
  process.stderr.write('Usage: recordWindow.mjs --window-id ID --bundle-id ID --seconds N --out FILE.mp4\n');
  process.exit(2);
}

const options = {};
for (let index = 2; index < process.argv.length; index += 2) {
  const flag = process.argv[index];
  const value = process.argv[index + 1];
  if (!value) usage(`${flag} requires a value`);
  if (flag === '--window-id') options.windowId = Number(value);
  else if (flag === '--bundle-id') options.targetBundleId = value;
  else if (flag === '--seconds') options.seconds = Number(value);
  else if (flag === '--out') options.outputPath = path.resolve(value);
  else usage(`unknown argument ${flag}`);
}
if (!Number.isInteger(options.windowId) || options.windowId < 1) usage('--window-id must be a positive integer');
if (!options.targetBundleId) usage('--bundle-id is required');
if (!Number.isFinite(options.seconds) || options.seconds <= 0) usage('--seconds must be positive');
if (!options.outputPath?.endsWith('.mp4')) usage('--out must end in .mp4');

try {
  const recording = await startEvidenceRecorderWindowRecording(options);
  let interrupted = null;
  const interrupt = new Promise((resolve) => {
    process.once('SIGINT', () => { interrupted = 'SIGINT'; resolve(); });
    process.once('SIGTERM', () => { interrupted = 'SIGTERM'; resolve(); });
  });
  let durationTimer;
  await Promise.race([
    new Promise((resolve) => { durationTimer = setTimeout(resolve, options.seconds * 1_000); }),
    interrupt,
  ]);
  clearTimeout(durationTimer);
  const result = await recording.stop();
  if (result.failure) throw new Error(result.failure);
  process.stdout.write(`${JSON.stringify(result)}\n`);
  if (interrupted) process.exitCode = interrupted === 'SIGINT' ? 130 : 143;
} catch (error) {
  process.stderr.write(`record-window: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
}
