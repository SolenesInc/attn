import type { Seed } from '../../hooks/useDaemonSocket';
import type {
  AutomationProvenance as AutomationProvenanceValue,
  Presentation,
  SessionPullRequest,
  SessionUsage,
} from '../../types/generated';
import type { SessionAgent } from '../../types/sessionAgent';
import type { UISessionState } from '../../types/sessionState';
import {
  type TerminalDockEdge,
  type TerminalNavigationDirection,
  type TerminalSplitDirection,
  type TerminalWorkspaceState,
  type TileContentState,
} from '../../types/workspace';
import type { ResolvedTheme } from '../../utils/terminalSizing';
import type { WorkspaceSelectionStyle } from '../../utils/workspaceSelectionStyle';
import { type ChainSession } from '../DelegationChain';
import { type SessionAnnotationApi } from '../TerminalAnnotations/AnnotatedTerminal';
import { type WorkspaceTileSessionOption } from './WorkspaceDockTile';
import type { DockTarget } from './dockTarget';
import { type LeafDropSnapshot } from './leafDrag';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import type { GhosttyPaneRuntime } from './useGhosttyPaneRuntime';

export interface SessionTerminalWorkspaceHandle
  extends Pick<
    GhosttyPaneRuntime,
    | 'fitPane'
    | 'fitActivePane'
    | 'focusPane'
    | 'isPaneInputFocused'
    | 'scrollPaneToTop'
    | 'getPaneText'
    | 'getPaneSize'
    | 'getPaneVisibleContent'
    | 'getPaneVisibleStyleSummary'
    | 'getPaneBlockState'
    | 'getPanePlacementState'
    | 'resetPaneTerminal'
    | 'injectPaneBytes'
    | 'injectPaneBase64'
    | 'drainPaneTerminal'
  > {
  focusLeaf: (leafId: string) => void;
  focusActivePane: (retries?: number) => void;
  typePaneTextViaUI: (paneId: string, text: string) => boolean;
  getLeafDropSnapshot: () => LeafDropSnapshot | null;
}

export interface SessionTerminalWorkspaceProps {
  workspaceId: string;
  workspaceDirectory?: string;
  workspaceSessions?: Array<{
    id: string;
    label: string;
    agent: SessionAgent;
    cwd: string;
    endpointId?: string;
    state?: UISessionState;
    usage?: SessionUsage;
    ticketUnread?: boolean;
    nudgeFiresAt?: string;
    autoSettleFiresAt?: string;
    autoSettleHeld?: boolean;
    autoSettleDismissArmed?: boolean;
    terminalBuildStale?: boolean;
    isActive?: boolean;
    presentation?: Presentation;
    seedId?: string;
    crewMember?: string;
    automation?: AutomationProvenanceValue;
    pullRequests?: SessionPullRequest[];
  }>;
  delegationSessions?: readonly ChainSession[];
  seedTargetSessions?: WorkspaceTileSessionOption[];
  gardenSeeds?: Seed[];
  onOpenSeed?: (seedId: string) => void;
  onRevealSeedInGarden?: (seedId: string) => void;
  backToCrewTileId?: string;
  onBackToCrew?: (returnFocus: HTMLElement) => void;
  seedPopoverRequest?: { sessionId: string; nonce: number };
  usagePopoverRequest?: { sessionId: string; nonce: number };
  annotationApi?: SessionAnnotationApi;
  workspace: TerminalWorkspaceState;
  workspaceSelectionStyle?: WorkspaceSelectionStyle;
  activePaneId: string;
  selectedSessionId?: string | null;
  fontSize: number;
  resolvedTheme?: ResolvedTheme;
  focusRequestToken?: number;
  enabled: boolean;
  isActiveSession: boolean;
  isSessionViewVisible?: boolean;
  terminalsLive?: boolean;
  eventRouter: PaneRuntimeEventRouter;
  onSplitPane: (targetPaneId: string, direction: TerminalSplitDirection) => void;
  onClosePane: (paneId: string) => void;
  onFocusPane: (paneId: string) => void;
  onRenameSession?: (sessionId: string, label: string) => Promise<void>;
  onSelectSession?: (sessionId: string) => void;
  onTriggerNudge?: (sessionId: string) => void;
  onCancelCountdown?: (sessionId: string) => void;
  onTerminalPointerActivity?: (sessionId: string) => void;
  onOpenPresentation?: (presentationId: string) => void;
  // Empty sessionId lets the daemon use the selected session.
  onOpenMarkdown?: (path: string, sessionId: string) => void;
  onTerminalModelRecovered?: () => void;
  zoomActive?: boolean;
  onSetZoomActive?: (active: boolean) => void;
  onNavigateOutOfSession: (direction: TerminalNavigationDirection) => void;
  onResizeSplit?: (splitId: string, ratio: number) => Promise<unknown> | void;
  // anchorId '' docks against the whole workspace; ratio is the moved leaf's
  // fraction of the new split.
  onMoveLeaf?: (leafId: string, anchorId: string, edge: TerminalDockEdge, ratio: number) => void;
  getActiveLeafDropSnapshot?: () => LeafDropSnapshot | null;
  onLeafDragStart?: (leafId: string) => void;
  onLeafDragGhostMove?: (clientX: number, clientY: number) => void;
  onLeafDragPreview?: (target: DockTarget | null) => void;
  onLeafDragEnd?: () => void;
  leafDragPreview?: {
    draggingLeafId: string | null;
    dockTarget: DockTarget | null;
    ghostPos: { x: number; y: number } | null;
  } | null;
  onUndockTile?: (tileId: string) => void;
  onUpdateTile?: (
    tileId: string,
    tileParams: string,
    tileSessionId?: string,
  ) => Promise<unknown> | void;
  tileContents?: Record<string, TileContentState>;
  allowLocalTileTargets?: boolean;
  onRequestTileContent?: (workspaceId: string, tileId: string) => void;
}

export interface WorkspaceAgentProps {
  agentPane: TerminalWorkspaceState['agents'][number];
  paneSession: NonNullable<SessionTerminalWorkspaceProps['workspaceSessions']>[number] | undefined;
  paneTitle: string;
}
