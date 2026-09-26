import { act, fireEvent, screen, within } from '@testing-library/react';
import { vi } from 'vitest';
import { agentPane, daemonSession, daemonWorkspace } from './daemonFixtures';
import type { EventMessage } from './protocol';
import { gesture, pressShortcut, renderApp } from './renderApp';
import type { ScriptedDaemon } from './scriptedDaemon';
import { serveSettings } from './settings';

export const HOME = '/home/me';
const AT = '2026-05-16T16:00:00Z';

type RepoInfo = NonNullable<EventMessage<'get_repo_info_result'>['info']>;

export function repoInfo(repo: string, worktrees: Array<{ path: string; branch: string }> = [], overrides: Partial<RepoInfo> = {}): RepoInfo {
  return {
    repo,
    current_branch: 'main',
    current_commit_hash: 'abcdef1234567890',
    current_commit_time: AT,
    default_branch: 'main',
    branches: [],
    worktrees: worktrees.map((worktree) => ({ ...worktree, main_repo: repo })),
    ...overrides,
  };
}

function absolute(path: string): string {
  const expanded = path === '~' || path.startsWith('~/') ? `${HOME}${path.slice(1)}` : path;
  return expanded.length > 1 ? expanded.replace(/\/+$/, '') : expanded;
}

export function inspection(path: string, { repoRoot, exists = true }: { repoRoot?: string; exists?: boolean } = {}): EventMessage<'inspect_path_result'> {
  return {
    event: 'inspect_path_result',
    success: true,
    inspection: {
      input_path: path,
      resolved_path: absolute(path),
      home_path: HOME,
      exists,
      is_directory: exists,
      ...(repoRoot ? { repo_root: repoRoot } : {}),
    },
  };
}

interface Machine {
  recent?: string[];
  directories?: Record<string, string[]>;
  repos?: RepoInfo[];
}

function repoRootOf(path: string, repos: RepoInfo[]): string | undefined {
  const target = absolute(path);
  for (const info of repos) {
    if (target === info.repo || info.worktrees?.some((worktree) => worktree.path === target)) return info.repo;
  }
  return undefined;
}

export function serveMachine(daemon: ScriptedDaemon, { recent = [], directories = {}, repos = [] }: Machine = {}) {
  daemon.on('get_recent_locations', () => ({
    event: 'recent_locations_result',
    success: true,
    home_path: HOME,
    recent_locations: recent.map((path) => ({ path, last_seen: AT, use_count: 1 })),
  }));
  daemon.on('browse_directory', ({ input_path }) => {
    const slash = input_path.lastIndexOf('/');
    const directory = input_path.slice(0, slash + 1);
    const fragment = input_path.slice(slash + 1);
    const names = (directories[absolute(directory)] ?? []).filter((name) => name.startsWith(fragment));
    return {
      event: 'browse_directory_result',
      success: true,
      input_path,
      home_path: HOME,
      directory: absolute(directory),
      entries: names.map((name) => ({ name, path: `${absolute(directory)}/${name}`, is_dir: true })),
    };
  });
  daemon.on('inspect_path', ({ path }) => inspection(path, { repoRoot: repoRootOf(path, repos) }));
  daemon.on('create_worktree', ({ main_repo, branch, endpoint_id }) => ({
    event: 'create_worktree_result',
    success: true,
    path: `${main_repo}--${branch}`,
    ...(endpoint_id ? { endpoint_id } : {}),
  }));
  daemon.on('get_repo_info', ({ repo, endpoint_id }) => ({
    event: 'get_repo_info_result',
    success: true,
    info: repos.find((entry) => entry.repo === repo)!,
    ...(endpoint_id ? { endpoint_id } : {}),
  }));
}

export function serveLaunches(daemon: ScriptedDaemon) {
  daemon.on('register_workspace', ({ id, title, directory }) => ({
    event: 'workspace_registered',
    workspace: daemonWorkspace(id, { root: null }, { title, directory }),
  }));
  daemon.on('workspace_layout_add_session_pane', ({ workspace_id, pane_id, session_id }) => [
    { event: 'workspace_layout_action_result', action: 'workspace_layout_add_session_pane', workspace_id, pane_id, success: true },
    {
      event: 'workspace_layout_updated',
      workspace_layout: daemonWorkspace(workspace_id, {
        root: { type: 'pane', pane_id },
        panes: [{ ...agentPane(session_id!, workspace_id), pane_id: pane_id! }],
      }).layout!,
    },
  ]);
  daemon.on('spawn_session', ({ id, workspace_id, cwd }) => [
    { event: 'spawn_result', id, success: true },
    { event: 'session_registered', session: daemonSession(id, { workspace_id, directory: cwd, state: 'launching' }) },
  ]);
}

export function pathInput() {
  return screen.getByTestId('location-picker-path-input') as HTMLInputElement;
}

export async function openPicker(machine: Machine = {}, initialState: Partial<EventMessage<'initial_state'>> = {}) {
  const view = await renderApp({ initialState });
  serveMachine(view.daemon, machine);
  serveLaunches(view.daemon);
  serveSettings(view.daemon, initialState.settings ?? {});
  await gesture(view.daemon, () => pressShortcut('session.newWorkspace'));
  return view;
}

export async function typePath(daemon: ScriptedDaemon, path: string) {
  fireEvent.change(pathInput(), { target: { value: path } });
  await act(() => vi.advanceTimersByTimeAsync(150));
  await daemon.idle();
}

export async function press(daemon: ScriptedDaemon, key: string, init: KeyboardEventInit = {}, target: Element = pathInput()) {
  fireEvent.keyDown(target, { key, ...init });
  await daemon.idle();
}

export async function submitPath(daemon: ScriptedDaemon, path: string) {
  await typePath(daemon, path);
  await press(daemon, 'Enter');
}

export function launchedAt(daemon: ScriptedDaemon) {
  return daemon.sentOf('spawn_session').map(({ cwd, agent, endpoint_id, yolo_mode, chief_of_staff, auto_mode }) => ({
    cwd,
    agent,
    ...(endpoint_id !== undefined ? { endpoint_id } : {}),
    ...(yolo_mode !== undefined ? { yolo_mode } : {}),
    ...(chief_of_staff !== undefined ? { chief_of_staff } : {}),
    ...(auto_mode !== undefined ? { auto_mode } : {}),
  }));
}

export function destinationMemory(repo: string, endpointId?: string) {
  return endpointId ? `new_session_destination_endpoint_${endpointId}_${repo}` : `new_session_destination_local_${repo}`;
}

export function chosenRow() {
  const destinations = within(screen.getByRole('listbox', { name: 'Open existing' }));
  const chosen = destinations.queryByRole('option', { selected: true });
  return chosen ? destinations.getAllByRole('option').indexOf(chosen) : -1;
}
