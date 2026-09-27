import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession } from './test/daemonFixtures';
import { fakeRects, sizeTerminals } from './test/layout';
import { gesture, pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { pane, relayOut, renderDesktop, split } from './test/desktopLayouts';

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

    expect(lastResize(daemon, 's1')).toMatchObject({ cols: 62 });
    expect(lastResize(daemon, 's2')).toMatchObject({ cols: 62 });
  });

  it('fits both panes to a divider drag once it ends, and the remaining pane when the split closes', async () => {
    layOutPanes();
    const { daemon } = await renderDesktop(SIDE_BY_SIDE, ['s1', 's2']);
    await openSession(daemon, 's1');

    const divider = document.querySelector<HTMLElement>('.desktop-split-divider[data-split-id="split-a"]')!;
    fireEvent.pointerDown(divider, { button: 0, pointerId: 1, clientX: 500 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 300 });
    await settleLayout(daemon);
    const { ratio } = daemon.sentOf('desktop_set_split_ratio')[0];

    expect(lastResize(daemon, 's1')).toMatchObject({ cols: Math.floor((ratio * PANES_WIDTH) / CELL_WIDTH) });
    expect(lastResize(daemon, 's2')).toMatchObject({ cols: Math.floor(((1 - ratio) * PANES_WIDTH) / CELL_WIDTH) });

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

    fireEvent.pointerDown(document.querySelector<HTMLElement>('.desktop-split-divider[data-split-id="split-a"]')!, { button: 0, pointerId: 1, clientX: 500, clientY: 250 });
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

    expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
    expect(daemon.sentOf('desktop_set_split_ratio')).toEqual([]);
  });

  it('fits the agent pane once, to its final width, when docking a document suspends an older one', async () => {
    layOutPanes();
    const { daemon } = await renderDesktop(pane('s1'), ['s1']);
    await openSession(daemon, 's1');
    const dock = async (...documentIds: string[]) => {
      const root = documentIds.reduce<unknown>(
        (layout, id) => split(`split-${id}`, 'vertical', [layout, { type: 'tile', tile_id: id, tile_kind: 'markdown', tile_params: `/tmp/${id}.md` }], 0.68),
        pane('s1'),
      );
      relayOut(daemon, root, ['s1'], { active: documentIds[documentIds.length - 1] });
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

  describe('attention ring', () => {
    const doc = (id: string, name = id) => ({ type: 'tile', tile_id: id, tile_kind: 'markdown', tile_params: `/tmp/${name}.md` });
    const beside = (id: string, left: unknown, right: unknown, ratio = 0.5) => split(id, 'vertical', [left, right], ratio);
    const crowded = () => beside('outer', beside('document-split', pane('s1'), doc('document', 'review'), 0.68), pane('s2'));
    const surfaceOf = () => document.querySelector<HTMLElement>('.session-terminal-desktop[data-desktop-id="ws"]')!;
    const slivers = () => screen.queryAllByRole('button', { name: /^Expand / }).map((button) => button.getAttribute('aria-label'));

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

    it('folds the agent into a sliver to make room for a document docked beside it on a narrow desktop', async () => {
      const { layOut } = await ring(560, pane('s1'), ['s1']);

      await layOut(beside('opened', pane('s1'), doc('document', 'review'), 0.68), 'document');

      expect(slivers()).toEqual(['Expand s1']);
      expect(surfaceOf()).toHaveAttribute('data-active-leaf-id', 'document');
    });

    it('folds the oldest documents first as more are docked', async () => {
      const { daemon, layOut } = await ring(1816, pane('s1'), ['s1']);
      const opened = ['oldest', 'older', 'newer', 'newest'];
      for (const [index, id] of opened.entries()) {
        await layOut(opened.slice(0, index + 1).reduce<unknown>((left, next) => beside(`split-${next}`, left, doc(next), 0.68), pane('s1')), id);
        if (id !== 'newest') {
          fireEvent.mouseDown(paneEl('s1'));
          await settleLayout(daemon);
        }
      }

      expect(slivers()).toEqual(['Expand oldest.md', 'Expand older.md']);
    });

    it('expands a clicked sliver and folds the leaf focused longest ago, not the one just left', async () => {
      const { daemon, show } = await ring(1100, crowded(), ['s1', 's2']);
      await show('document', 'review');
      expect(slivers()).toEqual(['Expand s2']);
      fireEvent.mouseDown(document.querySelector<HTMLElement>('[data-pane-id="document"]')!);
      await settleLayout(daemon);
      fireEvent.mouseDown(paneEl('s1'));
      await settleLayout(daemon);

      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Expand s2' })));
      await settleLayout(daemon);

      expect(slivers()).toEqual(['Expand review.md']);
      expect(surfaceOf()).toHaveAttribute('data-active-leaf-id', 'pane-s2');
    });

    it('folds a pane dragged below its minimum width and unfolds it when dragged back', async () => {
      const { daemon, show } = await ring(1100, crowded(), ['s1', 's2']);
      await show('document', 'review');
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Expand s2' })));
      await settleLayout(daemon);
      const divider = document.querySelector<HTMLElement>('.desktop-split-divider[data-split-grab][data-split-id="outer"]')!;

      fireEvent.pointerDown(divider, { button: 0, pointerId: 1, clientX: 500, clientY: 350 });
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 300, clientY: 350 });
      await act(() => vi.advanceTimersToNextFrame());
      expect(slivers()).toEqual(['Expand s1', 'Expand review.md']);

      fireEvent.pointerMove(window, { pointerId: 1, clientX: 520, clientY: 350 });
      await act(() => vi.advanceTimersToNextFrame());
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 520, clientY: 350 });
      await settleLayout(daemon);

      expect(slivers()).toEqual(['Expand review.md']);
    });

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
