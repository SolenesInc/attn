import { invoke, isTauri } from '@tauri-apps/api/core';
import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { agentPane, daemonSession, soloDesktop } from './test/daemonFixtures';
import { laidOutDesktop, pane, relayOut, split } from './test/desktopLayouts';
import { fakeRects, observedResizes, sizeTerminals } from './test/layout';
import { serveSettings } from './test/settings';
import type { CommandMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

const PANES_WIDTH = 2000;
const PANES_HEIGHT = 900;
const CHIP_STEP = 112;
const IDS = Array.from({ length: 11 }, (_, index) => `s${index + 1}`);

function rightChain(nodes: unknown[], depth = 0): unknown {
  const [first, ...rest] = nodes;
  return rest.length === 0 ? first : split(`split-${depth}`, 'vertical', [first, rightChain(rest, depth + 1)]);
}

const ELEVEN_TILES = rightChain(IDS.map(pane));

async function renderRow(settings: Record<string, string>, { root = ELEVEN_TILES, others = [] as string[], shelfWidth = PANES_WIDTH, startOn = 's1', panesHeight = PANES_HEIGHT } = {}) {
  sizeTerminals((terminal) => ({
    clientWidth: (PANES_WIDTH * parseFloat(terminal.closest<HTMLElement>('[data-pane-id]')?.style.width || '100')) / 100,
    clientHeight: panesHeight,
  }));
  fakeRects((element) => {
    if (element.classList.contains('session-terminal-panes')) return new DOMRect(0, 30, PANES_WIDTH, panesHeight);
    if (element.classList.contains('desktop-shelf')) return new DOMRect(0, 0, shelfWidth, 30);
    if (element.dataset.shelfAnchor) {
      const at = Array.from(document.querySelectorAll('[data-shelf-anchor]')).indexOf(element);
      return new DOMRect(at * CHIP_STEP, 4, CHIP_STEP - 4, 22);
    }
    return null;
  });
  const { daemon } = await renderApp({
    initialState: {
      settings,
      sessions: [...IDS, ...others].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: [
        ...others.filter((id) => id === startOn).map((id) => soloDesktop(id, { shortcut_slot: others.indexOf(id) + 2 })),
        { ...laidOutDesktop(root, IDS), active_pane_id: 'pane-s1', shortcut_slot: 1 },
        ...others.filter((id) => id !== startOn).map((id) => soloDesktop(id, { shortcut_slot: others.indexOf(id) + 2 })),
      ],
    },
    script: (scripted) => {
      serveSettings(scripted, settings);
      scripted.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
      scripted.on('spawn_session', ({ id, placement }) => {
        const paneId = `pane-${id}`;
        const below = placement?.direction === 'horizontal';
        scripted.arrangement.placeInRow('ws', { type: 'pane', pane_id: paneId }, placement?.anchor_pane_id ?? '', { below, pane: { ...agentPane(id, 'ws'), pane_id: paneId } });
        return [
          { event: 'session_registered', session: daemonSession(id, { state: 'idle' }) },
          scripted.arrangement.answer({ request_id: `spawn-${id}` }),
          { event: 'spawn_result', id, success: true, desktop_id: 'ws', pane_id: paneId },
        ];
      });
    },
  });
  await openSession(daemon, startOn);
  await daemon.idle();
  return daemon;
}

const FILLS_THE_AREA = 240;
const WIDER_THAN_THE_AREA = 300;
const ROOM_FOR_THAT_WIDTH = 2500;
const SAVED_AREA = 2300;
const SAVED_AREA_COLS = 280;
const SMALLER_AREA = 1800;
const FITS_ALONE_AT_THE_SMALLER_AREA = 180;

const NARROWEST_66 = { narrowest_tile_cols: '66' };

const paneIds = () => Array.from(document.querySelectorAll<HTMLElement>('.desktop-pane[data-pane-id]')).map((element) => element.dataset.paneId);
const onScreen = (...ids: string[]) => ids.map((id) => `pane-${id}`);
const chipKeys = () =>
  Array.from(document.querySelectorAll<HTMLElement>('[data-testid^="shelf-chip-"]')).map((element) => element.dataset.testid!.replace('shelf-chip-pane-', ''));
const widthLabelCounts = () => {
  const counts = Array.from(screen.getByTestId('shelf-width-label').querySelectorAll<HTMLElement>('.desktop-shelf-width-steps i'));
  return { all: counts.map((count) => Number(count.textContent)), current: Number(counts.find((count) => count.classList.contains('is-on'))?.textContent) };
};
const terminalInput = (id: string) => document.querySelector<HTMLElement>(`[data-pane-id="pane-${id}"] [aria-label="Terminal input"]`);
const surface = () => document.querySelector<HTMLElement>('[data-session-terminal-desktop="ws"]')!;
const mountedTerminals = () =>
  Array.from(document.querySelectorAll('[data-pane-id] [aria-label="Terminal input"]')).map((input) => input.closest<HTMLElement>('[data-pane-id]')!.dataset.paneId);

describe('Shelf layout', () => {
  it('shows the screen and the shelf, and unmounts off-screen terminals', async () => {
    const daemon = await renderRow(NARROWEST_66);

    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    expect(mountedTerminals()).toEqual(onScreen('s1', 's2', 's3'));
    expect(chipKeys()).toEqual(['s4', 's5', 's6', 's7', 's8', 's9', 's10', 's11']);
    expect(screen.getByTestId('shelf-live-dots').querySelectorAll('.desktop-shelf-dot')).toHaveLength(3);
    expect(document.querySelector<HTMLElement>('[data-pane-id="pane-s1"]')!.style.width).toBe('33.33333333333333%');
    await daemon.idle();
  });

  describe('with a browser tile off screen', () => {
    const LABEL = 'browser-ws-tile-browser';
    const hostCalls = (command: string) => vi.mocked(invoke).mock.calls
      .filter(([name]) => name === command)
      .map(([, args]) => args as { label: string; url?: string; geometry?: { visible: boolean } });
    const lastVisible = () => vi.mocked(invoke).mock.calls
        .filter(([name]) => name === 'browser_host_mount' || name === 'browser_host_update')
        .map(([, args]) => (args as { geometry: { visible: boolean } }).geometry.visible)
        .slice(-1)[0];
    const browserRow = (url: string) => rightChain([...IDS.slice(0, 3).map(pane), { type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: url }]);

    it('keeps its page loaded and hidden, and shows the same page when it comes back', async () => {
      vi.mocked(isTauri).mockReturnValue(true);
      const daemon = await renderRow(NARROWEST_66, { root: browserRow('https://example.com/') });

      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(hostCalls('browser_host_mount')).toEqual([expect.objectContaining({ label: LABEL, url: 'https://example.com/' })]);
      expect(lastVisible()).toBe(false);

      for (let step = 0; step < 3; step += 1) await gesture(daemon, () => pressShortcut('terminal.focusRight'));

      expect(paneIds()).toEqual(['pane-s2', 'pane-s3', 'tile-browser']);
      expect(lastVisible()).toBe(true);

      await gesture(daemon, () => pressShortcut('terminal.focusLeft'));
      await gesture(daemon, () => pressShortcut('terminal.focusLeft'));
      await gesture(daemon, () => pressShortcut('terminal.focusLeft'));

      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(lastVisible()).toBe(false);
      expect(hostCalls('browser_host_mount')).toHaveLength(1);
      expect(hostCalls('browser_host_unmount')).toEqual([]);
    });

    it('keeps its page loaded and hidden while another tile fills the screen in focus mode', async () => {
      vi.mocked(isTauri).mockReturnValue(true);
      const daemon = await renderRow(NARROWEST_66, { root: split('split-a', 'vertical', [pane('s1'), { type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://example.com/' }]) });
      expect(lastVisible()).toBe(true);

      await gesture(daemon, () => pressShortcut('terminal.toggleMaximize'));

      expect(surface()).toHaveAttribute('data-maximized-pane-id', 'pane-s1');
      expect(lastVisible()).toBe(false);

      await gesture(daemon, () => pressShortcut('terminal.toggleMaximize'));

      expect(lastVisible()).toBe(true);
      expect(hostCalls('browser_host_mount')).toHaveLength(1);
      expect(hostCalls('browser_host_unmount')).toEqual([]);
    });

    it('closes its page with the tile', async () => {
      vi.mocked(isTauri).mockReturnValue(true);
      const daemon = await renderRow(NARROWEST_66, { root: browserRow('https://example.com/') });

      relayOut(daemon, rightChain(IDS.slice(0, 3).map(pane)), IDS);
      await daemon.idle();

      expect(hostCalls('browser_host_unmount')).toEqual([{ label: LABEL }]);
    });
  });

  it('shows no shelf when every column fits', async () => {
    await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 3).map(pane)) });
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    expect(screen.queryByTestId('desktop-shelf')).toBeNull();
  });

  it('slides along the row with the move keys, and goes to the next desktop past the end', async () => {
    const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 4).map(pane)), others: ['next'] });

    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s3');
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));

    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s4');
    expect(paneIds()).toEqual(onScreen('s2', 's3', 's4'));
    expect(chipKeys()).toEqual(['s1']);
    expect(mountedTerminals()).toEqual(onScreen('s2', 's3', 's4'));

    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(daemon.sentOf('desktop_set_current').slice(-1)[0]).toMatchObject({ desktop_id: 'desktop-next' });
  });

  it('slides left only past the screen’s first column', async () => {
    const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 4).map(pane)) });
    for (let press = 0; press < 3; press += 1) await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(paneIds()).toEqual(onScreen('s2', 's3', 's4'));

    await gesture(daemon, () => pressShortcut('terminal.focusLeft'));
    await gesture(daemon, () => pressShortcut('terminal.focusLeft'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s2');
    expect(paneIds()).toEqual(onScreen('s2', 's3', 's4'));

    await gesture(daemon, () => pressShortcut('terminal.focusLeft'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s1');
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
  });

  it('returns to a stack’s most recently used tile', async () => {
    const stack = split('stack', 'horizontal', [pane('s2'), pane('s3')]);
    const daemon = await renderRow(NARROWEST_66, { root: rightChain([pane('s1'), stack, pane('s4')]) });

    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s2');
    await gesture(daemon, () => pressShortcut('terminal.focusDown'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s3');
    await gesture(daemon, () => pressShortcut('terminal.focusLeft'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s1');
    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s3');
  });

  it('puts chips that do not fit behind caps with their menus', async () => {
    const daemon = await renderRow(NARROWEST_66, { shelfWidth: 500 });

    expect(chipKeys()).toEqual(['s4', 's5']);
    expect(screen.getByTestId('shelf-cap-after')).toHaveTextContent('6 ›');
    fireEvent.click(screen.getByTestId('shelf-cap-after'));
    expect(chipKeys()).toEqual(['s4', 's5', 's6', 's7', 's8', 's9', 's10', 's11']);

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('shelf-chip-pane-s10')));
    expect(paneIds()).toEqual(onScreen('s1', 's10', 's2'));
    expect(screen.queryByTestId('shelf-menu-after')).toBeNull();
  });

  it('steps the narrowest width with the keys until one fewer or one more tile fits', async () => {
    const daemon = await renderRow(NARROWEST_66);

    await gesture(daemon, () => pressShortcut('shelf.fewerTiles'));
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '86' });
    expect(screen.getByTestId('shelf-width-label')).toHaveTextContent('2 tiles');
    expect(widthLabelCounts()).toEqual({ all: [1, 2, 3, 4], current: 2 });
    expect(screen.getByTestId('shelf-width-label')).not.toHaveTextContent('57');
    expect(paneIds()).toEqual(onScreen('s1', 's2'));

    await gesture(daemon, () => pressShortcut('shelf.moreTiles'));
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '76' });
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));

    await gesture(daemon, () => pressShortcut('shelf.moreTiles'));
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '57' });
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3', 's4'));
  });

  it('says the numbers when no narrower step can fit another tile', async () => {
    const daemon = await renderRow({ narrowest_tile_cols: '57' });
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3', 's4'));

    await gesture(daemon, () => pressShortcut('shelf.moreTiles'));

    expect(screen.getByText('5 tiles at the narrowest width (57 cols, 480px) need 2400px. Tiles have 2000px here.')).toBeInTheDocument();
    expect(daemon.sentOf('set_setting')).toEqual([]);
  });

  it('states the width in force when the setting is not a step', async () => {
    const daemon = await renderRow({ narrowest_tile_cols: '40' });
    await gesture(daemon, () => pressShortcut('shelf.moreTiles'));
    expect(screen.getByText('5 tiles at the narrowest width (40 cols, 480px) need 2400px. Tiles have 2000px here.')).toBeInTheDocument();
    expect(daemon.sentOf('set_setting')).toEqual([]);
  });

  it('steps down from a setting between steps, and keeps the highlighted count true', async () => {
    const daemon = await renderRow({ narrowest_tile_cols: '70' });

    await gesture(daemon, () => pressShortcut('shelf.fewerTiles'));
    expect(widthLabelCounts().current).toBe(paneIds().length);
    expect(screen.getByTestId('shelf-width-label')).toHaveTextContent(`at least ${daemon.sentOf('set_setting').slice(-1)[0].value} cols each`);
  });

  const wanted = (id: string, cols: number) => ({ ...(pane(id) as object), wanted_cols: cols });

  it('lets a dragged width wider than the tile area give way, and returns it when the room does', async () => {
    const resize = observedResizes();
    const daemon = await renderRow(NARROWEST_66, { root: rightChain([wanted('s1', WIDER_THAN_THE_AREA), pane('s2'), pane('s3')]) });
    const resizeTiles = (width: number) => gesture(daemon, () => resize('.session-terminal-panes', { width, height: PANES_HEIGHT }));

    await resizeTiles(ROOM_FOR_THAT_WIDTH);
    expect(paneIds()).toEqual(onScreen('s1'));

    await resizeTiles(PANES_WIDTH);
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    expect(daemon.sentOf('desktop_set_wanted_cols')).toEqual([]);

    await resizeTiles(ROOM_FOR_THAT_WIDTH);
    expect(paneIds()).toEqual(onScreen('s1'));
  });

  const wantedAt = (id: string, cols: number, areaCols: number) => ({ ...wanted(id, cols), wanted_area_cols: areaCols });
  const wantedColsOf = (daemon: ScriptedDaemon, id: string) => {
    const find = (node: { pane_id?: string; wanted_cols?: number; children?: never[] }): number | undefined =>
      node.pane_id === `pane-${id}` ? node.wanted_cols : node.children?.map(find).find((cols) => cols !== undefined);
    return find(JSON.parse(daemon.arrangement.desktop('ws')!.tree_json));
  };

  it('lets a saved width hide neighbours only at the area it was saved at or larger', async () => {
    const resize = observedResizes();
    const daemon = await renderRow(NARROWEST_66, { root: rightChain([wantedAt('s1', FILLS_THE_AREA, SAVED_AREA_COLS), pane('s2'), pane('s3')]) });
    const resizeTiles = (width: number) => gesture(daemon, () => resize('.session-terminal-panes', { width, height: PANES_HEIGHT }));

    await resizeTiles(PANES_WIDTH);
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));

    await resizeTiles(SAVED_AREA);
    expect(paneIds()).toEqual(onScreen('s1'));

    await resizeTiles(PANES_WIDTH);
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    expect(wantedColsOf(daemon, 's1')).toBe(FILLS_THE_AREA);
    expect(daemon.sentOf('desktop_set_wanted_cols')).toEqual([]);
  });

  it('keeps a saved width with no stored area hiding neighbours wherever it fits the area', async () => {
    const resize = observedResizes();
    const daemon = await renderRow(NARROWEST_66, { root: rightChain([wanted('s1', FITS_ALONE_AT_THE_SMALLER_AREA), pane('s2'), pane('s3')]) });

    await gesture(daemon, () => resize('.session-terminal-panes', { width: SMALLER_AREA, height: PANES_HEIGHT }));

    expect(paneIds()).toEqual(onScreen('s1'));
    expect(wantedColsOf(daemon, 's1')).toBe(FITS_ALONE_AT_THE_SMALLER_AREA);
  });

  describe('when a dragged width blocks more tiles', () => {
    const clearedLeaves = (daemon: ScriptedDaemon) => daemon.sentOf('desktop_set_wanted_cols').map((command) => [command.leaf_id, command.cols]);

    it('clears the width and brings the next tile back without changing the narrowest width', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain([wanted('s1', FILLS_THE_AREA), pane('s2')]) });
      expect(paneIds()).toEqual(onScreen('s1'));

      await gesture(daemon, () => pressShortcut('shelf.moreTiles'));

      expect(paneIds()).toEqual(onScreen('s1', 's2'));
      expect(clearedLeaves(daemon)).toEqual([['pane-s1', 0]]);
      expect(daemon.sentOf('set_setting')).toEqual([]);
      expect(screen.getByTestId('shelf-width-label')).toHaveTextContent('2 tiles');
      expect(screen.getByTestId('shelf-width-label')).toHaveTextContent('Cleared the dragged width');
    });

    it('clears the width of the tile coming back too', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain([wanted('s1', FILLS_THE_AREA), wanted('s2', FILLS_THE_AREA), pane('s3')]) });

      await gesture(daemon, () => pressShortcut('shelf.moreTiles'));

      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(clearedLeaves(daemon)).toEqual([['pane-s1', 0], ['pane-s2', 0]]);
      expect(screen.getByTestId('shelf-width-label')).toHaveTextContent('Cleared 2 dragged widths');
    });

    it('steps narrower as well when clearing alone is not enough', async () => {
      const daemon = await renderRow({ narrowest_tile_cols: '150' }, { root: rightChain([wanted('s1', FILLS_THE_AREA), pane('s2'), pane('s3')]) });
      expect(paneIds()).toEqual(onScreen('s1'));

      await gesture(daemon, () => pressShortcut('shelf.moreTiles'));

      expect(clearedLeaves(daemon)).toEqual([['pane-s1', 0]]);
      expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '120' });
      expect(paneIds()).toEqual(onScreen('s1', 's2'));
    });

    it('keeps a dragged width when a narrower step alone brings the tile back', async () => {
      const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: rightChain([wanted('s1', 90), pane('s2'), pane('s3')]) });
      expect(paneIds()).toEqual(onScreen('s1', 's2'));

      await gesture(daemon, () => pressShortcut('shelf.moreTiles'));

      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(clearedLeaves(daemon)).toEqual([]);
    });
  });

  it('offers the width steps in the palette', async () => {
    const daemon = await renderRow(NARROWEST_66);
    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    await gesture(daemon, () => fireEvent.change(screen.getByRole('combobox'), { target: { value: '>wider tiles' } }));

    await gesture(daemon, () => fireEvent.mouseDown(screen.getByRole('option', { name: /Fewer, wider tiles/ })));

    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '86' });
  });

  describe('moves', () => {
    const FOUR = rightChain(IDS.slice(0, 4).map(pane));
    const lastMove = (daemon: ScriptedDaemon) => daemon.sentOf('desktop_move_leaf').slice(-1)[0];
    const active = () => surface().getAttribute('data-active-leaf-id');
    const rowOf = (daemon: ScriptedDaemon) => {
      const leaves = (node: { pane_id?: string; tile_id?: string; children?: never[] }): string[] =>
        node.children ? node.children.flatMap(leaves) : [(node.pane_id ?? node.tile_id)!.replace('pane-', '')];
      return leaves(JSON.parse(daemon.arrangement.desktop('ws')!.tree_json));
    };
    const chip = (id: string) => screen.getByTestId(`shelf-chip-pane-${id}`);
    const slideTo = async (daemon: ScriptedDaemon, ...keys: ('terminal.focusLeft' | 'terminal.focusRight')[]) => {
      for (const key of keys) await gesture(daemon, () => pressShortcut(key));
    };

    it('carries the active column along the row and keeps it active', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });

      await gesture(daemon, () => pressShortcut('shelf.carryRight'));
      expect(lastMove(daemon)).toMatchObject({ source_desktop_id: 'ws', target_desktop_id: 'ws', leaf_id: 'pane-s1', anchor_id: 'pane-s2', edge: 'right', whole_column: true });
      expect(rowOf(daemon)).toEqual(['s2', 's1', 's3', 's4']);
      expect(active()).toBe('pane-s1');
      expect(paneIds()).toEqual(onScreen('s2', 's1', 's3'));

      await gesture(daemon, () => pressShortcut('shelf.carryLeft'));
      expect(rowOf(daemon)).toEqual(['s1', 's2', 's3', 's4']);
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    });

    it('carries a stack as one column, and names the row end', async () => {
      const stack = split('stack', 'horizontal', [pane('s1'), pane('s2')]);
      const daemon = await renderRow(NARROWEST_66, { root: rightChain([stack, pane('s3')]) });

      await gesture(daemon, () => pressShortcut('shelf.carryLeft'));
      expect(screen.getByText('This tile is already at the start of the row.')).toBeInTheDocument();
      expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);

      await gesture(daemon, () => pressShortcut('shelf.carryRight'));
      expect(rowOf(daemon)).toEqual(['s3', 's1', 's2']);
      expect(active()).toBe('pane-s1');
    });

    it('brings a chip in place of the neighbour, and leaves the rightmost active tile where it is', async () => {
      const daemon = await renderRow(NARROWEST_66);
      await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight');
      expect(active()).toBe('pane-s3');
      const shows = daemon.sentOf('desktop_show_leaf').length;

      await gesture(daemon, () => fireEvent.click(chip('s7')));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s7', anchor_id: 'pane-s3', edge: 'left', whole_column: true });
      expect(lastMove(daemon).activate).toBeUndefined();
      expect(daemon.sentOf('desktop_show_leaf')).toHaveLength(shows);
      expect(active()).toBe('pane-s7');
      expect(paneIds()).toEqual(onScreen('s2', 's7', 's3'));
      expect(rowOf(daemon).slice(0, 4)).toEqual(['s1', 's2', 's7', 's3']);
      expect(chip('s1')).toHaveClass('is-arrival');
    });

    describe('with two tiles on screen', () => {
      const TWO_FIT = { narrowest_tile_cols: '100' };

      it('keeps a dragged width that holds the brought tile apart, and shows that tile alone', async () => {
        const daemon = await renderRow(TWO_FIT, { root: rightChain([pane('s1'), pane('s2'), pane('s3'), wanted('s4', FILLS_THE_AREA)]) });
        expect(paneIds()).toEqual(onScreen('s1', 's2'));

        await gesture(daemon, () => fireEvent.click(chip('s4')));

        expect(paneIds()).toEqual(onScreen('s4'));
        expect(active()).toBe('pane-s4');
        expect(daemon.sentOf('desktop_set_wanted_cols')).toEqual([]);
      });

      it('replaces the right neighbour while the active tile is on the left', async () => {
        const daemon = await renderRow(TWO_FIT);
        expect(paneIds()).toEqual(onScreen('s1', 's2'));

        await gesture(daemon, () => fireEvent.click(chip('s4')));

        expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s4', anchor_id: 'pane-s1', edge: 'right' });
        expect(paneIds()).toEqual(onScreen('s1', 's4'));
        expect(active()).toBe('pane-s4');
      });

      it('replaces the left neighbour while the active tile is on the right', async () => {
        const daemon = await renderRow(TWO_FIT);
        await slideTo(daemon, 'terminal.focusRight');
        expect(paneIds()).toEqual(onScreen('s1', 's2'));
        expect(active()).toBe('pane-s2');

        await gesture(daemon, () => fireEvent.click(chip('s4')));

        expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s4', anchor_id: 'pane-s2', edge: 'left' });
        expect(rowOf(daemon).slice(0, 3)).toEqual(['s1', 's4', 's2']);
        expect(paneIds()).toEqual(onScreen('s4', 's2'));
        expect(active()).toBe('pane-s4');
      });
    });

    it('brings an off-screen tile from the sidebar and the agent palette', async () => {
      const daemon = await renderRow(NARROWEST_66);
      await openSession(daemon, 's9');
      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s9', anchor_id: 'pane-s1', whole_column: true });
      expect(paneIds()).toEqual(onScreen('s1', 's9', 's2'));

      await gesture(daemon, () => pressShortcut('ui.actionMenu'));
      const option = within(screen.getByRole('dialog', { name: 'Agents' })).getAllByRole('option').find((entry) => entry.textContent?.includes('s7'));
      await gesture(daemon, () => fireEvent.mouseDown(option!));
      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s7', anchor_id: 'pane-s9', whole_column: true });
      expect(paneIds()).toEqual(onScreen('s1', 's9', 's7'));
    });

    it('keeps typing for the brought terminal while the daemon answers, and reports it when the bring fails', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });
      const held: CommandMessage<'desktop_move_leaf'>[] = [];
      daemon.on('desktop_move_leaf', (command) => { held.push(command); return undefined; });

      await gesture(daemon, () => fireEvent.click(chip('s4')));
      await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
      expect(daemon.sentOf('pty_input')).toEqual([]);
      expect(active()).toBe('pane-s1');

      await gesture(daemon, () => {
        relayOut({ arrange: (change) => { daemon.arrangement.desktops = change(daemon.arrangement.desktops); } }, rightChain(['s1', 's4', 's2', 's3'].map(pane)), IDS.slice(0, 4), { active: 'pane-s4' });
        daemon.emit(daemon.arrangement.answer(held[0]));
        daemon.replyTo(held[0], { event: 'profile_action_result', action: held[0].cmd, request_id: held[0].request_id, success: true });
      });
      expect(active()).toBe('pane-s4');
      expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 's4', data: 'x' })]);

      await gesture(daemon, () => fireEvent.click(chip('s3')));
      await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'y', code: 'KeyY' }));
      await gesture(daemon, () => daemon.replyTo(held[1], { event: 'profile_action_result', action: held[1].cmd, request_id: held[1].request_id, success: false, error: 'refused' }));
      expect(screen.getByText(/Input typed while switching could not be delivered/)).toBeInTheDocument();
      expect(daemon.sentOf('pty_input')).toHaveLength(1);
    });

    it('slides, and reorders nothing, when ⌘[ goes back to an off-screen tile', async () => {
      const daemon = await renderRow(NARROWEST_66);
      await gesture(daemon, () => fireEvent.click(chip('s7')));
      const row = rowOf(daemon);
      const moves = daemon.sentOf('desktop_move_leaf').length;
      await gesture(daemon, () => pressShortcut('shelf.fewerTiles'));
      await gesture(daemon, () => pressShortcut('shelf.fewerTiles'));
      expect(paneIds()).toEqual(onScreen('s7'));

      await gesture(daemon, () => pressShortcut('session.historyBack'));

      expect(active()).toBe('pane-s1');
      expect(paneIds()).toEqual(onScreen('s1'));
      expect(daemon.sentOf('desktop_move_leaf')).toHaveLength(moves);
      expect(rowOf(daemon)).toEqual(row);
    });

    it('sends a single tile past the far side and keeps focus in its slot', async () => {
      const daemon = await renderRow(NARROWEST_66);
      await slideTo(daemon, 'terminal.focusRight');

      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s2', anchor_id: 'pane-s11', edge: 'right', whole_column: true, activate: false });
      expect(active()).toBe('pane-s3');
      expect(paneIds()).toEqual(onScreen('s1', 's3', 's4'));
      expect(rowOf(daemon).slice(-1)).toEqual(['s2']);
      expect(chip('s2')).toHaveClass('is-arrival');
    });

    it('sends a tile to the row start when nothing lies past the screen', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });
      await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight', 'terminal.focusRight');
      expect(paneIds()).toEqual(onScreen('s2', 's3', 's4'));

      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s4', anchor_id: 'pane-s1', edge: 'left', activate: false });
      expect(active()).toBe('pane-s3');
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(rowOf(daemon)).toEqual(['s4', 's1', 's2', 's3']);
    });

    it('takes a stacked tile out of its stack to the row end', async () => {
      const stack = split('stack', 'horizontal', [pane('s1'), pane('s2')]);
      const daemon = await renderRow(NARROWEST_66, { root: rightChain([stack, pane('s3'), pane('s4'), pane('s5')]) });

      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s1', anchor_id: 'pane-s5', edge: 'right', activate: false });
      expect(lastMove(daemon).whole_column).toBeUndefined();
      expect(active()).toBe('pane-s2');
      expect(rowOf(daemon)).toEqual(['s2', 's3', 's4', 's5', 's1']);
      expect(paneIds()).toEqual(onScreen('s2', 's3', 's4'));
      expect(chip('s1')).toHaveClass('is-arrival');
    });

    it('holds a column that would have fit, skips it when sliding, and gives it back when brought', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      await slideTo(daemon, 'terminal.focusRight');

      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
      expect(active()).toBe('pane-s3');
      expect(paneIds()).toEqual(onScreen('s1', 's3'));
      expect(chipKeys()).toEqual(['s2']);

      await slideTo(daemon, 'terminal.focusLeft');
      expect(active()).toBe('pane-s1');

      await gesture(daemon, () => fireEvent.click(chip('s2')));
      expect(active()).toBe('pane-s2');
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(screen.queryByTestId('desktop-shelf')).toBeNull();
    });

    it('holds again when another held column leaves room', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      await slideTo(daemon, 'terminal.focusRight');
      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));
      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
      expect(active()).toBe('pane-s1');
      expect(paneIds()).toEqual(onScreen('s1'));
      expect(chipKeys()).toEqual(['s2', 's3']);
    });

    it('steps to the next tile when the screen shows only one', async () => {
      const daemon = await renderRow({ narrowest_tile_cols: '150' }, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      expect(paneIds()).toEqual(onScreen('s1'));

      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s1', anchor_id: 'pane-s3', edge: 'right', activate: false });
      expect(active()).toBe('pane-s2');
      expect(paneIds()).toEqual(onScreen('s2'));
    });

    it('offers the moves in the palette and the shortcut help', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });
      await gesture(daemon, () => pressShortcut('ui.commandPalette'));
      await gesture(daemon, () => fireEvent.change(screen.getByRole('combobox'), { target: { value: '>carry tile right' } }));
      await gesture(daemon, () => fireEvent.mouseDown(screen.getByRole('option', { name: /Carry tile right along the row/ })));
      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s1', anchor_id: 'pane-s2', edge: 'right' });

      await gesture(daemon, () => pressShortcut('ui.commandPalette'));
      await gesture(daemon, () => fireEvent.change(screen.getByRole('combobox'), { target: { value: '>send tile to the shelf' } }));
      await gesture(daemon, () => fireEvent.mouseDown(screen.getByRole('option', { name: /Send tile to the shelf/ })));
      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s1', activate: false });

      await gesture(daemon, () => pressShortcut('ui.showShortcuts'));
      expect(screen.getByText('Carry tile along the row')).toBeInTheDocument();
      expect(screen.getByText('Send tile to the shelf')).toBeInTheDocument();
    });

    it('says when the only tile cannot go to the shelf', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: pane('s1') });
      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));
      expect(screen.getByText('This is the only tile on screen.')).toBeInTheDocument();
    });

    it('opens beside the active column at capacity, keeping the anchor on screen', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight');

      await gesture(daemon, () => pressShortcut('terminal.splitVertical'));

      const [spawn] = daemon.sentOf('spawn_session').slice(-1);
      expect(spawn.placement).toMatchObject({ direction: 'vertical', anchor_pane_id: 'pane-s3' });
      expect(active()).toBe(`pane-${spawn.id}`);
      expect(paneIds()).toEqual(['pane-s2', 'pane-s3', `pane-${spawn.id}`]);
      expect(chip('s1')).toHaveClass('is-arrival');
    });

    it('opens below in the same column until the height limit, then beside with the numbers', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 2).map(pane)) });

      await gesture(daemon, () => pressShortcut('terminal.splitHorizontal'));
      const [below] = daemon.sentOf('spawn_session').slice(-1);
      expect(below.placement).toMatchObject({ direction: 'horizontal', anchor_pane_id: 'pane-s1' });

      await gesture(daemon, () => pressShortcut('terminal.splitHorizontal'));
      const [beside] = daemon.sentOf('spawn_session').slice(-1);
      expect(beside.placement).toMatchObject({ direction: 'vertical' });
      expect(screen.getByText(/3 stacked tiles at the 320px minimum need 960px\. Tiles have 900px here, so it opened beside instead\./)).toBeInTheDocument();
    });

    describe('a tile an agent opens', () => {
      const agentOpens = (daemon: ScriptedDaemon, agent: string) => gesture(daemon, () => {
        const opened = { type: 'tile', tile_id: 'doc', tile_kind: 'markdown', tile_params: '/plan.md', tile_session_id: agent };
        daemon.arrangement.placeInRow('ws', opened, `pane-${agent}`, { focus: false });
        daemon.emit(daemon.arrangement.changed());
      });
      const closeDocument = (daemon: ScriptedDaemon) => gesture(daemon, () => pressShortcut('terminal.close'));
      const agentOpensAgain = (daemon: ScriptedDaemon, agent: string) => gesture(daemon, () => {
        daemon.arrangement.requestTileShow('ws', 'doc', agent);
        daemon.emit(daemon.arrangement.changed());
      });

      it('slides into view without taking focus when the active tile stays on screen, and closing puts the screen back', async () => {
        const daemon = await renderRow(NARROWEST_66);
        await slideTo(daemon, 'terminal.focusRight');
        expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));

        await agentOpens(daemon, 's3');

        expect(active()).toBe('pane-s2');
        expect(paneIds()).toEqual(['pane-s2', 'pane-s3', 'doc']);
        expect(screen.queryByTestId('shelf-chip-doc')).toBeNull();

        await gesture(daemon, () => {
          const desktop = daemon.arrangement.desktop('ws')!;
          daemon.arrangement.replace(daemon.arrangement.withTree(desktop, JSON.parse(JSON.stringify(rightChain(IDS.map(pane))))));
          daemon.emit(daemon.arrangement.changed());
        });
        expect(active()).toBe('pane-s2');
        expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      });

      it('counts as waiting when it cannot show, and ⌘J takes it before the queue; closing restores the screen', async () => {
        const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: FOUR });
        await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight');
        const screenBefore = paneIds();
        expect(screenBefore).toEqual(onScreen('s2', 's3'));
        await gesture(daemon, () => daemon.emit({ event: 'sessions_updated', sessions: [
          ...IDS.slice(0, 3).map((id) => daemonSession(id, { state: 'idle' })),
          daemonSession('s4', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-11-05T00:00:00Z' }),
        ] }));

        await agentOpens(daemon, 's1');

        expect(active()).toBe('pane-s3');
        expect(paneIds()).toEqual(screenBefore);
        expect(screen.getByTestId('shelf-chip-doc')).toHaveClass('is-needs', 'is-arrival');

        await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));

        expect(lastMove(daemon)).toMatchObject({ leaf_id: 'doc', anchor_id: 'pane-s3', edge: 'left', whole_column: true });
        expect(active()).toBe('doc');
        expect(paneIds()).toEqual(['doc', 'pane-s3']);

        daemon.on('desktop_remove_leaf', (command) => {
          const desktop = daemon.arrangement.desktop('ws')!;
          const placed = daemon.arrangement.withTree(desktop, JSON.parse(JSON.stringify(rightChain(IDS.slice(0, 4).map(pane)))));
          daemon.arrangement.replace({ ...placed, active_pane_id: 'pane-s4' });
          return [daemon.arrangement.answer(command), { event: 'profile_action_result', action: command.cmd, success: true, request_id: command.request_id ?? '' }];
        });
        await closeDocument(daemon);

        expect(paneIds()).toEqual(screenBefore);
        expect(active()).toBe('pane-s3');
      });

      it('with the agent on the shelf leaves the screen alone', async () => {
        const daemon = await renderRow(NARROWEST_66);
        await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight');
        await agentOpens(daemon, 's4');
        expect(active()).toBe('pane-s3');
        expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
        expect(screen.getByTestId('shelf-chip-doc')).toHaveClass('is-needs');
      });

      it('shows a document opened by an agent newly revealed by more tiles', async () => {
        const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: FOUR });
        await slideTo(daemon, 'terminal.focusRight');
        expect(active()).toBe('pane-s2');
        expect(paneIds()).toEqual(onScreen('s1', 's2'));
        await gesture(daemon, () => pressShortcut('shelf.moreTiles'));
        expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
        await agentOpens(daemon, 's3');
        expect(active()).toBe('pane-s2');
        expect(paneIds()).toEqual(['pane-s2', 'pane-s3', 'doc']);
      });

      describe('and opens again after the user viewed it', () => {
        const documentViewedThenLeft = async () => {
          const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: FOUR, others: ['other', 'third'] });
          await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight');
          await agentOpens(daemon, 's1');
          await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));
          expect(paneIds()).toEqual(['doc', 'pane-s3']);
          await gesture(daemon, () => pressShortcut('shelf.carryRight'));
          expect(paneIds()).toEqual(['pane-s3', 'doc']);
          await slideTo(daemon, 'terminal.focusLeft', 'terminal.focusLeft');
          expect(active()).toBe('pane-s2');
          expect(paneIds()).toEqual(onScreen('s2', 's3'));
          expect(screen.getByTestId('shelf-chip-doc')).not.toHaveClass('is-needs');
          return daemon;
        };

        it('counts as waiting once however often it asks, and ⌘J brings it', async () => {
          const daemon = await documentViewedThenLeft();

          await agentOpensAgain(daemon, 's1');
          await agentOpensAgain(daemon, 's1');

          expect(active()).toBe('pane-s2');
          expect(paneIds()).toEqual(onScreen('s2', 's3'));
          expect(screen.getByTestId('shelf-chip-doc')).toHaveClass('is-needs', 'is-arrival');

          await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));

          expect(active()).toBe('doc');
          expect(screen.queryByTestId('shelf-chip-doc')).toBeNull();
        });

        it('slides into view for an agent on screen when the active tile stays', async () => {
          const daemon = await renderRow(NARROWEST_66, { root: FOUR });
          await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight', 'terminal.focusRight');
          await agentOpens(daemon, 's4');
          expect(paneIds()).toEqual(['pane-s3', 'pane-s4', 'doc']);
          await slideTo(daemon, 'terminal.focusLeft', 'terminal.focusLeft', 'terminal.focusRight');
          expect(active()).toBe('pane-s3');
          expect(paneIds()).toEqual(onScreen('s2', 's3', 's4'));

          await agentOpensAgain(daemon, 's4');

          expect(active()).toBe('pane-s3');
          expect(paneIds()).toEqual(['pane-s3', 'pane-s4', 'doc']);
          expect(screen.queryByTestId('shelf-chip-doc')).toBeNull();
        });

        it('leaves a document already on screen alone, and closing it still restores the first screen', async () => {
          const daemon = await renderRow(NARROWEST_66);
          await slideTo(daemon, 'terminal.focusRight');
          await agentOpens(daemon, 's3');
          expect(paneIds()).toEqual(['pane-s2', 'pane-s3', 'doc']);

          await agentOpensAgain(daemon, 's3');

          expect(active()).toBe('pane-s2');
          expect(paneIds()).toEqual(['pane-s2', 'pane-s3', 'doc']);
          expect(screen.queryByTestId('shelf-chip-doc')).toBeNull();

          await gesture(daemon, () => {
            const desktop = daemon.arrangement.desktop('ws')!;
            daemon.arrangement.replace(daemon.arrangement.withTree(desktop, JSON.parse(JSON.stringify(rightChain(IDS.map(pane))))));
            daemon.emit(daemon.arrangement.changed());
          });
          expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
        });

        it.each([
          ['stays mounted', ['desktop.select2']],
          ['unmounted', ['desktop.select2', 'desktop.select3']],
        ] as const)('waits for the user when the desktop %s meanwhile', async (_name, leave) => {
          const daemon = await documentViewedThenLeft();
          for (const key of leave) await gesture(daemon, () => pressShortcut(key));

          await agentOpensAgain(daemon, 's1');

          await gesture(daemon, () => pressShortcut('desktop.select1'));
          expect(active()).toBe('pane-s2');
          expect(screen.getByTestId('shelf-chip-doc')).toHaveClass('is-needs');
          await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));
          expect(active()).toBe('doc');
        });
      });

      describe('while its desktop is not current', () => {
        const NARROW = { narrowest_tile_cols: '86' };
        const others = ['other', 'third'];
        const present = () => document.querySelector('[data-session-terminal-desktop="ws"]');
        const returnAndJump = async (daemon: ScriptedDaemon) => {
          await gesture(daemon, () => pressShortcut('desktop.select1'));
          expect(active()).not.toBe('doc');
          expect(screen.getByTestId('shelf-chip-doc')).toHaveClass('is-needs');
          await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));
          expect(active()).toBe('doc');
        };

        it('waits for the user while the desktop stays mounted', async () => {
          const daemon = await renderRow(NARROW, { root: FOUR, others });
          await gesture(daemon, () => pressShortcut('desktop.select2'));
          expect(present()).not.toBeNull();
          await agentOpens(daemon, 's4');
          await returnAndJump(daemon);
        });

        it('waits for the user after the desktop unmounted', async () => {
          const daemon = await renderRow(NARROW, { root: FOUR, others });
          await gesture(daemon, () => pressShortcut('desktop.select2'));
          await gesture(daemon, () => pressShortcut('desktop.select3'));
          expect(present()).toBeNull();
          await agentOpens(daemon, 's4');
          await returnAndJump(daemon);
        });

        it('waits for the user on a desktop this app has not shown yet', async () => {
          const daemon = await renderRow(NARROW, { root: FOUR, others, startOn: 'other' });
          expect(present()).toBeNull();
          await agentOpens(daemon, 's4');
          await returnAndJump(daemon);
        });

        it('slides into view on return when its agent was on screen and the active tile stays', async () => {
          const daemon = await renderRow(NARROW, { root: FOUR, others });
          await slideTo(daemon, 'terminal.focusRight');
          await gesture(daemon, () => pressShortcut('desktop.select2'));
          await gesture(daemon, () => pressShortcut('desktop.select3'));
          await agentOpens(daemon, 's2');
          await gesture(daemon, () => pressShortcut('desktop.select1'));
          expect(active()).toBe('pane-s2');
          expect(paneIds().filter((id) => id !== 'pane-third')).toEqual(['pane-s2', 'doc']);
        });
      });

      it('a terminal an agent starts flashes its chip and moves nothing', async () => {
        const daemon = await renderRow(NARROWEST_66);
        await gesture(daemon, () => {
          daemon.arrangement.placeInRow('ws', pane('late'), 'pane-s11', { focus: false, pane: agentPane('late', 'ws') });
          daemon.emit(daemon.arrangement.changed());
        });
        expect(active()).toBe('pane-s1');
        expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
        expect(chip('late')).toHaveClass('is-arrival');
        expect(chip('late')).not.toHaveClass('is-needs');
        act(() => { fireEvent.animationEnd(chip('late')); });
        expect(chip('late')).not.toHaveClass('is-arrival');
      });
    });

    it('keeps focus in the slot when ⌘W closes a tile and the next one slides in', async () => {
      const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: FOUR });
      await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight', 'terminal.focusRight');
      await slideTo(daemon, 'terminal.focusLeft', 'terminal.focusLeft');
      expect(paneIds()).toEqual(onScreen('s2', 's3'));
      expect(active()).toBe('pane-s2');
      expect(terminalInput('s2')).toHaveFocus();
      daemon.on('unregister', () => ({ event: 'session_unregistered', session: daemonSession('s2') }));

      await gesture(daemon, () => pressShortcut('session.close'));
      await gesture(daemon, () => {
        relayOut({ arrange: (change) => { daemon.arrangement.desktops = change(daemon.arrangement.desktops); } }, rightChain(['s1', 's3', 's4'].map(pane)), ['s1', 's3', 's4'], { active: 'pane-s3' });
        daemon.emit(daemon.arrangement.changed());
      });

      expect(paneIds()).toEqual(onScreen('s3', 's4'));
      expect(active()).toBe('pane-s3');
      expect(terminalInput('s3')).toHaveFocus();
    });
  });

  describe('drags', () => {
    const FOUR = rightChain(IDS.slice(0, 4).map(pane));
    const moves = (daemon: ScriptedDaemon) => daemon.sentOf('desktop_move_leaf');
    const widths = (daemon: ScriptedDaemon) => daemon.sentOf('desktop_set_wanted_cols');
    const active = () => surface().getAttribute('data-active-leaf-id');
    const rowOf = (daemon: ScriptedDaemon) => {
      const leaves = (node: { pane_id?: string; tile_id?: string; children?: never[] }): string[] =>
        node.children ? node.children.flatMap(leaves) : [(node.pane_id ?? node.tile_id)!.replace('pane-', '')];
      return leaves(JSON.parse(daemon.arrangement.desktop('ws')!.tree_json));
    };
    const chip = (id: string) => screen.getByTestId(`shelf-chip-pane-${id}`);
    const header = (id: string) => document.querySelector<HTMLElement>(`[data-pane-id="pane-${id}"] .desktop-pane-header`)!;
    const divider = (id: string) => document.querySelector<HTMLElement>(`.desktop-split-divider[data-split-id="row:pane-${id}"]`)!;
    const chipCentre = (id: string) => ({ clientX: chipKeys().indexOf(id) * CHIP_STEP + (CHIP_STEP - 4) / 2, clientY: 15 });
    const SLOT_WIDTH = PANES_WIDTH / 3;
    const tilePoint = (slot: number, x: number, y = 0.5) => ({ clientX: (slot + x) * SLOT_WIDTH, clientY: 40 + y * (PANES_HEIGHT - 40) });
    const press = (element: HTMLElement, at: { clientX: number; clientY: number }) => fireEvent.pointerDown(element, { button: 0, pointerId: 1, ...at });
    const moveTo = (at: { clientX: number; clientY: number }) => fireEvent.pointerMove(window, { pointerId: 1, ...at });
    const release = (at: { clientX: number; clientY: number }) => fireEvent.pointerUp(window, { pointerId: 1, ...at });
    const drag = (daemon: ScriptedDaemon, element: HTMLElement, from: { clientX: number; clientY: number }, to: { clientX: number; clientY: number }) =>
      gesture(daemon, () => {
        press(element, from);
        moveTo(to);
        release(to);
      });
    const dragDivider = (daemon: ScriptedDaemon, id: string, distance: number) => {
      const from = { clientX: SLOT_WIDTH, clientY: 300 };
      return drag(daemon, divider(id), from, { clientX: from.clientX + distance, clientY: 300 });
    };
    const paneWidth = (id: string) => parseFloat(document.querySelector<HTMLElement>(`[data-pane-id="pane-${id}"]`)!.style.width);

    it('swaps a chip dropped on a tile’s centre into that slot, and takes focus', async () => {
      const daemon = await renderRow(NARROWEST_66);

      await drag(daemon, chip('s7'), chipCentre('s7'), tilePoint(1, 0.5));

      expect(moves(daemon)).toEqual([
        expect.objectContaining({ leaf_id: 'pane-s7', anchor_id: 'pane-s2', edge: 'left', whole_column: true }),
        expect.objectContaining({ leaf_id: 'pane-s2', anchor_id: 'pane-s6', edge: 'right', whole_column: true, activate: false }),
      ]);
      expect(rowOf(daemon)).toEqual(['s1', 's7', 's3', 's4', 's5', 's6', 's2', 's8', 's9', 's10', 's11']);
      expect(paneIds()).toEqual(onScreen('s1', 's7', 's3'));
      expect(active()).toBe('pane-s7');
      expect(chipKeys()).toContain('s2');
    });

    it('moves a chip dropped on a tile’s side edge beside it, as a column', async () => {
      const daemon = await renderRow(NARROWEST_66);

      await drag(daemon, chip('s7'), chipCentre('s7'), tilePoint(0, 0.95));

      expect(moves(daemon)).toEqual([expect.objectContaining({ source_desktop_id: 'ws', target_desktop_id: 'ws', leaf_id: 'pane-s7', anchor_id: 'pane-s1', edge: 'right', whole_column: true })]);
      expect(rowOf(daemon).slice(0, 3)).toEqual(['s1', 's7', 's2']);
      expect(active()).toBe('pane-s7');
    });

    it('stacks a chip dropped on a tile’s top or bottom edge', async () => {
      const daemon = await renderRow(NARROWEST_66);

      await drag(daemon, chip('s7'), chipCentre('s7'), tilePoint(1, 0.5, 0.97));

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s7', anchor_id: 'pane-s2', edge: 'bottom', whole_column: true })]);
    });

    describe('a stack chip on a tile’s top edge', () => {
      const stackOnTheShelf = (lowerShare: number) => rightChain([
        ...IDS.slice(0, 9).map(pane),
        { ...split('stack-s10', 'horizontal', [pane('s10'), pane('s11')]), ratio: lowerShare },
      ]);
      const nearTop = (panesHeight: number, y = 0.02) => ({ clientX: 1.5 * SLOT_WIDTH, clientY: 30 + panesHeight * y });

      it.each([
        ['an even stack', 0.5, 1200, 0.02, 640 / 1200],
        ['a stack with a short tile', 0.8, 2000, 0.02, 1600 / 2000],
        ['the band the pointer chose when it already fits', 0.5, 4000, 0.2, 0.2],
      ])('keeps every tile at the minimum height: %s', async (_name, lowerShare, panesHeight, y, share) => {
        const daemon = await renderRow(NARROWEST_66, { root: stackOnTheShelf(lowerShare), panesHeight });

        await drag(daemon, chip('s10'), chipCentre('s10'), nearTop(panesHeight, y));

        const [move] = moves(daemon);
        expect(move).toMatchObject({ leaf_id: 'pane-s10', anchor_id: 'pane-s2', edge: 'top', whole_column: true });
        expect(move.leaf_share).toBeCloseTo(share, 5);
      });

      it('moves the stack beside the tile and says why when the tile cannot hold it', async () => {
        const daemon = await renderRow(NARROWEST_66, { root: stackOnTheShelf(0.5) });

        await gesture(daemon, () => {
          press(chip('s10'), chipCentre('s10'));
          moveTo(nearTop(PANES_HEIGHT));
        });
        const preview = document.querySelector<HTMLElement>('.desktop-dock-target')!;
        expect(parseFloat(preview.style.height)).toBeCloseTo(100);
        expect(parseFloat(preview.style.left)).toBeCloseTo((1.5 * 100) / 3);
        await gesture(daemon, () => release(nearTop(PANES_HEIGHT)));

        expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s10', anchor_id: 'pane-s2', edge: 'right', whole_column: true })]);
        expect(rowOf(daemon).slice(0, 4)).toEqual(['s1', 's2', 's10', 's11']);
        expect(screen.getByText('Stacking here needs 960px to keep every tile at the 320px minimum. This tile has 900px, so it moved beside instead.')).toBeInTheDocument();
      });
    });

    it('reorders chips without changing focus or the screen', async () => {
      const daemon = await renderRow(NARROWEST_66);
      const focused = daemon.sentOf('desktop_set_active_pane').length;

      const between = { clientX: chipKeys().indexOf('s5') * CHIP_STEP + 4, clientY: 15 };
      await drag(daemon, chip('s8'), chipCentre('s8'), between);

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s8', anchor_id: 'pane-s5', edge: 'left', whole_column: true, activate: false })]);
      expect(chipKeys()).toEqual(['s4', 's8', 's5', 's6', 's7', 's9', 's10', 's11']);
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(active()).toBe('pane-s1');
      expect(daemon.sentOf('desktop_set_active_pane')).toHaveLength(focused);
    });

    it('leaves the order alone when a chip is dropped on itself, and a dragged chip does not bring on release', async () => {
      const daemon = await renderRow(NARROWEST_66);
      const from = chipCentre('s8');

      await gesture(daemon, () => {
        press(chip('s8'), from);
        moveTo({ ...from, clientX: from.clientX + 10 });
        release({ ...from, clientX: from.clientX + 10 });
        fireEvent.click(chip('s8'), { clientX: from.clientX + 10, clientY: from.clientY });
      });

      expect(moves(daemon)).toEqual([]);
      expect(active()).toBe('pane-s1');
    });

    it('still brings a chip on a plain click', async () => {
      const daemon = await renderRow(NARROWEST_66);
      const at = chipCentre('s8');

      await gesture(daemon, () => {
        press(chip('s8'), at);
        release(at);
        fireEvent.click(chip('s8'), at);
      });

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s8', anchor_id: 'pane-s1', edge: 'right', whole_column: true })]);
    });

    const slideTo = async (daemon: ScriptedDaemon, ...keys: ('terminal.focusLeft' | 'terminal.focusRight')[]) => {
      for (const key of keys) await gesture(daemon, () => pressShortcut(key));
    };
    const anchorPoint = (testId: string, share: number) => {
      const at = Array.from(document.querySelectorAll('[data-shelf-anchor]')).indexOf(screen.getByTestId(testId));
      return { clientX: at * CHIP_STEP + share * (CHIP_STEP - 4), clientY: 15 };
    };
    const LEFT_HALF = 0.1;
    const RIGHT_HALF = 0.9;

    it('brings a tile dropped beside a chip on screen next to that chip, active and focused', async () => {
      const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: FOUR });
      await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight', 'terminal.focusRight');
      expect(paneIds()).toEqual(onScreen('s3', 's4'));

      await drag(daemon, header('s4'), { clientX: 1400, clientY: 60 }, anchorPoint('shelf-chip-pane-s1', RIGHT_HALF));

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s4', anchor_id: 'pane-s1', edge: 'right' })]);
      expect(rowOf(daemon)).toEqual(['s1', 's4', 's2', 's3']);
      expect(paneIds()).toEqual(onScreen('s1', 's4'));
      expect(active()).toBe('pane-s4');
      expect(terminalInput('s4')).toHaveFocus();
    });

    it.each([
      ['the active tile', 's1', 's3', LEFT_HALF],
      ['the active tile', 's1', 's3', RIGHT_HALF],
      ['the active tile', 's1', 's4', LEFT_HALF],
      ['the active tile', 's1', 's4', RIGHT_HALF],
      ['an inactive tile', 's2', 's3', LEFT_HALF],
      ['an inactive tile', 's2', 's3', RIGHT_HALF],
      ['an inactive tile', 's2', 's4', LEFT_HALF],
      ['an inactive tile', 's2', 's4', RIGHT_HALF],
    ])('shows %s %s dropped on %s at %s of its width beside that chip', async (_name, tile, chipId, share) => {
      const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: FOUR });
      expect(paneIds()).toEqual(onScreen('s1', 's2'));

      await drag(daemon, header(tile), { clientX: 400, clientY: 60 }, anchorPoint(`shelf-chip-pane-${chipId}`, share));

      const row = rowOf(daemon);
      expect(row.indexOf(tile) - row.indexOf(chipId)).toBe(share === LEFT_HALF ? -1 : 1);
      expect(paneIds()).toEqual(onScreen(...row.filter((id) => id === tile || id === chipId)));
      expect(active()).toBe(`pane-${tile}`);
    });

    it('shows the pair when the drop leaves the row order as it was', async () => {
      const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: FOUR });

      await drag(daemon, header('s2'), { clientX: 1400, clientY: 60 }, anchorPoint('shelf-chip-pane-s3', LEFT_HALF));

      expect(rowOf(daemon)).toEqual(['s1', 's2', 's3', 's4']);
      expect(paneIds()).toEqual(onScreen('s2', 's3'));
      expect(active()).toBe('pane-s2');
    });

    it('clears a saved width that keeps the pair apart, and shows the pair', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain([wanted('s1', 200), pane('s2'), pane('s3'), pane('s4')]) });
      await slideTo(daemon, 'terminal.focusRight');
      expect(paneIds()).toEqual(onScreen('s2', 's3', 's4'));

      await drag(daemon, header('s2'), { clientX: 400, clientY: 60 }, anchorPoint('shelf-chip-pane-s1', RIGHT_HALF));

      expect(rowOf(daemon)).toEqual(['s1', 's2', 's3', 's4']);
      expect(widths(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s1', cols: 0 })]);
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(active()).toBe('pane-s2');
      expect(terminalInput('s2')).toHaveFocus();
    });

    it('shows the dropped tile alone and says the numbers when two tiles cannot fit side by side', async () => {
      const daemon = await renderRow({ narrowest_tile_cols: '150' }, { root: FOUR });
      expect(paneIds()).toEqual(onScreen('s1'));

      await drag(daemon, header('s1'), { clientX: 400, clientY: 60 }, anchorPoint('shelf-chip-pane-s3', RIGHT_HALF));

      expect(rowOf(daemon)).toEqual(['s2', 's3', 's1', 's4']);
      expect(paneIds()).toEqual(onScreen('s1'));
      expect(active()).toBe('pane-s1');
      expect(screen.getByText(/^2 tiles side by side at the narrowest width \(150 cols, \d+px\) need \d+px\. Tiles have 2000px here, so the dropped tile shows alone\./)).toBeInTheDocument();
    });

    it('shows a tile dropped on a cap beside the row’s far column', async () => {
      const daemon = await renderRow(NARROWEST_66, { shelfWidth: 600 });

      await drag(daemon, header('s2'), { clientX: 1000, clientY: 60 }, anchorPoint('shelf-cap-after', 0.5));

      expect(rowOf(daemon).slice(-2)).toEqual(['s11', 's2']);
      expect(paneIds().slice(-2)).toEqual(onScreen('s11', 's2'));
      expect(active()).toBe('pane-s2');

    });

    it('shows a tile dropped on the leading cap beside the row’s first column', async () => {
      const daemon = await renderRow(NARROWEST_66, { shelfWidth: 600 });
      await slideTo(daemon, ...Array.from({ length: 10 }, () => 'terminal.focusRight' as const));
      expect(paneIds()).toEqual(onScreen('s9', 's10', 's11'));

      await drag(daemon, header('s10'), { clientX: 1000, clientY: 60 }, anchorPoint('shelf-cap-before', 0.5));

      expect(rowOf(daemon).slice(0, 2)).toEqual(['s10', 's1']);
      expect(paneIds().slice(0, 2)).toEqual(onScreen('s10', 's1'));
      expect(active()).toBe('pane-s10');
    });

    it('takes a stacked tile out of its stack and shows it beside the chip', async () => {
      const daemon = await renderRow({ narrowest_tile_cols: '86' }, { root: rightChain([pane('s1'), split('stack', 'horizontal', [pane('s2'), pane('s3')]), pane('s4')]) });

      await drag(daemon, header('s3'), { clientX: 1400, clientY: 600 }, anchorPoint('shelf-chip-pane-s4', RIGHT_HALF));

      expect(rowOf(daemon)).toEqual(['s1', 's2', 's4', 's3']);
      expect(paneIds()).toEqual(onScreen('s4', 's3'));
      expect(active()).toBe('pane-s3');
    });

    it('gives the tile back and names the failure when the daemon refuses a shelf drop', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });
      daemon.on('desktop_move_leaf', (command) => ({ event: 'profile_action_result', action: command.cmd, request_id: command.request_id, success: false, error: 'refused' }));

      await drag(daemon, header('s3'), { clientX: 1400, clientY: 60 }, chipCentre('s4'));

      expect(screen.getByText(/Could not move that tile: refused/)).toBeInTheDocument();
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    });

    it('moves a stack’s whole column when its chip is dropped on a sidebar desktop', async () => {
      const stack = split('stack', 'horizontal', [pane('s4'), pane('s5')]);
      const daemon = await renderRow(NARROWEST_66, { root: rightChain([pane('s1'), pane('s2'), pane('s3'), stack]), others: ['next'] });
      const target = document.querySelector<HTMLElement>('[data-desktop-id="desktop-next"] .sidebar-row-select')!;

      await gesture(daemon, () => {
        press(chip('s4'), chipCentre('s4'));
        moveTo({ clientX: 40, clientY: 400 });
        fireEvent.pointerEnter(target);
        fireEvent.pointerUp(target, { pointerId: 1, clientX: -40, clientY: 400 });
      });

      expect(moves(daemon)).toEqual([expect.objectContaining({ source_desktop_id: 'ws', target_desktop_id: 'desktop-next', leaf_id: 'pane-s4', whole_column: true })]);
    });

    it('moves a chip dropped on a sidebar desktop before the app has rendered the drag', async () => {
      const daemon = await renderRow(NARROWEST_66, { others: ['next'] });
      const target = document.querySelector<HTMLElement>('[data-desktop-id="desktop-next"] .sidebar-row-select')!;

      await gesture(daemon, () => {
        press(chip('s7'), chipCentre('s7'));
        act(() => {
          moveTo({ clientX: 40, clientY: 400 });
          fireEvent.pointerUp(target, { pointerId: 1, clientX: -40, clientY: 400 });
        });
      });

      expect(moves(daemon)).toEqual([expect.objectContaining({ target_desktop_id: 'desktop-next', leaf_id: 'pane-s7' })]);
    });

    it('cancels a chip or tile drag with Esc: no message, and the row stays', async () => {
      const daemon = await renderRow(NARROWEST_66);

      await gesture(daemon, () => {
        press(chip('s7'), chipCentre('s7'));
        moveTo(tilePoint(1, 0.5));
      });
      expect(document.querySelector('.desktop-dock-target')).not.toBeNull();
      await gesture(daemon, () => {
        fireEvent.keyDown(window, { key: 'Escape' });
        release(tilePoint(1, 0.5));
      });
      expect(document.querySelector('.desktop-dock-target')).toBeNull();

      await gesture(daemon, () => {
        press(header('s2'), { clientX: 700, clientY: 60 });
        moveTo(chipCentre('s7'));
      });
      expect(chip('s7').className).toMatch(/is-drop-/);
      await gesture(daemon, () => {
        fireEvent.keyDown(window, { key: 'Escape' });
        release(chipCentre('s7'));
      });
      expect(chip('s7').className).not.toMatch(/is-drop-/);

      expect(moves(daemon)).toEqual([]);
      expect(rowOf(daemon)).toEqual(IDS);
    });

    it('docks only against tiles, not the desktop’s outer frame', async () => {
      const daemon = await renderRow(NARROWEST_66);

      await drag(daemon, header('s2'), { clientX: 700, clientY: 60 }, { clientX: 5, clientY: 450 });

      expect(moves(daemon)).toEqual([expect.objectContaining({ leaf_id: 'pane-s2', anchor_id: 'pane-s1', edge: 'left' })]);
    });

    it('resizes neighbours by dragging a column divider, and keeps the width pending until the daemon’s echo', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });
      const held: CommandMessage<'desktop_set_wanted_cols'>[] = [];
      daemon.on('desktop_set_wanted_cols', (command) => { held.push(command); return undefined; });
      const before = paneWidth('s1');

      await dragDivider(daemon, 's1', 45);

      expect(held.map((command) => command.leaf_id)).toEqual(['pane-s1', 'pane-s2']);
      expect(held.every((command) => command.desktop_id === 'ws' && command.cols > 0)).toBe(true);
      const dragged = paneWidth('s1');
      expect(dragged).toBeGreaterThan(before);
      expect(paneWidth('s3')).toBeCloseTo(before, 0);
    });

    it('moves only the dragged divider when every wanted width is scaled to fit', async () => {
      const root = rightChain([{ ...pane('s1'), wanted_cols: 80 }, { ...pane('s2'), wanted_cols: 75 }, { ...pane('s3'), wanted_cols: 70 }]);
      const daemon = await renderRow(NARROWEST_66, { root });
      const [s1, s2, s3] = ['s1', 's2', 's3'].map(paneWidth);
      expect(s1 + s2 + s3).toBeCloseTo(100);
      expect(s1 / s3).toBeCloseTo(80 / 70);

      await dragDivider(daemon, 's1', 100);

      const pointerPercent = (100 / PANES_WIDTH) * 100;
      const oneColumnPercent = 0.6;
      expect(Math.abs(paneWidth('s1') - s1 - pointerPercent)).toBeLessThan(oneColumnPercent);
      expect(Math.abs(paneWidth('s3') - s3)).toBeLessThan(oneColumnPercent);
      expect(widths(daemon).map((command) => command.leaf_id)).toEqual(['pane-s1', 'pane-s2', 'pane-s3']);
    });

    it('saves a dragged width and keeps it after the echo', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });
      const before = paneWidth('s1');

      await dragDivider(daemon, 's1', 45);
      const dragged = paneWidth('s1');

      expect(widths(daemon).map((command) => command.leaf_id)).toEqual(['pane-s1', 'pane-s2']);
      expect(dragged).toBeGreaterThan(before);
      expect(JSON.parse(daemon.arrangement.desktop('ws')!.tree_json).children[0].wanted_cols).toBe(widths(daemon)[0].cols);
    });

    it('sends the far column to the shelf past the narrowest, and brings it back when dragged back, in one drag', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      expect(screen.queryByTestId('desktop-shelf')).toBeNull();
      const from = { clientX: SLOT_WIDTH, clientY: 300 };

      await gesture(daemon, () => {
        press(divider('s1'), from);
        moveTo({ clientX: from.clientX + 400, clientY: 300 });
      });
      expect(paneIds()).toEqual(onScreen('s1', 's2'));
      expect(chipKeys()).toEqual(['s3']);
      expect(widths(daemon)).toEqual([]);

      await gesture(daemon, () => {
        moveTo(from);
        release(from);
      });
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(widths(daemon)).toEqual([]);
      expect(screen.queryByTestId('desktop-shelf')).toBeNull();
    });

    it('stops the drag rather than shelve the active column', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      await gesture(daemon, () => pressShortcut('terminal.focusRight'));
      await gesture(daemon, () => pressShortcut('terminal.focusRight'));
      expect(active()).toBe('pane-s3');

      await dragDivider(daemon, 's1', 1500);

      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(active()).toBe('pane-s3');
    });

    it('saves the area with a drag past the narrowest, which holds there and gives way below it', async () => {
      const resize = observedResizes();
      const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 2).map(pane)) });
      const resizeTiles = (width: number) => gesture(daemon, () => resize('.session-terminal-panes', { width, height: PANES_HEIGHT }));
      await resizeTiles(PANES_WIDTH);

      await dragDivider(daemon, 's1', 1500);

      expect(paneIds()).toEqual(onScreen('s1'));
      const [saved] = widths(daemon);
      expect(saved).toEqual(expect.objectContaining({ leaf_id: 'pane-s1', area_cols: saved.cols }));

      const oneColumnLess = Math.floor((PANES_WIDTH * (saved.area_cols - 1)) / saved.area_cols);
      await resizeTiles(oneColumnLess);
      expect(paneIds()).toEqual(onScreen('s1', 's2'));
      expect(widths(daemon)).toHaveLength(1);

      await resizeTiles(PANES_WIDTH);
      expect(paneIds()).toEqual(onScreen('s1'));
    });

    it('still shelves the far column on a drag past the narrowest at a smaller area', async () => {
      const resize = observedResizes();
      const daemon = await renderRow(NARROWEST_66, { root: rightChain([wantedAt('s1', FILLS_THE_AREA, WIDER_THAN_THE_AREA), pane('s2')]) });
      const resizeTiles = (width: number) => gesture(daemon, () => resize('.session-terminal-panes', { width, height: PANES_HEIGHT }));
      await resizeTiles(PANES_WIDTH);
      expect(paneIds()).toEqual(onScreen('s1', 's2'));

      await dragDivider(daemon, 's1', 1500);

      expect(paneIds()).toEqual(onScreen('s1'));
      expect(widths(daemon)[0].area_cols).toBeLessThan(WIDER_THAN_THE_AREA);

      await resizeTiles(ROOM_FOR_THAT_WIDTH);
      await resizeTiles(PANES_WIDTH);
      expect(paneIds()).toEqual(onScreen('s1'));
    });

    it('cancels a divider drag with Esc and restores the screen', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      const before = paneWidth('s1');
      const from = { clientX: SLOT_WIDTH, clientY: 300 };

      await gesture(daemon, () => {
        press(divider('s1'), from);
        moveTo({ clientX: from.clientX + 400, clientY: 300 });
      });
      expect(paneIds()).toEqual(onScreen('s1', 's2'));

      await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
      expect(paneWidth('s1')).toBeCloseTo(before);
      expect(document.querySelector('.desktop-resize-shield')).toBeNull();

      await gesture(daemon, () => release({ clientX: from.clientX + 400, clientY: 300 }));
      expect(widths(daemon)).toEqual([]);
      expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    });

    it('returns the old width and names the failure when the daemon refuses', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });
      daemon.on('desktop_set_wanted_cols', (command) => ({ event: 'profile_action_result', action: command.cmd, request_id: command.request_id, success: false, error: 'refused' }));
      const before = paneWidth('s1');

      await dragDivider(daemon, 's1', 45);

      expect(screen.getByText(/Could not save that width: refused/)).toBeInTheDocument();
      expect(paneWidth('s1')).toBeCloseTo(before);
    });

    it('clears the wanted widths on both sides with a double-click, and on the whole desktop from the palette', async () => {
      const daemon = await renderRow(NARROWEST_66, { root: FOUR });
      const before = paneWidth('s1');
      await dragDivider(daemon, 's1', 45);
      const sent = widths(daemon).length;

      await gesture(daemon, () => fireEvent.dblClick(divider('s1')));
      expect(widths(daemon).slice(sent).map((command) => [command.leaf_id, command.cols])).toEqual([['pane-s1', 0], ['pane-s2', 0]]);
      expect(paneWidth('s1')).toBeCloseTo(before);

      await dragDivider(daemon, 's2', 45);
      const again = widths(daemon).length;
      await gesture(daemon, () => pressShortcut('ui.commandPalette'));
      await gesture(daemon, () => fireEvent.mouseDown(screen.getByRole('option', { name: /Clear dragged widths/ })));
      expect(widths(daemon).slice(again).map((command) => command.cols)).toEqual([0, 0]);
      expect(paneWidth('s1')).toBeCloseTo(before);

      await gesture(daemon, () => pressShortcut('ui.commandPalette'));
      await gesture(daemon, () => fireEvent.mouseDown(screen.getByRole('option', { name: /Clear dragged widths/ })));
      expect(screen.getByText('This desktop has no dragged widths.')).toBeInTheDocument();
    });

    it('moves a chip beside a tile, without replacing it, after hovering over to another desktop', async () => {
      const daemon = await renderRow(NARROWEST_66, { others: ['next'] });
      const target = document.querySelector<HTMLElement>('[data-desktop-id="desktop-next"] .sidebar-row-select')!;

      await gesture(daemon, () => {
        press(chip('s7'), chipCentre('s7'));
        moveTo({ clientX: -40, clientY: 400 });
        fireEvent.pointerEnter(target);
      });
      await act(() => vi.advanceTimersByTimeAsync(320));
      await daemon.idle();
      fireEvent.pointerLeave(target);
      const centre = { clientX: PANES_WIDTH / 2, clientY: 30 + PANES_HEIGHT / 2 };
      await gesture(daemon, () => {
        moveTo(centre);
        release(centre);
      });

      expect(moves(daemon)).toEqual([expect.objectContaining({ source_desktop_id: 'ws', target_desktop_id: 'desktop-next', leaf_id: 'pane-s7', anchor_id: 'pane-next', whole_column: true })]);
    });
  });
});
