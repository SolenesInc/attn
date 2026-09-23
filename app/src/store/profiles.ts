import { create } from 'zustand';
import type { Desktop, Profile } from '../types/generated';
import { persistSelectedProfileId } from '../utils/selectedProfile';

export interface ProfilesState {
  profiles: Profile[];
  selectedProfileId: string | null;
  desktops: Desktop[];
  previousDesktopId: string | null;
  selection: PendingSelection | null;
  selectionStarted: (profileId: string) => void;
  enterScope: (profiles: Profile[] | undefined, selectedProfileId: string | undefined, desktops: Desktop[] | undefined) => void;
  selectedProfileChanged: (profile: Profile, desktops: Desktop[]) => void;
  profilesChanged: (profiles: Profile[]) => void;
  arrangementChanged: (profile: Profile, desktops: Desktop[], deletedDesktopIds: string[] | undefined) => void;
}

interface PendingSelection {
  profileId: string;
  earlyProfile: Profile | null;
  earlyDesktops: Desktop[];
  earlyDeletedIds: string[];
}

function currentDesktopIdOf(profiles: Profile[], profileId: string | null): string | null {
  return profiles.find((profile) => profile.id === profileId)?.current_desktop_id ?? null;
}

function followCurrentDesktop(
  state: ProfilesState,
  profiles: Profile[],
  desktops: Desktop[],
): Pick<ProfilesState, 'previousDesktopId'> {
  const before = currentDesktopIdOf(state.profiles, state.selectedProfileId);
  const after = currentDesktopIdOf(profiles, state.selectedProfileId);
  const previous = before && after && before !== after ? before : state.previousDesktopId;
  const stillExists = previous !== null && previous !== after && desktops.some((desktop) => desktop.id === previous);
  return { previousDesktopId: stillExists ? previous : null };
}

function withProfile(profiles: Profile[], profile: Profile): Profile[] {
  const index = profiles.findIndex((entry) => entry.id === profile.id);
  if (index < 0) return [...profiles, profile];
  const next = profiles.slice();
  next[index] = profile;
  return next;
}

function newerOf(held: Desktop, incoming: Desktop | undefined): Desktop {
  return incoming && incoming.revision >= held.revision ? incoming : held;
}

function mergeDesktops(current: Desktop[], changed: Desktop[], deletedIds: string[] | undefined): Desktop[] {
  const deleted = new Set(deletedIds ?? []);
  const changedById = new Map(changed.map((desktop) => [desktop.id, desktop]));
  const merged = current
    .filter((desktop) => !deleted.has(desktop.id))
    .map((desktop) => newerOf(desktop, changedById.get(desktop.id)));
  const known = new Set(merged.map((desktop) => desktop.id));
  return [...merged, ...changed.filter((desktop) => !known.has(desktop.id) && !deleted.has(desktop.id))];
}

function scopeTo(profiles: Profile[], profileId: string | null, desktops: Desktop[]): Partial<ProfilesState> {
  persistSelectedProfileId(profileId ?? undefined);
  return { profiles, selectedProfileId: profileId, desktops, previousDesktopId: null, selection: null };
}

function heldForSelection(selection: PendingSelection, profile: Profile, desktops: Desktop[], deletedIds: string[] | undefined): PendingSelection {
  return {
    ...selection,
    earlyProfile: profile,
    earlyDesktops: mergeDesktops(selection.earlyDesktops, desktops, undefined),
    earlyDeletedIds: [...selection.earlyDeletedIds, ...(deletedIds ?? [])],
  };
}

function withEarlyArrival(selection: PendingSelection | null, profile: Profile, desktops: Desktop[]): { profile: Profile; desktops: Desktop[] } {
  if (!selection || selection.profileId !== profile.id) return { profile, desktops };
  return {
    profile: selection.earlyProfile ?? profile,
    desktops: mergeDesktops(desktops, selection.earlyDesktops, selection.earlyDeletedIds),
  };
}

export const useProfilesStore = create<ProfilesState>((set) => ({
  profiles: [],
  selectedProfileId: null,
  desktops: [],
  previousDesktopId: null,
  selection: null,

  selectionStarted: (profileId) =>
    set({ selection: { profileId, earlyProfile: null, earlyDesktops: [], earlyDeletedIds: [] } }),

  enterScope: (profiles, selectedProfileId, desktops) =>
    set(() => scopeTo(profiles ?? [], selectedProfileId || null, desktops ?? [])),

  selectedProfileChanged: (profile, desktops) =>
    set((state) => {
      const settled = withEarlyArrival(state.selection, profile, desktops);
      return scopeTo(withProfile(state.profiles, settled.profile), settled.profile.id, settled.desktops);
    }),

  profilesChanged: (profiles) =>
    set((state) => ({ profiles, ...followCurrentDesktop(state, profiles, state.desktops) })),

  arrangementChanged: (profile, desktops, deletedDesktopIds) =>
    set((state) => {
      if (profile.id !== state.selectedProfileId && state.selection?.profileId === profile.id) {
        return { selection: heldForSelection(state.selection, profile, desktops, deletedDesktopIds) };
      }
      if (profile.id !== state.selectedProfileId) {
        const selectedStillLive = state.profiles.some((entry) => entry.id === state.selectedProfileId);
        return selectedStillLive ? state : scopeTo(withProfile(state.profiles, profile), profile.id, desktops);
      }
      const profiles = withProfile(state.profiles, profile);
      const merged = mergeDesktops(state.desktops, desktops, deletedDesktopIds);
      return { profiles, desktops: merged, ...followCurrentDesktop(state, profiles, merged) };
    }),
}));
