import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { fakeRects } from './test/layout';
import { pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { pane, renderWorkspace, split, splitWorkspace } from './test/workspaces';

const PANES_WIDTH = 4000;
const SIDE_BY_SIDE = split('split-a', 'vertical', [pane('s1'), pane('s2')]);

function layOutPanes(width: number, height: number) {
  fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, width, height) : null));
}

async function openWorkspace(root: unknown, sessionIds: string[]) {
  const view = await renderWorkspace(root, sessionIds);
  await openSession(view.daemon, sessionIds[0]);
  return view;
}

function splitRatio(splitId: string) {
  return document.querySelector(`.workspace-split[data-split-id="${splitId}"]`)?.getAttribute('data-split-ratio');
}

async function dragDivider(daemon: ScriptedDaemon, splitId: string, clientX: number) {
  const divider = document.querySelector<HTMLElement>(`.workspace-split-divider[data-split-id="${splitId}"]`)!;
  const dividerX = (parseFloat(divider.style.left) / 100) * PANES_WIDTH;
  fireEvent.pointerDown(divider, { button: 0, pointerId: 1, clientX: dividerX });
  fireEvent.pointerUp(window, { pointerId: 1, clientX });
  await daemon.idle();
  return daemon.sentOf('workspace_layout_set_split_ratio').pop()!;
}

function refuseSplitRatio(daemon: ScriptedDaemon, request: { split_id: string; request_id?: string }, error: string) {
  daemon.emit({
    event: 'workspace_layout_action_result',
    action: 'workspace_layout_set_split_ratio',
    workspace_id: 'ws',
    split_id: request.split_id,
    request_id: request.request_id,
    success: false,
    error,
  });
}

function acceptSplitRatio(daemon: ScriptedDaemon, request: { split_id: string; request_id?: string }) {
  daemon.emit({
    event: 'workspace_layout_action_result',
    action: 'workspace_layout_set_split_ratio',
    workspace_id: 'ws',
    split_id: request.split_id,
    request_id: request.request_id,
    success: true,
  });
}

describe('App workspace layout', () => {
  it('rolls back a split whose shell failed to spawn, and reports it once its own pane has closed', async () => {
    const { daemon } = await openWorkspace(pane('s1'), ['s1']);
    daemon.on('workspace_layout_add_session_pane', ({ workspace_id, pane_id }) => ({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_add_session_pane',
      workspace_id,
      pane_id,
      success: true,
    }));
    daemon.on('spawn_session', ({ id }) => ({ event: 'spawn_result', id, success: false, error: 'shell exited during startup' }));

    pressShortcut('terminal.splitVertical');
    const close = await daemon.received('workspace_layout_close_pane');
    expect(close).toMatchObject({ workspace_id: 'ws', pane_id: daemon.sentOf('workspace_layout_add_session_pane')[0].pane_id });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_close_pane',
      workspace_id: 'workspace-other',
      pane_id: close.pane_id,
      success: true,
    });
    await daemon.idle();
    expect(screen.queryByText('shell exited during startup')).toBeNull();

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_close_pane',
      workspace_id: 'ws',
      pane_id: close.pane_id,
      success: true,
    });
    await daemon.idle();
    expect(screen.getByText('shell exited during startup')).toBeInTheDocument();
  });

  it('ranks a workspace dragged in the sidebar between its new neighbours', async () => {
    fakeRects((element) => (
      element.classList.contains('workspace-reorder-seam')
        ? new DOMRect(0, Number(element.dataset.seamIndex) * 100, 200, 0)
        : null
    ));
    const ids = ['a', 'b', 'c'];
    const { daemon } = await renderApp({
      initialState: {
        sessions: ids.map((id) => daemonSession(id)),
        workspaces: ids.map((id) => agentWorkspace(id)),
      },
    });
    const drag = async (label: string, fromY: number, toY: number) => {
      fireEvent.pointerDown(screen.getByRole('button', { name: `Open workspace ${label}` }), { button: 0, pointerId: 1, clientX: 10, clientY: fromY });
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: (fromY + toY) / 2 });
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: toY });
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 10, clientY: toY });
      await daemon.idle();
    };

    await drag('c', 250, 100);
    await drag('c', 250, 0);

    expect(daemon.sentOf('set_workspace_rank')).toEqual([
      { cmd: 'set_workspace_rank', workspace_id: 'workspace-c', prev_workspace_id: 'workspace-a', next_workspace_id: 'workspace-b' },
      { cmd: 'set_workspace_rank', workspace_id: 'workspace-c', next_workspace_id: 'workspace-a' },
    ]);
  });

  it('moves a pane dropped on the sidebar into a new workspace', async () => {
    const { daemon } = await openWorkspace(SIDE_BY_SIDE, ['s1', 's2']);

    fireEvent.pointerDown(document.querySelector('[data-pane-id="pane-s2"] .workspace-pane-header')!, { button: 0, pointerId: 1, clientX: 100, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 150, clientY: 60 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 200, clientY: 100 });
    const dropzone = screen.getByTestId('new-workspace-dropzone');
    fireEvent.pointerEnter(dropzone);
    fireEvent.pointerUp(dropzone, { pointerId: 1 });
    await daemon.idle();

    expect(daemon.sentOf('workspace_layout_move_leaf_to_new_workspace')).toEqual([{
      cmd: 'workspace_layout_move_leaf_to_new_workspace',
      source_workspace_id: 'ws',
      leaf_id: 'pane-s2',
      anchor_id: '',
      edge: 'left',
      ratio: 0.32,
    }]);
  });

  it('keeps a later split resize the daemon accepted when an earlier one on the same split is refused', async () => {
    layOutPanes(PANES_WIDTH, 600);
    const { daemon } = await openWorkspace(SIDE_BY_SIDE, ['s1', 's2']);

    const first = await dragDivider(daemon, 'split-a', 0.3 * PANES_WIDTH);
    const second = await dragDivider(daemon, 'split-a', 0.7 * PANES_WIDTH);
    expect([first.ratio, second.ratio]).toEqual([0.3, 0.7]);
    expect(first.request_id).not.toBe(second.request_id);

    acceptSplitRatio(daemon, second);
    await daemon.idle();
    expect(splitRatio('split-a')).toBe('0.700');

    refuseSplitRatio(daemon, first, 'first persist failed');
    await daemon.idle();
    expect(splitRatio('split-a')).toBe('0.700');
  });

  it('undoes only the split whose resize the daemon refused', async () => {
    layOutPanes(PANES_WIDTH, 600);
    const { daemon } = await openWorkspace(
      split('split-a', 'vertical', [pane('s1'), split('split-b', 'vertical', [pane('s2'), pane('s3')])]),
      ['s1', 's2', 's3'],
    );

    const resizeA = await dragDivider(daemon, 'split-a', 0.4 * PANES_WIDTH);
    const resizeB = await dragDivider(daemon, 'split-b', 0.85 * PANES_WIDTH);
    expect([resizeA.split_id, resizeB.split_id]).toEqual(['split-a', 'split-b']);

    refuseSplitRatio(daemon, resizeB, 'persist failed');
    acceptSplitRatio(daemon, resizeA);
    await daemon.idle();

    expect(splitRatio('split-a')).toBe(resizeA.ratio.toFixed(3));
    expect(splitRatio('split-b')).toBe('0.500');
  });

  it('persists the browser tile’s location again after the daemon refused to store it', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { daemon } = await openWorkspace(
      split('split-a', 'vertical', [pane('s1'), { type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://start.example' }]),
      ['s1'],
    );
    const navigate = async (url: string) => {
      act(() => {
        window.dispatchEvent(new CustomEvent('attn:browser-location', { detail: { label: 'browser-ws-tile-browser', url } }));
      });
      await daemon.idle();
    };

    await navigate('https://first.example');
    await navigate('https://second.example');
    const [first, second] = daemon.sentOf('workspace_layout_update_tile');
    expect([first.tile_params, second.tile_params]).toEqual(['https://first.example', 'https://second.example']);
    expect(first.request_id).not.toBe(second.request_id);

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_update_tile',
      workspace_id: 'ws',
      tile_id: 'tile-browser',
      request_id: second.request_id,
      success: false,
      error: 'persist failed',
    });
    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_update_tile',
      workspace_id: 'ws',
      tile_id: 'tile-browser',
      request_id: first.request_id,
      success: true,
    });
    await daemon.idle();
    await navigate('https://second.example');

    expect(daemon.sentOf('workspace_layout_update_tile').map((command) => command.tile_params)).toEqual([
      'https://first.example',
      'https://second.example',
      'https://second.example',
    ]);
  });

  it('asks again for a document tile’s content when the tile returns, instead of showing what it held before', async () => {
    const withTileRoot = split('split-a', 'vertical', [pane('s1'), { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }]);
    const withTile = splitWorkspace(withTileRoot, ['s1']);
    const withoutTile = splitWorkspace(pane('s1'), ['s1']);
    const { daemon } = await openWorkspace(withTileRoot, ['s1']);
    const tileBody = () => document.querySelector('[data-pane-id="tile-md"] .workspace-dock-tile-body');
    const deliverNotes = () => daemon.emit({
      event: 'workspace_tile_content',
      workspace_id: 'ws',
      tile_id: 'tile-md',
      tile_kind: 'markdown',
      path: '/tmp/notes.md',
      content: '# Notes',
    });
    deliverNotes();
    expect(screen.getByRole('heading', { name: 'Notes' })).toBeInTheDocument();

    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: withoutTile.layout! });
    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: withTile.layout! });
    await daemon.idle();
    expect(tileBody()).toHaveTextContent('Loading…');
    const requests = daemon.sentOf('workspace_tile_content_get').length;

    deliverNotes();
    daemon.emit({ event: 'workspace_unregistered', workspace: withTile });
    daemon.emit({ event: 'workspace_registered', workspace: withTile });
    await daemon.idle();
    expect(tileBody()).toHaveTextContent('Loading…');
    expect(daemon.sentOf('workspace_tile_content_get').length).toBeGreaterThan(requests);
  });

  describe('dragging a pane by its header', () => {
    async function renderPanes(root: unknown = SIDE_BY_SIDE, sessionIds = ['s1', 's2']) {
      layOutPanes(1000, 1000);
      return openWorkspace(root, sessionIds);
    }

    function paneHeader(sessionId: string) {
      return document.querySelector<HTMLElement>(`[data-pane-id="pane-${sessionId}"] .workspace-pane-header`)!;
    }

    async function drag(daemon: ScriptedDaemon, sessionId: string, from: [number, number], moves: Array<[number, number]>, end: 'pointerup' | 'pointercancel' = 'pointerup') {
      fireEvent.pointerDown(paneHeader(sessionId), { button: 0, pointerId: 1, clientX: from[0], clientY: from[1] });
      for (const [clientX, clientY] of moves) fireEvent.pointerMove(window, { pointerId: 1, clientX, clientY });
      const [endX, endY] = moves.slice(-1)[0] ?? from;
      if (end === 'pointercancel') fireEvent.pointerCancel(window, { pointerId: 1 });
      else fireEvent.pointerUp(window, { pointerId: 1, clientX: endX, clientY: endY });
      await daemon.idle();
    }

    const moves = (daemon: ScriptedDaemon) => daemon.sentOf('workspace_layout_move_leaf');

    it('docks the pane against the edge of the pane it is dropped on', async () => {
      const { daemon } = await renderPanes();

      await drag(daemon, 's1', [250, 10], [[400, 500], [900, 500]]);

      expect(moves(daemon)).toEqual([expect.objectContaining({ workspace_id: 'ws', leaf_id: 'pane-s1', anchor_id: 'pane-s2', edge: 'right' })]);
      expect(moves(daemon)[0].ratio).toBeCloseTo(0.2, 5);
    });

    it('still docks a fast drag whose pointer moves the browser folded into the release', async () => {
      const { daemon } = await renderPanes();

      fireEvent.pointerDown(paneHeader('s1'), { button: 0, pointerId: 1, clientX: 250, clientY: 10 });
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 900, clientY: 500 });
      await daemon.idle();

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s1', anchor_id: 'pane-s2', edge: 'right' })]);
    });

    it('docks against the whole workspace when dropped on the outer band of a side holding two panes', async () => {
      const { daemon } = await renderPanes(
        split('split-a', 'vertical', [split('split-b', 'horizontal', [pane('s1'), pane('s2')]), pane('s3')]),
        ['s1', 's2', 's3'],
      );

      await drag(daemon, 's3', [750, 10], [[400, 500], [10, 500]]);

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s3', anchor_id: '', edge: 'left', ratio: 0.5 })]);
    });

    it.each([
      ['a click on the header', [] as Array<[number, number]>, 'pointerup' as const],
      ['a jitter of a few pixels', [[752, 13]] as Array<[number, number]>, 'pointerup' as const],
      ['a drop back onto the dragged pane', [[400, 500], [700, 500]] as Array<[number, number]>, 'pointerup' as const],
      ['a cancelled drag', [[400, 500], [100, 500]] as Array<[number, number]>, 'pointercancel' as const],
    ])('moves nothing for %s', async (_, path, end) => {
      const { daemon } = await renderPanes();

      await drag(daemon, 's2', [750, 10], path, end);
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 100, clientY: 500 });
      await daemon.idle();

      expect(moves(daemon)).toEqual([]);
      expect(daemon.sentOf('workspace_layout_move_leaf_to_new_workspace')).toEqual([]);
    });
  });

  it('keeps a zoomed pane zoomed through a burst of session updates', async () => {
    const sessionIds = ['s1', 's2'];
    const { daemon } = await openWorkspace(SIDE_BY_SIDE, sessionIds);
    const zoomedPane = () => document.querySelector('[data-session-terminal-workspace="ws"]')?.getAttribute('data-zoomed-pane-id');

    pressShortcut('terminal.toggleZoom');
    await daemon.idle();
    expect(zoomedPane()).toBe('pane-s1');

    for (let update = 0; update < 60; update += 1) {
      daemon.emit({
        event: 'sessions_updated',
        sessions: sessionIds.map((id) => daemonSession(id, { workspace_id: 'ws', state: update % 2 ? 'working' : 'idle' })),
      });
    }
    await daemon.idle();

    expect(zoomedPane()).toBe('pane-s1');
  });
});
