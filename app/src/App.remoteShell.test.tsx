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
  id: 'studio-id', name: 'studio', address: 'studio:2200', state: 'connected',
  state_since: '2026-10-18T18:00:00Z',
};
const remote = () => daemonSession('remote', { agent: 'shell', host: outpost.id, state: 'idle' });
const tile = () => document.querySelector<HTMLElement>('[data-pane-id="pane-remote"]')!;

async function setup(state: OutpostInfo['state'] = 'connected') {
  fakeRects((element) => element.tagName === 'CANVAS' ? new DOMRect(0, 0, 800, 600) : null);
  const view = await renderApp({
    script: (daemon) => {
      daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
    },
    initialState: {
      sessions: [remote(), daemonSession('local', { agent: 'shell', state: 'idle' })],
      desktops: [soloDesktop('remote'), soloDesktop('local')],
      settings: { outposts_enabled: 'true' },
    },
  });
  await gesture(view.daemon, () => view.daemon.emit({ event: 'outposts_updated', outposts: [{ ...outpost, state }] }));
  return view;
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
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...outpost, state }] }));
    expect(tile()).toHaveAttribute('data-offline', 'true');
    expect(within(tile()).getByText('studio unreachable · typing is not sent')).toBeInTheDocument();
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('remote')).toContain('last remote screen');
    expect(screen.getByTestId('sidebar-session-remote')).toHaveAttribute('data-offline', 'true');
    expect(screen.getByTestId('session-remote')).toHaveAttribute('data-offline', 'true');
    expect(within(tile()).getByLabelText('offline')).toBeInTheDocument();
    const input = within(tile()).getByRole('textbox', { name: 'Terminal input' });
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'x', code: 'KeyX' }));
    expect(daemon.sentOf('pty_input')).toEqual([]);
    const firstFlash = tile().querySelector('.desktop-pane-refusal');
    expect(firstFlash).not.toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'y', code: 'KeyY' }));
    expect(tile().querySelector('.desktop-pane-refusal')).not.toBe(firstFlash);
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

  it.each(['stalled', 'connecting'] as const)('leaves a %s shell online', async (state) => {
    const { daemon } = await setup(state);
    await openSession(daemon, 'remote');
    expect(tile()).not.toHaveAttribute('data-offline', 'true');
    expect(within(tile()).queryByText(/unreachable/)).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(within(tile()).getByRole('textbox', { name: 'Terminal input' }), { key: 'x', code: 'KeyX' }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 'remote', data: 'x' })]);
  });

  it('mounts offline with an empty screen, attaches on recovery, and never replays refused input', async () => {
    const { daemon } = await setup('lost');
    await openSession(daemon, 'remote');
    expect(daemon.sentOf('attach_session')).toEqual([]);
    expect(daemon.sentOf('pty_resize')).toEqual([]);
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('remote')?.trim()).toBe('');
    expect(within(tile()).getByText('The screen is on studio.')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(within(tile()).getByRole('textbox', { name: 'Terminal input' }), { key: 'x', code: 'KeyX' }));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [outpost] }));
    expect(daemon.sentOf('attach_session').map(({ id }) => id)).toEqual(['remote']);
    expect(daemon.sentOf('pty_input')).toEqual([]);
    expect(within(tile()).queryByText(/unreachable/)).toBeNull();
  });

  it('ignores a pending attach completed while offline, then attaches once on recovery', async () => {
    const { daemon } = await setup();
    daemon.on('attach_session', () => undefined);
    await openSession(daemon, 'remote');
    expect(daemon.sentOf('attach_session')).toHaveLength(1);
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...outpost, state: 'lost' }] }));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [outpost] }));
    expect(daemon.sentOf('attach_session')).toHaveLength(1);
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
    await gesture(daemon, () => daemon.emit({ event: 'attach_result', id: 'remote', success: true, cols: 80, rows: 24, running: true }));
    expect(daemon.sentOf('attach_session').map(({ id }) => id)).toEqual(['remote', 'remote']);
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('remote')).not.toContain('Failed to attach');
    await gesture(daemon, () => fireEvent.keyDown(within(tile()).getByRole('textbox', { name: 'Terminal input' }), { key: 'z', code: 'KeyZ' }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 'remote', data: 'z' })]);
  });

  it('asks in place before removing terminals across profiles, and Cancel sends nothing', async () => {
    const { daemon } = await renderApp({ initialState: {
      settings: { outposts_enabled: 'true' },
      profiles: [defaultProfile('desktop-remote'), defaultProfile('', { id: 'other-profile', name: 'Other' })],
      sessions: [remote(), daemonSession('other', { agent: 'shell', host: outpost.id, profile_id: 'other-profile' }), daemonSession('local')],
      desktops: [soloDesktop('remote')],
    } });
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [outpost] }));
    await openSettings(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-outposts')));
    const row = within(screen.getByRole('listitem', { name: 'studio' }));
    row.getByRole('button', { name: 'Remove' }).focus();
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove' })));
    expect(daemon.sentOf('remove_outpost')).toEqual([]);
    expect(row.getByRole('button', { name: 'Remove and close 2 terminals' })).toHaveFocus();
    row.getByRole('button', { name: 'Cancel' }).focus();
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Cancel' })));
    expect(row.getByRole('button', { name: 'Remove' })).toHaveFocus();
    expect(daemon.sentOf('remove_outpost')).toEqual([]);
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove' })));
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove and close 2 terminals' })));
    expect(daemon.sentOf('remove_outpost')).toEqual([{ cmd: 'remove_outpost', id: outpost.id }]);
  });
});
