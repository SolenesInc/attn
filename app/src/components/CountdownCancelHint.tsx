import './CountdownCancelHint.css';
import { formatShortcut } from '../shortcuts/formatShortcut';

/** The key that stops a running countdown, rendered inside the countdown's own
 * indicator. `verb` is what stopping means here ("keep" a turn, "stop" a nudge). */
export function CountdownCancelHint({ verb }: { verb: string }) {
  const combo = formatShortcut('session.cancelCountdown');
  if (!combo) return null;
  return (
    <span className="countdown-cancel-hint">
      <kbd className="countdown-cancel-hint-key">{combo}</kbd>
      <span className="countdown-cancel-hint-verb">{verb}</span>
    </span>
  );
}
