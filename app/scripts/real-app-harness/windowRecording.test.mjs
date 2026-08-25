import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createScenarioRecorder, recordingEnabled } from './windowRecording.mjs';

let tmpDir;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'window-recording-test-'));
});

afterEach(() => {
  fs.rmSync(tmpDir, { recursive: true, force: true });
  vi.useRealTimers();
});

describe('recordingEnabled', () => {
  it('is off by default and for falsy spellings', () => {
    expect(recordingEnabled({})).toBe(false);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: '' })).toBe(false);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: '0' })).toBe(false);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: 'false' })).toBe(false);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: 'off' })).toBe(false);
  });

  it('accepts 1/true/on in any case', () => {
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: '1' })).toBe(true);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: 'true' })).toBe(true);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: 'ON' })).toBe(true);
  });
});

describe('createScenarioRecorder', () => {
  function makeRecorder({ windowIds, startError = null }) {
    vi.useFakeTimers();
    const ids = [...windowIds];
    const calls = [];
    const logs = [];
    const startRecordingFn = async (request) => {
      if (startError) throw startError;
      const call = { ...request, stops: 0 };
      calls.push(call);
      return {
        windowId: request.windowId,
        outputPath: request.outputPath,
        async stop() {
          call.stops += 1;
          fs.writeFileSync(request.outputPath, 'movie-bytes');
          return {
            windowId: request.windowId,
            outputPath: request.outputPath,
            bytes: 11,
            exitCode: 0,
            failure: null,
          };
        },
      };
    };
    const recorder = createScenarioRecorder({
      runDir: tmpDir,
      targetBundleId: 'com.attn.manager.test-profile',
      resolveWindowId: async () => (ids.length > 1 ? ids.shift() : ids[0]),
      log: (message, details) => logs.push({ message, details }),
      startRecordingFn,
    });
    return { recorder, calls, logs };
  }

  it('starts one segment and keeps it while the window id is stable', async () => {
    const { recorder, calls } = makeRecorder({ windowIds: [null, 11, 11] });
    recorder.start();
    await vi.advanceTimersByTimeAsync(3_000);

    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({
      windowId: 11,
      targetBundleId: 'com.attn.manager.test-profile',
      outputPath: path.join(tmpDir, 'recording-01.mp4'),
    });
  });

  it('finalizes the old segment before rotating to a new window id', async () => {
    const { recorder, calls } = makeRecorder({ windowIds: [11, 22] });
    recorder.start();
    await vi.advanceTimersByTimeAsync(1_500);

    expect(calls).toHaveLength(2);
    expect(calls[0].stops).toBe(1);
    expect(calls[1].windowId).toBe(22);
    expect(calls[1].outputPath).toBe(path.join(tmpDir, 'recording-02.mp4'));
  });

  it('writes the usable segment manifest when stopped', async () => {
    const { recorder } = makeRecorder({ windowIds: [11] });
    recorder.start();
    await vi.advanceTimersByTimeAsync(500);
    const segments = await recorder.stop();

    expect(segments).toHaveLength(1);
    expect(JSON.parse(fs.readFileSync(path.join(tmpDir, 'recording.json'), 'utf8')).segments).toHaveLength(1);
  });

  it('does nothing when no app window appears', async () => {
    const { recorder, calls } = makeRecorder({ windowIds: [null] });
    recorder.start();
    await vi.advanceTimersByTimeAsync(3_000);
    expect(calls).toHaveLength(0);
    expect(await recorder.stop()).toEqual([]);
  });

  it('disables itself when the stable recorder app is unavailable', async () => {
    const { recorder, calls, logs } = makeRecorder({
      windowIds: [11],
      startError: new Error('broker unavailable'),
    });
    recorder.start();
    await vi.advanceTimersByTimeAsync(3_000);

    expect(calls).toHaveLength(0);
    expect(logs.filter((entry) => entry.message === 'recording:disabled')).toHaveLength(1);
    expect(await recorder.stop()).toEqual([]);
  });

  it('tolerates window lookup failures and keeps polling', async () => {
    vi.useFakeTimers();
    const calls = [];
    const logs = [];
    let attempts = 0;
    const recorder = createScenarioRecorder({
      runDir: tmpDir,
      targetBundleId: 'com.attn.manager.test-profile',
      resolveWindowId: async () => {
        attempts += 1;
        if (attempts === 1) throw new Error('driver not ready');
        return 33;
      },
      log: (message, details) => logs.push({ message, details }),
      startRecordingFn: async (request) => {
        calls.push(request);
        return {
          ...request,
          stop: async () => ({ ...request, bytes: 0, exitCode: 0, failure: 'empty' }),
        };
      },
    });
    recorder.start();
    await vi.advanceTimersByTimeAsync(2_500);

    expect(logs.some((entry) => entry.message === 'recording:poll-error')).toBe(true);
    expect(calls).toHaveLength(1);
    expect(calls[0].windowId).toBe(33);
  });
});
