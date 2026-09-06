import { type MouseEvent as ReactMouseEvent } from 'react';
import { StateIndicator } from './StateIndicator';
import { SessionLabel } from './SessionLabel';
import { HarnessIcon } from './HarnessIcon';
import { harnessLabel } from './harnessLabel';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { SidebarSettlingBar } from './SettlingIndicator';
import { CrewWakeSun, useWakeConfirm } from './CrewWake';
import { formatShortcut } from '../shortcuts/formatShortcut';
import type { UISessionState } from '../types/sessionState';
import { formatTurnAge, type QueueBands as QueueBandsModel, type QueueRow } from '../utils/queueBands';
import { formatWakeTime } from '../utils/snoozeDurations';
import { crewDisplayName } from '../utils/crewName';
import { useNow, TURN_AGE_TICK_MS } from '../hooks/useNow';
import type { AutomationProvenance as AutomationProvenanceValue } from '../types/generated';
import { SessionProvenance } from './SessionProvenance';

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
  automation?: AutomationProvenanceValue;
}

export interface CrewMemberView {
  id: string;
  /** The session living this member's day. Absent means asleep. */
  binding_session?: string;
}

interface QueueBandsProps {
  bands: QueueBandsModel<QueueBandSessionView>;
  /** Members are permanent rows: an awake one renders from its live session, a sleeping one from this list. */
  crew?: CrewMemberView[];
  /** Start a sleeping member's day. Resolves once its session exists. */
  onWakeCrewMember?: (member: string) => void;
  onSleepCrewMember?: (member: string) => void;
  selectedId: string | null;
  onSelectSession: (id: string) => void;
  onSettleTurn: (id: string) => void;
  /** Sessions whose terminal tile is on screen; a band row draws the auto-settle countdown only for the others. */
  onScreenSessionIds?: ReadonlySet<string>;
  /** Pinning takes this agent out and leaves its workspace and every sibling in. */
  onPinSession?: (sessionId: string, pinned: boolean) => void;
  /** The per-session menu — chief of staff, close, reload — which the workspace tree row owns when the queue is off. */
  onOpenActions?: (session: { id: string; label: string; chiefOfStaff?: boolean }, event: ReactMouseEvent) => void;
  /** Open the duration menu for a row. Offered on settled rows too: deferring a run before it finishes is why snooze exists. */
  onOpenSnooze?: (session: { id: string; label: string }, event: ReactMouseEvent) => void;
}

function QueueRowView({
  row,
  selected,
  age,
  wake,
  onSelect,
  onSettle,
  onSnooze,
  onWake,
  onPin,
  onUnpin,
  onOpenActions,
  showSettling,
  testIdPrefix,
}: {
  row: QueueRow<QueueBandSessionView>;
  selected: boolean;
  age?: string;
  /** When a deferred agent comes back. Only snoozed rows carry one. */
  wake?: string;
  onSelect: () => void;
  onSettle?: () => void;
  onSnooze?: (event: ReactMouseEvent) => void;
  onWake?: () => void;
  onPin?: () => void;
  onUnpin?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
  showSettling?: boolean;
  testIdPrefix: string;
}) {
  const { session } = row;
  return (
    <div
      className={`session-item queue-row ${selected ? 'selected' : ''}`.trim()}
      data-testid={`${testIdPrefix}-${session.id}`}
      data-state={session.state}
      data-workspace-id={row.workspaceId}
    >
      {/* A real button, so the row is reachable by Tab and pressed by Enter or Space; the settle, pin and actions controls sit above it so they stay independently clickable. */}
      <button
        type="button"
        className="queue-row-select"
        data-testid={`queue-select-${session.id}`}
        aria-label={`Open ${session.label}`}
        title={harnessLabel(session.agent)}
        onClick={onSelect}
      />
      <StateIndicator state={session.state} size="md" seed={session.id} reason={session.state_reason} />
      {/* No workspace name in a band row: the label needs every column, and the pin button's tooltip names the workspace. */}
      <span className="sidebar-session-identity">
        <span className="sidebar-session-headline">
          <HarnessIcon agent={session.agent} />
          <SessionLabel label={session.label} />
        </span>
        <SessionProvenance automation={session.automation} density="compact" />
      </span>
      {session.chiefOfStaff && <ChiefOfStaffBadge />}
      {age && <span className="queue-row-age">{age}</span>}
      {wake && <span className="queue-row-wake-at">{wake}</span>}
      {(onOpenActions || onPin || onUnpin || onSettle || onSnooze || onWake) && (
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
          {onPin && (
            <button
              type="button"
              className="queue-row-pin"
              data-testid={`queue-pin-${session.id}`}
              title="Pin this agent — keep it in view, out of the queue"
              aria-label={`Pin ${session.label}`}
              onClick={(event) => {
                event.stopPropagation();
                onPin();
              }}
            >
              📍
            </button>
          )}
          {onUnpin && (
            <button
              type="button"
              className="queue-row-pin"
              data-testid={`queue-unpin-${session.id}`}
              title="Unpin — put this agent back in the queue"
              aria-label={`Unpin ${session.label}`}
              onClick={(event) => {
                event.stopPropagation();
                onUnpin();
              }}
            >
              📌
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
      )}
      {showSettling && (session.autoSettleFiresAt || session.autoSettleHeld) && (
        <SidebarSettlingBar firesAt={session.autoSettleFiresAt} held={session.autoSettleHeld} />
      )}
    </div>
  );
}

// The sidebar's standing order: the chief anchored, the turns the user owes oldest first,
// the settled rest, then the pinned. An agent appears in exactly one, so position carries meaning.
export function QueueBands({
  bands,
  crew,
  onWakeCrewMember,
  onSleepCrewMember,
  selectedId,
  onSelectSession,
  onSettleTurn,
  onScreenSessionIds,
  onPinSession,
  onOpenActions,
  onOpenSnooze,
}: QueueBandsProps) {
  const now = useNow(TURN_AGE_TICK_MS);
  const offScreen = (id: string) => !onScreenSessionIds?.has(id);
  const snoozeHandler = (session: QueueBandSessionView) =>
    onOpenSnooze && ((event: ReactMouseEvent) => onOpenSnooze(session, event));
  const crewInOtherBands = new Set(
    [...bands.turns, ...bands.settled, ...bands.pinned, ...bands.snoozed]
      .flatMap((row) => row.session.crewMember ? [row.session.crewMember] : []),
  );
  const crewRows = buildCrewRows(crew, bands.crew, crewInOtherBands);

  return (
    <div className="queue-bands" data-testid="sidebar-queue">
      {bands.chief && (
        <QueueRowView
          row={bands.chief}
          selected={selectedId === bands.chief.session.id}
          onSelect={() => onSelectSession(bands.chief!.session.id)}
          onOpenActions={onOpenActions && ((event) => onOpenActions(bands.chief!.session, event))}
          testIdPrefix="queue-chief"
        />
      )}
      <div className="queue-band-header">
        <span>Your turn</span>
        {bands.turns.length > 0 && <span className="queue-band-count">{bands.turns.length}</span>}
      </div>
      {bands.turns.length === 0 ? (
        <div className="queue-band-empty" data-testid="queue-empty">Nothing owed.</div>
      ) : (
        bands.turns.map((row) => (
          <QueueRowView
            key={row.session.id}
            row={row}
            selected={selectedId === row.session.id}
            age={formatTurnAge(row.session.turnOpenedAt, now)}
            onSelect={() => onSelectSession(row.session.id)}
            onSettle={() => onSettleTurn(row.session.id)}
            onSnooze={snoozeHandler(row.session)}
            onPin={onPinSession && (() => onPinSession(row.session.id, true))}
            onOpenActions={onOpenActions && ((event) => onOpenActions(row.session, event))}
            showSettling={offScreen(row.session.id)}
            testIdPrefix="queue-turn"
          />
        ))
      )}
      {bands.settled.length > 0 && (
        <>
          <div className="queue-band-header">
            <span>Settled</span>
          </div>
          {bands.settled.map((row) => (
            <QueueRowView
              key={row.session.id}
              row={row}
              selected={selectedId === row.session.id}
              onSelect={() => onSelectSession(row.session.id)}
              onSnooze={snoozeHandler(row.session)}
              onPin={onPinSession && (() => onPinSession(row.session.id, true))}
              onOpenActions={onOpenActions && ((event) => onOpenActions(row.session, event))}
              testIdPrefix="queue-settled"
            />
          ))}
        </>
      )}
      {(bands.pinned.length > 0 || crewRows.length > 0) && (
        <>
          <div className="queue-band-header">
            <span>Pinned</span>
            <span className="queue-band-count">{bands.pinned.length + crewRows.length}</span>
          </div>
          {/* A member is pin-shaped but is not a pin: nobody put it here and there is no unpin. */}
          {crewRows.map((crewRow) => (
            <CrewRowView
              key={crewRow.member}
              member={crewRow.member}
              row={crewRow.row}
              selected={crewRow.row ? selectedId === crewRow.row.session.id : false}
              onSelect={crewRow.row ? () => onSelectSession(crewRow.row!.session.id) : undefined}
              onWake={onWakeCrewMember && (() => onWakeCrewMember(crewRow.member))}
              onSleep={crewRow.row && onSleepCrewMember ? () => onSleepCrewMember(crewRow.member) : undefined}
              onOpenActions={
                crewRow.row && onOpenActions
                  ? (event) => onOpenActions(crewRow.row!.session, event)
                  : undefined
              }
            />
          ))}
          {bands.pinned.map((row) => (
            <QueueRowView
              key={row.session.id}
              row={row}
              selected={selectedId === row.session.id}
              onSelect={() => onSelectSession(row.session.id)}
              onUnpin={onPinSession && (() => onPinSession(row.session.id, false))}
              onOpenActions={onOpenActions && ((event) => onOpenActions(row.session, event))}
              testIdPrefix="queue-pinned"
            />
          ))}
        </>
      )}
    </div>
  );
}

// The roster is the authority on who exists, but a bound session whose member left the
// roster still gets a row: dropping it would hide a running agent.
function buildCrewRows(
  crew: CrewMemberView[] | undefined,
  awake: QueueRow<QueueBandSessionView>[],
  membersInOtherBands: ReadonlySet<string>,
): { member: string; row?: QueueRow<QueueBandSessionView> }[] {
  const byMember = new Map<string, QueueRow<QueueBandSessionView>>();
  for (const row of awake) {
    const member = row.session.crewMember;
    if (member && !byMember.has(member)) byMember.set(member, row);
  }
  const members = new Set<string>([...(crew ?? []).map((entry) => entry.id), ...byMember.keys()]);
  return [...members]
    .filter((member) => !membersInOtherBands.has(member))
    .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
    .map((member) => ({ member, row: byMember.get(member) }));
}

// Waking takes two clicks. Both asleep targets — the fill button and the sun — arm on the
// first and wake on the second, sharing one armed state; the daemon hears one `crew_wake`.
function CrewRowView({
  member,
  row,
  selected,
  onSelect,
  onWake,
  onSleep,
  onOpenActions,
}: {
  member: string;
  row?: QueueRow<QueueBandSessionView>;
  selected: boolean;
  onSelect?: () => void;
  onWake?: () => void;
  onSleep?: () => void;
  onOpenActions?: (event: ReactMouseEvent) => void;
}) {
  const awake = Boolean(row);
  const { phase, trigger, rowRef } = useWakeConfirm(onWake);
  const armed = phase === 'armed';
  // An awake member arrives with the daemon's label; a sleeping one has none, so the display rule names it here.
  const name = crewDisplayName(member);
  const label = row?.session.label || name;
  const wakeLabel = armed ? `Wake ${name} — click again to confirm` : `Wake ${name}`;
  return (
    <div
      ref={rowRef}
      className={`session-item queue-row queue-row--crew ${selected ? 'selected' : ''}`.trim()}
      data-testid={`queue-crew-${member}`}
      data-crew-member={member}
      data-crew-state={awake ? 'awake' : 'asleep'}
      data-crew-wake={awake || phase === 'rest' ? undefined : phase}
      data-state={row?.session.state}
      data-workspace-id={row?.workspaceId}
    >
      <button
        type="button"
        className="queue-row-select"
        data-testid={`queue-crew-select-${member}`}
        aria-label={awake ? `Open ${label}` : wakeLabel}
        title={awake ? harnessLabel(row!.session.agent) : undefined}
        onClick={awake ? onSelect : trigger}
      />
      {awake ? (
        <StateIndicator state={row!.session.state} size="md" seed={row!.session.id} reason={row!.session.state_reason} />
      ) : (
        // The hollow ring is the same size as an indicator, so every crew row's label starts on the same column.
        <span className="crew-asleep-dot" aria-hidden="true" />
      )}
      {awake && <HarnessIcon agent={row!.session.agent} />}
      <SessionLabel label={label} />
      <span className="crew-row-mark" title={awake ? `${name} is awake` : `${name} is asleep`}>
        {awake ? 'crew' : 'asleep'}
      </span>
      {!awake && onWake && (
        <div className="queue-row-controls">
          {armed && <span className="crew-wake-confirm">confirm</span>}
          <button
            type="button"
            className="queue-row-wake"
            data-testid={`queue-crew-wake-${member}`}
            title={armed ? `Click again to wake ${name}` : `Wake ${name} — start its day`}
            aria-label={wakeLabel}
            onClick={(event) => {
              event.stopPropagation();
              trigger();
            }}
          >
            <CrewWakeSun phase={phase} />
          </button>
        </div>
      )}
      {awake && (onSleep || onOpenActions) && (
        <div className="queue-row-controls">
          {onSleep && (
            <button
              type="button"
              className="queue-row-sleep"
              data-testid={`queue-crew-sleep-${member}`}
              title={`Ask ${name} to close its day and sleep`}
              aria-label={`Ask ${name} to sleep`}
              onClick={(event) => {
                event.stopPropagation();
                onSleep();
              }}
            >
              ☾
            </button>
          )}
          {onOpenActions && (
            <button
              type="button"
              className="session-actions session-more-btn"
              data-testid={`session-actions-${row!.session.id}`}
              title="Session actions"
              aria-label={`Actions for ${label}`}
              onClick={(event) => {
                event.stopPropagation();
                onOpenActions(event);
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

interface QueueSnoozedSectionProps {
  rows: QueueRow<QueueBandSessionView>[];
  selectedId: string | null;
  expanded: boolean;
  onToggleExpanded: () => void;
  onSelectSession: (id: string) => void;
  onWakeTurn: (id: string) => void;
}

// Deferred agents, collapsed at the foot of the sidebar. Not a band: the bands answer
// "whose turn is it", this answers "what did I put off".
export function QueueSnoozedSection({
  rows,
  selectedId,
  expanded,
  onToggleExpanded,
  onSelectSession,
  onWakeTurn,
}: QueueSnoozedSectionProps) {
  const now = useNow(TURN_AGE_TICK_MS);
  if (rows.length === 0) return null;

  return (
    <div className="muted-sessions-section" data-testid="sidebar-snoozed">
      <button
        type="button"
        className="muted-sessions-header"
        onClick={onToggleExpanded}
        aria-expanded={expanded}
        data-testid="snoozed-section-header"
      >
        <span className={`muted-sessions-chevron ${expanded ? 'expanded' : ''}`}>▸</span>
        Snoozed ({rows.length})
      </button>
      {expanded && (
        <div className="muted-sessions-list">
          {rows.map((row) => (
            // A snoozed turn is already closed: waking is the undo, not a second way to dismiss.
            <QueueRowView
              key={row.session.id}
              row={row}
              selected={selectedId === row.session.id}
              wake={formatWakeTime(row.session.turnSnoozedUntil, now)}
              onSelect={() => onSelectSession(row.session.id)}
              onWake={() => onWakeTurn(row.session.id)}
              testIdPrefix="queue-snoozed"
            />
          ))}
        </div>
      )}
    </div>
  );
}
