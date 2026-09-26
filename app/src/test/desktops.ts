import { vi } from 'vitest';
import { useProfilesStore } from '../store/profiles';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop, type Profile } from '../types/generated';
import {
  buildDesktopViewModels,
  type WorkspaceViewSession,
  type WorkspaceWithSessions,
} from '../utils/workspaceViewModels';

export const TEST_PROFILE_ID = 'profile-test';

export const paneIdOf = (sessionId: string) => `pane-${sessionId}`;

function treeOf(sessionIds: string[]): unknown {
  const [first, ...rest] = sessionIds;
  if (!first) return null;
  const leaf = { type: 'pane', pane_id: paneIdOf(first) };
  if (rest.length === 0) return leaf;
  return {
    type: 'split',
    split_id: `split-${first}`,
    direction: 'vertical',
    ratio: 0.5,
    children: [leaf, treeOf(rest)],
  };
}

export function agentDesktop(id: string, slot: number | null, sessionIds: string[], activeSessionId = sessionIds[0]): Desktop {
  const tree = treeOf(sessionIds);
  return {
    id,
    profile_id: TEST_PROFILE_ID,
    name: '',
    ...(slot ? { shortcut_slot: slot } : {}),
    order_key: slot ? `slot-${slot}` : `extra-${id}`,
    tree_json: tree ? JSON.stringify(tree) : '',
    active_pane_id: activeSessionId ? paneIdOf(activeSessionId) : '',
    revision: 1,
    panes: sessionIds.map((sessionId) => ({
      pane_id: paneIdOf(sessionId),
      desktop_id: id,
      session_id: sessionId,
      kind: LayoutPaneKind.Agent,
      title: sessionId,
      status: LayoutPaneStatus.Ready,
    })),
  };
}

export function arrangeDesktops(desktops: Desktop[], currentDesktopId = desktops[0]?.id ?? '') {
  const profile: Profile = { id: TEST_PROFILE_ID, name: 'Test', current_desktop_id: currentDesktopId, revision: 1 };
  useProfilesStore.getState().profilesChanged([profile]);
  useProfilesStore.getState().arrangementArrived(profile, desktops);
}

function sessionIdsOn(desktop: Desktop): string[] {
  return desktop.panes.map((pane) => pane.session_id);
}

function rearrange(change: (desktops: Desktop[], currentDesktopId: string) => { desktops: Desktop[]; currentDesktopId: string }) {
  const state = useProfilesStore.getState();
  const next = change(state.desktops, state.currentDesktopId ?? '');
  arrangeDesktops(next.desktops, next.currentDesktopId);
}

const ok = (action: string) => ({ event: 'profile_action_result', request_id: 'test', action, success: true });

export function fakeDesktopCommands() {
  return {
    sendProfileSelect: vi.fn(async () => ok('profile_select')),
    sendDesktopSetCurrent: vi.fn(async (_profileId: string, desktopId: string) => {
      rearrange((desktops) => ({ desktops, currentDesktopId: desktopId }));
      return ok('desktop_set_current');
    }),
    sendDesktopSetActivePane: vi.fn(async (desktopId: string, paneId: string) => {
      rearrange((desktops, currentDesktopId) => ({
        desktops: desktops.map((desktop) =>
          desktop.id === desktopId ? { ...desktop, active_pane_id: paneId, revision: desktop.revision + 1 } : desktop,
        ),
        currentDesktopId,
      }));
      return ok('desktop_set_active_pane');
    }),
    sendDesktopPlaceSession: vi.fn(async ({ desktopId, sessionId }: { desktopId: string; sessionId: string }) => {
      rearrange((desktops, currentDesktopId) => ({
        desktops: desktops.map((desktop) => {
          if (desktop.id !== desktopId) return desktop;
          const placed = agentDesktop(desktop.id, desktop.shortcut_slot ?? null, [...sessionIdsOn(desktop), sessionId], sessionId);
          return { ...placed, revision: desktop.revision + 1 };
        }),
        currentDesktopId,
      }));
      return ok('desktop_place_session');
    }),
    sendDesktopRemoveLeaf: vi.fn(async (_desktopId: string, _leafId: string, _expectedRevision: number) =>
      ok('desktop_remove_leaf'),
    ),
  };
}

export interface TestDesktopGroup {
  id: string;
  title: string;
  tree?: unknown;
}

export function desktopGroups<TSession extends WorkspaceViewSession>(
  groups: TestDesktopGroup[],
  sessions: TSession[],
): WorkspaceWithSessions<TSession>[] {
  const desktops = groups.map((group, index) => {
    const held = sessions.filter((session) => session.workspaceId === group.id).map((session) => session.id);
    const desktop = agentDesktop(group.id, index + 1, held);
    return group.tree ? { ...desktop, tree_json: JSON.stringify(group.tree) } : desktop;
  });
  const titleById = new Map(groups.map((group) => [group.id, group.title]));
  return buildDesktopViewModels(desktops, sessions).map((view) => ({ ...view, title: titleById.get(view.id) ?? view.title }));
}

export function groupIndexes(groups: Array<{ id: string }>): Map<string, number> {
  return new Map(groups.map((group, index) => [group.id, index]));
}
