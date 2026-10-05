import type { MouseEvent as ReactMouseEvent, PointerEvent as ReactPointerEvent } from 'react';
import { formatShortcut } from '../shortcuts/formatShortcut';
import type { SessionPullRequest } from '../types/generated';
import { parseNotebookTileParams, type TileContentState, type TileLeaf } from '../types/desktop';
import { describeSessionPullRequest, pickSessionPullRequest } from '../utils/sessionPullRequest';
import { deriveTileTitle, tileKindLabel } from '../utils/tilePresentation';
import { useDaemonStore } from '../store/daemonSessions';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { useSidebarContext } from './SidebarContext';
import { DelegatedFromChiefBadge } from './DelegatedFromChiefBadge';
import { DelegationChainTrigger } from './DelegationChain';
import { SessionLead } from './SessionLead';
import { SessionLabel } from './SessionLabel';
import { SessionProvenance } from './SessionProvenance';
import { SidebarSettlingBar } from './SettlingIndicator';
import { harnessLabel } from './harnessLabel';
import { describeUnknownReason } from './stateReason';
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
  'aria-current': ariaCurrent,
  'data-select-key': selectKey,
}: {
  desktopId: string;
  tile: TileLeaf;
  content?: TileContentState;
  selected: boolean;
  onSelect: () => void;
  onClose: () => void;
  onReload: () => void;
  'aria-current'?: 'true';
  'data-select-key'?: string;
}) {
  const seeds = useDaemonStore((state) => state.seeds);
  const { desktops } = useSidebarContext();
  const boundSession = tile.tileSessionId
    ? desktops.flatMap((desktop) => desktop.sessions).find((session) => session.id === tile.tileSessionId)
    : undefined;
  const title = deriveTileTitle(tile, content, (id) => seeds.find((seed) => seed.id === id)?.title);
  const kind = tileKindLabel(tile.tileKind);
  const tileIdentifier = tile.tileKind === 'notebook'
    ? parseNotebookTileParams(tile.tileParams).path
    : tile.tileParams;
  return (
    <div
      className={`session-item sidebar-leaf-row desktop-tile-item ${selected ? 'selected' : ''}`.trim()}
      aria-current={ariaCurrent}
      data-testid={`sidebar-tile-${desktopId}-${tile.tileId}`}
      data-tile-kind={tile.tileKind}
      title={tileIdentifier || tile.tileId}
    >
      <button
        type="button"
        className="sidebar-row-select"
        aria-label={`Open ${title}`}
        data-select-key={selectKey}
        onClick={onSelect}
      />
      <span className="desktop-tile-icon" aria-hidden="true">{kind.icon}</span>
      <span className="session-label">{title}</span>
      <span className="session-trailing">
        <span className="desktop-tile-kind">{kind.word}</span>
        {boundSession?.endpointName && (
          <span className={`session-endpoint-badge status-${boundSession.endpointStatus || 'connected'}`}>
            {boundSession.endpointName}
          </span>
        )}
      </span>
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
  showSettling,
}: {
  session: LocalSession;
  selected: boolean;
  showSettling: boolean;
}) {
  return (
    <>
      {showSettling && (session.autoSettleFiresAt || session.autoSettleHeld) ? (
        <SidebarSettlingBar firesAt={session.autoSettleFiresAt} held={session.autoSettleHeld} />
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
  showSettling,
  delegates,
  grouped = false,
  'aria-current': ariaCurrent,
  'data-select-key': selectKey,
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
  showSettling: boolean;
  delegates: readonly LocalSession[];
  grouped?: boolean;
  'aria-current'?: 'true';
  'data-select-key'?: string;
}) {
  const harnessTitle = harnessLabel(session.agent);
  const stateTitle = session.state === 'recoverable'
    ? 'Session will be recovered when opened'
    : session.state === 'unknown' ? describeUnknownReason(session.state_reason) : undefined;
  const hoverTitle = stateTitle ? `${harnessTitle} · ${stateTitle}` : harnessTitle;
  return (
    <div
      className={`session-item sidebar-leaf-row ${grouped ? 'grouped' : ''} ${selected ? 'selected' : ''} ${session.state === 'recoverable' ? 'recoverable' : ''} ${draggable ? 'session-item--draggable' : ''} ${dragging ? 'session-item--dragging' : ''}`
        .trim()
        .replace(/\s+/g, ' ')}
      aria-current={ariaCurrent}
      data-testid={`sidebar-session-${session.id}`}
      data-session-id={session.id}
      data-state={session.state}
      title={session.state === 'recoverable' ? 'Session will be recovered when opened' : undefined}
    >
      <button
        type="button"
        className="sidebar-row-select"
        aria-label={`Open ${session.label}`}
        data-select-key={selectKey}
        title={hoverTitle}
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
      />
    </div>
  );
}
