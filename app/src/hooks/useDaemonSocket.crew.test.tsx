import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
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

function member(id: string, bindingSession?: string) {
  return {
    id,
    charter_path: `/homes/${id}/CHARTER.md`,
    home_dir: `/homes/${id}`,
    awareness_dirs: [],
    ...(bindingSession ? { binding_session: bindingSession } : {}),
  };
}

describe('useDaemonSocket crew', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(false);
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  async function renderWithCrew(initialCrew?: unknown[]) {
    const onCrewUpdate = vi.fn();
    const rendered = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        onCrewUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );
    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
    });
    const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
    await waitFor(() => {
      expect(ws.readyState).toBe(FakeWebSocket.OPEN);
    });
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
        ...(initialCrew ? { crew: initialCrew } : {}),
      });
    });
    return { ws, onCrewUpdate, result: rendered.result };
  }

  it('draws the roster from initial_state', async () => {
    const { onCrewUpdate } = await renderWithCrew([member('keel'), member('trellis', 'sess-trellis')]);

    expect(onCrewUpdate).toHaveBeenCalledWith([
      expect.objectContaining({ id: 'keel' }),
      expect.objectContaining({ id: 'trellis', binding_session: 'sess-trellis' }),
    ]);
  });

  it('replaces the roster on every crew broadcast', async () => {
    const { ws, onCrewUpdate } = await renderWithCrew([member('keel')]);

    act(() => {
      ws.emit({ event: 'crew_updated', members: [member('keel', 'sess-keel')] });
    });

    expect(onCrewUpdate).toHaveBeenLastCalledWith([
      expect.objectContaining({ id: 'keel', binding_session: 'sess-keel' }),
    ]);
  });

  // An outpost holds no crew, and a daemon older than the app sends none: both read as an empty roster.
  it('reads a crew-less daemon as an empty roster', async () => {
    const { onCrewUpdate } = await renderWithCrew();

    expect(onCrewUpdate).toHaveBeenCalledWith([]);
  });

  it('resolves a wake with the session to focus', async () => {
    const { ws, result } = await renderWithCrew([member('keel')]);

    let woken: Promise<{ sessionId: string; alreadyAwake: boolean }>;
    act(() => {
      woken = result.current.sendCrewWake('keel');
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    expect(sent.cmd).toBe('crew_wake');
    expect(sent.member).toBe('keel');

    act(() => {
      ws.emit({
        event: 'crew_wake_result',
        request_id: sent.request_id,
        success: true,
        member: 'keel',
        session_id: 'sess-keel',
      });
    });

    await expect(woken!).resolves.toEqual({ sessionId: 'sess-keel', alreadyAwake: false });
  });

  it('resolves an already-awake member with its running day', async () => {
    const { ws, result } = await renderWithCrew([member('keel', 'sess-keel')]);

    let woken: Promise<{ sessionId: string; alreadyAwake: boolean }>;
    act(() => {
      woken = result.current.sendCrewWake('keel');
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    act(() => {
      ws.emit({
        event: 'crew_wake_result',
        request_id: sent.request_id,
        success: true,
        member: 'keel',
        session_id: 'sess-keel',
        already_awake: true,
      });
    });

    await expect(woken!).resolves.toEqual({ sessionId: 'sess-keel', alreadyAwake: true });
  });

  it('rejects a refused wake with what the daemon said', async () => {
    const { ws, result } = await renderWithCrew([member('keel')]);

    let woken: Promise<unknown>;
    act(() => {
      woken = result.current.sendCrewWake('keel');
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    act(() => {
      ws.emit({
        event: 'crew_wake_result',
        request_id: sent.request_id,
        success: false,
        error: 'keel launches in /gone, which is not there',
      });
    });

    await expect(woken!).rejects.toThrow('/gone');
  });

  it('asks an awake member to sleep and carries the delivery receipt', async () => {
    const { ws, result } = await renderWithCrew([member('keel', 'sess-keel')]);

    let asked: Promise<{
      member: string;
      sessionId?: string;
      alreadyAsleep: boolean;
      deliveryStatus?: string;
      detail?: string;
    }>;
    act(() => {
      asked = result.current.sendCrewSleep('keel');
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    expect(sent.cmd).toBe('crew_sleep');
    expect(sent.member).toBe('keel');

    act(() => {
      ws.emit({
        event: 'crew_sleep_result',
        request_id: sent.request_id,
        success: true,
        member: 'keel',
        session_id: 'sess-keel',
        delivery_status: 'delivered',
        detail: 'asked Keel to close its day and sleep',
      });
    });

    await expect(asked!).resolves.toEqual({
      member: 'keel',
      sessionId: 'sess-keel',
      alreadyAsleep: false,
      deliveryStatus: 'delivered',
      detail: 'asked Keel to close its day and sleep',
    });
  });

  it('names an already-asleep sleep request as a no-op', async () => {
    const { ws, result } = await renderWithCrew([member('keel')]);

    let asked: ReturnType<typeof result.current.sendCrewSleep>;
    act(() => {
      asked = result.current.sendCrewSleep('keel');
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    act(() => {
      ws.emit({
        event: 'crew_sleep_result',
        request_id: sent.request_id,
        success: true,
        member: 'keel',
        already_asleep: true,
        detail: 'Keel is already asleep',
      });
    });

    await expect(asked!).resolves.toEqual({
      member: 'keel',
      alreadyAsleep: true,
      detail: 'Keel is already asleep',
    });
  });

  it('rejects a refused sleep request with what the daemon said', async () => {
    const { ws, result } = await renderWithCrew([member('keel', 'sess-keel')]);

    let asked: Promise<unknown>;
    act(() => {
      asked = result.current.sendCrewSleep('keel');
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    act(() => {
      ws.emit({
        event: 'crew_sleep_result',
        request_id: sent.request_id,
        success: false,
        error: 'session sess-keel cannot receive agent messages',
      });
    });

    await expect(asked!).rejects.toThrow('cannot receive agent messages');
  });

  it('sends one atomic launch selection and returns the authoritative revision', async () => {
    const { ws, result } = await renderWithCrew([member('keel')]);

    let saved: ReturnType<typeof result.current.sendCrewSet>;
    act(() => {
      saved = result.current.sendCrewSet({
        member: 'keel', expectedRevision: 7, agent: 'codex', model: 'gpt-6-astra', effort: 'high',
      });
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    expect(sent).toMatchObject({
      cmd: 'crew_set', member: 'keel', expected_revision: 7,
      agent: 'codex', model: 'gpt-6-astra', effort: 'high',
    });

    const current = {
      ...member('keel'), revision: 8, agent: 'codex', model: 'gpt-6-astra', effort: 'high',
      resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high',
    };
    act(() => {
      ws.emit({
        event: 'crew_set_result', request_id: sent.request_id, success: true, conflict: false, member: current,
      });
    });

    await expect(saved!).resolves.toEqual({ success: true, conflict: false, member: current });
  });

  it('preserves an authoritative member on a settings conflict', async () => {
    const { ws, result } = await renderWithCrew([member('keel')]);
    let saved: ReturnType<typeof result.current.sendCrewSet>;
    act(() => {
      saved = result.current.sendCrewSet({
        member: 'keel', expectedRevision: 7, agent: '', model: '', effort: '',
      });
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    const current = { ...member('keel'), revision: 9, resolved_agent: 'claude' };
    act(() => {
      ws.emit({
        event: 'crew_set_result', request_id: sent.request_id, success: false, conflict: true,
        error: 'the member changed', member: current,
      });
    });

    await expect(saved!).resolves.toEqual({
      success: false, conflict: true, error: 'the member changed', member: current,
    });
  });

  it('correlates a full charter read and ignores another request result', async () => {
    const { ws, result } = await renderWithCrew([member('trellis')]);
    let read: ReturnType<typeof result.current.sendCrewCharterGet>;
    act(() => { read = result.current.sendCrewCharterGet('trellis'); });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    expect(sent).toMatchObject({ cmd: 'crew_charter_get', member: 'trellis' });

    act(() => {
      ws.emit({
        event: 'crew_charter_get_result', request_id: 'another-request', success: true,
        member: 'trellis', charter: { content: 'wrong', token: 'wrong' },
      });
      ws.emit({
        event: 'crew_charter_get_result', request_id: sent.request_id, success: true,
        member: 'trellis', charter: { content: '# Trellis\n', token: 'charter-token' },
      });
    });

    await expect(read!).resolves.toEqual({
      member: 'trellis', charter: { content: '# Trellis\n', token: 'charter-token' },
    });
  });

  it('sends charter content with its CAS token and returns conflicts as data', async () => {
    const { ws, result } = await renderWithCrew([member('alder')]);
    let saved: ReturnType<typeof result.current.sendCrewCharterSet>;
    act(() => { saved = result.current.sendCrewCharterSet('alder', '# Mine\n', 'old-token'); });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    expect(sent).toMatchObject({
      cmd: 'crew_charter_set', member: 'alder', content: '# Mine\n', expected_token: 'old-token',
    });

    act(() => {
      ws.emit({
        event: 'crew_charter_set_result', request_id: sent.request_id, success: true, conflict: true,
        member: 'alder', charter: { content: '# External\n', token: 'external-token' },
      });
    });
    await expect(saved!).resolves.toEqual({
      member: 'alder', conflict: true,
      charter: { content: '# External\n', token: 'external-token' },
    });
  });

  it('correlates an honest empty handoff history', async () => {
    const { ws, result } = await renderWithCrew([member('keel')]);
    let read: ReturnType<typeof result.current.sendCrewHandoffsGet>;
    act(() => { read = result.current.sendCrewHandoffsGet('keel'); });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    expect(sent).toMatchObject({ cmd: 'crew_handoffs_get', member: 'keel' });

    act(() => {
      ws.emit({
        event: 'crew_handoffs_get_result', request_id: sent.request_id, success: true,
        member: 'keel', handoffs: [],
      });
    });
    await expect(read!).resolves.toEqual({ member: 'keel', handoffs: [] });
  });

  it('reuses the caller restart identity and ignores another request result', async () => {
    const { ws, result } = await renderWithCrew([member('keel', 'sess-keel')]);
    let restarted: ReturnType<typeof result.current.sendCrewRestart>;
    act(() => {
      restarted = result.current.sendCrewRestart({
        member: 'keel', requestId: 'restart-stable', expectedSessionId: 'sess-keel', expectedRevision: 12,
      });
    });
    const sent = JSON.parse(ws.sent[ws.sent.length - 1]);
    expect(sent).toEqual({
      cmd: 'crew_restart', request_id: 'restart-stable', member: 'keel',
      expected_session_id: 'sess-keel', expected_revision: 12,
    });

    const current = {
      ...member('keel', 'sess-keel'), revision: 13, resolved_agent: 'claude',
      restart: { request_id: 'restart-stable', session_id: 'sess-keel', state: 'queued' },
    };
    act(() => {
      ws.emit({
        event: 'crew_restart_result', request_id: 'another-request', success: true, conflict: false,
        member: current, restart: current.restart,
      });
      ws.emit({
        event: 'crew_restart_result', request_id: 'restart-stable', success: true, conflict: false,
        member: current, restart: current.restart,
      });
    });

    await expect(restarted!).resolves.toEqual({
      success: true, conflict: false, member: current, restart: current.restart,
    });
  });
});
