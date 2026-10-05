import { formatShortcut } from '../shortcuts/formatShortcut';
import { useSidebarContext } from './SidebarContext';
import { CollapseIcon } from './SidebarIcons';
import './SidebarHeader.css';

export function CommandsButton({ collapsed = false }: { collapsed?: boolean }) {
  const { onOpenCommands } = useSidebarContext();
  return (
    <button
      type="button"
      className={collapsed ? 'icon-btn sidebar-commands' : 'sidebar-header-tool sidebar-commands'}
      title={`Commands (${formatShortcut('ui.commandPalette')})`}
      aria-label="Commands"
      onClick={onOpenCommands}
      disabled={!onOpenCommands}
    >
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" aria-hidden="true">
        <path d="M9.5 1.5 3 9h4l-.5 5.5L13 7H9z" />
      </svg>
    </button>
  );
}

export function SidebarPanelButtons({ collapsed = false }: { collapsed?: boolean }) {
  const { headerActions } = useSidebarContext();
  return headerActions.map((action) => (
    <button
      type="button"
      key={action.id}
      className={`${collapsed ? 'icon-btn' : 'sidebar-panel-button'} ${action.active ? 'active' : ''} ${action.toneClassName ?? ''}`.trim()}
      onClick={action.onClick}
      title={action.title}
      disabled={action.disabled}
      aria-label={action.title}
      aria-pressed={Boolean(action.active)}
    >
      {action.icon}
      {action.unread && <span className="sidebar-notification-dot" aria-label="Unread notifications" />}
    </button>
  ));
}

export function SidebarHeader() {
  const { instance, profileName, onSwitchProfile, onNewSession, onToggleCollapse } = useSidebarContext();
  return (
    <div className="sidebar-header">
      {instance && (
        <div className="sidebar-instance-marker" data-testid="sidebar-instance-marker">
          instance <strong>{instance}</strong>
        </div>
      )}
      <div className="sidebar-header-main">
        <button
          type="button"
          className="sidebar-profile-pill"
          title={`Switch profile (${formatShortcut('profile.switch')})`}
          onClick={onSwitchProfile}
          disabled={!onSwitchProfile}
        >
          <strong>{profileName ?? 'Profile'}</strong>
          <span aria-hidden="true">▾</span>
        </button>
        <button
          type="button"
          className="sidebar-header-tool sidebar-header-new"
          title={`New agent (${formatShortcut('session.new')})`}
          aria-label="New agent"
          onClick={onNewSession}
        >New</button>
        <CommandsButton />
        <button
          type="button"
          className="sidebar-header-tool"
          title={`Collapse sidebar (${formatShortcut('session.toggleSidebar')})`}
          aria-label="Collapse sidebar"
          onClick={onToggleCollapse}
        ><CollapseIcon /></button>
      </div>
      <div className="sidebar-header-panels"><SidebarPanelButtons /></div>
    </div>
  );
}
