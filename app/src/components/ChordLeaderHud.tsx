
import { useSyncExternalStore } from 'react';
import { subscribeChord, getChordSnapshot } from '../shortcuts/chordState';
import { shortcutTokens } from '../shortcuts/formatShortcut';
import { KeyCombo } from './Keycap';
import './ChordLeaderHud.css';

export function ChordLeaderHud() {
  const snapshot = useSyncExternalStore(subscribeChord, getChordSnapshot, getChordSnapshot);
  if (!snapshot.leader) return null;
  return (
    <div className="chord-leader-hud" role="status" data-testid="chord-leader-hud">
      <KeyCombo tokens={shortcutTokens(snapshot.leader)} />
      <span className="chord-leader-hud-then">then…</span>
    </div>
  );
}
