import type { MouseEvent as ReactMouseEvent } from 'react';
import { useMemo, useState } from 'react';
import { isAttentionSessionState } from '../types/sessionState';
import { type TileContentState } from '../types/desktop';
import { delegatesByDispatcher } from '../utils/delegationLinks';
import { sessionParticipatesInQueue } from '../utils/queueBands';
import { automationRunGroups } from '../utils/automationRuns';
import type { DockItem, LocalSession, SidebarProps, SidebarDesktop } from './sidebarTypes';
import { useSidebarDrag } from './useSidebarDrag';

const EMPTY_DOCK_ITEMS: DockItem[] = [];
const EMPTY_TILE_CONTENTS: Record<string, TileContentState> = {};

export function useSidebarState({
  desktops,
  visualIndexByDesktopId,
  selectedId,
  selectedDesktopId,
  selectedTile = null,
  tileContents = EMPTY_TILE_CONTENTS,
  collapsed,
  surface,
  instance = '',
  headerActions,
  criticalNotifications,
  onOpenNotifications,
  dockItems = EMPTY_DOCK_ITEMS,
  dockCollapsed = false,
  onToggleDockCollapsed,
  queue = null,
  crew,
  onWakeCrewMember,
  onSleepCrewMember,
  onManageCrew,
  onOpenCrewMemberDetails,
  onSettleTurn,
  onWalkRuns,
  onJumpToWaiting,
  profileName,
  onSwitchProfile,
  onOpenCommands,
  onOpenAgents,
  peeksSilenced = false,
  commandsBadge,
  agentListOpen = false,
  onToggleAgentList,
  onOpenOverview,
  onOpenSnooze,
  onWakeTurn,
  onScreenSessionIds,
  onRenameSession,
  onRenameDesktop,
  onChangeChiefOfStaff,
  queueModeEnabled = false,
  onToggleQueueMode,
  crewQueueEnabled = false,
  onToggleCrewQueue,
  harnessLogosEnabled = true,
  onToggleHarnessLogos,
  desktopSelectionStyle = 'rail',
  onDesktopSelectionStyleChange,
  leafDrag = null,
  dragHoverDesktopId = null,
  onDesktopDragEnter,
  onDesktopDragLeave,
  onDesktopDragDrop,
  onNewDesktopDrop,
  onSessionDragStart,
  onSessionDragEnd,
  onDesktopReorder,
  onSelectSession,
  onSelectDesktop,
  onSelectTile,
  onCloseTile,
  onReloadTile,
  onNewSession,
  onCloseSession,
  onReloadSession,
  onGoToDashboard,
  homeActive = false,
  onToggleCollapse,
}: SidebarProps) {
  const sessionWantsAttention = (session: LocalSession) =>
    queue
      ? sessionParticipatesInQueue(session, crewQueueEnabled) && Boolean(session.turnOwed)
      : isAttentionSessionState(session.state);

  const [agentFilter, setAgentFilter] = useState('');
  if (!agentListOpen && agentFilter) setAgentFilter('');
  const [renameTarget, setRenameTarget] = useState<{
    kind: 'session' | 'desktop';
    id: string;
    name: string;
    defaultName?: string;
    anchor: { top: number; left: number };
  } | null>(null);
  const [sessionActionsTarget, setSessionActionsTarget] = useState<{
    id: string;
    label: string;
    chiefOfStaff: boolean;
    crewMember?: string;
    trigger: HTMLElement;
    anchor: { top: number; left: number };
  } | null>(null);
  const [crewActionsTarget, setCrewActionsTarget] = useState<{
    member: string;
    trigger: HTMLElement;
    anchor: { top: number; left: number };
  } | null>(null);
  const [popoverSurface, setPopoverSurface] = useState(surface);
  if (surface !== popoverSurface) {
    setPopoverSurface(surface);
    setRenameTarget(null);
    setSessionActionsTarget(null);
    setCrewActionsTarget(null);
  }

  const openDesktopRename = (
    desktopId: string,
    desktop: { name: string; defaultLabel: string },
    event: ReactMouseEvent,
  ) => {
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    setRenameTarget({
      kind: 'desktop',
      id: desktopId,
      name: desktop.name,
      defaultName: desktop.defaultLabel,
      anchor: { top: rect.bottom + 4, left: rect.left },
    });
  };
  const openSessionActions = (
    session: { id: string; label: string; chiefOfStaff?: boolean; crewMember?: string },
    event: ReactMouseEvent,
  ) => {
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    setSessionActionsTarget({
      id: session.id,
      label: session.label,
      chiefOfStaff: Boolean(session.chiefOfStaff),
      crewMember: session.crewMember,
      trigger: event.currentTarget as HTMLElement,
      anchor: { top: rect.bottom + 4, left: rect.right - 190 },
    });
  };
  const openCrewMemberActions = (member: string, event: ReactMouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    setCrewActionsTarget({
      member,
      trigger: event.currentTarget,
      anchor: { top: rect.bottom + 4, left: rect.right - 190 },
    });
  };

  const automationGroups = useMemo(() => automationRunGroups(desktops, Date.now()), [desktops]);
  const allSessions = useMemo(() => {
    const byId = new Map<string, LocalSession>();
    for (const desktopView of desktops) {
      for (const session of desktopView.sessions) byId.set(session.id, session);
    }
    return [...byId.values()];
  }, [desktops]);
  const delegates = useMemo(() => delegatesByDispatcher(allSessions), [allSessions]);
  const rowDelegation = (session: LocalSession) => ({
    delegates: delegates.get(session.id) ?? [],
  });
  const visibleDesktops = desktops;
  const canAcceptLeafDrag = (desktopView: SidebarDesktop) =>
    Boolean(
      leafDrag &&
        desktopView.id !== leafDrag.sourceDesktopId,
    );

  const desktopDragClass = (desktopView: SidebarDesktop) => {
    if (!leafDrag) {
      return '';
    }
    if (!canAcceptLeafDrag(desktopView)) {
      return ' desktop-group--drag-disabled';
    }
    if (dragHoverDesktopId === desktopView.id) {
      return ' desktop-group--drag-entering';
    }
    return ' desktop-group--drag-target';
  };
  const visibleVisualOrder = desktops;
  const reorderParticipants = visibleDesktops.filter((desktopView) => desktopView.desktop);
  const visualIndexOfDesktop = (id: string) => visualIndexByDesktopId.get(id) ?? -1;

  const [newDesktopDropActive, setNewDesktopDropActive] = useState(false);
  const {
    reorderDrag,
    sessionDragGhost,
    draggingSessionId,
    reorderSeamIndexByDesktopId,
    reorderTrailingSeamIndex,
    lastReorderParticipantId,
    renderReorderSeam,
    handleHeaderPointerDown,
    handleHeaderClickCapture,
    handleSessionPointerDown,
    handleSessionClickCapture,
  } = useSidebarDrag({
    reorderParticipants,
    onDesktopReorder,
    onSessionDragStart,
    onSessionDragEnd,
  });

  return {
    desktops,
    selectedId,
    selectedDesktopId,
    selectedTile,
    tileContents,
    collapsed,
    instance,
    headerActions,
    criticalNotifications,
    onOpenNotifications,
    dockItems,
    dockCollapsed,
    onToggleDockCollapsed,
    queue,
    crew,
    onWakeCrewMember,
    onSleepCrewMember,
    onManageCrew,
    onOpenCrewMemberDetails,
    onSettleTurn,
    onWalkRuns,
    onJumpToWaiting,
    profileName,
    onSwitchProfile,
    onOpenCommands,
    onOpenAgents,
    peeksSilenced,
    commandsBadge,
    agentListOpen,
    onToggleAgentList,
    agentFilter,
    setAgentFilter,
    onOpenOverview,
    onOpenSnooze,
    onWakeTurn,
    onScreenSessionIds,
    onRenameSession,
    onRenameDesktop,
    onChangeChiefOfStaff,
    queueModeEnabled,
    onToggleQueueMode,
    crewQueueEnabled,
    onToggleCrewQueue,
    harnessLogosEnabled,
    onToggleHarnessLogos,
    desktopSelectionStyle,
    onDesktopSelectionStyleChange,
    leafDrag,
    dragHoverDesktopId,
    onDesktopDragEnter,
    onDesktopDragLeave,
    onDesktopDragDrop,
    onNewDesktopDrop,
    onSessionDragStart,
    onSelectSession,
    onSelectDesktop,
    onSelectTile,
    onCloseTile,
    onReloadTile,
    onNewSession,
    onCloseSession,
    onReloadSession,
    onGoToDashboard,
    homeActive,
    onToggleCollapse,
    sessionWantsAttention,
    renameTarget,
    setRenameTarget,
    sessionActionsTarget,
    setSessionActionsTarget,
    crewActionsTarget,
    setCrewActionsTarget,
    openDesktopRename,
    openSessionActions,
    openCrewMemberActions,
    automationGroups,
    allSessions,
    delegates,
    rowDelegation,
    visibleDesktops,
    visibleVisualOrder,
    canAcceptLeafDrag,
    desktopDragClass,
    visualIndexOfDesktop,
    newDesktopDropActive,
    setNewDesktopDropActive,
    reorderDrag,
    sessionDragGhost,
    draggingSessionId,
    reorderSeamIndexByDesktopId,
    reorderTrailingSeamIndex,
    lastReorderParticipantId,
    renderReorderSeam,
    handleHeaderPointerDown,
    handleHeaderClickCapture,
    handleSessionPointerDown,
    handleSessionClickCapture,
  };
}
