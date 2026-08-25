import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

const START_TIMEOUT_MS = 5_000;
const STOP_TIMEOUT_MS = 15_000;

export function evidenceRecorderManifestPath(env = process.env) {
  return env.ATTN_EVIDENCE_RECORDING_MANIFEST || path.join(
    os.homedir(),
    'Library',
    'Application Support',
    'com.attn.recorder',
    'evidence-recording.json',
  );
}

function readManifest(manifestPath) {
  let manifest;
  try {
    manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
  } catch (error) {
    throw new Error(
      `attn-recorder.app is unavailable at ${manifestPath}; run make install-evidence-recorder once`,
      { cause: error },
    );
  }
  if (!Number.isInteger(manifest.port) || manifest.port < 1 || manifest.port > 65_535
      || typeof manifest.token !== 'string' || manifest.token.length < 32) {
    throw new Error(`attn-recorder.app manifest is invalid at ${manifestPath}`);
  }
  return manifest;
}

export async function startEvidenceRecorderWindowRecording({
  windowId,
  targetBundleId,
  outputPath,
  manifestPath = evidenceRecorderManifestPath(),
  connect = (port) => net.createConnection({ host: '127.0.0.1', port }),
}) {
  const manifest = readManifest(manifestPath);

  return new Promise((resolve, reject) => {
    const socket = connect(manifest.port);
    let buffer = '';
    let started = false;
    let finished = false;
    let stopSent = false;
    let finishRecording;
    const recordingFinished = new Promise((finish) => {
      finishRecording = finish;
    });
    const startTimeout = setTimeout(
      () => fail(`start timed out after ${START_TIMEOUT_MS}ms`),
      START_TIMEOUT_MS,
    );
    startTimeout.unref();
    let stopTimeout = null;

    const finish = (result) => {
      if (finished) return;
      finished = true;
      clearTimeout(startTimeout);
      if (stopTimeout) clearTimeout(stopTimeout);
      finishRecording({ windowId, outputPath, ...result });
      socket.end();
    };

    const fail = (error) => {
      if (finished) return;
      const message = error instanceof Error ? error.message : String(error);
      if (!started) {
        finished = true;
        clearTimeout(startTimeout);
        reject(new Error(`attn-recorder.app failed to start recording: ${message}`));
        socket.destroy?.();
      } else {
        finish({ bytes: 0, exitCode: null, failure: `recording broker disconnected: ${message}` });
      }
    };

    const handle = {
      windowId,
      outputPath,
      async stop() {
        if (!stopSent && !finished) {
          stopSent = true;
          socket.write(`${JSON.stringify({ action: 'stop' })}\n`);
          stopTimeout = setTimeout(
            () => fail(`finalization timed out after ${STOP_TIMEOUT_MS}ms`),
            STOP_TIMEOUT_MS,
          );
          stopTimeout.unref();
        }
        return recordingFinished;
      },
    };

    socket.setEncoding('utf8');
    socket.on('connect', () => {
      socket.write(`${JSON.stringify({
        token: manifest.token,
        action: 'start',
        window_id: windowId,
        target_bundle_id: targetBundleId,
        output_path: outputPath,
      })}\n`);
    });
    socket.on('data', (chunk) => {
      buffer += chunk;
      while (buffer.includes('\n')) {
        const newline = buffer.indexOf('\n');
        const line = buffer.slice(0, newline).trim();
        buffer = buffer.slice(newline + 1);
        if (!line) continue;

        let event;
        try {
          event = JSON.parse(line);
        } catch (error) {
          fail(error);
          return;
        }
        if (event.event === 'error') {
          fail(event.error || 'unknown broker error');
          return;
        }
        if (event.event === 'started' && !started) {
          started = true;
          clearTimeout(startTimeout);
          resolve(handle);
          continue;
        }
        if (event.event === 'finished') {
          finish({
            bytes: event.bytes || 0,
            exitCode: event.exitCode ?? null,
            failure: event.failure || null,
          });
        }
      }
    });
    socket.on('error', fail);
    socket.on('close', () => {
      if (!finished) fail('connection closed before the recorder finalized');
    });
  });
}
