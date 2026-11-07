import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { laidOutDesktop, pane, split } from './test/desktopLayouts';
import { fakeRects, sizeTerminals } from './test/layout';
import { serveSettings } from './test/settings';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const PANES_WIDTH = 2000;
const PANES_HEIGHT = 900;
const IDS = Array.from({ length: 6 }, (_, index) => `s${index + 1}`);
const SHELF_ON = { shelf_layout_enabled: 'true', narrowest_tile_cols: '66' };
const LAYOUT_COMMANDS = ['desktop_set_active_pane', 'desktop_show_leaf', 'desktop_move_leaf', 'desktop_set_split_ratio', 'desktop_set_wanted_cols', 'pty_resize'];

function rightChain(nodes: unknown[], depth = 0): unknown {
  const [first, ...rest] = nodes;
  return rest.length === 0 ? first : split(`split-${depth}`, 'vertical', [first, rightChain(rest, depth + 1)]);
}

const OUTPOST = { id: 'outpost-1', name: 'studio', destination: 'studio:2200', state_since: '2026-10-18T18:00:00Z' };
const CONNECTED = { ...OUTPOST, state: 'connected', reach: 'reachable' } as const;
const CONNECTING = { ...OUTPOST, state: 'connecting', reach: 'connecting' } as const;

function answerInput(daemon: ScriptedDaemon, refuse: (data: string) => string | null = () => null) {
  daemon.on('pty_input', ({ id, data, probe_id }) => {
    if (!probe_id) return undefined;
    const error = refuse(data);
    return { event: 'pty_input_probe_result', id, probe_id, write_duration_us: 1, success: error === null, ...(error === null ? {} : { error }) };
  });
}

async function renderRow(settings: Record<string, string>, { screens = (id: string) => `screen of ${id}\n\n`, tiles = [] as Record<string, string>[], targetFields = {} as Record<string, unknown>, outposts = [] as (typeof CONNECTED | typeof CONNECTING)[] } = {}) {
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
      sessions: IDS.map((id) => daemonSession(id, { state: 'waiting_input', ...(id === 's5' ? targetFields : {}) })),
      outposts,
      desktops: [desktop, soloDesktop('other')],
    },
    script: (scripted) => {
      serveSettings(scripted, settings);
      scripted.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
      answerInput(scripted);
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
const repliesTo = (daemon: ScriptedDaemon, id: string) => daemon.sentOf('pty_input').filter((input) => input.id === id).map(({ data }) => data);
const layoutTraffic = (daemon: Awaited<ReturnType<typeof renderRow>>) => daemon.sent.filter((command) => LAYOUT_COMMANDS.includes(command.cmd));

function movePointerOnto(element: HTMLElement) {
  fireEvent.pointerEnter(element);
  fireEvent.pointerMove(element);
}

async function peekAt(daemon: Awaited<ReturnType<typeof renderRow>>, id: string) {
  movePointerOnto(chip(id));
  await advance(180);
  await daemon.idle();
  return screen.getByTestId('shelf-peek');
}

describe('Shelf peek', () => {
  it('opens after a short hover with the latest text, without attaching or changing the layout', async () => {
    const daemon = await renderRow(SHELF_ON);
    const layoutBefore = layoutTraffic(daemon).length;
    const shownBefore = paneIds();

    movePointerOnto(chip('s5'));
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

  it('opens nothing for a chip that arrives under a still pointer, until the pointer moves', async () => {
    const daemon = await renderRow(SHELF_ON);

    fireEvent.pointerEnter(chip('s5'));
    await advance(1000);
    expect(screen.queryByTestId('shelf-peek')).toBeNull();
    expect(daemon.sentOf('terminal_read_text')).toEqual([]);

    fireEvent.pointerMove(chip('s5'));
    await advance(180);
    await daemon.idle();
    expect(screen.getByTestId('shelf-peek')).toHaveAttribute('data-leaf-id', 'pane-s5');
  });

  it('counts the delay from the first move, and stays closed after Esc until the pointer comes back', async () => {
    const daemon = await renderRow(SHELF_ON);

    movePointerOnto(chip('s5'));
    await advance(179);
    fireEvent.pointerMove(chip('s5'));
    await advance(1);
    await daemon.idle();
    expect(screen.getByTestId('shelf-peek')).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.pointerMove(chip('s5'));
    await advance(1000);
    expect(screen.queryByTestId('shelf-peek')).toBeNull();

    fireEvent.pointerLeave(chip('s5'));
    await peekAt(daemon, 's5');
    expect(screen.getByTestId('shelf-peek')).toBeInTheDocument();
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

    movePointerOnto(screen.getByTestId('shelf-chip-tile-doc'));
    await advance(180);
    await daemon.idle();

    expect(screen.getByTestId('shelf-peek')).toHaveTextContent('notes.md');
    expect(screen.getByTestId('shelf-peek-kind')).toBeInTheDocument();
    expect(screen.queryByTestId('shelf-peek-input')).toBeNull();
    expect(daemon.sentOf('terminal_read_text')).toEqual([]);
  });

  it('says why when the text cannot be read', async () => {
    const daemon = await renderRow(SHELF_ON);
    daemon.on('terminal_read_text', () => ({ event: 'terminal_read_text_result', success: false, error: 'host unreachable' }));

    await peekAt(daemon, 's5');

    expect(screen.getByTestId('shelf-peek-screen')).toHaveTextContent('Could not read this terminal: host unreachable');
  });

  describe.each([
    ['an outpost still connecting', { host: 'outpost-1' }, [CONNECTING], 'Connecting to studio · typing is not sent'],
    ['a stopped agent', { state: 'idle', terminal_exit: { code: 143, at: '2026-10-07T00:20:04+02:00' } }, [], 'Agent stopped'],
  ])('with %s', (_name, targetFields, outposts, notice) => {
    it('keeps the reply, sends nothing, and says why', async () => {
      const daemon = await renderRow(SHELF_ON, { targetFields, outposts });
      await peekAt(daemon, 's5');
      expect(screen.getByTestId('shelf-peek-notice')).toHaveTextContent(notice);

      const input = screen.getByTestId('shelf-peek-input');
      fireEvent.change(input, { target: { value: 'ship it' } });
      await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

      expect(repliesTo(daemon, 's5')).toEqual([]);
      expect(input).toHaveValue('ship it');
      expect(screen.getByTestId('shelf-peek')).toBeInTheDocument();

      fireEvent.keyDown(window, { key: 'Escape' });
      await peekAt(daemon, 's5');
      expect(screen.getByTestId('shelf-peek-input')).toHaveValue('ship it');
    });

    it('shows the unsent reply on the tile after Open, where Discard ends it', async () => {
      const daemon = await renderRow(SHELF_ON, { targetFields, outposts });
      await peekAt(daemon, 's5');
      fireEvent.change(screen.getByTestId('shelf-peek-input'), { target: { value: 'ship it' } });

      await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('shelf-peek')).getByRole('button', { name: /Open/ })));

      const strip = within(document.querySelector<HTMLElement>('[data-pane-id="pane-s5"]')!).getByTestId('unsent-peek-reply');
      expect(strip).toHaveTextContent('Unsent reply: ship it');
      await gesture(daemon, () => fireEvent.click(within(strip).getByRole('button', { name: 'Discard' })));
      expect(screen.queryByTestId('unsent-peek-reply')).toBeNull();
    });
  });

  it('sends a kept reply once the outpost connects', async () => {
    const daemon = await renderRow(SHELF_ON, { targetFields: { host: 'outpost-1' }, outposts: [CONNECTING] });
    await peekAt(daemon, 's5');
    const input = screen.getByTestId('shelf-peek-input');
    fireEvent.change(input, { target: { value: 'ship it' } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [CONNECTED] }));
    expect(repliesTo(daemon, 's5')).toEqual([]);
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

    expect(repliesTo(daemon, 's5')).toEqual(['ship it', '\r']);
    expect(screen.queryByTestId('shelf-peek')).toBeNull();
    await peekAt(daemon, 's5');
    expect(screen.getByTestId('shelf-peek-input')).toHaveValue('');
  });

  it('keeps a refused reply, reports it at once, and sends it whole on retry', async () => {
    const daemon = await renderRow(SHELF_ON, { targetFields: { host: 'outpost-1' }, outposts: [CONNECTED] });
    answerInput(daemon, () => 'studio is unreachable; this needs it now');
    await peekAt(daemon, 's5');
    const input = screen.getByTestId('shelf-peek-input');
    fireEvent.change(input, { target: { value: 'ship it' } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

    expect(repliesTo(daemon, 's5')).toEqual(['ship it']);
    expect(screen.getByRole('alert')).toHaveTextContent('may not have arrived');
    expect(screen.getByRole('alert')).toHaveTextContent('studio is unreachable');

    answerInput(daemon);
    await peekAt(daemon, 's5');
    expect(screen.getByTestId('shelf-peek-notice')).toHaveTextContent('studio is unreachable');
    const reopened = screen.getByTestId('shelf-peek-input');
    expect(reopened).toHaveValue('ship it');
    await gesture(daemon, () => fireEvent.keyDown(reopened, { key: 'Enter' }));
    expect(repliesTo(daemon, 's5')).toEqual(['ship it', 'ship it', '\r']);
    await peekAt(daemon, 's5');
    expect(screen.getByTestId('shelf-peek-input')).toHaveValue('');
  });

  describe('when Return fails and the agent stops', () => {
    const STOPPED = { state: 'idle' as const, terminal_exit: { code: 1, at: '2026-10-07T00:20:04+02:00' } };
    const stop = (daemon: ScriptedDaemon) => daemon.emit({ event: 'session_state_changed', session: daemonSession('s5', STOPPED) });
    const onTile = () => within(document.querySelector<HTMLElement>('[data-pane-id="pane-s5"]')!).getByTestId('unsent-peek-reply');

    async function replyWhoseReturnFails(daemon: ScriptedDaemon, onReturn: () => void = () => {}) {
      answerInput(daemon, (data) => {
        if (data !== '\r') return null;
        onReturn();
        return 'session not running';
      });
      await peekAt(daemon, 's5');
      fireEvent.change(screen.getByTestId('shelf-peek-input'), { target: { value: 'ship it' } });
      await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('shelf-peek-input'), { key: 'Enter' }));
      expect(screen.getByRole('alert')).toHaveTextContent('typed but not submitted');
    }

    const screens = () => 'a screen without the reply';

    it.each([
      ['after the failure', false],
      ['before the failure arrives', true],
    ])('Open keeps the reply on the tile and types nothing, with the stop %s', async (_name, stopFirst) => {
      const daemon = await renderRow(SHELF_ON, { screens });
      await replyWhoseReturnFails(daemon, () => {
        if (stopFirst) stop(daemon);
      });
      if (!stopFirst) await gesture(daemon, () => stop(daemon));

      await peekAt(daemon, 's5');
      expect(screen.getByTestId('shelf-peek-notice')).toHaveTextContent('your reply stays there');
      await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('shelf-peek')).getByRole('button', { name: /Open/ })));

      expect(screen.getByText('Agent stopped.')).toBeVisible();
      expect(onTile()).toHaveTextContent('Unsent reply: ship it');
      expect(repliesTo(daemon, 's5')).toEqual(['ship it', '\r']);
      await gesture(daemon, () => fireEvent.click(within(onTile()).getByRole('button', { name: 'Discard' })));
      expect(screen.queryByTestId('unsent-peek-reply')).toBeNull();
    });

    it('the tile offers the reply when reached from its chip, without the peek', async () => {
      const daemon = await renderRow(SHELF_ON, { screens });
      await replyWhoseReturnFails(daemon);
      await gesture(daemon, () => stop(daemon));

      await gesture(daemon, () => fireEvent.click(chip('s5')));

      expect(onTile()).toHaveTextContent('Unsent reply: ship it');
    });

    it('after a resume, Enter sends the whole reply to the new process, and only then', async () => {
      const daemon = await renderRow(SHELF_ON, { screens });
      await replyWhoseReturnFails(daemon);
      await gesture(daemon, () => stop(daemon));
      await gesture(daemon, () => daemon.emit({ event: 'session_state_changed', session: daemonSession('s5', { state: 'waiting_input' as const }) }));
      answerInput(daemon);

      await peekAt(daemon, 's5');
      expect(repliesTo(daemon, 's5')).toEqual(['ship it', '\r']);
      expect(screen.getByTestId('shelf-peek-notice')).toHaveTextContent('may not have arrived: session not running. Check the screen');
      const input = screen.getByTestId('shelf-peek-input');
      expect(input).toHaveValue('ship it');
      expect(input).not.toHaveAttribute('readonly');

      await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
      expect(repliesTo(daemon, 's5')).toEqual(['ship it', '\r', 'ship it', '\r']);
      await peekAt(daemon, 's5');
      expect(screen.getByTestId('shelf-peek-input')).toHaveValue('');
    });
  });

  it('Open keeps a reply whose Return failed when the outpost cannot be reached', async () => {
    const daemon = await renderRow(SHELF_ON, { targetFields: { host: 'outpost-1' }, outposts: [CONNECTED] });
    answerInput(daemon, (data) => (data === '\r' ? 'the bridge ended before the answer' : null));
    await peekAt(daemon, 's5');
    fireEvent.change(screen.getByTestId('shelf-peek-input'), { target: { value: 'ship it' } });
    await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('shelf-peek-input'), { key: 'Enter' }));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [CONNECTING] }));

    await peekAt(daemon, 's5');
    await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('shelf-peek')).getByRole('button', { name: /Open/ })));

    expect(screen.getByTestId('unsent-peek-reply')).toHaveTextContent('Unsent reply: ship it');
    expect(repliesTo(daemon, 's5')).toEqual(['ship it', '\r']);
  });

  it('Open leaves a reply whose Return failed to the live terminal that holds its text', async () => {
    const daemon = await renderRow(SHELF_ON);
    answerInput(daemon, (data) => (data === '\r' ? 'the terminal did not answer' : null));
    await peekAt(daemon, 's5');
    fireEvent.change(screen.getByTestId('shelf-peek-input'), { target: { value: 'ship it' } });
    await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('shelf-peek-input'), { key: 'Enter' }));

    await peekAt(daemon, 's5');
    await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('shelf-peek')).getByRole('button', { name: /Open/ })));

    expect(screen.queryByTestId('unsent-peek-reply')).toBeNull();
    expect(repliesTo(daemon, 's5')).toEqual(['ship it', '\r']);
  });

  it('after the text lands and Return fails, a retry sends only Return', async () => {
    const daemon = await renderRow(SHELF_ON, { targetFields: { host: 'outpost-1' }, outposts: [CONNECTED] });
    answerInput(daemon, (data) => (data === '\r' ? 'the bridge ended before the answer' : null));
    await peekAt(daemon, 's5');
    const input = screen.getByTestId('shelf-peek-input');
    fireEvent.change(input, { target: { value: 'ship it' } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
    expect(screen.getByRole('alert')).toHaveTextContent('typed but not submitted');

    answerInput(daemon);
    await peekAt(daemon, 's5');
    expect(screen.getByTestId('shelf-peek-notice')).toHaveTextContent('↵ sends Return');
    const reopened = screen.getByTestId('shelf-peek-input');
    expect(reopened).toHaveAttribute('readonly');
    await gesture(daemon, () => fireEvent.keyDown(reopened, { key: 'Enter' }));

    expect(repliesTo(daemon, 's5')).toEqual(['ship it', '\r', '\r']);
    await peekAt(daemon, 's5');
    expect(screen.getByTestId('shelf-peek-input')).toHaveValue('');
  });

  it('does not send twice while the first send is unanswered, or resend by itself after the daemon drops', async () => {
    const daemon = await renderRow(SHELF_ON);
    daemon.on('pty_input', () => undefined);
    await peekAt(daemon, 's5');
    const input = screen.getByTestId('shelf-peek-input');
    fireEvent.change(input, { target: { value: 'ship it' } });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
    expect(screen.queryByTestId('shelf-peek')).toBeNull();

    await peekAt(daemon, 's5');
    expect(screen.getByTestId('shelf-peek-notice')).toHaveTextContent('Sending');
    await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('shelf-peek-input'), { key: 'Enter' }));
    expect(repliesTo(daemon, 's5')).toEqual(['ship it']);

    await gesture(daemon, () => daemon.disconnect());
    expect(screen.getByTestId('shelf-peek-notice')).toHaveTextContent('The daemon disconnected before answering');
    expect(screen.getByTestId('shelf-peek-input')).toHaveValue('ship it');
    await daemon.reconnect();
    await advance(10_000);
    await daemon.idle();
    expect(daemon.connection.sent.filter((command) => command.cmd === 'pty_input' && command.id === 's5')).toEqual([]);
  });
});
