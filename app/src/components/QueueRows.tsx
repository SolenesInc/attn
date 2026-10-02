import type { MouseEvent as ReactMouseEvent } from 'react';
import { SessionLabel } from './SessionLabel';
import { harnessLabel } from './harnessLabel';
import { SessionLead } from './SessionLead';
import { SidebarSettlingBar } from './SettlingIndicator';
import { CrewWakeSun, useWakeConfirm } from './CrewWake';
import { formatShortcut } from '../shortcuts/formatShortcut';
import type { UISessionState } from '../types/sessionState';
import type { QueueRow } from '../utils/queueBands';
import { crewDisplayName } from '../utils/crewName';
import type {
  AutomationProvenance as AutomationProvenanceValue,
  SessionDelegationRole,
} from '../types/generated';
import { SessionProvenance } from './SessionProvenance';
import { DelegationChainTrigger } from './DelegationChain';
import './SidebarRow.css';
import './QueueRows.css';

export interface QueueBandSessionView {
  id: string;
  agent?: string;
  label: string;
  state: UISessionState;
  state_reason?: string;
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  autoSettleFiresAt?: string;
  autoSettleHeld?: boolean;
  crewMember?: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
  delegation_role?: SessionDelegationRole;
  automation?: AutomationProvenanceValue;
}

export interface CrewMemberView {
  launch_desktop?: { label?: string };
  id: string;
  binding_session?: string;
}

function QueueRowControls({
  session,
  onSettle,
  onSnooze,
  onWake,
  onOpenActions,
}: {
  session: Pick<QueueBandSessionView, 'id' | 'label'>;
  onSettle?: () => void;
  onSnooze?: (event: ReactMouseEvent) => void;
  onWake?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
}) {
  if (!onOpenActions && !onSettle && !onSnooze && !onWake) return null;

  return (
    <div className="session-actions">
      {onWake && (
        <button
          type="button"
          className="queue-row-wake"
          data-testid={`queue-wake-${session.id}`}
          title="Wake now — bring it back to the queue"
          aria-label={`Wake ${session.label}`}
          onClick={(event) => {
            event.stopPropagation();
            onWake();
          }}
        >
          ↩
        </button>
      )}
      {onSnooze && (
        <button
          type="button"
          className="queue-row-snooze"
          data-testid={`queue-snooze-${session.id}`}
          title={`Snooze this agent (${formatShortcut('session.snooze')})`}
          aria-label={`Snooze ${session.label}`}
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
          aria-label={`Actions for ${session.label}`}
        >
          •••
        </button>
      )}
      {onSettle && (
        <button
          type="button"
          className="queue-row-settle"
          data-testid={`queue-settle-${session.id}`}
          title={`Settle this turn (${formatShortcut('session.settle')})`}
          aria-label={`Settle ${session.label}`}
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
  wake,
  onSelect,
  onSettle,
  onSnooze,
  onWake,
  onOpenActions,
  showSettling,
  delegates,
  testIdPrefix,
}: {
  row: QueueRow<QueueBandSessionView>;
  selected: boolean;
  where?: RowWhere;
  age?: string;
  wake?: string;
  onSelect: () => void;
  onSettle?: () => void;
  onSnooze?: (event: ReactMouseEvent) => void;
  onWake?: () => void;
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
      data-desktop-id={row.desktopId}
      title={session.chiefOfStaff ? session.label : undefined}
    >
      <QueueSessionSelection
        label={session.chiefOfStaff ? 'Chief' : session.label}
        title={[session.chiefOfStaff ? session.label : harnessLabel(session.agent), where?.title].filter(Boolean).join(' · ')}
        testId={`queue-select-${session.id}`}
        onSelect={onSelect}
      />
      <SessionLead
        agent={session.agent}
        state={session.state}
        reason={session.state_reason}
        seed={session.id}
        badge={where && <span className="queue-lead-badge" title={where.title}>{where.slot}</span>}
      />
      <span className="sidebar-session-identity">
        <SessionLabel
          label={session.chiefOfStaff ? 'Chief' : session.label}
          session={session}
          hasDelegates={delegates.length > 0}
        />
        <SessionProvenance automation={session.automation} density="compact" />
      </span>
      <span className="session-trailing">
        <DelegationChainTrigger session={session} hasDelegates={delegates.length > 0} />
        {session.chiefOfStaff && <span className="queue-chief-glyph" title="Chief of staff" aria-label="Chief of staff">⌁</span>}
        {age && <span className="queue-row-age">{age}</span>}
        {wake && <span className="queue-row-wake-at">{wake}</span>}
      </span>
      <QueueRowControls
        session={session}
        onSettle={onSettle}
        onSnooze={onSnooze}
        onWake={onWake}
        onOpenActions={onOpenActions}
      />
      {showSettling && (session.autoSettleFiresAt || session.autoSettleHeld) && (
        <SidebarSettlingBar firesAt={session.autoSettleFiresAt} held={session.autoSettleHeld} />
      )}
    </div>
  );
}

export interface RowWhere {
  slot: string;
  title: string;
}

interface CrewRowProps {
  desktopLabel?: string;
  member: string;
  row?: QueueRow<QueueBandSessionView>;
  where?: RowWhere;
  selected: boolean;
  onSelect?: () => void;
  onWake?: () => void;
  onSleep?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
  onOpenMemberActions?: (event: ReactMouseEvent<HTMLButtonElement>) => void;
  delegates: readonly QueueBandSessionView[];
}

export function CrewRowView(props: CrewRowProps) {
  return props.row ? (
    <AwakeCrewRow {...props} row={props.row} />
  ) : (
    <SleepingCrewRow
      member={props.member}
      desktopLabel={props.desktopLabel}
      selected={props.selected}
      onWake={props.onWake}
      onOpenMemberActions={props.onOpenMemberActions}
    />
  );
}

function SleepingCrewRow({
  member,
  desktopLabel,
  selected,
  onWake,
  onOpenMemberActions,
}: Pick<CrewRowProps, 'member' | 'selected' | 'onWake' | 'onOpenMemberActions' | 'desktopLabel'>) {
  const { phase, trigger, rowRef } = useWakeConfirm(onWake);
  const armed = phase === 'armed';
  const name = crewDisplayName(member);
  const wakeLabel = armed ? `Wake ${name} — click again to confirm` : `Wake ${name}`;
  return (
    <div
      ref={rowRef}
      className={`session-item sidebar-leaf-row queue-row queue-row--crew ${selected ? 'selected' : ''}`.trim()}
      data-testid={`queue-crew-${member}`}
      data-crew-member={member}
      data-crew-state="asleep"
      data-crew-wake={phase === 'rest' ? undefined : phase}
    >
      <button
        type="button"
        className="queue-row-select"
        data-testid={`queue-crew-select-${member}`}
        aria-label={wakeLabel}
        onClick={trigger}
        disabled={!onWake}
      />
      <button
        type="button"
        className="queue-crew-sun"
        data-testid={`queue-crew-wake-${member}`}
        title={armed ? `Click again to wake ${name}` : `Wake ${name} — start its day`}
        aria-label={wakeLabel}
        onClick={trigger}
        disabled={!onWake}
      >
        <CrewWakeSun phase={phase} />
      </button>
      <SessionLabel label={name} />
      {desktopLabel && <span className="queue-crew-desktop" title={desktopLabel}>{desktopLabel}</span>}
      <span className="session-trailing" />
      {(onWake || onOpenMemberActions) && (
        <div className="session-actions">
          {armed && <span className="crew-wake-confirm">confirm</span>}
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

function AwakeCrewRow({
  member,
  row,
  where,
  selected,
  onSelect,
  onSleep,
  onOpenActions,
  delegates,
}: CrewRowProps & {
  row: QueueRow<QueueBandSessionView>;
}) {
  const name = crewDisplayName(member);
  const { session } = row;
  const label = session.label || name;
  return (
    <div
      className={`session-item sidebar-leaf-row queue-row queue-row--crew ${selected ? 'selected' : ''}`.trim()}
      data-testid={`queue-crew-${member}`}
      data-crew-member={member}
      data-crew-state="awake"
      data-session-id={session.id}
      data-state={session.state}
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
        reason={session.state_reason}
        seed={session.id}
        badge={where && <span className="queue-lead-badge" title={where.title}>{where.slot}</span>}
      />
      <SessionLabel label={label} session={session} hasDelegates={delegates.length > 0} />
      <span className="session-trailing">
        <DelegationChainTrigger session={session} hasDelegates={delegates.length > 0} />
      </span>
      {(onSleep || onOpenActions) && (
        <div className="session-actions">
          {onSleep && (
            <button
              type="button"
              className="queue-row-sleep"
              data-testid={`queue-crew-sleep-${member}`}
              title={`Ask ${name} to close its day and sleep`}
              aria-label={`Ask ${name} to sleep`}
              onClick={onSleep}
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
