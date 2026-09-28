import type { MouseEvent as ReactMouseEvent, PointerEvent as ReactPointerEvent } from 'react';
import { formatShortcut } from '../shortcuts/formatShortcut';
import type { SessionPullRequest } from '../types/generated';
import { type TileContentState, type TileLeaf } from '../types/desktop';
import { describeSessionPullRequest, pickSessionPullRequest } from '../utils/sessionPullRequest';
import { deriveTileTitle } from '../utils/tilePresentation';
import { useDaemonStore } from '../store/daemonSessions';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { DelegatedFromChiefBadge } from './DelegatedFromChiefBadge';
import { DelegationChainTrigger } from './DelegationChain';
import { SidebarNudgeBar, deriveNudgeMode } from './NudgeIndicator';
import { SessionLead } from './SessionLead';
import { SessionLabel } from './SessionLabel';
import { SessionProvenance } from './SessionProvenance';
import { SidebarSettlingBar } from './SettlingIndicator';
import './Sidebar.css';
import './SidebarRow.css';
import type { LocalSession } from './sidebarTypes';

export function SidebarSessionPullRequest({
  pullRequests,
}: {
  pullRequests?: SessionPullRequest[];
}) {
  const pr = pickSessionPullRequest(pullRequests);
  if (!pr) return null;
  const { label, tone } = describeSessionPullRequest(pr);
  const description = [`${pr.repository}#${pr.number}`, label, pr.title]
    .filter(Boolean)
    .join(' · ');
  return (
    <span
      className="sidebar-session-pr"
      data-tone={tone}
      title={description}
      aria-label={description}
    >
      <span className="sidebar-session-pr__dot" aria-hidden="true" />
      <span className="sidebar-session-pr__number">#{pr.number}</span>
    </span>
  );
}

export function SidebarSessionIdentity({
  session,
  hasDelegates,
}: {
  session: LocalSession;
  hasDelegates: boolean;
}) {
  return (
    <span className="sidebar-session-identity">
      <span className="sidebar-session-headline">
        <SessionLabel label={session.label} session={session} hasDelegates={hasDelegates} />
        <SidebarSessionPullRequest pullRequests={session.pullRequests} />
      </span>
      <SessionProvenance automation={session.automation} density="compact" />
    </span>
  );
}

export function TileSidebarRow({
  desktopId,
  tile,
  content,
  selected,
  onSelect,
  onClose,
  onReload,
}: {
  desktopId: string;
  tile: TileLeaf;
  content?: TileContentState;
  selected: boolean;
  onSelect: () => void;
  onClose: () => void;
  onReload: () => void;
}) {
  const seeds = useDaemonStore((state) => state.seeds);
  const title = deriveTileTitle(tile, content, (id) => seeds.find((seed) => seed.id === id)?.title);
  return (
    <div
      className={`session-item sidebar-leaf-row desktop-tile-item grouped ${selected ? 'selected' : ''}`.trim()}
      data-testid={`sidebar-tile-${desktopId}-${tile.tileId}`}
      data-tile-kind={tile.tileKind}
    >
      <button
        type="button"
        className="sidebar-row-select"
        aria-label={`Open ${title}`}
        onClick={onSelect}
      />
      <span
        className={`desktop-tile-indicator desktop-tile-indicator--${tile.tileKind}`}
        aria-hidden="true"
      />
      <span className="session-label">{title}</span>
      <span className="session-trailing" />
      <div className="session-actions">
        {tile.tileKind === 'browser' && (
          <button
            type="button"
            className="session-action-btn reload-session-btn"
            data-testid={`reload-tile-${desktopId}-${tile.tileId}`}
            onClick={(event) => {
              event.stopPropagation();
              onReload();
            }}
            title="Reload browser"
            aria-label={`Reload ${title}`}
          >
            ↻
          </button>
        )}
        <button
          type="button"
          className="session-action-btn close-session-btn"
          data-testid={`close-tile-${desktopId}-${tile.tileId}`}
          onClick={(event) => {
            event.stopPropagation();
            onClose();
          }}
          title="Close tile"
          aria-label={`Close ${title}`}
        >
          ×
        </button>
      </div>
    </div>
  );
}

export function SidebarSessionBadges({ session }: { session: LocalSession }) {
  return (
    <>
      {session.endpointName && (
        <span className={`session-endpoint-badge status-${session.endpointStatus || 'connected'}`}>
          {session.endpointName}
        </span>
      )}
      {session.state === 'recoverable' && <span className="session-recoverable">recoverable</span>}
      {session.chiefOfStaff && <ChiefOfStaffBadge compact />}
      {session.delegatedFromChief && <DelegatedFromChiefBadge />}
    </>
  );
}

export function SidebarSessionCountdowns({
  session,
  selected,
  showSettling,
  onTriggerNudge,
}: {
  session: LocalSession;
  selected: boolean;
  showSettling: boolean;
  onTriggerNudge?: () => void;
}) {
  const nudgeMode = deriveNudgeMode({
    ticketUnread: session.ticketUnread,
    nudgeFiresAt: session.nudgeFiresAt,
    state: session.state,
    isActive: selected,
  });
  return (
    <>
      {showSettling && (session.autoSettleFiresAt || session.autoSettleHeld) ? (
        <SidebarSettlingBar firesAt={session.autoSettleFiresAt} held={session.autoSettleHeld} />
      ) : null}
      {nudgeMode ? (
        <SidebarNudgeBar
          mode={nudgeMode}
          firesAt={session.nudgeFiresAt}
          onTrigger={onTriggerNudge ?? (() => {})}
        />
      ) : null}
    </>
  );
}

export function SidebarSessionRow({
  session,
  selected,
  draggable = false,
  dragging = false,
  onSelect,
  onClickCapture,
  onPointerDown,
  onOpenActions,
  onSettle,
  onTriggerNudge,
  showSettling,
  delegates,
}: {
  session: LocalSession;
  selected: boolean;
  draggable?: boolean;
  dragging?: boolean;
  onSelect: () => void;
  onClickCapture?: (event: ReactMouseEvent) => void;
  onPointerDown?: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onOpenActions: (event: ReactMouseEvent) => void;
  onSettle?: () => void;
  onTriggerNudge?: () => void;
  showSettling: boolean;
  delegates: readonly LocalSession[];
}) {
  return (
    <div
      className={`session-item sidebar-leaf-row grouped ${selected ? 'selected' : ''} ${session.state === 'recoverable' ? 'recoverable' : ''} ${draggable ? 'session-item--draggable' : ''} ${dragging ? 'session-item--dragging' : ''}`
        .trim()
        .replace(/\s+/g, ' ')}
      data-testid={`sidebar-session-${session.id}`}
      data-session-id={session.id}
      data-state={session.state}
      title={session.state === 'recoverable' ? 'Session will be recovered when opened' : undefined}
    >
      <button
        type="button"
        className="sidebar-row-select"
        aria-label={`Open ${session.label}`}
        onClick={onSelect}
        onClickCapture={onClickCapture}
        onPointerDown={onPointerDown}
      />
      <SessionLead
        agent={session.agent}
        state={session.state}
        seed={session.id}
        reason={session.state_reason}
      />
      <SidebarSessionIdentity session={session} hasDelegates={delegates.length > 0} />
      <span className="session-trailing">
        <DelegationChainTrigger session={session} hasDelegates={delegates.length > 0} />
        <SidebarSessionBadges session={session} />
      </span>
      <div className="session-actions">
        {onSettle && (
          <button
            type="button"
            className="session-action-btn session-settle-btn"
            data-testid={`session-settle-${session.id}`}
            onClick={(event) => {
              event.stopPropagation();
              onSettle();
            }}
            title={`Settle this run (${formatShortcut('session.settle')})`}
            aria-label={`Settle ${session.label}`}
          >
            ✓
          </button>
        )}
        <button
          type="button"
          className="session-action-btn session-more-btn"
          data-testid={`session-actions-${session.id}`}
          onClick={onOpenActions}
          title={`Session actions (${formatShortcut('session.close')} closes)`}
          aria-label={`Actions for ${session.label}`}
        >
          •••
        </button>
      </div>
      <SidebarSessionCountdowns
        session={session}
        selected={selected}
        showSettling={showSettling}
        onTriggerNudge={onTriggerNudge}
      />
    </div>
  );
}
