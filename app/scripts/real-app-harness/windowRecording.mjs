import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { requireInstalledWindowRecorderLaunch } from './windowRecorderApp.mjs';

export function recordingEnabled(env = process.env) {
  const value = String(env.ATTN_HARNESS_RECORD ?? '').trim().toLowerCase();
  return value === '1' || value === 'true' || value === 'on';
}

export async function ensureWindowRecorder(options) {
  return requireInstalledWindowRecorderLaunch(options);
}

// Graceful stop-to-exit was <1s in every measured finalization; 10s catches a
// recorder that will never finalize, and then SIGKILL abandons the file.
const FINALIZE_TRIPWIRE_MS = 10_000;

export function startWindowRecording({ windowId, outputPath, command, spawnFn = spawn }) {
  const launch = typeof command === 'string'
    ? { command, argsPrefix: [], captureLaunchedStderr: false, stopWithFile: false }
    : command;
  const stopFilePath = launch.stopWithFile ? `${outputPath}.stop` : null;
  const launchedStderrPath = launch.captureLaunchedStderr ? `${outputPath}.stderr` : null;
  if (stopFilePath) fs.rmSync(stopFilePath, { force: true });
  if (launchedStderrPath) fs.rmSync(launchedStderrPath, { force: true });
  const args = [...launch.argsPrefix];
  if (launchedStderrPath) args.push('--stderr', launchedStderrPath, '--args');
  args.push(String(windowId), outputPath);
  if (stopFilePath) args.push('15', stopFilePath);
  const child = spawnFn(launch.command, args, {
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let stderr = '';
  child.stderr?.on('data', (chunk) => {
    if (stderr.length < 4096) stderr += chunk;
  });

  const exited = new Promise((resolve) => {
    child.once('error', (error) => resolve({ code: null, spawnError: error }));
    child.once('exit', (code) => resolve({ code, spawnError: null }));
  });

  return {
    windowId,
    outputPath,
    async stop() {
      let forced = false;
      try {
        if (stopFilePath) {
          fs.writeFileSync(stopFilePath, 'stop\n', 'utf8');
        } else {
          child.kill('SIGINT');
        }
      } catch {
      }
      const tripwire = setTimeout(() => {
        forced = true;
        try {
          child.kill('SIGKILL');
        } catch {}
      }, FINALIZE_TRIPWIRE_MS);
      tripwire.unref();
      const { code, spawnError } = await exited;
      clearTimeout(tripwire);
      if (launchedStderrPath) {
        try {
          stderr += fs.readFileSync(launchedStderrPath, 'utf8');
          fs.rmSync(launchedStderrPath, { force: true });
        } catch {}
      }
      if (stopFilePath) {
        try {
          fs.rmSync(stopFilePath, { force: true });
        } catch {}
      }

      let bytes = 0;
      try {
        bytes = fs.statSync(outputPath).size;
      } catch {}
      const failure = spawnError
        ? `recorder failed to spawn: ${spawnError.message}`
        : forced
          ? `recorder ignored the stop request for ${FINALIZE_TRIPWIRE_MS}ms and was SIGKILLed; the file is likely unplayable`
          : bytes === 0
            ? `recorder exited ${code} with no output${stderr ? `: ${stderr.trim()}` : ''}`
            : code !== 0
              ? `recorder exited ${code} leaving a possibly unplayable file${stderr ? `: ${stderr.trim()}` : ''}`
              : null;
      return { windowId, outputPath, bytes, exitCode: code, failure };
    },
  };
}

export function createScenarioRecorder({
  runDir,
  resolveWindowId,
  log = () => {},
  pollIntervalMs = 1_000,
  spawnFn = spawn,
  commandFn = ensureWindowRecorder,
}) {
  let active = null;
  let segmentIndex = 0;
  let pollPromise = null;
  let stopped = false;
  const segments = [];
  let timer = null;
  let commandPromise = null;

  const finalizeActive = async () => {
    if (!active) return;
    const handle = active;
    active = null;
    const result = await handle.stop();
    segments.push(result);
    if (result.failure) {
      log('recording:segment-failed', { path: result.outputPath, failure: result.failure });
    } else {
      log('recording:segment', { path: result.outputPath, bytes: result.bytes });
    }
  };

  const poll = async () => {
    if (pollPromise || stopped) return;
    pollPromise = (async () => {
      try {
        commandPromise ??= commandFn();
        let command;
        try {
          command = await commandPromise;
        } catch (error) {
          // A recorder binary that cannot build will never build this run.
          log('recording:disabled', { error: error instanceof Error ? error.message : String(error) });
          stopped = true;
          if (timer) {
            clearInterval(timer);
            timer = null;
          }
          return;
        }
        const windowId = await resolveWindowId();
        if (stopped) return;
        if (active && active.windowId === windowId) return;
        await finalizeActive();
        if (windowId && !stopped) {
          segmentIndex += 1;
          const outputPath = path.join(runDir, `recording-${String(segmentIndex).padStart(2, '0')}.mp4`);
          active = startWindowRecording({ windowId, outputPath, command, spawnFn });
          log('recording:start', { path: outputPath, windowId });
        }
      } catch (error) {
        log('recording:poll-error', { error: error instanceof Error ? error.message : String(error) });
      } finally {
        pollPromise = null;
      }
    })();
    await pollPromise;
  };

  return {
    start() {
      if (timer || stopped) return;
      timer = setInterval(poll, pollIntervalMs);
      void poll();
    },
    async stop() {
      if (stopped) return segments;
      stopped = true;
      if (timer) {
        clearInterval(timer);
        timer = null;
      }
      if (pollPromise) {
        await pollPromise.catch(() => {});
      }
      await finalizeActive();
      const usable = segments.filter((s) => !s.failure);
      if (usable.length > 0) {
        try {
          fs.writeFileSync(
            path.join(runDir, 'recording.json'),
            `${JSON.stringify({ segments }, null, 2)}\n`,
            'utf8'
          );
        } catch {}
      }
      log('recording:done', { segments: segments.length, usable: usable.length });
      return segments;
    },
  };
}
