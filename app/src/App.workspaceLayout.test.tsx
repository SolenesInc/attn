import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  agentPane,
  agentWorkspace,
  daemonSession,
  daemonWorkspace,
  type DaemonWorkspace,
} from './test/daemonFixtures';
import { pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const PANES_WIDTH = 4000;

function fakeLayout(rect: (element: HTMLElement) => DOMRect | null) {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    return rect(this) ?? new DOMRect(0, 0, 0, 0);
  });
}

function splitWorkspace(root: unknown, sessionIds: string[]): DaemonWorkspace {
  return daemonWorkspace('ws', { root, panes: sessionIds.map((id) => agentPane(id, 'ws')) }, { title: 'ws' });
}

function pane(sessionId: string) {
  return { type: 'pane', pane_id: `pane-${sessionId}` };
}

async function renderWorkspace(workspace: DaemonWorkspace, sessionIds: string[]) {
  const view = await renderApp({
    initialState: {
      sessions: sessionIds.map((id) => daemonSession(id, { workspace_id: workspace.id, state: 'idle' })),
      workspaces: [workspace],
    },
  });
  fireEvent.click(screen.getByRole('button', { name: `Open ${sessionIds[0]}` }));
  await view.daemon.idle();
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
    const { daemon } = await renderWorkspace(agentWorkspace('s1'), ['s1']);
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
    expect(close).toMatchObject({ workspace_id: 'workspace-s1', pane_id: daemon.sentOf('workspace_layout_add_session_pane')[0].pane_id });

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
      workspace_id: 'workspace-s1',
      pane_id: close.pane_id,
      success: true,
    });
    await daemon.idle();
    expect(screen.getByText('shell exited during startup')).toBeInTheDocument();
  });

  it('ranks a workspace dragged in the sidebar between its new neighbours', async () => {
    fakeLayout((element) => (
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
    const { daemon } = await renderWorkspace(
      splitWorkspace({ type: 'split', split_id: 'split-a', direction: 'vertical', ratio: 0.5, children: [pane('s1'), pane('s2')] }, ['s1', 's2']),
      ['s1', 's2'],
    );

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
    fakeLayout((element) => (
      element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, PANES_WIDTH, 600) : null
    ));
    const { daemon } = await renderWorkspace(
      splitWorkspace({ type: 'split', split_id: 'split-a', direction: 'vertical', ratio: 0.5, children: [pane('s1'), pane('s2')] }, ['s1', 's2']),
      ['s1', 's2'],
    );

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
    fakeLayout((element) => (
      element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, PANES_WIDTH, 600) : null
    ));
    const { daemon } = await renderWorkspace(
      splitWorkspace({
        type: 'split',
        split_id: 'split-a',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          pane('s1'),
          { type: 'split', split_id: 'split-b', direction: 'vertical', ratio: 0.5, children: [pane('s2'), pane('s3')] },
        ],
      }, ['s1', 's2', 's3']),
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
    const { daemon } = await renderWorkspace(
      splitWorkspace({
        type: 'split',
        split_id: 'split-a',
        direction: 'vertical',
        ratio: 0.5,
        children: [pane('s1'), { type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://start.example' }],
      }, ['s1']),
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
    const withTile = splitWorkspace({
      type: 'split',
      split_id: 'split-a',
      direction: 'vertical',
      ratio: 0.5,
      children: [pane('s1'), { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }],
    }, ['s1']);
    const withoutTile = splitWorkspace(pane('s1'), ['s1']);
    const { daemon } = await renderWorkspace(withTile, ['s1']);
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

  it('shows the tiles and preferred ratios the daemon lays out', async () => {
    await renderWorkspace(
      splitWorkspace({
        type: 'split',
        split_id: 'split-a',
        direction: 'vertical',
        ratio: 0.73,
        ratio_mode: 'preferred',
        children: [pane('s1'), { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }],
      }, ['s1']),
      ['s1'],
    );

    expect(document.querySelector('[data-pane-id="pane-s1"]')).not.toBeNull();
    expect(document.querySelector('[data-pane-id="tile-md"]')).not.toBeNull();
    expect(splitRatio('split-a')).toBe('0.730');
  });

  describe('active pane', () => {
    const activePane = () => document.querySelector('.workspace-pane.active')?.closest('[data-pane-id]')?.getAttribute('data-pane-id') ?? null;
    const choose = async (daemon: ScriptedDaemon, sessionId: string) => {
      fireEvent.mouseDown(document.querySelector(`[data-pane-id="pane-${sessionId}"] .terminal-container`)!);
      await daemon.idle();
    };
    const twoPanes = (ratio = 0.5, children = [pane('s1'), pane('s2')]) => splitWorkspace(
      { type: 'split', split_id: 'split-a', direction: 'vertical', ratio, children },
      ['s1', 's2'],
    );

    it('belongs to the workspace, whichever of its sessions is selected', async () => {
      const { daemon } = await renderWorkspace(twoPanes(), ['s1', 's2']);

      await choose(daemon, 's2');
      expect(activePane()).toBe('pane-s2');

      fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
      await daemon.idle();
      fireEvent.click(screen.getByRole('button', { name: 'Open s2' }));
      await daemon.idle();
      expect(activePane()).toBe('pane-s2');
    });

    it('survives a split resize, and follows the daemon when the panes change', async () => {
      const { daemon } = await renderWorkspace(twoPanes(), ['s1', 's2']);
      await choose(daemon, 's2');

      daemon.emit({ event: 'workspace_state_changed', workspace: twoPanes(0.3) });
      await daemon.idle();
      expect(activePane()).toBe('pane-s2');

      const reordered = twoPanes(0.3, [pane('s2'), pane('s1')]);
      daemon.emit({ event: 'workspace_state_changed', workspace: { ...reordered, layout: { ...reordered.layout!, active_pane_id: 'pane-s1' } } });
      await daemon.idle();
      expect(activePane()).toBe('pane-s1');
    });

    it.each([
      ['the pane the user used before it', ['s1', 's3', 's2'], 's3', 's1'],
      ['the left pane when the user used no other', ['s2'], 's1', 's3'],
    ])('passes to %s when the active pane closes', async (_, used, expected, daemonActive) => {
      const ids = ['s1', 's2', 's3'];
      const three = splitWorkspace({
        type: 'split',
        split_id: 'split-a',
        direction: 'vertical',
        ratio: 0.5,
        children: [pane('s1'), { type: 'split', split_id: 'split-b', direction: 'vertical', ratio: 0.5, children: [pane('s2'), pane('s3')] }],
      }, ids);
      const { daemon } = await renderWorkspace(three, ids);
      for (const id of used) await choose(daemon, id);
      const closing = used[used.length - 1];
      daemon.on('workspace_layout_close_pane', ({ workspace_id, pane_id }) => {
        const remaining = ids.filter((id) => `pane-${id}` !== pane_id);
        return [
          { event: 'workspace_layout_action_result', action: 'workspace_layout_close_pane', workspace_id, pane_id, success: true },
          {
            event: 'workspace_layout_updated',
            workspace_layout: {
              ...splitWorkspace({
                type: 'split',
                split_id: 'split-a',
                direction: 'vertical',
                ratio: 0.5,
                children: remaining.map(pane),
              }, remaining).layout!,
              active_pane_id: `pane-${daemonActive}`,
            },
          },
        ];
      });

      pressShortcut('terminal.close', document.querySelector(`[data-pane-id="pane-${closing}"] .terminal-container`)!);
      await daemon.idle();

      expect(daemon.sentOf('workspace_layout_close_pane').map(({ pane_id }) => pane_id)).toEqual([`pane-${closing}`]);
      expect(activePane()).toBe(`pane-${expected}`);
    });
  });
});
