import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { createMockDaemonApi } from '../test/mocks/daemon';
import type { AppContentProps } from './appSupport';
import { useChiefOfStaff } from './useChiefOfStaff';

function renderChief(daemonSessions: AppContentProps['daemonSessions']) {
  const api = createMockDaemonApi({});
  const wrapper = ({ children }: { children: ReactNode }) => <DaemonApiProvider api={api}>{children}</DaemonApiProvider>;
  return renderHook(() => useChiefOfStaff({ enrichedLocalSessions: [], daemonSessions, showError: vi.fn() }), { wrapper });
}

describe('useChiefOfStaff per profile', () => {
  it("reports a chief only when the selected profile has one", () => {
    useProfilesStore.setState({ selectedProfileId: 'profile-work' });
    const homeChief = { id: 'home-chief', chief_of_staff: true, profile_id: 'profile-home' } as AppContentProps['daemonSessions'][number];
    expect(renderChief([homeChief]).result.current.hasChiefOfStaff).toBe(false);

    const workChief = { id: 'work-chief', chief_of_staff: true, profile_id: 'profile-work' } as AppContentProps['daemonSessions'][number];
    expect(renderChief([homeChief, workChief]).result.current.hasChiefOfStaff).toBe(true);
  });
});

describe('useChiefOfStaff transfers', () => {
  function renderWith(daemonSessions: AppContentProps['daemonSessions'], local: Array<{ id: string; label: string; chiefOfStaff?: boolean }>) {
    const sendSetChiefOfStaff = vi.fn(async () => undefined);
    const api = createMockDaemonApi({ sendSetChiefOfStaff });
    const wrapper = ({ children }: { children: ReactNode }) => <DaemonApiProvider api={api}>{children}</DaemonApiProvider>;
    const enrichedLocalSessions = local as unknown as Parameters<typeof useChiefOfStaff>[0]['enrichedLocalSessions'];
    const hook = renderHook(() => useChiefOfStaff({ enrichedLocalSessions, daemonSessions, showError: vi.fn() }), { wrapper });
    return { hook, sendSetChiefOfStaff };
  }

  it("only asks to replace the selected profile's own chief", async () => {
    useProfilesStore.setState({ selectedProfileId: 'profile-work' });
    const daemonSessions = [
      { id: 'home-chief', chief_of_staff: true, profile_id: 'profile-home' },
      { id: 'work-agent', profile_id: 'profile-work' },
    ] as AppContentProps['daemonSessions'];
    const { hook, sendSetChiefOfStaff } = renderWith(daemonSessions, [
      { id: 'home-chief', label: 'Home chief', chiefOfStaff: true },
      { id: 'work-agent', label: 'Work agent' },
    ]);

    await act(async () => {
      hook.result.current.handleChangeChiefOfStaff('work-agent', true);
    });
    expect(hook.result.current.chiefTransferTarget).toBeNull();
    expect(sendSetChiefOfStaff).toHaveBeenCalledWith('work-agent', true);
  });

  it("names the selected profile's chief as the one being replaced", () => {
    useProfilesStore.setState({ selectedProfileId: 'profile-work' });
    const daemonSessions = [
      { id: 'home-chief', chief_of_staff: true, profile_id: 'profile-home' },
      { id: 'work-chief', chief_of_staff: true, profile_id: 'profile-work' },
      { id: 'work-agent', profile_id: 'profile-work' },
    ] as AppContentProps['daemonSessions'];
    const { hook } = renderWith(daemonSessions, [
      { id: 'home-chief', label: 'Home chief', chiefOfStaff: true },
      { id: 'work-chief', label: 'Work chief', chiefOfStaff: true },
      { id: 'work-agent', label: 'Work agent' },
    ]);

    act(() => {
      hook.result.current.handleChangeChiefOfStaff('work-agent', true);
    });
    expect(hook.result.current.chiefTransferTarget).toEqual({ sessionId: 'work-agent', targetLabel: 'Work agent', currentLabel: 'Work chief' });
  });
});
