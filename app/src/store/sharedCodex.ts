import { create } from 'zustand';
import { SharedCodexRestart, type SharedCodexState } from '../types/generated';

export type SharedCodexConfirm = 'restart' | 'reload';

interface SharedCodexStore {
  profileId: string | null;
  state: SharedCodexState | null;
  installing: boolean;
  installError: string | null;
  confirm: SharedCodexConfirm | null;
  received: (profileId: string, state: SharedCodexState | null) => void;
  installStarted: () => void;
  installFinished: (error: string | null) => void;
  clearInstallError: () => void;
  openConfirm: (confirm: SharedCodexConfirm) => void;
  closeConfirm: () => void;
}

export function canRestartNow(state: SharedCodexState | null): boolean {
  return state?.restart === SharedCodexRestart.Pending && (state.blockers?.length ?? 0) > 0;
}

export function staleSessionsOf(state: SharedCodexState | null): string[] {
  return state?.stale_sessions ?? [];
}

function confirmStillApplies(confirm: SharedCodexConfirm | null, state: SharedCodexState | null): boolean {
  if (confirm === 'restart') return canRestartNow(state);
  if (confirm === 'reload') return staleSessionsOf(state).length > 0;
  return false;
}

export const useSharedCodexStore = create<SharedCodexStore>((set) => ({
  profileId: null,
  state: null,
  installing: false,
  installError: null,
  confirm: null,
  received: (profileId, state) => set((current) => {
    const versionsMoved = current.state?.installed_version !== state?.installed_version
      || current.state?.latest_version !== state?.latest_version;
    return {
      profileId,
      state,
      installError: versionsMoved ? null : current.installError,
      confirm: confirmStillApplies(current.confirm, state) ? current.confirm : null,
    };
  }),
  installStarted: () => set({ installing: true, installError: null }),
  installFinished: (error) => set({ installing: false, installError: error }),
  clearInstallError: () => set({ installError: null }),
  openConfirm: (confirm) => set({ confirm }),
  closeConfirm: () => set({ confirm: null }),
}));
