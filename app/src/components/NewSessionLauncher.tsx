import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { LocationPickerProps } from './LocationPicker';
import type { RecentLocation } from '../hooks/useDaemonSocket';
import type { SessionAgent } from '../types/sessionAgent';
import { useSettings } from '../contexts/SettingsContext';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { PathInput } from './NewSessionDialog/PathInput';
import { SessionControls, useSessionControls } from './NewSessionDialog/SessionControls';
import {
  parseRepoDestination,
  repoDestinationSettingKey,
} from './NewSessionDialog/destinationMemory';
import { generateWorktreeName } from './NewSessionDialog/worktreeNames';
import { WhereList, locationAge, type WhereRow } from './NewSessionDialog/WhereList';
import {
  expandDisplayPath,
  normalizePickerPath,
  pathBasename,
  toDisplayPath,
} from '../utils/locationPickerPaths';
import { isAccelKeyPressed } from '../shortcuts/platform';
import { keyCombo } from '../shortcuts/formatShortcut';
import './LocationPicker.css';
import './NewSessionLauncher.css';

type RepoInfo = NonNullable<
  Awaited<ReturnType<NonNullable<LocationPickerProps['onGetRepoInfo']>>>['info']
>;
type Row = {
  kind: 'recent' | 'folder';
  path: string;
  name: string;
  repoRoot?: string;
  lastSeen?: string;
};
export interface NewSessionLauncherProps extends Omit<
  LocationPickerProps,
  'onSelect' | 'onCreateWorktreeSession' | 'projectsDirectory'
> {
  onSelect: (
    path: string,
    agent: SessionAgent,
    yolo?: boolean,
    chief?: boolean,
    auto?: boolean,
  ) => Promise<unknown>;
  onCreateWorktreeSession: (
    repo: string,
    branch: string,
    from: string,
    agent: SessionAgent,
    yolo: boolean,
    auto?: boolean,
    chief?: boolean,
    onCreated?: (path: string) => void,
  ) => Promise<unknown>;
}
type Where = { repo: string; info?: RepoInfo };

export function NewSessionLauncher({
  isOpen,
  initialAgent,
  variant = 'dialog',
  active = true,
  title = 'New session',
  onClose,
  onSelect,
  onGetRecentLocations,
  onBrowseDirectory,
  onInspectPath,
  onGetRepoInfo,
  onCreateWorktreeSession,
  onDeleteWorktree,
  agentAvailability,
  chiefExists,
}: NewSessionLauncherProps) {
  const inline = variant === 'inline';
  const { settings, setSetting } = useSettings();
  const controls = useSessionControls({
    isOpen,
    initialAgent,
    agentAvailability,
    chiefExists,
    resetAgentOnOpen: true,
  });
  const [query, setQuery] = useState('');
  const [highlight, setHighlight] = useState(-1);
  const [recents, setRecents] = useState<RecentLocation[]>([]);
  const [fresh, setFresh] = useState(false);
  const [home, setHome] = useState('');
  const [where, setWhere] = useState<Where | null>(null);
  const [branch, setBranch] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [deletion, setDeletion] = useState<{
    path: string;
    branch: string;
    force: boolean;
  } | null>(null);
  const generation = useRef(0);
  const generated = useRef(new Map<string, string>());
  const created = useRef(new Map<string, string>());
  const folders = useRef(new Map<string, boolean>());
  const root = useRef<HTMLDivElement>(null);
  const pathMode = query.trim().startsWith('~') || query.trim().startsWith('/');
  const remember = (repo: string) =>
    parseRepoDestination(settings[repoDestinationSettingKey(repo)]) || 'main_repo';
  const display = (path: string) => toDisplayPath(path, home);

  const browse = useCallback(
    async (path: string) => {
      if (!onBrowseDirectory) throw new Error('Folder browsing is unavailable.');
      const reply = await onBrowseDirectory(path);
      for (const entry of reply.entries || [])
        folders.current.set(entry.path, entry.is_repo === true);
      return reply;
    },
    [onBrowseDirectory],
  );
  const { suggestions, currentDir } = useFilesystemSuggestions(
    pathMode && !where ? query.trim() : '',
    browse,
    {
      homePath: home,
      onHomePathChange: setHome,
      enabled: isOpen && pathMode && !where,
    },
  );

  useEffect(() => {
    generation.current++;
    setFresh(false);
    if (!isOpen) return;
    setQuery('');
    setHighlight(-1);
    setWhere(null);
    setBranch('');
    setError('');
    setDeletion(null);
    setBusy(false);
    generated.current.clear();
    created.current.clear();
    folders.current.clear();
    let cancelled = false;
    onGetRecentLocations?.()
      .then((reply) => {
        if (cancelled) return;
        setRecents(
          [...reply.locations].sort((a, b) => b.last_seen.localeCompare(a.last_seen)),
        );
        if (reply.home_path) setHome(reply.home_path);
        setFresh(true);
      })
      .catch((reason) => {
        if (!cancelled) setError(String(reason));
      });
    return () => {
      cancelled = true;
      generation.current++;
    };
  }, [isOpen, onGetRecentLocations]);

  const rows = useMemo<Row[]>(() => {
    const needle = expandDisplayPath(query.trim(), home).toLowerCase();
    const recent = recents.filter((item) =>
      pathMode
        ? item.path.toLowerCase().startsWith(needle) &&
          item.path.replace(/\/+$/, '').toLowerCase() !== needle.replace(/\/+$/, '')
        : item.path.toLowerCase().includes(needle),
    );
    const recentRows: Row[] = recent.map((item) => ({
      kind: 'recent',
      path: item.path,
      name: pathBasename(item.path),
      repoRoot: item.repo_root,
      lastSeen: item.last_seen,
    }));
    const directoryRows: Row[] = pathMode
      ? suggestions
          .filter((item) => item.isDir)
          .map((item) => ({
            kind: 'folder',
            path: item.absPath,
            name: item.name,
            repoRoot: item.isRepo ? item.absPath : undefined,
          }))
      : [];
    return [...recentRows, ...directoryRows];
  }, [query, home, recents, pathMode, suggestions]);
  useEffect(() => {
    if (!where && !pathMode) setHighlight(fresh && rows.length ? 0 : -1);
  }, [query, fresh, rows, where, pathMode]);
  useEffect(() => {
    root.current
      ?.querySelector(`[data-index="${highlight}"]`)
      ?.scrollIntoView({ block: 'nearest' });
  }, [highlight, query, branch, where?.info]);

  const name = where ? branch.trim() || generated.current.get(where.repo) || '' : '';
  const whereRows = useMemo<WhereRow[]>(() => {
    if (!where?.info) return [];
    const info = where.info;
    return [
      { kind: 'new', from: 'default', branch: info.default_branch } as const,
      ...(info.current_branch !== info.default_branch
        ? [
            {
              kind: 'new',
              from: 'current',
              branch: info.current_branch,
            } as const,
          ]
        : []),
      { kind: 'main', branch: info.current_branch } as const,
      ...info.worktrees
        .filter(
          (item) =>
            item.path !== where.repo &&
            item.branch.toLowerCase().includes(branch.trim().toLowerCase()),
        )
        .map((item) => ({
          kind: 'existing' as const,
          path: item.path,
          branch: item.branch,
          createdAt: item.created_at,
        })),
    ];
  }, [where, branch]);
  const memory = where ? remember(where.repo) : 'main_repo';
  useEffect(() => {
    if (!where?.info) return;
    const exact = branch.trim()
      ? whereRows.findIndex((row) => row.kind === 'existing' && row.branch === branch.trim())
      : -1;
    setHighlight(
      branch.trim()
        ? Math.max(0, exact)
        : memory === 'new_worktree'
          ? 0
          : whereRows.findIndex((row) => row.kind === 'main'),
    );
  }, [branch, where?.repo, Boolean(where?.info), memory]); // eslint-disable-line react-hooks/exhaustive-deps

  const changeQuery = (value: string) => {
    generation.current++;
    setQuery(value);
    setHighlight(value.trim().startsWith('~') || value.trim().startsWith('/') ? -1 : 0);
    setError('');
  };
  const stepBack = () => {
    if (busy) return;
    setError('');
    if (deletion) {
      setDeletion(null);
      return;
    }
    generation.current++;
    if (where) {
      setHighlight(0);
      setWhere(null);
      setBranch('');
    } else if (query) {
      setQuery('');
      setHighlight(fresh && recents.length ? 0 : -1);
    } else if (!inline) onClose();
  };
  useEscapeStack(stepBack, isOpen && active && !inline);

  async function openWhere(repo: string) {
    const request = ++generation.current;
    setWhere({ repo });
    setBranch('');
    setHighlight(-1);
    setError('');
    try {
      const reply = await onGetRepoInfo?.(repo);
      if (request !== generation.current) return;
      if (!reply?.success || !reply.info)
        throw new Error(reply?.error || 'Failed to read repository');
      if (!generated.current.has(repo))
        generated.current.set(
          repo,
          generateWorktreeName(reply.info.worktrees.map((item) => item.branch)),
        );
      setWhere((value) => value && { ...value, info: reply.info });
    } catch (reason) {
      if (request === generation.current)
        setError(reason instanceof Error ? reason.message : String(reason));
    }
  }

  async function launch(row?: Row, terminal = false, destination?: WhereRow) {
    if (busy) return;
    const agent = terminal ? 'shell' : controls.agent;
    const yolo = agent !== 'shell' && controls.yoloMode && controls.yoloSupported;
    const chief = agent !== 'shell' && controls.chiefOfStaff && controls.chiefToggleEligible;
    const auto =
      agent !== 'shell' && controls.autoModeSupported ? controls.autoMode : undefined;
    const request = generation.current;
    setBusy(true);
    setError('');
    try {
      let path = row?.path;
      let repo = row?.repoRoot;
      let action = destination;
      if (!where && !row && pathMode) {
        const typed = normalizePickerPath(query, home);
        const reply = await onInspectPath?.(typed);
        if (request !== generation.current) return;
        if (!reply?.inspection?.exists || !reply.inspection.is_directory)
          throw new Error(`Directory not found: ${typed}`);
        path = reply.inspection.resolved_path;
        repo = reply.inspection.repo_root === path ? path : undefined;
      }
      if (where) repo = where.repo;
      if (!action && repo)
        action =
          agent !== 'shell' && remember(repo) === 'new_worktree'
            ? { kind: 'new', from: 'default', branch: '' }
            : { kind: 'main', branch: '' };
      let destinationMemory: 'main_repo' | 'new_worktree' | undefined;
      if (action?.kind === 'new' && repo) {
        if (!generated.current.has(repo)) generated.current.set(repo, generateWorktreeName());
        const branchName = where ? name : generated.current.get(repo)!;
        const from =
          action.from === 'default'
            ? where?.info
              ? `origin/${where.info.default_branch}`
              : ''
            : action.branch;
        let startingFrom = from;
        if (!where) {
          const reply = await onGetRepoInfo?.(repo);
          if (!reply?.success || !reply.info)
            throw new Error(reply?.error || 'Failed to read repository');
          startingFrom = `origin/${reply.info.default_branch}`;
        }
        const key = `${repo}:${branchName}:${startingFrom}`;
        const existing = created.current.get(key);
        if (existing) await onSelect(existing, agent, yolo, chief, auto);
        else
          await onCreateWorktreeSession(
            repo,
            branchName,
            startingFrom,
            agent,
            yolo,
            auto,
            chief,
            (path) => created.current.set(key, path),
          );
        destinationMemory = 'new_worktree';
      } else {
        const target =
          action?.kind === 'existing' ? action.path : action?.kind === 'main' ? repo : path;
        if (!target) return;
        await onSelect(target, agent, yolo, chief, auto);
        if (action?.kind === 'main') destinationMemory = 'main_repo';
      }
      if (
        repo &&
        destinationMemory &&
        agent !== 'shell' &&
        settings[repoDestinationSettingKey(repo)] !== destinationMemory
      )
        setSetting(repoDestinationSettingKey(repo), destinationMemory);
      onClose();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  }

  async function tab() {
    if (where || busy) return;
    const row = rows[highlight];
    if (controls.agent !== 'shell' && row?.repoRoot) {
      await openWhere(row.repoRoot);
      return;
    }
    if (controls.agent !== 'shell' && !row && pathMode) {
      const path = normalizePickerPath(query, home);
      const known = folders.current.get(path);
      if (known === true) {
        await openWhere(path);
        return;
      }
      if (known === undefined) {
        const request = generation.current;
        try {
          const reply = await onInspectPath?.(path);
          if (request !== generation.current) return;
          if (
            reply?.inspection?.repo_root === reply?.inspection?.resolved_path &&
            reply?.inspection?.repo_root
          ) {
            await openWhere(reply.inspection.repo_root);
            return;
          }
        } catch (reason) {
          setError(reason instanceof Error ? reason.message : String(reason));
          return;
        }
      }
    }
    const target = row?.path || suggestions[0]?.absPath;
    if (target) changeQuery(`${display(target).replace(/\/+$/, '')}/`);
  }

  async function deleteWorktree() {
    if (!deletion || !onDeleteWorktree || busy) return;
    setBusy(true);
    setError('');
    try {
      const reply = await onDeleteWorktree(
        deletion.path,
        deletion.force ? { force: true } : undefined,
      );
      if (!reply.success) throw new Error(reply.error || 'Failed to delete worktree');
      const remaining = whereRows.length - 1;
      setWhere((value) =>
        value?.info
          ? {
              ...value,
              info: {
                ...value.info,
                worktrees: value.info.worktrees.filter((item) => item.path !== deletion.path),
              },
            }
          : value,
      );
      setHighlight(Math.min(highlight, remaining - 1));
      setDeletion(null);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
      if (!deletion.force && (reason as { forceable?: boolean }).forceable)
        setDeletion({ ...deletion, force: true });
      else setDeletion(null);
    } finally {
      setBusy(false);
    }
  }

  function keyDown(event: React.KeyboardEvent) {
    if (busy) {
      if (['Enter', 'Tab', 'Escape'].includes(event.key)) event.preventDefault();
      return;
    }
    if (deletion) {
      event.preventDefault();
      event.stopPropagation();
      if (event.key.toLowerCase() === 'y') void deleteWorktree();
      else if (event.key.toLowerCase() === 'n' || event.key === 'Escape') {
        setDeletion(null);
        setError('');
      }
      return;
    }
    if (controls.handleControlKey(event)) return;
    const list = where ? whereRows : rows;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (list.length)
        setHighlight(
          event.key === 'ArrowDown'
            ? (highlight + 1) % list.length
            : highlight <= 0
              ? list.length - 1
              : highlight - 1,
        );
    } else if (event.key === 'Enter') {
      event.preventDefault();
      event.stopPropagation();
      if (where ? whereRows[highlight] : rows[highlight] || pathMode)
        void launch(
          rows[highlight],
          isAccelKeyPressed(event),
          where ? whereRows[highlight] : undefined,
        );
    } else if (event.key === 'Tab') {
      event.preventDefault();
      void tab();
    } else if (
      event.key === 'Escape' ||
      (where && event.key === 'Backspace' && !branch && !isAccelKeyPressed(event))
    ) {
      event.preventDefault();
      event.stopPropagation();
      stepBack();
    } else if (where && isAccelKeyPressed(event) && event.key === 'Backspace') {
      event.preventDefault();
      setError('');
      const row = whereRows[highlight];
      if (row?.kind === 'existing')
        setDeletion({ path: row.path, branch: row.branch, force: false });
    }
  }

  if (!isOpen) return null;
  const selected = rows[highlight];
  const ghostPath = pathMode
    ? selected?.kind === 'folder'
      ? selected.path
      : suggestions[0]?.absPath
    : '';
  const ghost = ghostPath ? `${display(ghostPath).replace(/\/+$/, '')}/` : '';
  const typedRepo =
    highlight < 0 &&
    pathMode &&
    folders.current.get(normalizePickerPath(query, home)) === true;
  const frame = (
    <div
      ref={root}
      className={`location-picker new-session-launcher${inline ? ' location-picker--inline' : ''}`}
      data-testid={inline ? 'location-picker-inline' : 'location-picker'}
      data-launcher="one-screen"
      onClick={(event) => event.stopPropagation()}
      onKeyDown={keyDown}
      aria-busy={busy}
    >
      <div className="launcher-controls">
        <SessionControls controls={controls} compact />
      </div>
      <div className="picker-header">
        <div className="picker-title" data-testid="location-picker-title">
          {initialAgent === 'shell' ? 'New terminal' : title}
        </div>
        <PathInput
          value={where ? branch : query}
          onChange={(value) => {
            if (busy || deletion) return;
            if (where) {
              setBranch(value);
              setError('');
            } else changeQuery(value);
          }}
          leading={
            where && <span className="launcher-repo-chip">{pathBasename(where.repo)} ›</span>
          }
          ghostText={where ? '' : ghost}
          onKeyDown={(event) => {
            event.stopPropagation();
            keyDown(event);
          }}
          onSelect={() => {}}
          onSubmit={() => {}}
          onTabComplete={() => {}}
          placeholder={where ? 'name a new worktree, or pick below' : 'Project or path'}
          autoFocus={active}
        />
        {!where && pathMode && currentDir && (
          <span
            className="picker-breadcrumb-path"
            data-testid="location-picker-breadcrumb-path"
          >
            {currentDir}
          </span>
        )}
      </div>
      {where ? (
        where.info ? (
          <WhereList
            rows={whereRows}
            highlight={highlight}
            repo={where.repo}
            name={name}
            typed={Boolean(branch.trim())}
            remembered={memory}
            deletion={deletion}
            onPick={(index) => {
              if (busy || deletion) return;
              setHighlight(index);
              void launch(undefined, false, whereRows[index]);
            }}
          />
        ) : (
          <div className="picker-empty" role="status">
            {!error && `Reading ${pathBasename(where.repo)}…`}
          </div>
        )
      ) : (
        <div className="picker-results" role="listbox" aria-label="Locations">
          {rows.map((row, index) => (
            <Fragment key={`${row.kind}:${row.path}`}>
              {(index === 0 || rows[index - 1].kind !== row.kind) && (
                <div className="picker-section-title">
                  {row.kind === 'recent'
                    ? 'Recent'
                    : `Folders in ${currentDir.replace(/\/$/, '')}/`}
                </div>
              )}
              <div
                className={`picker-item${highlight === index ? ' selected' : ''}`}
                role="option"
                aria-selected={highlight === index}
                data-testid={`location-picker-item-${index}`}
                data-index={index}
                data-kind={row.kind === 'folder' ? 'directory' : 'recent'}
                data-path={row.path}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => {
                  setHighlight(index);
                  void launch(row);
                }}
              >
                <span className="launcher-icon">
                  {row.kind === 'recent' ? '◷' : row.repoRoot ? '⎇' : '▸'}
                </span>
                <div className="picker-info">
                  <div className="picker-name">
                    {row.name}
                    {row.kind === 'folder' && '/'}
                  </div>
                  {row.kind === 'recent' && (
                    <div className="picker-path">{display(row.path)}</div>
                  )}
                </div>
                {row.repoRoot &&
                  (controls.agent !== 'shell' &&
                  (row.kind === 'recent' || highlight === index) ? (
                    <span
                      className={`launcher-badge launcher-destination${remember(row.repoRoot) === 'new_worktree' ? ' launcher-worktree' : ''}`}
                    >
                      {remember(row.repoRoot) === 'new_worktree'
                        ? '⎇ new worktree'
                        : 'main checkout'}
                    </span>
                  ) : (
                    row.kind === 'folder' && <span className="launcher-badge">repo</span>
                  ))}
                {row.lastSeen && (
                  <span className="launcher-age">{locationAge(row.lastSeen)}</span>
                )}
              </div>
            </Fragment>
          ))}
          {!rows.length && (
            <div className="picker-empty" data-testid="location-picker-empty">
              {pathMode
                ? 'No folders match. Enter uses the path as typed.'
                : 'No recent project matches. Type ~/ to browse folders.'}
            </div>
          )}
        </div>
      )}
      {error && (
        <div className="launcher-error" role="alert">
          {error}
        </div>
      )}
      {busy && (
        <div className="launcher-progress" role="status">
          Starting…
        </div>
      )}
      <div className="picker-footer">
        {deletion ? (
          <>
            <span>
              <kbd>y</kbd> delete
            </span>
            <span>
              <kbd>n</kbd> or <kbd>Esc</kbd> keep it
            </span>
          </>
        ) : (
          <>
            {where && <span>type to name it</span>}
            <span>
              <kbd>↑↓</kbd> {where ? 'choose' : 'move'}
            </span>
            <span>
              <kbd>Enter</kbd>{' '}
              {where ? 'start' : controls.agent === 'shell' ? 'open terminal' : 'start'}
            </span>
            {!where && (
              <span>
                <kbd>Tab</kbd>{' '}
                {controls.agent !== 'shell' && (selected?.repoRoot || typedRepo)
                  ? 'where…'
                  : 'complete'}
              </span>
            )}
            {(where || controls.agent !== 'shell') && (
              <span>
                <kbd>{keyCombo('accel', 'Enter')}</kbd> terminal here
              </span>
            )}
            {where && whereRows[highlight]?.kind === 'existing' && (
              <span>
                <kbd>{keyCombo('accel', '⌫')}</kbd> delete
              </span>
            )}
            <span className="launcher-footer-back">
              <kbd>Esc</kbd> {where ? 'back' : 'close'}
            </span>
          </>
        )}
      </div>
    </div>
  );
  return inline ? (
    frame
  ) : (
    <div
      className="location-picker-overlay"
      data-testid="location-picker-overlay"
      onClick={() => {
        if (!busy) onClose();
      }}
    >
      {frame}
    </div>
  );
}
