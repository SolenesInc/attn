import { EventEmitter } from 'node:events';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { startEvidenceRecorderWindowRecording } from './evidenceRecordingClient.mjs';

let tmpDir;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'evidence-recording-client-test-'));
});

afterEach(() => {
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

function writeManifest() {
  const manifestPath = path.join(tmpDir, 'manifest.json');
  fs.writeFileSync(manifestPath, JSON.stringify({
    port: 43210,
    token: 'a'.repeat(64),
  }));
  return manifestPath;
}

class FakeBrokerSocket extends EventEmitter {
  constructor() {
    super();
    this.requests = [];
    queueMicrotask(() => this.emit('connect'));
  }

  setEncoding() {}

  write(line) {
    const request = JSON.parse(line);
    this.requests.push(request);
    if (request.action === 'start') {
      queueMicrotask(() => this.emit('data', `${JSON.stringify({ event: 'started', pid: 44 })}\n`));
    } else if (request.action === 'stop') {
      queueMicrotask(() => this.emit('data', `${JSON.stringify({
        event: 'finished',
        bytes: 123,
        exitCode: 0,
        failure: null,
      })}\n`));
    }
  }

  end() {}

  destroy() {}
}

describe('startEvidenceRecorderWindowRecording', () => {
  it('keeps one broker connection for the recording lifetime', async () => {
    const manifestPath = writeManifest();
    const socket = new FakeBrokerSocket();
    const outputPath = path.join(tmpDir, 'clip.mp4');
    const handle = await startEvidenceRecorderWindowRecording({
      windowId: 42,
      targetBundleId: 'com.attn.manager.profile',
      outputPath,
      manifestPath,
      connect: () => socket,
    });

    expect(socket.requests[0]).toMatchObject({
      token: 'a'.repeat(64),
      action: 'start',
      window_id: 42,
      target_bundle_id: 'com.attn.manager.profile',
      output_path: outputPath,
    });
    await expect(handle.stop()).resolves.toEqual({
      windowId: 42,
      outputPath,
      bytes: 123,
      exitCode: 0,
      failure: null,
    });
  });

  it('names the stable app when its manifest is absent', async () => {
    await expect(startEvidenceRecorderWindowRecording({
      windowId: 42,
      targetBundleId: 'com.attn.manager.profile',
      outputPath: path.join(tmpDir, 'clip.mp4'),
      manifestPath: path.join(tmpDir, 'missing.json'),
    })).rejects.toThrow('attn-recorder.app is unavailable');
  });

});
