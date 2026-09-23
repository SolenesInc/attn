import { clearBrowserHostFocus, isBrowserHostOwnedTarget } from '../browser/host';
import { type SessionCreationPhase } from '../components/SessionCreationProgress';
import type { DockTarget } from '../components/SessionTerminalWorkspace/dockTarget';
import {
  CriticalNotificationState,
  DaemonEndpoint,
  DaemonPlugin,
  DaemonPluginIssue,
  DaemonPR,
  DaemonSession,
  DaemonWorkspace,
  SessionExitInfo,
} from '../hooks/useDaemonSocket';
import { type OpenPRProgress } from '../hooks/useOpenPR';
import { type Session, type TerminalWorkspaceState } from '../store/sessions';
import type { Presentation } from '../types/generated';
import { type SessionAgent } from '../types/sessionAgent';
import { hasPane } from '../types/workspace';
import { crewDisplayName } from '../utils/crewName';
export const RELEASES_LATEST_API = 'https://api.github.com/repos/victorarias/attn/releases/latest';

export const RELEASES_LATEST_WEB = 'https://github.com/victorarias/attn/releases/latest';

export const RELEASE_CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;

export const UPDATE_BANNER_DISMISSED_STORAGE_KEY = 'attn.update_banner.dismissed_version';

export const CHIEF_OF_STAFF_CLOSE_HINT =
  'Chief of staff is protected — unset the chief role to close it.';

export function crewMemberCloseHint(memberId: string): string {
  const name = crewDisplayName(memberId);
  return `${name} is protected — put ${name} to sleep to close the day.`;
}

export function sessionCloseProtectionHint(sessions: DaemonSession[], id: string): string | null {
  const session = sessions.find((candidate) => candidate.id === id);
  if (session?.chief_of_staff === true) {
    return CHIEF_OF_STAFF_CLOSE_HINT;
  }
  return session?.crew_member ? crewMemberCloseHint(session.crew_member) : null;
}

export const TERMINAL_AGENT: SessionAgent = 'shell';

export type LocationPickerPurpose = 'workspace' | 'session' | 'reopen';

export function handleAppPointerDownCapture(event: { target: EventTarget | null }): void {
  if (!isBrowserHostOwnedTarget(event.target)) {
    clearBrowserHostFocus();
  }
}

export interface SplitSessionOptions {
  baseSessionId?: string;
  cwd?: string;
  endpointId?: string | null;
  label?: string;
  yoloMode?: boolean;
  autoMode?: boolean;
}

export function paneIdForSession(sessionId: string): string {
  return `pane-${sessionId}`;
}

export interface GitHubReleaseResponse {
  tag_name?: string;
  html_url?: string;
  prerelease?: boolean;
  draft?: boolean;
}

export interface LeafWorkspaceDragState {
  sourceWorkspaceId: string;
  sourceEndpointId?: string;
  leafId: string;
}

export interface LeafDragPreviewState {
  draggingLeafId: string | null;
  dockTarget: DockTarget | null;
  ghostPos: { x: number; y: number } | null;
}

export const SIDEBAR_LEAF_DROP_PLACEMENT = { anchorId: '', edge: 'left' as const, ratio: 0.32 };

export function terminalStateForWorkspaceSessions(
  sessions: Session[],
): TerminalWorkspaceState | null {
  let selected: TerminalWorkspaceState | null = null;
  for (const session of sessions) {
    const candidate = session.workspace;
    if (!candidate.layoutTree && candidate.agents.length === 0) {
      continue;
    }
    if (!selected || candidate.agents.length > selected.agents.length) {
      selected = candidate;
    }
  }
  return selected;
}

export function activePaneIdForWorkspace(
  workspace: TerminalWorkspaceState,
  focusedSessionId: string | null,
): string {
  if (focusedSessionId) {
    const focusedPane = workspace.agents.find((pane) => pane.sessionId === focusedSessionId);
    if (focusedPane) {
      return focusedPane.id;
    }
  }
  return workspace.agents[0]?.id || '';
}

export function activePaneIdForFocusedSession(
  workspace: TerminalWorkspaceState,
  session: Session | null,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
): string {
  const sessionActivePaneId = getActivePaneIdForSession(session);
  if (
    sessionActivePaneId &&
    workspace.layoutTree &&
    hasPane(workspace.layoutTree, sessionActivePaneId)
  ) {
    return sessionActivePaneId;
  }
  return activePaneIdForWorkspace(workspace, session?.id ?? null);
}

export function diagnosticFocusKind(element: Element | null): string {
  if (!element) return 'none';
  if (element.closest('.terminal-container, .grid-view-stage')) return 'terminal';
  if (element.matches('input, textarea, [contenteditable="true"]')) return 'editor';
  if (element.matches('button, a, select')) return 'control';
  return element === document.body ? 'body' : 'other';
}

export function shortenDiagnosticPath(path: string): string {
  return path
    .replace(/^\/Users\/[^/]+(?=\/|$)/, '~')
    .replace(/^\/home\/[^/]+(?=\/|$)/, '~')
    .replace(/^[A-Za-z]:\\Users\\[^\\]+(?=\\|$)/, '~');
}

export function parseSemver(version: string): [number, number, number] | null {
  const match = version.trim().match(/^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/);
  if (!match) return null;
  return [Number(match[1]), Number(match[2]), Number(match[3])];
}

export function isNewerVersion(currentVersion: string, latestVersion: string): boolean {
  const current = parseSemver(currentVersion);
  const latest = parseSemver(latestVersion);
  if (!current || !latest) return false;

  if (latest[0] !== current[0]) return latest[0] > current[0];
  if (latest[1] !== current[1]) return latest[1] > current[1];
  return latest[2] > current[2];
}

export function getDismissedUpdateVersion(): string | null {
  try {
    return window.localStorage.getItem(UPDATE_BANNER_DISMISSED_STORAGE_KEY);
  } catch (err) {
    console.warn('[App] Failed to read dismissed update version:', err);
    return null;
  }
}

export function persistDismissedUpdateVersion(version: string): void {
  try {
    window.localStorage.setItem(UPDATE_BANNER_DISMISSED_STORAGE_KEY, version);
  } catch (err) {
    console.warn('[App] Failed to persist dismissed update version:', err);
  }
}

export const SHOW_SESSIONLESS_WORKSPACES_STORAGE_KEY = 'attn.sidebar.showSessionless';

export function readShowSessionlessWorkspaces(): boolean {
  try {
    return window.localStorage.getItem(SHOW_SESSIONLESS_WORKSPACES_STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

export function persistShowSessionlessWorkspaces(value: boolean): void {
  try {
    window.localStorage.setItem(SHOW_SESSIONLESS_WORKSPACES_STORAGE_KEY, value ? '1' : '0');
  } catch (err) {
    console.warn('[App] Failed to persist show-sessionless preference:', err);
  }
}

export function toneForDockPanel(
  status?: string,
): 'default' | 'idle' | 'running' | 'awaiting_user' | 'completed' | 'stopped' | 'error' {
  switch (status) {
    case 'running':
    case 'awaiting_user':
    case 'completed':
    case 'stopped':
    case 'error':
      return status;
    default:
      return 'default';
  }
}

export type OpenPRLauncherJob = {
  id: number;
  pr: DaemonPR;
  progress: OpenPRProgress;
};

export type SessionCreationJob = {
  id: number;
  label: string;
  path: string;
  phase: SessionCreationPhase;
  sessionId?: string;
  error?: string | null;
};

export interface AppContentProps {
  daemonSessions: DaemonSession[];
  daemonWorkspaces: DaemonWorkspace[];
  prs: DaemonPR[];
  daemonEndpoints: DaemonEndpoint[];
  daemonPlugins: DaemonPlugin[];
  daemonPluginIssues: DaemonPluginIssue[];
  daemonGitHubHosts: string[];
  githubPollingOffReason: string | null;
  settings: Record<string, string>;
  updateAvailableVersion: string | null;
  onOpenLatestRelease: () => Promise<void>;
  onDismissLatestRelease: () => void;
  presentationNotices: Presentation[];
  settingError: string | null;
  clearSettingError: () => void;
  notificationsUnread: number;
  criticalNotifications: CriticalNotificationState;
  notificationsChangeSignal: number;
  fsChangeSignals: Record<string, number>;
  notebookTaskChangeSignal: number;
  registerSessionExitHandler: (handler: ((info: SessionExitInfo) => void) | null) => void;
}
