import type { FocusEvent as ReactFocusEvent } from 'react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useAnnotationSend } from '../../annotations/useAnnotationSend';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import { useFrameScheduler, useTileWindow } from '../../tileWindow';
import type { MarkdownAnnotationsSendHandle, ReviewSend } from '../MarkdownReader';
import type { MarkdownAnnotationsDestination, MarkdownAnnotationsSubmitOptions } from '../MarkdownReader/annotations/transport';
import { getMarkdownAnnotationsTransport } from '../MarkdownReader/annotations/transport';
import { type MarkdownDocumentSource } from '../MarkdownReader/documentSource';
import { type SeedDocument } from '../SeedDocumentView';

import type { DesktopTileSessionOption } from './DesktopDockTile';
type MarkdownSendResult =
  | { kind: 'sent'; destination: 'session' | 'seed'; kept: number }
  | { kind: 'skipped' }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string };

const SEND_SENT_CLEAR_MS = 4000;
const SKIPPED_APPROVAL_MESSAGE = 'Target is waiting for approval — not sent';
const NOT_HYDRATED_MESSAGE = 'Annotations are still syncing — try again in a moment';
interface Options {
  desktopId: string;
  leafId: string;
  popped: boolean;
  isSeed: boolean;
  visible: boolean;
  path: string;
  documentSource: MarkdownDocumentSource;
  seedDocument: SeedDocument | null;
  desktopSessions: DesktopTileSessionOption[];
  boundSessionIdInput?: string;
  onRetargetTile?: (sessionId: string) => Promise<unknown> | void;
  /** The head of the review the tile shows; absent for any other document. */
  reviewHead?: string;
}
export function useTileAnnotations({
  desktopId,
  leafId,
  popped,
  isSeed,
  visible,
  path,
  documentSource,
  seedDocument,
  desktopSessions,
  boundSessionIdInput,
  onRetargetTile,
  reviewHead,
}: Options) {
  const isReview = reviewHead !== undefined;
  const seedTenderSessionId = seedDocument?.seed.claimed
    ? seedDocument.seed.tender?.session_id ?? ''
    : '';
  const annotationsSendRef = useRef<MarkdownAnnotationsSendHandle | null>(null);
  const [annotationCount, setAnnotationCount] = useState(0);
  // Gates the ⌘Enter shortcut's registration: with focus in a terminal pane the shortcut
  // must not exist at all, so the key falls through to the PTY untouched.
  const [hasFocusWithin, setHasFocusWithin] = useState(false);

  const {
    targetSessionId,
    targetSessionLabel,
    retargetSession: requestRetarget,
  } = useAnnotationTarget({ desktopSessions, boundSessionIdInput, onRetargetTile });
  const primaryDestination = useMemo<MarkdownAnnotationsDestination | null>(() => {
    if (isSeed) {
      if (!seedDocument || !path) return null;
      return seedTenderSessionId
        ? { kind: 'session', sessionId: seedTenderSessionId }
        : { kind: 'seed', seedId: path };
    }
    return targetSessionId ? { kind: 'session', sessionId: targetSessionId } : null;
  }, [isSeed, path, seedDocument, seedTenderSessionId, targetSessionId]);
  const transportAvailable = getMarkdownAnnotationsTransport() !== null;

  const performAnnotationSend = useCallback(
    (
      destination: MarkdownAnnotationsDestination | null,
      options?: MarkdownAnnotationsSubmitOptions,
    ): MarkdownSendResult | null | Promise<MarkdownSendResult> => {
      const handle = annotationsSendRef.current;
      const transport = getMarkdownAnnotationsTransport();
      if ((annotationCount === 0 && !options) || !destination || !handle || !transport || !path) {
        return null;
      }
      if (!handle.isHydrated()) {
        // The daemon draft has not been loaded (hydrate in flight or failed), so the daemon would
        // format a STALE stored draft. Refuse rather than mis-deliver.
        return { kind: 'error', message: NOT_HYDRATED_MESSAGE };
      }
      return (async () => {
        // Flush the 500ms save debounce first so the daemon formats a draft that includes the
        // last keystroke's edit.
        await handle.flushPendingSave();
        const result = await transport.submitMarkdownAnnotations(
          documentSource,
          destination,
          handle.getOrphanedIds(),
          options,
        );
        if ((result.status === 'delivered' || result.status === 'noted') && result.error) {
          return { kind: 'warning', message: result.error };
        }
        if (result.status === 'delivered' || result.status === 'noted') {
          const kept = result.draft ? handle.adoptSentDraft(documentSource.uri, result.draft) : 0;
          return { kind: 'sent', destination: result.status === 'noted' ? 'seed' : 'session', kept };
        }
        if (result.status === 'skipped_pending_approval') {
          return { kind: 'skipped' };
        }
        return { kind: 'error', message: result.error || 'Send failed' };
      })();
    },
    [annotationCount, documentSource, path],
  );

  const sendEnabled =
    visible && hasFocusWithin && (annotationCount > 0 || isReview) && !!primaryDestination && transportAvailable;
  const {
    outcome: sendOutcome,
    send: sendNow,
    sendAlternative,
    clearOutcome: clearSendOutcome,
  } = useAnnotationSend<MarkdownSendResult>({
    send: () => {
      if (annotationsSendRef.current?.openReviewSend()) return null;
      return performAnnotationSend(primaryDestination);
    },
    shortcutId: 'markdown.sendAnnotations',
    enabled: sendEnabled,
    shortcutScope: { desktopId, leafId, active: !popped },
    sentClearMs: SEND_SENT_CLEAR_MS,
  });
  const sendStatus = sendOutcome ?? { kind: 'idle' as const };

  const reviewDestination = primaryDestination?.kind === 'session' ? targetSessionLabel : null;
  const reviewSend = useMemo<ReviewSend | undefined>(() => (isReview
    ? {
        destination: reviewDestination,
        ask: async (id: string) => {
          const result = await performAnnotationSend(primaryDestination, { onlyIds: [id] });
          if (result === null) return 'no session to ask';
          if (result.kind === 'sent') return null;
          return result.kind === 'skipped' ? SKIPPED_APPROVAL_MESSAGE : result.message;
        },
        send: (verdict: string) => sendAlternative(() => performAnnotationSend(primaryDestination, { verdict, head: reviewHead })),
      }
    : undefined), [isReview, performAnnotationSend, primaryDestination, reviewDestination, reviewHead, sendAlternative]);

  const handleTileFocus = useCallback(() => {
    setHasFocusWithin(true);
  }, []);
  const handleTileBlur = useCallback((event: ReactFocusEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setHasFocusWithin(false);
    }
  }, []);

  const sending = sendStatus.kind === 'sending';
  const sendDisabled =
    sending || (annotationCount === 0 && !isReview) || !primaryDestination || !transportAvailable;
  const {
    destinationGroupRef,
    destinationCaretRef,
    destinationMenuRef,
    destinationMenuOpen,
    setOpenDestinationMenuKey,
    destinationMenuKey,
    closeDestinationMenu,
  } = useAnnotationDestinationMenu({
    isSeed,
    seedTenderSessionId,
    seedDocument,
    desktopSessions,
  });

  const retargetSession = (sessionId: string) => {
    closeDestinationMenu();
    if (!sessionId || sessionId === targetSessionId) return;
    clearSendOutcome();
    requestRetarget(sessionId);
  };
  const {
    sendHasProblem,
    sendStatusMessage,
    sendActionLabel,
    showSessionSendAction,
    sendButtonTitle,
  } = annotationSendPresentation(
    sendStatus,
    isSeed,
    seedTenderSessionId,
    annotationCount,
    targetSessionId,
    targetSessionLabel,
    isReview,
  );
  return {
    annotationsSendRef,
    annotationCount,
    setAnnotationCount,
    hasFocusWithin,
    handleTileFocus,
    handleTileBlur,
    clearSendOutcome,
    sendStatusMessage,
    destinationGroupRef,
    seedTenderSessionId,
    sendStatus,
    sendHasProblem,
    sendDisabled,
    sendButtonTitle,
    sendNow,
    sendActionLabel,
    destinationCaretRef,
    destinationMenuOpen,
    setOpenDestinationMenuKey,
    destinationMenuKey,
    destinationMenuRef,
    closeDestinationMenu,
    sendAlternative,
    performAnnotationSend,
    showSessionSendAction,
    targetSessionId,
    targetSessionLabel,
    retargetSession,
    reviewSend,
  };
}

function sendDoneLabel(destination: 'session' | 'seed') {
  return destination === 'seed' ? 'Noted ✓' : 'Sent ✓';
}

function annotationSendPresentation(
  sendStatus: MarkdownSendResult | { kind: 'idle' | 'sending' },
  isSeed: boolean,
  seedTenderSessionId: string,
  annotationCount: number,
  targetSessionId: string,
  targetSessionLabel: string,
  isReview: boolean,
) {
  const sendHasProblem =
    sendStatus.kind === 'skipped' || sendStatus.kind === 'warning' || sendStatus.kind === 'error';
  const sendStatusMessage =
    sendStatus.kind === 'sending'
      ? isSeed && !seedTenderSessionId
        ? 'Noting…'
        : 'Sending…'
      : sendStatus.kind === 'sent'
        ? `${sendDoneLabel(sendStatus.destination)}${sendStatus.kept > 0 ? ` — ${sendStatus.kept} still here` : ''}`
        : sendStatus.kind === 'skipped'
          ? SKIPPED_APPROVAL_MESSAGE
          : sendStatus.kind === 'warning' || sendStatus.kind === 'error'
            ? sendStatus.message
            : null;
  const sendActionLabel =
    sendStatus.kind === 'sending'
      ? (sendStatusMessage as string)
      : sendStatus.kind === 'sent'
        ? sendDoneLabel(sendStatus.destination)
        : sendStatus.kind === 'skipped'
        ? 'Approval needed'
        : sendStatus.kind === 'warning'
          ? 'Needs attention'
          : sendStatus.kind === 'error'
            ? 'Send failed'
            : isSeed && !seedTenderSessionId
              ? `Note on seed ${annotationCount}`
              : isReview
                ? annotationCount > 0 ? `Review ${annotationCount}` : 'Review'
                : `Send ${annotationCount}`;
  const showSessionSendAction = annotationCount > 0 || isReview || sendStatus.kind !== 'idle';
  const sendButtonTitle = sendHasProblem
    ? (sendStatusMessage ?? undefined)
    : isSeed
      ? seedTenderSessionId
        ? `Send annotations to the tending session (${formatShortcut('markdown.sendAnnotations')})`
        : `Leave annotations as a note on the seed (${formatShortcut('markdown.sendAnnotations')})`
      : targetSessionId
        ? isReview
          ? `Send the review to ${targetSessionLabel} with a verdict (${formatShortcut('markdown.sendAnnotations')})`
          : `Send annotations to ${targetSessionLabel} (${formatShortcut('markdown.sendAnnotations')})`
        : 'Choose a session before sending annotations';

  return {
    sendHasProblem,
    sendStatusMessage,
    sendActionLabel,
    showSessionSendAction,
    sendButtonTitle,
  };
}

function useAnnotationDestinationMenu({
  isSeed,
  seedTenderSessionId,
  seedDocument,
  desktopSessions,
}: Pick<Options, 'isSeed' | 'seedDocument' | 'desktopSessions'> & {
  seedTenderSessionId: string;
}) {
  const destinationMenuKey = isSeed
    ? seedTenderSessionId && seedDocument
      ? `seed:${seedTenderSessionId}:${seedDocument.seed.rev}`
      : null
    : desktopSessions.length > 0
      ? `session:${desktopSessions.map((session) => `${session.sessionId}:${session.state ?? ''}`).join('|')}`
      : null;
  const [openDestinationMenuKey, setOpenDestinationMenuKey] = useState<string | null>(null);
  const destinationMenuOpen =
    destinationMenuKey !== null && openDestinationMenuKey === destinationMenuKey;
  const destinationGroupRef = useRef<HTMLDivElement>(null);
  const destinationCaretRef = useRef<HTMLButtonElement>(null);
  const destinationMenuRef = useRef<HTMLDivElement>(null);
  const tileWindow = useTileWindow();
  const frames = useFrameScheduler();
  const closeDestinationMenu = useCallback((restoreFocus = false) => {
    setOpenDestinationMenuKey(null);
    if (restoreFocus) {
      frames.request(() => destinationCaretRef.current?.focus());
    }
  }, [frames]);
  useEscapeStack(() => closeDestinationMenu(true), destinationMenuOpen);
  useEffect(() => {
    if (!destinationMenuOpen) return;
    destinationMenuRef.current
      ?.querySelector<HTMLButtonElement>('[role="menuitem"], [role="menuitemradio"]')
      ?.focus();
    const handleMouseDown = (event: MouseEvent) => {
      if (!destinationGroupRef.current?.contains(event.target as Node)) {
        closeDestinationMenu();
      }
    };
    const tileDocument = tileWindow.document;
    tileDocument.addEventListener('mousedown', handleMouseDown);
    return () => tileDocument.removeEventListener('mousedown', handleMouseDown);
  }, [closeDestinationMenu, destinationMenuOpen, tileWindow]);

  return {
    destinationGroupRef,
    destinationCaretRef,
    destinationMenuRef,
    destinationMenuOpen,
    setOpenDestinationMenuKey,
    destinationMenuKey,
    closeDestinationMenu,
  };
}

function useAnnotationTarget({
  desktopSessions,
  boundSessionIdInput,
  onRetargetTile,
}: Pick<Options, 'desktopSessions' | 'boundSessionIdInput' | 'onRetargetTile'>) {
  // `tile.tileSessionId` only updates when the daemon's layout broadcast echoes the
  // rebind back, which can lag the click; the user's pick is held locally meanwhile.
  const boundSessionId = boundSessionIdInput ?? '';
  const [pendingTargetSessionId, setPendingTargetSessionId] = useState<string | null>(null);
  if (pendingTargetSessionId !== null && boundSessionId === pendingTargetSessionId) {
    setPendingTargetSessionId(null);
  }
  const pendingInDesktop =
    pendingTargetSessionId !== null &&
    desktopSessions.some((s) => s.sessionId === pendingTargetSessionId);
  const boundInDesktop = desktopSessions.some((s) => s.sessionId === boundSessionId);
  const targetSessionId = pendingInDesktop
    ? (pendingTargetSessionId as string)
    : boundInDesktop
      ? boundSessionId
      : '';
  const targetSession = desktopSessions.find((session) => session.sessionId === targetSessionId);
  const targetSessionLabel = targetSession?.label ?? 'No session';
  const retargetSession = useCallback(
    (sessionId: string) => {
      if (!sessionId || sessionId === targetSessionId) {
        return;
      }
      setPendingTargetSessionId(sessionId);
      void Promise.resolve(onRetargetTile?.(sessionId)).catch((error) => {
        console.warn('[DesktopDockTile] Failed to retarget tile session:', error);
        setPendingTargetSessionId((prev) => (prev === sessionId ? null : prev));
      });
    },
    [onRetargetTile, targetSessionId],
  );
  return { targetSessionId, targetSessionLabel, retargetSession };
}
