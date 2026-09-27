import { useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useAgentOnScreen } from '../hooks/useDesktopSelectionBridge';
import { TURN_AGE_TICK_MS, useNow } from '../hooks/useNow';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { tileContentKey, type TileLeaf } from '../types/desktop';
import { nextRunNeedingYou, runCount, runsNeedingYouCount } from '../utils/automationRuns';
import { slotShortcut } from '../utils/desktops';
import { deriveTileTitle } from '../utils/tilePresentation';
import { clampIntoViewport } from '../utils/viewportClamp';
import { UNPLACED_GROUP_ID } from '../utils/desktopViewModels';
import { CriticalNotificationStrip } from './CriticalNotificationStrip';
import { AgentRowView, AgentSessionRow, type SlotOf } from './palette/AgentRows';
import { agentPaletteRows, type AgentPaletteRow } from './palette/agentPaletteRows';
import './QueueBar.css';
import { useSidebarContext } from './SidebarContext';
import { ExpandIcon } from './SidebarIcons';
import type { LocalSession } from './sidebarTypes';
import { useDesktopChipDrop } from './useDesktopChipDrop';

const LEAD_TURNS = 3;
const PEEK_ROWS = 16;

export function QueueBar() {
  const { instance, criticalNotifications, onOpenNotifications, profileName, onSwitchProfile, onToggleCollapse } =
    useSidebarContext();

  return (
    <div className="queue-bar" data-testid="queue-bar">
      <button
        type="button"
        className="queue-bar-tool"
        data-testid="queue-bar-show-sidebar"
        title={`Show sidebar (${formatShortcut('session.toggleSidebar')})`}
        aria-label="Show sidebar"
        onClick={onToggleCollapse}
      >
        <ExpandIcon />
      </button>
      {instance && (
        <span className="queue-bar-instance" data-testid="sidebar-instance-marker">
          {instance}
        </span>
      )}
      <button
        type="button"
        className="queue-bar-profile"
        data-testid="queue-profile-pill"
        title={`Switch profile (${formatShortcut('profile.switch')})`}
        onClick={onSwitchProfile}
        disabled={!onSwitchProfile}
      >
        <strong>{profileName ?? 'Profile'}</strong>
        <span className="queue-bar-chevron" aria-hidden="true">▾</span>
      </button>
      <PeekAnchor className="queue-bar-waiting-anchor" testId="queue-bar-waiting" peek={<WaitingPeek />}>
        <WaitingPill />
      </PeekAnchor>
      {onOpenNotifications && (
        <CriticalNotificationStrip
          count={criticalNotifications?.count ?? 0}
          title={criticalNotifications?.title ?? ''}
          onOpen={onOpenNotifications}
        />
      )}
      <span className="queue-bar-spacer" />
      <RunsChip />
      <DesktopChips />
    </div>
  );
}

function WaitingPill() {
  const { queue, onOpenAgents } = useSidebarContext();
  const turns = queue?.turns ?? [];
  const lead = turns.slice(0, LEAD_TURNS);
  const hidden = turns.length - lead.length;
  return (
    <button
      type="button"
      className="queue-bar-pill"
      data-testid="queue-bar-pill"
      data-waiting={turns.length}
      title={`Hover to peek · click or ${formatShortcut('ui.actionMenu')} to open the palette`}
      onClick={onOpenAgents}
    >
      <span className="queue-bar-pill-count">
        <span className={`queue-bar-dot ${turns.length ? 'is-waiting' : ''}`} />
        {turns.length ? `${turns.length} waiting` : 'nothing owed'}
      </span>
      {lead.length > 0 && <span className="queue-bar-sep">·</span>}
      {lead.map((row, index) => (
        <span key={row.session.id} className="queue-bar-crumb-wrap">
          {index > 0 && <span className="queue-bar-sep">›</span>}
          <span className="queue-bar-crumb">{row.session.label}</span>
        </span>
      ))}
      {hidden > 0 && <span className="queue-bar-crumb is-more">+{hidden}</span>}
      <span className="queue-bar-chevron" aria-hidden="true">▾</span>
    </button>
  );
}

function useSlotOf(): SlotOf {
  const { desktops, visualIndexOfDesktop } = useSidebarContext();
  const desktopOfSession = useMemo(() => {
    const byId = new Map<string, string>();
    for (const desktop of desktops) {
      for (const session of desktop.sessions) byId.set(session.id, desktop.id);
    }
    return byId;
  }, [desktops]);
  return (desktopId, sessionId) => {
    const id = sessionId ? desktopOfSession.get(sessionId) : desktopId;
    if (!id || id === UNPLACED_GROUP_ID) return '—';
    const index = visualIndexOfDesktop(id);
    return index >= 0 ? slotShortcut(index + 1) : '·';
  };
}

function WaitingPeek() {
  const { queue, crew, desktops, tileContents, onSelectSession, onWakeCrewMember, onSelectTile } =
    useSidebarContext();
  const now = useNow(TURN_AGE_TICK_MS);
  const slotOf = useSlotOf();
  const rows = useMemo(() => {
    if (!queue) return [];
    const tileTitle = (desktopId: string, tile: TileLeaf) =>
      deriveTileTitle(tile, tileContents[tileContentKey(desktopId, tile.tileId)]);
    return agentPaletteRows<LocalSession>(
      { bands: queue, crewRoster: (crew ?? []).map((member) => member.id), desktops, tileTitle, now },
      '',
    ).filter((row) => row.kind !== 'runs' && !(row.kind === 'agent' && row.session.automation));
  }, [crew, now, queue, tileContents, desktops]);

  const shown: AgentPaletteRow<LocalSession>[] = [];
  let entries = 0;
  for (const row of rows) {
    if (row.kind === 'divider') {
      shown.push(row);
      continue;
    }
    if (entries === PEEK_ROWS) break;
    shown.push(row);
    entries += 1;
  }
  const more = rows.filter((row) => row.kind !== 'divider').length - entries;

  const pick = (row: AgentPaletteRow<LocalSession>) => {
    if (row.kind === 'agent') onSelectSession(row.session.id);
    else if (row.kind === 'member') onWakeCrewMember?.(row.member);
    else if (row.kind === 'tile') onSelectTile?.(row.desktopId, row.tile.tileId);
  };

  return (
    <QueueBarPeek testId="queue-bar-waiting-peek">
      {shown.map((row) =>
        row.kind === 'divider' ? (
          <AgentRowView key={row.key} row={row} now={now} slotOf={slotOf} />
        ) : (
          <button
            key={row.key}
            type="button"
            tabIndex={-1}
            className="queue-bar-peek-row"
            data-testid={`queue-bar-peek-${row.key}`}
            onClick={() => pick(row)}
          >
            <AgentRowView row={row} now={now} slotOf={slotOf} />
          </button>
        ),
      )}
      {more > 0 && (
        <div className="queue-bar-peek-more" data-testid="queue-bar-peek-more">
          {more} more · {formatShortcut('ui.actionMenu')} to filter · {formatShortcut('ui.commandPalette')} commands
        </div>
      )}
      <div className="queue-bar-peek-foot">
        Automation runs are not in the queue · ⚙ chip on the right, or {formatShortcut('session.nextRun')}
      </div>
    </QueueBarPeek>
  );
}

function QueueBarPeek({ testId, alignRight = false, children }: { testId: string; alignRight?: boolean; children: ReactNode }) {
  const panelRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const panel = panelRef.current!;
    const bar = panel.closest<HTMLElement>('.queue-bar')!;
    const shiftIntoViewport = () => {
      panel.style.transform = '';
      const rect = panel.getBoundingClientRect();
      const clamped = clampIntoViewport(rect, rect);
      const x = clamped.left - rect.left;
      const y = clamped.top - rect.top;
      panel.style.transform = x || y ? `translate(${x}px, ${y}px)` : '';
    };
    shiftIntoViewport();
    const barLayout = new ResizeObserver(shiftIntoViewport);
    for (const item of [bar, ...bar.children, panel]) barLayout.observe(item);
    return () => barLayout.disconnect();
  }, []);
  return (
    <div className={`queue-bar-peek${alignRight ? ' is-right' : ''}`} data-testid={testId}>
      <div ref={panelRef} className="queue-bar-peek-panel">
        {children}
      </div>
    </div>
  );
}

function PeekAnchor({
  className,
  testId,
  peek,
  children,
}: {
  className?: string;
  testId: string;
  peek: ReactNode;
  children: ReactNode;
}) {
  const { peeksSilenced } = useSidebarContext();
  const [hovered, setHovered] = useState(false);
  if (peeksSilenced && hovered) setHovered(false);
  return (
    <div
      className={`queue-bar-peek-anchor${className ? ` ${className}` : ''}`}
      data-testid={testId}
      onPointerEnter={() => setHovered(true)}
      onPointerLeave={() => setHovered(false)}
      onClickCapture={() => setHovered(false)}
      onKeyDownCapture={(event) => {
        if (event.key === 'Enter' || event.key === ' ') setHovered(false);
      }}
    >
      {children}
      {hovered && !peeksSilenced && peek}
    </div>
  );
}

function RunsChip() {
  const { automationGroups, onWalkRuns } = useSidebarContext();
  const runs = runCount(automationGroups);
  if (runs === 0) return null;
  const needingYou = runsNeedingYouCount(automationGroups);
  const title = [
    `${automationGroups.length} automation${automationGroups.length === 1 ? '' : 's'}`,
    `${runs} run${runs === 1 ? '' : 's'}`,
    ...(needingYou ? [`${needingYou} still need you`] : []),
    'runs stay out of the queue',
    `hover to look, click or ${formatShortcut('session.nextRun')} to walk them`,
  ].join(' · ');
  return (
    <>
      <PeekAnchor testId="queue-bar-runs-anchor" peek={<RunsPeek />}>
        <button
          type="button"
          className="queue-bar-runs"
          data-testid="queue-bar-runs"
          data-runs={runs}
          data-needing={needingYou}
          title={title}
          onClick={onWalkRuns}
        >
          ⚙ {runs}
          {needingYou > 0 && <span className="queue-bar-runs-needing">{needingYou}</span>}
        </button>
      </PeekAnchor>
      <span className="queue-bar-divider" />
    </>
  );
}

function RunsPeek() {
  const { automationGroups, onSelectSession } = useSidebarContext();
  const agentOnScreenId = useAgentOnScreen();
  const now = useNow(TURN_AGE_TICK_MS);
  const slotOf = useSlotOf();
  const next = nextRunNeedingYou(automationGroups, agentOnScreenId)?.run.id;
  return (
    <QueueBarPeek testId="queue-bar-runs-peek" alignRight>
      {automationGroups.map((group) => (
        <div key={group.id} data-testid={`queue-bar-runs-group-${group.id}`}>
          <div className="queue-bar-peek-group">
            <span className="queue-bar-peek-group-name" title={group.name}>{group.name}</span>
            <span className="queue-bar-peek-group-count">
              {group.needingYou.length > 0 && `${group.needingYou.length} need you · `}
              {group.runs.length} run{group.runs.length === 1 ? '' : 's'}
            </span>
          </div>
          {group.runs.map((run) => (
            <button
              key={run.id}
              type="button"
              tabIndex={-1}
              className="queue-bar-peek-row"
              data-testid={`queue-bar-peek-run-${run.id}`}
              onClick={() => onSelectSession(run.id)}
            >
              <AgentSessionRow
                session={run}
                tag={run.id === next ? 'session.nextRun' : null}
                now={now}
                slot={slotOf(undefined, run.id)}
              />
            </button>
          ))}
        </div>
      ))}
      <div className="queue-bar-peek-foot">
        Automation runs never join the queue ·{' '}
        {next ? `${formatShortcut('session.nextRun')} opens the next one needing you` : 'nothing here needs you'}
      </div>
    </QueueBarPeek>
  );
}

function DesktopChips() {
  const { desktops, queue, selectedDesktopId, visualIndexOfDesktop, onSelectDesktop, onOpenOverview } =
    useSidebarContext();
  const chipDrop = useDesktopChipDrop();
  const placed = desktops.filter((desktop) => desktop.id !== UNPLACED_GROUP_ID);
  const slotted = placed
    .filter((desktop) => visualIndexOfDesktop(desktop.id) >= 0)
    .sort((a, b) => visualIndexOfDesktop(a.id) - visualIndexOfDesktop(b.id));
  const extras = placed.filter((desktop) => visualIndexOfDesktop(desktop.id) < 0);
  const waitingOn = new Map<string, number>();
  for (const row of queue?.turns ?? []) waitingOn.set(row.desktopId, (waitingOn.get(row.desktopId) ?? 0) + 1);
  const extrasWaiting = extras.reduce((total, desktop) => total + (waitingOn.get(desktop.id) ?? 0), 0);

  return (
    <div className="queue-bar-desktops" data-testid="queue-bar-desktops">
      {slotted.map((desktop) => {
        const slot = visualIndexOfDesktop(desktop.id) + 1;
        const waiting = waitingOn.get(desktop.id) ?? 0;
        const { dropClass, dropHandlers } = chipDrop(desktop);
        return (
          <button
            key={desktop.id}
            type="button"
            className={`queue-bar-desktop${desktop.sessions.length || desktop.children.length ? ' has-panes' : ''}${desktop.id === selectedDesktopId ? ' is-current' : ''}${dropClass}`}
            data-testid={`queue-bar-desktop-${slot}`}
            data-desktop-id={desktop.id}
            data-waiting={waiting || undefined}
            title={`${desktop.title} (${slotShortcut(slot)})`}
            onClick={() => onSelectDesktop(desktop.id)}
            {...dropHandlers}
          >
            {slot}
            {waiting > 0 && <span className="queue-bar-desktop-waiting">{waiting}</span>}
          </button>
        );
      })}
      {extras.length > 0 && (
        <button
          type="button"
          className={`queue-bar-desktop is-extra${extras.some((desktop) => desktop.id === selectedDesktopId) ? ' is-current' : ''}`}
          data-testid="queue-bar-desktop-extras"
          title={`${extras.length} more desktop${extras.length === 1 ? '' : 's'} without a shortcut (${formatShortcut('desktop.overview')})`}
          onClick={onOpenOverview}
        >
          +{extras.length}
          {extrasWaiting > 0 && <span className="queue-bar-desktop-waiting">{extrasWaiting}</span>}
        </button>
      )}
    </div>
  );
}
