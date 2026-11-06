import { useEffect, useMemo, useRef, useState } from 'react';
import { encode } from 'uqr';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { usePhonesStore } from '../store/phones';
import { useProfilesStore } from '../store/profiles';
import type { PhoneLink, PhonePairing, PhonePairingOffer } from '../types/generated';

const PAIRING_LIFETIME_MINUTES = 10;

function QrCode({ text, label }: { text: string; label: string }) {
  const { size, path } = useMemo(() => {
    const { data, size } = encode(text, { ecc: 'M', border: 2 });
    let path = '';
    data.forEach((row, y) => row.forEach((dark, x) => { if (dark) path += `M${x} ${y}h1v1h-1z`; }));
    return { size, path };
  }, [text]);
  return (
    <svg className="settings-phone-qr" role="img" aria-label={label} viewBox={`0 0 ${size} ${size}`} shapeRendering="crispEdges">
      <rect width={size} height={size} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  );
}

function linkText(link: PhoneLink): { text: string; tone: string } {
  switch (link.state) {
    case 'connected':
      return { text: `Connected to Attn Cloud at ${link.cloud_url}`, tone: 'good' };
    case 'connecting':
      return link.error
        ? { text: `Reconnecting to Attn Cloud at ${link.cloud_url}. Last error: ${link.error}`, tone: 'bad' }
        : { text: `Connecting to Attn Cloud at ${link.cloud_url}…`, tone: '' };
    default:
      return link.error
        ? { text: link.error, tone: 'bad' }
        : { text: 'Phone access is off, so this home holds no connection to Attn Cloud.', tone: '' };
  }
}

function useExpired(expiresAt: string | undefined): boolean {
  const [expired, setExpired] = useState(false);
  useEffect(() => {
    if (!expiresAt) return;
    const left = new Date(expiresAt).getTime() - Date.now();
    setExpired(left <= 0);
    if (left <= 0) return;
    const timer = window.setTimeout(() => setExpired(true), left);
    return () => window.clearTimeout(timer);
  }, [expiresAt]);
  return expired;
}

function PairingCode({ offer, profileName, onCancel }: { offer: PhonePairingOffer; profileName: string; onCancel: () => void }) {
  const expired = useExpired(offer.expires_at);
  if (expired) {
    return <p className="settings-warning" role="status">Pairing code expired after {PAIRING_LIFETIME_MINUTES} min; show a new one.</p>;
  }
  return (
    <div className="settings-phone-code">
      <QrCode text={offer.qr} label={`Pairing code for ${profileName}`} />
      <p className="settings-description">
        Scan with the attn app. The code works once, until {new Date(offer.expires_at).toLocaleTimeString()}.
      </p>
      <button type="button" className="settings-action" onClick={onCancel}>Cancel</button>
    </div>
  );
}

function PairedPhone({ pairing, onRevoke }: { pairing: PhonePairing; onRevoke: () => void }) {
  return (
    <li className="settings-row-card" aria-label={pairing.name}>
      <div>
        <p className="settings-row-title">{pairing.name}</p>
        <p className="settings-row-copy">
          {pairing.connected
            ? 'Connected now'
            : <>Last seen <time dateTime={pairing.last_seen_at}>{new Date(pairing.last_seen_at).toLocaleString()}</time></>}
        </p>
      </div>
      <button type="button" className="settings-action" onClick={onRevoke}>Revoke</button>
    </li>
  );
}

export function PhonesSettings() {
  const { sendPhonesGet, sendPhonePairBegin, sendPhonePairCancel, sendPhonePairingRevoke, connectionGeneration } = useDaemonApi();
  const phones = usePhonesStore((state) => state.phones);
  const profiles = useProfilesStore((state) => state.profiles);
  const [errors, setErrors] = useState<Record<string, string>>({});

  const offeredProfiles = useRef<string[]>([]);
  offeredProfiles.current = phones?.offers.map((offer) => offer.profile_id) ?? [];
  const left = useRef(false);

  useEffect(() => { sendPhonesGet(); }, [sendPhonesGet, connectionGeneration]);

  useEffect(() => {
    left.current = false;
    return () => {
      left.current = true;
      offeredProfiles.current.forEach(cancelQuietly);
    };
    function cancelQuietly(profileId: string) {
      sendPhonePairCancel(profileId).catch((error: unknown) => console.warn('[phones] cancelling on leave:', error));
    }
  }, [sendPhonePairCancel]);

  function failed(profileId: string, error: unknown) {
    setErrors((current) => ({ ...current, [profileId]: error instanceof Error ? error.message : String(error) }));
  }

  function pair(profileId: string) {
    setErrors((current) => ({ ...current, [profileId]: '' }));
    sendPhonePairBegin(profileId)
      .then(() => { if (left.current) return sendPhonePairCancel(profileId); })
      .catch((error: unknown) => failed(profileId, error));
  }

  function cancel(profileId: string) {
    sendPhonePairCancel(profileId).catch((error: unknown) => failed(profileId, error));
  }

  function revoke(pairing: PhonePairing) {
    sendPhonePairingRevoke(pairing.device_id, pairing.profile_id).catch((error: unknown) => failed(pairing.profile_id, error));
  }

  if (!phones) return <p className="settings-description">Loading phones…</p>;
  const link = linkText(phones.link);
  return (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <h3>Attn Cloud</h3>
          <p className="settings-description">Phones reach this home through Attn Cloud, which relays end-to-end encrypted traffic it cannot read.</p>
        </div>
        <div className="settings-block-body">
          <p className={link.tone === 'bad' ? 'settings-warning' : 'settings-description'} role="status" data-testid="phones-link-state">{link.text}</p>
        </div>
      </section>
      {profiles.map((profile) => {
        const offer = phones.offers.find((o) => o.profile_id === profile.id);
        const paired = phones.pairings.filter((p) => p.profile_id === profile.id);
        return (
          <section key={profile.id} className="settings-block" aria-label={`Phones for ${profile.name}`}>
            <div className="settings-block-intro">
              <h3>{profile.name}</h3>
              <p className="settings-description">A phone you pair here sees this profile's agents and nothing else.</p>
            </div>
            <div className="settings-block-body">
              {paired.length > 0 && (
                <ul className="settings-phone-list">
                  {paired.map((pairing) => <PairedPhone key={pairing.device_id} pairing={pairing} onRevoke={() => revoke(pairing)} />)}
                </ul>
              )}
              {offer && <PairingCode offer={offer} profileName={profile.name} onCancel={() => cancel(profile.id)} />}
              {errors[profile.id] && <p className="settings-warning" role="alert">{errors[profile.id]}</p>}
              <button type="button" className="settings-action" disabled={phones.link.state === 'off'} onClick={() => pair(profile.id)}>
                {offer ? 'Show a new pairing code' : 'Pair a phone'}
              </button>
            </div>
          </section>
        );
      })}
    </>
  );
}
