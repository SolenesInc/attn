import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { laidOutDesktop, pane, split } from './test/desktopLayouts';
import { fakeRects, sizeTerminals } from './test/layout';
import { serveSettings } from './test/settings';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

const PANES_WIDTH = 2000;
const PANES_HEIGHT = 900;
const IDS = Array.from({ length: 11 }, (_, index) => `s${index + 1}`);

function rightChain(nodes: unknown[], depth = 0): unknown {
  const [first, ...rest] = nodes;
  return rest.length === 0 ? first : split(`split-${depth}`, 'vertical', [first, rightChain(rest, depth + 1)]);
}

const ELEVEN_TILES = rightChain(IDS.map(pane));

async function renderRow(settings: Record<string, string>, { root = ELEVEN_TILES, others = [] as string[], shelfWidth = PANES_WIDTH } = {}) {
  sizeTerminals((terminal) => ({
    clientWidth: (PANES_WIDTH * parseFloat(terminal.closest<HTMLElement>('[data-pane-id]')?.style.width || '100')) / 100,
    clientHeight: PANES_HEIGHT,
  }));
  fakeRects((element) => {
    if (element.classList.contains('session-terminal-panes')) return new DOMRect(0, 0, PANES_WIDTH, PANES_HEIGHT);
    if (element.classList.contains('desktop-shelf')) return new DOMRect(0, 0, shelfWidth, 30);
    return null;
  });
  const { daemon } = await renderApp({
    initialState: {
      settings,
      sessions: [...IDS, ...others].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: [{ ...laidOutDesktop(root, IDS), active_pane_id: 'pane-s1' }, ...others.map((id) => soloDesktop(id))],
    },
    script: (scripted) => {
      serveSettings(scripted, settings);
      scripted.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    },
  });
  await openSession(daemon, 's1');
  await daemon.idle();
  return daemon;
}

const SHELF_ON = { shelf_layout_enabled: 'true', narrowest_tile_cols: '66' };

const paneIds = () => Array.from(document.querySelectorAll<HTMLElement>('.desktop-pane[data-pane-id]')).map((element) => element.dataset.paneId);
const onScreen = (...ids: string[]) => ids.map((id) => `pane-${id}`);
const chipKeys = () =>
  Array.from(document.querySelectorAll<HTMLElement>('[data-testid^="shelf-chip-"]')).map((element) => element.dataset.testid!.replace('shelf-chip-pane-', ''));
const surface = () => document.querySelector<HTMLElement>('[data-session-terminal-desktop="ws"]')!;
const mountedTerminals = () =>
  Array.from(document.querySelectorAll('[data-pane-id] [aria-label="Terminal input"]')).map((input) => input.closest<HTMLElement>('[data-pane-id]')!.dataset.paneId);

describe('Shelf layout', () => {
  it('shows the screen and the shelf instead of slivers, and unmounts off-screen terminals', async () => {
    const daemon = await renderRow(SHELF_ON);

    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    expect(document.querySelector('[data-pane-suspended]')).toBeNull();
    expect(document.querySelector('[data-suspension-animating]')).toBeNull();
    expect(mountedTerminals()).toEqual(onScreen('s1', 's2', 's3'));
    expect(chipKeys()).toEqual(['s4', 's5', 's6', 's7', 's8', 's9', 's10', 's11']);
    expect(screen.getByTestId('shelf-live-dots').querySelectorAll('.desktop-shelf-dot')).toHaveLength(3);
    expect(document.querySelector<HTMLElement>('[data-pane-id="pane-s1"]')!.style.width).toBe('33.33333333333333%');
    await daemon.idle();
  });

  it('shows no shelf when every column fits', async () => {
    await renderRow(SHELF_ON, { root: rightChain(IDS.slice(0, 3).map(pane)) });
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
    expect(screen.queryByTestId('desktop-shelf')).toBeNull();
  });

  it('slides along the row with the move keys, and goes to the next desktop past the end', async () => {
    const daemon = await renderRow(SHELF_ON, { root: rightChain(IDS.slice(0, 4).map(pane)), others: ['next'] });

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
    const daemon = await renderRow(SHELF_ON, { root: rightChain(IDS.slice(0, 4).map(pane)) });
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
    const daemon = await renderRow(SHELF_ON, { root: rightChain([pane('s1'), stack, pane('s4')]) });

    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s2');
    await gesture(daemon, () => pressShortcut('terminal.focusDown'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s3');
    await gesture(daemon, () => pressShortcut('terminal.focusLeft'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s1');
    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s3');
  });

  it('slides to a chip when it is clicked, without moving it in the row', async () => {
    const daemon = await renderRow(SHELF_ON);

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('shelf-chip-pane-s7')));

    expect(surface()).toHaveAttribute('data-active-leaf-id', 'pane-s7');
    expect(paneIds()).toEqual(onScreen('s5', 's6', 's7'));
    expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
  });

  it('puts chips that do not fit behind caps with their menus', async () => {
    const daemon = await renderRow(SHELF_ON, { shelfWidth: 500 });

    expect(chipKeys()).toEqual(['s4', 's5']);
    expect(screen.getByTestId('shelf-cap-after')).toHaveTextContent('6 ›');
    fireEvent.click(screen.getByTestId('shelf-cap-after'));
    expect(chipKeys()).toEqual(['s4', 's5', 's6', 's7', 's8', 's9', 's10', 's11']);

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('shelf-chip-pane-s10')));
    expect(paneIds()).toEqual(onScreen('s8', 's9', 's10'));
    expect(screen.getByTestId('shelf-cap-before')).toHaveTextContent('‹');
    expect(screen.queryByTestId('shelf-menu-after')).toBeNull();
  });

  it('steps the narrowest width with the keys until one fewer or one more tile fits', async () => {
    const daemon = await renderRow(SHELF_ON);

    await gesture(daemon, () => pressShortcut('shelf.fewerTiles'));
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '86' });
    expect(screen.getByTestId('shelf-width-label')).toHaveTextContent('2 tiles');
    expect(paneIds()).toEqual(onScreen('s1', 's2'));

    await gesture(daemon, () => pressShortcut('shelf.moreTiles'));
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '76' });
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));

    await gesture(daemon, () => pressShortcut('shelf.moreTiles'));
    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '57' });
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3', 's4'));
  });

  it('says the numbers when no narrower step can fit another tile', async () => {
    const daemon = await renderRow({ ...SHELF_ON, narrowest_tile_cols: '57' });
    expect(paneIds()).toEqual(onScreen('s1', 's2', 's3', 's4'));

    await gesture(daemon, () => pressShortcut('shelf.moreTiles'));

    expect(screen.getByText('5 tiles at the narrowest step (57 cols, 480px) need 2400px. Tiles have 2000px here.')).toBeInTheDocument();
    expect(daemon.sentOf('set_setting')).toEqual([]);
  });

  it('offers the width steps in the palette while the setting is on', async () => {
    const daemon = await renderRow(SHELF_ON);
    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    await gesture(daemon, () => fireEvent.change(screen.getByRole('combobox'), { target: { value: '>wider tiles' } }));

    await gesture(daemon, () => fireEvent.mouseDown(screen.getByRole('option', { name: /Fewer, wider tiles/ })));

    expect(daemon.sentOf('set_setting').slice(-1)[0]).toMatchObject({ key: 'narrowest_tile_cols', value: '86' });
  });

  it('with the setting off shows slivers, and the width keys do nothing', async () => {
    const daemon = await renderRow({});

    expect(screen.queryByTestId('desktop-shelf')).toBeNull();
    expect(document.querySelectorAll('[data-pane-suspended]').length).toBeGreaterThan(0);
    expect(paneIds()).toHaveLength(11);

    await gesture(daemon, () => pressShortcut('shelf.fewerTiles'));
    await gesture(daemon, () => pressShortcut('shelf.moreTiles'));
    expect(daemon.sentOf('set_setting')).toEqual([]);

    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    await gesture(daemon, () => fireEvent.change(screen.getByRole('combobox'), { target: { value: '>wider tiles' } }));
    expect(screen.queryByRole('option', { name: /Fewer, wider tiles/ })).toBeNull();
  });
});
