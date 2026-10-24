import {
  DaemonPlugin,
  DaemonPluginIssue,
  DaemonSettings,
  PluginListResult,
  Task,
} from '../hooks/useDaemonSocket';
import type { ThemePreference } from '../hooks/useTheme';
import { type SaveSetting } from './SettingsAutosave';

export const OPEN_SENT_FILES_ENABLED_SETTING = 'open_sent_files_enabled';

export interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  mutedRepos: string[];
  githubHosts: string[];
  onUnmuteRepo: (repo: string) => void;
  mutedAuthors: string[];
  onUnmuteAuthor: (author: string) => void;
  settings: DaemonSettings;
  plugins: DaemonPlugin[];
  pluginIssues: DaemonPluginIssue[];
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
  onOpenSession?: (sessionId: string) => void;
  onOpenSeed?: (seedId: string) => void;
}

export type SettingsSectionID =
  | 'general'
  | 'desktop'
  | 'hygiene'
  | 'agents'
  | 'models'
  | 'backgroundAgents'
  | 'experimental'
  | 'autoMode'
  | 'delegation'
  | 'connectivity'
  | 'outposts'
  | 'plugins'
  | 'backgroundTasks'
  | 'eventBus'
  | 'analytics';

export const DEFAULT_CONTEXT_WINDOW_CAP = 128000;

export interface SettingsNavItem {
  id: SettingsSectionID;
  label: string;
  description: string;
  keywords: string;
}

export interface SettingsNavGroup {
  label: string;
  items: SettingsNavItem[];
}

export interface SettingsModalHandle {
  close(): Promise<void>;
}
