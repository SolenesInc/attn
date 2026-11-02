import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openSection } from '../test/settings';
import { gesture } from '../test/renderApp';
import { DEFAULT_PROFILE_ID } from '../test/daemonFixtures';
import type { ScriptedDaemon } from '../test/scriptedDaemon';
import type { PhonePairing, PhonePairingOffer, PhonesUpdatedMessage } from '../types/generated';
import { PhoneLinkState, PhonesUpdatedMessageEvent } from '../types/generated';

const link = { state: PhoneLinkState.Connected, cloud_url: 'https://cloud.attn.codes', home_id: 'home-1' };

function phones(overrides: Partial<PhonesUpdatedMessage> = {}): PhonesUpdatedMessage {
  return { event: PhonesUpdatedMessageEvent.PhonesUpdated, link, offers: [], pairings: [], ...overrides };
}

function offerAt(expiresAt: Date): PhonePairingOffer {
  return { profile_id: DEFAULT_PROFILE_ID, expires_at: expiresAt.toISOString(), qr: '{"v":1,"pairing_id":"p1","secret":"s"}' };
}

const phone: PhonePairing = {
  device_id: 'device-1', profile_id: DEFAULT_PROFILE_ID, name: 'Victor’s iPhone',
  created_at: '2026-11-02T10:00:00Z', last_seen_at: '2026-11-02T10:00:00Z', connected: true,
};

async function openPhones(script: (daemon: ScriptedDaemon) => void = () => {}): Promise<ScriptedDaemon> {
  return openSection('phones', { settings: { phone_access_enabled: 'true' } }, (daemon) => {
    daemon.on('phones_get', () => phones());
    script(daemon);
  });
}

const profileSection = () => screen.getByRole('region', { name: 'Phones for Default' });

describe('Settings → Phones', () => {
  it('shows the link, a QR code for the profile, then the paired phone, and revokes it', async () => {
    const offer = offerAt(new Date(Date.now() + 10 * 60_000));
    const daemon = await openPhones((d) => {
      d.on('phone_pair_begin', () => [{ event: 'phone_pair_begin_result', success: true, offer }, phones({ offers: [offer] })]);
      d.on('phone_pairing_revoke', () => [{ event: 'phone_pairing_revoke_result', success: true }, phones()]);
    });
    expect(screen.getByTestId('phones-link-state')).toHaveTextContent('Connected to Attn Cloud at https://cloud.attn.codes');

    await gesture(daemon, () => fireEvent.click(within(profileSection()).getByRole('button', { name: 'Pair a phone' })));
    expect(daemon.sentOf('phone_pair_begin').map(({ profile_id }) => profile_id)).toEqual([DEFAULT_PROFILE_ID]);
    expect(within(profileSection()).getByRole('img', { name: 'Pairing code for Default' })).toBeInTheDocument();

    await gesture(daemon, () => daemon.emit(phones({ pairings: [phone] })));
    expect(within(profileSection()).queryByRole('img', { name: 'Pairing code for Default' })).toBeNull();
    const paired = within(profileSection()).getByRole('listitem', { name: 'Victor’s iPhone' });
    expect(paired).toHaveTextContent('Connected now');

    await gesture(daemon, () => fireEvent.click(within(paired).getByRole('button', { name: 'Revoke' })));
    expect(daemon.sentOf('phone_pairing_revoke').map(({ device_id, profile_id }) => [device_id, profile_id])).toEqual([['device-1', DEFAULT_PROFILE_ID]]);
    expect(within(profileSection()).queryByRole('listitem', { name: 'Victor’s iPhone' })).toBeNull();
    expect(daemon.sentOf('phones_get')).toHaveLength(1);
  });

  it('says a pairing code expired after its ten minutes', async () => {
    const offer = offerAt(new Date(Date.now() + 10 * 60_000));
    const daemon = await openPhones((d) => d.on('phones_get', () => phones({ offers: [offer] })));
    expect(within(profileSection()).getByRole('img', { name: 'Pairing code for Default' })).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(10 * 60_000));
    expect(within(profileSection()).getByRole('status')).toHaveTextContent('Pairing code expired after 10 min; show a new one.');
    expect(within(profileSection()).getByRole('button', { name: 'Show a new pairing code' })).toBeEnabled();
    expect(daemon.sentOf('phone_pair_begin')).toHaveLength(0);
  });

  it('shows why the link is down and why a pairing code could not be shown', async () => {
    const down = { ...link, state: PhoneLinkState.Connecting, error: 'dialing the relay at wss://cloud.attn.codes/v1/homes/x/home: connection refused' };
    const daemon = await openPhones((d) => {
      d.on('phones_get', () => phones({ link: down }));
      d.on('phone_pair_begin', () => ({ event: 'phone_pair_begin_result', success: false, error: 'this home has not registered with Attn Cloud yet (connection refused); a QR code needs its home id' }));
    });
    expect(screen.getByTestId('phones-link-state')).toHaveTextContent('Last error: dialing the relay at wss://cloud.attn.codes/v1/homes/x/home: connection refused');
    await gesture(daemon, () => fireEvent.click(within(profileSection()).getByRole('button', { name: 'Pair a phone' })));
    expect(within(profileSection()).getByRole('alert')).toHaveTextContent('this home has not registered with Attn Cloud yet');
  });

  it('reads the phones again after the daemon connection comes back', async () => {
    const offer = offerAt(new Date(Date.now() + 10 * 60_000));
    let current = phones({ offers: [offer] });
    const daemon = await openPhones((d) => d.on('phones_get', () => current));
    expect(within(profileSection()).getByRole('img', { name: 'Pairing code for Default' })).toBeInTheDocument();

    current = phones({ pairings: [{ ...phone, connected: false }] });
    await daemon.reconnect();
    await daemon.idle();
    expect(within(profileSection()).queryByRole('img', { name: 'Pairing code for Default' })).toBeNull();
    expect(within(profileSection()).getByRole('listitem', { name: 'Victor’s iPhone' })).toHaveTextContent('Last seen');
    expect(daemon.sentOf('phones_get')).toHaveLength(2);
  });
});
