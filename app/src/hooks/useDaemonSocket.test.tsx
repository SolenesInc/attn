import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { ptySpawn } from '../pty/bridge';
import { useDaemonSocket } from './useDaemonSocket';

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  sent: string[] = [];

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
    queueMicrotask(() => {
      this.readyState = FakeWebSocket.OPEN;
      this.onopen?.(new Event('open'));
    });
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close'));
  }

  emit(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) } as MessageEvent);
  }
}

async function waitForOpenSocket(timeout = 1_000): Promise<FakeWebSocket> {
  await waitFor(() => {
    expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
  }, { timeout });
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  expect(ws).toBeDefined();
  await waitFor(() => {
    expect(ws.readyState).toBe(FakeWebSocket.OPEN);
  });
  return ws;
}

describe('useDaemonSocket PTY kill sequencing', () => {

  let originalSetTimeout: typeof globalThis.setTimeout;
  let originalClearTimeout: typeof globalThis.clearTimeout;
  let pendingTimeouts: Set<ReturnType<typeof globalThis.setTimeout>>;

  beforeEach(() => {

    originalSetTimeout = globalThis.setTimeout;
    originalClearTimeout = globalThis.clearTimeout;
    pendingTimeouts = new Set();
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    globalThis.setTimeout = ((handler: TimerHandler, timeout?: number, ...args: any[]) => {
      let timeoutId: ReturnType<typeof globalThis.setTimeout>;
      timeoutId = originalSetTimeout((...callbackArgs: any[]) => {
        pendingTimeouts.delete(timeoutId);
        if (typeof handler === 'function') {
          handler(...callbackArgs);
        }
      }, timeout, ...args);
      pendingTimeouts.add(timeoutId);
      return timeoutId;
    }) as typeof globalThis.setTimeout;
    globalThis.clearTimeout = ((timeoutId?: ReturnType<typeof globalThis.setTimeout>) => {
      if (timeoutId !== undefined) {
        pendingTimeouts.delete(timeoutId);
      }
      return originalClearTimeout(timeoutId);
    }) as typeof globalThis.clearTimeout;
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockResolvedValue(true);
  });

  afterEach(() => {
    for (const timeoutId of pendingTimeouts) {
      originalClearTimeout(timeoutId);
    }
    pendingTimeouts.clear();
    globalThis.setTimeout = originalSetTimeout;
    globalThis.clearTimeout = originalClearTimeout;
    vi.unstubAllGlobals();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it('stops at a migration failure marker instead of retrying the daemon', async () => {
    let ensureAttempts = 0;
    vi.mocked(invoke).mockImplementation(async (cmd) => {
      if (cmd === 'ensure_daemon') {
        ensureAttempts++;
        throw new Error('daemon ensure failed: conversion aborted');
      }
      if (cmd === 'read_migration_failure') {
        return {
          marker_path: '/tmp/attn/migration-failure.json',
          contents: JSON.stringify({ error: 'conversion aborted', database_path: '/tmp/attn/attn.db' }),
        };
      }
      return true;
    });

    const { result, unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    await waitFor(() => {
      expect(result.current.migrationFailure).toEqual({
        markerPath: '/tmp/attn/migration-failure.json',
        facts: [
          { label: 'Database', value: '/tmp/attn/attn.db' },
          { label: 'Error', value: 'conversion aborted' },
        ],
      });
    });
    expect(pendingTimeouts.size).toBe(0);
    expect(ensureAttempts).toBe(1);
    expect(FakeWebSocket.instances).toHaveLength(0);

    unmount();
  });

  it('stops at a marker it cannot read and shows why', async () => {
    let ensureAttempts = 0;
    vi.mocked(invoke).mockImplementation(async (cmd) => {
      if (cmd === 'ensure_daemon') {
        ensureAttempts++;
        throw new Error('daemon ensure failed: conversion aborted');
      }
      if (cmd === 'read_migration_failure') {
        return { marker_path: '/tmp/attn/migration-failure.json', contents: '', read_error: 'Permission denied (os error 13)' };
      }
      return true;
    });

    const { result, unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    await waitFor(() => {
      expect(result.current.migrationFailure).toEqual({
        markerPath: '/tmp/attn/migration-failure.json',
        facts: [{ label: 'Marker could not be read', value: 'Permission denied (os error 13)' }],
      });
    });
    expect(pendingTimeouts.size).toBe(0);
    expect(ensureAttempts).toBe(1);
    expect(FakeWebSocket.instances).toHaveLength(0);

    unmount();
  });

  it('serializes endpoint actions so concurrent removals do not collide', async () => {
    const { result, unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onEndpointsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    const first = result.current.sendRemoveEndpoint('ep-1');
    await expect(result.current.sendRemoveEndpoint('ep-2')).rejects.toThrow(
      'Another endpoint action is already in progress',
    );

    act(() => {
      ws.emit({
        event: 'endpoint_action_result',
        action: 'remove',
        endpoint_id: 'ep-1',
        success: true,
      });
    });

    await expect(first).resolves.toMatchObject({ success: true, endpoint_id: 'ep-1' });
    unmount();
  });

  it('sends the requested cwd and agent when spawning a new agent session', async () => {
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();
    const spawnPromise = ptySpawn({
      args: {
        id: 'sess-new',
        cwd: '/tmp/repo',
        agent: 'claude',
        cols: 80,
        rows: 24,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({
        cmd: 'spawn_session',
        id: 'sess-new',
        cwd: '/tmp/repo',
        placement: {},
        agent: 'claude',
        cols: 80,
        rows: 24,
      });
    });

    act(() => {
      ws.emit({ event: 'spawn_result', id: 'sess-new', success: true, placement_error: 'desktop desktop-1 is gone' });
    });
    await expect(spawnPromise).resolves.toEqual({ placementError: 'desktop desktop-1 is gone' });
    expect(ws.sent.map((entry) => JSON.parse(entry)).filter((message) =>
      message.cmd === 'attach_session' || message.cmd === 'pty_resize',
    )).toEqual([]);
    unmount();
  });

});
