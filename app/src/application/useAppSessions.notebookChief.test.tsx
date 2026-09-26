import { renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useProfilesStore } from '../store/profiles';
import type { Session } from '../store/sessions';
import { createDefaultWorkspaceState } from '../types/workspace';
import type { AppContentProps } from './appSupport';
import { useAppSessions } from './useAppSessions';

function localSession(id: string, state: Session['state']): Session {
  return {
    id,
    label: id,
    state,
    cwd: '/repo',
    workspaceId: '',
    profileId: '',
    desktopId: '',
    agent: 'claude',
    transcriptMatched: true,
    desktop: createDefaultWorkspaceState(),
    daemonActivePaneId: '',
  };
}

describe('useAppSessions Notebook chief status', () => {
  it("follows the selected profile's chief, not whichever chief comes first", () => {
    const daemonSessions = [
      { id: 'home-chief', chief_of_staff: true, profile_id: 'profile-home', state: 'working' },
      { id: 'work-chief', chief_of_staff: true, profile_id: 'profile-work', state: 'idle' },
    ] as AppContentProps['daemonSessions'];
    const render = () => renderHook(() => useAppSessions({
      activeSessionId: null,
      daemonEndpoints: [],
      sessions: [localSession('home-chief', 'working'), localSession('work-chief', 'idle')],
      daemonSessions,
      daemonWorkspaces: [],
      connect: vi.fn(async () => undefined),
    }));

    useProfilesStore.setState({ selectedProfileId: 'profile-work' });
    expect(render().result.current.notebookChiefActive).toBe(false);

    useProfilesStore.setState({ selectedProfileId: 'profile-home' });
    expect(render().result.current.notebookChiefActive).toBe(true);

    useProfilesStore.setState({ selectedProfileId: 'profile-without-chief' });
    expect(render().result.current.notebookChiefActive).toBeUndefined();
  });
});
