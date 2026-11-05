import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { laidOutDesktop, pane, split } from './test/desktopLayouts';
import { fakeRects, sizeTerminals } from './test/layout';
import { serveSettings } from './test/settings';
import { gesture, renderApp } from './test/renderApp';

const PANES_WIDTH = 2000;
const PANES_HEIGHT = 900;
const IDS = Array.from({ length: 6 }, (_, index) => `s${index + 1}`);
const SHELF_ON = { shelf_layout_enabled: 'true', narrowest_tile_cols: '66' };
const LAYOUT_COMMANDS = ['desktop_set_active_pane', 'desktop_show_leaf', 'desktop_move_leaf', 'desktop_set_split_ratio', 'desktop_set_wanted_cols', 'pty_resize'];

function rightChain(nodes: unknown[], depth = 0): unknown {
  const [first, ...rest] = nodes;
  return rest.length === 0 ? first : split(`split-${depth}`, 'vertical', [first, rightChain(rest, depth + 1)]);
}

async function renderRow(settings: Record<string, string>, { screens = (id: string) => `screen of ${id}\n\n`, tiles = [] as Record<string, string>[] } = {}) {
  sizeTerminals((terminal) => ({
    clientWidth: (PANES_WIDTH * parseFloat(terminal.closest<HTMLElement>('[data-pane-id]')?.style.width || '100')) / 100,
    clientHeight: PANES_HEIGHT,
  }));
  fakeRects((element) => {
    if (element.classList.contains('session-terminal-panes')) return new DOMRect(0, 0, PANES_WIDTH, PANES_HEIGHT);
    if (element.classList.contains('desktop-shelf')) return new DOMRect(0, 0, PANES_WIDTH, 30);
    return null;
  });
  const root = rightChain([...IDS.map(pane), ...tiles.map((tile) => ({ type: 'tile', ...tile }))]);
  const desktop = { ...laidOutDesktop(root, IDS), active_pane_id: 'pane-s1' };
  const { daemon } = await renderApp({
    initialState: {
      settings,
      sessions: IDS.map((id) => daemonSession(id, { state: 'waiting_input' })),
      desktops: [desktop, soloDesktop('other')],
    },
    script: (scripted) => {
      serveSettings(scripted, settings);
      scripted.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
      scripted.on('terminal_read_text', ({ terminal_id }) => ({ event: 'terminal_read_text_result', success: true, text: screens(terminal_id) }));
    },
  });
  await openSession(daemon, 's1');
  await daemon.idle();
  return daemon;
}

const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));
const chip = (id: string) => screen.getByTestId(`shelf-chip-pane-${id}`);
const paneIds = () => Array.from(document.querySelectorAll<HTMLElement>('.desktop-pane[data-pane-id]')).map((element) => element.dataset.paneId);
const layoutTraffic = (daemon: Awaited<ReturnType<typeof renderRow>>) => daemon.sent.filter((command) => LAYOUT_COMMANDS.includes(command.cmd));

async function peekAt(daemon: Awaited<ReturnType<typeof renderRow>>, id: string) {
  fireEvent.pointerEnter(chip(id));
  await advance(180);
  await daemon.idle();
  return screen.getByTestId('shelf-peek');
}

describe('Shelf peek', () => {
  it('opens after a short hover with the latest text, without attaching or changing the layout', async () => {
    const daemon = await renderRow(SHELF_ON);
    const layoutBefore = layoutTraffic(daemon).length;
    const shownBefore = paneIds();

    fireEvent.pointerEnter(chip('s5'));
    await advance(179);
    expect(screen.queryByTestId('shelf-peek')).toBeNull();
    expect(daemon.sentOf('terminal_read_text')).toEqual([]);

    await advance(1);
    await daemon.idle();
    expect(screen.getByTestId('shelf-peek-screen').textContent).toBe('screen of s5');
    expect(daemon.sentOf('terminal_read_text')).toEqual([expect.objectContaining({ terminal_id: 's5', mode: 'screen' })]);
    expect(daemon.sentOf('attach_session').map(({ id }) => id)).not.toContain('s5');
    expect(layoutTraffic(daemon)).toHaveLength(layoutBefore);
    expect(paneIds()).toEqual(shownBefore);
  });

  it('refreshes the text while open and reads nothing once closed', async () => {
    let reads = 0;
    const daemon = await renderRow(SHELF_ON, { screens: () => `read ${++reads}` });
    await peekAt(daemon, 's5');
    expect(screen.getByTestId('shelf-peek-screen').textContent).toBe('read 1');

    await advance(1000);
    await daemon.idle();
    expect(screen.getByTestId('shelf-peek-screen').textContent).toBe('read 2');

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('shelf-peek')).toBeNull();
    const readsWhenClosed = daemon.sentOf('terminal_read_text').length;
    await advance(5000);
    await daemon.idle();
    expect(daemon.sentOf('terminal_read_text')).toHaveLength(readsWhenClosed);
  });

  it('sends a reply to that terminal and changes nothing on screen', async () => {
    const daemon = await renderRow(SHELF_ON);
    const layoutBefore = layoutTraffic(daemon).length;
    const shownBefore = paneIds();
    await peekAt(daemon, 's5');
    const input = screen.getByTestId('shelf-peek-input');

    fireEvent.change(input, { target: { value: 'ship it' } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

    expect(daemon.sentOf('pty_input').filter(({ id }) => id === 's5').map(({ data }) => data)).toEqual(['ship it', '\r']);
    expect(screen.queryByTestId('shelf-peek')).toBeNull();
    expect(paneIds()).toEqual(shownBefore);
    expect(layoutTraffic(daemon)).toHaveLength(layoutBefore);
    expect(document.querySelector('[data-session-terminal-desktop="ws"]')).toHaveAttribute('data-active-leaf-id', 'pane-s1');
  });

  it('opens the tile on Enter with an empty reply', async () => {
    const daemon = await renderRow(SHELF_ON);
    await peekAt(daemon, 's5');

    await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('shelf-peek-input'), { key: 'Enter' }));

    expect(screen.queryByTestId('shelf-peek')).toBeNull();
    expect(document.querySelector('[data-session-terminal-desktop="ws"]')).toHaveAttribute('data-active-leaf-id', 'pane-s5');
    expect(paneIds()).toContain('pane-s5');
    expect(daemon.sentOf('pty_input').filter(({ id }) => id === 's5')).toEqual([]);
  });

  it('closes on Esc, on a click outside, and after the pointer leaves', async () => {
    const daemon = await renderRow(SHELF_ON);
    const shownBefore = paneIds();

    await peekAt(daemon, 's5');
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('shelf-peek')).toBeNull();

    await peekAt(daemon, 's5');
    fireEvent.pointerDown(document.body);
    expect(screen.queryByTestId('shelf-peek')).toBeNull();

    await peekAt(daemon, 's5');
    fireEvent.pointerLeave(chip('s5'));
    await advance(159);
    expect(screen.getByTestId('shelf-peek')).toBeInTheDocument();
    fireEvent.pointerEnter(screen.getByTestId('shelf-peek'));
    await advance(1000);
    await daemon.idle();
    expect(screen.getByTestId('shelf-peek')).toBeInTheDocument();
    fireEvent.pointerLeave(screen.getByTestId('shelf-peek'));
    await advance(160);
    expect(screen.queryByTestId('shelf-peek')).toBeNull();
    expect(paneIds()).toEqual(shownBefore);
  });

  it('shows only the title and kind for a document chip', async () => {
    const daemon = await renderRow(SHELF_ON, { tiles: [{ tile_id: 'tile-doc', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }] });

    fireEvent.pointerEnter(screen.getByTestId('shelf-chip-tile-doc'));
    await advance(180);
    await daemon.idle();

    expect(screen.getByTestId('shelf-peek')).toHaveTextContent('notes.md');
    expect(screen.getByTestId('shelf-peek-kind')).toBeInTheDocument();
    expect(screen.queryByTestId('shelf-peek-input')).toBeNull();
    expect(daemon.sentOf('terminal_read_text')).toEqual([]);
  });

  it('opens from the sidebar row of an off-screen tile, and not from one on screen', async () => {
    const daemon = await renderRow(SHELF_ON);

    fireEvent.pointerEnter(screen.getByTestId('sidebar-session-s1'));
    await advance(180);
    expect(screen.queryByTestId('shelf-peek')).toBeNull();

    fireEvent.pointerEnter(screen.getByTestId('sidebar-session-s5'));
    await advance(180);
    await daemon.idle();
    expect(screen.getByTestId('shelf-peek')).toHaveAttribute('data-leaf-id', 'pane-s5');
  });

  it('says why when the text cannot be read', async () => {
    const daemon = await renderRow(SHELF_ON);
    daemon.on('terminal_read_text', () => ({ event: 'terminal_read_text_result', success: false, error: 'host unreachable' }));

    await peekAt(daemon, 's5');

    expect(screen.getByTestId('shelf-peek-screen')).toHaveTextContent('Could not read this terminal: host unreachable');
  });

  it('with the setting off, sidebar rows open nothing', async () => {
    const daemon = await renderRow({});

    fireEvent.pointerEnter(screen.getByTestId('sidebar-session-s5'));
    await advance(1000);
    await daemon.idle();

    expect(screen.queryByTestId('shelf-peek')).toBeNull();
    expect(daemon.sentOf('terminal_read_text')).toEqual([]);
  });
});
