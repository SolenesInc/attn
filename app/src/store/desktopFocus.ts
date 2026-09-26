import { create } from 'zustand';

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
