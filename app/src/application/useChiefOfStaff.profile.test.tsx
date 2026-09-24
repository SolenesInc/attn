import { renderHook } from '@testing-library/react';
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
