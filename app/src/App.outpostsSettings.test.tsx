import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import { openSection, openSettings, savedSettings } from './test/settings';
import { initialState } from './test/scriptedDaemon';

type Outpost = EventMessage<'outposts_updated'>['outposts'][number];
const connected: Outpost = {
  id: 'outpost-1', name: 'studio', address: 'studio:2200', state: 'connected',
  state_since: '2026-10-18T18:00:00Z', daemon_id: 'studio-daemon',
  host_key_fingerprint: 'SHA256:studio-key', build: 'v0.12.0 (source abc, protocol def)',
};
const enabled = { outposts_enabled: 'true' };
const name = () => screen.getByRole('textbox', { name: 'Name' });
const address = () => screen.getByRole('textbox', { name: 'Address' });
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

  it('adds by name and address, retaining the draft until a snapshot confirms it', async () => {
    const daemon = await openSection('outposts', { settings: enabled }, scripted => {
      scripted.on('add_outpost', () => {});
    });
    await gesture(daemon, () => { fill(' studio ', ' studio:2200 '); fireEvent.submit(form()); });
    expect(daemon.sentOf('add_outpost')).toEqual([{ cmd: 'add_outpost', name: 'studio', address: 'studio:2200' }]);
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
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [connected, { ...connected, id: 'laptop', name: 'laptop', address: 'laptop:2200' }] }));
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

  it('shows the full refusal and keeps the daemon’s last error through reconnecting until connected', async () => {
    const daemon = await openSection('outposts', { settings: enabled });
    const error = 'studio:2200 changed host key: home pinned studio-daemon SHA256:old-key; outpost offered SHA256:new-key. Re-pair this outpost to accept its new identity.\nBoth identities must be checked before reconnecting.';
    const refused: Outpost = { ...connected, state: 'refused', last_error: error, last_error_at: '2026-10-18T18:01:00Z' };
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

  it('clears old rows when reconnecting to a home with outposts disabled, then renders a fresh snapshot', async () => {
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
