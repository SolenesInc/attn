import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession } from './test/daemonFixtures';
import { fakeRects, sizeTerminals } from './test/layout';
import { gesture, pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { pane, relayOut, renderDesktop, split } from './test/desktopLayouts';

const PANES_WIDTH = 2000;
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

const columnDivider = (sessionId: string) => document.querySelector<HTMLElement>(`.desktop-split-divider[data-split-id="row:pane-${sessionId}"]`)!;

const surface = () => document.querySelector<HTMLElement>('[data-session-terminal-desktop="ws"]')!;

function lastResize(daemon: ScriptedDaemon, sessionId: string) {
  return daemon.sentOf('pty_resize').filter(({ id }) => id === sessionId).slice(-1)[0];
}

describe('App desktop panes', () => {
  it('fits every pane of a split session to its share of the desktop when it becomes active', async () => {
    layOutPanes();
    const { daemon } = await renderDesktop(SIDE_BY_SIDE, ['s1', 's2']);
    expect(daemon.sentOf('pty_resize')).toEqual([]);

    await openSession(daemon, 's1');

    expect(lastResize(daemon, 's1')).toMatchObject({ cols: 125 });
    expect(lastResize(daemon, 's2')).toMatchObject({ cols: 125 });
  });

  it('fits both tiles to a column divider drag once it ends, and the remaining tile when the other closes', async () => {
    layOutPanes();
    const { daemon } = await renderDesktop(SIDE_BY_SIDE, ['s1', 's2']);
    await openSession(daemon, 's1');

    fireEvent.pointerDown(columnDivider('s1'), { button: 0, pointerId: 1, clientX: 1000 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 800 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 800 });
    await settleLayout(daemon);
    const widthOf = (sessionId: string) => (parseFloat(paneEl(sessionId).style.width) * PANES_WIDTH) / 100;
    expect(widthOf('s1')).toBeLessThan(1000);

    expect(lastResize(daemon, 's1')).toMatchObject({ cols: Math.floor(widthOf('s1') / CELL_WIDTH) });
    expect(lastResize(daemon, 's2')).toMatchObject({ cols: Math.floor(widthOf('s2') / CELL_WIDTH) });

    relayOut(daemon, pane('s1'), ['s1']);
    await settleLayout(daemon);

    expect(lastResize(daemon, 's1')).toMatchObject({ cols: PANES_WIDTH / CELL_WIDTH });
  });

  it('keeps a visible terminal attached through unrelated session updates', async () => {
    const { daemon } = await renderDesktop(pane('s1'), ['s1'], ['s2']);
    await openSession(daemon, 's1');

    for (const state of ['working', 'idle', 'waiting_input'] as const) {
      daemon.emit({ event: 'session_state_changed', session: daemonSession('s2', { state }) });
      daemon.emit({ event: 'session_state_changed', session: daemonSession('s1', { state }) });
    }
    await daemon.idle();

    expect(daemon.sent.filter(({ cmd }) => cmd === 'attach_session' || cmd === 'detach_session')).toEqual([
      { cmd: 'attach_session', id: 's1', attach_policy: 'same_app_remount' },
    ]);
  });

  it('gives a pane’s terminal the keyboard and the sidebar selection as soon as the pointer presses it', async () => {
    const { daemon } = await renderDesktop(SIDE_BY_SIDE, ['s1', 's2']);
    await openSession(daemon, 's1');

    fireEvent.mouseDown(paneEl('s2'));
    await daemon.idle();

    expect(terminalInput('s2')).toHaveFocus();
    expect(document.querySelector('[data-testid="sidebar-session-s2"]')).toHaveClass('selected');
    expect(document.querySelector('[data-testid="sidebar-session-s1"]')).not.toHaveClass('selected');

    pressShortcut('terminal.focusLeft');
    await daemon.idle();

    expect(terminalInput('s1')).toHaveFocus();
    expect(document.querySelector('[data-testid="sidebar-session-s1"]')).toHaveClass('selected');
  });

  it('moves between panes of nested splits, and on to the next session past the edge', async () => {
    const { daemon } = await renderDesktop(
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
    expect(daemon.sentOf('desktop_set_current').slice(-1)[0]).toMatchObject({ desktop_id: 'desktop-s4' });
  });

  it('opens a desktop at its active pane when the user jumps to it by number', async () => {
    const { daemon } = await renderDesktop(SIDE_BY_SIDE, ['s1', 's2'], ['s3']);
    daemon.arrange((desktops) => desktops.map((desktop) => ({ ...desktop, shortcut_slot: desktop.id === 'ws' ? 1 : desktop.id === 'desktop-s3' ? 2 : undefined })));
    await openSession(daemon, 's2');
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s2');

    fireEvent.keyDown(window, { key: '2', code: 'Digit2', metaKey: true });
    await daemon.idle();
    fireEvent.keyDown(window, { key: '1', code: 'Digit1', metaKey: true });
    await daemon.idle();

    expect(daemon.sentOf('desktop_set_current').slice(-2)).toEqual([
      expect.objectContaining({ desktop_id: 'desktop-s3' }),
      expect.objectContaining({ desktop_id: 'ws' }),
    ]);
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s2');
  });

  it('arms zoom on a lone pane and applies it once a split exists, then follows the active pane', async () => {
    const { daemon } = await renderDesktop(pane('s1'), ['s1', 's2', 's3']);
    await openSession(daemon, 's1');

    pressShortcut('terminal.toggleZoom');
    await daemon.idle();

    relayOut(daemon, split('split-a', 'vertical', [pane('s1'), split('split-b', 'horizontal', [pane('s2'), pane('s3')])]), ['s1', 's2', 's3']);
    await daemon.idle();
    expect(surface()).toHaveAttribute('data-zoomed-pane-id', 'pane-s1');

    fireEvent.mouseDown(paneEl('s3'));
    await daemon.idle();
    expect(surface()).toHaveAttribute('data-zoomed-pane-id', 'pane-s3');
  });

  it('locks text selection only while a divider or tile is being dragged', async () => {
    const tile = { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' };
    layOutPanes();
    const { daemon } = await renderDesktop(split('split-a', 'vertical', [pane('s1'), tile]), ['s1']);
    await openSession(daemon, 's1');
    const selection = () => document.body.style.userSelect;
    const tileHeader = () => document.querySelector<HTMLElement>('[data-pane-id="tile-md"] .desktop-dock-tile-header')!;

    fireEvent.pointerDown(columnDivider('s1'), { button: 0, pointerId: 1, clientX: 1000, clientY: 250 });
    expect(selection()).toBe('');
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 900, clientY: 250 });
    expect(selection()).toBe('none');
    fireEvent.pointerCancel(window, { pointerId: 1 });
    expect(selection()).toBe('');

    fireEvent.pointerDown(tileHeader(), { button: 0, pointerId: 1, clientX: 1500, clientY: 8 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 1500, clientY: 8 });
    expect(selection()).toBe('');

    fireEvent.pointerDown(tileHeader(), { button: 0, pointerId: 1, clientX: 1500, clientY: 8 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 1510, clientY: 280 });
    expect(selection()).toBe('none');
    fireEvent.blur(window);
    expect(selection()).toBe('');
    await daemon.idle();

    expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
    expect(daemon.sentOf('desktop_set_wanted_cols')).toEqual([]);
  });

  it('fits the agent tile once, to its final width, when a docked document narrows it', async () => {
    layOutPanes();
    const { daemon } = await renderDesktop(pane('s1'), ['s1']);
    await openSession(daemon, 's1');
    const dock = async (...documentIds: string[]) => {
      const root = documentIds.reduce<unknown>(
        (layout, id) => split(`split-${id}`, 'vertical', [layout, { type: 'tile', tile_id: id, tile_kind: 'markdown', tile_params: `/tmp/${id}.md` }]),
        pane('s1'),
      );
      relayOut(daemon, root, ['s1'], { active: documentIds[documentIds.length - 1] });
      await settleLayout(daemon);
    };
    await dock('oldest');
    fireEvent.mouseDown(paneEl('s1'));
    await settleLayout(daemon);
    const widthBefore = parseFloat(paneEl('s1').style.width);
    const before = daemon.sentOf('pty_resize').length;

    await dock('oldest', 'newest');

    const width = parseFloat(paneEl('s1').style.width);
    expect(width).toBeLessThan(widthBefore);
    const finalCols = Math.floor((width * PANES_WIDTH) / 100 / CELL_WIDTH);
    expect(daemon.sentOf('pty_resize').slice(before)).toEqual([expect.objectContaining({ id: 's1', cols: finalCols })]);
  });

  describe('document focus', () => {
    const doc = (id: string, name = id) => ({ type: 'tile', tile_id: id, tile_kind: 'markdown', tile_params: `/tmp/${name}.md` });
    const beside = (id: string, left: unknown, right: unknown, ratio = 0.5) => split(id, 'vertical', [left, right], ratio);
    const surfaceOf = () => document.querySelector<HTMLElement>('.session-terminal-desktop[data-desktop-id="ws"]')!;

    async function ring(width: number, root: unknown, sessionIds: string[]) {
      fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, width, 700) : null));
      const view = await renderDesktop(root, sessionIds);
      await openSession(view.daemon, 's1');
      const layOut = async (next: unknown, docked?: string) => {
        relayOut(view.daemon, next, sessionIds, { active: docked });
        await settleLayout(view.daemon);
      };
      const show = async (tileId: string, name: string) => {
        view.daemon.emit({ event: 'desktop_tile_content', desktop_id: 'ws', tile_id: tileId, tile_kind: 'markdown', path: `/tmp/${name}.md`, content: `# ${name}` });
        await view.daemon.idle();
      };
      return { ...view, layOut, show };
    }

    it('focuses documents as a tabbed review deck that Escape unwinds one layer at a time', async () => {
      const root = beside('outer', beside('document-split', pane('s1'), doc('document', 'review'), 0.68), beside('second-split', pane('s2'), doc('second-document', 'second')));
      const { daemon, show } = await ring(2000, root, ['s1', 's2']);
      await show('document', 'review');
      await show('second-document', 'second');
      const tab = (name: string) => screen.getByRole('tab', { name });
      const escape = () => gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));

      await gesture(daemon, () => fireEvent.click(within(document.querySelector<HTMLElement>('[data-pane-id="document"]')!).getByRole('button', { name: 'Focus document' })));
      expect(surfaceOf()).toHaveClass('focus-mode');
      expect(tab('review.md')).toHaveAttribute('aria-selected', 'true');

      await gesture(daemon, () => fireEvent.click(tab('second.md')));
      expect(tab('second.md')).toHaveAttribute('aria-selected', 'true');
      await gesture(daemon, () => fireEvent.click(within(document.querySelector<HTMLElement>('[data-pane-id="second-document"]')!).getByRole('button', { name: 'Notes 0' })));
      expect(screen.getByRole('dialog', { name: 'Review notes' })).toBeInTheDocument();

      await escape();
      expect(screen.queryByRole('dialog', { name: 'Review notes' })).toBeNull();
      expect(surfaceOf()).toHaveClass('focus-mode');

      await escape();
      expect(surfaceOf()).not.toHaveClass('focus-mode');
    });
  });
});
