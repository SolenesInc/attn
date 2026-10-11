import { useEffect, useMemo, useRef } from 'react';
import { useShallow } from 'zustand/react/shallow';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore, type ProfilesState } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { activeLeafOf, sameLeaf, sessionOfLeaf, type ActiveLeaf } from '../navigation/activeLeaf';
import { displayedArrangement } from '../navigation/sessionNavigation';
import { arrangementOf } from '../store/sessionNavigationSlice';
import { isRetainedTerminalEvent, retainTerminalEvent, usePendingTerminalInput, type PendingInputTarget } from '../store/pendingTerminalInput';
import { removeCompositionTextNodes } from '../ghostty/input';

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
        tileSessionId: leaf?.kind === 'tile' ? leaf.tileSessionId : null,
      };
    }),
  );
  return useMemo<ActiveLeaf | null>(() => {
    const { kind, profileId, desktopId, leafId, sessionId, tileSessionId } = fields;
    if (kind === 'agent') return { kind, profileId, desktopId, leafId, sessionId };
    if (kind === 'tile') return { kind, profileId, desktopId, leafId, tileSessionId };
    return null;
  }, [fields]);
}

export type Surface =
  | { kind: 'dashboard' }
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

// The session behind what is on screen, while the session view shows it.
export function useSessionBehindScreen(): string | null {
  const view = useSessionStore((state) => state.view);
  const leaf = useActiveLeaf();
  return view === 'session' ? sessionOfLeaf(leaf) : null;
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
  const intent = useSessionStore((state) => state.intent);
  const sent = useRef(0);
  const reportFailureRef = useRef(reportFailure);
  useEffect(() => usePendingTerminalInput.subscribe((state, previous) => {
    if (state.failure && state.failure !== previous.failure) reportFailureRef.current(state.failure.message);
  }), []);
  useEffect(() => {
    const retain = (event: Event) => {
      const intent = useSessionStore.getState().intent;
      if (event.defaultPrevented || isRetainedTerminalEvent(event)) return;
      const target = event.target instanceof HTMLElement ? event.target : null;
      const terminal = target?.closest<HTMLElement>('.terminal-container');
      if (target?.closest('input, textarea, select, [contenteditable="true"]') && !terminal) return;
      let recipient: PendingInputTarget | null = null;
      if (intent?.target.kind === 'session' && !intent.focusRequested) {
        recipient = { kind: 'agent', id: intent.target.sessionId };
      } else if (terminal?.dataset.terminalInputReady === '0') {
        const { terminalKind, terminalSessionId, terminalRuntimeId } = terminal.dataset;
        if (terminalKind === 'agent' && terminalSessionId) recipient = { kind: 'agent', id: terminalSessionId };
        else if (terminalRuntimeId) recipient = { kind: 'terminal', id: terminalRuntimeId, desktopId: terminal.closest<HTMLElement>('[data-desktop-id]')?.dataset.desktopId ?? '' };
      }
      if (!recipient) return;
      const selectionId = intent && ['session', 'leaf', 'desktop'].includes(intent.target.kind) ? intent.id : 0;
      usePendingTerminalInput.getState().append(recipient, selectionId, retainTerminalEvent(event));
      event.preventDefault();
      event.stopPropagation();
      if (event.type === 'compositionend' && target?.closest('.terminal-container')) removeCompositionTextNodes(target);
    };
    const events = ['keydown', 'keyup', 'paste', 'compositionstart', 'compositionupdate', 'compositionend'];
    for (const event of events) document.addEventListener(event, retain, true);
    return () => {
      for (const event of events) document.removeEventListener(event, retain, true);
    };
  }, []);
  useEffect(() => useSessionStore.subscribe((state, previous) => {
    if (state.intent === previous.intent) return;
    const arrangement = displayedArrangement(arrangementOf(state), state.intent);
    const profiles = useProfilesStore.getState();
    const profile = profiles.profiles.find((entry) => entry.id === arrangement.profileId);
    if (!profile) return;
    profiles.arrangementArrived({ ...profile, current_desktop_id: arrangement.currentDesktopId ?? '' }, arrangement.desktops);
  }), []);
  useEffect(() => {
    reportFailureRef.current = reportFailure;
  }, [reportFailure]);

  useEffect(() => {
    if (!intent?.sendsShow || intent.id === sent.current) return;
    const { id, target, profileId, historyCursor } = intent;
    if (target.kind !== 'session' && target.kind !== 'leaf') return;
    sent.current = id;
    const request = target.kind === 'session'
      ? sendDesktopShowSession(target.sessionId)
      : sendDesktopShowLeaf(target.desktopId, target.leafId);
    request.then(
      () => {
        if (historyCursor === null || target.kind !== 'leaf') return;
        useSessionStore.getState().historyLanded(profileId, historyCursor, { leafId: target.leafId, lastKnownDesktopId: target.desktopId });
      },
      (error: unknown) => {
        const store = useSessionStore.getState();
        const current = store.intent?.id === id;
        const reason = error instanceof Error ? error.message : String(error);
        const discarded = store.intentFailed(id, reason);
        if (!current || discarded) return;
        reportFailureRef.current(`Could not show that ${target.kind === 'session' ? 'agent' : 'leaf'}: ${reason}`);
      },
    );
  }, [intent, sendDesktopShowSession, sendDesktopShowLeaf]);

  useEffect(
    () =>
      useProfilesStore.subscribe((state, previous) => {
        const leaf = activeLeafIn(state);
        const before = activeLeafIn(previous);
        if (!leaf || sameLeaf(leaf, before)) return;
        const sessions = useSessionStore.getState();
        if (sessions.view !== 'session' || sessions.intent) return;
        if (focusBelongsTo(before)) sessions.transferFocus(leaf);
      }),
    [],
  );
}
