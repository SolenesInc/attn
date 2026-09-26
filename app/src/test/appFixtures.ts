import { act, fireEvent, screen } from '@testing-library/react';
import { onTestFinished, vi } from 'vitest';
import { daemonSession, workspaceWithTiles, type DaemonSession, type DaemonTile, type DaemonWorkspace } from './daemonFixtures';
import { fakeRects } from './layout';
import type { EventMessage } from './protocol';
import { pressShortcut, renderApp } from './renderApp';
import type { ScriptedDaemon } from './scriptedDaemon';

type InitialState = Partial<EventMessage<'initial_state'>>;

export async function openSession(daemon: ScriptedDaemon, sessionId: string) {
  fireEvent.click(screen.getByRole('button', { name: `Open ${sessionId}` }));
  await daemon.idle();
}

export async function openActionMenu(daemon: ScriptedDaemon) {
  pressShortcut('ui.actionMenu');
  await act(() => vi.advanceTimersToNextFrame());
  await daemon.idle();
  return screen.getByRole('textbox', { name: 'Search actions' });
}

export function stubTextLayout() {
  const rect = new DOMRect(40, 20, 8, 16);
  const rects = vi.spyOn(Range.prototype, 'getClientRects').mockReturnValue(
    Object.assign([rect], { item: (index: number) => (index === 0 ? rect : null) }),
  );
  const bounds = vi.spyOn(Range.prototype, 'getBoundingClientRect').mockReturnValue(rect);
  onTestFinished(() => {
    rects.mockRestore();
    bounds.mockRestore();
  });
}

export interface TileOptions {
  workspace?: Partial<DaemonWorkspace>;
  session?: Partial<DaemonSession>;
  initialState?: InitialState;
  persisted?: boolean;
  script?: (daemon: ScriptedDaemon) => void;
}

export async function openTiles(tiles: DaemonTile[], { workspace = {}, session = {}, initialState = {}, persisted = false, script }: TileOptions = {}) {
  const layoutOf = (next: DaemonTile[]) => workspaceWithTiles(next, workspace);
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { workspace_id: layoutOf([]).id, ...session })],
      workspaces: [layoutOf(persisted ? tiles : [])],
      ...initialState,
    },
  });
  script?.(view.daemon);
  await openSession(view.daemon, 's1');
  const layout = async (next: DaemonTile[]) => {
    view.daemon.emit({ event: 'workspace_layout_updated', workspace_layout: layoutOf(next).layout! });
    await view.daemon.idle();
  };
  await layout(tiles);
  const tile = (tileId = tiles[0]?.tile_id) => document.querySelector<HTMLElement>(`[data-pane-id="${tileId}"]`)!;
  return { ...view, layout, tile };
}

export async function openMarkdownTiles(
  content: string,
  { path, tileIds = ['tile-a'], ...options }: TileOptions & { path: string; tileIds?: string[] },
) {
  const view = await openTiles(tileIds.map((tileId) => ({ tile_id: tileId, tile_kind: 'markdown', tile_params: path, tile_session_id: 's1' })), options);
  const workspaceId = options.workspace?.id ?? 'ws';
  const show = async (next: string) => {
    for (const tileId of tileIds) {
      view.daemon.emit({ event: 'workspace_tile_content', workspace_id: workspaceId, tile_id: tileId, tile_kind: 'markdown', path, content: next });
    }
    await view.daemon.idle();
  };
  await show(content);
  return { ...view, show };
}

export interface TerminalOptions {
  sessions: DaemonSession[];
  workspaces: DaemonWorkspace[];
  initialState?: InitialState;
  output?: Record<string, string>;
  script?: (daemon: ScriptedDaemon) => void;
}

export async function openAttachedTerminals({ sessions, workspaces, initialState = {}, output = {}, script }: TerminalOptions) {
  fakeRects((element) => (element.tagName === 'CANVAS' ? new DOMRect(0, 0, 800, 600) : null));
  const view = await renderApp({ initialState: { sessions, workspaces, ...initialState } });
  view.daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
  script?.(view.daemon);
  await openSession(view.daemon, sessions[0].id);
  for (const [sessionId, text] of Object.entries(output)) {
    view.daemon.emit({ event: 'pty_output', id: sessionId, seq: 1, data: btoa(text) });
  }
  await view.daemon.idle();
  return view;
}
