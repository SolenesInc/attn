import { create } from 'zustand';
import type { Desktop, MigrationPhase, Profile } from '../types/generated';
import { persistSelectedProfileId } from '../utils/selectedProfile';

export interface ProfilesState {
  profiles: Profile[];
  selectedProfileId: string | null;
  currentDesktopId: string | null;
  desktops: Desktop[];
  previousDesktopId: string | null;
  migrationPhase: MigrationPhase | null;
  migrationPhaseChanged: (phase: MigrationPhase | null) => void;
  enterScope: (profiles: Profile[] | undefined, selectedProfileId: string | undefined, desktops: Desktop[] | undefined) => void;
  profilesChanged: (profiles: Profile[]) => void;
  arrangementArrived: (profile: Profile, desktops: Desktop[]) => void;
}

type Arrangement = Pick<ProfilesState, 'selectedProfileId' | 'currentDesktopId' | 'desktops' | 'previousDesktopId'>;

const NO_ARRANGEMENT: Arrangement = { selectedProfileId: null, currentDesktopId: null, desktops: [], previousDesktopId: null };

function bounceTarget(state: Arrangement, profile: Profile, desktops: Desktop[]): string | null {
  if (profile.id !== state.selectedProfileId) return null;
  const leftDesktop = state.currentDesktopId !== profile.current_desktop_id ? state.currentDesktopId : null;
  const target = leftDesktop ?? state.previousDesktopId;
  if (!target || target === profile.current_desktop_id) return null;
  return desktops.some((desktop) => desktop.id === target) ? target : null;
}

function arrangementOf(state: Arrangement, profile: Profile, desktops: Desktop[]): Arrangement {
  if (profile.id !== state.selectedProfileId) persistSelectedProfileId(profile.id);
  return {
    selectedProfileId: profile.id,
    currentDesktopId: profile.current_desktop_id || null,
    desktops,
    previousDesktopId: bounceTarget(state, profile, desktops),
  };
}

export const useProfilesStore = create<ProfilesState>((set) => ({
  profiles: [],
  ...NO_ARRANGEMENT,
  migrationPhase: null,

  migrationPhaseChanged: (migrationPhase) => set({ migrationPhase }),

  enterScope: (profiles, selectedProfileId, desktops) =>
    set(() => {
      const live = profiles ?? [];
      const selected = live.find((profile) => profile.id === selectedProfileId);
      if (!selected) {
        persistSelectedProfileId(undefined);
        return { profiles: live, ...NO_ARRANGEMENT };
      }
      return { profiles: live, ...arrangementOf(NO_ARRANGEMENT, selected, desktops ?? []) };
    }),

  profilesChanged: (profiles) => set({ profiles }),

  arrangementArrived: (profile, desktops) => set((state) => arrangementOf(state, profile, desktops)),
}));
