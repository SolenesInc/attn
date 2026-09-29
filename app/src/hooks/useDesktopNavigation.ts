import { useCallback, useMemo } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import type { Desktop } from '../types/generated';
import { withFreshDesktopRevisions } from './desktopRevisions';
import { actThenShow } from '../application/openThenShow';
import { desktopInSlot, desktopLabel, firstFreeSlot, isEmptyDesktop, slotShortcut } from '../utils/desktops';

type ShowNotice = (message: string) => void;

function currentDesktopOf(state: ReturnType<typeof useProfilesStore.getState>): Desktop | undefined {
  return state.desktops.find((desktop) => desktop.id === state.currentDesktopId);
}

function failureMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function useDesktopNavigation(showNotice: ShowNotice) {
  const {
    sendDesktopSetCurrent,
    sendDesktopMoveLeaf,
    sendDesktopDelete,
    sendDesktopSetShortcutSlot,
    sendDesktopCreate,
    sendDesktopRename,
    sendDesktopReorder,
    sendProfileSelect,
    sendProfileCreate,
    sendProfileRename,
    sendProfileDelete,
  } = useDaemonApi();
  const profiles = useProfilesStore((state) => state.profiles);
  const selectedProfileId = useProfilesStore((state) => state.selectedProfileId);
  const desktops = useProfilesStore((state) => state.desktops);
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const selectedProfile = useMemo(
    () => profiles.find((profile) => profile.id === selectedProfileId),
    [selectedProfileId, profiles],
  );
  const currentDesktop = useMemo(
    () => desktops.find((desktop) => desktop.id === currentDesktopId),
    [desktops, currentDesktopId],
  );

  const report = useCallback(
    (action: Promise<unknown>) => {
      void action.catch((err) => showNotice(failureMessage(err)));
    },
    [showNotice],
  );

  const switchToDesktop = useCallback(
    (desktopId: string) => {
      const state = useProfilesStore.getState();
      if (!state.selectedProfileId) return;
      const profileId = state.selectedProfileId;
      report(sendDesktopSetCurrent(profileId, desktopId));
    },
    [report, sendDesktopSetCurrent],
  );

  // Bouncing back applies only while the user is looking at the slot's desktop.
  const switchToSlot = useCallback(
    (slot: number, looking = true) => {
      const state = useProfilesStore.getState();
      const target = desktopInSlot(state.desktops, slot);
      if (!target) {
        showNotice(`No desktop on ${slotShortcut(slot)}. Give one a shortcut from the overview.`);
        return;
      }
      const toggleBack = looking && target.id === currentDesktopOf(state)?.id && state.previousDesktopId;
      switchToDesktop(toggleBack || target.id);
    },
    [showNotice, switchToDesktop],
  );

  const moveActiveLeaf = useCallback(
    async (targetDesktopId: string, follow: boolean): Promise<void> => {
      const state = useProfilesStore.getState();
      const source = currentDesktopOf(state);
      const target = state.desktops.find((desktop) => desktop.id === targetDesktopId);
      if (!source || !target || source.id === target.id) return;
      const leafId = source.active_pane_id;
      if (!leafId) {
        showNotice('Nothing is active to move.');
        return;
      }
      const move = () => withFreshDesktopRevisions([source.id, target.id], (revisionOf) =>
        sendDesktopMoveLeaf({
          sourceDesktopId: source.id,
          targetDesktopId: target.id,
          leafId,
          anchorId: target.active_pane_id || undefined,
          edge: 'right',
          expectedSourceRevision: revisionOf(source.id),
          expectedTargetRevision: revisionOf(target.id),
        }),
      );
      if (!follow) {
        await move();
        return;
      }
      await actThenShow({ kind: 'move', leafId, sourceDesktopId: source.id, targetDesktopId: target.id }, move, (result) =>
        result.pane_id ? { desktopId: target.id, leafId: result.pane_id } : null);
    },
    [sendDesktopMoveLeaf, showNotice],
  );

  const moveActiveLeafToDesktop = useCallback(
    (desktopId: string, follow: boolean) => report(moveActiveLeaf(desktopId, follow)),
    [moveActiveLeaf, report],
  );

  const moveActiveLeafToSlot = useCallback(
    (slot: number, follow: boolean) => {
      const target = desktopInSlot(useProfilesStore.getState().desktops, slot);
      if (!target) {
        showNotice(`No desktop on ${slotShortcut(slot)} to move to. Give one a shortcut from the overview.`);
        return;
      }
      moveActiveLeafToDesktop(target.id, follow);
    },
    [moveActiveLeafToDesktop, showNotice],
  );

  const deleteDesktop = useCallback(
    (desktopId: string) => {
      const state = useProfilesStore.getState();
      const desktop = state.desktops.find((entry) => entry.id === desktopId);
      if (!desktop) return;
      if (!isEmptyDesktop(desktop)) {
        showNotice(`${desktopLabel(desktop, state.desktops)} still has panes; only an empty desktop can be deleted.`);
        return;
      }
      report(sendDesktopDelete(desktop.id, desktop.revision));
    },
    [report, sendDesktopDelete, showNotice],
  );

  const giveShortcutSlot = useCallback(
    (desktopId: string) => {
      const state = useProfilesStore.getState();
      const desktop = state.desktops.find((entry) => entry.id === desktopId);
      if (!desktop || desktop.shortcut_slot) return;
      const slot = firstFreeSlot(state.desktops);
      if (slot === null) {
        showNotice(`Every shortcut ${slotShortcut(1)} to ${slotShortcut(9)} is taken. Delete an empty desktop to free one.`);
        return;
      }
      report(sendDesktopSetShortcutSlot(desktop.id, slot, desktop.revision));
    },
    [report, sendDesktopSetShortcutSlot, showNotice],
  );

  const createDesktop = useCallback(() => {
    const profileId = useProfilesStore.getState().selectedProfileId;
    if (!profileId) return;
    report(
      sendDesktopCreate(profileId).then((result) => {
        const created = result.desktops?.[0];
        if (created) return sendDesktopSetCurrent(profileId, created.id);
        return undefined;
      }),
    );
  }, [report, sendDesktopCreate, sendDesktopSetCurrent]);

  const renameDesktop = useCallback(
    (desktopId: string, name: string) =>
      withFreshDesktopRevisions([desktopId], (revisionOf) =>
        sendDesktopRename(desktopId, name, revisionOf(desktopId)),
      ).then(() => undefined),
    [sendDesktopRename],
  );

  const reorderDesktop = useCallback(
    (move: { desktopId: string; previousDesktopId?: string; nextDesktopId?: string }) =>
      report(
        withFreshDesktopRevisions([move.desktopId], (revisionOf) =>
          sendDesktopReorder({ ...move, expectedRevision: revisionOf(move.desktopId) }),
        ),
      ),
    [report, sendDesktopReorder],
  );

  const selectProfile = useCallback(
    (profileId: string) => {
      if (profileId === useProfilesStore.getState().selectedProfileId) return;
      report(sendProfileSelect(profileId));
    },
    [report, sendProfileSelect],
  );

  const createProfile = useCallback(
    async (name: string): Promise<void> => {
      const created = await sendProfileCreate(name);
      if (created.profile) await sendProfileSelect(created.profile.id);
    },
    [sendProfileCreate, sendProfileSelect],
  );

  const renameProfile = useCallback(
    async (profileId: string, name: string): Promise<void> => {
      const profile = useProfilesStore.getState().profiles.find((entry) => entry.id === profileId);
      if (!profile) return;
      await sendProfileRename(profileId, name, profile.revision);
    },
    [sendProfileRename],
  );

  const deleteProfile = useCallback(
    async (profileId: string, destinationProfileId: string): Promise<void> => {
      const profile = useProfilesStore.getState().profiles.find((entry) => entry.id === profileId);
      if (!profile) return;
      await sendProfileDelete(profileId, profile.revision, destinationProfileId);
    },
    [sendProfileDelete],
  );

  return {
    profiles,
    selectedProfile,
    createProfile,
    renameProfile,
    deleteProfile,
    desktops,
    currentDesktop,
    switchToDesktop,
    switchToSlot,
    moveActiveLeafToDesktop,
    moveActiveLeafToSlot,
    deleteDesktop,
    giveShortcutSlot,
    createDesktop,
    renameDesktop,
    reorderDesktop,
    selectProfile,
  };
}
