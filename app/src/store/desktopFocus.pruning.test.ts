import { describe, expect, it } from 'vitest';
import type { Profile } from '../types/generated';
import { agentDesktop, arrangeDesktops } from '../test/desktops';
import { useDesktopFocus } from './desktopFocus';
import { useProfilesStore } from './profiles';

const focused = () => useDesktopFocus.getState().focusedLeafByDesktop;

describe('desktop focus after desktops change', () => {
  it('forgets the focused pane of a deleted desktop and keeps the others', () => {
    arrangeDesktops([agentDesktop('d1', 1, ['s1']), agentDesktop('d2', 2, ['s2'])]);
    useDesktopFocus.getState().setFocusedLeaf('d1', 'pane-s1');
    useDesktopFocus.getState().setFocusedLeaf('d2', 'pane-s2');

    arrangeDesktops([agentDesktop('d2', 2, ['s2'])]);

    expect(focused()).toEqual({ d2: 'pane-s2' });
  });

  it('starts a profile the user switches to unmaximized, forgetting the one left', () => {
    arrangeDesktops([agentDesktop('d1', 1, ['s1'])]);
    useDesktopFocus.getState().setFocusedLeaf('d1', 'pane-s1');

    const other: Profile = { id: 'other', name: 'Other', current_desktop_id: 'e1', revision: 1 };
    useProfilesStore.getState().profilesChanged([other]);
    useProfilesStore.getState().arrangementArrived(other, [{ ...agentDesktop('e1', 1, ['s9']), profile_id: 'other' }]);

    expect(focused()).toEqual({});
  });
});
