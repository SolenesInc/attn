import { create } from 'zustand';
import { useShallow } from 'zustand/react/shallow';
import type { Desktop, MigrationPhase, MigrationState, Profile } from '../types/generated';
import { persistSelectedProfileId } from '../utils/selectedProfile';

export interface ProfilesState {
  profiles: Profile[];
  selectedProfileId: string | null;
  currentDesktopId: string | null;
  desktops: Desktop[];
  previousDesktopId: string | null;
  migrationPhase: MigrationPhase | null;
  migration: MigrationState | null;
  migrationFromThisConnection: boolean;
  connectionReportedMigrationPhase: (phase: MigrationPhase | null) => void;
  migrationArrived: (migration: MigrationState) => void;
  enterScope: (profiles: Profile[] | undefined, selectedProfileId: string | undefined, desktops: Desktop[] | undefined) => void;
  profilesChanged: (profiles: Profile[]) => void;
  arrangementArrived: (profile: Profile, desktops: Desktop[]) => void;
}

export interface TileSelection {
  desktopId: string;
  tileId: string;
}

export function selectedTile(state: Pick<ProfilesState, 'desktops' | 'currentDesktopId'>): TileSelection | null {
  const desktop = state.desktops.find((entry) => entry.id === state.currentDesktopId);
  const leafId = desktop?.active_pane_id;
  if (!desktop || !leafId) return null;
  if (desktop.panes.some((pane) => pane.pane_id === leafId && pane.session_id)) return null;
  return { desktopId: desktop.id, tileId: leafId };
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
  migration: null,
  migrationFromThisConnection: false,

  connectionReportedMigrationPhase: (migrationPhase) => set({ migrationPhase, migrationFromThisConnection: false }),

  migrationArrived: (migration) =>
    set((state) =>
      state.migrationFromThisConnection && state.migration && migration.revision < state.migration.revision
        ? state
        : { migration, migrationPhase: migration.phase, migrationFromThisConnection: true },
    ),

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

export function useSelectedTile(): TileSelection | null {
  return useProfilesStore(useShallow(selectedTile));
}
