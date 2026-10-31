import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../contexts/DaemonApiContext';
import { createMockDaemonApi } from '../test/mocks/daemon';
import { useProfilesStore } from '../store/profiles';
import type { Desktop, Profile } from '../types/generated';
import { useDesktopNavigation } from './useDesktopNavigation';

const PROFILE: Profile = { id: 'set-default', name: 'Default', current_desktop_id: 'd1', revision: 3 };

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
    sendDesktopMoveLeaf: vi.fn().mockResolvedValue(ok),
    sendDesktopCreate: vi.fn().mockResolvedValue(ok),
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

  it('shows the current slot again when there is nowhere to bounce, so its leaf takes the keyboard', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 })]);
    const { api, showError, result } = renderNavigation();

    act(() => result.current.switchToSlot(1));

    expect(vi.mocked(api.sendDesktopSetCurrent).mock.calls.map((call) => call[1])).toEqual(['d1']);
    expect(showError).not.toHaveBeenCalled();
  });

  it('does nothing quietly when nothing is active to move', () => {
    seedStore([desktop('d1', { shortcut_slot: 1 }), desktop('d2', { shortcut_slot: 2 })]);
    const { api, showError, result } = renderNavigation();

    act(() => result.current.moveActiveLeafToSlot(2, false));

    expect(api.sendDesktopMoveLeaf).not.toHaveBeenCalled();
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
