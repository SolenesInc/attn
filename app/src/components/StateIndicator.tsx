import './StateIndicator.css';
import { pickSessionEmoji } from '../utils/sessionEmoji';
import type { UISessionState } from '../types/sessionState';
import { describeUnknownReason } from './stateReason';

type StateIndicatorState = UISessionState;
type StateIndicatorSize = 'sm' | 'md' | 'lg';
type StateIndicatorKind = 'session' | 'pr';

interface StateIndicatorProps {
  state: StateIndicatorState;
  size?: StateIndicatorSize;
  kind?: StateIndicatorKind;
  seed?: string;
  className?: string;
  /** The resolver clause behind this state, from the daemon's `state_reason`. */
  reason?: string;
}

export function StateIndicator({
  state,
  size = 'md',
  kind = 'session',
  seed,
  className = '',
  reason,
}: StateIndicatorProps) {
  const stateClass = state.replace('_', '-');
  const launchingEmoji = state === 'launching' ? pickSessionEmoji(seed ?? '') : null;
  // Only `unknown` gets a tooltip; every other state says what it means by name.
  const explanation = state === 'unknown' ? describeUnknownReason(reason) : undefined;

  return (
    <span
      className={`state-indicator state-indicator--${size} state-indicator--${stateClass} state-indicator--${kind} ${className}`.trim()}
      data-testid="state-indicator"
      title={explanation}
      aria-label={
        state === 'unknown'
          ? (explanation ?? 'state unknown')
          : state === 'scheduled'
            ? 'scheduled'
            : undefined
      }
    >
      {launchingEmoji}
    </span>
  );
}
