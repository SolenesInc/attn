import type { ReactNode } from 'react';
import type { UISessionState } from '../types/sessionState';
import { pickSessionEmoji } from '../utils/sessionEmoji';
import { HarnessIcon } from './HarnessIcon';
import { StateIndicator } from './StateIndicator';
import { harnessLabel } from './harnessLabel';
import { describeUnknownReason } from './stateReason';
import './SessionLead.css';

export function SessionLead({ agent, state, reason, seed, badge }: {
  agent?: string;
  state: UISessionState;
  reason?: string;
  seed: string;
  badge?: ReactNode;
}) {
  const explanation = state === 'unknown' ? describeUnknownReason(reason) : undefined;
  const name = harnessLabel(agent);
  return (
    <span className="session-lead" data-state={state} title={explanation}>
      {state === 'launching' ? (
        <span className="session-lead-emoji" aria-label="launching">{pickSessionEmoji(seed)}</span>
      ) : (
        <HarnessIcon
          agent={agent}
          ariaLabel={`${name} · ${explanation ?? state.replace('_', ' ')}`}
          title={explanation ? `${name} · ${explanation}` : undefined}
        />
      )}
      <StateIndicator state={state} seed={seed} reason={reason} />
      {badge && <span className="session-lead-badge">{badge}</span>}
    </span>
  );
}
