import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { useHarnesses } from '../hooks/useHarnesses';
import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
import { PathInput } from './NewSessionDialog/PathInput';
import { RepoOptions } from './NewSessionDialog/RepoOptions';
import type {
  BrowseDirectoryResult,
  InspectPathResult,
  RecentLocation,
} from '../hooks/useDaemonSocket';
import type { SessionAgent } from '../types/sessionAgent';
import { useSettings } from '../contexts/SettingsContext';
import {
  agentLabel,
  type AgentAvailability,
  getAgentCapabilities,
  hasAnyAvailableAgents,
  isAgentAvailable,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import {
  buildInitialPickerInput,
  expandDisplayPath,
  normalizePickerPath,
  pathBasename,
  toDisplayPath,
} from '../utils/locationPickerPaths';
import './LocationPicker.css';
import { keyCombo } from '../shortcuts/formatShortcut';

interface BackendRepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: Array<{ path: string; branch: string }>;
}

interface RepoInfo {
  repo: string;
  currentBranch: string;
  currentCommitHash: string;
  currentCommitTime: string;
  defaultBranch: string;
  worktrees: Array<{ path: string; branch: string }>;
}

interface PathSelectableItem {
  kind: 'recent' | 'directory';
  key: string;
  path: string;
}

interface LocationPickerProps {
  isOpen: boolean;
  // Inline lives on an empty desktop: no overlay, and Escape never dismisses it.
  variant?: 'dialog' | 'inline';
  // False while another surface holds the keyboard: no focus grab, no Escape handling.
  active?: boolean;
  title?: string;
  onClose: () => void;
  onSelect: (
    path: string,
    agent: SessionAgent,
    yoloMode?: boolean,
    chiefOfStaff?: boolean,
    autoMode?: boolean,
  ) => void;
  onGetRecentLocations?: () => Promise<{ locations: RecentLocation[]; home_path?: string }>;
  onBrowseDirectory?: (inputPath: string) => Promise<BrowseDirectoryResult>;
  onInspectPath?: (path: string) => Promise<InspectPathResult>;
  onGetRepoInfo?: (mainRepo: string) => Promise<{ success: boolean; info?: BackendRepoInfo; error?: string }>;
  onCreateWorktree?: (mainRepo: string, branch: string, path?: string, startingFrom?: string) => Promise<{ success: boolean; path?: string; error?: string }>;
  onCreateWorktreeSession?: (mainRepo: string, branch: string, startingFrom: string, agent: SessionAgent, yoloMode: boolean, autoMode?: boolean, chiefOfStaff?: boolean) => void;
  onDeleteWorktree?: (path: string, options?: { force?: boolean }) => Promise<{ success: boolean; error?: string }>;
  onError?: (message: string) => void;
  projectsDirectory?: string;
  agentAvailability?: AgentAvailability;
  chiefExists?: boolean;
}

const MAX_RECENT_LOCATIONS = 10;
const SESSION_AGENT_KEY = 'new_session_agent';
const SESSION_YOLO_KEYS = ['new_session_yolo_local', 'new_session_yolo'];
const SESSION_DESTINATION_KEY = 'new_session_destination';
// READ-ONLY, daemon-computed. The launcher always sends its answer explicitly,
// so a default that moves later cannot move an already-launched session.
const AUTOMODE_DEFAULT_KEY = 'automode_enabled_default';
const AUTOMODE_CAPABILITY = 'auto_mode';
const TERMINAL_AGENT: SessionAgent = 'shell';

const DEFAULT_AGENT_AVAILABILITY: AgentAvailability = {
  shell: true,
  codex: true,
  claude: true,
  copilot: true,
};

const normalizeAgent = (value?: string): SessionAgent | null => {
  if (!value) return null;
  const lower = value.toLowerCase().trim();
  return lower || null;
};

function pathStartsWith(candidate: string, input: string): boolean {
  const normalizedCandidate = candidate.toLowerCase();
  const normalizedInput = input.toLowerCase();
  return normalizedCandidate.startsWith(normalizedInput);
}

function parseBooleanSetting(value?: string): boolean | null {
  if (typeof value !== 'string') {
    return null;
  }
  const normalized = value.trim().toLowerCase();
  if (normalized === 'true') {
    return true;
  }
  if (normalized === 'false') {
    return false;
  }
  return null;
}

type RepoDestination = 'new_worktree' | 'main_repo';

function repoDestinationSettingKey(repoRoot: string): string {
  return `${SESSION_DESTINATION_KEY}_local_${repoRoot}`;
}

function parseRepoDestination(value?: string): RepoDestination | undefined {
  const normalized = value?.trim();
  return normalized === 'new_worktree' || normalized === 'main_repo' ? normalized : undefined;
}

function pickerAgentLabel(agent: SessionAgent): string {
  return agent === TERMINAL_AGENT ? 'Terminal' : agentLabel(agent);
}

function withoutTerminalAgent(availability: AgentAvailability): AgentAvailability {
  const { [TERMINAL_AGENT]: _terminal, ...rest } = availability;
  return rest;
}

function resolvePickerAgent(
  preferred: SessionAgent | undefined,
  availability: AgentAvailability,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  if (preferred === TERMINAL_AGENT && availability[TERMINAL_AGENT]) {
    return TERMINAL_AGENT;
  }
  const nonTerminalAvailability = withoutTerminalAgent(availability);
  if (hasAnyAvailableAgents(nonTerminalAvailability)) {
    return resolvePreferredAgent(preferred, nonTerminalAvailability, fallback);
  }
  return availability[TERMINAL_AGENT]
    ? TERMINAL_AGENT
    : resolvePreferredAgent(preferred, availability, fallback);
}

function toChooserRepoInfo(info: BackendRepoInfo): RepoInfo {
  return {
    repo: info.repo,
    currentBranch: info.current_branch,
    currentCommitHash: info.current_commit_hash,
    currentCommitTime: info.current_commit_time,
    defaultBranch: info.default_branch,
    worktrees: info.worktrees,
  };
}

type Mode = 'path-input' | 'repo-options';

export function LocationPicker({
  isOpen,
  variant = 'dialog',
  active = true,
  title = 'New Session Location',
  onClose,
  onSelect,
  onGetRecentLocations,
  onBrowseDirectory,
  onInspectPath,
  onGetRepoInfo,
  onCreateWorktree,
  onCreateWorktreeSession,
  onDeleteWorktree,
  onError,
  projectsDirectory,
  agentAvailability,
  chiefExists = false,
}: LocationPickerProps) {
  const { settings, setSetting } = useSettings();
  const noAgentsMessage = 'No supported agent CLI found in PATH.';
  const inline = variant === 'inline';

  const [mode, setMode] = useState<Mode>('path-input');
  const [inputValue, setInputValue] = useState('');
  const [highlightedItemKey, setHighlightedItemKey] = useState<string | null>(null);
  const [selectedPath, setSelectedPath] = useState('');
  const [recentLocations, setRecentLocations] = useState<RecentLocation[]>([]);
  const [homePath, setHomePath] = useState('');
  const [agent, setAgent] = useState<SessionAgent>('claude');
  const [yoloMode, setYoloMode] = useState(false);
  const [chiefOfStaff, setChiefOfStaff] = useState(false);
  const [autoMode, setAutoMode] = useState(true);
  const autoModeTouchedRef = useRef(false);
  const [repoRootPath, setRepoRootPath] = useState<string | null>(null);
  const [repoInfo, setRepoInfo] = useState<RepoInfo | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [pickerOperation, setPickerOperation] = useState<string | null>(null);
  const [hasSelectedSinceTab, setHasSelectedSinceTab] = useState(true);
  const [autoHighlight, setAutoHighlight] = useState(false);
  // Cached recents still render but must never drive the automatic highlight:
  // bare Enter could launch a stale path.
  const [recentsFresh, setRecentsFresh] = useState(false);
  const autoHighlightDoneRef = useRef(false);
  const requestGenerationRef = useRef(0);

  const agentCapabilities = useMemo(() => getAgentCapabilities(settings), [settings]);
  const effectiveAgentAvailability = useMemo<AgentAvailability>(
    () => ({ ...(agentAvailability || DEFAULT_AGENT_AVAILABILITY), [TERMINAL_AGENT]: true }),
    [agentAvailability],
  );
  const hasAvailableAgents = hasAnyAvailableAgents(effectiveAgentAvailability);
  const savedYoloMode = useMemo(() => {
    for (const key of SESSION_YOLO_KEYS) {
      const parsed = parseBooleanSetting(settings[key]);
      if (parsed != null) {
        return parsed;
      }
    }
    return false;
  }, [settings]);

  const handleHomePathChange = useCallback((nextHomePath: string) => {
    setHomePath((prev) => (prev === nextHomePath ? prev : nextHomePath));
  }, []);

  const { suggestions: fsSuggestions, currentDir } = useFilesystemSuggestions(
    inputValue,
    onBrowseDirectory,
    { homePath, onHomePathChange: handleHomePathChange, enabled: isOpen },
  );

  const daemonApi = useOptionalDaemonApi();
  const { harnesses, hasCatalog, loading: harnessesLoading, error: harnessesError, retry: retryHarnesses } = useHarnesses(isOpen);
  const orderedAgentList = useMemo(() => {
    const reported = Object.keys(effectiveAgentAvailability) as SessionAgent[];
    if (!daemonApi) return [...new Set([TERMINAL_AGENT, ...reported])];
    const discovered = harnesses.map(harness => harness.id);
    const interactive = hasCatalog || harnessesError ? reported.filter(candidate => !discovered.includes(candidate)).sort((a, b) => a.localeCompare(b)) : [];
    return [...new Set([TERMINAL_AGENT, ...discovered, ...interactive])];
  }, [effectiveAgentAvailability, harnesses, hasCatalog, harnessesError, daemonApi]);
  const agentShortcutByName = useMemo(() => {
    const shortcuts = new Map<SessionAgent, number>();
    if (daemonApi && !hasCatalog) return shortcuts;
    orderedAgentList.filter((candidate) => candidate !== TERMINAL_AGENT).forEach((candidate, index) => {
      shortcuts.set(candidate, index + 1);
    });
    return shortcuts;
  }, [orderedAgentList, daemonApi, hasCatalog]);

  const savedAgent = normalizeAgent(settings[SESSION_AGENT_KEY]);
  const yoloSupported = Boolean(agentCapabilities[agent]?.yolo);
  const autoModeSupported = Boolean(agentCapabilities[agent]?.[AUTOMODE_CAPABILITY]);
  const autoModeDefault = parseBooleanSetting(settings[AUTOMODE_DEFAULT_KEY]) ?? true;
  // The agent gate matches the daemon's agentSupportsChiefReload.
  const chiefToggleEligible = !chiefExists && (agent === 'claude' || agent === 'codex');

  const invalidateRequestGeneration = useCallback(() => {
    requestGenerationRef.current += 1;
  }, []);

  const beginRequestGeneration = useCallback(() => {
    const nextGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = nextGeneration;
    return nextGeneration;
  }, []);

  const isRequestCurrent = useCallback(
    (requestGeneration: number) => requestGenerationRef.current === requestGeneration,
    [],
  );

  useEffect(() => {
    if (!savedAgent) return;
    const resolvedSavedAgent = resolvePickerAgent(savedAgent, effectiveAgentAvailability, 'codex');
    setAgent((prev) => (prev === resolvedSavedAgent ? prev : resolvedSavedAgent));
  }, [effectiveAgentAvailability, savedAgent]);

  useEffect(() => {
    const resolvedAgent = resolvePickerAgent(agent, effectiveAgentAvailability, 'codex');
    if (resolvedAgent !== agent) {
      setAgent(resolvedAgent);
    }
  }, [agent, effectiveAgentAvailability]);

  useEffect(() => {
    setYoloMode((prev) => (prev === savedYoloMode ? prev : savedYoloMode));
  }, [savedYoloMode]);

  useEffect(() => {
    if (!yoloSupported) {
      setYoloMode(false);
    }
  }, [yoloSupported]);

  useEffect(() => {
    if (!chiefToggleEligible) {
      setChiefOfStaff(false);
    }
  }, [chiefToggleEligible]);

  useEffect(() => {
    if (autoModeTouchedRef.current) return;
    setAutoMode((prev) => (prev === autoModeDefault ? prev : autoModeDefault));
  }, [autoModeDefault]);

  useEffect(() => {
    if (!isOpen) {
      invalidateRequestGeneration();
      return;
    }
    setMode('path-input');
    setInputValue(buildInitialPickerInput(projectsDirectory, homePath));
    setHighlightedItemKey(null);
    setAutoHighlight(false);
    autoHighlightDoneRef.current = false;
    setSelectedPath('');
    setRepoRootPath(null);
    setRepoInfo(null);
    setRefreshing(false);
    setPickerOperation(null);
  }, [isOpen]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    setRecentsFresh(false);
    if (!isOpen) {
      return;
    }

    let cancelled = false;
    if (!onGetRecentLocations) {
      setRecentLocations([]);
      return;
    }

    onGetRecentLocations()
      .then((result) => {
        if (cancelled) {
          return;
        }
        setRecentLocations(result.locations);
        setRecentsFresh(true);
        const nextHomePath = result.home_path;
        if (nextHomePath) {
          setHomePath((prev) => (prev === nextHomePath ? prev : nextHomePath));
        }
      })
      .catch((err) => {
        if (cancelled) {
          return;
        }
        console.error('[LocationPicker] Failed to fetch recent locations:', err);
        setRecentLocations([]);
      });

    return () => {
      cancelled = true;
    };
  }, [isOpen, onGetRecentLocations]);

  const expandedInput = expandDisplayPath(inputValue, homePath);
  const filteredRecent = useMemo(
    () => (expandedInput
      ? recentLocations.filter(
          (loc) => loc.path.toLowerCase().includes(expandedInput.toLowerCase()),
        )
      : recentLocations),
    [expandedInput, recentLocations],
  );
  const visibleRecent = useMemo(
    () => filteredRecent.slice(0, MAX_RECENT_LOCATIONS).map((loc) => ({
      ...loc,
      name: pathBasename(loc.path),
      selectionPath: homePath ? toDisplayPath(loc.path, homePath) : loc.path,
    })),
    [filteredRecent, homePath],
  );
  const selectableItems = useMemo<PathSelectableItem[]>(
    () => [
      ...visibleRecent.map((loc) => ({
        kind: 'recent' as const,
        key: `recent:${loc.path}`,
        path: loc.selectionPath,
      })),
      ...fsSuggestions.map((item) => ({
        kind: 'directory' as const,
        key: `directory:${item.path}`,
        path: item.path,
      })),
    ],
    [fsSuggestions, visibleRecent],
  );
  const highlightedIndex = useMemo(
    () => (highlightedItemKey ? selectableItems.findIndex((item) => item.key === highlightedItemKey) : -1),
    [highlightedItemKey, selectableItems],
  );
  const highlightedItem = highlightedIndex >= 0 ? selectableItems[highlightedIndex] : null;
  const completionCandidate = useMemo(() => {
    const trimmedInput = inputValue.trim();
    if (!trimmedInput) {
      return '';
    }
    return selectableItems.find((item) => pathStartsWith(item.path, trimmedInput))?.path || '';
  }, [inputValue, selectableItems]);
  const ghostText = completionCandidate !== inputValue && pathStartsWith(completionCandidate, inputValue)
    ? completionCandidate
    : '';
  const tabCompletionValue = highlightedItem?.path || ghostText;

  useEffect(() => {
    if (highlightedItemKey && highlightedIndex < 0) {
      setHighlightedItemKey(null);
    }
  }, [highlightedIndex, highlightedItemKey]);

  useEffect(() => {
    if (!isOpen || mode !== 'path-input' || !recentsFresh || autoHighlightDoneRef.current || visibleRecent.length === 0) {
      return;
    }
    autoHighlightDoneRef.current = true;
    setAutoHighlight(true);
    setHighlightedItemKey(`recent:${visibleRecent[0].path}`);
  }, [isOpen, mode, recentsFresh, visibleRecent]);

  useEffect(() => {
    if (highlightedIndex >= 0) {
      document.querySelector(`[data-index="${highlightedIndex}"]`)?.scrollIntoView({ block: 'nearest' });
    }
  }, [highlightedIndex]);

  const launchSelection = useCallback((path: string) => {
    const selectedAgent = resolvePickerAgent(agent, effectiveAgentAvailability, 'codex');
    onSelect(
      path,
      selectedAgent,
      yoloMode && yoloSupported,
      chiefOfStaff && chiefToggleEligible,
      autoModeSupported ? autoMode : undefined,
    );
    onClose();
  }, [
    agent,
    autoMode,
    autoModeSupported,
    chiefOfStaff,
    chiefToggleEligible,
    effectiveAgentAvailability,
    onClose,
    onSelect,
    yoloMode,
    yoloSupported,
  ]);

  const updateInputValue = useCallback((nextPath: string) => {
    if (nextPath === inputValue) {
      return;
    }
    invalidateRequestGeneration();
    autoHighlightDoneRef.current = true;
    setAutoHighlight(false);
    setInputValue(nextPath);
    setHighlightedItemKey(null);
    setSelectedPath('');
    setHasSelectedSinceTab(true);
    setPickerOperation(null);
  }, [inputValue, invalidateRequestGeneration]);

  const handleTabComplete = useCallback((nextPath: string) => {
    if (nextPath === inputValue) {
      return;
    }
    invalidateRequestGeneration();
    autoHighlightDoneRef.current = true;
    setAutoHighlight(false);
    setInputValue(nextPath);
    setHighlightedItemKey(null);
    setSelectedPath('');
    setHasSelectedSinceTab(false);
    setPickerOperation(null);
  }, [inputValue, invalidateRequestGeneration]);

  const handlePathSelect = useCallback((path: string) => {
    setSelectedPath(path);
  }, []);

  const setSelectedPathFromPhysical = useCallback((path: string) => {
    setSelectedPath((prev) => (prev === path ? prev : path));
  }, []);

  const handleSelectPath = useCallback(async (rawPath: string) => {
    if (!onInspectPath) {
      onError?.('Path inspection is not available.');
      return;
    }

    const sanitizedPath = normalizePickerPath(rawPath, homePath);
    if (!sanitizedPath) {
      return;
    }
    const requestGeneration = beginRequestGeneration();
    setPickerOperation('Inspecting path');

    try {
      const inspected = await onInspectPath(sanitizedPath);
      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      const inspection = inspected.inspection;
      if (!inspection?.exists || !inspection.is_directory) {
        setPickerOperation(null);
        onError?.(`Directory not found: ${sanitizedPath}`);
        return;
      }

      const inspectedHomePath = inspection.home_path;
      if (inspectedHomePath) {
        setHomePath((prev) => (prev === inspectedHomePath ? prev : inspectedHomePath));
      }
      const resolvedPath = inspection.resolved_path;
      setSelectedPathFromPhysical(resolvedPath);
      const repoRoot = inspection.repo_root;
      if (repoRoot && onGetRepoInfo) {
        setPickerOperation('Loading repo options');
        const result = await onGetRepoInfo(repoRoot);
        if (!isRequestCurrent(requestGeneration)) {
          return;
        }
        if (result.success && result.info) {
          setMode('repo-options');
          setRepoRootPath(repoRoot);
          setRepoInfo(toChooserRepoInfo(result.info));
          setPickerOperation(null);
          return;
        }
        onError?.(result.error || 'Failed to load repo options');
        setPickerOperation(null);
        launchSelection(resolvedPath);
        return;
      }

      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      setPickerOperation(null);
      launchSelection(resolvedPath);
    } catch (err) {
      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      console.log('[LocationPicker] inspect path error:', err);
      setPickerOperation(null);
      onError?.(err instanceof Error ? err.message : String(err));
    }
  }, [
    beginRequestGeneration,
    homePath,
    isRequestCurrent,
    launchSelection,
    onError,
    onGetRepoInfo,
    onInspectPath,
    setSelectedPathFromPhysical,
  ]);

  const handleAgentChange = useCallback((nextAgent: SessionAgent) => {
    if (!isAgentAvailable(effectiveAgentAvailability, nextAgent)) {
      return;
    }
    setAgent(nextAgent);
    if (nextAgent === TERMINAL_AGENT) {
      return;
    }
    setSetting(SESSION_AGENT_KEY, nextAgent);
  }, [effectiveAgentAvailability, setSetting]);

  const toggleYoloMode = useCallback(() => {
    if (!yoloSupported) {
      return;
    }
    const nextYoloMode = !yoloMode;
    setSetting(SESSION_YOLO_KEYS[0], String(nextYoloMode));
    setYoloMode(nextYoloMode);
  }, [setSetting, yoloMode, yoloSupported]);

  const handlePathInputSubmit = useCallback(() => {
    const pathToOpen = highlightedItem?.path || inputValue;
    if (!pathToOpen.trim()) {
      return;
    }
    void handleSelectPath(pathToOpen);
  }, [handleSelectPath, highlightedItem, inputValue]);

  const repoDestinationKey = useMemo(
    () => (repoRootPath ? repoDestinationSettingKey(repoRootPath) : null),
    [repoRootPath],
  );
  const rememberedDestination = repoDestinationKey
    ? parseRepoDestination(settings[repoDestinationKey])
    : undefined;
  // Only when it actually moves: re-storing the same value would broadcast a
  // settings snapshot to every client for nothing.
  const rememberRepoDestination = useCallback((destination: RepoDestination) => {
    if (!repoDestinationKey || settings[repoDestinationKey] === destination) {
      return;
    }
    setSetting(repoDestinationKey, destination);
  }, [repoDestinationKey, setSetting, settings]);

  const handleSelectMainRepo = useCallback(() => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    if (repoRootPath) {
      rememberRepoDestination('main_repo');
      launchSelection(repoRootPath);
    }
  }, [hasAvailableAgents, launchSelection, onError, rememberRepoDestination, repoRootPath]);

  const handleSelectWorktree = useCallback((path: string) => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    launchSelection(path);
  }, [hasAvailableAgents, launchSelection, onError]);

  const handleCreateWorktree = useCallback(async (branchName: string, startingFrom: string) => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    if (!repoRootPath || !onCreateWorktree) {
      return;
    }
    rememberRepoDestination('new_worktree');

    if (onCreateWorktreeSession) {
      const selectedAgent = resolvePickerAgent(agent, effectiveAgentAvailability, 'codex');
      onCreateWorktreeSession(
        repoRootPath,
        branchName,
        startingFrom,
        selectedAgent,
        yoloMode && yoloSupported,
        autoModeSupported ? autoMode : undefined,
        chiefOfStaff && chiefToggleEligible,
      );
      onClose();
      return;
    }

    const requestGeneration = beginRequestGeneration();
    try {
      const result = await onCreateWorktree(repoRootPath, branchName, undefined, startingFrom);
      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      if (result.success && result.path) {
        setSelectedPathFromPhysical(result.path);
        launchSelection(result.path);
      } else {
        onError?.(result.error || 'Failed to create worktree');
      }
    } catch (err) {
      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      console.error('[LocationPicker] Failed to create worktree:', err);
      onError?.(err instanceof Error ? err.message : 'Failed to create worktree');
    }
  }, [
    beginRequestGeneration,
    hasAvailableAgents,
    isRequestCurrent,
    launchSelection,
    onClose,
    onCreateWorktree,
    onCreateWorktreeSession,
    onError,
    rememberRepoDestination,
    repoRootPath,
    agent,
    autoMode,
    autoModeSupported,
    chiefOfStaff,
    chiefToggleEligible,
    effectiveAgentAvailability,
    setSelectedPathFromPhysical,
    yoloMode,
    yoloSupported,
  ]);

  const handleRefresh = useCallback(async () => {
    if (!repoRootPath || !onGetRepoInfo || refreshing) {
      return;
    }

    const requestGeneration = requestGenerationRef.current;
    setRefreshing(true);
    try {
      const result = await onGetRepoInfo(repoRootPath);
      if (isRequestCurrent(requestGeneration) && result.success && result.info) {
        setRepoInfo(toChooserRepoInfo(result.info));
      }
    } catch (err) {
      console.error('[LocationPicker] Failed to refresh repo info:', err);
    } finally {
      if (isRequestCurrent(requestGeneration)) {
        setRefreshing(false);
      }
    }
  }, [isRequestCurrent, onGetRepoInfo, refreshing, repoRootPath]);

  const handleBack = useCallback(() => {
    invalidateRequestGeneration();
    setMode('path-input');
    setRepoRootPath(null);
    setRepoInfo(null);
    setRefreshing(false);
    setPickerOperation(null);
  }, [invalidateRequestGeneration]);

  const movePathSelection = useCallback((direction: 'up' | 'down') => {
    const totalItems = selectableItems.length;
    if (totalItems === 0) {
      return;
    }

    const nextIndex = direction === 'down'
      ? (highlightedIndex < 0 ? 0 : (highlightedIndex + 1) % totalItems)
      : (highlightedIndex <= 0 ? totalItems - 1 : highlightedIndex - 1);
    const nextItem = selectableItems[nextIndex];
    if (nextItem) {
      setAutoHighlight(false);
      setHighlightedItemKey(nextItem.key);
    }
  }, [highlightedIndex, selectableItems]);

  const handleClosePicker = useCallback(() => {
    invalidateRequestGeneration();
    onClose();
  }, [invalidateRequestGeneration, onClose]);

  // Returns false when there is nothing to step back from, which dismisses the dialog.
  const stepBack = useCallback(() => {
    if (mode === 'repo-options') {
      // RepoOptions pushes its own handlers above this one in the escape
      // stack, so this branch fires only when those are inactive.
      handleBack();
      return true;
    }
    if (highlightedItemKey && !autoHighlight) {
      setHighlightedItemKey(null);
      return true;
    }
    return false;
  }, [mode, handleBack, highlightedItemKey, autoHighlight]);

  const handleDialogKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.altKey && !e.metaKey && !e.ctrlKey) {
      if (e.code === 'KeyT') {
        e.preventDefault();
        handleAgentChange(TERMINAL_AGENT);
        return;
      }

      if (e.code === 'KeyQ') {
        e.preventDefault();
        toggleYoloMode();
        return;
      }

      const digitMatch = /^Digit([1-9])$/.exec(e.code);
      if (digitMatch) {
        e.preventDefault();
        const nextAgent = [...agentShortcutByName].find(([, number]) => number === Number(digitMatch[1]))?.[0];
        if (nextAgent) {
          e.preventDefault();
          handleAgentChange(nextAgent);
        }
        return;
      }
    }

    if (mode !== 'path-input') {
      return;
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      movePathSelection('down');
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      movePathSelection('up');
    }
  }, [
    agentShortcutByName,
    handleAgentChange,
    mode,
    movePathSelection,
    toggleYoloMode,
  ]);

  if (!isOpen) {
    return null;
  }

  return (
    <PickerFrame inline={inline} onDismiss={handleClosePicker} onStepBack={stepBack} onKeyDown={handleDialogKeyDown}>
      <div className="picker-agent-bar">
        <div className="picker-agent-label">SESSION AGENT</div>
        <div className="picker-agent-controls">
          {harnessesLoading && <span role="status">Loading harnesses…</span>}
          {harnessesError && <span role="alert">{harnessesError}<button type="button" onClick={retryHarnesses}>Retry</button></span>}
          <div className="agent-toggle" role="radiogroup" aria-label="Session agent">
            {orderedAgentList.map((candidate) => {
              const available = isAgentAvailable(effectiveAgentAvailability, candidate);
              const shortcutNumber = agentShortcutByName.get(candidate);
              const shortcut = candidate === TERMINAL_AGENT
                ? keyCombo('alt', 'T')
                : shortcutNumber && shortcutNumber <= 9 ? keyCombo('alt', String(shortcutNumber)) : null;
              const label = pickerAgentLabel(candidate);
              return (
                <button
                  key={candidate}
                  type="button"
                  className={`agent-option ${agent === candidate ? 'active' : ''}`}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => handleAgentChange(candidate)}
                  role="radio"
                  aria-checked={agent === candidate}
                  disabled={!available}
                  title={!available ? `${label} is not available on this machine` : undefined}
                >
                  <span className="agent-option-name">{label}</span>
                  {available && shortcut && <kbd className="agent-shortcut">{shortcut}</kbd>}
                </button>
              );
            })}
          </div>
        </div>
      </div>
      {autoModeSupported && (
        <div className="picker-chief-bar">
          <div className="picker-agent-label">AUTO MODE</div>
          <div className="picker-chief-controls">
            <div className="agent-toggle">
              <button
                type="button"
                className={`agent-option ${autoMode ? 'active' : ''}`}
                data-testid="location-picker-automode-toggle"
                role="switch"
                aria-checked={autoMode}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  autoModeTouchedRef.current = true;
                  setAutoMode((prev) => !prev);
                }}
                title="Judge calls that reach outside this session's directory against what the conversation asked for, instead of running everything"
              >
                <span className="agent-option-name">{autoMode ? 'On' : 'Off'}</span>
              </button>
            </div>
          </div>
        </div>
      )}
      {chiefToggleEligible && (
        <div className="picker-chief-bar">
          <div className="picker-agent-label">CHIEF OF STAFF</div>
          <div className="picker-chief-controls">
            <div className="agent-toggle">
              <button
                type="button"
                className={`agent-option ${chiefOfStaff ? 'active' : ''}`}
                data-testid="location-picker-chief-toggle"
                role="switch"
                aria-checked={chiefOfStaff}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => setChiefOfStaff((prev) => !prev)}
                title="Launch this session as the chief of staff — it runs the notebook and delegates work to other sessions"
              >
                <span className="agent-option-name">{chiefOfStaff ? 'On' : 'Off'}</span>
              </button>
            </div>
          </div>
        </div>
      )}
      <div className="picker-chief-bar">
        <div className="picker-agent-label">YOLO</div>
        <div className="picker-chief-controls">
          <div className="agent-toggle">
              <button
                type="button"
                className={`agent-option ${yoloMode ? 'active' : ''}`}
                data-testid="location-picker-yolo-toggle"
                role="switch"
                aria-checked={yoloMode}
                disabled={!yoloSupported}
                onMouseDown={(e) => e.preventDefault()}
                onClick={toggleYoloMode}
                title={yoloSupported
                  ? 'Run the agent without asking for permission'
                  : `${agentLabel(agent)} does not support yolo mode`}
              >
                <span className="agent-option-name">{yoloMode ? 'On' : 'Off'}</span>
                {yoloSupported && <kbd className="agent-shortcut">{keyCombo('alt', 'Q')}</kbd>}
              </button>
            </div>
          </div>
        </div>
      {!hasAvailableAgents && (
        <div className="picker-agent-warning">{noAgentsMessage}</div>
      )}
      {mode === 'path-input' ? (
        <>
          <div className="picker-header">
            <div className="picker-header-top">
              <div className="picker-title" data-testid="location-picker-title">
                {title}
              </div>
            </div>
            <PathInput
              value={inputValue}
              onChange={updateInputValue}
              onTabComplete={handleTabComplete}
              onSelect={handlePathSelect}
              onSubmit={handlePathInputSubmit}
              ghostText={ghostText}
              completionValue={tabCompletionValue}
              hasSelectedSinceTab={hasSelectedSinceTab}
              placeholder="Type path (e.g., ~/projects) or search..."
              autoFocus={active}
            />
            {currentDir && (
              <div className="picker-breadcrumb">
                <span className="picker-breadcrumb-label">Browsing:</span>
                <span className="picker-breadcrumb-path" data-testid="location-picker-breadcrumb-path">{currentDir}</span>
              </div>
            )}
            {pickerOperation && (
              <div className="picker-operation" role="status" aria-label={pickerOperation}>
                <span className="picker-operation-pulse" aria-hidden="true" />
                <span>{pickerOperation}</span>
                {selectedPath && <span className="picker-operation-path">{toDisplayPath(selectedPath, homePath)}</span>}
              </div>
            )}
          </div>

          <div className="picker-results" role="listbox" aria-label="Locations">
            {visibleRecent.length > 0 && (
              <div className="picker-section" role="group" aria-label="Recent">
                <div className="picker-section-title">RECENT</div>
                {visibleRecent.map((loc, index) => (
                  <div
                    key={loc.path}
                    className={`picker-item ${index === highlightedIndex ? 'selected' : ''}`}
                    role="option"
                    tabIndex={-1}
                    aria-selected={index === highlightedIndex}
                    data-testid={`location-picker-item-${index}`}
                    data-index={index}
                    data-kind="recent"
                    data-path={loc.selectionPath}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => void handleSelectPath(loc.selectionPath)}
                  >
                    <div className="picker-icon">🕐</div>
                    <div className="picker-info">
                      <div className="picker-name">{loc.name}</div>
                      <div className="picker-path">{loc.selectionPath}</div>
                    </div>
                  </div>
                ))}
              </div>
            )}

            {fsSuggestions.length > 0 && (
              <div className="picker-section" role="group" aria-label="Directories">
                <div className="picker-section-title">DIRECTORIES</div>
                {fsSuggestions.map((item, index) => {
                  const globalIndex = visibleRecent.length + index;
                  return (
                    <div
                      key={item.path}
                      className={`picker-item ${globalIndex === highlightedIndex ? 'selected' : ''}`}
                      role="option"
                      tabIndex={-1}
                      aria-selected={globalIndex === highlightedIndex}
                      data-testid={`location-picker-item-${globalIndex}`}
                      data-index={globalIndex}
                      data-kind="directory"
                      data-path={item.path}
                      onMouseDown={(e) => e.preventDefault()}
                      onClick={() => void handleSelectPath(item.path)}
                    >
                      <div className="picker-icon">📁</div>
                      <div className="picker-info">
                        <div className="picker-name">{item.name}</div>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}

            {fsSuggestions.length === 0 && visibleRecent.length === 0 && (
              <div className="picker-empty" data-testid="location-picker-empty">
                {inputValue
                  ? 'No matches. Press Enter to use path directly.'
                  : 'Type a path to browse directories'}
              </div>
            )}
          </div>

          <div className="picker-footer">
            <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
            <span className="shortcut"><kbd>Tab</kbd> autocomplete</span>
            <span className="shortcut"><kbd>Enter</kbd> select</span>
            <span className="shortcut picker-footer-cancel"><kbd>Esc</kbd> cancel</span>
          </div>
        </>
      ) : repoInfo ? (
        <RepoOptions
          repoInfo={repoInfo}
          selectedPath={selectedPath || repoRootPath || undefined}
          preferredDestination={rememberedDestination}
          onSelectedPathChange={(path) => {
            setSelectedPathFromPhysical(path);
          }}
          onSelectMainRepo={handleSelectMainRepo}
          onSelectWorktree={handleSelectWorktree}
          onCreateWorktree={handleCreateWorktree}
          onDeleteWorktree={onDeleteWorktree ? async (path, options) => {
            const requestGeneration = requestGenerationRef.current;
            const deleteResult = options
              ? await onDeleteWorktree(path, options)
              : await onDeleteWorktree(path);
            if (!deleteResult.success) {
              throw new Error(deleteResult.error || 'Failed to delete worktree');
            }
            if (repoRootPath && onGetRepoInfo) {
              const result = await onGetRepoInfo(repoRootPath);
              if (isRequestCurrent(requestGeneration) && result.success && result.info) {
                setRepoInfo(toChooserRepoInfo(result.info));
              } else if (isRequestCurrent(requestGeneration) && !result.success) {
                throw new Error(result.error || 'Failed to refresh repo options');
              }
            }
          } : undefined}
          onError={onError}
          onRefresh={handleRefresh}
          onBack={handleBack}
          refreshing={refreshing}
          globalEscape={!inline}
        />
      ) : null}
    </PickerFrame>
  );
}

// The dialog sits in a dismissable overlay; inline, the frame stands alone and Escape only steps back.
function PickerFrame({ inline, onDismiss, onStepBack, onKeyDown, children }: {
  inline: boolean;
  onDismiss: () => void;
  onStepBack: () => boolean;
  onKeyDown: (e: React.KeyboardEvent<HTMLDivElement>) => void;
  children: React.ReactNode;
}) {
  // Inline takes Escape only with focus inside it; the global stack would take it from the sidebar.
  useEscapeStack(() => {
    if (!onStepBack()) onDismiss();
  }, !inline);
  const handleKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (inline && e.key === 'Escape') {
      if (!e.defaultPrevented) onStepBack();
      e.preventDefault();
      return;
    }
    onKeyDown(e);
  };
  const frame = (
    <div
      className={`location-picker${inline ? ' location-picker--inline' : ''}`}
      data-testid={inline ? 'location-picker-inline' : 'location-picker'}
      tabIndex={-1}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={handleKeyDown}
    >
      {children}
    </div>
  );
  if (inline) return frame;
  return (
    <div className="location-picker-overlay" data-testid="location-picker-overlay" onClick={onDismiss}>
      {frame}
    </div>
  );
}
