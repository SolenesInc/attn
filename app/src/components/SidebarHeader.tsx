import { formatShortcut } from '../shortcuts/formatShortcut';
import { useSidebarContext } from './SidebarContext';
import { CollapseIcon } from './SidebarIcons';
import './SidebarHeader.css';

export function FlowToggleButton() {
  const { queue, collapsed, onToggleFlow } = useSidebarContext();
  const title = queue ? 'Switch to desktop flow' : 'Switch to queue flow';
  return (
    <button
      type="button"
      className={collapsed ? (queue ? 'queue-bar-tool' : 'icon-btn') : 'sidebar-header-tool'}
      title={title}
      aria-label={title}
      onClick={onToggleFlow}
      disabled={!onToggleFlow}
    >
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        {queue ? (
          <>
            <rect x="2" y="2" width="12" height="9" rx="1.5" />
            <path d="M8 11v3M5.5 14h5" />
          </>
        ) : (
          <>
            <path d="M6 3h8M6 8h8M6 13h8" />
            <path d="m1.5 6 2 2-2 2" />
            <path d="M2.5 3h.01M2.5 13h.01" />
          </>
        )}
      </svg>
    </button>
  );
}

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
        <FlowToggleButton />
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
