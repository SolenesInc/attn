import { useCallback, useMemo } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { useDaemonStore } from '../store/daemonSessions';
import { useAgentOnScreen } from './useDesktopSelectionBridge';
import type { Desktop } from '../types/generated';
import { actThenShow } from '../application/openThenShow';
import { desktopInSlot } from '../utils/desktops';

type ShowError = (message: string) => void;

function currentDesktopOf(state: ReturnType<typeof useProfilesStore.getState>): Desktop | undefined {
  return state.desktops.find((desktop) => desktop.id === state.currentDesktopId);
}

function failureMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function useDesktopNavigation(showError: ShowError) {
  const {
    sendDesktopSetCurrent,
    sendDesktopMoveLeaf,
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
  const agentOnScreenId = useAgentOnScreen();
  const sessions = useDaemonStore((state) => state.daemonSessions);
  const canMoveWithDelegates = useMemo(() => {
    if (!agentOnScreenId) return false;
    const delegates = new Set(sessions
      .filter((session) => session.dispatcher_session_id === agentOnScreenId)
      .map((session) => session.id));
    return desktops.find((desktop) => desktop.id === currentDesktopId)?.panes
      .some((pane) => delegates.has(pane.session_id)) ?? false;
  }, [agentOnScreenId, currentDesktopId, desktops, sessions]);
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
      void action.catch((err) => showError(failureMessage(err)));
    },
    [showError],
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

  // Bouncing back applies only while the user is looking at the slot's desktop. An empty slot gets a desktop.
  const switchToSlot = useCallback(
    (slot: number, looking = true) => {
      const state = useProfilesStore.getState();
      const profileId = state.selectedProfileId;
      const target = desktopInSlot(state.desktops, slot);
      if (!target) {
        if (!profileId) return;
        report(
          sendDesktopCreate(profileId, slot).then((result) => {
            const created = result.desktops?.[0];
            if (created) return sendDesktopSetCurrent(profileId, created.id);
            return undefined;
          }),
        );
        return;
      }
      const toggleBack = looking && target.id === currentDesktopOf(state)?.id && state.previousDesktopId;
      switchToDesktop(toggleBack || target.id);
    },
    [report, sendDesktopCreate, sendDesktopSetCurrent, switchToDesktop],
  );

  const moveActiveLeaf = useCallback(
    async (to: { desktopId: string } | { slot: number }, follow: boolean, withDelegates = false): Promise<void> => {
      const state = useProfilesStore.getState();
      const source = currentDesktopOf(state);
      if (!source || !state.selectedProfileId) return;
      if ('slot' in to ? source.shortcut_slot === to.slot : source.id === to.desktopId) return;
      const leafId = source.active_pane_id;
      if (!leafId) return;
      const move = async () => {
        const result = await sendDesktopMoveLeaf({
          sourceDesktopId: source.id,
          ...('slot' in to ? { targetShortcutSlot: to.slot } : { targetDesktopId: to.desktopId }),
          leafId,
          ...(withDelegates ? { withDelegates: true } : {}),
        });
        const target = result.desktops?.find((desktop) => desktop.id !== source.id) ?? result.desktops?.[0];
        return { targetId: target?.id, leafId: result.pane_id };
      };
      if (!follow) {
        useSessionStore.getState().cancelIntent();
        await move();
        return;
      }
      await actThenShow({ kind: 'move', leafId, sourceDesktopId: source.id, targetDesktopId: 'desktopId' in to ? to.desktopId : undefined }, move, (moved) =>
        moved.leafId && moved.targetId ? { desktopId: moved.targetId, leafId: moved.leafId } : null);
    },
    [sendDesktopMoveLeaf],
  );

  const moveActiveLeafToDesktop = useCallback(
    (desktopId: string, follow: boolean, withDelegates = false) => report(moveActiveLeaf({ desktopId }, follow, withDelegates)),
    [moveActiveLeaf, report],
  );

  const moveActiveLeafToSlot = useCallback(
    (slot: number, follow: boolean) => report(moveActiveLeaf({ slot }, follow)),
    [moveActiveLeaf, report],
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
      sendDesktopRename(desktopId, name).then(() => undefined),
    [sendDesktopRename],
  );

  const reorderDesktop = useCallback(
    (move: { desktopId: string; previousDesktopId?: string; nextDesktopId?: string }) =>
      report(sendDesktopReorder(move)),
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
    async (profileId: string): Promise<void> => {
      const profile = useProfilesStore.getState().profiles.find((entry) => entry.id === profileId);
      if (!profile) return;
      await sendProfileDelete(profileId, profile.revision);
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
    canMoveWithDelegates,
    moveActiveLeafToSlot,
    createDesktop,
    renameDesktop,
    reorderDesktop,
    selectProfile,
  };
}
