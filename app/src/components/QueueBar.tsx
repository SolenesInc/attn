import { useMemo, useState } from 'react';
import { useAppViewTitleResolver } from '../hooks/useAppViewTitle';
import { TURN_AGE_TICK_MS, useNow } from '../hooks/useNow';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { tileContentKey, type TileLeaf } from '../types/workspace';
import { nextRunNeedingYou, runCount, runsNeedingYouCount } from '../utils/automationRuns';
import { slotShortcut } from '../utils/desktops';
import { deriveTileTitle } from '../utils/tilePresentation';
import { UNPLACED_GROUP_ID } from '../utils/workspaceViewModels';
import { CriticalNotificationStrip } from './CriticalNotificationStrip';
import { AgentRowView, AgentSessionRow, type SlotOf } from './palette/AgentRows';
import { agentPaletteRows, type AgentPaletteRow } from './palette/agentPaletteRows';
import './QueueBar.css';
import { SidebarPopovers } from './SidebarChrome';
import { useSidebarContext } from './SidebarContext';
import { ExpandIcon } from './SidebarIcons';
import type { LocalSession } from './sidebarTypes';
import { useDesktopChipDrop } from './useDesktopChipDrop';

const LEAD_TURNS = 3;
const PEEK_ROWS = 16;

type Peek = 'waiting' | 'runs';

export function QueueBar() {
  const { instance, criticalNotifications, onOpenNotifications, profileName, onSwitchProfile, onToggleCollapse, peeksSilenced } =
    useSidebarContext();
  const [peek, setPeek] = useState<Peek | null>(null);
  const peekHandlers = (which: Peek) => ({
    onPointerEnter: () => setPeek(which),
    onPointerLeave: () => setPeek((open) => (open === which ? null : open)),
  });
  const shown = peeksSilenced ? null : peek;

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
      <div className="queue-bar-peek-anchor queue-bar-waiting-anchor" data-testid="queue-bar-waiting" {...peekHandlers('waiting')}>
        <WaitingPill />
        {shown === 'waiting' && <WaitingPeek onPicked={() => setPeek(null)} />}
      </div>
      {onOpenNotifications && (
        <CriticalNotificationStrip
          count={criticalNotifications?.count ?? 0}
          title={criticalNotifications?.title ?? ''}
          onOpen={onOpenNotifications}
        />
      )}
      <span className="queue-bar-spacer" />
      <RunsChip peekHandlers={peekHandlers('runs')} peekOpen={shown === 'runs'} onPicked={() => setPeek(null)} />
      <DesktopChips />
      <SidebarPopovers />
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
  const { workspaces, visualIndexOfWorkspace } = useSidebarContext();
  const desktopOfSession = useMemo(() => {
    const byId = new Map<string, string>();
    for (const workspace of workspaces) {
      for (const session of workspace.sessions) byId.set(session.id, workspace.id);
    }
    return byId;
  }, [workspaces]);
  return (desktopId, sessionId) => {
    const id = sessionId ? desktopOfSession.get(sessionId) : desktopId;
    if (!id || id === UNPLACED_GROUP_ID) return '—';
    const index = visualIndexOfWorkspace(id);
    return index >= 0 ? slotShortcut(index + 1) : '·';
  };
}

function WaitingPeek({ onPicked }: { onPicked: () => void }) {
  const { queue, crew, workspaces, tileContents, onSelectSession, onWakeCrewMember, onSelectTile } =
    useSidebarContext();
  const now = useNow(TURN_AGE_TICK_MS);
  const appViewTitle = useAppViewTitleResolver();
  const slotOf = useSlotOf();
  const rows = useMemo(() => {
    if (!queue) return [];
    const tileTitle = (desktopId: string, tile: TileLeaf) =>
      deriveTileTitle(tile, tileContents[tileContentKey(desktopId, tile.tileId)], appViewTitle);
    return agentPaletteRows<LocalSession>(
      { bands: queue, crewRoster: (crew ?? []).map((member) => member.id), workspaces, tileTitle, now },
      '',
    ).filter((row) => row.kind !== 'runs' && !(row.kind === 'agent' && row.session.automation));
  }, [appViewTitle, crew, now, queue, tileContents, workspaces]);

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
    onPicked();
    if (row.kind === 'agent') onSelectSession(row.session.id);
    else if (row.kind === 'member') onWakeCrewMember?.(row.member);
    else if (row.kind === 'tile') onSelectTile?.(row.desktopId, row.tile.tileId);
  };

  return (
    <div className="queue-bar-peek" data-testid="queue-bar-waiting-peek">
      <div className="queue-bar-peek-panel">
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
      </div>
    </div>
  );
}

function RunsChip({
  peekHandlers,
  peekOpen,
  onPicked,
}: {
  peekHandlers: { onPointerEnter: () => void; onPointerLeave: () => void };
  peekOpen: boolean;
  onPicked: () => void;
}) {
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
      <div className="queue-bar-peek-anchor" data-testid="queue-bar-runs-anchor" {...peekHandlers}>
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
        {peekOpen && <RunsPeek onPicked={onPicked} />}
      </div>
      <span className="queue-bar-divider" />
    </>
  );
}

function RunsPeek({ onPicked }: { onPicked: () => void }) {
  const { automationGroups, selectedId, onSelectSession } = useSidebarContext();
  const now = useNow(TURN_AGE_TICK_MS);
  const slotOf = useSlotOf();
  const next = nextRunNeedingYou(automationGroups, selectedId)?.run.id;
  return (
    <div className="queue-bar-peek is-right" data-testid="queue-bar-runs-peek">
      <div className="queue-bar-peek-panel">
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
                onClick={() => {
                  onPicked();
                  onSelectSession(run.id);
                }}
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
      </div>
    </div>
  );
}

function DesktopChips() {
  const { workspaces, queue, selectedWorkspaceId, visualIndexOfWorkspace, onSelectWorkspace, onOpenOverview } =
    useSidebarContext();
  const chipDrop = useDesktopChipDrop();
  const desktops = workspaces.filter((workspace) => workspace.id !== UNPLACED_GROUP_ID);
  const slotted = desktops
    .filter((workspace) => visualIndexOfWorkspace(workspace.id) >= 0)
    .sort((a, b) => visualIndexOfWorkspace(a.id) - visualIndexOfWorkspace(b.id));
  const extras = desktops.filter((workspace) => visualIndexOfWorkspace(workspace.id) < 0);
  const waitingOn = new Map<string, number>();
  for (const row of queue?.turns ?? []) waitingOn.set(row.workspaceId, (waitingOn.get(row.workspaceId) ?? 0) + 1);
  const extrasWaiting = extras.reduce((total, workspace) => total + (waitingOn.get(workspace.id) ?? 0), 0);

  return (
    <div className="queue-bar-desktops" data-testid="queue-bar-desktops">
      {slotted.map((workspace) => {
        const slot = visualIndexOfWorkspace(workspace.id) + 1;
        const waiting = waitingOn.get(workspace.id) ?? 0;
        const { dropClass, dropHandlers } = chipDrop(workspace);
        return (
          <button
            key={workspace.id}
            type="button"
            className={`queue-bar-desktop${workspace.sessions.length || workspace.children.length ? ' has-panes' : ''}${workspace.id === selectedWorkspaceId ? ' is-current' : ''}${dropClass}`}
            data-testid={`queue-bar-desktop-${slot}`}
            data-desktop-id={workspace.id}
            data-waiting={waiting || undefined}
            title={`${workspace.title} (${slotShortcut(slot)})`}
            onClick={() => onSelectWorkspace(workspace.id)}
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
          className={`queue-bar-desktop is-extra${extras.some((workspace) => workspace.id === selectedWorkspaceId) ? ' is-current' : ''}`}
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
