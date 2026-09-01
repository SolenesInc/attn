// While recording, the global shortcut dispatcher is suspended across every step,
// so capturing a combo never fires its action.

import { useEffect, useRef, useState } from 'react';
import { Binding, Combo, Chord } from '../shortcuts/registry';
import { formatShortcut, shortcutTokens } from '../shortcuts/formatShortcut';
import { eventToBinding, isRiskyBinding } from '../shortcuts/resolver';
import { setShortcutCaptureSuspended } from '../shortcuts/useShortcut';
import { KeyCombo } from './Keycap';
import './KeyCaptureInput.css';
import { modifierGlyphs } from '../shortcuts/platform';

export type CaptureMode = 'combo' | 'chord';

interface KeyCaptureInputProps {
  binding: Binding | null;
  recording: boolean;
  mode: CaptureMode;
  onStart: () => void;
  onStartChord: () => void;
  onCapture: (def: Combo) => void;
  onCaptureChord: (chord: Chord) => void;
  onCancel: () => void;
}

export function KeyCaptureInput({
  binding,
  recording,
  mode,
  onStart,
  onStartChord,
  onCapture,
  onCaptureChord,
  onCancel,
}: KeyCaptureInputProps) {
  const glyphs = modifierGlyphs();
  const [error, setError] = useState<string | null>(null);
  const [leader, setLeader] = useState<Combo | null>(null);

  const onCaptureRef = useRef(onCapture);
  onCaptureRef.current = onCapture;
  const onCaptureChordRef = useRef(onCaptureChord);
  onCaptureChordRef.current = onCaptureChord;
  const onCancelRef = useRef(onCancel);
  onCancelRef.current = onCancel;
  const modeRef = useRef(mode);
  modeRef.current = mode;
  const leaderRef = useRef(leader);
  leaderRef.current = leader;

  useEffect(() => {
    if (!recording) {
      setError(null);
      setLeader(null);
      return;
    }
    setShortcutCaptureSuspended(true);
    const handle = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.key === 'Escape') {
        onCancelRef.current();
        return;
      }
      const result = eventToBinding(e);
      if (result.kind === 'ignored') return;
      if (result.kind === 'error') {
        setError(result.message);
        return;
      }
      if (modeRef.current === 'combo') {
        onCaptureRef.current(result.def);
        return;
      }
      if (!leaderRef.current) {
        if (isRiskyBinding(result.def)) {
          setError(`A chord leader needs a ${glyphs.accel} or ${glyphs.alt} modifier.`);
          return;
        }
        setError(null);
        setLeader(result.def);
        return;
      }
      onCaptureChordRef.current({ leader: leaderRef.current, then: result.def });
    };
    window.addEventListener('keydown', handle, true);
    return () => {
      window.removeEventListener('keydown', handle, true);
      setShortcutCaptureSuspended(false);
    };
  }, [recording]);

  if (recording) {
    return (
      <span className="key-capture key-capture--recording">
        <span className="key-capture-prompt">
          {mode === 'chord' && leader ? (
            <>
              <KeyCombo tokens={shortcutTokens(leader)} /> then…
            </>
          ) : mode === 'chord' ? (
            'Press leader…'
          ) : (
            'Press keys…'
          )}
          <span className="key-capture-esc">Esc to cancel</span>
        </span>
        {error && <span className="key-capture-error">{error}</span>}
      </span>
    );
  }

  return (
    <span className="key-capture-buttons">
      <button
        type="button"
        className={`key-capture-button${binding && isRiskyBinding(binding) ? ' key-capture-button--risky' : ''}`}
        onClick={onStart}
        title={binding && isRiskyBinding(binding)
          ? `No ${glyphs.accel}/${glyphs.alt} modifier — this may collide with typing in the terminal`
          : 'Click to rebind'}
      >
        {binding
          ? <KeyCombo tokens={shortcutTokens(binding)} />
          : <span className="key-capture-empty">Unassigned</span>}
      </button>
      <button
        type="button"
        className="key-capture-chord-btn"
        onClick={onStartChord}
        title={`Record a leader-key chord (e.g. ${formatShortcut('ui.actionMenu')} then D)`}
        aria-label="Record a chord"
      >
        chord
      </button>
    </span>
  );
}
