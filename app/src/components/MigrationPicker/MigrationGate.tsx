import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useMigrationAutomationBridge } from '../../hooks/useMigrationAutomationBridge';
import { useProfilesStore } from '../../store/profiles';
import { MigrationPhase } from '../../types/generated';
import { MigrationPicker } from './MigrationPicker';
import './MigrationPicker.css';

function MigrationDone({ profileName, onContinue }: { profileName: string; onContinue: () => void }) {
  const continueRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    continueRef.current?.focus();
  }, []);
  return (
    <main className="mp-shell">
      <div className="mp-topline">
        <div className="mp-brand"><span className="mp-brandmark" aria-hidden="true" />attn</div>
      </div>
      <section className="mp-done" aria-labelledby="mp-done-title">
        <div className="mp-done-mark" aria-hidden="true">✓</div>
        <h1 id="mp-done-title">Your <em>{profileName}</em> profile is ready.</h1>
        <p className="mp-done-sub">You can now have different attn profiles, for example, one for work, and one for personal agents.</p>
        <div className="mp-done-actions">
          <button type="button" ref={continueRef} className="mp-button primary" onClick={onContinue}>Continue →</button>
        </div>
      </section>
    </main>
  );
}

export function MigrationGate({ children }: { children: ReactNode }) {
  const phase = useProfilesStore((state) => state.migrationPhase);
  const profileName = useProfilesStore((state) => {
    const id = state.migration?.profile_id;
    return state.profiles.find((profile) => profile.id === id)?.name ?? 'Default';
  });
  const placing = phase === MigrationPhase.PlacementRequired;
  const [sawPlacement, setSawPlacement] = useState(placing);
  const [continued, setContinued] = useState(false);
  if (placing && !sawPlacement) setSawPlacement(true);

  if (placing) return <MigrationScreen><MigrationPicker /></MigrationScreen>;
  if (sawPlacement && !continued) {
    return (
      <MigrationScreen>
        <MigrationDone profileName={profileName} onContinue={() => setContinued(true)} />
      </MigrationScreen>
    );
  }
  return <>{children}</>;
}

function MigrationScreen({ children }: { children: ReactNode }) {
  useMigrationAutomationBridge();
  return <>{children}</>;
}
