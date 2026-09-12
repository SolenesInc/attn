import {
  DaemonEndpoint,
  DaemonPlugin,
  DaemonPluginIssue,
  DaemonSettings,
  PluginListResult,
  Task,
} from '../hooks/useDaemonSocket';
import type { ThemePreference } from '../hooks/useTheme';
import { type SessionAgent } from '../types/sessionAgent';
import { type SaveSetting } from './SettingsAutosave';

export const OPEN_SENT_FILES_ENABLED_SETTING = 'open_sent_files_enabled';

export const PTY_BACKENDS: Record<string, { label: string; hint: string }> = {
  migrating: {
    label: 'Dedicated + shared workers',
    hint: 'Existing terminals keep their worker, including across daemon restarts.',
  },
  shared: {
    label: 'Shared Rust host',
    hint: 'New terminals share a Rust host. Selected by a backend override.',
  },
  worker: {
    label: 'Dedicated Go workers',
    hint: 'Sessions run in per-session worker processes and can survive daemon restarts.',
  },
  embedded: {
    label: 'Embedded in daemon',
    hint: 'Sessions run inside the daemon process and stop if the daemon restarts.',
  },
  unknown: {
    label: 'Unknown',
    hint: 'Backend mode is not currently reported by the daemon.',
  },
};

export interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  mutedRepos: string[];
  githubHosts: string[];
  onUnmuteRepo: (repo: string) => void;
  mutedAuthors: string[];
  onUnmuteAuthor: (author: string) => void;
  settings: DaemonSettings;
  endpoints: DaemonEndpoint[];
  plugins: DaemonPlugin[];
  pluginIssues: DaemonPluginIssue[];
  onAddEndpoint: (name: string, sshTarget: string, profile?: string) => Promise<{ success: boolean }>;
  onUpdateEndpoint: (
    endpointId: string,
    updates: { name?: string; ssh_target?: string; enabled?: boolean; profile?: string },
  ) => Promise<{ success: boolean }>;
  onRemoveEndpoint: (endpointId: string) => Promise<{ success: boolean }>;
  onSetEndpointRemoteWeb: (endpointId: string, enabled: boolean) => Promise<{ success: boolean }>;
  onListPlugins: () => Promise<PluginListResult>;
  onInstallPlugin: (source: string) => Promise<{ success: boolean; name?: string }>;
  onInstallBundledPlugin?: (name: string) => Promise<{ success: boolean; name?: string }>;
  onUninstallPlugin?: (name: string) => Promise<{ success: boolean; name?: string }>;
  onRemovePlugin: (name: string) => Promise<{ success: boolean; name?: string }>;
  onSetPluginPriority: (name: string, priority: number) => Promise<{ success: boolean; name?: string }>;
  onSetSetting: SaveSetting;
  themePreference: ThemePreference;
  onSetTheme: (theme: ThemePreference) => void;
  uiScale?: number;
  onIncreaseUIScale?: () => void;
  onDecreaseUIScale?: () => void;
  onResetUIScale?: () => void;
  gardenScale?: number | null;
  effectiveGardenScale?: number;
  onIncreaseGardenScale?: () => void;
  onDecreaseGardenScale?: () => void;
  onMatchAppGardenScale?: () => void;
  listTasks?: () => Promise<Task[]>;
  retryTask?: (taskId: string) => Promise<Task | null>;
  taskChangeSignal?: number;
}

export type SettingsSectionID =
  | 'general'
  | 'workspace'
  | 'hygiene'
  | 'agents'
  | 'backgroundAgents'
  | 'terminal'
  | 'autoMode'
  | 'delegation'
  | 'workflows'
  | 'connectivity'
  | 'plugins'
  | 'backgroundTasks'
  | 'eventBus'
  | 'data';

export const DEFAULT_CONTEXT_WINDOW_CAP = 128000;

export const MODEL_CAPTURE_INTERVAL_OPTIONS = [5, 10, 30, 60];

export const MODEL_CAPTURE_MAX_GB_OPTIONS = [1, 2, 5, 10, 25];

export function formatByteCount(raw: string | undefined): string {
  const bytes = Number(raw || '0');
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** unit;
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}

export const CHIEF_EFFORT_LEVELS: Partial<Record<SessionAgent, string[]>> = {
  claude: ['low', 'medium', 'high', 'xhigh', 'max'],
  codex: ['minimal', 'low', 'medium', 'high', 'xhigh'],
};

export interface SettingsNavItem {
  id: SettingsSectionID;
  label: string;
  title: string;
  description: string;
  count: number;
  keywords: string;
}

export interface SettingsNavGroup {
  label: string;
  items: SettingsNavItem[];
}

export interface SettingsModalHandle {
  close(): Promise<void>;
}
