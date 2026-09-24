import type { MouseEvent as ReactMouseEvent } from 'react';
import { useMemo, useState } from 'react';
import { isAttentionSessionState } from '../types/sessionState';
import { type TileContentState } from '../types/workspace';
import { delegatesByDispatcher } from '../utils/delegationLinks';
import { sessionParticipatesInQueue } from '../utils/queueBands';
import { UNPLACED_GROUP_ID } from '../utils/workspaceViewModels';
import { groupAutomationSessions, isSessionless } from './sidebarModel';
import type { DockItem, LocalSession, SidebarProps, SidebarWorkspace } from './sidebarTypes';
import { useSidebarDrag } from './useSidebarDrag';

const EMPTY_DOCK_ITEMS: DockItem[] = [];
const EMPTY_TILE_CONTENTS: Record<string, TileContentState> = {};

export function useSidebarState({
  workspaces,
  visualIndexByWorkspaceId,
  selectedId,
  selectedWorkspaceId,
  selectedTile = null,
  tileContents = EMPTY_TILE_CONTENTS,
  collapsed,
  instance = '',
  headerActions,
  criticalNotifications,
  onOpenNotifications,
  gridLayout,
  onSelectGridLayout,
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
  onOpenSnooze,
  onWakeTurn,
  onScreenSessionIds,
  onRenameSession,
  onRenameWorkspace,
  onChangeChiefOfStaff,
  showSessionless = false,
  onToggleShowSessionless,
  queueModeEnabled = false,
  onToggleQueueMode,
  crewQueueEnabled = false,
  onToggleCrewQueue,
  harnessLogosEnabled = true,
  onToggleHarnessLogos,
  workspaceSelectionStyle = 'rail',
  onWorkspaceSelectionStyleChange,
  leafDrag = null,
  dragHoverWorkspaceId = null,
  onWorkspaceDragEnter,
  onWorkspaceDragLeave,
  onWorkspaceDragDrop,
  onNewWorkspaceDrop,
  onSessionDragStart,
  onSessionDragEnd,
  onWorkspaceReorder,
  onSelectSession,
  onTriggerNudge,
  onSelectWorkspace,
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

  const [snoozedExpanded, setSnoozedExpanded] = useState(false);
  const [expandedAutomationGroups, setExpandedAutomationGroups] = useState<Set<string>>(
    () => new Set(),
  );
  const [displayMode, setDisplayMode] = useState<'open' | 'tight' | 'boxed'>('boxed');
  const [renameTarget, setRenameTarget] = useState<{
    kind: 'session' | 'workspace';
    id: string;
    name: string;
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

  const openRename = (
    kind: 'session' | 'workspace',
    id: string,
    name: string,
    event: ReactMouseEvent,
  ) => {
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    setRenameTarget({ kind, id, name, anchor: { top: rect.bottom + 4, left: rect.left } });
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

  const automationGroups = useMemo(
    () => groupAutomationSessions(workspaces),
    [workspaces],
  );
  const allSessions = useMemo(() => {
    const byId = new Map<string, LocalSession>();
    for (const workspace of workspaces) {
      for (const session of workspace.sessions) byId.set(session.id, session);
    }
    return [...byId.values()];
  }, [workspaces]);
  const delegates = useMemo(() => delegatesByDispatcher(allSessions), [allSessions]);
  const rowDelegation = (session: LocalSession) => ({
    delegates: delegates.get(session.id) ?? [],
  });
  const toggleAutomationGroup = (definitionId: string) => {
    setExpandedAutomationGroups((current) => {
      const next = new Set(current);
      if (next.has(definitionId)) {
        next.delete(definitionId);
      } else {
        next.add(definitionId);
      }
      return next;
    });
  };

  const withoutAutomationRows = (workspace: SidebarWorkspace): SidebarWorkspace => ({
    ...workspace,
    sessions: workspace.sessions.filter((session) => !session.automation),
    children: workspace.children.filter(
      (child) => child.kind === 'tile' || !child.session.automation,
    ),
  });

  // The chief holds its anchored slot whatever its workspace is, so a workspace
  // that survives in the tree must not draw it a second time.
  const withoutChiefRow = (workspace: SidebarWorkspace): SidebarWorkspace => {
    if (!queue || !workspace.sessions.some((session) => session.chiefOfStaff)) {
      return workspace;
    }
    return {
      ...workspace,
      sessions: workspace.sessions.filter((session) => !session.chiefOfStaff),
      children: workspace.children.filter(
        (child) => child.kind === 'tile' || !child.session.chiefOfStaff,
      ),
    };
  };

  const isWorkspaceVisible = (workspace: SidebarWorkspace) =>
    !isSessionless(workspace) ||
    workspace.hasUnresolvedAgentPanes ||
    showSessionless;
  // Queue mode renders every ordinary agent as a flat row in a band, so drawing
  // its workspace group too would show the same agent twice.
  const isTreeWorkspace = (workspace: SidebarWorkspace) =>
    !queue || isSessionless(workspace);
  const visibleWorkspaces = workspaces.flatMap((candidate) => {
    const workspace = withoutChiefRow(withoutAutomationRows(candidate));
    return isWorkspaceVisible(workspace) && isTreeWorkspace(workspace) ? [workspace] : [];
  });
  const canAcceptLeafDrag = (workspace: SidebarWorkspace) =>
    Boolean(
      leafDrag &&
        workspace.id !== leafDrag.sourceWorkspaceId &&
        workspace.id !== UNPLACED_GROUP_ID &&
        (workspace.endpointId || '') === (leafDrag.endpointId || ''),
    );

  const workspaceDragClass = (workspace: SidebarWorkspace) => {
    if (!leafDrag) {
      return '';
    }
    if (!canAcceptLeafDrag(workspace)) {
      return ' workspace-group--drag-disabled';
    }
    if (dragHoverWorkspaceId === workspace.id) {
      return ' workspace-group--drag-entering';
    }
    return ' workspace-group--drag-target';
  };
  const visibleVisualOrder = workspaces.filter(isWorkspaceVisible);
  const visualIndexOfWorkspace = (id: string) => visualIndexByWorkspaceId.get(id) ?? -1;

  const [newWorkspaceDropActive, setNewWorkspaceDropActive] = useState(false);
  const {
    reorderDrag,
    sessionDragGhost,
    draggingSessionId,
    reorderSeamIndexByWorkspaceId,
    reorderTrailingSeamIndex,
    lastReorderParticipantId,
    renderReorderSeam,
    handleHeaderPointerDown,
    handleHeaderClickCapture,
    handleSessionPointerDown,
    handleSessionClickCapture,
  } = useSidebarDrag({
    visibleVisualOrder,
    onWorkspaceReorder,
    onSessionDragStart,
    onSessionDragEnd,
  });

  return {
    selectedId,
    selectedWorkspaceId,
    selectedTile,
    tileContents,
    collapsed,
    instance,
    headerActions,
    criticalNotifications,
    onOpenNotifications,
    gridLayout,
    onSelectGridLayout,
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
    onOpenSnooze,
    onWakeTurn,
    onScreenSessionIds,
    onRenameSession,
    onRenameWorkspace,
    onChangeChiefOfStaff,
    showSessionless,
    onToggleShowSessionless,
    queueModeEnabled,
    onToggleQueueMode,
    crewQueueEnabled,
    onToggleCrewQueue,
    harnessLogosEnabled,
    onToggleHarnessLogos,
    workspaceSelectionStyle,
    onWorkspaceSelectionStyleChange,
    leafDrag,
    onWorkspaceDragEnter,
    onWorkspaceDragLeave,
    onWorkspaceDragDrop,
    onNewWorkspaceDrop,
    onSessionDragStart,
    onSelectSession,
    onTriggerNudge,
    onSelectWorkspace,
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
    snoozedExpanded,
    setSnoozedExpanded,
    expandedAutomationGroups,
    displayMode,
    setDisplayMode,
    renameTarget,
    setRenameTarget,
    sessionActionsTarget,
    setSessionActionsTarget,
    crewActionsTarget,
    setCrewActionsTarget,
    openRename,
    openSessionActions,
    openCrewMemberActions,
    automationGroups,
    allSessions,
    delegates,
    rowDelegation,
    toggleAutomationGroup,
    visibleWorkspaces,
    visibleVisualOrder,
    canAcceptLeafDrag,
    workspaceDragClass,
    visualIndexOfWorkspace,
    newWorkspaceDropActive,
    setNewWorkspaceDropActive,
    reorderDrag,
    sessionDragGhost,
    draggingSessionId,
    reorderSeamIndexByWorkspaceId,
    reorderTrailingSeamIndex,
    lastReorderParticipantId,
    renderReorderSeam,
    handleHeaderPointerDown,
    handleHeaderClickCapture,
    handleSessionPointerDown,
    handleSessionClickCapture,
  };
}
