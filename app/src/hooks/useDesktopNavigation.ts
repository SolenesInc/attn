import { useCallback, useMemo } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import type { Desktop } from '../types/generated';
import { withFreshDesktopRevisions } from './desktopRevisions';
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
    sendProfileSelect,
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
      if (!state.selectedProfileId || currentDesktopOf(state)?.id === desktopId) return;
      report(sendDesktopSetCurrent(state.selectedProfileId, desktopId));
    },
    [report, sendDesktopSetCurrent],
  );

  const switchToSlot = useCallback(
    (slot: number) => {
      const state = useProfilesStore.getState();
      const target = desktopInSlot(state.desktops, slot);
      if (!target) {
        showNotice(`No desktop on ${slotShortcut(slot)}. Give one a shortcut from the overview.`);
        return;
      }
      if (target.id !== currentDesktopOf(state)?.id) {
        switchToDesktop(target.id);
        return;
      }
      if (state.previousDesktopId) switchToDesktop(state.previousDesktopId);
    },
    [showNotice, switchToDesktop],
  );

  const moveActivePane = useCallback(
    async (targetDesktopId: string): Promise<void> => {
      const state = useProfilesStore.getState();
      const source = currentDesktopOf(state);
      const target = state.desktops.find((desktop) => desktop.id === targetDesktopId);
      if (!source || !target || source.id === target.id) return;
      const leafId = source.active_pane_id;
      if (!leafId) {
        showNotice('No focused pane to send.');
        return;
      }
      await withFreshDesktopRevisions([source.id, target.id], (revisionOf) =>
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
    },
    [sendDesktopMoveLeaf, showNotice],
  );

  const sendActivePaneToDesktop = useCallback(
    (desktopId: string) => report(moveActivePane(desktopId)),
    [moveActivePane, report],
  );

  const sendActivePaneToSlot = useCallback(
    (slot: number) => {
      const target = desktopInSlot(useProfilesStore.getState().desktops, slot);
      if (!target) {
        showNotice(`No desktop on ${slotShortcut(slot)} to send to. Give one a shortcut from the overview.`);
        return;
      }
      sendActivePaneToDesktop(target.id);
    },
    [sendActivePaneToDesktop, showNotice],
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

  const selectProfile = useCallback(
    (profileId: string) => {
      if (profileId === useProfilesStore.getState().selectedProfileId) return;
      report(sendProfileSelect(profileId));
    },
    [report, sendProfileSelect],
  );

  return {
    profiles,
    selectedProfile,
    desktops,
    currentDesktop,
    switchToDesktop,
    switchToSlot,
    sendActivePaneToDesktop,
    sendActivePaneToSlot,
    deleteDesktop,
    giveShortcutSlot,
    createDesktop,
    selectProfile,
  };
}
