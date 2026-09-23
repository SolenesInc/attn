import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { PROTOCOL_VERSION, useDaemonSocket } from './useDaemonSocket';
import { ProfileCommandError } from './daemonProfileEvents';
import { useProfilesStore } from '../store/profiles';
import { SELECTED_PROFILE_STORAGE_KEY } from '../utils/selectedProfile';
import type { Desktop, Profile } from '../types/generated';
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

function profile(id: string, currentDesktopId: string, lastUsedAt = '2026-09-23T10:00:00Z'): Profile {
  return { id, name: id === 'set-default' ? 'Default' : 'Work', current_desktop_id: currentDesktopId, last_used_at: lastUsedAt, revision: 1 };
}

const MARKDOWN_TILE = JSON.stringify({ type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/notes/plan.md' });

function desktop(id: string, profileId: string, slot?: number, treeJson = ''): Desktop {
  return {
    id,
    profile_id: profileId,
    name: '',
    ...(slot ? { shortcut_slot: slot } : {}),
    order_key: id,
    tree_json: treeJson,
    active_pane_id: '',
    panes: [],
    revision: 1,
  };
}

const DEFAULT_DESKTOPS = [desktop('d1', 'set-default', 1), desktop('d2', 'set-default', 2), desktop('d10', 'set-default')];

describe('useDaemonSocket profiles', () => {
  beforeEach(() => {
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    vi.mocked(isTauri).mockReturnValue(false);
    window.localStorage.clear();
    useProfilesStore.setState({ profiles: [], selectedProfileId: null, currentDesktopId: null, desktops: [], previousDesktopId: null });
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
        profiles: [profile('set-default', 'd1'), profile('set-work', 'w1', '2026-09-22T10:00:00Z')],
        selected_profile_id: 'set-default',
        desktops: DEFAULT_DESKTOPS,
      });
    });
    return { ws, result: rendered.result };
  }

  it('scopes to the profile hello resolved and remembers it for the next hello', async () => {
    window.localStorage.setItem(SELECTED_PROFILE_STORAGE_KEY, 'set-deleted');
    const { ws } = await connect();

    expect(JSON.parse(ws.sent[0])).toMatchObject({ cmd: 'client_hello', profile_id: 'set-deleted' });
    expect(useProfilesStore.getState()).toMatchObject({
      selectedProfileId: 'set-default',
      currentDesktopId: 'd1',
      previousDesktopId: null,
    });
    expect(useProfilesStore.getState().desktops.map((entry) => entry.id)).toEqual(['d1', 'd2', 'd10']);
    expect(window.localStorage.getItem(SELECTED_PROFILE_STORAGE_KEY)).toBe('set-default');
  });

  it('follows desktop switches made by another client and remembers where to bounce back', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd2'), desktops: DEFAULT_DESKTOPS });
    });

    expect(useProfilesStore.getState()).toMatchObject({ currentDesktopId: 'd2', previousDesktopId: 'd1' });
  });

  it('forgets the bounce target when that desktop is deleted', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd2'), desktops: DEFAULT_DESKTOPS });
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd2'), desktops: DEFAULT_DESKTOPS.slice(1) });
    });

    expect(useProfilesStore.getState().previousDesktopId).toBeNull();
    expect(useProfilesStore.getState().desktops.map((entry) => entry.id)).toEqual(['d2', 'd10']);
  });

  it('shows the last arrangement it received in full', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd1'), desktops: [{ ...desktop('d2', 'set-default', 2, MARKDOWN_TILE), revision: 5 }] });
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd1'), desktops: [desktop('d1', 'set-default', 1), { ...desktop('d2', 'set-default', 2), revision: 6 }] });
    });

    expect(useProfilesStore.getState().desktops.map((entry) => [entry.id, entry.revision, entry.tree_json])).toEqual([
      ['d1', 1, ''],
      ['d2', 6, ''],
    ]);
  });

  it('leaves the current desktop to the arrangement when the profile list changes', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'profiles_changed', profiles: [profile('set-default', 'd2'), profile('set-work', 'w2')] });
    });

    expect(useProfilesStore.getState()).toMatchObject({ currentDesktopId: 'd1', previousDesktopId: null });
    expect(useProfilesStore.getState().profiles.map((entry) => entry.current_desktop_id)).toEqual(['d2', 'w2']);
  });

  it('moves to another profile when its arrangement arrives, not when profile_select answers', async () => {
    const { ws, result } = await connect();

    let selection!: Promise<unknown>;
    act(() => {
      selection = result.current.sendProfileSelect('set-work');
    });
    const [command] = ws.commands('profile_select');
    expect(command).toMatchObject({ profile_id: 'set-work' });
    act(() => {
      ws.emit({ event: 'profile_action_result', request_id: command.request_id, action: 'profile_select', success: true });
    });
    await selection;
    expect(useProfilesStore.getState().selectedProfileId).toBe('set-default');

    act(() => {
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-work', 'w1', '2026-09-23T11:00:00Z'), desktops: [desktop('w1', 'set-work', 1)] });
    });

    expect(useProfilesStore.getState()).toMatchObject({ selectedProfileId: 'set-work', currentDesktopId: 'w1', previousDesktopId: null });
    expect(useProfilesStore.getState().desktops.map((entry) => entry.id)).toEqual(['w1']);
    expect(window.localStorage.getItem(SELECTED_PROFILE_STORAGE_KEY)).toBe('set-work');
  });

  it('moves to the destination when the daemon deletes the profile this client is on', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'profiles_changed', profiles: [profile('set-work', 'w1')] });
      ws.emit({
        event: 'profile_arrangement_changed',
        profile: profile('set-work', 'w1'),
        desktops: [desktop('w1', 'set-work', 1), desktop('w2', 'set-work', 2)],
      });
    });

    expect(useProfilesStore.getState().selectedProfileId).toBe('set-work');
    expect(useProfilesStore.getState().desktops.map((entry) => entry.id)).toEqual(['w1', 'w2']);
    expect(window.localStorage.getItem(SELECTED_PROFILE_STORAGE_KEY)).toBe('set-work');
  });

  it('ends on the profile whose arrangement arrived last when a late one of the old profile comes first', async () => {
    const { ws } = await connect();

    act(() => {
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd2'), desktops: DEFAULT_DESKTOPS });
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-work', 'w1'), desktops: [desktop('w1', 'set-work', 1)] });
    });

    expect(useProfilesStore.getState()).toMatchObject({ selectedProfileId: 'set-work', currentDesktopId: 'w1', previousDesktopId: null });
    expect(useProfilesStore.getState().desktops.map((entry) => entry.id)).toEqual(['w1']);
    expect(window.localStorage.getItem(SELECTED_PROFILE_STORAGE_KEY)).toBe('set-work');
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
        event: 'profile_action_result',
        request_id: command.request_id,
        action: 'desktop_move_leaf',
        success: false,
        error: 'desktop d2 changed since revision 1',
        error_code: 'stale_revision',
      });
    });

    await expect(move).rejects.toBeInstanceOf(ProfileCommandError);
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
    expect(ws.commands('desktop_tile_content_get')).toEqual([]);
    act(() => {
      ws.emit({ event: 'profile_action_result', request_id: command.request_id, action: 'desktop_dock_tile', success: true, desktops: [desktop('d1', 'set-default', 1, MARKDOWN_TILE)] });
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd1'), desktops: [desktop('d1', 'set-default', 1, MARKDOWN_TILE)] });
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
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd1'), desktops: [desktop('d1', 'set-default', 1)] });
    });
    expect(result.current.desktopTileContents).toEqual({});
  });

  it('keeps content that arrives before the arrangement of its desktop', async () => {
    const { ws, result } = await connect();

    act(() => {
      ws.emit({ event: 'desktop_tile_content', desktop_id: 'w1', tile_id: 'tile-md', tile_kind: 'markdown', path: '/notes/plan.md', content: '# Plan' });
    });
    act(() => {
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-work', 'w1'), desktops: [desktop('w1', 'set-work', 1, MARKDOWN_TILE)] });
    });

    expect(result.current.desktopTileContents[tileContentKey('w1', 'tile-md')]).toMatchObject({ content: '# Plan' });
  });

  it('keeps content only for the current desktop', async () => {
    const { ws, result } = await connect();
    const withTile = [desktop('d1', 'set-default', 1, MARKDOWN_TILE), desktop('d2', 'set-default', 2, MARKDOWN_TILE)];

    act(() => {
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd1'), desktops: withTile });
      ws.emit({ event: 'desktop_tile_content', desktop_id: 'd1', tile_id: 'tile-md', tile_kind: 'markdown', path: '/notes/plan.md', content: '# Plan' });
    });
    expect(Object.keys(result.current.desktopTileContents)).toEqual([tileContentKey('d1', 'tile-md')]);

    act(() => {
      ws.emit({ event: 'profile_arrangement_changed', profile: profile('set-default', 'd2'), desktops: withTile });
    });
    expect(result.current.desktopTileContents).toEqual({});
  });
});
