import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import type { Desktop } from '../types/generated';
import { useAppDeepLinks } from './useAppDeepLinks';

const deepLink = vi.hoisted(() => ({
  coldStart: [] as string[],
  listener: null as ((urls: string[]) => void) | null,
}));

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  getCurrent: vi.fn(async () => deepLink.coldStart),
  onOpenUrl: vi.fn(async (listener: (urls: string[]) => void) => {
    deepLink.listener = listener;
    return () => {};
  }),
}));

const SPAWN_URL = 'attn://spawn?cwd=%2Frepo%2Fplan&label=plan';

const desktop: Desktop = {
  id: 'desktop-1',
  profile_id: 'profile-1',
  name: '',
  order_key: 'i',
  revision: 1,
  tree_json: '',
  active_pane_id: '',
  panes: [],
};

function renderDeepLinks(launchAgent: (label: string, cwd: string) => Promise<string>) {
  renderHook(() => useAppDeepLinks({ selectAgent: vi.fn(() => true), launchAgent }));
}

describe('useAppDeepLinks', () => {
  beforeEach(() => {
    deepLink.coldStart = [];
    deepLink.listener = null;
    useSessionStore.setState({ sessions: [] });
    useProfilesStore.setState({ selectedProfileId: null, currentDesktopId: null, desktops: [] });
  });

  it('holds a cold-start spawn link until the current desktop arrives, then launches it once', async () => {
    deepLink.coldStart = [SPAWN_URL];
    const launchAgent = vi.fn(async () => 'session-plan');
    renderDeepLinks(launchAgent);

    await waitFor(() => expect(deepLink.listener).not.toBeNull());
    deepLink.listener?.([SPAWN_URL]);
    expect(launchAgent).not.toHaveBeenCalled();

    useProfilesStore.setState({ selectedProfileId: 'profile-1', currentDesktopId: 'desktop-1', desktops: [desktop] });

    await waitFor(() => expect(launchAgent).toHaveBeenCalledTimes(1));
    expect(launchAgent).toHaveBeenCalledWith('plan', '/repo/plan');
  });

  it('lets a link whose launch failed be delivered again', async () => {
    useProfilesStore.setState({ selectedProfileId: 'profile-1', currentDesktopId: 'desktop-1', desktops: [desktop] });
    const launchAgent = vi.fn()
      .mockRejectedValueOnce(new Error('spawn failed'))
      .mockResolvedValueOnce('session-plan');
    renderDeepLinks(launchAgent);
    vi.spyOn(console, 'error').mockImplementation(() => {});

    await waitFor(() => expect(deepLink.listener).not.toBeNull());
    deepLink.listener?.([SPAWN_URL]);
    await waitFor(() => expect(launchAgent).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(console.error).toHaveBeenCalled());

    deepLink.listener?.([SPAWN_URL]);
    await waitFor(() => expect(launchAgent).toHaveBeenCalledTimes(2));
  });
});
