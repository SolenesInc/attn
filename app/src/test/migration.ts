import { vi } from 'vitest';
import { ProfileCommandError, type MigrationResult } from '../hooks/daemonProfileEvents';
import { useProfilesStore } from '../store/profiles';
import {
  LayoutPaneKind,
  LayoutPaneStatus,
  MigrationPhase,
  ProfileErrorCode,
  type MigrationDraftDesktop,
  type MigrationGroup,
  type MigrationState,
} from '../types/generated';
import { createMockDaemonApi } from './mocks/daemon';

export const MIGRATION_PROFILE_ID = 'profile-default';

export function importedGroup(
  id: string,
  title: string,
  desktopId: string,
  options: { confirmed?: boolean; status?: LayoutPaneStatus; agents?: number } = {},
): MigrationGroup {
  const count = options.agents ?? 1;
  const paneIds = Array.from({ length: count }, (_, index) => `${id}-p${index + 1}`);
  const tree = paneIds.length === 1
    ? { type: 'pane', pane_id: paneIds[0] }
    : { type: 'split', split_id: `${id}-s`, direction: 'vertical', ratio: 0.5, children: paneIds.map((paneId) => ({ type: 'pane', pane_id: paneId })) };
  return {
    group_id: id,
    title,
    directory: `/work/${id}`,
    source_desktop_id: desktopId,
    confirmed: options.confirmed ?? false,
    tree_json: JSON.stringify(tree),
    panes: paneIds.map((paneId, index) => ({
      desktop_id: desktopId,
      pane_id: paneId,
      kind: LayoutPaneKind.Agent,
      session_id: `${paneId}-session`,
      status: options.status ?? LayoutPaneStatus.Ready,
      title: `${title} agent ${index + 1}`,
    })),
  };
}

export function draftDesktop(key: string, slot: number | null, tree: unknown): MigrationDraftDesktop {
  return {
    key,
    ...(key.startsWith('slot-') ? {} : { desktop_id: key }),
    ...(slot ? { shortcut_slot: slot } : {}),
    tree_json: tree ? JSON.stringify(tree) : '',
  };
}

export function slotsWith(placed: Record<number, [string, unknown]>, extras: Array<[string, unknown]> = []): MigrationDraftDesktop[] {
  const slots = Array.from({ length: 9 }, (_, index) => {
    const slot = index + 1;
    const entry = placed[slot];
    return entry ? draftDesktop(entry[0], slot, entry[1]) : draftDesktop(`slot-${slot}`, slot, null);
  });
  return [...slots, ...extras.map(([key, tree]) => draftDesktop(key, null, tree))];
}

export function migrationState(overrides: Partial<MigrationState> = {}): MigrationState {
  return {
    phase: MigrationPhase.PlacementRequired,
    revision: 3,
    profile_id: MIGRATION_PROFILE_ID,
    can_undo: false,
    suggestion_available: true,
    groups: [
      importedGroup('g1', 'Daemon lifecycle', 'd1', { agents: 2 }),
      importedGroup('g2', 'Garden & crew', 'd2'),
      importedGroup('g3', 'Side project', 'd3'),
    ],
    desktops: slotsWith(
      { 1: ['d1', { group: 'g1' }], 2: ['d2', { group: 'g2' }] },
      [['d3', { group: 'g3' }]],
    ),
    ...overrides,
  };
}

export function confirm(state: MigrationState, ids: string[]): MigrationState {
  return {
    ...state,
    revision: state.revision + 1,
    can_undo: true,
    groups: state.groups.map((group) => (ids.includes(group.group_id) ? { ...group, confirmed: true } : group)),
  };
}

type Reply = MigrationState | ProfileCommandError | Error;
type Handler = (current: MigrationState, args: unknown[]) => Reply;

export function commandError(action: string, code: ProfileErrorCode, error: string): ProfileCommandError {
  return new ProfileCommandError({ action, request_id: 'r', success: false, error, error_code: code });
}

export function fakeMigrationDaemon(initial: MigrationState) {
  let current = initial;
  const handlers = new Map<string, Handler>();
  const respond = (cmd: string, handler: Handler) => handlers.set(cmd, handler);

  const command = (cmd: string, fallback: Handler) =>
    vi.fn(async (...args: unknown[]): Promise<MigrationResult> => {
      const reply = (handlers.get(cmd) ?? fallback)(current, args);
      if (reply instanceof Error) throw reply;
      current = reply;
      useProfilesStore.getState().migrationArrived(reply);
      return { event: 'migration_result' as never, action: cmd, request_id: 'r', success: true, state: reply };
    });

  const api = {
    connectionGeneration: 1,
    sendMigrationGet: command('migration_get', (state) => state),
    sendMigrationKeep: command('migration_keep', (state, args) => confirm(state, args[0] as string[])),
    sendMigrationMove: command('migration_move', () => new Error('no migration_move reply configured')),
    sendMigrationSuggest: command('migration_suggest', () => new Error('no migration_suggest reply configured')),
    sendMigrationUndo: command('migration_undo', () => new Error('no migration_undo reply configured')),
    sendMigrationFinish: command('migration_finish', (state) => ({
      ...state,
      phase: MigrationPhase.Complete,
      revision: state.revision + 1,
      groups: [],
      desktops: [],
      can_undo: false,
      suggestion_available: false,
    })),
  };

  return {
    api,
    daemonApi: (extra: Parameters<typeof createMockDaemonApi>[0] = {}) => createMockDaemonApi({ ...api, ...extra }),
    respond,
    broadcast(next: MigrationState) {
      current = next;
      useProfilesStore.getState().migrationArrived(next);
    },
    get state() {
      return current;
    },
  };
}

export function resetMigrationStore(phase: MigrationPhase | null = MigrationPhase.PlacementRequired) {
  useProfilesStore.setState({
    migrationPhase: phase,
    migration: null,
    profiles: [{ id: MIGRATION_PROFILE_ID, name: 'Default', current_desktop_id: 'd1', revision: 1 }],
  });
}
