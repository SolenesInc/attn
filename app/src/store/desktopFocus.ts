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
  if (state.desktops === previous.desktops) return;
  const current = new Set(state.desktops.map((desktop) => desktop.id));
  for (const desktopId of Object.keys(useDesktopFocus.getState().focusedLeafByDesktop)) {
    if (!current.has(desktopId)) useDesktopFocus.getState().setFocusedLeaf(desktopId, null);
  }
});
