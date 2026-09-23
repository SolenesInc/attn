import { useCallback, useMemo } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useSetupsStore } from '../store/setups';
import type { Desktop } from '../types/generated';
import { SetupCommandError } from './daemonSetupEvents';
import { desktopInSlot, desktopLabel, firstFreeSlot, isEmptyDesktop, slotShortcut } from '../utils/desktops';

type ShowNotice = (message: string) => void;

export const FRESH_ARRANGEMENT_TRIPWIRE_MS = 5_000;

function revisionOf(desktopId: string): number | null {
  return useSetupsStore.getState().desktops.find((desktop) => desktop.id === desktopId)?.revision ?? null;
}

function arrangementAdvanced(seen: Map<string, number>): boolean {
  return [...seen].some(([desktopId, revision]) => revisionOf(desktopId) !== revision);
}

function waitForArrangementAfter(seen: Map<string, number>): Promise<void> {
  if (arrangementAdvanced(seen)) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => {
      unsubscribe();
      reject(new Error(
        `Another window changed this desktop and its new layout did not arrive within ${FRESH_ARRANGEMENT_TRIPWIRE_MS / 1000}s. Try again.`,
      ));
    }, FRESH_ARRANGEMENT_TRIPWIRE_MS);
    const unsubscribe = useSetupsStore.subscribe(() => {
      if (!arrangementAdvanced(seen)) return;
      window.clearTimeout(timer);
      unsubscribe();
      resolve();
    });
  });
}

function currentDesktopOf(state: ReturnType<typeof useSetupsStore.getState>): Desktop | undefined {
  const setup = state.setups.find((entry) => entry.id === state.selectedSetupId);
  return state.desktops.find((desktop) => desktop.id === setup?.current_desktop_id);
}

function failureMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function useDesktopNavigation(showNotice: ShowNotice) {
  const {
    sendDesktopSetCurrent,
    sendDesktopSetActivePane,
    sendDesktopMoveLeaf,
    sendDesktopDelete,
    sendDesktopSetShortcutSlot,
    sendDesktopCreate,
    sendSetupSelect,
  } = useDaemonApi();
  const setups = useSetupsStore((state) => state.setups);
  const selectedSetupId = useSetupsStore((state) => state.selectedSetupId);
  const desktops = useSetupsStore((state) => state.desktops);
  const selectedSetup = useMemo(
    () => setups.find((setup) => setup.id === selectedSetupId),
    [selectedSetupId, setups],
  );
  const currentDesktop = useMemo(
    () => desktops.find((desktop) => desktop.id === selectedSetup?.current_desktop_id),
    [desktops, selectedSetup],
  );

  const report = useCallback(
    (action: Promise<unknown>) => {
      void action.catch((err) => showNotice(failureMessage(err)));
    },
    [showNotice],
  );

  const switchToDesktop = useCallback(
    (desktopId: string) => {
      const state = useSetupsStore.getState();
      if (!state.selectedSetupId || currentDesktopOf(state)?.id === desktopId) return;
      report(sendDesktopSetCurrent(state.selectedSetupId, desktopId));
    },
    [report, sendDesktopSetCurrent],
  );

  const switchToSlot = useCallback(
    (slot: number) => {
      const state = useSetupsStore.getState();
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
    async (targetDesktopId: string, retryOnStale: boolean): Promise<void> => {
      const state = useSetupsStore.getState();
      const source = currentDesktopOf(state);
      const target = state.desktops.find((desktop) => desktop.id === targetDesktopId);
      if (!source || !target || source.id === target.id) return;
      const paneId = source.active_pane_id;
      if (!paneId) {
        showNotice('No focused pane to send.');
        return;
      }
      try {
        await sendDesktopMoveLeaf({
          sourceDesktopId: source.id,
          targetDesktopId: target.id,
          leafId: paneId,
          anchorId: target.active_pane_id || undefined,
          edge: 'right',
          expectedSourceRevision: source.revision,
          expectedTargetRevision: target.revision,
        });
      } catch (err) {
        if (retryOnStale && err instanceof SetupCommandError && err.code === 'stale_revision') {
          await waitForArrangementAfter(new Map([[source.id, source.revision], [target.id, target.revision]]));
          return moveActivePane(targetDesktopId, false);
        }
        throw err;
      }
      await sendDesktopSetActivePane(target.id, paneId);
    },
    [sendDesktopMoveLeaf, sendDesktopSetActivePane, showNotice],
  );

  const sendActivePaneToDesktop = useCallback(
    (desktopId: string) => report(moveActivePane(desktopId, true)),
    [moveActivePane, report],
  );

  const sendActivePaneToSlot = useCallback(
    (slot: number) => {
      const target = desktopInSlot(useSetupsStore.getState().desktops, slot);
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
      const state = useSetupsStore.getState();
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
      const state = useSetupsStore.getState();
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
    const setupId = useSetupsStore.getState().selectedSetupId;
    if (!setupId) return;
    report(
      sendDesktopCreate(setupId).then((result) => {
        const created = result.desktops?.[0];
        if (created) return sendDesktopSetCurrent(setupId, created.id);
        return undefined;
      }),
    );
  }, [report, sendDesktopCreate, sendDesktopSetCurrent]);

  const selectSetup = useCallback(
    (setupId: string) => {
      if (setupId === useSetupsStore.getState().selectedSetupId) return;
      report(sendSetupSelect(setupId));
    },
    [report, sendSetupSelect],
  );

  return {
    setups,
    selectedSetup,
    desktops,
    currentDesktop,
    switchToDesktop,
    switchToSlot,
    sendActivePaneToDesktop,
    sendActivePaneToSlot,
    deleteDesktop,
    giveShortcutSlot,
    createDesktop,
    selectSetup,
  };
}
