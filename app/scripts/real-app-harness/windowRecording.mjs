import fs from 'node:fs';
import path from 'node:path';
import { startEvidenceRecorderWindowRecording } from './evidenceRecordingClient.mjs';

export function recordingEnabled(env = process.env) {
  const value = String(env.ATTN_HARNESS_RECORD ?? '').trim().toLowerCase();
  return value === '1' || value === 'true' || value === 'on';
}

export function createScenarioRecorder({
  runDir,
  resolveWindowId,
  targetBundleId,
  log = () => {},
  pollIntervalMs = 1_000,
  startRecordingFn = startEvidenceRecorderWindowRecording,
}) {
  let active = null;
  let segmentIndex = 0;
  let pollPromise = null;
  let stopped = false;
  const segments = [];
  let timer = null;

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
        const windowId = await resolveWindowId();
        if (stopped) return;
        if (active && active.windowId === windowId) return;
        await finalizeActive();
        if (windowId && !stopped) {
          segmentIndex += 1;
          const outputPath = path.join(runDir, `recording-${String(segmentIndex).padStart(2, '0')}.mp4`);
          try {
            active = await startRecordingFn({ windowId, targetBundleId, outputPath });
          } catch (error) {
            log('recording:disabled', { error: error instanceof Error ? error.message : String(error) });
            stopped = true;
            if (timer) {
              clearInterval(timer);
              timer = null;
            }
            return;
          }
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
