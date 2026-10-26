import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSession, soloDesktop, defaultProfile } from './test/daemonFixtures';
import { fakeRects } from './test/layout';
import { openSession } from './test/appFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { openSettings } from './test/settings';
import type { EventMessage } from './test/protocol';

type OutpostInfo = EventMessage<'outposts_updated'>['outposts'][number];

const outpost: OutpostInfo = {
  id: 'studio-id', name: 'studio', address: 'studio:2200', state: 'connected', reach: 'reachable',
  state_since: '2026-10-18T18:00:00Z',
};
const connecting: OutpostInfo = { ...outpost, state: 'connecting', reach: 'connecting' };
const lost: OutpostInfo = { ...outpost, state: 'lost', reach: 'unreachable' };
const remote = () => daemonSession('remote', { agent: 'shell', host: outpost.id, state: 'idle' });
const tile = () => document.querySelector<HTMLElement>('[data-pane-id="pane-remote"]')!;
const terminalInput = () => within(tile()).getByRole('textbox', { name: 'Terminal input' });
const flash = () => tile().querySelector('.desktop-pane-refusal');

async function setup(row: OutpostInfo | null = outpost, settings: Record<string, string> = { outposts_enabled: 'true' }) {
  fakeRects((element) => element.tagName === 'CANVAS' ? new DOMRect(0, 0, 800, 600) : null);
  return renderApp({
    script: (daemon) => {
      daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
    },
    initialState: {
      sessions: [remote(), daemonSession('local', { agent: 'shell', state: 'idle' })],
      desktops: [soloDesktop('remote'), soloDesktop('local')],
      settings,
      outposts: row ? [row] : [],
    },
  });
}

describe('Remote shells in the app', () => {
  it('names the outpost only in its tile header and keeps Cmd+D on that host and start directory', async () => {
    const { daemon } = await setup();
    await openSession(daemon, 'remote');
    expect(within(tile()).getByText('studio')).toHaveClass('desktop-pane-host');
    expect(screen.getByTestId('sidebar-session-remote')).not.toHaveTextContent('studio');
    await gesture(daemon, () => pressShortcut('terminal.splitVertical'));
    expect(daemon.sentOf('spawn_session')).toEqual([expect.objectContaining({ host: outpost.id, cwd: '/tmp/remote', agent: 'shell' })]);
  });

  it.each(['lost', 'refused'] as const)('marks %s offline on every surface, drops typing, and refuses close, split and reload', async (state) => {
    const { daemon } = await setup();
    await openSession(daemon, 'remote');
    await gesture(daemon, () => daemon.emit({ event: 'pty_output', id: 'remote', seq: 1, data: btoa('last remote screen') }));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...outpost, state, reach: 'unreachable' }] }));
    expect(tile()).toHaveAttribute('data-offline', 'true');
    expect(within(tile()).getByText('studio unreachable · typing is not sent')).toBeInTheDocument();
    expect(within(tile()).queryByText('The screen is on studio.')).toBeNull();
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('remote')).toContain('last remote screen');
    expect(screen.getByTestId('sidebar-session-remote')).toHaveAttribute('data-offline', 'true');
    expect(screen.getByTestId('session-remote')).toHaveAttribute('data-offline', 'true');
    expect(within(tile()).getByLabelText('offline')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'x', code: 'KeyX' }));
    expect(daemon.sentOf('pty_input')).toEqual([]);
    const firstFlash = flash();
    expect(firstFlash).not.toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'y', code: 'KeyY' }));
    expect(flash()).not.toBe(firstFlash);
    expect(within(tile()).queryByText(/may not have been sent/)).toBeNull();
    await gesture(daemon, () => pressShortcut('terminal.splitVertical'));
    await gesture(daemon, () => pressShortcut('session.close'));
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('session-actions-remote')));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Reload session' })));
    expect(daemon.sentOf('spawn_session')).toEqual([]);
    expect(daemon.sentOf('unregister')).toEqual([]);
    expect(daemon.sentOf('desktop_close_tile')).toEqual([]);
    expect(daemon.sentOf('reload_session')).toEqual([]);
    expect(screen.getAllByText(/studio is unreachable/).length).toBeGreaterThan(0);
    await openSession(daemon, 'local');
    expect(document.querySelector('[data-pane-id="pane-local"] .desktop-pane-host')).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(within(document.querySelector<HTMLElement>('[data-pane-id="pane-local"]')!).getByRole('textbox', { name: 'Terminal input' }), { key: 'l', code: 'KeyL' }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 'local', data: 'l' })]);
  });

  it('shows remote tiles offline and named while Outposts is off', async () => {
    const { daemon } = await setup({ ...connecting, reach: 'unreachable' }, { outposts_enabled: 'false' });
    await openSession(daemon, 'remote');
    expect(tile()).toHaveAttribute('data-offline', 'true');
    expect(within(tile()).getByText('studio')).toHaveClass('desktop-pane-host');
    expect(within(tile()).getByText('studio unreachable · typing is not sent')).toBeInTheDocument();
    expect(within(tile()).getByText('The screen is on studio.')).toBeInTheDocument();
    expect(screen.getByTestId('sidebar-session-remote')).toHaveAttribute('data-offline', 'true');
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'x', code: 'KeyX' }));
    expect(flash()).not.toBeNull();
    await gesture(daemon, () => pressShortcut('session.close'));
    expect(daemon.sentOf('attach_session')).toEqual([]);
    expect(daemon.sentOf('pty_input')).toEqual([]);
    expect(daemon.sentOf('unregister')).toEqual([]);
    expect(screen.getAllByText(/studio is unreachable/).length).toBeGreaterThan(0);
  });

  it('reads a tile whose host has no outpost row as unreachable', async () => {
    const { daemon } = await setup(null);
    await openSession(daemon, 'remote');
    expect(tile()).toHaveAttribute('data-offline', 'true');
    expect(daemon.sentOf('attach_session')).toEqual([]);
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'x', code: 'KeyX' }));
    expect(daemon.sentOf('pty_input')).toEqual([]);
    expect(flash()).not.toBeNull();
  });

  it('leaves a stalled shell online and sends its typing', async () => {
    const { daemon } = await setup({ ...outpost, state: 'stalled' });
    await openSession(daemon, 'remote');
    expect(tile()).not.toHaveAttribute('data-offline', 'true');
    expect(within(tile()).queryByRole('status')).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'x', code: 'KeyX' }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 'remote', data: 'x' })]);
    expect(flash()).toBeNull();
  });

  it('refuses typing with a flash and no strip while connecting, then attaches once reachable', async () => {
    const { daemon } = await setup(connecting);
    await openSession(daemon, 'remote');
    expect(tile()).not.toHaveAttribute('data-offline', 'true');
    expect(within(tile()).queryByRole('status')).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'x', code: 'KeyX' }));
    expect(flash()).not.toBeNull();
    expect(within(tile()).queryByRole('status')).toBeNull();
    expect(daemon.sentOf('attach_session')).toEqual([]);
    expect(daemon.sentOf('pty_resize')).toEqual([]);
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [outpost] }));
    expect(daemon.sentOf('attach_session').map(({ id }) => id)).toEqual(['remote']);
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'z', code: 'KeyZ' }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 'remote', data: 'z' })]);
  });

  it('says the screen is on the outpost when a tile mounts unreachable, attaches on recovery, and never replays refused input', async () => {
    const { daemon } = await setup(lost);
    await openSession(daemon, 'remote');
    expect(daemon.sentOf('attach_session')).toEqual([]);
    expect(daemon.sentOf('pty_resize')).toEqual([]);
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('remote')?.trim()).toBe('');
    expect(within(tile()).getByText('The screen is on studio.')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'x', code: 'KeyX' }));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [outpost] }));
    expect(daemon.sentOf('attach_session').map(({ id }) => id)).toEqual(['remote']);
    expect(daemon.sentOf('pty_input')).toEqual([]);
    expect(within(tile()).queryByText(/unreachable/)).toBeNull();
  });

  it('says the screen is on the outpost when a tile mounted while connecting turns unreachable', async () => {
    const { daemon } = await setup(connecting);
    await openSession(daemon, 'remote');
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [lost] }));
    expect(within(tile()).getByText('studio unreachable · typing is not sent')).toBeInTheDocument();
    expect(within(tile()).getByText('The screen is on studio.')).toBeInTheDocument();
  });

  it('flashes and counts keys the daemon refused when a stalled bridge turns lost, and replays none', async () => {
    const { daemon } = await setup({ ...outpost, state: 'stalled' });
    await openSession(daemon, 'remote');
    for (const key of ['l', 's', 'Enter']) {
      await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key, code: key === 'Enter' ? 'Enter' : `Key${key.toUpperCase()}` }));
    }
    const typed = daemon.sentOf('pty_input');
    expect(typed).toHaveLength(3);
    expect(flash()).toBeNull();
    for (const _ of typed) {
      await gesture(daemon, () => daemon.emit({ event: 'command_error', cmd: 'pty_input', success: false, error: 'studio is unreachable; this needs it now', terminal_id: 'remote' }));
    }
    expect(flash()).not.toBeNull();
    expect(within(tile()).getByText('3 keys may not have been sent')).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [lost] }));
    expect(within(tile()).getByText('studio unreachable · typing is not sent')).toBeInTheDocument();
    expect(within(tile()).getByText('3 keys may not have been sent')).toBeInTheDocument();
    const beforeResizeRefusal = flash();
    await gesture(daemon, () => daemon.emit({ event: 'command_error', cmd: 'pty_resize', success: false, error: 'studio is unreachable; this needs it now', terminal_id: 'remote' }));
    expect(flash()).not.toBe(beforeResizeRefusal);
    expect(within(tile()).getByText('3 keys may not have been sent')).toBeInTheDocument();
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [outpost] }));
    expect(within(tile()).getByText('3 keys may not have been sent')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'z', code: 'KeyZ' }));
    expect(within(tile()).queryByText(/may not have been sent/)).toBeNull();
    expect(daemon.sentOf('pty_input').slice(typed.length)).toEqual([expect.objectContaining({ id: 'remote', data: 'z' })]);
  });

  it('waits through re-pair connecting before attaching an unreachable terminal again', async () => {
    const { daemon } = await setup();
    await openSession(daemon, 'remote');
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...outpost, state: 'refused', reach: 'unreachable' }] }));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connecting] }));
    expect(tile()).not.toHaveAttribute('data-offline', 'true');
    expect(daemon.sentOf('attach_session')).toHaveLength(1);
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [outpost] }));
    expect(daemon.sentOf('attach_session').map(({ id }) => id)).toEqual(['remote', 'remote']);
  });

  it('ignores a pending attach completed while offline, then attaches once on recovery', async () => {
    const { daemon } = await setup();
    daemon.on('attach_session', () => undefined);
    await openSession(daemon, 'remote');
    expect(daemon.sentOf('attach_session')).toHaveLength(1);
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [lost] }));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [outpost] }));
    expect(daemon.sentOf('attach_session')).toHaveLength(1);
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
    await gesture(daemon, () => daemon.emit({ event: 'attach_result', id: 'remote', success: true, cols: 80, rows: 24, running: true }));
    expect(daemon.sentOf('attach_session').map(({ id }) => id)).toEqual(['remote', 'remote']);
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('remote')).not.toContain('Failed to attach');
    await gesture(daemon, () => fireEvent.keyDown(terminalInput(), { key: 'z', code: 'KeyZ' }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 'remote', data: 'z' })]);
  });

  it('asks in place before removing terminals across profiles, and Cancel sends nothing', async () => {
    const { daemon } = await renderApp({ initialState: {
      settings: { outposts_enabled: 'true' },
      profiles: [defaultProfile('desktop-remote'), defaultProfile('', { id: 'other-profile', name: 'Other' })],
      sessions: [remote(), daemonSession('other', { agent: 'shell', host: outpost.id, profile_id: 'other-profile' }), daemonSession('local')],
      desktops: [soloDesktop('remote')],
      outposts: [outpost],
    } });
    await openSettings(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-outposts')));
    const row = within(screen.getByRole('listitem', { name: 'studio' }));
    row.getByRole('button', { name: 'Remove' }).focus();
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove' })));
    expect(daemon.sentOf('remove_outpost')).toEqual([]);
    expect(row.getByRole('button', { name: 'Remove and close 2 terminals' })).toHaveFocus();
    expect(row.queryByText(/keep running/)).toBeNull();
    row.getByRole('button', { name: 'Cancel' }).focus();
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Cancel' })));
    expect(row.getByRole('button', { name: 'Remove' })).toHaveFocus();
    expect(daemon.sentOf('remove_outpost')).toEqual([]);
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove' })));
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove and close 2 terminals' })));
    expect(daemon.sentOf('remove_outpost')).toEqual([{ cmd: 'remove_outpost', id: outpost.id }]);
  });

  it('says the shells of an unreachable outpost keep running there before removing it', async () => {
    const { daemon } = await renderApp({ initialState: {
      settings: { outposts_enabled: 'true' },
      sessions: [remote(), daemonSession('local')],
      desktops: [soloDesktop('remote')],
      outposts: [lost],
    } });
    await openSettings(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-outposts')));
    const row = within(screen.getByRole('listitem', { name: 'studio' }));
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove' })));
    expect(row.getByText('studio is unreachable, so its shells keep running there until you run attn enrollment leave on it.')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove and forget 1 terminal' })));
    expect(daemon.sentOf('remove_outpost')).toEqual([{ cmd: 'remove_outpost', id: outpost.id }]);
  });
});
