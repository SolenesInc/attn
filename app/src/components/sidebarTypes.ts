import type { MouseEvent as ReactMouseEvent, ReactNode } from 'react';
import type { CriticalNotificationState } from '../hooks/useDaemonSocket';
import type {
  AutomationProvenance as AutomationProvenanceValue,
  SessionDelegationRole,
  SessionPullRequest,
} from '../types/generated';
import { type UISessionState } from '../types/sessionState';
import { type TileContentState } from '../types/desktop';
import { type QueueBands as QueueBandsModel } from '../utils/queueBands';
import type { DesktopWithSessions } from '../utils/desktopViewModels';
import { type CrewMemberView } from './QueueRows';

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
  autoSettleFiresAt?: string;
  autoSettleHeld?: boolean;
  state_reason?: string;
  priority?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  crewMember?: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
  delegation_role?: SessionDelegationRole;
  automation?: AutomationProvenanceValue;
  pullRequests?: SessionPullRequest[];
}

export type SidebarDesktop = DesktopWithSessions<LocalSession>;

export interface SelectedTile {
  desktopId: string;
  tileId: string;
}

export type SidebarSurface = 'queue-open' | 'queue-collapsed' | 'tree-open' | 'tree-collapsed' | 'hidden';

export interface SidebarProps {
  desktops: SidebarDesktop[];
  visualIndexByDesktopId: Map<string, number>;
  selectedId: string | null;
  selectionRequest?: { id: number } | null;
  selectedDesktopId: string | null;
  selectedTile?: SelectedTile | null;
  tileContents?: Record<string, TileContentState>;
  collapsed: boolean;
  surface: SidebarSurface;
  instance?: string;
  headerActions: SidebarHeaderAction[];
  criticalNotifications?: CriticalNotificationState;
  onOpenNotifications?: () => void;
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
  onWalkRuns?: () => void;
  onJumpToWaiting?: () => void;
  profileName?: string;
  onSwitchProfile?: () => void;
  onOpenCommands?: () => void;
  onToggleFlow?: () => void;
  onOpenAgents?: () => void;
  peeksSilenced?: boolean;
  agentListOpen?: boolean;
  onToggleAgentList?: () => void;
  onOpenOverview?: () => void;
  onOpenSnooze?: (session: { id: string; label: string }, event: ReactMouseEvent) => void;
  onWakeTurn?: (id: string) => void;
  /** The auto-settle countdown lives on the tile, so the sidebar draws it only
      for sessions NOT in here, or it would run twice. */
  onScreenSessionIds?: ReadonlySet<string>;
  onRenameSession?: (sessionId: string, label: string) => Promise<void>;
  onRenameDesktop?: (desktopId: string, title: string) => Promise<void>;
  onTogglePriority?: (session: LocalSession) => void;
  onChangeChiefOfStaff?: (sessionId: string, enabled: boolean) => void;
  crewQueueEnabled?: boolean;
  harnessLogosEnabled?: boolean;
  leafDrag?: { sourceDesktopId: string } | null;
  dragHoverDesktopId?: string | null;
  onDesktopDragEnter?: (desktop: SidebarDesktop) => void;
  onDesktopDragLeave?: (desktop: SidebarDesktop) => void;
  onDesktopDragDrop?: (desktop: SidebarDesktop) => void;
  onNewDesktopDrop?: () => void;
  onSessionDragStart?: (desktopId: string, paneId: string) => void;
  onSessionDragEnd?: () => void;
  // prevDesktopId ends up directly above the moved desktop, nextDesktopId
  // directly below; either may be undefined at the very top or bottom.
  onDesktopReorder?: (args: {
    desktopId: string;
    prevDesktopId?: string;
    nextDesktopId?: string;
  }) => void;
  onSelectSession: (id: string) => void;
  onSelectDesktop: (id: string) => void;
  onSelectTile?: (desktopId: string, tileId: string) => void;
  onCloseTile?: (desktopId: string, tileId: string) => void;
  onReloadTile?: (desktopId: string, tileId: string) => void;
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
  unread?: boolean;
  onClick: () => void;
}

export interface DockItem {
  id: string;
  label: string;
  keys: string;
  active?: boolean;
  onClick?: () => void;
}
