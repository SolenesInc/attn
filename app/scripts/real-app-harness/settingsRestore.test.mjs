import { afterEach, describe, expect, it } from 'vitest';
import { WebSocketServer } from 'ws';
import { writeDaemonSettings } from './common.mjs';

const PINS = [
  { key: 'claude_executable', value: '' },
  { key: 'codex_executable', value: '' },
];

let server = null;

// Stands in for the daemon: applies every set_setting, then answers with the
// snapshots `emit` asks for, each carrying the settings stored so far.
function fakeDaemon(emit) {
  server = new WebSocketServer({ host: '127.0.0.1', port: 0 });
  const stored = { claude_executable: '/mock/agent', codex_executable: '/mock/agent' };
  server.on('connection', (socket) => {
    const writes = [];
    socket.on('message', (raw) => {
      const data = JSON.parse(raw.toString());
      if (data.cmd !== 'set_setting') return;
      stored[data.key] = data.value;
      writes.push(data.key);
      emit(socket, writes, stored);
    });
  });
  return new Promise((resolve) => {
    server.on('listening', () => resolve(`ws://127.0.0.1:${server.address().port}`));
  });
}

const snapshot = (changedKey, stored) => JSON.stringify({
  event: 'settings_updated',
  changed_key: changedKey,
  settings: { ...stored },
});

afterEach(() => {
  server?.close();
  server = null;
});

describe('restoring the settings the harness pinned', () => {
  it('finishes when one coalesced snapshot carries every write', async () => {
    const wsUrl = await fakeDaemon((socket, writes, stored) => {
      if (writes.length < PINS.length) return;
      socket.send(snapshot(writes[writes.length - 1], stored));
    });

    await expect(writeDaemonSettings(PINS, { wsUrl, timeoutMs: 4000 })).resolves.toBeUndefined();
  });

  it('finishes when the daemon answers each write separately', async () => {
    const wsUrl = await fakeDaemon((socket, writes, stored) => {
      socket.send(snapshot(writes[writes.length - 1], stored));
    });

    await expect(writeDaemonSettings(PINS, { wsUrl, timeoutMs: 4000 })).resolves.toBeUndefined();
  });

  it('names the settings still unwritten when the daemon never answers', async () => {
    const wsUrl = await fakeDaemon(() => {});

    await expect(writeDaemonSettings(PINS, { wsUrl, timeoutMs: 300 }))
      .rejects.toThrow(/timed out writing claude_executable, codex_executable/);
  });

  it('names the settings still unwritten when a snapshot misses one', async () => {
    const wsUrl = await fakeDaemon((socket, writes, stored) => {
      socket.send(snapshot(writes[writes.length - 1], { ...stored, codex_executable: '/mock/agent' }));
    });

    await expect(writeDaemonSettings(PINS, { wsUrl, timeoutMs: 300 }))
      .rejects.toThrow(/timed out writing codex_executable/);
  });
});
