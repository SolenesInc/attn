import { useEffect, useRef } from 'react';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { RenamePopover } from './RenamePopover';
import { SessionActionsPopover } from './SessionActionsPopover';
import { CrewMemberActionsPopover } from './CrewMemberActionsPopover';
import { crewDisplayName } from '../utils/crewName';
import './Sidebar.css';
import { useSidebarContext } from './SidebarContext';
import { ExpandIcon, HomeIcon, PlusIcon } from './SidebarIcons';
import { desktopShortcut, hasNoLeaves } from './sidebarModel';
import { DesktopChip } from './SidebarDesktops';
import { CrewRowView } from './QueueRows';
import { CommandsButton, SidebarPanelButtons } from './SidebarHeader';
export { SidebarHeader } from './SidebarHeader';

export function SidebarCollapsed() {
  const {
    instance,
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
        <CommandsButton collapsed />
        <div className="icon-divider" />
        <SidebarPanelButtons collapsed />
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

export function SidebarSleepingCrew() {
  const { crew, desktops, onManageCrew, onWakeCrewMember, openCrewMemberActions } = useSidebarContext();
  if (!crew?.length) return null;
  const awakeMembers = new Set(desktops.flatMap((desktop) => desktop.sessions.map((session) => session.crewMember)));
  const sleeping = crew.filter((member) => !awakeMembers.has(member.id));
  return (
    <div className="sidebar-sleeping-crew">
      <div className="queue-section-rule queue-crew-rule">
        <span>Crew</span>
        <span className="queue-rule-line" aria-hidden="true" />
        {onManageCrew && <button type="button" data-testid="manage-crew" onClick={onManageCrew}>manage</button>}
      </div>
      {sleeping.map((member) => (
        <CrewRowView
          key={member.id}
          member={member.id}
          agent={member.resolved_agent}
          selected={false}
          delegates={[]}
          onWake={onWakeCrewMember && (() => onWakeCrewMember(member.id))}
          onOpenMemberActions={(event) => openCrewMemberActions(member.id, event)}
        />
      ))}
    </div>
  );
}

export function SidebarPopovers() {
  const {
    crew,
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
          desktopLabel={crew?.find((member) => member.id === crewActionsTarget.member)?.launch_desktop?.label}
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
