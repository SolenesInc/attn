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
  SidebarDesktopList,
  SidebarDesktopOverview,
} from './SidebarDesktops';
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
    onNewDesktopDrop,
    onGoToDashboard,
    homeActive,
    newDesktopDropActive,
    setNewDesktopDropActive,
    reorderDrag,
    sessionDragGhost,
  } = useSidebarContext();
  return (
    <div
      className={`sidebar ${harnessLogosEnabled ? '' : 'sidebar--hide-harness-logos'}`.trim()}
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
      <SidebarDesktopOverview />

      <div className={`session-list ${reorderDrag ? 'session-list--reordering' : ''}`.trim()}>
        <SidebarDesktopList />
        <SidebarAutomationGroups />
        {leafDrag && (
          <div
            className={`new-desktop-dropzone${newDesktopDropActive ? ' new-desktop-dropzone--active' : ''}`}
            data-testid="new-desktop-dropzone"
            onPointerEnter={() => setNewDesktopDropActive(true)}
            onPointerLeave={() => setNewDesktopDropActive(false)}
            onPointerUp={() => {
              setNewDesktopDropActive(false);
              onNewDesktopDrop?.();
            }}
          >
            <span className="new-desktop-dropzone-plus">＋</span>
            <span className="new-desktop-dropzone-label">New desktop</span>
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
