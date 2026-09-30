import { useEffect, useRef } from 'react';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { GridLayoutControl } from './grid/GridLayoutControl';
import { RenamePopover } from './RenamePopover';
import { SessionActionsPopover } from './SessionActionsPopover';
import { CrewMemberActionsPopover } from './CrewMemberActionsPopover';
import { crewDisplayName } from '../utils/crewName';
import './Sidebar.css';
import { useSidebarContext } from './SidebarContext';
import { CollapseIcon, ExpandIcon, HomeIcon, PlusIcon } from './SidebarIcons';
import { desktopShortcut, hasNoLeaves } from './sidebarModel';
import { DesktopChip } from './SidebarDesktops';
import { SidebarSettings } from './SidebarSettings';

export function SidebarCollapsed() {
  const {
    instance,
    headerActions,
    gridLayout,
    onSelectGridLayout,
    onNewSession,
    onGoToDashboard,
    homeActive,
    onToggleCollapse,
  } = useSidebarContext();
  return (
    <div className="sidebar collapsed">
      {instance && (
        <div
          className="sidebar-instance-marker sidebar-instance-marker--collapsed"
          title={`Instance: ${instance}`}
        >
          {instance}
        </div>
      )}
      <div className="icon-rail">
        <button
          className={`icon-btn ${homeActive ? 'active' : ''}`}
          onClick={onGoToDashboard}
          title={`Home (${formatShortcut('session.goToDashboard')})`}
          aria-label="Home"
          aria-current={homeActive ? 'page' : undefined}
        >
          <HomeIcon />
        </button>
        <div className="icon-divider" />
        {gridLayout && onSelectGridLayout && (
          <GridLayoutControl layout={gridLayout} onSelect={onSelectGridLayout} />
        )}
        {headerActions.map((action) => (
          <button
            key={action.id}
            className={`icon-btn sidebar-tool-btn ${action.active ? 'active' : ''} ${action.toneClassName || ''}`}
            onClick={action.onClick}
            title={action.title}
            disabled={action.disabled}
            aria-label={action.title}
          >
            {action.icon}
            {action.badge !== undefined && (
              <span className="sidebar-tool-badge">
                {typeof action.badge === 'number' && action.badge > 9 ? '9+' : action.badge}
              </span>
            )}
          </button>
        ))}
        <div className="icon-divider" />
        <RailDesktops />
        <button
          className="icon-btn"
          onClick={onNewSession}
          title={`New Session (${formatShortcut('session.new')})`}
        >
          <PlusIcon />
        </button>
        <button className="icon-btn expand-btn" onClick={onToggleCollapse} title="Expand sidebar">
          <ExpandIcon />
        </button>
      </div>
    </div>
  );
}

function RailDesktops() {
  const {
    selectedDesktopId,
    onSelectDesktop,
    sessionWantsAttention,
    visibleVisualOrder,
    visualIndexOfDesktop,
  } = useSidebarContext();
  const desktopListRef = useRef<HTMLDivElement>(null);
  const desktopOrder = JSON.stringify(visibleVisualOrder.map((desktop) => desktop.id));
  useEffect(() => {
    const list = desktopListRef.current;
    if (!list) return;
    const revealCurrent = () => {
      list.querySelector('[aria-current="true"]')?.scrollIntoView({ block: 'nearest' });
    };
    revealCurrent();
    const observer = new ResizeObserver(revealCurrent);
    observer.observe(list);
    return () => observer.disconnect();
  }, [selectedDesktopId, desktopOrder]);
  return (
    <div className="rail-desktops" ref={desktopListRef}>
      {visibleVisualOrder.map((desktopView) => {
        const shortcut = desktopShortcut(visualIndexOfDesktop(desktopView.id));
        const label = shortcut ? `${desktopView.title} (${shortcut})` : desktopView.title;
        const current = selectedDesktopId === desktopView.id;
        return (
          <button
            key={desktopView.id}
            className={`icon-btn session-icon ${current ? 'active' : ''}`}
            onClick={() => onSelectDesktop(desktopView.id)}
            title={label}
            aria-label={label}
            aria-current={current ? 'true' : undefined}
          >
            <DesktopChip
              number={desktopView.desktop?.number}
              current={current}
              empty={hasNoLeaves(desktopView)}
            >
              {desktopView.sessions.some(sessionWantsAttention) && (
                <span
                  className={`mini-badge ${desktopView.status === 'pending_approval' ? 'pending' : ''} ${desktopView.status === 'unknown' ? 'unknown' : ''}`}
                />
              )}
            </DesktopChip>
          </button>
        );
      })}
    </div>
  );
}

export function SidebarFooter() {
  const { dockItems, dockCollapsed, onToggleDockCollapsed } = useSidebarContext();
  return (
    <>
      <div className={`sidebar-footer ${dockCollapsed ? 'sidebar-footer--collapsed' : ''}`.trim()}>
        <div className="sidebar-footer-head">
          <span className="sidebar-footer-label">Dock</span>
          {onToggleDockCollapsed && (
            <button
              type="button"
              className="dock-collapse-btn"
              onClick={onToggleDockCollapsed}
              aria-expanded={!dockCollapsed}
              title={dockCollapsed ? 'Show dock' : 'Hide dock'}
              aria-label={dockCollapsed ? 'Show dock' : 'Hide dock'}
            >
              {dockCollapsed ? '+' : '−'}
            </button>
          )}
        </div>
        {!dockCollapsed && (
          <div className="sidebar-dock-items">
            {dockItems.map((item) =>
              item.onClick ? (
                <button
                  key={item.id}
                  type="button"
                  className={`shortcut-hint shortcut-hint--action ${item.active ? 'active' : ''}`.trim()}
                  data-active={item.active ? 'true' : 'false'}
                  onClick={item.onClick}
                  title={item.label}
                >
                  {item.keys && <span className="shortcut-hint-keys">{item.keys}</span>}
                  {item.label}
                </button>
              ) : (
                <span
                  key={item.id}
                  className={`shortcut-hint ${item.active ? 'active' : ''}`.trim()}
                  data-active={item.active ? 'true' : 'false'}
                >
                  {item.keys && <span className="shortcut-hint-keys">{item.keys}</span>}
                  {item.label}
                </span>
              ),
            )}
          </div>
        )}
      </div>
    </>
  );
}

export function SidebarCrewManage() {
  const { crew, onManageCrew } = useSidebarContext();
  if (!crew?.length || !onManageCrew) return null;
  return (
    <button type="button" className="sidebar-crew-manage" data-testid="manage-crew" onClick={onManageCrew}>
      <span>Manage crew</span>
      <span className="sidebar-crew-count">{crew.length}</span>
    </button>
  );
}

export function SidebarPopovers() {
  const {
    onRenameSession,
    onRenameDesktop,
    onChangeChiefOfStaff,
    onCloseSession,
    onReloadSession,
    renameTarget,
    setRenameTarget,
    sessionActionsTarget,
    setSessionActionsTarget,
    crewActionsTarget,
    setCrewActionsTarget,
    onOpenCrewMemberDetails,
  } = useSidebarContext();
  return (
    <>
      {renameTarget && (
        <RenamePopover
          key={`${renameTarget.kind}:${renameTarget.id}`}
          initialValue={renameTarget.name}
          defaultName={renameTarget.defaultName}
          label={renameTarget.kind === 'desktop' ? 'Rename desktop' : 'Rename session'}
          anchor={renameTarget.anchor}
          onSubmit={async (value) => {
            if (renameTarget.kind === 'desktop') {
              await onRenameDesktop?.(renameTarget.id, value);
            } else {
              await onRenameSession?.(renameTarget.id, value);
            }
          }}
          onClose={() => setRenameTarget(null)}
        />
      )}
      {sessionActionsTarget && (
        <SessionActionsPopover
          sessionLabel={sessionActionsTarget.label}
          chiefOfStaff={sessionActionsTarget.chiefOfStaff}
          anchor={sessionActionsTarget.anchor}
          canRename={Boolean(onRenameSession)}
          onRename={() => {
            setRenameTarget({
              kind: 'session',
              id: sessionActionsTarget.id,
              name: sessionActionsTarget.label,
              anchor: sessionActionsTarget.anchor,
            });
          }}
          onChangeChiefOfStaff={(enabled) =>
            onChangeChiefOfStaff?.(sessionActionsTarget.id, enabled)
          }
          onCloseSession={() => onCloseSession(sessionActionsTarget.id)}
          onReloadSession={() => onReloadSession(sessionActionsTarget.id)}
          onMemberDetails={
            sessionActionsTarget.crewMember && onOpenCrewMemberDetails
              ? () =>
                  onOpenCrewMemberDetails(
                    sessionActionsTarget.crewMember!,
                    sessionActionsTarget.trigger,
                  )
              : undefined
          }
          onClose={() => setSessionActionsTarget(null)}
        />
      )}
      {crewActionsTarget && (
        <CrewMemberActionsPopover
          memberName={crewDisplayName(crewActionsTarget.member)}
          anchor={crewActionsTarget.anchor}
          onOpenDetails={() => {
            const target = crewActionsTarget;
            setCrewActionsTarget(null);
            onOpenCrewMemberDetails?.(target.member, target.trigger);
          }}
          onClose={() => setCrewActionsTarget(null)}
        />
      )}
    </>
  );
}

export function SidebarHeader() {
  const {
    instance,
    headerActions,
    gridLayout,
    onSelectGridLayout,
    queueModeEnabled,
    onToggleQueueMode,
    crewQueueEnabled,
    onToggleCrewQueue,
    harnessLogosEnabled,
    onToggleHarnessLogos,
    desktopSelectionStyle,
    onDesktopSelectionStyleChange,
    onNewSession,
    onToggleCollapse,
  } = useSidebarContext();
  return (
    <>
      <div className="sidebar-header">
        {instance && (
          <div className="sidebar-instance-marker">
            instance <strong>{instance}</strong>
          </div>
        )}
        <div className="sidebar-tool-row">
          {gridLayout && onSelectGridLayout && (
            <GridLayoutControl layout={gridLayout} onSelect={onSelectGridLayout} />
          )}
          {headerActions.map((action) => (
            <button
              key={action.id}
              className={`sidebar-tool-btn ${action.active ? 'active' : ''} ${action.toneClassName || ''}`}
              onClick={action.onClick}
              title={action.title}
              disabled={action.disabled}
              aria-label={action.title}
            >
              {action.icon}
              {action.badge !== undefined && (
                <span className="sidebar-tool-badge">
                  {typeof action.badge === 'number' && action.badge > 9 ? '9+' : action.badge}
                </span>
              )}
            </button>
          ))}
          <button
            className="collapse-btn"
            onClick={onToggleCollapse}
            title="Collapse sidebar"
            aria-label="Collapse sidebar"
          >
            <CollapseIcon />
          </button>
        </div>
        <div className="sidebar-header-row">
          <button
            className="new-session-btn"
            onClick={onNewSession}
            title={`New Session (${formatShortcut('session.new')})`}
            aria-label="New Session"
          >
            <PlusIcon />
          </button>
          <SidebarSettings
            queueModeEnabled={queueModeEnabled}
            onToggleQueueMode={onToggleQueueMode}
            crewQueueEnabled={crewQueueEnabled}
            onToggleCrewQueue={onToggleCrewQueue}
            harnessLogosEnabled={harnessLogosEnabled}
            onToggleHarnessLogos={onToggleHarnessLogos}
            desktopSelectionStyle={desktopSelectionStyle}
            onDesktopSelectionStyleChange={onDesktopSelectionStyleChange}
          />
        </div>
      </div>
    </>
  );
}
