import type { MouseEvent as ReactMouseEvent, PointerEvent as ReactPointerEvent } from 'react';
import { useAppViewTitleResolver } from '../hooks/useAppViewTitle';
import { formatShortcut } from '../shortcuts/formatShortcut';
import type { SessionPullRequest } from '../types/generated';
import { type TileContentState, type TileLeaf } from '../types/workspace';
import { describeSessionPullRequest, pickSessionPullRequest } from '../utils/sessionPullRequest';
import { deriveTileTitle } from '../utils/tilePresentation';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { DelegatedFromChiefBadge } from './DelegatedFromChiefBadge';
import { DelegationChainTrigger } from './DelegationChain';
import { HarnessIcon } from './HarnessIcon';
import { SidebarNudgeBar, deriveNudgeMode } from './NudgeIndicator';
import { SessionLabel } from './SessionLabel';
import { SessionProvenance } from './SessionProvenance';
import { SidebarSettlingBar } from './SettlingIndicator';
import './Sidebar.css';
import type { LocalSession } from './sidebarTypes';
import { StateIndicator } from './StateIndicator';

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
        <HarnessIcon agent={session.agent} />
        <SessionLabel label={session.label} session={session} hasDelegates={hasDelegates} />
        <SidebarSessionPullRequest pullRequests={session.pullRequests} />
      </span>
      <SessionProvenance automation={session.automation} density="compact" />
    </span>
  );
}

export function TileSidebarRow({
  workspaceId,
  tile,
  content,
  selected,
  muted = false,
  onSelect,
  onClose,
  onReload,
}: {
  workspaceId: string;
  tile: TileLeaf;
  content?: TileContentState;
  selected: boolean;
  muted?: boolean;
  onSelect: () => void;
  onClose: () => void;
  onReload: () => void;
}) {
  const appViewTitle = useAppViewTitleResolver();
  const title = deriveTileTitle(tile, content, appViewTitle);
  return (
    <div
      className={`session-item workspace-tile-item grouped ${selected ? 'selected' : ''} ${muted ? 'muted-session' : ''}`.trim()}
      data-testid={`sidebar-tile-${workspaceId}-${tile.tileId}`}
      data-tile-kind={tile.tileKind}
    >
      <button
        type="button"
        className="sidebar-row-select"
        aria-label={`Open ${title}`}
        onClick={onSelect}
      />
      <span
        className={`workspace-tile-indicator workspace-tile-indicator--${tile.tileKind}`}
        aria-hidden="true"
      />
      <span className="session-label">{title}</span>
      {!muted && (
        <div className="session-actions">
          {tile.tileKind === 'browser' && (
            <button
              className="session-action-btn reload-session-btn"
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
            className="session-action-btn close-session-btn"
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
      )}
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
      {session.chiefOfStaff && <ChiefOfStaffBadge />}
      {session.delegatedFromChief && <DelegatedFromChiefBadge />}
      {session.isWorktree && <span className="worktree-indicator">⎇</span>}
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
  onTriggerNudge?: () => void;
  showSettling: boolean;
  delegates: readonly LocalSession[];
}) {
  return (
    <div
      className={`session-item grouped ${selected ? 'selected' : ''} ${session.state === 'recoverable' ? 'recoverable' : ''} ${draggable ? 'session-item--draggable' : ''} ${dragging ? 'session-item--dragging' : ''}`
        .trim()
        .replace(/\s+/g, ' ')}
      data-testid={`sidebar-session-${session.id}`}
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
      <StateIndicator
        state={session.state}
        size="md"
        seed={session.id}
        reason={session.state_reason}
      />
      <SidebarSessionIdentity session={session} hasDelegates={delegates.length > 0} />
      <DelegationChainTrigger session={session} hasDelegates={delegates.length > 0} />
      <SidebarSessionBadges session={session} />
      <div className="session-actions">
        <button
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
