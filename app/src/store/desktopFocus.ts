import { create } from 'zustand';
import { useProfilesStore } from './profiles';

interface DesktopFocusState {
  focusedLeafByDesktop: Readonly<Record<string, string>>;
  setFocusedLeaf: (desktopId: string, leafId: string | null) => void;
}

export const useDesktopFocus = create<DesktopFocusState>((set) => ({
  focusedLeafByDesktop: {},
  setFocusedLeaf: (desktopId, leafId) =>
    set((state) => {
      if ((state.focusedLeafByDesktop[desktopId] ?? null) === leafId) return state;
      const { [desktopId]: _cleared, ...others } = state.focusedLeafByDesktop;
      return { focusedLeafByDesktop: leafId ? { ...others, [desktopId]: leafId } : others };
    }),
}));

useProfilesStore.subscribe((state, previous) => {
  if (state.desktops === previous.desktops || state.selectedProfileId !== previous.selectedProfileId) return;
  const remaining = new Set(state.desktops.map((desktop) => desktop.id));
  for (const desktop of previous.desktops) {
    if (!remaining.has(desktop.id)) useDesktopFocus.getState().setFocusedLeaf(desktop.id, null);
  }
});
