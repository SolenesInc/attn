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
import type { AgentAvailability } from '../utils/agentAvailability';
import { useSessionControls, SessionControls, resolvePickerAgent } from './NewSessionDialog/SessionControls';
import { repoDestinationSettingKey, parseRepoDestination, type RepoDestination } from './NewSessionDialog/destinationMemory';
import {
  buildInitialPickerInput,
  expandDisplayPath,
  normalizePickerPath,
  pathBasename,
  toDisplayPath,
} from '../utils/locationPickerPaths';
import './LocationPicker.css';

interface BackendRepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: Array<{ path: string; branch: string; created_at?: string }>;
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

export interface LocationPickerProps {
  initialAgent?: SessionAgent;
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
    autoMode?: boolean,
  ) => void;
  onGetRecentLocations?: () => Promise<{ locations: RecentLocation[]; home_path?: string }>;
  onBrowseDirectory?: (inputPath: string) => Promise<BrowseDirectoryResult>;
  onInspectPath?: (path: string) => Promise<InspectPathResult>;
  onGetRepoInfo?: (mainRepo: string) => Promise<{ success: boolean; info?: BackendRepoInfo; error?: string }>;
  onCreateWorktree?: (mainRepo: string, branch: string, path?: string, startingFrom?: string) => Promise<{ success: boolean; path?: string; error?: string }>;
  onCreateWorktreeSession?: (mainRepo: string, branch: string, startingFrom: string, agent: SessionAgent, yoloMode: boolean, autoMode?: boolean) => void;
  onDeleteWorktree?: (path: string, options?: { force?: boolean }) => Promise<{ success: boolean; error?: string }>;
  onError?: (message: string) => void;
  projectsDirectory?: string;
  agentAvailability?: AgentAvailability;
}

const MAX_RECENT_LOCATIONS = 10;
function pathStartsWith(candidate: string, input: string): boolean {
  const normalizedCandidate = candidate.toLowerCase();
  const normalizedInput = input.toLowerCase();
  return normalizedCandidate.startsWith(normalizedInput);
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
  initialAgent,
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
}: LocationPickerProps) {
  const { settings, setSetting } = useSettings();
  const noAgentsMessage = 'No supported agent CLI found in PATH.';
  const controls = useSessionControls({ isOpen, initialAgent, agentAvailability });
  const { agent, yoloMode, autoMode, yoloSupported, autoModeSupported, effectiveAgentAvailability, hasAvailableAgents, agentShortcutByName, handleAgentChange, toggleYoloMode } = controls;
  const inline = variant === 'inline';

  const [mode, setMode] = useState<Mode>('path-input');
  const [inputValue, setInputValue] = useState('');
  const [highlightedItemKey, setHighlightedItemKey] = useState<string | null>(null);
  const [selectedPath, setSelectedPath] = useState('');
  const [recentLocations, setRecentLocations] = useState<RecentLocation[]>([]);
  const [homePath, setHomePath] = useState('');
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

  const handleHomePathChange = useCallback((nextHomePath: string) => {
    setHomePath((prev) => (prev === nextHomePath ? prev : nextHomePath));
  }, []);

  const { suggestions: fsSuggestions, currentDir } = useFilesystemSuggestions(
    inputValue,
    onBrowseDirectory,
    { homePath, onHomePathChange: handleHomePathChange, enabled: isOpen },
  );

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
      autoModeSupported ? autoMode : undefined,
    );
    onClose();
  }, [
    agent,
    autoMode,
    autoModeSupported,
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
        handleAgentChange('shell');
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
      <SessionControls controls={controls} />
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
