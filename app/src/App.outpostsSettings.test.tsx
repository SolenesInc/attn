import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import { openSection, openSettings, savedSettings } from './test/settings';
import { openSession } from './test/appFixtures';
import { daemonSession, defaultProfile, soloDesktop, splitDesktop } from './test/daemonFixtures';
import { initialState } from './test/scriptedDaemon';

type Outpost = EventMessage<'outposts_updated'>['outposts'][number];
const connected: Outpost = {
  id: 'outpost-1', name: 'studio', destination: 'studio:2200', state: 'connected', reach: 'reachable',
  state_since: '2026-10-18T18:00:00Z', daemon_id: 'studio-daemon',
  host_key_fingerprint: 'SHA256:studio-key', build: 'v0.12.0 (source abc, protocol def)',
};
const enabled = { outposts_enabled: 'true' };
const name = () => screen.getByRole('textbox', { name: 'Name' });
const address = () => screen.getByRole('textbox', { name: 'ssh destination' });
const form = () => screen.getByRole('form', { name: 'Add an outpost' });
function fill(nextName: string, nextAddress: string) {
  fireEvent.change(name(), { target: { value: nextName } });
  fireEvent.change(address(), { target: { value: nextAddress } });
}

describe('Outposts settings', () => {
  it('keeps the section hidden until opt-in, and hides an open section when disabled elsewhere', async () => {
    const daemon = await openSection('experimental');
    expect(screen.queryByTestId('settings-nav-outposts')).toBeNull();
    expect(screen.getByRole('switch', { name: 'Outposts' })).toHaveAttribute('aria-checked', 'false');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('switch', { name: 'Outposts' })));
    expect(savedSettings(daemon)).toEqual([['outposts_enabled', 'true']]);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-outposts')));
    expect(screen.getByText('No outposts added.')).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { outposts_enabled: 'false' } }));
    expect(screen.queryByTestId('settings-nav-outposts')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Add outpost' })).toBeNull();
    expect(screen.getByTestId('settings-section-connectivity')).toBeInTheDocument();
  });

  it('adds by name and ssh destination, retaining the draft until a snapshot confirms it', async () => {
    const daemon = await openSection('outposts', { settings: enabled }, scripted => {
      scripted.on('add_outpost', () => {});
    });
    await gesture(daemon, () => { fill(' studio ', ' studio:2200 '); fireEvent.submit(form()); });
    expect(daemon.sentOf('add_outpost')).toEqual([{ cmd: 'add_outpost', name: 'studio', destination: 'studio:2200' }]);
    expect(name()).toHaveValue(' studio ');
    const commands = [...daemon.sent];
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected] }));
    expect(name()).toHaveValue('');
    expect(address()).toHaveValue('');
    const row = within(screen.getByRole('listitem', { name: 'studio' }));
    expect(row.getByText('connected')).toBeInTheDocument();
    expect(row.getByText(connected.build!)).toBeInTheDocument();
    expect(row.getByText(connected.host_key_fingerprint!)).toBeInTheDocument();
    expect(row.getByText(connected.daemon_id!)).toBeInTheDocument();
    expect(row.getByText(new Date(connected.state_since).toLocaleString())).toHaveAttribute('datetime', connected.state_since);
    expect(daemon.sent).toEqual(commands);
  });

  it('requires only the ssh destination and defaults the name to it', async () => {
    const daemon = await openSection('outposts', { settings: enabled }, scripted => { scripted.on('add_outpost', () => {}); });
    await gesture(daemon, () => { fireEvent.change(address(), { target: { value: ' user@host ' } }); fireEvent.submit(form()); });
    expect(daemon.sentOf('add_outpost')).toEqual([{ cmd: 'add_outpost', name: 'user@host', destination: 'user@host' }]);
  });

  it('preserves a failed or edited draft and shows command failures without clearing them on unrelated updates', async () => {
    const daemon = await openSection('outposts', { settings: enabled }, scripted => {
      scripted.on('add_outpost', () => ({ event: 'command_error', cmd: 'add_outpost', success: false, error: 'Address studio:2200 is already paired' }));
    });
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected] }));
    await gesture(daemon, () => { fill('studio', 'studio:2200'); fireEvent.submit(form()); });
    expect(name()).toHaveValue('studio');
    expect(screen.getByRole('alert')).toHaveTextContent('Address studio:2200 is already paired');
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected] }));
    expect(screen.getByRole('alert')).toHaveTextContent('Address studio:2200 is already paired');
    expect(address()).toHaveValue('studio:2200');
    daemon.on('add_outpost', () => {});
    await gesture(daemon, () => { fill('laptop', 'laptop:2200'); fireEvent.submit(form()); });
    await gesture(daemon, () => fill('next machine', 'next:2200'));
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected, { ...connected, id: 'laptop', name: 'laptop', destination: 'laptop:2200' }] }));
    expect(name()).toHaveValue('next machine');
    expect(address()).toHaveValue('next:2200');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('re-pairs and removes the selected row by id, waiting for daemon state', async () => {
    const daemon = await openSection('outposts', { settings: enabled }, scripted => {
      scripted.on('repair_outpost', () => {});
      scripted.on('remove_outpost', () => {});
    });
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected] }));
    const row = within(screen.getByRole('listitem', { name: 'studio' }));
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Re-pair' })));
    expect(daemon.sentOf('repair_outpost')).toEqual([{ cmd: 'repair_outpost', id: connected.id }]);
    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'Remove' })));
    expect(daemon.sentOf('remove_outpost')).toEqual([{ cmd: 'remove_outpost', id: connected.id }]);
    expect(screen.getByRole('listitem', { name: 'studio' })).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [] }));
    expect(screen.queryByRole('listitem', { name: 'studio' })).toBeNull();
  });

  it('offers a keyboard button only for an ssh refusal and shows a failed open in settings', async () => {
    const error = 'Host key verification failed.\nPlease contact your system administrator.\n';
    const refused: Outpost = { ...connected, state: 'refused', reach: 'unreachable', connectable: true, last_error: error };
    const daemon = await openSection('outposts', { settings: enabled, outposts: [refused] }, scripted => {
      scripted.on('connect_outpost_in_terminal', command => ({ event: 'profile_action_result', request_id: command.request_id, action: command.cmd, success: false, error: 'Connection terminal unavailable' }));
    });
    expect(screen.getByText(error, { exact: true, normalizer: text => text })).toBeInTheDocument();
    const button = screen.getByRole('button', { name: 'Connect in a terminal' });
    button.focus();
    expect(button).toHaveFocus();
    expect(button).not.toHaveAttribute('tabindex', '-1');
    await gesture(daemon, () => fireEvent.click(button));
    expect(daemon.sentOf('connect_outpost_in_terminal')).toMatchObject([{ cmd: 'connect_outpost_in_terminal', id: connected.id }]);
    expect(screen.getByRole('alert')).toHaveTextContent('Connection terminal unavailable');
    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...refused, connectable: false }] }));
    expect(screen.queryByRole('button', { name: 'Connect in a terminal' })).toBeNull();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...refused, state: 'lost' }] }));
    expect(screen.queryByRole('button', { name: 'Connect in a terminal' })).toBeNull();
  });

  it.each([
    ['dashboard', false], ['session', false], ['dashboard', true], ['session', true],
  ] as const)('shows and focuses the returned connection tile from %s, reused=%s', async (origin, reuse) => {
    const anchor = daemonSession('anchor', { agent: 'shell', state: 'idle' });
    const terminal = daemonSession('connection', { agent: 'shell', command_terminal: true, state: 'idle' });
    const before = splitDesktop('desktop-anchor', reuse ? ['anchor', 'connection'] : ['anchor']);
    const after = splitDesktop('desktop-anchor', ['anchor', 'connection'], { active_pane_id: 'pane-connection', revision: 2 });
    const { daemon } = await renderApp({ initialState: {
      settings: { ...enabled, queue_mode_enabled: 'false' },
      sessions: reuse ? [anchor, terminal] : [anchor], desktops: [before],
      outposts: [{ ...connected, state: 'refused', reach: 'unreachable', connectable: true }],
    } });
    daemon.on('connect_outpost_in_terminal', () => {});
    if (origin === 'session') await openSession(daemon, anchor.id);
    await openSettings(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-outposts')));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Connect in a terminal' })));
    const command = daemon.sentOf('connect_outpost_in_terminal')[0];
    expect(command).toMatchObject({ id: connected.id });
    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
    await gesture(daemon, () => {
      if (!reuse) daemon.emit({ event: 'session_registered', session: terminal });
      daemon.arrangement.replace(after);
      daemon.arrangement.show(after.id, 'pane-connection');
      daemon.emit({ event: 'profile_arrangement_changed', request_id: command.request_id, profile: defaultProfile(after.id), desktops: [after] });
      daemon.emit({ event: 'profile_action_result', request_id: command.request_id, action: command.cmd, success: true, desktops: [after], pane_id: 'pane-connection' });
    });
    expect(screen.queryByTestId('settings-modal')).toBeNull();
    const pane = document.querySelector<HTMLElement>('[data-session-visible="1"] [data-pane-id="pane-connection"]');
    expect(pane).not.toBeNull();
    expect(within(pane!).getByRole('textbox', { name: 'Terminal input' })).toHaveFocus();
    expect(daemon.sentOf('desktop_show_leaf')).toMatchObject([{ desktop_id: after.id, leaf_id: 'pane-connection' }]);
  });

  it.each(['true', 'false'])('keeps a connection terminal out of the queue and home in queue flow %s', async (queue) => {
    const shell = daemonSession('connection', { agent: 'shell', command_terminal: true, label: 'Connect to studio', state: 'idle' });
    await renderApp({ initialState: { settings: { queue_mode_enabled: queue }, sessions: [shell], desktops: [soloDesktop(shell.id)] } });
    expect(screen.queryByTestId('session-connection')).toBeNull();
    expect(screen.queryByTestId('queue-turn-connection')).toBeNull();
    expect(screen.queryByTestId('queue-settled-connection')).toBeNull();
  });

  it('shows the full refusal and keeps the daemon’s last error through reconnecting until connected', async () => {
    const daemon = await openSection('outposts', { settings: enabled });
    const error = 'studio:2200 changed host key: home pinned studio-daemon SHA256:old-key; outpost offered SHA256:new-key. Re-pair this outpost to accept its new identity.\nBoth identities must be checked before reconnecting.';
    const refused: Outpost = { ...connected, state: 'refused', reach: 'unreachable', last_error: error, last_error_at: '2026-10-18T18:01:00Z' };
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [refused] }));
    const row = within(screen.getByRole('listitem', { name: 'studio' }));
    expect(row.getByText('refused')).toBeInTheDocument();
    expect(row.getByText(error, { exact: true, normalizer: text => text })).toBeInTheDocument();
    for (const state of ['connecting', 'stalled', 'lost'] as const) {
      await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...refused, state }] }));
      expect(row.getByText(state)).toBeInTheDocument();
      expect(row.getByText(error, { exact: true, normalizer: text => text })).toBeInTheDocument();
    }
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected] }));
    expect(row.queryByText(error, { exact: true, normalizer: text => text })).toBeNull();
  });

  it('disables mutations when disconnected and presents recovering-daemon errors', async () => {
    const daemon = await openSection('outposts', { settings: enabled }, scripted => {
      scripted.on('repair_outpost', () => ({ event: 'command_error', cmd: 'repair_outpost', success: false, error: 'daemon_recovering' }));
    });
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected] }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Re-pair' })));
    expect(screen.getByRole('alert')).toHaveTextContent('The home daemon is recovering. Try again when it is ready.');
    await gesture(daemon, () => daemon.connection.close());
    expect(screen.getByRole('button', { name: 'Add outpost' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Re-pair' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Remove' })).toBeDisabled();
    expect(daemon.sentOf('repair_outpost')).toHaveLength(1);
  });

  it('accepts a snapshot before settings open without fetching or polling outposts', async () => {
    const { daemon } = await renderApp({ initialState: { settings: enabled } });
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected] }));
    await openSettings(daemon);
    const commands = [...daemon.sent];
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-outposts')));
    expect(screen.getByRole('listitem', { name: 'studio' })).toBeInTheDocument();
    expect(daemon.sent).toEqual(commands);
  });

  it('replaces old rows with the initial_state of a home reconnected with outposts disabled, then renders a fresh snapshot', async () => {
    const daemon = await openSection('outposts', { settings: enabled });
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected] }));
    daemon.on('client_hello', () => initialState({ settings: { outposts_enabled: 'false' } }));
    await daemon.reconnect();
    await daemon.idle();
    expect(screen.queryByTestId('settings-nav-outposts')).toBeNull();
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: enabled }));
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-outposts')));
    expect(screen.getByText('No outposts added.')).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [{ ...connected, name: 'New studio' }] }));
    expect(screen.getByRole('listitem', { name: 'New studio' })).toBeInTheDocument();
    expect(screen.queryByRole('listitem', { name: 'studio' })).toBeNull();
    expect(daemon.sentOf('add_outpost')).toEqual([]);
    expect(daemon.sentOf('repair_outpost')).toEqual([]);
    expect(daemon.sentOf('remove_outpost')).toEqual([]);
  });
});
