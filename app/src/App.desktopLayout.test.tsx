import { act, fireEvent, screen } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import { openActionMenu, openSession } from './test/appFixtures';
import { emptyDesktop, soloDesktop, daemonSession } from './test/daemonFixtures';
import { fakeRects } from './test/layout';
import { pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { pane, relayOut, renderDesktop, split } from './test/desktopLayouts';

const PANES_WIDTH = 4000;
const SIDE_BY_SIDE = split('split-a', 'vertical', [pane('s1'), pane('s2')]);

function layOutPanes(width: number, height: number) {
  fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, width, height) : null));
}

async function openDesktop(root: unknown, sessionIds: string[]) {
  const view = await renderDesktop(root, sessionIds);
  await openSession(view.daemon, sessionIds[0]);
  return view;
}

function splitRatio(splitId: string) {
  return document.querySelector(`.desktop-split[data-split-id="${splitId}"]`)?.getAttribute('data-split-ratio');
}

async function dragDivider(daemon: ScriptedDaemon, splitId: string, clientX: number) {
  const divider = document.querySelector<HTMLElement>(`.desktop-split-divider[data-split-id="${splitId}"]`)!;
  const dividerX = (parseFloat(divider.style.left) / 100) * PANES_WIDTH;
  fireEvent.pointerDown(divider, { button: 0, pointerId: 1, clientX: dividerX });
  fireEvent.pointerUp(window, { pointerId: 1, clientX });
  await daemon.idle();
  return daemon.sentOf('desktop_set_split_ratio').pop()!;
}

function answer(daemon: ScriptedDaemon, request: { cmd: string; request_id?: string }, error?: string) {
  daemon.emit({
    event: 'profile_action_result',
    action: request.cmd,
    request_id: request.request_id ?? '',
    success: !error,
    ...(error ? { error } : {}),
  });
}

function holdAnswers(daemon: ScriptedDaemon, ...commands: Array<'desktop_set_split_ratio' | 'desktop_update_tile'>) {
  for (const command of commands) daemon.on(command, () => undefined);
}

describe('App desktop layout', () => {
  it('reports a split whose shell failed to spawn', async () => {
    const { daemon } = await openDesktop(pane('s1'), ['s1']);
    daemon.on('spawn_session', ({ id }) => ({ event: 'spawn_result', id, success: false, error: 'shell exited during startup' }));

    pressShortcut('terminal.splitVertical');
    await daemon.idle();

    expect(daemon.sentOf('spawn_session')).toEqual([expect.objectContaining({ agent: 'shell', placement: expect.objectContaining({ desktop_id: 'ws', anchor_pane_id: 'pane-s1' }) })]);
    expect(screen.getByText(/shell exited during startup/)).toBeInTheDocument();
  });

  it('reorders a desktop dragged in the sidebar between its new neighbours', async () => {
    fakeRects((element) => (
      element.classList.contains('desktop-reorder-seam')
        ? new DOMRect(0, Number(element.dataset.seamIndex) * 100, 200, 0)
        : null
    ));
    const ids = ['a', 'b', 'c'];
    const { daemon } = await renderApp({
      initialState: {
        sessions: ids.map((id) => daemonSession(id)),
        desktops: ids.map((id) => soloDesktop(id, { name: `desk ${id}` })),
      },
    });
    daemon.on('desktop_reorder', (command) => ({ event: 'profile_action_result', action: command.cmd, request_id: command.request_id, success: true }));
    const drag = async (label: string, fromY: number, toY: number) => {
      fireEvent.pointerDown(screen.getByRole('button', { name: `Open desk ${label}` }), { button: 0, pointerId: 1, clientX: 10, clientY: fromY });
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: (fromY + toY) / 2 });
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: toY });
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 10, clientY: toY });
      await daemon.idle();
    };

    await drag('c', 250, 100);
    await drag('c', 250, 0);

    expect(daemon.sentOf('desktop_reorder').map(({ desktop_id, previous_desktop_id, next_desktop_id }) => ({ desktop_id, previous_desktop_id, next_desktop_id }))).toEqual([
      { desktop_id: 'desktop-c', previous_desktop_id: 'desktop-a', next_desktop_id: 'desktop-b' },
      { desktop_id: 'desktop-c', previous_desktop_id: undefined, next_desktop_id: 'desktop-a' },
    ]);
  });

  it('moves a pane dropped on the sidebar onto a new desktop', async () => {
    const { daemon } = await openDesktop(SIDE_BY_SIDE, ['s1', 's2']);
    daemon.on('desktop_create', (command) => {
      const created = emptyDesktop('desktop-new', { order_key: 'z' });
      daemon.arrangement.desktops = [...daemon.arrangement.desktops, created];
      return [
        { event: 'profile_action_result', action: command.cmd, request_id: command.request_id, success: true, desktops: [created] },
        daemon.arrangement.changed(),
      ];
    });
    daemon.on('desktop_move_leaf', (command) => ({ event: 'profile_action_result', action: command.cmd, request_id: command.request_id, success: true }));

    fireEvent.pointerDown(document.querySelector('[data-pane-id="pane-s2"] .desktop-pane-header')!, { button: 0, pointerId: 1, clientX: 100, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 150, clientY: 60 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 200, clientY: 100 });
    const dropzone = screen.getByTestId('new-desktop-dropzone');
    fireEvent.pointerEnter(dropzone);
    fireEvent.pointerUp(dropzone, { pointerId: 1 });
    await daemon.idle();

    expect(daemon.sentOf('desktop_move_leaf')).toEqual([expect.objectContaining({
      source_desktop_id: 'ws',
      target_desktop_id: 'desktop-new',
      leaf_id: 'pane-s2',
    })]);
  });

  it('keeps a later split resize the daemon accepted when an earlier one on the same split is refused', async () => {
    layOutPanes(PANES_WIDTH, 600);
    const { daemon } = await openDesktop(SIDE_BY_SIDE, ['s1', 's2']);
    holdAnswers(daemon, 'desktop_set_split_ratio');

    const first = await dragDivider(daemon, 'split-a', 0.3 * PANES_WIDTH);
    const second = await dragDivider(daemon, 'split-a', 0.7 * PANES_WIDTH);
    expect([first.ratio, second.ratio]).toEqual([0.3, 0.7]);
    expect(first.request_id).not.toBe(second.request_id);

    answer(daemon, second);
    await daemon.idle();
    expect(splitRatio('split-a')).toBe('0.700');

    answer(daemon, first, 'first persist failed');
    await daemon.idle();
    expect(splitRatio('split-a')).toBe('0.700');
  });

  it('undoes only the split whose resize the daemon refused', async () => {
    layOutPanes(PANES_WIDTH, 600);
    const { daemon } = await openDesktop(
      split('split-a', 'vertical', [pane('s1'), split('split-b', 'vertical', [pane('s2'), pane('s3')])]),
      ['s1', 's2', 's3'],
    );
    holdAnswers(daemon, 'desktop_set_split_ratio');

    const resizeA = await dragDivider(daemon, 'split-a', 0.4 * PANES_WIDTH);
    const resizeB = await dragDivider(daemon, 'split-b', 0.85 * PANES_WIDTH);
    expect([resizeA.split_id, resizeB.split_id]).toEqual(['split-a', 'split-b']);

    answer(daemon, resizeB, 'persist failed');
    answer(daemon, resizeA);
    await daemon.idle();

    expect(splitRatio('split-a')).toBe(resizeA.ratio.toFixed(3));
    expect(splitRatio('split-b')).toBe('0.500');
  });

  it('persists the browser tile’s location again after the daemon refused to store it', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { daemon } = await openDesktop(
      split('split-a', 'vertical', [pane('s1'), { type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://start.example' }]),
      ['s1'],
    );
    holdAnswers(daemon, 'desktop_update_tile');
    const navigate = async (url: string) => {
      act(() => {
        window.dispatchEvent(new CustomEvent('attn:browser-location', { detail: { label: 'browser-ws-tile-browser', url } }));
      });
      await daemon.idle();
    };

    await navigate('https://first.example');
    await navigate('https://second.example');
    const [first, second] = daemon.sentOf('desktop_update_tile');
    expect([first.tile_params, second.tile_params]).toEqual(['https://first.example', 'https://second.example']);
    expect(first.request_id).not.toBe(second.request_id);

    answer(daemon, second, 'persist failed');
    answer(daemon, first);
    await daemon.idle();
    await navigate('https://second.example');

    expect(daemon.sentOf('desktop_update_tile').map((command) => command.tile_params)).toEqual([
      'https://first.example',
      'https://second.example',
      'https://second.example',
    ]);
  });

  it('waits for a document tile’s content again when the tile returns, instead of showing what it held before', async () => {
    const withTileRoot = split('split-a', 'vertical', [pane('s1'), { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }]);
    const { daemon } = await openDesktop(withTileRoot, ['s1']);
    const tileBody = () => document.querySelector('[data-pane-id="tile-md"] .desktop-dock-tile-body');
    const deliverNotes = () => daemon.emit({
      event: 'desktop_tile_content',
      desktop_id: 'ws',
      tile_id: 'tile-md',
      tile_kind: 'markdown',
      path: '/tmp/notes.md',
      content: '# Notes',
    });
    deliverNotes();
    expect(screen.getByRole('heading', { name: 'Notes' })).toBeInTheDocument();

    relayOut(daemon, pane('s1'), ['s1']);
    await daemon.idle();
    relayOut(daemon, withTileRoot, ['s1']);
    await daemon.idle();
    expect(tileBody()).toHaveTextContent('Loading…');

    deliverNotes();
    expect(screen.getByRole('heading', { name: 'Notes' })).toBeInTheDocument();
  });

  describe('dragging a pane by its header', () => {
    async function renderPanes(root: unknown = SIDE_BY_SIDE, sessionIds = ['s1', 's2']) {
      layOutPanes(1000, 1000);
      return openDesktop(root, sessionIds);
    }

    function paneHeader(sessionId: string) {
      return document.querySelector<HTMLElement>(`[data-pane-id="pane-${sessionId}"] .desktop-pane-header`)!;
    }

    async function drag(daemon: ScriptedDaemon, sessionId: string, from: [number, number], moves: Array<[number, number]>, end: 'pointerup' | 'pointercancel' = 'pointerup') {
      fireEvent.pointerDown(paneHeader(sessionId), { button: 0, pointerId: 1, clientX: from[0], clientY: from[1] });
      for (const [clientX, clientY] of moves) fireEvent.pointerMove(window, { pointerId: 1, clientX, clientY });
      const [endX, endY] = moves.slice(-1)[0] ?? from;
      if (end === 'pointercancel') fireEvent.pointerCancel(window, { pointerId: 1 });
      else fireEvent.pointerUp(window, { pointerId: 1, clientX: endX, clientY: endY });
      await daemon.idle();
    }

    const moves = (daemon: ScriptedDaemon) => daemon.sentOf('desktop_move_leaf');

    it('docks the pane against the edge of the pane it is dropped on', async () => {
      const { daemon } = await renderPanes();

      await drag(daemon, 's1', [250, 10], [[400, 500], [900, 500]]);

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s1', anchor_id: 'pane-s2', edge: 'right' })]);
      expect(moves(daemon)[0].leaf_share).toBeCloseTo(0.2, 5);
    });

    it('still docks a fast drag whose pointer moves the browser folded into the release', async () => {
      const { daemon } = await renderPanes();

      fireEvent.pointerDown(paneHeader('s1'), { button: 0, pointerId: 1, clientX: 250, clientY: 10 });
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 900, clientY: 500 });
      await daemon.idle();

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s1', anchor_id: 'pane-s2', edge: 'right' })]);
    });

    it('docks against the whole desktop when dropped on the outer band of a side holding two panes', async () => {
      const { daemon } = await renderPanes(
        split('split-a', 'vertical', [split('split-b', 'horizontal', [pane('s1'), pane('s2')]), pane('s3')]),
        ['s1', 's2', 's3'],
      );

      await drag(daemon, 's3', [750, 10], [[400, 500], [10, 500]]);

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s3', edge: 'left', leaf_share: 0.5 })]);
      expect(moves(daemon)[0]).not.toHaveProperty('anchor_id');
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
      expect(daemon.sentOf('desktop_create')).toEqual([]);
    });
  });

  it('shows the tiles and preferred ratios the daemon lays out', async () => {
    await openDesktop(
      { ...split('split-a', 'vertical', [pane('s1'), { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }], 0.73), ratio_mode: 'preferred' },
      ['s1'],
    );

    expect(document.querySelector('[data-pane-id="pane-s1"]')).not.toBeNull();
    expect(document.querySelector('[data-pane-id="tile-md"]')).not.toBeNull();
    expect(splitRatio('split-a')).toBe('0.730');
  });

  describe('active pane', () => {
    const activePane = () => document.querySelector('.desktop-pane.active')?.closest('[data-pane-id]')?.getAttribute('data-pane-id') ?? null;
    const choose = async (daemon: ScriptedDaemon, sessionId: string) => {
      fireEvent.mouseDown(document.querySelector(`[data-pane-id="pane-${sessionId}"] .terminal-container`)!);
      await daemon.idle();
    };

    it('belongs to the desktop, whichever of its sessions is selected', async () => {
      const { daemon } = await openDesktop(SIDE_BY_SIDE, ['s1', 's2']);

      await choose(daemon, 's2');
      expect(activePane()).toBe('pane-s2');

      await openSession(daemon, 's1');
      await openSession(daemon, 's2');
      expect(activePane()).toBe('pane-s2');
    });

    it('survives a split resize, and follows the daemon when the panes change', async () => {
      const { daemon } = await openDesktop(SIDE_BY_SIDE, ['s1', 's2']);
      await choose(daemon, 's2');

      relayOut(daemon, split('split-a', 'vertical', [pane('s1'), pane('s2')], 0.3), ['s1', 's2']);
      await daemon.idle();
      expect(activePane()).toBe('pane-s2');

      relayOut(daemon, split('split-a', 'vertical', [pane('s2'), pane('s1')], 0.3), ['s1', 's2'], { active: 'pane-s1' });
      await daemon.idle();
      expect(activePane()).toBe('pane-s1');
    });

    it.each([
      ['after the user used several', ['s1', 's3', 's2'], 's1'],
      ['after the user used no other', ['s2'], 's3'],
    ])('passes to the pane the daemon names when the active pane closes %s', async (_, used, daemonActive) => {
      const ids = ['s1', 's2', 's3'];
      const { daemon } = await openDesktop(split('split-a', 'vertical', [pane('s1'), split('split-b', 'vertical', [pane('s2'), pane('s3')])]), ids);
      for (const id of used) await choose(daemon, id);
      const closing = used[used.length - 1];
      daemon.on('unregister', ({ id }) => {
        const remaining = ids.filter((other) => other !== id);
        relayOut(daemon, split('split-a', 'vertical', remaining.map((other) => pane(other))), remaining, { active: `pane-${daemonActive}` });
        return { event: 'session_unregistered', session: daemonSession(id) };
      });

      pressShortcut('terminal.close', document.querySelector(`[data-pane-id="pane-${closing}"] .terminal-container`)!);
      await daemon.idle();

      expect(daemon.sentOf('unregister').map(({ id }) => id)).toEqual([closing]);
      expect(activePane()).toBe(`pane-${daemonActive}`);
    });
  });

  it('keeps a zoomed pane zoomed through a burst of session updates', async () => {
    const sessionIds = ['s1', 's2'];
    const { daemon } = await openDesktop(SIDE_BY_SIDE, sessionIds);
    const zoomedPane = () => document.querySelector('[data-session-terminal-desktop="ws"]')?.getAttribute('data-zoomed-pane-id');

    pressShortcut('terminal.toggleZoom');
    await daemon.idle();
    expect(zoomedPane()).toBe('pane-s1');

    for (let update = 0; update < 60; update += 1) {
      daemon.emit({
        event: 'sessions_updated',
        sessions: sessionIds.map((id) => daemonSession(id, { state: update % 2 ? 'working' : 'idle' })),
      });
    }
    await daemon.idle();

    expect(zoomedPane()).toBe('pane-s1');
  });

  describe('native browser tile', () => {
    const LABEL = 'browser-ws-tile-browser';
    const browserAt = (url: string) => split('split-a', 'vertical', [pane('s1'), { type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: url }]);

    let nativeVisible: boolean | undefined;

    async function openBrowser(url: string, { holdMount = false } = {}) {
      let landMount = () => {};
      nativeVisible = undefined;
      vi.mocked(isTauri).mockReturnValue(true);
      vi.mocked(invoke).mockImplementation(async (command: string, args?: unknown) => {
        const { geometry } = (args ?? {}) as { geometry?: { visible: boolean } };
        if (command === 'browser_host_mount') {
          if (holdMount) await new Promise<void>((resolve) => { landMount = resolve; });
          nativeVisible = geometry!.visible;
        }
        if (command === 'browser_host_update') {
          if (nativeVisible === undefined) throw new Error('browser host not mounted');
          nativeVisible = geometry!.visible;
        }
        return undefined;
      });
      const view = await openDesktop(pane('s1'), ['s1']);
      const layOut = async (root: unknown) => {
        relayOut(view.daemon, root, ['s1']);
        await view.daemon.idle();
      };
      await layOut(browserAt(url));
      const land = async () => {
        landMount();
        await view.daemon.idle();
      };
      return { ...view, layOut, land };
    }

    const host = (command: string) => vi.mocked(invoke).mock.calls.filter(([name]) => name === command).map(([, args]) => args as Record<string, unknown>);
    const mounted = () => host('browser_host_mount').map(({ url }) => url);

    it('points the open browser at a new address in place, and closes it with its tile', async () => {
      const { layOut } = await openBrowser('https://first.example');
      expect(mounted()).toEqual(['https://first.example']);

      await layOut(browserAt('https://second.example'));
      expect(mounted()).toEqual(['https://first.example', 'https://second.example']);
      expect(host('browser_host_unmount')).toEqual([]);

      await layOut(pane('s1'));
      expect(host('browser_host_unmount')).toEqual([{ label: LABEL }]);
    });

    it('does not load a page again when the daemon stores the address the user browsed to', async () => {
      const { daemon, layOut } = await openBrowser('https://first.example');

      act(() => {
        window.dispatchEvent(new CustomEvent('attn:browser-location', { detail: { label: LABEL, url: 'https://first.example/dashboard' } }));
      });
      await daemon.idle();
      await layOut(browserAt('https://first.example/dashboard'));

      expect(mounted()).toEqual(['https://first.example']);
    });

    it('closes a browser whose tile went away while it was still opening, once it has opened', async () => {
      const { layOut, land } = await openBrowser('https://first.example', { holdMount: true });

      await layOut(pane('s1'));
      expect(host('browser_host_unmount')).toEqual([]);

      await land();
      expect(host('browser_host_unmount')).toEqual([{ label: LABEL }]);
    });

    it('hides the browser under an overlay, even one that opened while the browser was still opening', async () => {
      const { daemon, land } = await openBrowser('https://first.example', { holdMount: true });

      const search = await openActionMenu(daemon);
      await land();
      expect(nativeVisible).toBe(false);

      fireEvent.keyDown(search, { key: 'Escape' });
      await daemon.idle();
      expect(nativeVisible).toBe(true);
    });
  });
});
