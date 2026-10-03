import { useRef, type KeyboardEvent as ReactKeyboardEvent, type RefObject } from 'react';
import { TURN_AGE_TICK_MS, useNow } from '../hooks/useNow';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { crewRows, formatTurnAge, type QueueRow } from '../utils/queueBands';
import { slotShortcut } from '../utils/desktops';
import { formatWakeTimeShort } from '../utils/snoozeDurations';
import { CriticalNotificationStrip } from './CriticalNotificationStrip';
import { CrewRowView, QueueRowView, type QueueBandSessionView, type RowWhere } from './QueueRows';
import './QueueSidebar.css';
import { SidebarPopovers } from './SidebarChrome';
import { useSidebarContext } from './SidebarContext';
import { useDesktopChipDrop } from './useDesktopChipDrop';
import { CollapseIcon, HomeIcon, PlusIcon } from './SidebarIcons';
import { SidebarDesktopOverview } from './SidebarDesktops';
import { useWaitingFit } from './useWaitingFit';

const WALK_ROW_SELECTOR = '.queue-row-select, .sidebar-row-select';

export function QueueSidebar() {
  const {
    harnessLogosEnabled,
    criticalNotifications,
    onOpenNotifications,
    agentListOpen,
    onToggleAgentList,
    agentFilter,
    setAgentFilter,
    selectedId,
    onSelectSession,
    queue,
  } = useSidebarContext();
  const rootRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const leadRef = useRef<HTMLDivElement>(null);
  const leadCount = useWaitingFit(bodyRef, leadRef, queue?.turns.length ?? 0, Boolean(agentListOpen));

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    const root = rootRef.current;
    if (!root) return;
    const target = event.target as HTMLElement;
    if (!target.closest('.queue-sidebar-body')) return;
    const inFilter = target.matches('[data-testid="queue-agent-filter"]');
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      const rows = Array.from(root.querySelectorAll<HTMLElement>(WALK_ROW_SELECTOR));
      if (rows.length === 0) return;
      event.preventDefault();
      const at = rows.indexOf(target);
      const step = event.key === 'ArrowDown' ? 1 : -1;
      const next = at === -1 ? (step === 1 ? 0 : rows.length - 1) : (at + step + rows.length) % rows.length;
      rows[next].focus();
      return;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      if (agentFilter) {
        setAgentFilter('');
      } else if (agentListOpen) {
        onToggleAgentList?.();
      } else if (selectedId) {
        onSelectSession(selectedId);
      } else {
        target.blur();
      }
      return;
    }
    if (agentListOpen && !inFilter && /^[\p{L}\p{N}]$/u.test(event.key)) {
      event.preventDefault();
      setAgentFilter(agentFilter + event.key);
      root.querySelector<HTMLInputElement>('[data-testid="queue-agent-filter"]')?.focus();
    }
  };

  return (
    <div
      ref={rootRef}
      className={`sidebar queue-sidebar ${harnessLogosEnabled ? '' : 'sidebar--hide-harness-logos'}`.trim()}
      data-testid="queue-sidebar"
      onKeyDown={onKeyDown}
    >
      <QueueSidebarHeader />
      {onOpenNotifications && (
        <CriticalNotificationStrip
          count={criticalNotifications?.count ?? 0}
          title={criticalNotifications?.title ?? ''}
          onOpen={onOpenNotifications}
        />
      )}
      <div ref={bodyRef} className="queue-sidebar-body" data-testid="sidebar-queue">
        <HomeRow />
        <CrewBlock />
        <WaitingCard leadRef={leadRef} leadCount={leadCount} />
      </div>
      <DesktopStrip />
      <SidebarPopovers />
    </div>
  );
}

function QueueSidebarHeader() {
  const { instance, queue, profileName, onSwitchProfile, onNewSession, onOpenCommands, commandsBadge, onToggleCollapse } =
    useSidebarContext();
  const waiting = queue?.turns.length ?? 0;
  return (
    <div className="queue-sidebar-header">
      {instance && (
        <div className="sidebar-instance-marker" data-testid="sidebar-instance-marker">
          instance <strong>{instance}</strong>
        </div>
      )}
      <button
        type="button"
        className="queue-profile-pill"
        data-testid="queue-profile-pill"
        title={`Switch profile (${formatShortcut('profile.switch')})`}
        onClick={onSwitchProfile}
        disabled={!onSwitchProfile}
      >
        <strong>{profileName ?? 'Profile'}</strong>
        {waiting > 0 && <span className="queue-profile-pill-waiting">{waiting}</span>}
        <span className="queue-profile-pill-chevron" aria-hidden="true">▾</span>
      </button>
      <div className="queue-sidebar-tools">
        <button
          type="button"
          className="queue-sidebar-tool queue-sidebar-tool--primary"
          data-testid="queue-new-agent"
          title={`New agent (${formatShortcut('session.new')})`}
          aria-label="New agent"
          onClick={onNewSession}
        >
          <PlusIcon />
        </button>
        <button
          type="button"
          className="queue-sidebar-tool"
          data-testid="queue-commands"
          title={`Commands (${formatShortcut('ui.commandPalette')})`}
          aria-label="Commands"
          onClick={onOpenCommands}
          disabled={!onOpenCommands}
        >
          ⋯
          {commandsBadge ? <span className="queue-sidebar-tool-badge">{commandsBadge > 9 ? '9+' : commandsBadge}</span> : null}
        </button>
        <button
          type="button"
          className="queue-sidebar-tool"
          title={`Collapse sidebar (${formatShortcut('session.toggleSidebar')})`}
          aria-label="Collapse sidebar"
          onClick={onToggleCollapse}
        >
          <CollapseIcon />
        </button>
      </div>
    </div>
  );
}

function HomeRow() {
  const { onGoToDashboard, homeActive } = useSidebarContext();
  return (
    <button
      type="button"
      className={`sidebar-home-row ${homeActive ? 'selected' : ''}`}
      data-testid="sidebar-home"
      onClick={onGoToDashboard}
      aria-current={homeActive ? 'page' : undefined}
    >
      <HomeIcon />
      <span className="sidebar-home-label">Home</span>
      <span className="sidebar-home-shortcut">{formatShortcut('session.goToDashboard')}</span>
    </button>
  );
}

function useRowWhere(): (row: QueueRow<QueueBandSessionView>) => RowWhere {
  const { visualIndexOfDesktop } = useSidebarContext();
  return (row) => {
    const index = visualIndexOfDesktop(row.desktopId);
    return index >= 0
      ? { slot: String(index + 1), title: row.desktopTitle }
      : { slot: '·', title: `${row.desktopTitle} · no shortcut` };
  };
}

function CrewBlock() {
  const {
    queue,
    crew,
    selectedId,
    onSelectSession,
    onWakeCrewMember,
    onSleepCrewMember,
    openCrewMemberActions,
    openSessionActions,
    delegates,
    onManageCrew,
  } = useSidebarContext();
  const where = useRowWhere();
  if (!queue) return null;
  const members = crewRows(crew, queue);
  if (!queue.chief && members.length === 0) return null;
  const chief = queue.chief;
  return (
    <div className="queue-crew-block" data-testid="queue-crew-block">
      <div className="queue-section-rule queue-crew-rule">
        <span>Crew</span>
        <span className="queue-rule-line" aria-hidden="true" />
        {onManageCrew && <button type="button" data-testid="manage-crew" onClick={onManageCrew}>manage</button>}
      </div>
      {chief && (
        <QueueRowView
          row={chief}
          selected={selectedId === chief.session.id}
          where={where(chief)}
          onSelect={() => onSelectSession(chief.session.id)}
          onOpenActions={(event) => openSessionActions(chief.session, event)}
          delegates={delegates.get(chief.session.id) ?? []}
          testIdPrefix="queue-chief"
        />
      )}
      {members.map(({ member, row }) => (
        <CrewRowView
          key={member}
          member={member}
          desktopLabel={crew?.find((candidate) => candidate.id === member)?.launch_desktop?.label}
          row={row}
          where={row ? where(row) : undefined}
          selected={row ? selectedId === row.session.id : false}
          onSelect={row ? () => onSelectSession(row.session.id) : undefined}
          onWake={onWakeCrewMember && (() => onWakeCrewMember(member))}
          onSleep={row && onSleepCrewMember ? () => onSleepCrewMember(member) : undefined}
          onOpenActions={row ? (event) => openSessionActions(row.session, event) : undefined}
          delegates={row ? (delegates.get(row.session.id) ?? []) : []}
          onOpenMemberActions={(event) => openCrewMemberActions(member, event)}
        />
      ))}
    </div>
  );
}

function WaitingCard({ leadRef, leadCount }: { leadRef: RefObject<HTMLDivElement | null>; leadCount: number }) {
  const {
    queue,
    selectedId,
    onSelectSession,
    onSettleTurn,
    onOpenSnooze,
    onWakeTurn,
    onJumpToWaiting,
    openSessionActions,
    onScreenSessionIds,
    delegates,
    agentListOpen,
    onToggleAgentList,
    agentFilter,
    setAgentFilter,
  } = useSidebarContext();
  const now = useNow(TURN_AGE_TICK_MS);
  const where = useRowWhere();
  if (!queue) return null;

  const turns = queue.turns;
  const lead = turns.slice(0, leadCount);
  const hidden = turns.length - lead.length;
  const pinnedCrew = new Set(queue.crew.map((row) => row.session.id));
  const matches = (row: QueueRow<QueueBandSessionView>) =>
    !agentFilter || row.session.label.toLowerCase().includes(agentFilter.toLowerCase());
  const hiddenWaiting = turns.slice(leadCount).filter((row) => !pinnedCrew.has(row.session.id)).length;
  const hiddenWorking = queue.settled.filter((row) => !pinnedCrew.has(row.session.id)).length;
  const hiddenSnoozed = queue.snoozed.filter((row) => !pinnedCrew.has(row.session.id)).length;
  const more = hiddenWaiting + hiddenWorking + hiddenSnoozed;
  const parts = [
    [hiddenWaiting, 'waiting'],
    [hiddenWorking, 'working'],
    [hiddenSnoozed, 'snoozed'],
  ] as const;

  const turnRow = (row: QueueRow<QueueBandSessionView>) => (
    <QueueRowView
      key={row.session.id}
      row={row}
      selected={selectedId === row.session.id}
      where={where(row)}
      age={formatTurnAge(row.session.turnOpenedAt, now)}
      onSelect={() => onSelectSession(row.session.id)}
      onSettle={onSettleTurn && (() => onSettleTurn(row.session.id))}
      onSnooze={onOpenSnooze && ((event) => onOpenSnooze(row.session, event))}
      onOpenActions={(event) => openSessionActions(row.session, event)}
      showSettling={!onScreenSessionIds?.has(row.session.id)}
      delegates={delegates.get(row.session.id) ?? []}
      testIdPrefix="queue-turn"
    />
  );
  const working = queue.settled.filter(matches);
  const snoozed = queue.snoozed.filter(matches);

  return (
    <div className="queue-waiting-card" data-testid="queue-waiting-card" data-waiting={turns.length}>
      <button
        type="button"
        className="queue-section-rule queue-waiting-head"
        data-testid="queue-waiting-head"
        title={`Open the oldest turn (${formatShortcut('session.jumpToWaiting')})`}
        onClick={onJumpToWaiting}
        disabled={turns.length === 0}
      >
        {turns.length > 0 ? (
          <span className="queue-waiting-count">
            <span className="queue-waiting-number">{turns.length}</span> waiting
          </span>
        ) : (
          <span className="queue-waiting-count is-zero" data-testid="queue-empty">
            Nothing owed
          </span>
        )}
        <span className="queue-rule-line" aria-hidden="true" />
        <kbd>{formatShortcut('session.jumpToWaiting')}</kbd>
      </button>
      {lead.length > 0 && <div ref={leadRef} className="queue-waiting-lead">{lead.map(turnRow)}</div>}
      <button
        type="button"
        className="queue-section-rule queue-agents-toggle"
        data-testid="queue-agents-toggle"
        aria-expanded={Boolean(agentListOpen)}
        title={`Show every agent in this profile (${formatShortcut('sidebar.agentList')})`}
        onClick={onToggleAgentList}
      >
        <span className={`queue-agents-chevron ${agentListOpen ? 'is-open' : ''}`}>▸</span>
        {more === 0 ? 'No more agents' : `${more} more agent${more === 1 ? '' : 's'}`}
        <span className="queue-rule-line" aria-hidden="true" />
        <kbd>{formatShortcut('sidebar.agentList')}</kbd>
      </button>
      {!agentListOpen && more > 0 && (
        <div className="queue-agents-counts" data-testid="queue-agents-counts">
          {parts.filter(([count]) => count > 0).map(([count, label]) => (
            <span key={label}><b>{count}</b> {label}</span>
          ))}
        </div>
      )}
      {agentListOpen && (
        <div className="queue-agent-list" data-testid="queue-agent-list">
          <label className="queue-agent-filter">
            <span aria-hidden="true">⌕</span>
            <input
              data-testid="queue-agent-filter"
              placeholder="filter agents"
              aria-label="Filter agents"
              value={agentFilter}
              onChange={(event) => setAgentFilter(event.target.value)}
            />
          </label>
          {hidden > 0 && (
            <>
              <div className="queue-section-rule queue-band-header" data-testid="queue-also-waiting-header">
                <span>Also waiting {turns.slice(leadCount).filter(matches).length}</span>
                <span className="queue-rule-line" aria-hidden="true" />
              </div>
              {turns.slice(leadCount).filter(matches).map(turnRow)}
            </>
          )}
          <div className="queue-section-rule queue-band-header">
            <span>Working {working.length}</span>
            <span className="queue-rule-line" aria-hidden="true" />
          </div>
          {working.length === 0 ? (
            <div className="queue-band-empty">Nobody else.</div>
          ) : (
            working.map((row) => (
              <QueueRowView
                key={row.session.id}
                row={row}
                selected={selectedId === row.session.id}
                where={where(row)}
                onSelect={() => onSelectSession(row.session.id)}
                onSnooze={onOpenSnooze && ((event) => onOpenSnooze(row.session, event))}
                onOpenActions={(event) => openSessionActions(row.session, event)}
                delegates={delegates.get(row.session.id) ?? []}
                testIdPrefix="queue-settled"
              />
            ))
          )}
          {snoozed.length > 0 && (
            <>
              <div className="queue-section-rule queue-band-header" data-testid="queue-snoozed-header" data-count={snoozed.length}>
                <span>Snoozed {snoozed.length}</span>
                <span className="queue-rule-line" aria-hidden="true" />
              </div>
              {snoozed.map((row) => (
                <QueueRowView
                  key={row.session.id}
                  row={row}
                  selected={selectedId === row.session.id}
                  where={where(row)}
                  wake={formatWakeTimeShort(row.session.turnSnoozedUntil, now)}
                  onSelect={() => onSelectSession(row.session.id)}
                  onWake={onWakeTurn && (() => onWakeTurn(row.session.id))}
                  delegates={delegates.get(row.session.id) ?? []}
                  testIdPrefix="queue-snoozed"
                />
              ))}
            </>
          )}
        </div>
      )}
    </div>
  );
}

function DesktopStrip() {
  const {
    desktops,
    queue,
    selectedDesktopId,
    visualIndexOfDesktop,
    onSelectDesktop,
    onOpenOverview,
  } = useSidebarContext();
  const chipDrop = useDesktopChipDrop();
  const slotted = desktops
    .filter((desktop) => visualIndexOfDesktop(desktop.id) >= 0)
    .sort((a, b) => visualIndexOfDesktop(a.id) - visualIndexOfDesktop(b.id));
  const extras = desktops.filter((desktop) => visualIndexOfDesktop(desktop.id) < 0);
  const waitingOn = new Set((queue?.turns ?? []).map((row) => row.desktopId));
  const current = desktops.find((desktop) => desktop.id === selectedDesktopId);
  const currentIsExtra = Boolean(current && visualIndexOfDesktop(current.id) < 0);

  return (
    <div className="queue-desktop-strip" data-testid="queue-desktop-strip">
      <div className="queue-desktop-chips">
        {slotted.map((desktop) => {
          const slot = visualIndexOfDesktop(desktop.id) + 1;
          const waiting = waitingOn.has(desktop.id);
          const { dropClass, dropHandlers } = chipDrop(desktop);
          return (
            <button
              key={desktop.id}
              type="button"
              className={`queue-desktop-chip${desktop.sessions.length || desktop.children.length ? ' has-panes' : ''}${desktop.id === selectedDesktopId ? ' is-current' : ''}${dropClass}`}
              data-testid={`queue-desktop-chip-${slot}`}
              data-desktop-id={desktop.id}
              data-waiting={waiting || undefined}
              title={`${desktop.title} (${slotShortcut(slot)})`}
              onClick={() => onSelectDesktop(desktop.id)}
              {...dropHandlers}
            >
              {slot}
              {waiting && <span className="queue-desktop-chip-waiting" aria-label="has turns waiting" />}
            </button>
          );
        })}
        {extras.length > 0 && (
          <button
            type="button"
            className={`queue-desktop-chip is-extra${currentIsExtra ? ' is-current' : ''}`}
            data-testid="queue-desktop-extras"
            title={`${extras.length} more desktop${extras.length === 1 ? '' : 's'} without a shortcut (${formatShortcut('desktop.overview')})`}
            onClick={onOpenOverview}
          >
            +{extras.length}
            {extras.some((desktop) => waitingOn.has(desktop.id)) && (
              <span className="queue-desktop-chip-waiting" aria-label="has turns waiting" />
            )}
          </button>
        )}
        <SidebarDesktopOverview compact />
      </div>
      <div className="queue-desktop-current" data-testid="queue-desktop-current">
        <span className="queue-desktop-current-name">{current?.title ?? ''}</span>
        {currentIsExtra && <i>no shortcut</i>}
        <kbd>{formatShortcut('desktop.overview')}</kbd>
      </div>
    </div>
  );
}
