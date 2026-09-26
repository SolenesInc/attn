import { formatShortcut } from '../shortcuts/formatShortcut';
import { CriticalNotificationStrip } from './CriticalNotificationStrip';
import { QueueBar } from './QueueBar';
import { QueueSidebar } from './QueueSidebar';
import './Sidebar.css';
import { SidebarCollapsed, SidebarCrewManage, SidebarFooter, SidebarHeader, SidebarPopovers } from './SidebarChrome';
import { SidebarContext, useSidebarContext } from './SidebarContext';
import { HomeIcon } from './SidebarIcons';
import type { SidebarProps } from './sidebarTypes';
import {
  SidebarAutomationGroups,
  SidebarWorkspaceList,
} from './SidebarWorkspaces';
import { useSidebarState } from './useSidebarState';
export type { DockItem, SidebarHeaderAction } from './sidebarTypes';

export function Sidebar(props: SidebarProps) {
  const state = useSidebarState(props);
  const QueueChrome = props.collapsed ? QueueBar : QueueSidebar;
  const TreeChrome = props.collapsed ? SidebarCollapsed : SidebarExpanded;
  return (
    <SidebarContext.Provider value={state}>
      {props.queue ? <QueueChrome /> : <TreeChrome />}
    </SidebarContext.Provider>
  );
}

function SidebarExpanded() {
  const {
    criticalNotifications,
    onOpenNotifications,
    harnessLogosEnabled,
    leafDrag,
    onNewWorkspaceDrop,
    onGoToDashboard,
    homeActive,
    displayMode,
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
            <span className="new-workspace-dropzone-label">New desktop</span>
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

      <SidebarFooter />
      <SidebarPopovers />
    </div>
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
