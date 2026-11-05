import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSession } from './test/appFixtures';
import { agentPane, daemonSession, soloDesktop } from './test/daemonFixtures';
import { laidOutDesktop, pane, relayOut, split } from './test/desktopLayouts';
import { fakeRects, sizeTerminals } from './test/layout';
import { serveSettings } from './test/settings';
import type { CommandMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';
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
  await openSession(daemon, 's1');
  await daemon.idle();
  return daemon;
}

const SHELF_ON = { shelf_layout_enabled: 'true', narrowest_tile_cols: '66' };

const paneIds = () => Array.from(document.querySelectorAll<HTMLElement>('.desktop-pane[data-pane-id]')).map((element) => element.dataset.paneId);
const onScreen = (...ids: string[]) => ids.map((id) => `pane-${id}`);
const chipKeys = () =>
  Array.from(document.querySelectorAll<HTMLElement>('[data-testid^="shelf-chip-"]')).map((element) => element.dataset.testid!.replace('shelf-chip-pane-', ''));
const terminalInput = (id: string) => document.querySelector<HTMLElement>(`[data-pane-id="pane-${id}"] [aria-label="Terminal input"]`);
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

  it('puts chips that do not fit behind caps with their menus', async () => {
    const daemon = await renderRow(SHELF_ON, { shelfWidth: 500 });

    expect(chipKeys()).toEqual(['s4', 's5']);
    expect(screen.getByTestId('shelf-cap-after')).toHaveTextContent('6 ›');
    fireEvent.click(screen.getByTestId('shelf-cap-after'));
    expect(chipKeys()).toEqual(['s4', 's5', 's6', 's7', 's8', 's9', 's10', 's11']);

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('shelf-chip-pane-s10')));
    expect(paneIds()).toEqual(onScreen('s1', 's10', 's2'));
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
      const daemon = await renderRow(SHELF_ON, { root: FOUR });

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
      const daemon = await renderRow(SHELF_ON, { root: rightChain([stack, pane('s3')]) });

      await gesture(daemon, () => pressShortcut('shelf.carryLeft'));
      expect(screen.getByText('This tile is already at the start of the row.')).toBeInTheDocument();
      expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);

      await gesture(daemon, () => pressShortcut('shelf.carryRight'));
      expect(rowOf(daemon)).toEqual(['s3', 's1', 's2']);
      expect(active()).toBe('pane-s1');
    });

    it('brings a chip beside the active tile, and keeps the anchor on screen', async () => {
      const daemon = await renderRow(SHELF_ON);
      await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight');
      expect(active()).toBe('pane-s3');
      const shows = daemon.sentOf('desktop_show_leaf').length;

      await gesture(daemon, () => fireEvent.click(chip('s7')));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s7', anchor_id: 'pane-s3', edge: 'right', whole_column: true });
      expect(lastMove(daemon).activate).toBeUndefined();
      expect(daemon.sentOf('desktop_show_leaf')).toHaveLength(shows);
      expect(active()).toBe('pane-s7');
      expect(paneIds()).toEqual(onScreen('s2', 's3', 's7'));
      expect(rowOf(daemon).slice(0, 4)).toEqual(['s1', 's2', 's3', 's7']);
      expect(chip('s1')).toHaveClass('is-arrival');
    });

    it('brings an off-screen tile from the sidebar and the agent palette', async () => {
      const daemon = await renderRow(SHELF_ON);
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
      const daemon = await renderRow(SHELF_ON, { root: FOUR });
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
      const daemon = await renderRow(SHELF_ON);
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
      const daemon = await renderRow(SHELF_ON);
      await slideTo(daemon, 'terminal.focusRight');

      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s2', anchor_id: 'pane-s11', edge: 'right', whole_column: true, activate: false });
      expect(active()).toBe('pane-s3');
      expect(paneIds()).toEqual(onScreen('s1', 's3', 's4'));
      expect(rowOf(daemon).slice(-1)).toEqual(['s2']);
      expect(chip('s2')).toHaveClass('is-arrival');
    });

    it('sends a tile to the row start when nothing lies past the screen', async () => {
      const daemon = await renderRow(SHELF_ON, { root: FOUR });
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
      const daemon = await renderRow(SHELF_ON, { root: rightChain([stack, pane('s3'), pane('s4'), pane('s5')]) });

      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s1', anchor_id: 'pane-s5', edge: 'right', activate: false });
      expect(lastMove(daemon).whole_column).toBeUndefined();
      expect(active()).toBe('pane-s2');
      expect(rowOf(daemon)).toEqual(['s2', 's3', 's4', 's5', 's1']);
      expect(paneIds()).toEqual(onScreen('s2', 's3', 's4'));
      expect(chip('s1')).toHaveClass('is-arrival');
    });

    it('holds a column that would have fit, skips it when sliding, and gives it back when brought', async () => {
      const daemon = await renderRow(SHELF_ON, { root: rightChain(IDS.slice(0, 3).map(pane)) });
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
      const daemon = await renderRow(SHELF_ON, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      await slideTo(daemon, 'terminal.focusRight');
      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));
      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
      expect(active()).toBe('pane-s1');
      expect(paneIds()).toEqual(onScreen('s1'));
      expect(chipKeys()).toEqual(['s2', 's3']);
    });

    it('steps to the next tile when the screen shows only one', async () => {
      const daemon = await renderRow({ ...SHELF_ON, narrowest_tile_cols: '150' }, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      expect(paneIds()).toEqual(onScreen('s1'));

      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));

      expect(lastMove(daemon)).toMatchObject({ leaf_id: 'pane-s1', anchor_id: 'pane-s3', edge: 'right', activate: false });
      expect(active()).toBe('pane-s2');
      expect(paneIds()).toEqual(onScreen('s2'));
    });

    it('offers the moves in the palette and the shortcut help only while the setting is on', async () => {
      const daemon = await renderRow(SHELF_ON, { root: FOUR });
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
      const daemon = await renderRow(SHELF_ON, { root: pane('s1') });
      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));
      expect(screen.getByText('This is the only tile on screen.')).toBeInTheDocument();
    });

    it('opens beside the active column at capacity, keeping the anchor on screen', async () => {
      const daemon = await renderRow(SHELF_ON, { root: rightChain(IDS.slice(0, 3).map(pane)) });
      await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight');

      await gesture(daemon, () => pressShortcut('terminal.splitVertical'));

      const [spawn] = daemon.sentOf('spawn_session').slice(-1);
      expect(spawn.placement).toMatchObject({ direction: 'vertical', anchor_pane_id: 'pane-s3' });
      expect(active()).toBe(`pane-${spawn.id}`);
      expect(paneIds()).toEqual(['pane-s2', 'pane-s3', `pane-${spawn.id}`]);
      expect(chip('s1')).toHaveClass('is-arrival');
    });

    it('opens below in the same column until the height limit, then beside with the numbers', async () => {
      const daemon = await renderRow(SHELF_ON, { root: rightChain(IDS.slice(0, 2).map(pane)) });

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

      it('slides into view without taking focus when the active tile stays on screen, and closing puts the screen back', async () => {
        const daemon = await renderRow(SHELF_ON);
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
        const daemon = await renderRow({ ...SHELF_ON, narrowest_tile_cols: '86' }, { root: FOUR });
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

        expect(lastMove(daemon)).toMatchObject({ leaf_id: 'doc', anchor_id: 'pane-s3', whole_column: true });
        expect(active()).toBe('doc');
        expect(paneIds()).toEqual(['pane-s3', 'doc']);

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
        const daemon = await renderRow(SHELF_ON);
        await slideTo(daemon, 'terminal.focusRight', 'terminal.focusRight');
        await agentOpens(daemon, 's4');
        expect(active()).toBe('pane-s3');
        expect(paneIds()).toEqual(onScreen('s1', 's2', 's3'));
        expect(screen.getByTestId('shelf-chip-doc')).toHaveClass('is-needs');
      });

      it('a terminal an agent starts flashes its chip and moves nothing', async () => {
        const daemon = await renderRow(SHELF_ON);
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
      const daemon = await renderRow({ ...SHELF_ON, narrowest_tile_cols: '86' }, { root: FOUR });
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

    it('with the setting off the new keys do nothing', async () => {
      const daemon = await renderRow({});
      await gesture(daemon, () => pressShortcut('shelf.carryRight'));
      await gesture(daemon, () => pressShortcut('shelf.sendToShelf'));
      expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
      expect(paneIds()).toHaveLength(11);
    });
  });
});
