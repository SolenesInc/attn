import { formatShortcut } from '../shortcuts/formatShortcut';
import { CriticalNotificationStrip } from './CriticalNotificationStrip';
import { QueueBands, QueueSnoozedSection } from './QueueBands';
import './Sidebar.css';
import { SidebarCollapsed, SidebarFooter, SidebarHeader, SidebarPopovers } from './SidebarChrome';
import { SidebarContext, useSidebarContext } from './SidebarContext';
import { HomeIcon } from './SidebarIcons';
import type { SidebarProps } from './sidebarTypes';
import {
  SidebarAutomationGroups,
  SidebarMutedWorkspaces,
  SidebarWorkspaceList,
} from './SidebarWorkspaces';
import { useSidebarState } from './useSidebarState';
export type { DockItem, SidebarHeaderAction } from './sidebarTypes';

export function Sidebar(props: SidebarProps) {
  const state = useSidebarState(props);
  return (
    <SidebarContext.Provider value={state}>
      {props.collapsed ? <SidebarCollapsed /> : <SidebarExpanded />}
    </SidebarContext.Provider>
  );
}

function SidebarExpanded() {
  const {
    selectedId,
    criticalNotifications,
    onOpenNotifications,
    queue,
    crew,
    onWakeCrewMember,
    onSleepCrewMember,
    openCrewMemberActions,
    onSettleTurn,
    onOpenSnooze,
    onWakeTurn,
    onScreenSessionIds,
    harnessLogosEnabled,
    leafDrag,
    onNewWorkspaceDrop,
    onSelectSession,
    onGoToDashboard,
    homeActive,
    snoozedExpanded,
    setSnoozedExpanded,
    displayMode,
    openSessionActions,
    allSessions,
    newWorkspaceDropActive,
    setNewWorkspaceDropActive,
    reorderDrag,
    sessionDragGhost,
  } = useSidebarContext();
  return (
    <div
      className={`sidebar sidebar--display-${displayMode} ${harnessLogosEnabled ? '' : 'sidebar--hide-harness-logos'}`.trim()}
    >
      <SidebarHeader />
      {/* Above Home, because it outranks it: this only exists when something is owed. */}
      {onOpenNotifications && (
        <CriticalNotificationStrip
          count={criticalNotifications?.count ?? 0}
          title={criticalNotifications?.title ?? ''}
          onOpen={onOpenNotifications}
        />
      )}

      {/* Home sits above everything, the chief's slot included: it is the one row that is not an agent. */}
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

      <SidebarCrewManage />

      {queue && (
        <QueueBands
          bands={queue}
          crew={crew}
          onWakeCrewMember={onWakeCrewMember}
          onSleepCrewMember={onSleepCrewMember}
          onOpenCrewMemberActions={openCrewMemberActions}
          selectedId={selectedId}
          onSelectSession={onSelectSession}
          onSettleTurn={(id) => onSettleTurn?.(id)}
          onScreenSessionIds={onScreenSessionIds}
          onOpenActions={openSessionActions}
          onOpenSnooze={onOpenSnooze}
          allSessions={allSessions}
        />
      )}

      <div className={`session-list ${reorderDrag ? 'session-list--reordering' : ''}`.trim()}>
        <SidebarWorkspaceList />
        <SidebarAutomationGroups />
        {leafDrag && (
          <div
            className={`new-workspace-dropzone${newWorkspaceDropActive ? ' new-workspace-dropzone--active' : ''}`}
            data-testid="new-workspace-dropzone"
            onPointerEnter={() => setNewWorkspaceDropActive(true)}
            onPointerLeave={() => setNewWorkspaceDropActive(false)}
            onPointerUp={() => {
              setNewWorkspaceDropActive(false);
              onNewWorkspaceDrop?.();
            }}
          >
            <span className="new-workspace-dropzone-plus">＋</span>
            <span className="new-workspace-dropzone-label">New workspace</span>
          </div>
        )}
        {sessionDragGhost && (
          <div
            className="session-drag-ghost"
            data-testid="session-drag-ghost"
            style={{ left: sessionDragGhost.x + 12, top: sessionDragGhost.y + 12 }}
          >
            {sessionDragGhost.label}
          </div>
        )}
      </div>

      {/* Above muted: *not yet* is nearer to your attention than *not ever*. */}
      {queue && onWakeTurn && (
        <QueueSnoozedSection
          rows={queue.snoozed}
          selectedId={selectedId}
          expanded={snoozedExpanded}
          onToggleExpanded={() => setSnoozedExpanded(!snoozedExpanded)}
          onSelectSession={onSelectSession}
          onWakeTurn={onWakeTurn}
          allSessions={allSessions}
        />
      )}

      <SidebarMutedWorkspaces />
      <SidebarFooter />
      <SidebarPopovers />
    </div>
  );
}

function SidebarCrewManage() {
  const { crew, onManageCrew } = useSidebarContext();
  if (!crew?.length || !onManageCrew) return null;
  return (
    <button type="button" className="sidebar-crew-manage" data-testid="manage-crew" onClick={onManageCrew}>
      <span>Manage crew</span>
      <span className="sidebar-crew-count">{crew.length}</span>
    </button>
  );
}

export {
  DiffIcon,
  EditorIcon,
  MarkdownIcon,
  NotebookIcon,
  PRsIcon,
  WorkflowIcon,
} from './SidebarIcons';
