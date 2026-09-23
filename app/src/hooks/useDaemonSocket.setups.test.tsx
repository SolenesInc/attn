import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { PROTOCOL_VERSION, useDaemonSocket } from './useDaemonSocket';
import { SetupCommandError } from './daemonSetupEvents';
import { useSetupsStore } from '../store/setups';
import { SELECTED_SETUP_STORAGE_KEY } from '../utils/selectedSetup';
import type { Desktop, Setup } from '../types/generated';
import { tileContentKey } from '../types/workspace';

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

  commands(cmd: string): Array<Record<string, unknown>> {
    return this.sent.map((raw) => JSON.parse(raw)).filter((message) => message.cmd === cmd);
  }
}

function setup(id: string, currentDesktopId: string, lastUsedAt = '2026-09-23T10:00:00Z'): Setup {
  return { id, name: id === 'set-default' ? 'Default' : 'Work', current_desktop_id: currentDesktopId, last_used_at: lastUsedAt, revision: 1 };
}

const MARKDOWN_TILE = JSON.stringify({ type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/notes/plan.md' });

function desktop(id: string, setupId: string, slot?: number, treeJson = ''): Desktop {
  return {
    id,
    setup_id: setupId,
    name: '',
    ...(slot ? { shortcut_slot: slot } : {}),
    order_key: id,
    tree_json: treeJson,
    active_pane_id: '',
    panes: [],
    revision: 1,
  };
}

describe('useDaemonSocket setups', () => {
  beforeEach(() => {
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    vi.mocked(isTauri).mockReturnValue(false);
    window.localStorage.clear();
    useSetupsStore.setState({ setups: [], selectedSetupId: null, desktops: [], previousDesktopId: null });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  async function connect() {
    const rendered = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );
    await waitFor(() => expect(FakeWebSocket.instances.length).toBeGreaterThan(0));
    const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
    await waitFor(() => expect(ws.readyState).toBe(FakeWebSocket.OPEN));
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
        setups: [setup('set-default', 'd1'), setup('set-work', 'w1', '2026-09-22T10:00:00Z')],
        selected_setup_id: 'set-default',
        desktops: [desktop('d1', 'set-default', 1), desktop('d2', 'set-default', 2), desktop('d10', 'set-default')],
      });
    });
    return { ws, result: rendered.result };
  }

  it('scopes to the setup hello resolved and remembers it for the next hello', async () => {
    window.localStorage.setItem(SELECTED_SETUP_STORAGE_KEY, 'set-deleted');
    const { ws } = await connect();

    expect(JSON.parse(ws.sent[0])).toMatchObject({ cmd: 'client_hello', setup_id: 'set-deleted' });
    expect(useSetupsStore.getState()).toMatchObject({
      selectedSetupId: 'set-default',
      previousDesktopId: null,
    });
    expect(useSetupsStore.getState().desktops.map((entry) => entry.id)).toEqual(['d1', 'd2', 'd10']);
    expect(window.localStorage.getItem(SELECTED_SETUP_STORAGE_KEY)).toBe('set-default');
  });

  it('follows desktop switches made by another client and remembers where to bounce back', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({
        event: 'setup_arrangement_changed',
        setup: setup('set-default', 'd2'),
        desktops: [{ ...desktop('d2', 'set-default', 2), active_pane_id: '' }],
      });
      ws.emit({ event: 'setups_changed', setups: [setup('set-default', 'd2'), setup('set-work', 'w1')] });
    });

    const state = useSetupsStore.getState();
    expect(state.setups.find((entry) => entry.id === 'set-default')?.current_desktop_id).toBe('d2');
    expect(state.previousDesktopId).toBe('d1');
  });

  it('forgets the bounce target when that desktop is deleted', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'setup_arrangement_changed', setup: setup('set-default', 'd2'), desktops: [] });
      ws.emit({ event: 'setup_arrangement_changed', setup: setup('set-default', 'd2'), desktops: [], deleted_desktop_ids: ['d1'] });
    });

    expect(useSetupsStore.getState().previousDesktopId).toBeNull();
    expect(useSetupsStore.getState().desktops.map((entry) => entry.id)).toEqual(['d2', 'd10']);
  });

  it('ignores current-desktop moves of setups this client is not on', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'setups_changed', setups: [setup('set-default', 'd1'), setup('set-work', 'w2')] });
    });

    expect(useSetupsStore.getState().previousDesktopId).toBeNull();
  });

  it('rescopes to the setup a setup_select answers with', async () => {
    const { ws, result } = await connect();

    let selection!: Promise<unknown>;
    act(() => {
      selection = result.current.sendSetupSelect('set-work');
    });
    const [command] = ws.commands('setup_select');
    expect(command).toMatchObject({ setup_id: 'set-work' });
    act(() => {
      ws.emit({
        event: 'setup_action_result',
        request_id: command.request_id,
        action: 'setup_select',
        success: true,
        setup: setup('set-work', 'w1', '2026-09-23T11:00:00Z'),
        desktops: [desktop('w1', 'set-work', 1)],
      });
    });
    await selection;

    expect(useSetupsStore.getState()).toMatchObject({ selectedSetupId: 'set-work', previousDesktopId: null });
    expect(useSetupsStore.getState().desktops.map((entry) => entry.id)).toEqual(['w1']);
    expect(window.localStorage.getItem(SELECTED_SETUP_STORAGE_KEY)).toBe('set-work');
  });

  it('moves to the destination when the daemon deletes the setup this client is on', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'setups_changed', setups: [setup('set-work', 'w1')] });
      ws.emit({
        event: 'setup_arrangement_changed',
        setup: setup('set-work', 'w1'),
        desktops: [desktop('w1', 'set-work', 1), desktop('w2', 'set-work', 2)],
      });
    });

    expect(useSetupsStore.getState().selectedSetupId).toBe('set-work');
    expect(useSetupsStore.getState().desktops.map((entry) => entry.id)).toEqual(['w1', 'w2']);
    expect(window.localStorage.getItem(SELECTED_SETUP_STORAGE_KEY)).toBe('set-work');
  });

  it('ignores a late arrangement of the setup it just left', async () => {
    const { ws, result } = await connect();

    let selection!: Promise<unknown>;
    act(() => {
      selection = result.current.sendSetupSelect('set-work');
    });
    const [command] = ws.commands('setup_select');
    act(() => {
      ws.emit({
        event: 'setup_action_result',
        request_id: command.request_id,
        action: 'setup_select',
        success: true,
        setup: setup('set-work', 'w1'),
        desktops: [desktop('w1', 'set-work', 1)],
      });
      ws.emit({ event: 'setup_arrangement_changed', setup: setup('set-default', 'd2'), desktops: [desktop('d2', 'set-default', 2)] });
    });
    await selection;

    expect(useSetupsStore.getState().selectedSetupId).toBe('set-work');
    expect(useSetupsStore.getState().desktops.map((entry) => entry.id)).toEqual(['w1']);
    expect(window.localStorage.getItem(SELECTED_SETUP_STORAGE_KEY)).toBe('set-work');
  });

  it('rejects a refused command with its error code', async () => {
    const { ws, result } = await connect();

    let move!: Promise<unknown>;
    act(() => {
      move = result.current.sendDesktopMoveLeaf({
        sourceDesktopId: 'd1',
        targetDesktopId: 'd2',
        leafId: 'pane-1',
        edge: 'right',
        expectedSourceRevision: 1,
        expectedTargetRevision: 1,
      });
    });
    const [command] = ws.commands('desktop_move_leaf');
    expect(command).toMatchObject({
      source_desktop_id: 'd1',
      target_desktop_id: 'd2',
      leaf_id: 'pane-1',
      edge: 'right',
      expected_source_revision: 1,
      expected_target_revision: 1,
    });
    act(() => {
      ws.emit({
        event: 'setup_action_result',
        request_id: command.request_id,
        action: 'desktop_move_leaf',
        success: false,
        error: 'desktop d2 changed since revision 1',
        error_code: 'stale_revision',
      });
    });

    await expect(move).rejects.toBeInstanceOf(SetupCommandError);
    await expect(move).rejects.toMatchObject({ code: 'stale_revision', message: 'desktop d2 changed since revision 1' });
  });
  it('docks a tile and follows its content on a desktop', async () => {
    const { ws, result } = await connect();

    let dock!: Promise<unknown>;
    act(() => {
      dock = result.current.sendDesktopDockTile({
        desktopId: 'd1',
        expectedRevision: 1,
        tileId: 'tile-md',
        tileKind: 'markdown',
        tileParams: '/notes/plan.md',
        edge: 'right',
      });
      result.current.sendDesktopTileContentGet('d1', 'tile-md');
    });
    const [command] = ws.commands('desktop_dock_tile');
    expect(command).toMatchObject({
      desktop_id: 'd1',
      expected_revision: 1,
      tile_id: 'tile-md',
      tile_kind: 'markdown',
      tile_params: '/notes/plan.md',
      edge: 'right',
    });
    expect(ws.commands('desktop_tile_content_get')).toEqual([
      { cmd: 'desktop_tile_content_get', desktop_id: 'd1', tile_id: 'tile-md' },
    ]);
    act(() => {
      ws.emit({ event: 'setup_action_result', request_id: command.request_id, action: 'desktop_dock_tile', success: true, desktops: [desktop('d1', 'set-default', 1, MARKDOWN_TILE)] });
      ws.emit({ event: 'setup_arrangement_changed', setup: setup('set-default', 'd1'), desktops: [desktop('d1', 'set-default', 1, MARKDOWN_TILE)] });
      ws.emit({
        event: 'desktop_tile_content',
        desktop_id: 'd1',
        tile_id: 'tile-md',
        tile_kind: 'markdown',
        path: '/notes/plan.md',
        content: '# Plan',
      });
    });
    await dock;

    expect(result.current.desktopTileContents[tileContentKey('d1', 'tile-md')]).toEqual({
      path: '/notes/plan.md',
      content: '# Plan',
      error: undefined,
    });

    act(() => {
      ws.emit({ event: 'setup_arrangement_changed', setup: setup('set-default', 'd1'), desktops: [desktop('d1', 'set-default', 1)] });
    });
    expect(result.current.desktopTileContents).toEqual({});
  });
});
