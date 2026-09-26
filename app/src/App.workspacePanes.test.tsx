import { act, fireEvent, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession, splitWorkspace } from './test/daemonFixtures';
import { fakeRects, sizeTerminals } from './test/layout';
import { pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { laidOutWorkspace, pane, renderWorkspace, split } from './test/workspaces';

const PANES_WIDTH = 1000;
const PANES_HEIGHT = 600;
const CELL_WIDTH = 8;

function layOutPanes() {
  sizeTerminals((terminal) => ({
    clientWidth: (PANES_WIDTH * parseFloat(terminal.closest<HTMLElement>('[data-pane-id]')?.style.width || '100')) / 100,
    clientHeight: PANES_HEIGHT,
  }));
  fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, PANES_WIDTH, PANES_HEIGHT) : null));
}

const SIDE_BY_SIDE = split('split-a', 'vertical', [pane('s1'), pane('s2')]);

async function settleLayout(daemon: ScriptedDaemon) {
  await act(() => vi.advanceTimersByTimeAsync(300));
  await daemon.idle();
}

function paneEl(sessionId: string) {
  return document.querySelector<HTMLElement>(`[data-pane-id="pane-${sessionId}"]`)!;
}

function terminalInput(sessionId: string) {
  return within(paneEl(sessionId)).getByRole('textbox', { name: 'Terminal input' });
}

const surface = () => document.querySelector<HTMLElement>('[data-session-terminal-workspace="ws"]')!;

function lastResize(daemon: ScriptedDaemon, sessionId: string) {
  return daemon.sentOf('pty_resize').filter(({ id }) => id === sessionId).slice(-1)[0];
}

describe('App workspace panes', () => {
  it('fits every pane of a split session to its share of the workspace when it becomes active', async () => {
    layOutPanes();
    const { daemon } = await renderWorkspace(SIDE_BY_SIDE, ['s1', 's2']);
    expect(daemon.sentOf('pty_resize')).toEqual([]);

    await openSession(daemon, 's1');

    expect(lastResize(daemon, 's1')).toMatchObject({ cols: 62 });
    expect(lastResize(daemon, 's2')).toMatchObject({ cols: 62 });
  });

  it('fits both panes to a divider drag once it ends, and the remaining pane when the split closes', async () => {
    layOutPanes();
    const { daemon } = await renderWorkspace(SIDE_BY_SIDE, ['s1', 's2']);
    await openSession(daemon, 's1');

    const divider = document.querySelector<HTMLElement>('.workspace-split-divider[data-split-id="split-a"]')!;
    fireEvent.pointerDown(divider, { button: 0, pointerId: 1, clientX: 500 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 300 });
    await settleLayout(daemon);
    const { ratio } = daemon.sentOf('workspace_layout_set_split_ratio')[0];

    expect(lastResize(daemon, 's1')).toMatchObject({ cols: Math.floor((ratio * PANES_WIDTH) / CELL_WIDTH) });
    expect(lastResize(daemon, 's2')).toMatchObject({ cols: Math.floor(((1 - ratio) * PANES_WIDTH) / CELL_WIDTH) });

    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: splitWorkspace('ws', ['s1']).layout! });
    await settleLayout(daemon);

    expect(lastResize(daemon, 's1')).toMatchObject({ cols: PANES_WIDTH / CELL_WIDTH });
  });

  it('keeps a visible terminal attached through unrelated session updates', async () => {
    const { daemon } = await renderWorkspace(pane('s1'), ['s1'], ['s2']);
    await openSession(daemon, 's1');

    for (const state of ['working', 'idle', 'waiting_input'] as const) {
      daemon.emit({ event: 'session_state_changed', session: daemonSession('s2', { state }) });
      daemon.emit({ event: 'session_state_changed', session: daemonSession('s1', { workspace_id: 'ws', state }) });
    }
    await daemon.idle();

    expect(daemon.sent.filter(({ cmd }) => cmd === 'attach_session' || cmd === 'detach_session')).toEqual([
      { cmd: 'attach_session', id: 's1', attach_policy: 'same_app_remount' },
    ]);
  });

  it('gives a pane’s terminal the keyboard as soon as the pointer presses it', async () => {
    const { daemon } = await renderWorkspace(SIDE_BY_SIDE, ['s1', 's2']);
    await openSession(daemon, 's1');

    fireEvent.mouseDown(paneEl('s2'));

    expect(terminalInput('s2')).toHaveFocus();
  });

  it('moves between panes of nested splits, and on to the next session past the edge', async () => {
    const { daemon } = await renderWorkspace(
      split('split-a', 'vertical', [pane('s1'), split('split-b', 'horizontal', [pane('s2'), pane('s3')])]),
      ['s1', 's2', 's3'],
      ['s4'],
    );
    await openSession(daemon, 's2');

    pressShortcut('terminal.focusDown');
    await daemon.idle();
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s3');

    pressShortcut('terminal.focusRight');
    await daemon.idle();
    expect(daemon.sentOf('workspace_selected').slice(-1)[0]).toEqual({ cmd: 'workspace_selected', workspace_id: 'workspace-s4' });
  });

  it('arms zoom on a lone pane and applies it once a split exists, then follows the active pane', async () => {
    const { daemon } = await renderWorkspace(pane('s1'), ['s1', 's2', 's3']);
    await openSession(daemon, 's1');

    pressShortcut('terminal.toggleZoom');
    await daemon.idle();

    daemon.emit({
      event: 'workspace_layout_updated',
      workspace_layout: laidOutWorkspace(
        split('split-a', 'vertical', [pane('s1'), split('split-b', 'horizontal', [pane('s2'), pane('s3')])]),
        ['s1', 's2', 's3'],
      ).layout!,
    });
    await daemon.idle();
    expect(surface()).toHaveAttribute('data-zoomed-pane-id', 'pane-s1');

    fireEvent.mouseDown(paneEl('s3'));
    await daemon.idle();
    expect(surface()).toHaveAttribute('data-zoomed-pane-id', 'pane-s3');
  });

  it('locks text selection only while a divider or tile is being dragged', async () => {
    const tile = { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' };
    layOutPanes();
    const { daemon } = await renderWorkspace(split('split-a', 'vertical', [pane('s1'), tile]), ['s1']);
    await openSession(daemon, 's1');
    const selection = () => document.body.style.userSelect;
    const tileHeader = () => document.querySelector<HTMLElement>('[data-pane-id="tile-md"] .workspace-dock-tile-header')!;

    fireEvent.pointerDown(document.querySelector<HTMLElement>('.workspace-split-divider[data-split-id="split-a"]')!, { button: 0, pointerId: 1, clientX: 500, clientY: 250 });
    expect(selection()).toBe('none');
    fireEvent.pointerCancel(window, { pointerId: 1 });
    expect(selection()).toBe('');

    fireEvent.pointerDown(tileHeader(), { button: 0, pointerId: 1, clientX: 750, clientY: 8 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 750, clientY: 8 });
    expect(selection()).toBe('');

    fireEvent.pointerDown(tileHeader(), { button: 0, pointerId: 1, clientX: 750, clientY: 8 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 760, clientY: 280 });
    expect(selection()).toBe('none');
    fireEvent.blur(window);
    expect(selection()).toBe('');
    await daemon.idle();

    expect(daemon.sentOf('workspace_layout_move_leaf')).toEqual([]);
    expect(daemon.sentOf('workspace_layout_set_split_ratio')).toEqual([]);
  });

  it('fits the agent pane once, to its final width, when docking a document suspends an older one', async () => {
    layOutPanes();
    const { daemon } = await renderWorkspace(pane('s1'), ['s1']);
    await openSession(daemon, 's1');
    const dock = async (...documentIds: string[]) => {
      const root = documentIds.reduce<unknown>(
        (layout, id) => split(`split-${id}`, 'vertical', [layout, { type: 'tile', tile_id: id, tile_kind: 'markdown', tile_params: `/tmp/${id}.md` }], 0.68),
        pane('s1'),
      );
      daemon.emit({ event: 'workspace_layout_updated', workspace_layout: laidOutWorkspace(root, ['s1']).layout! });
      await settleLayout(daemon);
    };
    await dock('oldest');
    fireEvent.mouseDown(paneEl('s1'));
    await settleLayout(daemon);
    const before = daemon.sentOf('pty_resize').length;

    await dock('oldest', 'newest');

    expect(document.querySelector('[data-pane-id="oldest"]')).toHaveAttribute('data-pane-suspended', 'true');
    const finalCols = Math.floor((parseFloat(paneEl('s1').style.width) * PANES_WIDTH) / 100 / CELL_WIDTH);
    expect(daemon.sentOf('pty_resize').slice(before)).toEqual([expect.objectContaining({ id: 's1', cols: finalCols })]);
  });
});
