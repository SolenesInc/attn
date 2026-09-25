import { act } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { ptyAttach, ptyDetach, ptyKill, ptyReload, ptySpawn } from '../pty/bridge';
import { LOCAL_SNAPSHOT_FORMAT } from '../pty/attachPlanning';
import { AutomationActionTimeoutError } from './useDaemonSocket';
import { fileMarkdownSource, seedMarkdownSource } from '../components/MarkdownReader/documentSource';
import { renderWithDaemon } from '../test/renderApp';
import { initialState } from '../test/scriptedDaemon';
import type { EventMessage } from '../test/protocol';

type Session = EventMessage<'session_registered'>['session'];
type Workspace = EventMessage<'workspace_state_changed'>['workspace'];
type Pane = NonNullable<Workspace['layout']>['panes'][number];

const AT = '2026-04-08T00:00:00Z';

function session(fields: Partial<Session> & Pick<Session, 'id'>): Session {
  return {
    label: fields.id,
    agent: 'claude',
    directory: '/tmp/repo',
    workspace_id: `workspace-${fields.id}`,
    state: 'idle',
    last_seen: AT,
    state_since: AT,
    state_updated_at: AT,
    ...fields,
  };
}

function workspace(fields: Partial<Workspace> & Pick<Workspace, 'id'>): Workspace {
  return {
    title: fields.id,
    directory: '/tmp/repo',
    status: 'idle',
    muted: false,
    pinned: false,
    rank: fields.id,
    ...fields,
  };
}

function pane(fields: Partial<Pane> & Pick<Pane, 'pane_id' | 'workspace_id'>): Pane {
  return { kind: 'agent', status: 'ready', title: fields.pane_id, ...fields };
}

const MARKDOWN_TILE_LAYOUT = '{"type":"split","split_id":"root","direction":"vertical","ratio":0.5,"children":[{"type":"pane","pane_id":"pane-1"},{"type":"tile","tile_id":"tile-md","tile_kind":"markdown"}]}';

describe('useDaemonSocket PTY kill sequencing', () => {
  it('waits for session_exited before resolving ptyKill', async () => {
    const { daemon } = await renderWithDaemon();

    let resolved = false;
    const killPromise = ptyKill({ id: 'reload-race' }).then(() => {
      resolved = true;
    });

    await daemon.idle();
    expect(resolved).toBe(false);

    const sent = daemon.sent;
    const detachIdx = sent.findIndex((entry) => entry.cmd === 'detach_session');
    const killIdx = sent.findIndex((entry) => entry.cmd === 'kill_session');
    expect(detachIdx).toBeGreaterThanOrEqual(0);
    expect(killIdx).toBeGreaterThan(detachIdx);

    daemon.emit({ event: 'session_exited', id: 'reload-race', exit_code: 0 });
    await killPromise;
    expect(resolved).toBe(true);
  });

  it('resolves or rejects ptyReload from its daemon result', async () => {
    const { daemon } = await renderWithDaemon();
    const successfulReload = ptyReload({ id: 'reload-success', cols: 120, rows: 40 });
    expect(daemon.sent).toContainEqual({
      cmd: 'reload_session',
      id: 'reload-success',
      cols: 120,
      rows: 40,
    });
    daemon.emit({ event: 'reload_session_result', id: 'reload-success', success: true });

    await expect(successfulReload).resolves.toBeUndefined();

    const failedReload = ptyReload({ id: 'reload-failure', cols: 80, rows: 24 });
    daemon.emit({ event: 'reload_session_result', id: 'reload-failure', success: false, error: 'reload denied' });

    await expect(failedReload).rejects.toThrow('reload denied');
  });

  it('advertises and handles in-app browser control', async () => {
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockResolvedValue('{"title":"Fixture"}');
    const { daemon } = await renderWithDaemon();
    const hello = daemon.sent.find((entry) => entry.cmd === 'client_hello');
    expect(hello?.capabilities).toContain('browser_host');
    expect(hello?.browser_host_token).toBe('{"title":"Fixture"}');

    daemon.emit({
      event: 'browser_control_request',
      request_id: 'browser-request-1',
      workspace_id: 'workspace-1',
      tile_id: 'tile-browser',
      action: 'type',
      selector: '#query',
      text: 'browser text',
    });

    const result = await daemon.received('browser_control_result');
    expect(vi.mocked(invoke)).toHaveBeenCalledWith('browser_host_control', {
      label: 'browser-workspace-1-tile-browser',
      action: 'type',
      selector: '#query',
      text: 'browser text',
    });
    expect(result).toEqual({
      cmd: 'browser_control_result',
      request_id: 'browser-request-1',
      success: true,
      data: '{"title":"Fixture"}',
    });
  });

  it('stores daemon git operation lifecycle events by operation id', async () => {
    const { api, daemon } = await renderWithDaemon();

    daemon.emit({
      event: 'git_operation_started',
      operation: {
        id: 'op-1',
        kind: 'delete_worktree',
        status: 'running',
        path: '/tmp/repo/.worktrees/feature-a',
        started_at: '2026-05-16T16:00:00Z',
      },
    });

    expect(api.current.gitOperations['op-1']).toMatchObject({
      kind: 'delete_worktree',
      status: 'running',
      path: '/tmp/repo/.worktrees/feature-a',
    });

    daemon.emit({
      event: 'git_operation_finished',
      operation: {
        id: 'op-1',
        kind: 'delete_worktree',
        status: 'succeeded',
        path: '/tmp/repo/.worktrees/feature-a',
        started_at: '2026-05-16T16:00:00Z',
        finished_at: '2026-05-16T16:00:05Z',
        duration_ms: 5000,
      },
    });

    expect(api.current.gitOperations['op-1']).toMatchObject({
      status: 'succeeded',
      duration_ms: 5000,
    });
  });

  it('sends force option for delete worktree requests', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendDeleteWorktree('/tmp/repo--feature', undefined, { force: true });

    expect(await daemon.received('delete_worktree')).toMatchObject({
      cmd: 'delete_worktree',
      path: '/tmp/repo--feature',
      force: true,
    });

    daemon.emit({
      event: 'delete_worktree_result',
      path: '/tmp/repo--feature',
      success: true,
    });

    await expect(promise).resolves.toMatchObject({ success: true });
  });

  it('preserves forceable delete worktree failure details on rejection', async () => {
    const { api, daemon } = await renderWithDaemon();
    const promise = api.current.sendDeleteWorktree('/tmp/repo--feature');

    daemon.emit({
      event: 'delete_worktree_result',
      path: '/tmp/repo--feature',
      success: false,
      error: 'contains modified or untracked files',
      forceable: true,
      reason_kind: 'dirty_worktree',
    });

    await expect(promise).rejects.toMatchObject({
      message: 'contains modified or untracked files',
      forceable: true,
      reason_kind: 'dirty_worktree',
    });
  });

  it('serializes endpoint actions so concurrent updates do not collide', async () => {
    const { api, daemon } = await renderWithDaemon();

    const first = api.current.sendUpdateEndpoint('ep-1', { enabled: false });
    await expect(api.current.sendUpdateEndpoint('ep-2', { enabled: false })).rejects.toThrow(
      'Another endpoint action is already in progress',
    );

    daemon.emit({
      event: 'endpoint_action_result',
      action: 'update',
      endpoint_id: 'ep-1',
      success: true,
    });

    await expect(first).resolves.toMatchObject({ success: true, endpoint_id: 'ep-1' });
  });

  it('resolves automation definitions from a correlated automation_definitions_result, ignoring a mismatched request_id', async () => {
    const { api, daemon } = await renderWithDaemon();
    const request = api.current.listAutomationDefinitions();
    const command = await daemon.received('automation_definitions_get');
    expect(command.request_id).toMatch(/^automation_definitions_get:/);

    let resolved = false;
    request.then(() => {
      resolved = true;
    });

    const definition = {
      id: 'd1',
      name: 'PR reviewer',
      enabled: true,
      revision: 1,
      trigger_type: 'manual',
      updated_at: '2026-01-01T00:00:00Z',
    };
    daemon.emit({
      event: 'automation_definitions_result',
      request_id: 'mismatched-request-id',
      success: true,
      definitions: [{ ...definition, id: 'wrong' }],
    });

    await daemon.idle();
    expect(resolved).toBe(false);

    daemon.emit({
      event: 'automation_definitions_result',
      request_id: command.request_id,
      success: true,
      definitions: [definition],
    });

    await expect(request).resolves.toEqual([definition]);
  });

  it('rejects setAutomationEnabled with the daemon error string on failure', async () => {
    const { api, daemon } = await renderWithDaemon();
    daemon.on('automation_set_enabled', () => ({
      event: 'automation_set_enabled_result',
      success: false,
      error: 'automation definition is disabled elsewhere',
    }));

    await expect(api.current.setAutomationEnabled('d1', false)).rejects.toThrow('automation definition is disabled elsewhere');
    expect(await daemon.received('automation_set_enabled')).toMatchObject({ definition_id: 'd1', enabled: false });
  });

  it('resolves runAutomationNow with the run summary', async () => {
    const { api, daemon } = await renderWithDaemon();
    const run = {
      id: 'run-1',
      definition_id: 'd1',
      state: 'delivered',
      seed_id: 's-seed01',
      session_id: 'session-1',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
    daemon.on('automation_run', () => ({ event: 'automation_run_result', success: true, run }));

    await expect(api.current.runAutomationNow('d1', 'req-1')).resolves.toEqual(run);
    expect(await daemon.received('automation_run')).toMatchObject({ definition_id: 'd1', request_id: 'req-1' });
  });

  it('rejects runAutomationNow with AutomationActionTimeoutError when no result arrives within 30s', async () => {
    const { api } = await renderWithDaemon();
    let caught: unknown;
    const request = api.current.runAutomationNow('d1', 'req-timeout').catch((err) => {
      caught = err;
    });

    await vi.advanceTimersByTimeAsync(30000);
    await request;

    expect(caught).toBeInstanceOf(AutomationActionTimeoutError);
  });

  it('leaves attachment to the mounted pane after spawning a daemon-known runtime', async () => {
    const { daemon } = await renderWithDaemon(null, {
      initialState: {
        workspaces: [workspace({
          id: 'workspace-sess-remote',
          title: 'Remote',
          layout: {
            workspace_id: 'workspace-sess-remote',
            active_pane_id: 'pane-session',
            layout_json: '',
            panes: [pane({
              workspace_id: 'workspace-sess-remote',
              pane_id: 'pane-shell-1',
              runtime_id: 'runtime-shell-1',
              title: 'Shell 1',
            })],
          },
        })],
      },
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'runtime-shell-1',
        cwd: '/tmp/repo',
        workspace_id: 'workspace-sess-remote',
        endpoint_id: 'ep-remote',
        cols: 80,
        rows: 24,
        shell: true,
      },
    });

    await daemon.received('spawn_session', (command) => command.id === 'runtime-shell-1');

    daemon.emit({ event: 'spawn_result', id: 'runtime-shell-1', success: true });

    await expect(spawnPromise).resolves.toBeUndefined();
    expect(daemon.sent.filter((message) =>
      message.cmd === 'attach_session' || message.cmd === 'pty_resize',
    )).toEqual([]);
  });

  it('ignores an attach result after the runtime was detached', async () => {
    const snapshot = initialState({
      sessions: [session({ id: 'sess-canceled', label: 'Canceled', agent: 'codex', state: 'working' })],
    });
    const { daemon } = await renderWithDaemon(null, { initialState: snapshot });

    const attachPromise = ptyAttach({
      args: {
        id: 'sess-canceled',
        cols: 120,
        rows: 40,
        agent: 'codex',
        policy: 'same_app_remount',
      },
    });
    expect(await daemon.received('attach_session')).toEqual({
      cmd: 'attach_session',
      id: 'sess-canceled',
      attach_policy: 'same_app_remount',
    });

    await ptyDetach({ id: 'sess-canceled' });
    daemon.emit({
      event: 'attach_result',
      id: 'sess-canceled',
      success: true,
      cols: 120,
      rows: 40,
      running: true,
    });

    await expect(attachPromise).rejects.toThrow('Attach session canceled');

    daemon.emit(snapshot);

    const attachCommands = daemon.sent
      .filter((entry) => entry.cmd === 'attach_session' && entry.id === 'sess-canceled');
    expect(attachCommands).toHaveLength(1);
  });

  it('sends measured geometry only with a revive attach policy', async () => {
    const { daemon } = await renderWithDaemon(null, {
      initialState: {
        sessions: [session({ id: 'sess-recoverable', label: 'Recoverable', state: 'recoverable' })],
      },
    });

    const attachPromise = ptyAttach({
      args: {
        id: 'sess-recoverable',
        cols: 113,
        rows: 37,
        agent: 'claude',
        policy: 'revive',
      },
    });
    expect(await daemon.received('attach_session')).toEqual({
      cmd: 'attach_session',
      id: 'sess-recoverable',
      attach_policy: 'revive',
      cols: 113,
      rows: 37,
    });

    daemon.emit({
      event: 'attach_result',
      id: 'sess-recoverable',
      success: true,
      cols: 113,
      rows: 37,
      running: true,
      revived: true,
    });

    await expect(attachPromise).resolves.toBeUndefined();
  });

  it('includes the owning workspace when spawning a new agent session', async () => {
    const { daemon } = await renderWithDaemon();
    const spawnPromise = ptySpawn({
      args: {
        id: 'sess-new',
        cwd: '/tmp/repo',
        workspace_id: 'workspace-sess-new',
        agent: 'claude',
        cols: 80,
        rows: 24,
      },
    });

    expect(daemon.sent).toContainEqual({
      cmd: 'spawn_session',
      id: 'sess-new',
      cwd: '/tmp/repo',
      workspace_id: 'workspace-sess-new',
      agent: 'claude',
      cols: 80,
      rows: 24,
    });

    daemon.emit({ event: 'spawn_result', id: 'sess-new', success: true });

    await expect(spawnPromise).resolves.toBeUndefined();
    expect(daemon.sent.filter((message) =>
      message.cmd === 'attach_session' || message.cmd === 'pty_resize',
    )).toEqual([]);
  });

  it('resolves a pane close only on its own workspace action result', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });
    const closePromise = api.current.sendWorkspaceClosePane('workspace-1', 'pane-1');
    daemon.emit(initialState());

    expect(daemon.sent).toContainEqual({
      cmd: 'workspace_layout_close_pane',
      workspace_id: 'workspace-1',
      pane_id: 'pane-1',
    });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_close_pane',
      workspace_id: 'workspace-2',
      pane_id: 'pane-1',
      success: true,
    });

    const resultMarker = vi.fn();
    closePromise.then(resultMarker, resultMarker);
    await daemon.idle();
    expect(resultMarker).not.toHaveBeenCalled();

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_close_pane',
      workspace_id: 'workspace-1',
      pane_id: 'pane-1',
      success: true,
    });

    await expect(closePromise).resolves.toEqual({ success: true });
  });

  it('sends set_workspace_rank with neighbour ids and resolves on the action result', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });
    const rankPromise = api.current.sendSetWorkspaceRank('workspace-1', 'workspace-0', 'workspace-2');
    daemon.emit(initialState());

    expect(daemon.sent).toContainEqual({
      cmd: 'set_workspace_rank',
      workspace_id: 'workspace-1',
      prev_workspace_id: 'workspace-0',
      next_workspace_id: 'workspace-2',
    });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'set_workspace_rank',
      workspace_id: 'workspace-1',
      success: true,
    });

    await expect(rankPromise).resolves.toEqual({ success: true });
  });

  it('omits empty neighbour ids when moving a workspace to an edge', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });
    const rankPromise = api.current.sendSetWorkspaceRank('workspace-1', undefined, 'workspace-2');
    daemon.emit(initialState());

    expect(daemon.sent).toContainEqual({
      cmd: 'set_workspace_rank',
      workspace_id: 'workspace-1',
      next_workspace_id: 'workspace-2',
    });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'set_workspace_rank',
      workspace_id: 'workspace-1',
      success: false,
      error: 'rank persist failed',
    });

    await expect(rankPromise).rejects.toThrow('rank persist failed');
  });

  it('sends move_leaf_to_new_workspace and resolves on the leaf-keyed action result', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });
    const movePromise = api.current.sendWorkspaceMoveLeafToNewWorkspace('workspace-1', 'pane-7');
    daemon.emit(initialState());

    expect(daemon.sent).toContainEqual({
      cmd: 'workspace_layout_move_leaf_to_new_workspace',
      source_workspace_id: 'workspace-1',
      leaf_id: 'pane-7',
      anchor_id: '',
      edge: 'left',
    });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_move_leaf_to_new_workspace',
      workspace_id: 'workspace-2',
      leaf_id: 'pane-7',
      success: true,
    });

    const marker = vi.fn();
    movePromise.then(marker, marker);
    await daemon.idle();
    expect(marker).not.toHaveBeenCalled();

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_move_leaf_to_new_workspace',
      workspace_id: 'workspace-1',
      leaf_id: 'pane-7',
      success: true,
    });

    await expect(movePromise).resolves.toEqual({ success: true, final_leaf_id: undefined });
  });

  it('correlates concurrent split resize results by split id', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });
    api.current.sendSessionSelected('session-selected');
    api.current.sendWorkspaceSelected('workspace-selected');

    const first = api.current.sendWorkspaceSetSplitRatio('workspace-1', 'split-a', 0.3);
    const second = api.current.sendWorkspaceSetSplitRatio('workspace-1', 'split-b', 0.7);
    daemon.emit(initialState());

    expect(daemon.sent).toContainEqual({ cmd: 'session_selected', id: 'session-selected' });
    expect(daemon.sent).toContainEqual({ cmd: 'workspace_selected', workspace_id: 'workspace-selected' });
    const splitARequest = await daemon.received('workspace_layout_set_split_ratio', (command) => command.split_id === 'split-a');
    const splitBRequest = await daemon.received('workspace_layout_set_split_ratio', (command) => command.split_id === 'split-b');
    expect(splitARequest).toMatchObject({ workspace_id: 'workspace-1', ratio: 0.3 });
    expect(splitBRequest).toMatchObject({ workspace_id: 'workspace-1', ratio: 0.7 });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_set_split_ratio',
      workspace_id: 'workspace-1',
      split_id: 'split-a',
      request_id: splitARequest.request_id,
      success: true,
    });

    await expect(first).resolves.toEqual({ success: true });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_set_split_ratio',
      workspace_id: 'workspace-1',
      split_id: 'split-b',
      request_id: splitBRequest.request_id,
      success: false,
      error: 'persist failed',
    });

    await expect(second).rejects.toThrow('persist failed');
  });

  it('drops terminal pointer activity until the daemon is ready', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });

    api.current.sendTerminalPointerActivity('session-pointer');
    expect(daemon.sent).not.toContainEqual({
      cmd: 'terminal_pointer_activity',
      id: 'session-pointer',
    });

    daemon.emit(initialState());

    expect(daemon.sent).not.toContainEqual({
      cmd: 'terminal_pointer_activity',
      id: 'session-pointer',
    });

    act(() => {
      api.current.sendTerminalPointerActivity('session-pointer');
    });
    expect(daemon.sent).toContainEqual({
      cmd: 'terminal_pointer_activity',
      id: 'session-pointer',
    });
  });

  it('correlates overlapping resize results for the same split by request id', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });
    const first = api.current.sendWorkspaceSetSplitRatio('workspace-1', 'split-a', 0.3);
    const second = api.current.sendWorkspaceSetSplitRatio('workspace-1', 'split-a', 0.7);
    daemon.emit(initialState());

    const sent = daemon.sent;
    expect(sent.filter((entry) => entry.cmd === 'workspace_layout_set_split_ratio')).toHaveLength(2);

    const [firstRequest, secondRequest] = daemon.sent
      .filter((entry) => entry.cmd === 'workspace_layout_set_split_ratio');
    expect(firstRequest.request_id).not.toBe(secondRequest.request_id);

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_set_split_ratio',
      workspace_id: 'workspace-1',
      split_id: 'split-a',
      request_id: secondRequest.request_id,
      success: true,
    });

    await expect(second).resolves.toEqual({ success: true });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_set_split_ratio',
      workspace_id: 'workspace-1',
      split_id: 'split-a',
      request_id: firstRequest.request_id,
      success: false,
      error: 'first persist failed',
    });

    await expect(first).rejects.toThrow('first persist failed');
  });

  it('correlates overlapping tile updates by request id', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });
    const first = api.current.sendWorkspaceUpdateTile('workspace-1', 'tile-browser', 'https://first.example');
    const second = api.current.sendWorkspaceUpdateTile('workspace-1', 'tile-browser', 'https://second.example');
    daemon.emit(initialState());

    const [firstRequest, secondRequest] = daemon.sent.filter((entry) => entry.cmd === 'workspace_layout_update_tile');
    expect(firstRequest.request_id).not.toBe(secondRequest.request_id);

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_update_tile',
      workspace_id: 'workspace-1',
      tile_id: 'tile-browser',
      request_id: secondRequest.request_id,
      success: true,
    });

    await expect(second).resolves.toEqual({ success: true });

    daemon.emit({
      event: 'workspace_layout_action_result',
      action: 'workspace_layout_update_tile',
      workspace_id: 'workspace-1',
      tile_id: 'tile-browser',
      request_id: firstRequest.request_id,
      success: false,
      error: 'first persist failed',
    });

    await expect(first).rejects.toThrow('first persist failed');
  });

  it('prunes cached tile content when its layout leaf or workspace disappears', async () => {
    const markdownTileWorkspace = workspace({
      id: 'workspace-1',
      title: 'one',
      directory: '/tmp/one',
      layout: {
        workspace_id: 'workspace-1',
        active_pane_id: 'pane-1',
        layout_json: MARKDOWN_TILE_LAYOUT,
        panes: [],
      },
    });
    const { api, daemon } = await renderWithDaemon(null, { initialState: { workspaces: [markdownTileWorkspace] } });
    daemon.emit({
      event: 'workspace_tile_content',
      workspace_id: 'workspace-1',
      tile_id: 'tile-md',
      tile_kind: 'markdown',
      path: '/tmp/notes.md',
      content: '# Notes',
    });

    expect(api.current.tileContents['workspace-1::tile-md']?.content).toBe('# Notes');

    daemon.emit({
      event: 'workspace_layout_updated',
      workspace_layout: {
        workspace_id: 'workspace-1',
        active_pane_id: 'pane-1',
        layout_json: '{"type":"pane","pane_id":"pane-1"}',
        panes: [],
      },
    });

    expect(api.current.tileContents).toEqual({});

    daemon.emit({
      event: 'workspace_tile_content',
      workspace_id: 'workspace-1',
      tile_id: 'tile-md',
      tile_kind: 'markdown',
      path: '/tmp/notes.md',
      content: '# Notes',
    });
    daemon.emit({ event: 'workspace_unregistered', workspace: workspace({ id: 'workspace-1', title: 'one', directory: '/tmp/one' }) });

    expect(api.current.tileContents).toEqual({});
  });

  it('refetches persisted tile content after websocket reconnect', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });
    const snapshot = initialState({
      workspaces: [workspace({
        id: 'workspace-1',
        title: 'one',
        directory: '/tmp/one',
        layout: {
          workspace_id: 'workspace-1',
          active_pane_id: 'pane-1',
          layout_json: MARKDOWN_TILE_LAYOUT,
          panes: [],
        },
      })],
    });
    api.current.sendSessionSelected('session-selected');
    expect(daemon.sent).toContainEqual({ cmd: 'session_selected', id: 'session-selected' });

    daemon.emit(snapshot);
    daemon.emit({
      event: 'workspace_tile_content',
      workspace_id: 'workspace-1',
      tile_id: 'tile-md',
      tile_kind: 'markdown',
      path: '/tmp/notes.md',
      content: '# Before reconnect',
    });

    expect(api.current.tileContents['workspace-1::tile-md']?.content).toBe('# Before reconnect');

    const reconnected = await daemon.reconnect();

    reconnected.emit(snapshot);
    expect(reconnected.sent).toContainEqual({ cmd: 'session_selected', id: 'session-selected' });
    expect(reconnected.sent).toContainEqual({
      cmd: 'workspace_tile_content_get',
      workspace_id: 'workspace-1',
      tile_id: 'tile-md',
    });
  });

  it('hydrates a remounted runtime by resizing before re-attaching', async () => {
    const { daemon } = await renderWithDaemon(null, {
      initialState: { sessions: [session({ id: 'sess-existing', label: 'attn', agent: 'codex' })] },
    });

    const attachPromise = ptyAttach({
      args: {
        id: 'sess-existing',
        cols: 58,
        rows: 46,
        shell: false,
        reason: 'remount_attach',
        policy: 'same_app_remount',
        xpixel: 1044,
        ypixel: 1932,
      },
      forceResizeBeforeAttach: true,
    });

    await daemon.received('attach_session');
    const sent = daemon.sent;
    expect(sent).toContainEqual({ cmd: 'pty_resize', id: 'sess-existing', cols: 58, rows: 46, xpixel: 1044, ypixel: 1932 });
    expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'same_app_remount' });
    const resizeIndex = sent.findIndex((entry) => entry.cmd === 'pty_resize' && entry.id === 'sess-existing');
    const attachIndex = sent.findIndex((entry) => entry.cmd === 'attach_session' && entry.id === 'sess-existing');
    expect(resizeIndex).toBeGreaterThanOrEqual(0);
    expect(attachIndex).toBeGreaterThan(resizeIndex);

    daemon.emit({
      event: 'attach_result',
      id: 'sess-existing',
      success: true,
      cols: 58,
      rows: 46,
      running: true,
    });

    await expect(attachPromise).resolves.toBeUndefined();
  });

  it('replays a fresh same-app snapshot when the mounted pane geometry differs', async () => {
    (window as Window & {
      __TEST_PTY_EVENTS?: Array<{
        event: string;
        id: string;
        data?: string;
        cols?: number;
        rows?: number;
        source?: string;
        reason?: string;
      }>;
    }).__TEST_PTY_EVENTS = [];
    const { daemon } = await renderWithDaemon(null, {
      initialState: {
        sessions: [session({ id: 'sess-existing', label: 'thunk', agent: 'codex' })],
      },
    });

    const attachPromise = ptyAttach({
      args: {
        id: 'sess-existing',
        cols: 52,
        rows: 35,
        shell: false,
        agent: 'codex',
        policy: 'fresh_spawn',
      },
    });

    expect(await daemon.received('attach_session')).toEqual({
      cmd: 'attach_session',
      id: 'sess-existing',
      attach_policy: 'same_app_remount',
    });

    daemon.emit({
      event: 'pty_output',
      id: 'sess-existing',
      seq: 29377,
      data: btoa('live-after-snapshot'),
    });

    daemon.emit({
      event: 'attach_result',
      id: 'sess-existing',
      success: true,
      cols: 56,
      rows: 35,
      snapshot: {
        cols: 56,
        rows: 35,
        snapshot_b64: btoa('fresh-daemon-frame'),
        format: LOCAL_SNAPSHOT_FORMAT,
        scrollback_truncated: false,
      },
      last_seq: 29376,
      running: true,
    });

    await expect(attachPromise).resolves.toBeUndefined();
    const ptyEvents = (window as Window & {
      __TEST_PTY_EVENTS?: Array<{
        event: string;
        id: string;
        data?: string;
        cols?: number;
        rows?: number;
        source?: string;
        reason?: string;
      }>;
    }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents).toContainEqual({
      event: 'local_resize',
      id: 'sess-existing',
      cols: 56,
      rows: 35,
      source: 'attach_restore',
    });
    expect(ptyEvents).toContainEqual({
      event: 'reset',
      id: 'sess-existing',
      reason: 'snapshot_restore',
    });
    expect(ptyEvents).toContainEqual({
      event: 'restore_snapshot',
      id: 'sess-existing',
      data: btoa('fresh-daemon-frame'),
    });
    expect(ptyEvents).toContainEqual({ event: 'restore_complete', id: 'sess-existing' });
    const snapshotIndex = ptyEvents.findIndex((event) => event.event === 'restore_snapshot');
    const queuedLiveIndex = ptyEvents.findIndex((event) => (
      event.event === 'data' && event.data === btoa('live-after-snapshot')
    ));
    const restoreCompleteIndex = ptyEvents.findIndex((event) => event.event === 'restore_complete');
    expect(snapshotIndex).toBeGreaterThanOrEqual(0);
    expect(queuedLiveIndex).toBeGreaterThan(snapshotIndex);
    expect(restoreCompleteIndex).toBeGreaterThan(queuedLiveIndex);
  });

  it('resolves a chief-of-staff update from its result event', async () => {
    const { api, daemon } = await renderWithDaemon();
    daemon.on('set_chief_of_staff', () => ({
      event: 'chief_of_staff_result',
      session_id: 'session-1',
      chief_of_staff: true,
      success: true,
    }));

    await expect(api.current.sendSetChiefOfStaff('session-1', true)).resolves.toBeUndefined();
    expect(await daemon.received('set_chief_of_staff')).toEqual({
      cmd: 'set_chief_of_staff',
      session_id: 'session-1',
      chief_of_staff: true,
    });
  });

  it('queues sendSetTerminalTheme until initial_state, then flushes it fire-and-forget', async () => {
    const { api, daemon } = await renderWithDaemon(null, { initialState: false });

    act(() => {
      api.current.sendSetTerminalTheme({
        foreground: '#d4d4d4', background: '#1e1e1e', cursor: '#d4d4d4',
        ansi_palette: Array(16).fill('#000000'),
      });
    });
    expect(daemon.sent).not.toContainEqual(
      expect.objectContaining({ cmd: 'set_terminal_theme' }),
    );

    daemon.emit(initialState());

    expect(daemon.sent).toContainEqual({
      cmd: 'set_terminal_theme',
      foreground: '#d4d4d4',
      background: '#1e1e1e',
      cursor: '#d4d4d4',
      ansi_palette: Array(16).fill('#000000'),
    });
  });

  it('re-pushes the last terminal theme on reconnect without a new sendSetTerminalTheme call', async () => {
    const { api, daemon } = await renderWithDaemon();

    act(() => {
      api.current.sendSetTerminalTheme({
        foreground: '#d4d4d4', background: '#1e1e1e', cursor: '#d4d4d4',
        ansi_palette: Array(16).fill('#000000'),
      });
    });
    expect(daemon.sent).toContainEqual({
      cmd: 'set_terminal_theme',
      foreground: '#d4d4d4',
      background: '#1e1e1e',
      cursor: '#d4d4d4',
      ansi_palette: Array(16).fill('#000000'),
    });

    const reconnected = await daemon.reconnect();

    expect(reconnected.sent).toContainEqual({
      cmd: 'set_terminal_theme',
      foreground: '#d4d4d4',
      background: '#1e1e1e',
      cursor: '#d4d4d4',
      ansi_palette: Array(16).fill('#000000'),
    });
  });
});

describe('useDaemonSocket fs surface', () => {
  it('sends fs_list and resolves entries, converting is_dir to isDir', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendFsList('knowledge');
    const sent = await daemon.received('fs_list');
    expect(sent.cmd).toBe('fs_list');
    expect(sent.path).toBe('knowledge');

    daemon.emit({
      event: 'fs_list_result',
      request_id: sent.request_id,
      success: true,
      entries: [
        { path: 'knowledge/areas', name: 'areas', is_dir: true, size: 0 },
        { path: 'knowledge/index.md', name: 'index.md', is_dir: false, size: 12, modified: '2026-06-20T00:00:00Z' },
      ],
    });

    await expect(promise).resolves.toEqual([
      { path: 'knowledge/areas', name: 'areas', isDir: true, size: 0, modified: undefined },
      { path: 'knowledge/index.md', name: 'index.md', isDir: false, size: 12, modified: '2026-06-20T00:00:00Z' },
    ]);
  });

  it('omits path from fs_list when listing the root', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendFsList();
    const sent = await daemon.received('fs_list');
    expect(sent.cmd).toBe('fs_list');
    expect('path' in sent).toBe(false);

    daemon.emit({ event: 'fs_list_result', request_id: sent.request_id, success: true, entries: [] });
    await expect(promise).resolves.toEqual([]);
  });

  it('resolves fs_read with the file content and hash', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendFsRead('notes/todo.txt');
    const sent = await daemon.received('fs_read');
    expect(sent).toMatchObject({ cmd: 'fs_read', path: 'notes/todo.txt' });

    daemon.emit({
      event: 'fs_read_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'notes/todo.txt', content: 'buy milk', hash: 'h1' },
    });
    await expect(promise).resolves.toEqual({ path: 'notes/todo.txt', content: 'buy milk', hash: 'h1' });
  });

  it('rejects fs_read on a failed result', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendFsRead('gone.txt');
    const sent = await daemon.received('fs_read');
    daemon.emit({ event: 'fs_read_result', request_id: sent.request_id, success: false, error: 'fsdoc: gone.txt not found' });

    await expect(promise).rejects.toThrow(/not found/);
  });

  it('resolves fs_write, mapping current_hash to currentHash on a conflict', async () => {
    const { api, daemon } = await renderWithDaemon();

    const ok = api.current.sendFsWrite('a.txt', 'v1');
    let sent = await daemon.received('fs_write');
    expect(sent).toMatchObject({ cmd: 'fs_write', path: 'a.txt', content: 'v1' });
    expect('base_hash' in sent).toBe(false);
    daemon.emit({
      event: 'fs_write_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'a.txt', hash: 'h2', conflict: false },
    });
    await expect(ok).resolves.toEqual({ path: 'a.txt', hash: 'h2', conflict: false, currentHash: undefined });

    const conflicting = api.current.sendFsWrite('a.txt', 'v2', 'deadbeef');
    sent = await daemon.received('fs_write', ({ content }) => content === 'v2');
    expect(sent.base_hash).toBe('deadbeef');
    daemon.emit({
      event: 'fs_write_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'a.txt', conflict: true, current_hash: 'h2' },
    });
    await expect(conflicting).resolves.toMatchObject({ conflict: true, currentHash: 'h2' });
  });

  it('sends and resolves fs rename and delete actions', async () => {
    const { api, daemon } = await renderWithDaemon();

    const rename = api.current.sendFsRename('tickets/tk/plan.md', 'tickets/tk/implementation.md');
    const renameSent = await daemon.received('fs_rename');
    expect(renameSent).toMatchObject({ cmd: 'fs_rename', path: 'tickets/tk/plan.md', new_path: 'tickets/tk/implementation.md' });
    daemon.emit({ event: 'fs_rename_result', request_id: renameSent.request_id, success: true, result: { path: renameSent.path, new_path: renameSent.new_path } });
    await expect(rename).resolves.toEqual({ path: 'tickets/tk/plan.md', new_path: 'tickets/tk/implementation.md' });

    const deletion = api.current.sendFsDelete('tickets/tk/implementation.md');
    const deleteSent = await daemon.received('fs_delete');
    expect(deleteSent).toMatchObject({ cmd: 'fs_delete', path: 'tickets/tk/implementation.md' });
    daemon.emit({ event: 'fs_delete_result', request_id: deleteSent.request_id, success: true, result: { path: deleteSent.path } });
    await expect(deletion).resolves.toEqual({ path: 'tickets/tk/implementation.md' });
  });

});

describe('useDaemonSocket seed resume request/result', () => {
  it('resolves sendSeedResume with the session to focus on a successful result', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSeedResume('s-1');
    const sent = await daemon.received('seed_resume');
    expect(sent.cmd).toBe('seed_resume');
    expect(sent.seed_id).toBe('s-1');

    daemon.emit({
      event: 'seed_resume_result',
      request_id: sent.request_id,
      success: true,
      session_id: 'sess-1',
      workspace_id: 'workspace-sess-1',
    });
    await expect(promise).resolves.toEqual({
      sessionId: 'sess-1',
      workspaceId: 'workspace-sess-1',
      alreadyRunning: false,
    });
  });

  it('sends a guarded Handover and resolves its delegated session', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSeedHandover({
      seedId: 's-1',
      requestId: 'stable-handover-request',
      cwd: '/tmp/placed',
      checkout: { kind: 'reuse', branch: 'feature' },
      sourceSessionId: 'sess-chief',
      handoff: 'Continue from the parser tests.',
    });
    const sent = await daemon.received('delegate');
    expect(sent).toMatchObject({
      cmd: 'delegate',
      request_id: 'stable-handover-request',
      source_session_id: 'sess-chief',
      assignment: { kind: 'seed', seed_id: 's-1', handover: { note: 'Continue from the parser tests.' } },
      cwd: '/tmp/placed',
      checkout: { kind: 'reuse', branch: 'feature' },
    });

    daemon.emit({
      event: 'delegate_result',
      request_id: sent.request_id,
      success: true,
      result: {
        session_id: 'sess-new',
        workspace_id: 'workspace-new',
        directory: '/tmp/placed',
        agent: 'claude',
        checkout: 'reuse',
        effort: 'high',
        model: 'opus',
        seed_id: 's-1',
      },
    });
    await expect(promise).resolves.toMatchObject({ session_id: 'sess-new' });
  });

  it('resolves sendSeedResume with alreadyRunning when the tender was still tracked', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSeedResume('s-1');
    const sent = await daemon.received('seed_resume');

    daemon.emit({
      event: 'seed_resume_result',
      request_id: sent.request_id,
      success: true,
      session_id: 'sess-1',
      already_running: true,
    });
    await expect(promise).resolves.toMatchObject({ sessionId: 'sess-1', alreadyRunning: true });
  });

  it('rejects sendSeedResume when seed_resume_result reports failure', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSeedResume('missing');
    const sent = await daemon.received('seed_resume');
    expect(sent.cmd).toBe('seed_resume');

    daemon.emit({
      event: 'seed_resume_result',
      request_id: sent.request_id,
      success: false,
      error: 'seed has no agent session to reopen',
    });
    await expect(promise).rejects.toThrow('seed has no agent session to reopen');
  });
});

describe('useDaemonSocket notebook and annotation events', () => {
  it('routes ledger updates only while the ledger is subscribed', async () => {
    const { api, daemon } = await renderWithDaemon();
    const listener = vi.fn();
    const unsubscribe = api.current.subscribeSessionLedger(listener);
    const entry = { ...session({ id: 's1' }), closed_at: '2026-09-05T10:00:00Z' };

    expect(listener).toHaveBeenCalledWith({ type: 'connection', connected: true, connectionGeneration: 1 });

    daemon.emit({ event: 'session_closed', session_ledger_entry: entry });
    expect(listener).toHaveBeenLastCalledWith({
      type: 'closed',
      entry,
      connectionGeneration: 1,
    });

    unsubscribe();
    daemon.emit({ event: 'session_closed', session_ledger_entry: entry });
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it('resolves session_messages_get with the annotatable window', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionMessagesGet('session-1');
    const sent = await daemon.received('session_messages_get');
    expect(sent).toMatchObject({ cmd: 'session_messages_get', session_id: 'session-1' });

    daemon.emit({
      event: 'session_messages_get_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      status: 'ready',
      messages: [
        { key: 'turn-1', markdown: 'The first answer.' },
        { key: 'turn-2', markdown: 'The second answer.' },
      ],
      truncated: true,
    });
    await expect(promise).resolves.toEqual({
      messages: [
        { key: 'turn-1', markdown: 'The first answer.' },
        { key: 'turn-2', markdown: 'The second answer.' },
      ],
      status: 'ready',
      detail: undefined,
      truncated: true,
    });
  });

  it('resolves session_messages_get with an empty window rather than rejecting', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionMessagesGet('session-1');
    const { request_id } = await daemon.received('session_messages_get');
    daemon.emit({
      event: 'session_messages_get_result',
      request_id,
      session_id: 'session-1',
      success: true,
      status: 'ready',
      messages: [],
      truncated: false,
    });

    await expect(promise).resolves.toEqual({ messages: [], status: 'ready', detail: undefined, truncated: false });
  });

  it('routes message-window invalidations only to that session’s subscribers', async () => {
    const { api, daemon } = await renderWithDaemon();
    const first = vi.fn();
    const second = vi.fn();
    const unsubscribe = api.current.subscribeSessionMessagesChanged('session-1', first);
    api.current.subscribeSessionMessagesChanged('session-2', second);

    daemon.emit({ event: 'session_messages_changed', session_id: 'session-1' });
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).not.toHaveBeenCalled();

    unsubscribe();
    daemon.emit({ event: 'session_messages_changed', session_id: 'session-1' });
    expect(first).toHaveBeenCalledTimes(1);
  });

  it('revalidates subscribed message windows when the socket reconnects', async () => {
    const { api, daemon } = await renderWithDaemon();
    const listener = vi.fn();
    api.current.subscribeSessionMessagesChanged('session-1', listener);
    const original = daemon.connection;

    expect(await daemon.reconnect()).not.toBe(original);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it('rejects session_messages_get when the daemon reports a failure', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionMessagesGet('session-1');
    const { request_id } = await daemon.received('session_messages_get');
    daemon.emit({
      event: 'session_messages_get_result',
      request_id,
      session_id: 'session-1',
      success: false,
      status: 'unavailable',
      messages: [],
      truncated: false,
      error: 'no transcript for session session-1',
    });

    await expect(promise).rejects.toThrow('no transcript for session session-1');
  });

  it('decodes an emoji-only old-format session annotation to its quick-label id', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsGet('session-1');
    const sent = await daemon.received('session_annotations_get');
    expect(sent).toMatchObject({ cmd: 'session_annotations_get', session_id: 'session-1' });

    daemon.emit({
      event: 'session_annotations_get_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      annotations: [{
        id: 'anno-1',
        message_key: 'turn-1',
        start: 4,
        end: 10,
        quote: 'parser',
        emoji: '❓',
        comment: 'why this?',
      }],
      generation: 7,
    });

    await expect(promise).resolves.toEqual({
      annotations: [{
        id: 'anno-1',
        messageKey: 'turn-1',
        start: 4,
        end: 10,
        quote: 'parser',
        quickLabelId: 'clarify-this',
        comment: 'why this?',
      }],
      note: '',
      generation: 7,
    });
  });

  it('carries the draft note back with the marks', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsGet('session-1');
    const sent = await daemon.received('session_annotations_get');

    daemon.emit({
      event: 'session_annotations_get_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      annotations: [],
      note: 'Split this into two PRs.',
      generation: 3,
    });

    await expect(promise).resolves.toEqual({
      annotations: [],
      note: 'Split this into two PRs.',
      generation: 3,
    });
  });

  it('sends a save as the whole list under its generation', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsSave('session-1', [{
      id: 'anno-1',
      messageKey: 'turn-1',
      start: 4,
      end: 10,
      quote: 'parser',
      quickLabelId: 'clarify-this',
      comment: 'why this?',
    }], 'and land the retry wrapper as is', 3);
    const sent = await daemon.received('session_annotations_save');
    expect(sent).toMatchObject({
      cmd: 'session_annotations_save',
      session_id: 'session-1',
      note: 'and land the retry wrapper as is',
      generation: 3,
      annotations: [{
        id: 'anno-1',
        message_key: 'turn-1',
        start: 4,
        end: 10,
        quote: 'parser',
        quick_label_id: 'clarify-this',
        comment: 'why this?',
      }],
    });

    daemon.emit({
      event: 'session_annotations_save_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      generation: 3,
    });
    await expect(promise).resolves.toEqual({ stale: false });
  });

  it('resolves a stale save rather than rejecting it', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsSave('session-1', [], '', 2);
    const { request_id } = await daemon.received('session_annotations_save');
    daemon.emit({
      event: 'session_annotations_save_result',
      request_id,
      session_id: 'session-1',
      success: false,
      stale: true,
      generation: 9,
    });

    await expect(promise).resolves.toEqual({ stale: true });
  });

  it('rejects a save the daemon failed for any other reason', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsSave('session-1', [], '', 2);
    const { request_id } = await daemon.received('session_annotations_save');
    daemon.emit({
      event: 'session_annotations_save_result',
      request_id,
      session_id: 'session-1',
      success: false,
      error: 'database is locked',
      generation: 0,
    });

    await expect(promise).rejects.toThrow('database is locked');
  });

  it('resolves session_annotations_clear with the tombstone the daemon settled on', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsClear('session-1', 5);
    const sent = await daemon.received('session_annotations_clear');
    expect(sent).toMatchObject({
      cmd: 'session_annotations_clear',
      session_id: 'session-1',
      generation: 5,
    });

    daemon.emit({
      event: 'session_annotations_clear_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      generation: 5,
    });
    await expect(promise).resolves.toEqual({ generation: 5 });
  });

  it('resolves session_annotations_submit with the delivered status', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsSubmit('session-1', 'Feedback on your last message.');
    const sent = await daemon.received('session_annotations_submit');
    expect(sent).toMatchObject({
      cmd: 'session_annotations_submit',
      session_id: 'session-1',
      text: 'Feedback on your last message.',
    });

    daemon.emit({
      event: 'session_annotations_submit_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      status: 'delivered',
    });
    await expect(promise).resolves.toEqual({ status: 'delivered' });
  });

  it('resolves a submit the daemon refused for a pending approval', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsSubmit('session-1', 'feedback');
    const { request_id } = await daemon.received('session_annotations_submit');
    daemon.emit({
      event: 'session_annotations_submit_result',
      request_id,
      session_id: 'session-1',
      success: false,
      status: 'skipped_pending_approval',
    });

    await expect(promise).resolves.toEqual({ status: 'skipped_pending_approval' });
  });

  it('rejects a submit the daemon failed to deliver', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSessionAnnotationsSubmit('session-1', 'feedback');
    const { request_id } = await daemon.received('session_annotations_submit');
    daemon.emit({
      event: 'session_annotations_submit_result',
      request_id,
      session_id: 'session-1',
      success: false,
      status: 'error',
      error: 'pty write failed',
    });

    await expect(promise).rejects.toThrow('pty write failed');
  });

  it('resolves notebook_read with the daemon result', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendNotebookRead('journal/2026-08-01.md');
    const sent = await daemon.received('notebook_read');
    expect(sent).toMatchObject({ cmd: 'notebook_read', path: 'journal/2026-08-01.md' });

    daemon.emit({
      event: 'notebook_read_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'journal/2026-08-01.md', content: 'today', hash: 'h1' },
    });
    await expect(promise).resolves.toEqual({ path: 'journal/2026-08-01.md', content: 'today', hash: 'h1' });
  });

  it('rejects notebook_read when the daemon reports success without a result', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendNotebookRead('gone.md');
    const { request_id } = await daemon.received('notebook_read');
    daemon.emit({ event: 'notebook_read_result', request_id, success: true });

    await expect(promise).rejects.toThrow('Notebook read failed');
  });

  it('resolves notebook_write on a conflict rather than rejecting', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendNotebookWrite('a.md', 'v2', 'h1');
    const sent = await daemon.received('notebook_write');
    expect(sent).toMatchObject({ cmd: 'notebook_write', path: 'a.md' });

    daemon.emit({
      event: 'notebook_write_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'a.md', hash: 'h2', conflict: true, current_hash: 'h3' },
    });
    await expect(promise).resolves.toEqual({ path: 'a.md', hash: 'h2', conflict: true, currentHash: 'h3' });
  });

  it('resolves notebook_list and notebook_backlinks from their own pending keys', async () => {
    const { api, daemon } = await renderWithDaemon();

    const list = api.current.sendNotebookList('journal');
    const listSent = await daemon.received('notebook_list');
    const backlinks = api.current.sendNotebookBacklinks('journal/a.md');
    const backlinksSent = await daemon.received('notebook_backlinks');
    expect(listSent.cmd).toBe('notebook_list');
    expect(backlinksSent.cmd).toBe('notebook_backlinks');

    daemon.emit({ event: 'notebook_backlinks_result', request_id: backlinksSent.request_id, success: true, entries: [{ path: 'b.md', size: 2 }] });
    daemon.emit({ event: 'notebook_list_result', request_id: listSent.request_id, success: true, entries: [{ path: 'a.md', size: 1 }] });

    await expect(backlinks).resolves.toEqual([{ path: 'b.md', size: 2 }]);
    await expect(list).resolves.toEqual([{ path: 'a.md', size: 1 }]);
  });

  it('treats a stale save as a resolved outcome, not an error', async () => {
    const { api, daemon } = await renderWithDaemon();

    const source = seedMarkdownSource('s-7k3f9m');
    const promise = api.current.saveMarkdownAnnotations(source, [], 3);
    const sent = await daemon.received('markdown_annotations_save');
    expect(sent).toMatchObject({
      document_uri: 'attn://seed/s-7k3f9m',
      source_kind: 'seed',
      seed_id: 's-7k3f9m',
    });

    daemon.emit({
      event: 'markdown_annotations_save_result',
      request_id: sent.request_id,
      document_uri: source.uri,
      source_kind: 'seed',
      generation: 4,
      success: false,
      stale: true,
    });
    await expect(promise).resolves.toEqual({ stale: true });
  });

  it('drops an annotation result whose request was superseded', async () => {
    const { api, daemon } = await renderWithDaemon();

    const source = fileMarkdownSource('ws-1', '/tmp/doc.md');
    const first = api.current.saveMarkdownAnnotations(source, [], 1);
    const firstSent = await daemon.received('markdown_annotations_save');
    const second = api.current.saveMarkdownAnnotations(source, [], 2);
    const secondSent = await daemon.received('markdown_annotations_save', (command) => command !== firstSent);
    expect(firstSent.request_id).not.toBe(secondSent.request_id);
    await expect(first).rejects.toThrow('Superseded by a newer request');

    daemon.emit({
      event: 'markdown_annotations_save_result',
      request_id: firstSent.request_id,
      document_uri: source.uri,
      source_kind: 'file',
      generation: 2,
      success: false,
      stale: true,
    });
    daemon.emit({
      event: 'markdown_annotations_save_result',
      request_id: secondSent.request_id,
      document_uri: source.uri,
      source_kind: 'file',
      generation: 2,
      success: true,
    });
    await expect(second).resolves.toEqual({ stale: false });
  });

  it('shares one in-flight round-trip when the same document is hydrated twice', async () => {
    const { api, daemon } = await renderWithDaemon();
    const annotation = { id: 'a1', type: 'comment', created_at: 1 };

    const source = fileMarkdownSource('ws-1', '/tmp/doc.md');
    const first = api.current.getMarkdownAnnotations(source);
    const sent = await daemon.received('markdown_annotations_get');
    const second = api.current.getMarkdownAnnotations(source);
    await daemon.idle();
    expect(daemon.sentOf('markdown_annotations_get')).toEqual([sent]);

    daemon.emit({
      event: 'markdown_annotations_get_result',
      request_id: sent.request_id,
      document_uri: source.uri,
      source_kind: 'file',
      success: true,
      annotations: [annotation],
      generation: 4,
    });
    await expect(first).resolves.toEqual({ annotations: [annotation], generation: 4 });
    await expect(second).resolves.toEqual({ annotations: [annotation], generation: 4 });
  });

  it('opens a seed tile with typed identity', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendOpenSeed('s-7k3f9m', { sessionId: 'session-1' });
    const sent = await daemon.received('open_seed');
    expect(sent).toMatchObject({ cmd: 'open_seed', seed_id: 's-7k3f9m', session_id: 'session-1' });
    expect(sent).not.toHaveProperty('standalone');

    daemon.emit({
      event: 'open_seed_result',
      request_id: sent.request_id,
      seed_id: 's-7k3f9m',
      success: true,
      workspace_id: 'workspace-1',
      tile_id: 'tile-seed-s-7k3f9m',
    });
    await expect(promise).resolves.toEqual({ workspaceId: 'workspace-1', tileId: 'tile-seed-s-7k3f9m' });
  });

  it('opens a standalone seed reader without naming a session', async () => {
    const { api, daemon } = await renderWithDaemon();

    void api.current.sendOpenSeed('s-7k3f9m', 'standalone');
    const sent = await daemon.received('open_seed');
    expect(sent).toMatchObject({ cmd: 'open_seed', seed_id: 's-7k3f9m', standalone: true });
    expect(sent).not.toHaveProperty('session_id');
  });

  it('resolves typed seed artifact targets by request id', async () => {
    const { api, daemon } = await renderWithDaemon();

    const promise = api.current.sendSeedArtifactTarget('s-7k3f9m', 'cover%20art.png', 'image');
    const sent = await daemon.received('seed_artifact_target');
    expect(sent).toMatchObject({
      cmd: 'seed_artifact_target',
      seed_id: 's-7k3f9m',
      relative_target: 'cover%20art.png',
      purpose: 'image',
    });
    daemon.emit({
      event: 'seed_artifact_target_result',
      request_id: sent.request_id,
      success: true,
      result: { relative_target: 'cover%20art.png', mime_type: 'image/png', data_base64: 'aW1hZ2U=' },
    });
    await expect(promise).resolves.toEqual({
      relative_target: 'cover%20art.png',
      mime_type: 'image/png',
      data_base64: 'aW1hZ2U=',
    });
  });

  it('sends a recoverable transfer with a five minute client timeout', async () => {
    const { api, daemon } = await renderWithDaemon();
    const legacyReference = { kind: 'markdown_file', path: '/tmp/report.pdf' };

    const promise = api.current.sendSeedArtifactTransfer({
      seedId: 's-7k3f9m', operation: 'move', sourcePath: '/tmp/report.pdf', legacyReference,
    });
    const sent = await daemon.received('seed_artifact_transfer');
    expect(sent).toMatchObject({
      seed_id: 's-7k3f9m',
      operation: 'move',
      source_path: '/tmp/report.pdf',
      legacy_reference: legacyReference,
    });
    await vi.advanceTimersByTimeAsync(5 * 60 * 1000 - 1);
    const transfer = {
      operation_id: 'op-1', seed_id: 's-7k3f9m', operation: 'move',
      source_path: '/tmp/report.pdf', destination_path: '/notebook/seeds/s-7k3f9m/report.pdf',
      relative_target: 'report.pdf', recovered: false,
    };
    daemon.emit({ event: 'seed_artifact_transfer_result', request_id: sent.request_id, success: true, result: transfer });
    await expect(promise).resolves.toEqual(transfer);
  });

  it('gives up on a transfer the daemon never answers after five minutes', async () => {
    const { api } = await renderWithDaemon();

    const abandoned = expect(api.current.sendSeedArtifactTransfer({
      seedId: 's-7k3f9m', operation: 'move', sourcePath: '/tmp/report.pdf',
    })).rejects.toThrow('Seed artifact transfer timed out');
    await vi.advanceTimersByTimeAsync(5 * 60 * 1000);

    await abandoned;
  });
});

