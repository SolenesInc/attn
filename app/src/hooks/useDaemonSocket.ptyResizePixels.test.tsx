// Zero and absent mean the same thing to the daemon, so a resize that measured
// nothing must OMIT the pixels rather than send zeros.
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { ptyResize } from '../pty/bridge';
import { PROTOCOL_VERSION, useDaemonSocket } from './useDaemonSocket';

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

async function waitForOpenSocket(): Promise<FakeWebSocket> {
  await waitFor(() => {
    expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
  });
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  expect(ws).toBeDefined();
  await waitFor(() => {
    expect(ws.readyState).toBe(FakeWebSocket.OPEN);
  });
  return ws;
}

function renderSocket() {
  return renderHook(() =>
    useDaemonSocket({
      onSessionsUpdate: vi.fn(),
      onWorkspacesUpdate: vi.fn(),
      onPRsUpdate: vi.fn(),
      onReposUpdate: vi.fn(),
      onAuthorsUpdate: vi.fn(),
      wsUrl: 'ws://localhost:9999/ws',
    }),
  );
}

function emitInitialState(ws: FakeWebSocket) {
  act(() => {
    ws.emit({
      event: 'initial_state',
      protocol_version: PROTOCOL_VERSION,
      sessions: [],
      workspaces: [],
      prs: [],
      repos: [],
      authors: [],
      settings: {},
    });
  });
}

async function resizeCommand(ws: FakeWebSocket) {
  let command: Record<string, unknown> | undefined;
  await waitFor(() => {
    command = ws.sent
      .map((entry) => JSON.parse(entry) as Record<string, unknown>)
      .find((entry) => entry.cmd === 'pty_resize');
    expect(command).toBeDefined();
  });
  return command!;
}

describe('useDaemonSocket pty_resize pixel geometry', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockResolvedValue(true);
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  it('carries the pane total a fit measured', async () => {
    const { unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    await act(async () => {
      await ptyResize({ id: 'sess-1', cols: 40, rows: 12, reason: 'ghostty_fit', xpixel: 720, ypixel: 540 });
    });

    expect(await resizeCommand(ws)).toEqual({
      cmd: 'pty_resize', id: 'sess-1', cols: 40, rows: 12, xpixel: 720, ypixel: 540,
    });
    unmount();
  });

  it('omits the fields entirely when the resize measured nothing', async () => {
    const { unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    await act(async () => {
      await ptyResize({ id: 'sess-1', cols: 40, rows: 12, reason: 'daemon_known_attach' });
    });

    expect(await resizeCommand(ws)).toEqual({ cmd: 'pty_resize', id: 'sess-1', cols: 40, rows: 12 });
    unmount();
  });

  it('omits a half-measured pane rather than reporting one axis', async () => {
    const { unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    await act(async () => {
      await ptyResize({ id: 'sess-1', cols: 40, rows: 12, xpixel: 720, ypixel: 0 });
    });

    expect(await resizeCommand(ws)).toEqual({ cmd: 'pty_resize', id: 'sess-1', cols: 40, rows: 12 });
    unmount();
  });
});
