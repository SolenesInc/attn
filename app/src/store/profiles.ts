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
  migrationDeparted: string[];
  connectionOpened: () => void;
  migrationPhaseChanged: (phase: MigrationPhase | null) => void;
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

function departedGroupTitles(previous: MigrationState, next: MigrationState): string[] {
  if (next.phase !== previous.phase) return [];
  const remaining = new Set(next.groups.map((group) => group.group_id));
  return previous.groups.filter((group) => !remaining.has(group.group_id)).map((group) => group.title);
}

export const useProfilesStore = create<ProfilesState>((set) => ({
  profiles: [],
  ...NO_ARRANGEMENT,
  migrationPhase: null,
  migration: null,
  migrationFromThisConnection: false,
  migrationDeparted: [],

  connectionOpened: () => set({ migrationFromThisConnection: false }),

  migrationPhaseChanged: (migrationPhase) => set({ migrationPhase }),

  migrationArrived: (migration) =>
    set((state) => {
      const previous = state.migrationFromThisConnection ? state.migration : null;
      if (previous && migration.revision < previous.revision) return state;
      return {
        migration,
        migrationPhase: migration.phase,
        migrationFromThisConnection: true,
        migrationDeparted: previous ? departedGroupTitles(previous, migration) : [],
      };
    }),

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
