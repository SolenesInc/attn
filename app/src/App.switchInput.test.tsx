import { act, fireEvent, screen } from '@testing-library/react';
import { onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { describe, expect, it, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { agentPane, daemonDesktop, daemonSession, dockTiles, soloDesktop, splitDesktop } from './test/daemonFixtures';
import { gesture, pressShortcut } from './test/renderApp';
import type { CommandMessage } from './test/protocol';

describe('typing while a selection is awaiting the daemon', () => {
  it('allows the browser paste accelerator to produce a payload while the target attaches', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2'].map((id) => soloDesktop(id)),
    });
    const attaches: CommandMessage<'attach_session'>[] = [];
    daemon.on('attach_session', (command) => { attaches.push(command); return undefined; });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
    await gesture(daemon, () => {
      const element = document.activeElement!;
      if (fireEvent.keyDown(element, { key: 'v', code: 'KeyV', metaKey: true })) {
        fireEvent.paste(element, { clipboardData: { getData: () => 'pasted', files: [] } });
      }
    });
    expect(daemon.sentOf('pty_input')).toEqual([]);
    await gesture(daemon, () => daemon.replyTo(attaches[0], { event: 'attach_result', id: 's2', success: true, cols: 80, rows: 24, running: true }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 's2', data: 'pasted' })]);
  });

  it('retains input when a new pane arrives before its session registration', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: [daemonSession('s1', { state: 'idle' })],
      desktops: [soloDesktop('s1')],
    });
    await gesture(daemon, () => daemon.arrange((desktops) => [...desktops, soloDesktop('s2', { shortcut_slot: 2 })]));
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    expect(screen.getByText(/Waiting for s2/)).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    await gesture(daemon, () => daemon.emit({ event: 'sessions_updated', sessions: [daemonSession('s1', { state: 'idle' })] }));
    await gesture(daemon, () => daemon.emit({ event: 'session_registered', session: daemonSession('s2', { state: 'idle' }) }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 's2', data: 'x' })]);
  });

  it('releases a closed target’s retained input and desktop after its selection already succeeded', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2', 's3', 's4'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2', 's3', 's4'].map((id) => soloDesktop(id)),
    });
    const attaches: CommandMessage<'attach_session'>[] = [];
    daemon.on('attach_session', (command) => {
      if (command.id === 's2') { attaches.push(command); return undefined; }
      return { event: 'attach_result', id: command.id, success: true, cols: 80, rows: 24, running: true };
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    for (const id of ['s3', 's4']) await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: `Open ${id}` })));
    expect(document.querySelector('[data-session-terminal-desktop="desktop-s2"]')).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'session_unregistered', session: daemonSession('s2', { state: 'idle' }) }));
    expect(screen.getByText(/Input typed while switching could not be delivered: Its terminal closed/)).toBeInTheDocument();
    expect(document.querySelector('[data-session-terminal-desktop="desktop-s2"]')).not.toBeInTheDocument();
    await gesture(daemon, () => daemon.replyTo(attaches[0], { event: 'attach_result', id: 's2', success: true, cols: 80, rows: 24, running: true }));
    expect(daemon.sentOf('pty_input')).toEqual([]);
    expect(document.activeElement?.closest('[data-pane-id]')).toHaveAttribute('data-pane-id', 'pane-s4');
  });

  it('discards a failed cold desktop selection’s input before its attachment finishes', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2'].map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
    });
    const shows: CommandMessage<'desktop_set_current'>[] = [];
    const attaches: CommandMessage<'attach_session'>[] = [];
    daemon.on('desktop_set_current', (command) => { shows.push(command); return undefined; });
    daemon.on('attach_session', (command) => { attaches.push(command); return undefined; });
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    await gesture(daemon, () => daemon.replyTo(shows[0], { event: 'profile_action_result', action: shows[0].cmd, request_id: shows[0].request_id, success: false, error: 'desktop closed', error_code: 'not_found' }));
    expect(screen.getByText(/Input typed while switching could not be delivered/)).toBeInTheDocument();
    await gesture(daemon, () => daemon.replyTo(attaches[0], { event: 'attach_result', id: 's2', success: true, cols: 80, rows: 24, running: true }));
    expect(daemon.sentOf('pty_input')).toEqual([]);
    expect(document.activeElement?.closest('[data-pane-id]')).toHaveAttribute('data-pane-id', 'pane-s1');
  });

  it('reports and discards unconfirmed input when the daemon disconnects', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: [soloDesktop('s1')],
    });
    daemon.on('desktop_show_session', () => undefined);
    await gesture(daemon, () => act(() => vi.mocked(onOpenUrl).mock.lastCall![0](['attn://spawn?cwd=%2Ftmp%2Fs2'])));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    await gesture(daemon, () => daemon.disconnect());
    expect(screen.getByText(/Input typed while switching could not be delivered: The daemon disconnected/)).toBeInTheDocument();
    daemon.arrangement.place('s2', 'pane-s2');
    daemon.arrangement.show('desktop-s1', 'pane-s2');
    await daemon.reconnect();
    await daemon.idle();
    expect(daemon.sentOf('pty_input')).toEqual([]);
  });

  it('encodes retained keys with the selected terminal’s restored keyboard mode', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2'].map((id) => soloDesktop(id)),
    });
    const held: CommandMessage<'attach_session'>[] = [];
    daemon.on('attach_session', (command) => { held.push(command); return undefined; });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
    await gesture(daemon, () => {
      fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' });
      fireEvent.keyUp(document.activeElement!, { key: 'x', code: 'KeyX' });
    });
    expect(daemon.sentOf('pty_input')).toEqual([]);
    await gesture(daemon, () => {
      daemon.emit({ event: 'pty_output', id: 's2', seq: 1, data: btoa('\x1b[>31u') });
      daemon.replyTo(held[0], { event: 'attach_result', id: 's2', success: true, cols: 80, rows: 24, running: true, last_seq: 0 });
    });
    expect(daemon.sentOf('pty_input')).toEqual([
      expect.objectContaining({ id: 's2', data: '\x1b[120;;120u' }),
      expect.objectContaining({ id: 's2', data: '\x1b[120;1:3u' }),
    ]);
  });

  it('reports input that could not be delivered after a refused placement without sending it to the previous agent', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: [soloDesktop('s1')],
    });
    const held: CommandMessage<'desktop_show_session'>[] = [];
    daemon.on('desktop_show_session', (command) => { held.push(command); return undefined; });
    await gesture(daemon, () => act(() => vi.mocked(onOpenUrl).mock.lastCall![0](['attn://spawn?cwd=%2Ftmp%2Fs2'])));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    await gesture(daemon, () => daemon.replyTo(held[0], { event: 'profile_action_result', action: held[0].cmd, request_id: held[0].request_id, success: false, error: 'agent closed', error_code: 'session_closed' }));
    expect(screen.getByText(/Input typed while switching could not be delivered/)).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'y', code: 'KeyY' }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 's1', data: 'y' })]);
  });

  it('delivers each unplaced agent its own input while the later pick is still awaiting placement', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2', 's3'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: [soloDesktop('s1')],
    });
    const held: CommandMessage<'desktop_show_session'>[] = [];
    daemon.on('desktop_show_session', (command) => { held.push(command); return undefined; });
    for (const [id, key] of [['s2', 'x'], ['s3', 'y']]) {
      await gesture(daemon, () => act(() => vi.mocked(onOpenUrl).mock.lastCall![0]([`attn://spawn?cwd=%2Ftmp%2F${id}`])));
      await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key, code: `Key${key.toUpperCase()}` }));
    }
    for (const [index, id] of ['s2', 's3'].entries()) {
      await gesture(daemon, () => {
        daemon.arrangement.place(id, `pane-${id}`);
        daemon.arrangement.show('desktop-s1', `pane-${id}`);
        daemon.emit(daemon.arrangement.answer(held[index]));
        daemon.replyTo(held[index], { event: 'profile_action_result', action: held[index].cmd, request_id: held[index].request_id, success: true });
      });
      expect(daemon.sentOf('pty_input')).toEqual([
        expect.objectContaining({ id: 's2', data: 'x' }),
        ...(index ? [expect.objectContaining({ id: 's3', data: 'y' })] : []),
      ]);
    }
  });

  it('retains a cold terminal until its input is delivered after two further selections', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2', 's3', 's4'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2', 's3', 's4'].map((id) => soloDesktop(id)),
    });
    const held: CommandMessage<'attach_session'>[] = [];
    daemon.on('attach_session', (command) => {
      if (command.id === 's2') { held.push(command); return undefined; }
      return { event: 'attach_result', id: command.id, success: true, cols: 80, rows: 24, running: true };
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    for (const id of ['s3', 's4']) await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: `Open ${id}` })));
    expect(daemon.sentOf('pty_input')).toEqual([]);
    expect(held).toHaveLength(1);
    await gesture(daemon, () => daemon.replyTo(held[0], { event: 'attach_result', id: 's2', success: true, cols: 80, rows: 24, running: true }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 's2', data: 'x' })]);
    expect(document.activeElement?.closest('[data-pane-id]')).toHaveAttribute('data-pane-id', 'pane-s4');
  });

  it.each([false, true])('keeps input for an agent awaiting placement (another pick: %s)', async (moveOn) => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2', 's3', 's4'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's3', 's4'].map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
    });
    const held: CommandMessage<'desktop_show_session'>[] = [];
    daemon.on('desktop_show_session', (command) => { held.push(command); return undefined; });
    await gesture(daemon, () => act(() => vi.mocked(onOpenUrl).mock.lastCall![0](['attn://spawn?cwd=%2Ftmp%2Fs2'])));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'Enter', code: 'Enter', shiftKey: true }));
    expect(daemon.sentOf('pty_input')).toEqual([]);
    if (moveOn) {
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s3' })));
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s4' })));
    }
    await gesture(daemon, () => {
      daemon.arrangement.place('s2', 'pane-s2');
      const placement = daemon.arrangement.placementOf('s2');
      daemon.arrangement.show(placement!.desktopId, placement!.paneId);
      daemon.emit(daemon.arrangement.answer(held[0]));
      daemon.replyTo(held[0], { event: 'profile_action_result', action: held[0].cmd, request_id: held[0].request_id, success: true });
    });
    expect(daemon.sentOf('pty_input')).toEqual([
      expect.objectContaining({ id: 's2', data: 'x' }),
      expect.objectContaining({ id: 's2', data: '\n' }),
    ]);
    expect(document.activeElement?.closest('[data-pane-id]')).toHaveAttribute('data-pane-id', moveOn ? 'pane-s4' : 'pane-s2');
  });

  it.each(['key', 'paste', 'composition', 'submit'] as const)('keeps %s input when the selected terminal is mounting', async (input) => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2', 's3'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2', 's3'].map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
    });
    daemon.on('desktop_show_session', () => undefined);
    fireEvent.click(screen.getByRole('button', { name: 'Open s3' }));
    const terminal = document.activeElement!;
    if (input === 'key' || input === 'submit') {
      fireEvent.keyDown(terminal, { key: 'x', code: 'KeyX' });
      fireEvent.keyUp(terminal, { key: 'x', code: 'KeyX' });
    }
    if (input === 'submit') fireEvent.keyDown(terminal, { key: 'Enter', code: 'Enter', shiftKey: true });
    if (input === 'paste') fireEvent.paste(terminal, { clipboardData: { items: [], getData: () => 'x' } });
    if (input === 'composition') {
      fireEvent(terminal, new CompositionEvent('compositionstart', { bubbles: true }));
      fireEvent(terminal, Object.defineProperty(new CompositionEvent('compositionend', { bubbles: true }), 'data', { value: 'x' }));
    }
    await daemon.idle();
    expect(daemon.sentOf('pty_input')).toEqual([
      expect.objectContaining({ id: 's3', data: 'x' }),
      ...(input === 'submit' ? [expect.objectContaining({ id: 's3', data: '\n' })] : []),
    ]);
  });

  it.each(['sidebar', 'desktop', 'desktop row', 'pane', 'palette', 'history', 'queue'] as const)('sends input to the target picked through %s before the reply', async (entry) => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2'].map((id, index) => daemonSession(id, { state: 'idle', turn_owed: true, turn_opened_at: `2026-10-11T1${index}:00:00Z` })),
      initialState: { settings: { queue_mode_enabled: entry === 'queue' ? 'true' : 'false' } },
      desktops: entry === 'pane'
        ? [splitDesktop('ws', ['s1', 's2'])]
        : ['s1', 's2'].map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
    });
    if (entry === 'history') {
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
    }
    if (entry === 'palette') await gesture(daemon, () => pressShortcut('ui.actionMenu'));
    daemon.on('desktop_show_session', () => undefined);
    daemon.on('desktop_show_leaf', () => undefined);
    daemon.on('desktop_set_current', () => undefined);
    await gesture(daemon, () => {
      if (entry === 'sidebar') fireEvent.click(screen.getByRole('button', { name: 'Open s2' }));
      if (entry === 'desktop') pressShortcut('desktop.select2');
      if (entry === 'desktop row') fireEvent.click(screen.getByRole('button', { name: 'Open Desktop 2' }));
      if (entry === 'palette') fireEvent.mouseDown(screen.getByTestId('palette-agent-s2').closest('[role="option"]')!);
      if (entry === 'history') pressShortcut('session.historyBack');
      if (entry === 'queue') daemon.emit({ event: 'sessions_updated', sessions: [daemonSession('s1', { state: 'idle', turn_owed: false }), daemonSession('s2', { state: 'idle', turn_owed: true, turn_opened_at: '2026-10-11T11:00:00Z' })] });
      if (entry === 'pane') fireEvent.mouseDown(document.querySelector('[data-pane-id="pane-s2"]')!);
    });
    await act(async () => {
      fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' });
      await daemon.idle();
    });
    expect(daemon.sentOf('pty_input')).toEqual([
      expect.objectContaining({ id: 's2', data: 'x' }),
    ]);
  });

  it.each(['sidebar', 'desktop'] as const)('keeps %s input on the picked terminal during a long daemon stall', async (entry) => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2'].map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
    });
    daemon.on('desktop_show_session', () => undefined);
    daemon.on('desktop_set_current', () => undefined);
    await gesture(daemon, () => {
      if (entry === 'sidebar') fireEvent.click(screen.getByRole('button', { name: 'Open s2' }));
      else pressShortcut('desktop.select2');
    });
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 's2', data: 'x' })]);
  });

  it('keeps typing in the latest pick when an earlier reply arrives', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: ['s1', 's2', 's3'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2', 's3'].map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
    });
    const held: CommandMessage<'desktop_show_session'>[] = [];
    daemon.on('desktop_show_session', (command) => { held.push(command); return undefined; });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s3' })));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    await gesture(daemon, () => {
      daemon.arrangement.show('desktop-s2', 'pane-s2');
      daemon.emit(daemon.arrangement.answer(held[0]));
      daemon.replyTo(held[0], { event: 'profile_action_result', action: held[0].cmd, request_id: held[0].request_id, success: true });
    });
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'y', code: 'KeyY' }));
    expect(held.map((command) => command.session_id)).toEqual(['s2', 's3']);
    expect(daemon.sentOf('pty_input')).toEqual([
      expect.objectContaining({ id: 's3', data: 'x' }),
      expect.objectContaining({ id: 's3', data: 'y' }),
    ]);
  });

  it('gives a picked markdown tile the keyboard before its reply', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: [daemonSession('s1', { state: 'idle' })],
      desktops: [daemonDesktop('ws', {
        root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, [{ tile_id: 'notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }]),
        panes: [agentPane('s1', 'ws')],
      }, { active_pane_id: 'pane-s1' })],
    });
    daemon.on('desktop_show_leaf', () => undefined);
    await gesture(daemon, () => fireEvent.mouseDown(document.querySelector('[data-pane-id="notes"]')!));
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    expect(document.activeElement?.closest('[data-pane-id]')).toHaveAttribute('data-pane-id', 'notes');
    expect(daemon.sentOf('desktop_show_leaf')).toEqual([expect.objectContaining({ desktop_id: 'ws', leaf_id: 'notes' })]);
    expect(daemon.sentOf('pty_input')).toEqual([]);
  });
});
