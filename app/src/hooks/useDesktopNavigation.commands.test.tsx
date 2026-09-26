import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../contexts/DaemonApiContext';
import { createMockDaemonApi } from '../test/mocks/daemon';
import { useProfilesStore } from '../store/profiles';
import type { Desktop, Profile } from '../types/generated';
import { ProfileCommandError } from './daemonProfileEvents';
import { FRESH_ARRANGEMENT_TRIPWIRE_MS } from './desktopRevisions';
import { useDesktopNavigation } from './useDesktopNavigation';

const PROFILE: Profile = { id: 'set-default', name: 'Default', current_desktop_id: 'd1', revision: 3 };
const TREE_WITH_PANE = (paneId: string) => JSON.stringify({ type: 'pane', pane_id: paneId });

function desktop(id: string, overrides: Partial<Desktop> = {}): Desktop {
  return {
    id,
    profile_id: PROFILE.id,
    name: '',
    order_key: id,
    tree_json: '',
    active_pane_id: '',
    panes: [],
    revision: 1,
    ...overrides,
  };
}

function seedStore(desktops: Desktop[], previousDesktopId: string | null = null, currentDesktopId = 'd1') {
  useProfilesStore.setState({
    profiles: [{ ...PROFILE, current_desktop_id: currentDesktopId }],
    selectedProfileId: PROFILE.id,
    currentDesktopId,
    desktops,
    previousDesktopId,
  });
}

const ok = { request_id: 'r', action: 'x', success: true, event: 'profile_action_result' };

function renderNavigation() {
  const api = {
    sendDesktopSetCurrent: vi.fn().mockResolvedValue(ok),
    sendDesktopSetActivePane: vi.fn().mockResolvedValue(ok),
    sendDesktopMoveLeaf: vi.fn().mockResolvedValue(ok),
    sendDesktopDelete: vi.fn().mockResolvedValue(ok),
    sendDesktopSetShortcutSlot: vi.fn().mockResolvedValue(ok),
    sendDesktopCreate: vi.fn().mockResolvedValue(ok),
    sendProfileSelect: vi.fn().mockResolvedValue(ok),
  };
  const showNotice = vi.fn();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <DaemonApiProvider api={createMockDaemonApi(api)}>{children}</DaemonApiProvider>
  );
  const { result } = renderHook(() => useDesktopNavigation(showNotice), { wrapper });
  return { api, showNotice, result };
}

function staleRevision() {
  return new ProfileCommandError({
    ...ok,
    action: 'desktop_move_leaf',
    success: false,
    error: 'stale',
    error_code: 'stale_revision',
  } as never);
}

async function settle() {
  await act(async () => {
    await Promise.resolve();
  });
}

describe('useDesktopNavigation', () => {
  beforeEach(() => {
    useProfilesStore.setState({ profiles: [], selectedProfileId: null, currentDesktopId: null, desktops: [], previousDesktopId: null });
  });

  it('switches to the desktop on a slot', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d2', { shortcut_slot: 2 })]);
    const { api, result } = renderNavigation();

    act(() => result.current.switchToSlot(2));

    expect(api.sendDesktopSetCurrent.mock.calls).toEqual([['set-default', 'd2']]);
  });

  it('bounces back to the previous desktop when the slot is already current', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d10')], 'd10');
    const { api, result } = renderNavigation();

    act(() => result.current.switchToSlot(1));

    expect(api.sendDesktopSetCurrent.mock.calls).toEqual([['set-default', 'd10']]);
  });

  it('stays put on the current slot with nowhere to bounce', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 })]);
    const { api, showNotice, result } = renderNavigation();

    act(() => result.current.switchToSlot(1));

    expect(api.sendDesktopSetCurrent).not.toHaveBeenCalled();
    expect(showNotice).not.toHaveBeenCalled();
  });

  it('says so when no desktop holds the slot', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 })]);
    const { api, showNotice, result } = renderNavigation();

    act(() => result.current.switchToSlot(5));

    expect(api.sendDesktopSetCurrent).not.toHaveBeenCalled();
    expect(showNotice).toHaveBeenCalledWith(expect.stringContaining('No desktop on'));
  });

  it('sends the focused pane beside the target active leaf and leaves focusing it to the daemon', async () => {
    seedStore([
      desktop('d1', { shortcut_slot: 1, tree_json: TREE_WITH_PANE('p1'), active_pane_id: 'p1', revision: 4 }),
      desktop('d2', { shortcut_slot: 2, tree_json: TREE_WITH_PANE('p9'), active_pane_id: 'p9', revision: 7 }),
    ]);
    const { api, result } = renderNavigation();

    act(() => result.current.sendActivePaneToSlot(2));
    await settle();

    expect(api.sendDesktopMoveLeaf.mock.calls).toEqual([[{
      sourceDesktopId: 'd1',
      targetDesktopId: 'd2',
      leafId: 'p1',
      anchorId: 'p9',
      edge: 'right',
      expectedSourceRevision: 4,
      expectedTargetRevision: 7,
    }]]);
    expect(api.sendDesktopSetActivePane).not.toHaveBeenCalled();
    expect(api.sendDesktopSetCurrent).not.toHaveBeenCalled();
  });

  it('sends the tile that is the active leaf', async () => {
    const withTile = JSON.stringify({
      type: 'split',
      split_id: 's1',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'pane', pane_id: 'p1' },
        { type: 'tile', tile_id: 't1', tile_kind: 'markdown', tile_params: '/notes.md' },
      ],
    });
    seedStore([
      desktop('d1', { shortcut_slot: 1, tree_json: withTile, active_pane_id: 't1', revision: 4 }),
      desktop('d2', { shortcut_slot: 2, tree_json: TREE_WITH_PANE('p9'), active_pane_id: 'p9', revision: 7 }),
    ]);
    const { api, result } = renderNavigation();

    act(() => result.current.sendActivePaneToSlot(2));
    await settle();

    expect(api.sendDesktopMoveLeaf.mock.calls[0][0]).toMatchObject({ leafId: 't1', targetDesktopId: 'd2', anchorId: 'p9' });
  });

  it('retries a send once the other client\'s newer arrangement arrives', async () => {
    seedStore([
      desktop('d1', { shortcut_slot: 1, tree_json: TREE_WITH_PANE('p1'), active_pane_id: 'p1', revision: 4 }),
      desktop('d2', { shortcut_slot: 2, revision: 7 }),
    ]);
    const { api, result } = renderNavigation();
    api.sendDesktopMoveLeaf.mockRejectedValueOnce(staleRevision());

    act(() => result.current.sendActivePaneToDesktop('d2'));
    await settle();
    expect(api.sendDesktopMoveLeaf).toHaveBeenCalledTimes(1);

    act(() => {
      useProfilesStore.setState((state) => ({
        desktops: state.desktops.map((entry) => (entry.id === 'd2' ? { ...entry, revision: 8 } : entry)),
      }));
    });
    await settle();
    await settle();

    expect(api.sendDesktopMoveLeaf.mock.calls.map(([move]) => move.expectedTargetRevision)).toEqual([7, 8]);
  });

  it('names the wait when a newer arrangement never arrives after a stale send', async () => {
    vi.useFakeTimers();
    try {
      seedStore([
        desktop('d1', { shortcut_slot: 1, tree_json: TREE_WITH_PANE('p1'), active_pane_id: 'p1', revision: 4 }),
        desktop('d2', { shortcut_slot: 2, revision: 7 }),
      ]);
      const { api, showNotice, result } = renderNavigation();
      api.sendDesktopMoveLeaf.mockRejectedValueOnce(staleRevision());

      act(() => result.current.sendActivePaneToDesktop('d2'));
      await settle();
      await act(async () => {
        vi.advanceTimersByTime(FRESH_ARRANGEMENT_TRIPWIRE_MS);
      });
      await settle();

      expect(api.sendDesktopMoveLeaf).toHaveBeenCalledTimes(1);
      expect(showNotice).toHaveBeenCalledWith(expect.stringContaining('did not arrive within 5s'));
    } finally {
      vi.useRealTimers();
    }
  });

  it('says so when there is no focused pane to send', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d2', { shortcut_slot: 2 })]);
    const { api, showNotice, result } = renderNavigation();

    act(() => result.current.sendActivePaneToSlot(2));

    expect(api.sendDesktopMoveLeaf).not.toHaveBeenCalled();
    expect(showNotice).toHaveBeenCalledWith('No focused pane to send.');
  });

  it('gives an extra desktop the first free shortcut slot', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d3', { shortcut_slot: 3 }), desktop('d10', { revision: 5 })]);
    const { api, result } = renderNavigation();

    act(() => result.current.giveShortcutSlot('d10'));

    expect(api.sendDesktopSetShortcutSlot.mock.calls).toEqual([['d10', 2, 5]]);
  });

  it('names the limit when every shortcut slot is taken', () => {
    const slotted = [1, 2, 3, 4, 5, 6, 7, 8, 9].map((slot) => desktop(`d${slot}`, { shortcut_slot: slot }));
    seedStore([...slotted, desktop('d10')]);
    const { api, showNotice, result } = renderNavigation();

    act(() => result.current.giveShortcutSlot('d10'));

    expect(api.sendDesktopSetShortcutSlot).not.toHaveBeenCalled();
    expect(showNotice).toHaveBeenCalledWith(expect.stringContaining('is taken'));
  });

  it('deletes only an empty desktop', () => {
    seedStore([
      desktop('d1', { shortcut_slot: 1 }),
      desktop('d2', { shortcut_slot: 2, tree_json: TREE_WITH_PANE('p2') }),
      desktop('d3', { shortcut_slot: 3, revision: 2 }),
    ]);
    const { api, showNotice, result } = renderNavigation();

    act(() => result.current.deleteDesktop('d2'));
    act(() => result.current.deleteDesktop('d3'));

    expect(showNotice).toHaveBeenCalledWith('Desktop 2 still has panes; only an empty desktop can be deleted.');
    expect(api.sendDesktopDelete.mock.calls).toEqual([['d3', 2]]);
  });

  it('shows a refused command to the user', async () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d2', { shortcut_slot: 2 })]);
    const { api, showNotice, result } = renderNavigation();
    api.sendDesktopSetCurrent.mockRejectedValueOnce(new Error('desktop d2 not found'));

    act(() => result.current.switchToSlot(2));
    await settle();

    expect(showNotice).toHaveBeenCalledWith('desktop d2 not found');
  });
});
