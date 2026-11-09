import { sessionDisplayName } from '../utils/sessionDisplayName';
import type { MouseEvent as ReactMouseEvent } from 'react';
import { SessionLabel } from './SessionLabel';
import { harnessLabel } from './harnessLabel';
import { SessionLead } from './SessionLead';
import { SidebarSettlingBar } from './SettlingIndicator';
import { CrewStartSun, useStartConfirm } from './CrewStart';
import { formatShortcut } from '../shortcuts/formatShortcut';
import type { UISessionState } from '../types/sessionState';
import type { QueueRow } from '../utils/queueBands';
import type {
  ActorView,
  AutomationProvenance as AutomationProvenanceValue,
  SessionDelegationRole,
} from '../types/generated';
import { SessionProvenance } from './SessionProvenance';
import { DelegationChainTrigger } from './DelegationChain';
import { AskMarker } from './Asks/AskMarker';
import './SidebarRow.css';
import './QueueRows.css';

export interface QueueBandSessionView {
  id: string;
  agent?: string;
  label: string;
  state: UISessionState;
  offline?: boolean;
  state_reason?: string;
  chiefOfStaff?: boolean;
  priority?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  autoSettleFiresAt?: string;
  autoSettleHeld?: boolean;
  crewMember?: string;
  dispatcher_session_id?: string;
  dispatcher?: ActorView;
  delegation_role?: SessionDelegationRole;
  automation?: AutomationProvenanceValue;
}

export interface CrewMemberView {
  launch_desktop?: { label?: string };
  id: string;
  name: string;
  resolved_agent?: string;
  binding_session?: string;
}

function QueueRowControls({
  session,
  onSettle,
  onSnooze,
  onUnsnooze,
  onOpenActions,
}: {
  session: Pick<QueueBandSessionView, 'id' | 'label' | 'offline'>;
  onSettle?: () => void;
  onSnooze?: (event: ReactMouseEvent) => void;
  onUnsnooze?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
}) {
  if (!onOpenActions && !onSettle && !onSnooze && !onUnsnooze) return null;

  return (
    <div className="session-actions">
      {onUnsnooze && !session.offline && (
        <button
          type="button"
          className="queue-row-unsnooze"
          data-testid={`queue-unsnooze-${session.id}`}
          title="Unsnooze — bring it back to the queue"
          aria-label={`Unsnooze ${sessionDisplayName(session)}`}
          onClick={(event) => {
            event.stopPropagation();
            onUnsnooze();
          }}
        >
          ↩
        </button>
      )}
      {onSnooze && !session.offline && (
        <button
          type="button"
          className="queue-row-snooze"
          data-testid={`queue-snooze-${session.id}`}
          title={`Snooze this agent (${formatShortcut('session.snooze')})`}
          aria-label={`Snooze ${sessionDisplayName(session)}`}
          onClick={(event) => {
            event.stopPropagation();
            onSnooze(event);
          }}
        >
          ☾
        </button>
      )}
      {onOpenActions && (
        <button
          type="button"
          className="session-action-btn session-more-btn"
          data-testid={`session-actions-${session.id}`}
          onClick={onOpenActions}
          title="Session actions"
          aria-label={`Actions for ${sessionDisplayName(session)}`}
        >
          •••
        </button>
      )}
      {onSettle && !session.offline && (
        <button
          type="button"
          className="queue-row-settle"
          data-testid={`queue-settle-${session.id}`}
          title={`Settle this turn (${formatShortcut('session.settle')})`}
          aria-label={`Settle ${sessionDisplayName(session)}`}
          onClick={(event) => {
            event.stopPropagation();
            onSettle();
          }}
        >
          ✓
        </button>
      )}
    </div>
  );
}

export function QueueRowView({
  row,
  selected,
  where,
  age,
  snoozedUntil,
  onSelect,
  onSettle,
  onSnooze,
  onUnsnooze,
  onOpenActions,
  showSettling,
  delegates,
  testIdPrefix,
}: {
  row: QueueRow<QueueBandSessionView>;
  selected: boolean;
  where?: RowWhere;
  age?: string;
  snoozedUntil?: string;
  onSelect: () => void;
  onSettle?: () => void;
  onSnooze?: (event: ReactMouseEvent) => void;
  onUnsnooze?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
  showSettling?: boolean;
  delegates: readonly QueueBandSessionView[];
  testIdPrefix: string;
}) {
  const { session } = row;
  return (
    <div
      className={`session-item sidebar-leaf-row queue-row ${selected ? 'selected' : ''}`.trim()}
      data-testid={`${testIdPrefix}-${session.id}`}
      data-session-id={session.id}
      data-state={session.state}
      data-offline={session.offline || undefined}
      data-desktop-id={row.desktopId}
      title={session.chiefOfStaff ? sessionDisplayName(session) : undefined}
    >
      <QueueSessionSelection
        label={session.chiefOfStaff ? 'Chief' : sessionDisplayName(session)}
        title={[session.chiefOfStaff ? sessionDisplayName(session) : harnessLabel(session.agent), where?.title].filter(Boolean).join(' · ')}
        testId={`queue-select-${session.id}`}
        onSelect={onSelect}
      />
      <SessionLead
        agent={session.agent}
        state={session.state}
        offline={session.offline}
        reason={session.state_reason}
        seed={session.id}
        badge={where && <span className="queue-lead-badge" title={where.title}>{where.slot}</span>}
      />
      <span className="sidebar-session-identity">
        <SessionLabel
          label={session.chiefOfStaff ? 'Chief' : sessionDisplayName(session)}
          session={session}
          hasDelegates={delegates.length > 0}
        />
        <SessionProvenance automation={session.automation} density="compact" />
      </span>
      <span className="session-trailing">
        <DelegationChainTrigger session={session} hasDelegates={delegates.length > 0} />
        {session.chiefOfStaff && <span className="queue-chief-glyph" title="Chief of staff" aria-label="Chief of staff">⌁</span>}
        <AskMarker session={session} />
        {age && <span className="queue-row-age">{age}</span>}
        {snoozedUntil && <span className="queue-row-snoozed-until">{snoozedUntil}</span>}
      </span>
      <QueueRowControls
        session={session}
        onSettle={onSettle}
        onSnooze={onSnooze}
        onUnsnooze={onUnsnooze}
        onOpenActions={onOpenActions}
      />
      {showSettling && (session.autoSettleFiresAt || session.autoSettleHeld) && (
        <SidebarSettlingBar firesAt={session.offline ? undefined : session.autoSettleFiresAt} held={session.autoSettleHeld || session.offline} />
      )}
    </div>
  );
}

export interface RowWhere {
  slot: string;
  title: string;
}

interface CrewRowProps {
  chief?: boolean;
  closing?: boolean;
  agent?: string;
  member: string;
  name: string;
  row?: QueueRow<QueueBandSessionView>;
  where?: RowWhere;
  selected: boolean;
  onSelect?: () => void;
  onStart?: () => void;
  onAskClose?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
  onOpenMemberActions?: (event: ReactMouseEvent<HTMLButtonElement>) => void;
  delegates: readonly QueueBandSessionView[];
}

export function CrewRowView(props: CrewRowProps) {
  return props.row ? (
    <CrewRowInSession {...props} row={props.row} />
  ) : (
    <CrewRowBetweenSessions
      chief={props.chief}
      member={props.member}
      name={props.name}
      agent={props.agent}
      selected={props.selected}
      onStart={props.onStart}
      onOpenMemberActions={props.onOpenMemberActions}
    />
  );
}

function CrewRowBetweenSessions({
  chief,
  member,
  name,
  agent,
  selected,
  onStart,
  onOpenMemberActions,
}: Pick<CrewRowProps, 'chief' | 'member' | 'name' | 'selected' | 'onStart' | 'onOpenMemberActions' | 'agent'>) {
  const { phase, trigger, rowRef } = useStartConfirm(onStart, chief);
  const armed = phase === 'armed';
  const startLabel = armed ? `Start ${name} — click again to confirm` : `Start ${name}`;
  return (
    <div
      ref={rowRef}
      className={`session-item sidebar-leaf-row queue-row queue-row--crew ${selected ? 'selected' : ''}`.trim()}
      data-testid={`queue-crew-${member}`}
      data-crew-member={member}
      data-crew-state="between-sessions"
      data-crew-start={phase === 'rest' ? undefined : phase}
    >
      <button
        type="button"
        className="queue-row-select"
        data-testid={`queue-crew-select-${member}`}
        aria-label={startLabel}
        onClick={trigger}
        disabled={!onStart}
      />
      <SessionLead agent={agent} state="idle" seed={member} />
      <SessionLabel label={name} />
      <span className="session-trailing">
        {chief && <span className="queue-chief-glyph" title={name}>⌁</span>}
        {chief && <span className="queue-row-age">between sessions</span>}
        <AskMarker session={{ id: '', crewMember: member }} />
        <button
          type="button"
          className="queue-crew-sun"
          data-testid={`queue-crew-start-${member}`}
          title={armed ? `Click again to start ${name}` : `Start ${name}`}
          aria-label={startLabel}
          onClick={trigger}
          disabled={!onStart}
        >
          <CrewStartSun phase={phase} />
        </button>
      </span>
      {(onStart || onOpenMemberActions) && (
        <div className="session-actions">
          {armed && <span className="crew-start-confirm">confirm</span>}
          {onOpenMemberActions && (
            <button
              type="button"
              className="session-action-btn session-more-btn"
              data-testid={`crew-actions-${member}`}
              title={`Actions for ${name}`}
              aria-label={`Actions for ${name}`}
              onClick={(event) => {
                event.stopPropagation();
                onOpenMemberActions(event);
              }}
            >
              •••
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function CrewRowInSession({
  chief,
  closing,
  member,
  name,
  row,
  where,
  selected,
  onSelect,
  onAskClose,
  onOpenActions,
  delegates,
}: CrewRowProps & {
  row: QueueRow<QueueBandSessionView>;
}) {
  const { session } = row;
  const label = chief ? name : sessionDisplayName(session) || name;
  return (
    <div
      className={`session-item sidebar-leaf-row queue-row queue-row--crew ${selected ? 'selected' : ''}`.trim()}
      data-testid={`queue-crew-${member}`}
      data-crew-member={member}
      data-crew-state={closing ? 'closing' : 'in-session'}
      data-session-id={session.id}
      data-state={session.state}
      data-offline={session.offline || undefined}
      data-desktop-id={row.desktopId}
    >
      <QueueSessionSelection
        label={label}
        title={[harnessLabel(session.agent), where?.title].filter(Boolean).join(' · ')}
        testId={`queue-crew-select-${member}`}
        onSelect={onSelect}
      />
      <SessionLead
        agent={session.agent}
        state={session.state}
        offline={session.offline}
        reason={session.state_reason}
        seed={session.id}
        badge={where && <span className="queue-lead-badge" title={where.title}>{where.slot}</span>}
      />
      <SessionLabel label={label} session={session} hasDelegates={delegates.length > 0} />
      <span className="session-trailing">
        {chief && <span className="queue-chief-glyph" title={name}>⌁</span>}
        {closing && <span className="queue-row-age">closing</span>}
        <DelegationChainTrigger session={session} hasDelegates={delegates.length > 0} />
        <AskMarker session={session} />
      </span>
      {(onAskClose || onOpenActions) && (
        <div className="session-actions">
          {onAskClose && (
            <button
              type="button"
              className="queue-row-crew-close"
              data-testid={`queue-crew-close-${member}`}
              title={`Ask ${name} to close`}
              aria-label={`Ask ${name} to close`}
              onClick={onAskClose}
            >
              ☾
            </button>
          )}
          {onOpenActions && (
            <button
              type="button"
              className="session-action-btn session-more-btn"
              data-testid={`session-actions-${session.id}`}
              title="Session actions"
              aria-label={`Actions for ${label}`}
              onClick={onOpenActions}
            >
              •••
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function QueueSessionSelection({
  label,
  title,
  testId,
  onSelect,
}: {
  label: string;
  title: string;
  testId: string;
  onSelect?: () => void;
}) {
  return (
    <>
      <button
        type="button"
        className="queue-row-select"
        data-testid={testId}
        aria-label={`Open ${label}`}
        title={title}
        onClick={onSelect}
      />
    </>
  );
}
