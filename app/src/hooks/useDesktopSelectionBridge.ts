import { useEffect, useMemo, useRef } from 'react';
import { useShallow } from 'zustand/react/shallow';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore, type ProfilesState } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { activeLeafOf, sameLeaf, type ActiveLeaf } from '../navigation/activeLeaf';

export function activeLeafIn(state: Pick<ProfilesState, 'selectedProfileId' | 'currentDesktopId' | 'desktops'>): ActiveLeaf | null {
  return activeLeafOf({
    profileId: state.selectedProfileId ?? '',
    currentDesktopId: state.currentDesktopId,
    desktops: state.desktops,
  });
}

export function currentActiveLeaf(): ActiveLeaf | null {
  return activeLeafIn(useProfilesStore.getState());
}

export function shownAgentId(): string | null {
  if (useSessionStore.getState().view !== 'session') return null;
  const leaf = currentActiveLeaf();
  return leaf?.kind === 'agent' ? leaf.sessionId : null;
}

export function useActiveLeaf(): ActiveLeaf | null {
  const fields = useProfilesStore(
    useShallow((state) => {
      const leaf = activeLeafIn(state);
      return {
        kind: leaf?.kind ?? null,
        profileId: leaf?.profileId ?? '',
        desktopId: leaf?.desktopId ?? '',
        leafId: leaf?.leafId ?? '',
        sessionId: leaf?.kind === 'agent' ? leaf.sessionId : '',
      };
    }),
  );
  return useMemo<ActiveLeaf | null>(() => {
    const { kind, profileId, desktopId, leafId, sessionId } = fields;
    if (kind === 'agent') return { kind, profileId, desktopId, leafId, sessionId };
    if (kind === 'tile') return { kind, profileId, desktopId, leafId };
    return null;
  }, [fields]);
}

export type Surface =
  | { kind: 'dashboard' }
  | { kind: 'grid' }
  | { kind: 'tile'; desktopId: string; leafId: string }
  | { kind: 'agent'; sessionId: string | null };

export function useSurface(): Surface {
  const view = useSessionStore((state) => state.view);
  const leaf = useActiveLeaf();
  return useMemo<Surface>(() => {
    if (view !== 'session') return { kind: view };
    if (leaf?.kind === 'tile') return { kind: 'tile', desktopId: leaf.desktopId, leafId: leaf.leafId };
    return { kind: 'agent', sessionId: leaf?.kind === 'agent' ? leaf.sessionId : null };
  }, [view, leaf]);
}

export function useAgentOnScreen(): string | null {
  const surface = useSurface();
  return surface.kind === 'agent' ? surface.sessionId : null;
}

function focusBelongsTo(leaf: ActiveLeaf | null): boolean {
  if (!leaf) return false;
  const focused = document.activeElement;
  const pane = focused instanceof Element ? focused.closest('[data-pane-id]') : null;
  return pane?.getAttribute('data-pane-id') === leaf.leafId && pane.closest(`[data-desktop-id="${leaf.desktopId}"]`) !== null;
}

export function useDesktopSelectionBridge(reportFailure: (message: string) => void) {
  const { sendDesktopShowSession, sendDesktopShowLeaf } = useDaemonApi();
  const pending = useSessionStore((state) => state.pendingSelection);
  const sent = useRef(0);
  const reportFailureRef = useRef(reportFailure);
  useEffect(() => {
    reportFailureRef.current = reportFailure;
  }, [reportFailure]);

  useEffect(() => {
    if (!pending || pending.id === sent.current) return;
    sent.current = pending.id;
    const { id, target } = pending;
    const request = target.kind === 'session'
      ? sendDesktopShowSession(target.sessionId)
      : sendDesktopShowLeaf(target.desktopId, target.leafId);
    request.catch((error: unknown) => {
      const store = useSessionStore.getState();
      if (store.pendingSelection?.id !== id) return;
      store.selectionFailed(id);
      reportFailureRef.current(`Could not show that ${target.kind === 'session' ? 'agent' : 'leaf'}: ${error instanceof Error ? error.message : String(error)}`);
    });
  }, [pending, sendDesktopShowSession, sendDesktopShowLeaf]);

  useEffect(
    () =>
      useProfilesStore.subscribe((state, previous) => {
        const leaf = activeLeafIn(state);
        const before = activeLeafIn(previous);
        if (!leaf || sameLeaf(leaf, before)) return;
        const sessions = useSessionStore.getState();
        if (sessions.view !== 'session' || sessions.pendingSelection) return;
        if (focusBelongsTo(before)) sessions.transferFocus(leaf);
      }),
    [],
  );
}
