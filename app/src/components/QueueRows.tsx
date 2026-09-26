import type { MouseEvent as ReactMouseEvent } from 'react';
import { StateIndicator } from './StateIndicator';
import { SessionLabel } from './SessionLabel';
import { HarnessIcon } from './HarnessIcon';
import { harnessLabel } from './harnessLabel';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
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
    <div className="queue-row-controls">
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
        <div className="session-actions">
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
        </div>
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
      className={`session-item queue-row ${selected ? 'selected' : ''}`.trim()}
      data-testid={`${testIdPrefix}-${session.id}`}
      data-session-id={session.id}
      data-state={session.state}
      data-workspace-id={row.workspaceId}
    >
      <QueueSessionSelection
        session={session}
        label={session.label}
        testId={`queue-select-${session.id}`}
        onSelect={onSelect}
      />
      <span className="sidebar-session-identity">
        <span className="sidebar-session-headline">
          <HarnessIcon agent={session.agent} />
          <SessionLabel
            label={session.label}
            session={session}
            hasDelegates={delegates.length > 0}
          />
        </span>
        <SessionProvenance automation={session.automation} density="compact" />
      </span>
      {session.chiefOfStaff && <ChiefOfStaffBadge />}
      <DelegationChainTrigger session={session} hasDelegates={delegates.length > 0} />
      {where && <RowWhereChip where={where} />}
      {age && <span className="queue-row-age">{age}</span>}
      {wake && <span className="queue-row-wake-at">{wake}</span>}
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

function RowWhereChip({ where }: { where: RowWhere }) {
  return (
    <span className={`queue-row-where ${where.slot === '—' ? 'is-unplaced' : ''}`.trim()} title={where.title}>
      {where.slot}
    </span>
  );
}

interface CrewRowProps {
  member: string;
  row?: QueueRow<QueueBandSessionView>;
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
      selected={props.selected}
      onWake={props.onWake}
      onOpenMemberActions={props.onOpenMemberActions}
    />
  );
}

function SleepingCrewRow({
  member,
  selected,
  onWake,
  onOpenMemberActions,
}: Pick<CrewRowProps, 'member' | 'selected' | 'onWake' | 'onOpenMemberActions'>) {
  const { phase, trigger, rowRef } = useWakeConfirm(onWake);
  const armed = phase === 'armed';
  const name = crewDisplayName(member);
  const wakeLabel = armed ? `Wake ${name} — click again to confirm` : `Wake ${name}`;
  return (
    <div
      ref={rowRef}
      className={`session-item queue-row queue-row--crew ${selected ? 'selected' : ''}`.trim()}
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
      <span className="crew-asleep-dot" aria-hidden="true" />
      <SessionLabel label={name} />
      <span className="crew-row-mark" title={`${name} is asleep`}>
        asleep
      </span>
      {(onWake || onOpenMemberActions) && (
        <div className="queue-row-controls">
          {armed && <span className="crew-wake-confirm">confirm</span>}
          {onWake && (
            <button
              type="button"
              className="queue-row-wake"
              data-testid={`queue-crew-wake-${member}`}
              title={armed ? `Click again to wake ${name}` : `Wake ${name} — start its day`}
              aria-label={wakeLabel}
              onClick={trigger}
            >
              <CrewWakeSun phase={phase} />
            </button>
          )}
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
      className={`session-item queue-row queue-row--crew ${selected ? 'selected' : ''}`.trim()}
      data-testid={`queue-crew-${member}`}
      data-crew-member={member}
      data-crew-state="awake"
      data-session-id={session.id}
      data-state={session.state}
      data-workspace-id={row.workspaceId}
    >
      <QueueSessionSelection
        session={session}
        label={label}
        testId={`queue-crew-select-${member}`}
        onSelect={onSelect}
      />
      <HarnessIcon agent={session.agent} />
      <SessionLabel label={label} session={session} hasDelegates={delegates.length > 0} />
      <span className="crew-row-mark" title={`${name} is awake`}>
        crew
      </span>
      <DelegationChainTrigger session={session} hasDelegates={delegates.length > 0} />
      {(onSleep || onOpenActions) && (
        <div className="queue-row-controls">
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
              className="session-actions session-more-btn"
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
  session,
  label,
  testId,
  onSelect,
}: {
  session: QueueBandSessionView;
  label: string;
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
        title={harnessLabel(session.agent)}
        onClick={onSelect}
      />
      <StateIndicator
        state={session.state}
        size="md"
        seed={session.id}
        reason={session.state_reason}
      />
    </>
  );
}
