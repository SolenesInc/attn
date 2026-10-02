import { useDaemonStore } from '../../store/daemonSessions';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import type { ShortcutId } from '../../shortcuts/registry';
import { crewDisplayName } from '../../utils/crewName';
import { formatTurnAge } from '../../utils/queueBands';
import { isSnoozed } from '../../utils/snoozeDurations';
import { tileKindLabel } from '../../utils/tilePresentation';
import { agentStatus, type AgentPaletteRow, type PaletteSession } from './agentPaletteRows';
import './AgentRows.css';

export type SlotOf = (desktopId: string | undefined, sessionId?: string) => string;

export function AgentRowView<S extends PaletteSession>({
  row,
  now,
  slotOf,
}: {
  row: AgentPaletteRow<S>;
  now: number;
  slotOf: SlotOf;
}) {
  switch (row.kind) {
    case 'divider':
      return <hr className="unified-palette-divider" />;
    case 'runs':
      return (
        <div className="unified-palette-runs" data-testid={`palette-runs-${row.key}`}>
          <span className="unified-palette-name">{row.name}</span>
          <span className="unified-palette-runs-count">
            {row.needYou > 0 ? `${row.needYou} need you · ` : ''}
            {row.runs} run{row.runs === 1 ? '' : 's'} · never in the queue
          </span>
        </div>
      );
    case 'member':
      return <SleepingMember member={row.member} />;
    case 'tile':
      return <TileRow row={row} slot={slotOf(row.desktopId)} />;
    case 'agent':
      return (
        <AgentSessionRow
          session={row.session}
          tag={row.queueHead ? 'session.jumpToWaiting' : null}
          now={now}
          slot={slotOf(undefined, row.session.id)}
        />
      );
  }
}

function TileRow<S extends PaletteSession>({ row, slot }: { row: Extract<AgentPaletteRow<S>, { kind: 'tile' }>; slot: string }) {
  const kind = tileKindLabel(row.tile.tileKind);
  return (
    <div className="unified-palette-row is-tile" data-testid={`palette-tile-${row.tile.tileId}`}>
      <span className="unified-palette-tile-icon" aria-hidden="true">{kind.icon}</span>
      <span className="unified-palette-tile-kind">{kind.word}</span>
      <span className="unified-palette-name">{row.title}</span>
      <kbd className="unified-palette-slot">{slot}</kbd>
    </div>
  );
}

export function AgentSessionRow({
  session,
  tag,
  now,
  slot,
}: {
  session: PaletteSession;
  tag: ShortcutId | null;
  now: number;
  slot: string;
}) {
  const status = agentStatus(session, now);
  const owedAge = session.turnOwed && !isSnoozed(session.turnSnoozedUntil, now);
  return (
    <div className="unified-palette-row" data-testid={`palette-agent-${session.id}`}>
      <span className={`unified-palette-dot is-${status}`} />
      <span className="unified-palette-name">
        {session.label}
        {session.crewMember && !session.chiefOfStaff && <span className="unified-palette-muted"> · crew</span>}
      </span>
      <kbd className="unified-palette-slot">{slot}</kbd>
      <span className={`unified-palette-pill is-${status}`}>{status}</span>
      <span className="unified-palette-age">{owedAge ? formatTurnAge(session.turnOpenedAt, now) : ''}</span>
      <span className="unified-palette-tag">{tag && <kbd>{formatShortcut(tag)}</kbd>}</span>
    </div>
  );
}

function SleepingMember({ member }: { member: string }) {
  const label = useDaemonStore((state) => state.crew.find((entry) => entry.id === member)?.launch_desktop?.label);
  return <div className="unified-palette-row">
    <span className="unified-palette-dot is-asleep" />
    <span className="unified-palette-name">{crewDisplayName(member)} <span className="unified-palette-muted">· crew{label ? ` · ${label}` : ''}</span></span>
    <span className="unified-palette-pill">asleep</span>
    <span className="unified-palette-age">wake</span>
    <span className="unified-palette-tag" />
  </div>;
}
