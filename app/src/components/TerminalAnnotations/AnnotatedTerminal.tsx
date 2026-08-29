import React, { forwardRef, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  GhosttyTerminal,
  type GhosttyTerminalHandle,
  type GhosttyTerminalProps,
} from '../GhosttyTerminal';
import {
  TerminalAnnotationStore,
  type AnnotatableMessage,
  type MessageAnchor,
  type TerminalAnnotation,
} from '../../utils/terminalAnnotations';
import { buildAnnotationPayload, labelById } from './quickLabels';
import {
  PROMOTED_LABELS,
  QUICK_LABEL_PICKER_GROUPS,
  QUICK_LABEL_PICKER_LABELS,
} from '../../annotations/quickLabels';
import { QuickLabelPicker } from '../../annotations/QuickLabelPicker';
import {
  clampToBounds,
  clampToViewport,
  placePopup,
  type PlaceOptions,
  type Placement,
  type Size,
} from './placement';
import { useAnnotationSend } from '../../annotations/useAnnotationSend';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import './TerminalAnnotations.css';

export interface SessionMessagesResult {
  messages: AnnotatableMessage[];
  status: 'discovering' | 'ready' | 'unavailable';
  detail?: string;
  truncated: boolean;
}

export interface SessionAnnotationsResult {
  annotations: TerminalAnnotation[];
  note: string;
  generation: number;
}

export type SessionAnnotationsSubmitStatus =
  | 'delivered'
  | 'skipped_pending_approval'
  | (string & {});

export interface SessionAnnotationApi {
  fetchMessages: (sessionId: string) => Promise<SessionMessagesResult>;
  subscribeMessagesChanged: (sessionId: string, listener: () => void) => () => void;
  fetchAnnotations: (sessionId: string) => Promise<SessionAnnotationsResult>;
  saveAnnotations: (
    sessionId: string,
    annotations: readonly TerminalAnnotation[],
    note: string,
    generation: number,
  ) => Promise<{ stale: boolean }>;
  clearAnnotations: (sessionId: string, generation: number) => Promise<{ generation: number }>;
  submitAnnotations: (
    sessionId: string,
    text: string,
  ) => Promise<{ status: SessionAnnotationsSubmitStatus }>;
}

type TerminalProps = Omit<
  GhosttyTerminalProps,
  'annotations' | 'annotationsVersion' | 'onAnnotationAnchor' | 'onAnnotationActivate'
>;

export interface AnnotatedTerminalProps extends TerminalProps {
  sessionId: string;
  annotationApi?: SessionAnnotationApi;
  // Gates the send shortcut's *registration*: the dispatcher consumes ⌘Enter
  // whenever a handler exists, so every mounted pane registering would eat it.
  paneActive?: boolean;
}

interface Notice {
  text: string;
  clientX: number;
  clientY: number;
  seq: number;
}

type TerminalSendResult =
  | { kind: 'sent'; count: number; kept: number }
  | { kind: 'skipped' }
  | { kind: 'error'; message: string };

interface Composer {
  annotationId: string;
  clientX: number;
  clientY: number;
  writing: boolean;
}

export const AnnotatedTerminal = forwardRef<GhosttyTerminalHandle, AnnotatedTerminalProps>(
  function AnnotatedTerminal(
    { sessionId, annotationApi, paneActive = false, ...terminalProps },
    ref,
  ) {
    // Built once: `useRef(new …)` would construct and discard a store per render.
    const [store] = useState(() => new TerminalAnnotationStore());
    const [version, setVersion] = useState(0);
    const [composer, setComposer] = useState<Composer | null>(null);
    const [notice, setNotice] = useState<Notice | null>(null);
    const noticeRef = useRef<HTMLDivElement>(null);
    const [noticeAt, setNoticeAt] = useState<Placement | null>(null);
    const windowErrorRef = useRef<string | null>(null);
    const windowStatusRef = useRef<SessionMessagesResult['status']>('discovering');
    const windowDetailRef = useRef<string | null>(null);
    const [draft, setDraft] = useState('');

    const [hint, setHint] = useState<string | null>(null);
    const [labelPickerOpen, setLabelPickerOpen] = useState(false);
    const labelPickerTriggerRef = useRef<HTMLButtonElement>(null);
    const hintProps = useCallback((text: string) => ({
      onMouseEnter: () => setHint(text),
      onMouseLeave: () => setHint((current) => (current === text ? null : current)),
      onFocus: () => setHint(text),
      onBlur: () => setHint((current) => (current === text ? null : current)),
    }), []);
    // Mirrored into a ref: the writes carrying it read in the same tick as a state
    // update that has not landed, which would re-save what was just spent.
    const [note, setNote] = useState('');
    const noteRef = useRef('');
    const writeNote = useCallback((next: string) => {
      noteRef.current = next;
      setNote(next);
    }, []);
    const noteSaveTimerRef = useRef<number | null>(null);
    const commentRef = useRef<HTMLTextAreaElement>(null);
    const popupRef = useRef<HTMLDialogElement>(null);
    const [popupAt, setPopupAt] = useState<Placement | null>(null);
    const [popupDragging, setPopupDragging] = useState(false);
    const popupGrabRef = useRef<{ dx: number; dy: number } | null>(null);
    const popupManuallyPlacedRef = useRef(false);
    const panelRef = useRef<HTMLDivElement>(null);
    const [panelAt, setPanelAt] = useState<Placement | null>(null);
    const [panelDragging, setPanelDragging] = useState(false);
    const panelGrabRef = useRef<{ dx: number; dy: number } | null>(null);
    const generationRef = useRef(0);
    const enabled = Boolean(annotationApi);

    const terminalRef = useRef<GhosttyTerminalHandle | null>(null);
    const attachTerminal = useCallback((handle: GhosttyTerminalHandle | null) => {
      terminalRef.current = handle;
      if (typeof ref === 'function') ref(handle);
      else if (ref) (ref as React.MutableRefObject<GhosttyTerminalHandle | null>).current = handle;
    }, [ref]);

    const bump = useCallback(() => setVersion((value) => value + 1), []);

    const persist = useCallback(() => {
      if (!annotationApi) return;
      generationRef.current += 1;
      const generation = generationRef.current;
      const annotations = store.list().map((entry) => ({ ...entry }));
      void annotationApi.saveAnnotations(sessionId, annotations, noteRef.current, generation)
        .then((result) => {
          if (!result.stale) return;
          return annotationApi.fetchAnnotations(sessionId).then((stored) => {
            store.hydrate(stored.annotations);
            writeNote(stored.note);
            generationRef.current = stored.generation;
            bump();
          });
        })
        .catch(() => {
        });
    }, [annotationApi, bump, sessionId, store, writeNote]);

    const persistRef = useRef(persist);
    useLayoutEffect(() => {
      persistRef.current = persist;
    }, [persist]);

    // Measured: 400ms clears an ordinary inter-keystroke gap (~100-250ms).
    const NOTE_SAVE_PAUSE_MS = 400;
    const scheduleNoteSave = useCallback(() => {
      if (noteSaveTimerRef.current !== null) window.clearTimeout(noteSaveTimerRef.current);
      noteSaveTimerRef.current = window.setTimeout(() => {
        noteSaveTimerRef.current = null;
        persistRef.current();
      }, NOTE_SAVE_PAUSE_MS);
    }, []);

    const flushNoteSave = useCallback(() => {
      if (noteSaveTimerRef.current === null) return;
      window.clearTimeout(noteSaveTimerRef.current);
      noteSaveTimerRef.current = null;
      persistRef.current();
    }, []);
    useEffect(() => flushNoteSave, [flushNoteSave]);

    useEffect(() => {
      if (!enabled || !sessionId) return;
      let cancelled = false;
      void annotationApi!.fetchAnnotations(sessionId)
        .then((stored) => {
          if (cancelled) return;
          store.hydrate(stored.annotations);
          writeNote(stored.note);
          generationRef.current = stored.generation;
          bump();
        })
        .catch(() => {
        });
      return () => {
        cancelled = true;
      };
    }, [annotationApi, bump, enabled, sessionId, store]);

    useEffect(() => {
      if (!enabled || !sessionId) return;
      let cancelled = false;
      let request = 0;
      const refresh = () => {
        const current = ++request;
        void annotationApi!.fetchMessages(sessionId)
          .then((result) => {
            if (cancelled || current !== request) return;
            windowErrorRef.current = null;
            windowStatusRef.current = result.status;
            windowDetailRef.current = result.detail ?? null;
            if (store.setMessages(result.messages)) bump();
          })
          .catch((error: unknown) => {
            if (cancelled || current !== request) return;
            const detail = error instanceof Error ? error.message : String(error);
            windowStatusRef.current = 'unavailable';
            windowDetailRef.current = detail;
            windowErrorRef.current = detail;
            console.warn(`[annotations] ${sessionId}: message window unavailable: ${detail}`);
          });
      };
      const unsubscribe = annotationApi!.subscribeMessagesChanged(sessionId, refresh);
      refresh();
      return () => {
        cancelled = true;
        unsubscribe();
      };
    }, [annotationApi, bump, enabled, sessionId, store]);

    const closeComposer = useCallback((restoreFocus = true) => {
      setComposer(null);
      setLabelPickerOpen(false);
      setHint(null);
      setPopupAt(null);
      setPopupDragging(false);
      popupGrabRef.current = null;
      popupManuallyPlacedRef.current = false;
      setDraft('');
      if (restoreFocus) terminalRef.current?.focus();
    }, []);

    const dismissComposer = useCallback((restoreFocus = true) => {
      const current = composer;
      if (current) {
        const annotation = store.list().find((entry) => entry.id === current.annotationId);
        if (annotation && !annotation.quickLabelId && !annotation.comment) {
          store.remove(current.annotationId);
          persist();
          bump();
        }
      }
      closeComposer(restoreFocus);
    }, [bump, closeComposer, composer, persist, store]);

    useEscapeStack(dismissComposer, Boolean(composer));

    const handleAnchor = useCallback(
      (anchor: MessageAnchor, at: { clientX: number; clientY: number }) => {
        if (composer?.writing) {
          commentRef.current?.focus();
          return;
        }
        const annotation = store.add(anchor.messageKey, anchor.start, anchor.end);
        if (!annotation) return;
        setDraft('');
        setPopupAt(null);
        popupManuallyPlacedRef.current = false;
        setComposer({ annotationId: annotation.id, clientX: at.clientX, clientY: at.clientY, writing: false });
        bump();
      },
      [bump, composer?.writing, store],
    );

    const missSeqRef = useRef(0);
    const handleMiss = useCallback(
      (reason: 'no-messages' | 'outside-messages', at: { clientX: number; clientY: number }) => {
        const text = reason === 'outside-messages'
          ? 'Only what the agent wrote can be annotated. This text is the TUI’s own, your own, or from a turn that has scrolled out of the window.'
          : windowErrorRef.current
            ? 'No transcript could be read for this session, so there is nothing to annotate. The daemon log names the lookup that failed.'
            : windowStatusRef.current === 'discovering'
              ? 'The agent’s first completed message is still being recorded. Try again in a moment.'
              : windowStatusRef.current === 'unavailable'
                ? windowDetailRef.current || 'No exact transcript is available for this session.'
              : 'The agent has not written a message to annotate yet.';
        setNoticeAt(null);
        setNotice({ text, ...at, seq: missSeqRef.current++ });
      },
      [],
    );

    const openAnnotation = useCallback(
      (
        annotationId: string,
        at: { clientX: number; clientY: number },
        options?: { writing?: boolean },
      ) => {
        if (composer?.writing) {
          commentRef.current?.focus();
          return;
        }
        const annotation = store.list().find((entry) => entry.id === annotationId);
        if (!annotation) return;
        setDraft(annotation.comment);
        setPopupAt(null);
        popupManuallyPlacedRef.current = false;
        setComposer({
          annotationId,
          clientX: at.clientX,
          clientY: at.clientY,
          writing: options?.writing || Boolean(annotation.comment),
        });
      },
      [composer?.writing, store],
    );

    // Capture phase, so the terminal's own pointerdown still runs and the click can
    // start the next selection.
    useEffect(() => {
      if (!composer) return;
      const onDown = (event: PointerEvent) => {
        if (labelPickerOpen) return;
        if (popupRef.current?.contains(event.target as Node)) return;
        if (composer.writing) return;
        dismissComposer(false);
      };
      window.addEventListener('pointerdown', onDown, true);
      return () => window.removeEventListener('pointerdown', onDown, true);
    }, [composer, dismissComposer, labelPickerOpen]);

    const placementOptions = useCallback((): PlaceOptions => ({
      bounds: terminalRef.current?.getBounds() ?? null,
      avoid: panelRef.current?.getBoundingClientRect() ?? null,
    }), []);

    const clampPopupToTerminal = useCallback((at: Placement, size: Size) => clampToBounds(
      at,
      size,
      { width: window.innerWidth, height: window.innerHeight },
      terminalRef.current?.getBounds() ?? null,
    ), []);

    const fitToPane = useCallback((
      node: HTMLElement | null,
      anchor: { clientX: number; clientY: number } | null,
      apply: (next: Placement) => void,
    ) => {
      if (!node || !anchor) return;
      const rect = node.getBoundingClientRect();
      apply(placePopup(
        { x: anchor.clientX, y: anchor.clientY },
        { width: rect.width, height: rect.height },
        { width: window.innerWidth, height: window.innerHeight },
        placementOptions(),
      ));
    }, [placementOptions]);

    const composerRef = useRef<Composer | null>(null);

    const applyPopupAt = useCallback((next: Placement) => {
      setPopupAt((current) => (
        current && current.left === next.left && current.top === next.top ? current : next
      ));
    }, []);

    const repositionPopup = useCallback(() => {
      const node = popupRef.current;
      if (popupManuallyPlacedRef.current) {
        if (!node) return;
        const rect = node.getBoundingClientRect();
        setPopupAt((current) => (current ? clampPopupToTerminal(
          current,
          { width: rect.width, height: rect.height },
        ) : current));
        return;
      }
      fitToPane(popupRef.current, composerRef.current, applyPopupAt);
    }, [applyPopupAt, clampPopupToTerminal, fitToPane]);

    useLayoutEffect(() => {
      composerRef.current = composer;
      repositionPopup();
    }, [composer, panelAt, repositionPopup, version]);

    useEffect(() => {
      const node = popupRef.current;
      if (!composer || !node || typeof ResizeObserver === 'undefined') return;
      const observer = new ResizeObserver(() => repositionPopup());
      observer.observe(node);
      return () => observer.disconnect();
    }, [composer, repositionPopup]);

    useEffect(() => {
      if (!composer) return;
      window.addEventListener('resize', repositionPopup);
      return () => window.removeEventListener('resize', repositionPopup);
    }, [composer, repositionPopup]);

    useEffect(() => {
      if (!composer?.writing) return;
      const box = commentRef.current;
      if (!box) return;
      box.focus();
      box.setSelectionRange(box.value.length, box.value.length);
    }, [composer?.writing, composer?.annotationId]);

    const annotations = store.list();
    const composed = composer
      ? annotations.find((entry) => entry.id === composer.annotationId) ?? null
      : null;
    const selectedPickerLabel = composed
      ? QUICK_LABEL_PICKER_LABELS.find((label) => label.id === composed.quickLabelId)
      : undefined;

    const applyLabel = (quickLabelId: string) => {
      if (!composed) return;
      const next = composed.quickLabelId === quickLabelId ? '' : quickLabelId;
      store.update(composed.id, { quickLabelId: next });
      if (!next && !composed.comment && !composer?.writing) store.remove(composed.id);
      persist();
      bump();
      if (!composed.comment && !composer?.writing) closeComposer();
    };

    const saveComment = () => {
      if (!composed) return;
      store.update(composed.id, { comment: draft.trim() });
      if (!draft.trim() && !composed.quickLabelId) store.remove(composed.id);
      persist();
      bump();
      closeComposer();
    };

    const removeAnnotation = (id: string, restoreFocus = true) => {
      store.remove(id);
      if (composer?.annotationId === id) closeComposer(restoreFocus);
      persist();
      bump();
    };

    const reopen = (annotation: TerminalAnnotation, at: { clientX: number; clientY: number }) => {
      openAnnotation(annotation.id, at, { writing: true });
    };

    const reopenFromCard = (annotation: TerminalAnnotation, event: React.MouseEvent) => {
      const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
      reopen(annotation, { clientX: rect.left + rect.width / 2, clientY: rect.top });
    };

    const startPopupDrag = (event: React.MouseEvent) => {
      const node = popupRef.current;
      if (!node || event.button !== 0) return;
      const rect = node.getBoundingClientRect();
      popupGrabRef.current = { dx: event.clientX - rect.left, dy: event.clientY - rect.top };
      popupManuallyPlacedRef.current = true;
      setPopupAt({ left: rect.left, top: rect.top });
      setPopupDragging(true);
      event.preventDefault();
    };

    const focusCommentFromPopupBackground = (target: EventTarget) => {
      if (!composer?.writing || !(target instanceof Element)) return;
      if (target === commentRef.current || target.closest('button, .anno-popup-drag-handle')) return;
      commentRef.current?.focus();
    };

    const movePopupWithKeyboard = (event: React.KeyboardEvent<HTMLButtonElement>) => {
      const directions: Partial<Record<string, Placement>> = {
        ArrowLeft: { left: -1, top: 0 },
        ArrowRight: { left: 1, top: 0 },
        ArrowUp: { left: 0, top: -1 },
        ArrowDown: { left: 0, top: 1 },
      };
      const direction = directions[event.key];
      const node = popupRef.current;
      if (!direction || !node) return;
      const distance = event.shiftKey ? 40 : 10;
      const rect = node.getBoundingClientRect();
      popupManuallyPlacedRef.current = true;
      setPopupAt((current) => clampPopupToTerminal(
        {
          left: (current?.left ?? rect.left) + direction.left * distance,
          top: (current?.top ?? rect.top) + direction.top * distance,
        },
        { width: rect.width, height: rect.height },
      ));
      event.preventDefault();
    };

    const startPanelDrag = (event: React.MouseEvent) => {
      const node = panelRef.current;
      if (!node || event.button !== 0) return;
      const rect = node.getBoundingClientRect();
      panelGrabRef.current = { dx: event.clientX - rect.left, dy: event.clientY - rect.top };
      setPanelAt({ left: rect.left, top: rect.top });
      setPanelDragging(true);
      event.preventDefault();
    };

    const performSend = (): Promise<TerminalSendResult | null> | null => {
      if (annotations.length === 0 || !annotationApi) return null;
      flushNoteSave();
      if (composed && composer?.writing) {
        const comment = draft.trim();
        store.update(composed.id, { comment });
        if (!comment && !composed.quickLabelId) store.remove(composed.id);
      }
      // Copied, not referenced: `list()` hands back the store's own array, and this
      // is held across the round trip, where a mark made mid-flight must not land.
      const sending = store.list().map((entry) => ({ ...entry }));
      closeComposer();
      bump();
      if (sending.length === 0) {
        persist();
        return null;
      }
      const sendingNote = note.trim();
      const payload = buildAnnotationPayload(sending.map((entry) => ({
        quote: entry.quote,
        quickLabelId: entry.quickLabelId,
        comment: entry.comment,
        start: entry.start,
      })), sendingNote);
      return annotationApi.submitAnnotations(sessionId, payload)
        .then((result) => {
          if (result.status !== 'delivered') {
            return result.status === 'skipped_pending_approval'
              ? { kind: 'skipped' }
              : { kind: 'error', message: 'The session did not take the feedback. Nothing was sent.' };
          }
          const current = new Map(store.list().map((entry) => [entry.id, entry]));
          sending.forEach((entry) => {
            const now = current.get(entry.id);
            if (!now) return;
            if (now.quickLabelId !== entry.quickLabelId || now.comment !== entry.comment) return;
            store.remove(entry.id);
          });
          if (sendingNote && noteRef.current.trim() === sendingNote) writeNote('');
          const kept = store.list().length;
          bump();
          if (kept > 0 || noteRef.current.trim()) {
            persist();
            return { kind: 'sent', count: sending.length, kept };
          }
          generationRef.current += 1;
          void annotationApi.clearAnnotations(sessionId, generationRef.current)
            .then((cleared) => {
              generationRef.current = Math.max(generationRef.current, cleared.generation);
            })
            .catch(() => {
            });
          return { kind: 'sent', count: sending.length, kept };
        });
    };

    const { outcome, send } = useAnnotationSend<TerminalSendResult>({
      send: performSend,
      shortcutId: 'terminal.sendAnnotations',
      enabled: enabled && paneActive && annotations.length > 0,
      sentClearMs: 2200,
    });

    useLayoutEffect(() => {
      fitToPane(noticeRef.current, notice, setNoticeAt);
    }, [fitToPane, notice]);

    useEffect(() => {
      if (!notice) return;
      const timer = window.setTimeout(() => setNotice(null), 5000);
      return () => window.clearTimeout(timer);
    }, [notice]);

    useEffect(() => {
      if (composer) setNotice(null);
    }, [composer]);

    useEffect(() => {
      if (!popupDragging) return;
      const onMove = (event: MouseEvent) => {
        const grab = popupGrabRef.current;
        const node = popupRef.current;
        if (!grab || !node) return;
        const rect = node.getBoundingClientRect();
        setPopupAt(clampPopupToTerminal(
          { left: event.clientX - grab.dx, top: event.clientY - grab.dy },
          { width: rect.width, height: rect.height },
        ));
      };
      const onUp = () => {
        popupGrabRef.current = null;
        setPopupDragging(false);
      };
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
      return () => {
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      };
    }, [clampPopupToTerminal, popupDragging]);

    useEffect(() => {
      if (!panelDragging) return;
      const onMove = (event: MouseEvent) => {
        const grab = panelGrabRef.current;
        const node = panelRef.current;
        if (!grab || !node) return;
        const rect = node.getBoundingClientRect();
        setPanelAt(clampToViewport(
          { left: event.clientX - grab.dx, top: event.clientY - grab.dy },
          { width: rect.width, height: rect.height },
          { width: window.innerWidth, height: window.innerHeight },
        ));
      };
      const onUp = () => setPanelDragging(false);
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
      return () => {
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      };
    }, [panelDragging]);

    useEffect(() => {
      if (!panelAt) return;
      const onResize = () => {
        const node = panelRef.current;
        if (!node) return;
        const rect = node.getBoundingClientRect();
        setPanelAt((current) => (current ? clampToViewport(
          current,
          { width: rect.width, height: rect.height },
          { width: window.innerWidth, height: window.innerHeight },
        ) : current));
      };
      window.addEventListener('resize', onResize);
      return () => window.removeEventListener('resize', onResize);
    }, [panelAt]);

    const panelOpen = annotations.length > 0 || outcome?.kind === 'sent';

    const outcomeText = outcome?.kind === 'skipped'
      ? 'Not sent — the session is waiting on an approval, where the sending Enter would answer it. Send again once you have answered.'
      : outcome?.kind === 'error'
        ? outcome.message
        : outcome?.kind === 'sent' && outcome.kept > 0
          ? `✓ sent ${outcome.count} to the session. ${outcome.kept} still here — annotated or changed while it was sending, so not part of what went.`
          : null;

    return (
      <>
        <GhosttyTerminal
          {...terminalProps}
          ref={attachTerminal}
          annotations={enabled ? store : undefined}
          annotationsVersion={version}
          onAnnotationAnchor={enabled ? handleAnchor : undefined}
          onAnnotationMiss={enabled ? handleMiss : undefined}
          onAnnotationActivate={enabled ? openAnnotation : undefined}
        />
        {/* Portalled out of the pane: these are positioned against the window and reach
            past the pane's edge, which its stacking context would draw under the chrome. */}
        {createPortal(
          <>
        {notice ? (
          <div
            ref={noticeRef}
            className={`anno-notice${noticeAt ? ' anno-notice--placed' : ''}`}
            data-testid="annotation-notice"
            role="status"
            style={noticeAt
              ? { left: noticeAt.left, top: noticeAt.top }
              : { left: notice.clientX, top: notice.clientY }}
          >
            {notice.text}
          </div>
        ) : null}
        {composed && composer ? (
          <dialog
            open
            ref={popupRef}
            className={`anno-popup${popupAt ? ' anno-popup--placed' : ''}${popupDragging ? ' anno-popup--dragging' : ''}`}
            data-testid="annotation-popup"
            aria-label="Edit terminal annotation"
            style={popupAt
              ? { left: popupAt.left, top: popupAt.top }
              : { left: composer.clientX, top: composer.clientY }}
            onMouseDown={(event) => {
              if (event.target === commentRef.current) return;
              event.preventDefault();
              focusCommentFromPopupBackground(event.target);
            }}
            onMouseUp={(event) => focusCommentFromPopupBackground(event.target)}
          >
            {composer.writing ? (
              <button
                type="button"
                className="anno-popup-drag-handle"
                data-testid="annotation-popup-drag-handle"
                title="Drag to move; use arrow keys for precise movement"
                aria-label="Move comment editor with arrow keys"
                onMouseDown={startPopupDrag}
                onKeyDown={movePopupWithKeyboard}
              >
                <span aria-hidden="true">⠿</span>
              </button>
            ) : null}
            <QuickLabelPicker
              mode="chips"
              className="anno-popup-labels"
              groups={[PROMOTED_LABELS]}
              isSelected={(label) => composed.quickLabelId === label.id}
              onSelect={(label) => applyLabel(label.id)}
              onHint={setHint}
            >
              <button
                ref={labelPickerTriggerRef}
                type="button"
                className={`anno-popup-label${selectedPickerLabel ? ' anno-popup-label--on' : ''}`}
                title="More labels"
                aria-label="More labels"
                aria-expanded={labelPickerOpen}
                onClick={() => setLabelPickerOpen(!labelPickerOpen)}
                {...hintProps('More labels')}
              >
                {selectedPickerLabel?.emoji ?? '⚡'}
              </button>
              <span className="anno-popup-divider" />
              <button
                type="button"
                className="anno-popup-label"
                title="Write a comment"
                aria-label="Write a comment"
                onClick={() => setComposer((current) => (current ? { ...current, writing: true } : current))}
                {...hintProps('Write a comment')}
              >
                💬
              </button>
              <button
                type="button"
                className="anno-popup-label anno-popup-label--delete"
                title="Remove this annotation"
                aria-label="Remove this annotation"
                onClick={() => removeAnnotation(composed.id)}
                {...hintProps('Remove this annotation')}
              >
                🗑
              </button>
            </QuickLabelPicker>
            {labelPickerOpen && labelPickerTriggerRef.current ? (
              <QuickLabelPicker
                className="md-quick-label-picker"
                groups={QUICK_LABEL_PICKER_GROUPS}
                anchorEl={labelPickerTriggerRef.current}
                onSelect={(label) => {
                  setLabelPickerOpen(false);
                  setHint(null);
                  applyLabel(label.id);
                }}
                onDismiss={() => {
                  setLabelPickerOpen(false);
                  setHint(null);
                }}
                onHint={setHint}
              />
            ) : null}
            <div className="anno-popup-hint" data-testid="annotation-popup-hint">
              {hint ?? labelById(composed.quickLabelId)?.text ?? 'Pick a label, or write a comment'}
            </div>
            {composer.writing ? (
              <div className="anno-popup-compose">
                <blockquote className="anno-popup-quote">{composed.quote}</blockquote>
                <textarea
                  ref={commentRef}
                  className="anno-popup-text"
                  value={draft}
                  aria-label="Annotation comment"
                  placeholder="What should change here?"
                  onChange={(event) => setDraft(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                      event.preventDefault();
                      saveComment();
                    }
                  }}
                />
                <div className="anno-popup-actions">
                  <button
                    type="button"
                    className="anno-popup-remove"
                    onClick={() => removeAnnotation(composed.id)}
                  >
                    Remove
                  </button>
                  <span className="anno-popup-actions-gap" />
                  <button type="button" className="anno-popup-cancel" onClick={() => dismissComposer()}>
                    Cancel
                  </button>
                  <button type="button" className="anno-popup-save" onClick={saveComment}>
                    Comment
                  </button>
                </div>
              </div>
            ) : null}
          </dialog>
        ) : null}
          </>,
          document.body,
        )}
        {panelOpen ? (
          <div
            ref={panelRef}
            className={`anno-panel${panelDragging ? ' anno-panel--dragging' : ''}`}
            data-testid="annotation-panel"
            style={panelAt ? { left: panelAt.left, top: panelAt.top, right: 'auto', bottom: 'auto' } : undefined}
          >
            <div className="anno-panel-head" onMouseDown={startPanelDrag}>
              <span className="anno-panel-grip" aria-hidden="true">⠿</span>
              <span className="anno-panel-title">Annotations</span>
              <span className="anno-panel-count">{annotations.length}</span>
            </div>
            <div className="anno-panel-body">
              {annotations.map((annotation) => (
                <div key={annotation.id} className="anno-card">
                  <button
                    type="button"
                    className="anno-card-open"
                    title="Edit this annotation"
                    onClick={(event) => reopenFromCard(annotation, event)}
                  >
                    <span className="anno-card-chip">
                      {labelById(annotation.quickLabelId)?.emoji || '💬'}
                    </span>
                    <span className="anno-card-quote">{annotation.quote}</span>
                    {annotation.comment ? (
                      <span className="anno-card-comment">{annotation.comment}</span>
                    ) : null}
                  </button>
                  <button
                    type="button"
                    className="anno-card-remove"
                    title="Remove annotation"
                    aria-label="Remove annotation"
                    onClick={() => removeAnnotation(annotation.id, false)}
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
            <textarea
              className="anno-panel-note"
              data-testid="annotation-note"
              value={note}
              aria-label="Note sent with these annotations"
              placeholder="Anything else to say with these?"
              onChange={(event) => {
                writeNote(event.target.value);
                scheduleNoteSave();
              }}
              onBlur={flushNoteSave}
              // The dispatcher stays out of native editable targets, so ⌘Enter is
              // bound here too.
              onKeyDown={(event) => {
                if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                  event.preventDefault();
                  send();
                }
              }}
            />
            {outcomeText ? (
              <div className="anno-panel-outcome" data-testid="annotation-send-note" role="status">
                {outcomeText}
              </div>
            ) : null}
            <div className="anno-panel-foot">
              {outcome?.kind === 'sent' && outcome.kept === 0 ? (
                <span className="anno-panel-sent">✓ sent {outcome.count} to the session</span>
              ) : (
                <>
                  <span className="anno-panel-n">
                    {annotations.length} annotation{annotations.length === 1 ? '' : 's'}
                  </span>
                  <button
                    type="button"
                    className="anno-panel-send"
                    onClick={send}
                    disabled={outcome?.kind === 'sending'}
                  >
                    {outcome?.kind === 'sending' ? 'Sending…' : 'Send all'}
                    {paneActive && outcome?.kind !== 'sending' ? (
                      <span className="anno-panel-send-key">{formatShortcut('terminal.sendAnnotations')}</span>
                    ) : null}
                  </button>
                </>
              )}
            </div>
          </div>
        ) : null}
      </>
    );
  },
);
