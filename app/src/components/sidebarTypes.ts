import type { MouseEvent as ReactMouseEvent, ReactNode } from 'react';
import type { CriticalNotificationState } from '../hooks/useDaemonSocket';
import type {
  AutomationProvenance as AutomationProvenanceValue,
  SessionDelegationRole,
  SessionPullRequest,
} from '../types/generated';
import { type UISessionState } from '../types/sessionState';
import { type TileContentState } from '../types/workspace';
import { type QueueBands as QueueBandsModel } from '../utils/queueBands';
import type { WorkspaceSelectionStyle } from '../utils/workspaceSelectionStyle';
import type { WorkspaceWithSessions } from '../utils/workspaceViewModels';
import { type CrewMemberView } from './QueueBands';
import type { GridLayout } from './grid/gridLayout';

export interface LocalSession {
  id: string;
  label: string;
  state: UISessionState;
  agent?: string;
  branch?: string;
  isWorktree?: boolean;
  cwd?: string;
  endpointId?: string;
  endpointName?: string;
  endpointStatus?: string;
  chiefOfStaff?: boolean;
  delegatedFromChief?: boolean;
  ticketUnread?: boolean;
  nudgeFiresAt?: string;
  autoSettleFiresAt?: string;
  autoSettleHeld?: boolean;
  state_reason?: string;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  crewMember?: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
  delegation_role?: SessionDelegationRole;
  automation?: AutomationProvenanceValue;
  pullRequests?: SessionPullRequest[];
}

export type SidebarWorkspace = WorkspaceWithSessions<LocalSession>;

export interface SelectedTile {
  workspaceId: string;
  tileId: string;
}

export interface SidebarProps {
  workspaces: SidebarWorkspace[];
  visualIndexByWorkspaceId: Map<string, number>;
  selectedId: string | null;
  selectedWorkspaceId: string | null;
  selectedTile?: SelectedTile | null;
  tileContents?: Record<string, TileContentState>;
  collapsed: boolean;
  instance?: string;
  headerActions: SidebarHeaderAction[];
  criticalNotifications?: CriticalNotificationState;
  onOpenNotifications?: () => void;
  gridLayout?: GridLayout;
  onSelectGridLayout?: (layout: GridLayout) => void;
  dockItems?: DockItem[];
  dockCollapsed?: boolean;
  onToggleDockCollapsed?: () => void;
  queue?: QueueBandsModel<LocalSession> | null;
  crew?: CrewMemberView[];
  onWakeCrewMember?: (member: string) => void;
  onSleepCrewMember?: (member: string) => void;
  onManageCrew?: (event: ReactMouseEvent<HTMLButtonElement>) => void;
  onOpenCrewMemberDetails?: (member: string, returnFocus: HTMLElement) => void;
  onSettleTurn?: (id: string) => void;
  onOpenSnooze?: (session: { id: string; label: string }, event: ReactMouseEvent) => void;
  onWakeTurn?: (id: string) => void;
  /** The auto-settle countdown lives on the tile, so the sidebar draws it only
      for sessions NOT in here, or it would run twice. */
  onScreenSessionIds?: ReadonlySet<string>;
  onRenameSession?: (sessionId: string, label: string) => Promise<void>;
  onRenameWorkspace?: (workspaceId: string, title: string) => Promise<void>;
  onChangeChiefOfStaff?: (sessionId: string, enabled: boolean) => void;
  showSessionless?: boolean;
  onToggleShowSessionless?: () => void;
  queueModeEnabled?: boolean;
  onToggleQueueMode?: () => void;
  crewQueueEnabled?: boolean;
  onToggleCrewQueue?: () => void;
  harnessLogosEnabled?: boolean;
  onToggleHarnessLogos?: () => void;
  workspaceSelectionStyle?: WorkspaceSelectionStyle;
  onWorkspaceSelectionStyleChange?: (style: WorkspaceSelectionStyle) => void;
  leafDrag?: { sourceWorkspaceId: string; endpointId?: string } | null;
  dragHoverWorkspaceId?: string | null;
  onWorkspaceDragEnter?: (workspace: SidebarWorkspace) => void;
  onWorkspaceDragLeave?: (workspace: SidebarWorkspace) => void;
  onWorkspaceDragDrop?: (workspace: SidebarWorkspace) => void;
  onNewWorkspaceDrop?: () => void;
  onSessionDragStart?: (
    workspaceId: string,
    endpointId: string | undefined,
    paneId: string,
  ) => void;
  onSessionDragEnd?: () => void;
  // prevWorkspaceId ends up directly above the moved workspace, nextWorkspaceId
  // directly below; either may be undefined at the very top or bottom.
  onWorkspaceReorder?: (args: {
    workspaceId: string;
    prevWorkspaceId?: string;
    nextWorkspaceId?: string;
  }) => void;
  onSelectSession: (id: string) => void;
  onTriggerNudge?: (id: string) => void;
  onSelectWorkspace: (id: string) => void;
  onSelectTile?: (workspaceId: string, tileId: string) => void;
  onCloseTile?: (workspaceId: string, tileId: string) => void;
  onReloadTile?: (workspaceId: string, tileId: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  onReloadSession: (id: string) => void;
  onGoToDashboard: () => void;
  homeActive?: boolean;
  onToggleCollapse: () => void;
}

export interface SidebarHeaderAction {
  id: string;
  title: string;
  icon: ReactNode;
  disabled?: boolean;
  active?: boolean;
  toneClassName?: string;
  badge?: string | number;
  onClick: () => void;
}

export interface DockItem {
  id: string;
  label: string;
  keys: string;
  active?: boolean;
  onClick?: () => void;
}
