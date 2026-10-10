import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../contexts/DaemonApiContext';
import { createMockDaemonApi } from '../test/mocks/daemon';
import { useProfilesStore } from '../store/profiles';
import type { Desktop, Profile } from '../types/generated';
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
    sendDesktopCreate: vi.fn().mockResolvedValue(ok),
    sendDesktopRename: vi.fn().mockResolvedValue(ok),
    sendDesktopReorder: vi.fn().mockResolvedValue(ok),
    sendProfileSelect: vi.fn().mockResolvedValue(ok),
  };
  const showError = vi.fn();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <DaemonApiProvider api={createMockDaemonApi(api)}>{children}</DaemonApiProvider>
  );
  const { result } = renderHook(() => useDesktopNavigation(showError), { wrapper });
  return { api, showError, result };
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

  it('shows the current slot again when there is nowhere to bounce, so its leaf takes the keyboard', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 })]);
    const { api, showError, result } = renderNavigation();

    act(() => result.current.switchToSlot(1));

    expect(vi.mocked(api.sendDesktopSetCurrent).mock.calls.map((call) => call[1])).toEqual(['d1']);
    expect(showError).not.toHaveBeenCalled();
  });

  it('moves the active leaf beside the target active leaf and stays on the source desktop', async () => {
    seedStore([
      desktop('d1', { shortcut_slot: 1, tree_json: TREE_WITH_PANE('p1'), active_pane_id: 'p1', revision: 4 }),
      desktop('d2', { shortcut_slot: 2, tree_json: TREE_WITH_PANE('p9'), active_pane_id: 'p9', revision: 7 }),
    ]);
    const { api, result } = renderNavigation();

    act(() => result.current.moveActiveLeafToSlot(2, false));
    await settle();

    expect(api.sendDesktopMoveLeaf.mock.calls).toEqual([[{
      sourceDesktopId: 'd1',
      targetShortcutSlot: 2,
      leafId: 'p1',
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

    act(() => result.current.moveActiveLeafToSlot(2, false));
    await settle();

    expect(api.sendDesktopMoveLeaf.mock.calls[0][0]).toMatchObject({ leafId: 't1', targetShortcutSlot: 2 });
  });

  it('does nothing quietly when nothing is active to move', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d2', { shortcut_slot: 2 })]);
    const { api, showError, result } = renderNavigation();

    act(() => result.current.moveActiveLeafToSlot(2, false));

    expect(api.sendDesktopMoveLeaf).not.toHaveBeenCalled();
    expect(showError).not.toHaveBeenCalled();
  });


  it('renames a desktop and hands a refusal back to the rename form', async () => {
    seedStore([desktop('d1', { shortcut_slot: 1, revision: 6 })]);
    const { api, showError, result } = renderNavigation();

    await act(() => result.current.renameDesktop('d1', 'Reviews'));
    api.sendDesktopRename.mockRejectedValueOnce(new Error('desktop d1 not found'));
    const refused = act(() => result.current.renameDesktop('d1', ''));

    await expect(refused).rejects.toThrow('desktop d1 not found');
    expect(api.sendDesktopRename.mock.calls).toEqual([['d1', 'Reviews'], ['d1', '']]);
    expect(showError).not.toHaveBeenCalled();
  });

  it('reorders a desktop between its new neighbours and shows a refusal', async () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d2', { revision: 3 }), desktop('d3')]);
    const { api, showError, result } = renderNavigation();
    api.sendDesktopReorder.mockRejectedValueOnce(new Error('desktop d3 belongs to another profile'));

    act(() => result.current.reorderDesktop({ desktopId: 'd2', nextDesktopId: 'd1' }));
    await settle();

    expect(api.sendDesktopReorder.mock.calls).toEqual([[{ desktopId: 'd2', nextDesktopId: 'd1' }]]);
    expect(showError).toHaveBeenCalledWith('desktop d3 belongs to another profile');
  });

  it('shows a refused command to the user', async () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d2', { shortcut_slot: 2 })]);
    const { api, showError, result } = renderNavigation();
    api.sendDesktopSetCurrent.mockRejectedValueOnce(new Error('desktop d2 not found'));

    act(() => result.current.switchToSlot(2));
    await settle();

    expect(showError).toHaveBeenCalledWith('desktop d2 not found');
  });
});
