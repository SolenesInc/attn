// Mount this in the OUTER MarkdownReader, outside the memo-gated body: the content
// effect must fire when the body remounted, or the pass clears still-valid Ranges.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { RefObject } from 'react';
import { createAnchor } from '../anchoring/create';
import { resolveDomRange } from '../anchoring/domRange';
import { extractBlockTexts } from '../anchoring/extractBlocks';
import { createHighlightPainter } from '../anchoring/painter';
import type { HighlightKind, HighlightPainter } from '../anchoring/painter';
import { resolveOrRebase } from '../anchoring/resolve';
import type { BlockText, OrphanReason } from '../anchoring/types';
import {
  registerMarkdownAnnotationsAutomationHandle,
  type MarkdownAnnotationsAutomationState,
} from './annotationsAutomation';
import { evaluateSelection, type PendingSelection } from './selection';
import { getMarkdownAnnotationsTransport, type MarkdownAnnotationsTransport } from './transport';
import type { MarkdownAnnotationsDraft } from './transport';
import {
  announceDocumentDraft,
  listenToDocumentDraft,
  rebaseOnDraft,
  sameMarks,
  syncedMarks,
  unsavedChanges,
  type SyncedMarks,
} from './documentDraft';
import { annotationFromWire, type Annotation, type AnnotationEdit, type FileAnchor } from './types';
import type { QuickLabel } from './quickLabels';
import type { MarkdownDocumentSource } from '../documentSource';
import type { ReviewChrome } from '../../ReviewReader/reviewChrome';
import { isDomInstance } from '../../../utils/realm';
import { useFrameScheduler, useTileWindow } from '../../../tileWindow';

const ANNOTATION_SAVE_DEBOUNCE_MS = 500;
const ANNOTATION_HYDRATE_RETRY_MS = 2000;
const ANNOTATION_SAVE_RETRY_MS = 5000;

const PENDING_PAINT_ID = 'md-pending-selection';
const FOCUS_PAINT_ID = 'md-focus-glow';
const FOCUS_GLOW_MS = 2000;

export type AnnotationOrphanReason = OrphanReason | 'non-paintable-block' | 'unpaintable';

export interface UseAnnotationsOptions {
  rootRef: RefObject<HTMLElement | null>;
  /** Raw markdown content — MUST be the same string the reader body renders. */
  content: string;
  source: MarkdownDocumentSource;
  enabled: boolean;
  transport?: MarkdownAnnotationsTransport | null;
  /** MUST be the chrome the reader body renders with. */
  reviewChrome?: ReviewChrome | null;
  /** The draft generation the daemon says changed outside this app, as when an
      agent answers a question. A higher one than this reader holds reads the draft again. */
  draftGeneration?: number;
}

export interface UseAnnotationsApi {
  annotations: Annotation[];
  orphans: Map<string, AnnotationOrphanReason>;
  selectedId: string | null;
  pending: PendingSelection | null;

  handleSelectionChange(selection: Selection | null): PendingSelection | null;
  beginBlockSelection(blockId: string): PendingSelection | null;
  clearPendingSelection(): void;

  addDeletion(): Annotation | null;
  submitComment(text: string): Annotation | null;
  applyQuickLabel(label: QuickLabel): Annotation | null;
  /** A note or question on lines of a review's code. */
  addFileMark(type: 'comment' | 'question', text: string, anchor: FileAnchor): Annotation | null;
  /** A note or question on a whole prose block. */
  addBlockMark(type: 'comment' | 'question', text: string, blockId: string): Annotation | null;
  /** An edit the user saved to lines of a review's file. */
  addEditMark(anchor: FileAnchor, edit: AnnotationEdit): Annotation | null;
  addGlobalComment(text: string): Annotation | null;
  deleteAnnotation(id: string): void;
  clearAll(): void;

  flushPendingSave(): Promise<void>;
  /** While false, saves are suppressed: a Send that does not gate on this
      delivers the daemon's stale draft. */
  isHydrated(): boolean;
  /** Tells every reader of the document what a send left in its draft. Returns how
      many marks stay in this reader. */
  adoptSentDraft(uri: string, draft: MarkdownAnnotationsDraft): number;

  selectAnnotation(id: string | null): void;
  focusAnnotation(id: string): void;

  justCreatedIdRef: RefObject<string | null>;
  /** Read for THIS tile's mouseup guard, not a document-wide popover query:
      two markdown tiles must not block each other's selection handling. */
  popoverOpenRef: RefObject<boolean>;
  lastMousePosRef: RefObject<{ x: number; y: number } | null>;
  painterMode: 'custom-highlight' | 'mark' | 'none';
}

function paintKindFor(annotation: Annotation): HighlightKind {
  return annotation.type === 'deletion' ? 'deletion' : 'comment';
}

export function useAnnotations({
  rootRef,
  content,
  source,
  enabled,
  transport,
  reviewChrome = null,
  draftGeneration,
}: UseAnnotationsOptions): UseAnnotationsApi {
  const [annotations, setAnnotations] = useState<Annotation[]>([]);
  const [orphans, setOrphans] = useState<Map<string, AnnotationOrphanReason>>(new Map());
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [pending, setPending] = useState<PendingSelection | null>(null);

  const annotationsRef = useRef<Annotation[]>(annotations);
  const pendingRef = useRef<PendingSelection | null>(null);
  const selectedIdRef = useRef<string | null>(null);
  const painterRef = useRef<HighlightPainter | null>(null);
  const blocksRef = useRef<BlockText[] | null>(null);
  const rangesRef = useRef<Map<string, Range>>(new Map());
  const contentRef = useRef(content);
  contentRef.current = content;
  const reviewChromeRef = useRef(reviewChrome);
  reviewChromeRef.current = reviewChrome;
  // Written by the hydrate effect (not render) so its CLEANUP still sees the
  // previous document and can flush that document's pending save.
  const sourceRef = useRef(source);
  const sourceUri = source.uri;
  const sourceKind = source.kind;
  const sourcePath = source.kind === 'file' ? source.path : '';
  const sourceHost = source.kind === 'file' ? source.host : undefined;
  const sourceSeedId = source.kind === 'seed' ? source.seedId : '';
  const generationRef = useRef(0);
  const syncedRef = useRef<SyncedMarks>(new Map());
  const hasHydratedRef = useRef(false);
  const hydrateTokenRef = useRef(0);
  const hydrateRetryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mountedRef = useRef(true);
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const rafRef = useRef<number | null>(null);
  const pendingClearRafRef = useRef<number | null>(null);
  const focusTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const justCreatedIdRef = useRef<string | null>(null);
  const lastMousePosRef = useRef<{ x: number; y: number } | null>(null);
  const popoverOpenRef = useRef(false);
  const orphansRef = useRef(orphans);
  const tileWindow = useTileWindow();
  const frames = useFrameScheduler();

  const transportRef = useRef<MarkdownAnnotationsTransport | null | undefined>(transport);
  transportRef.current = transport;
  const getTransport = useCallback((): MarkdownAnnotationsTransport | null => {
    return transportRef.current !== undefined
      ? transportRef.current
      : getMarkdownAnnotationsTransport();
  }, []);

  const ensurePainter = useCallback((): HighlightPainter | null => {
    const root = rootRef.current;
    if (!root) {
      return null;
    }
    return (painterRef.current ??= createHighlightPainter(root));
  }, [rootRef]);


  const persistNowRef = useRef<(() => void) | null>(null);
  /** flushPendingSave awaits this so a Send after the debounce fired — but
      before its request settled — cannot deliver a draft missing the edit. */
  const inFlightPersistRef = useRef<Promise<void> | null>(null);

  const schedulePersistRetry = useCallback((saveUri: string, op: string, err: unknown) => {
    console.warn(`[md-annotations] ${op} failed for ${saveUri}; retrying`, err);
    if (!mountedRef.current || sourceRef.current.uri !== saveUri || saveTimerRef.current !== null) {
      return;
    }
    saveTimerRef.current = setTimeout(() => {
      saveTimerRef.current = null;
      persistNowRef.current?.();
    }, ANNOTATION_SAVE_RETRY_MS);
  }, []);

  const persistNow = useCallback((): Promise<void> => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
    }
    if (!hasHydratedRef.current) {
      return Promise.resolve(); // never save over a draft we have not loaded yet
    }
    const t = getTransport();
    if (!t) {
      return Promise.resolve();
    }
    const { upserts, removedIds } = unsavedChanges(annotationsRef.current, syncedRef.current);
    if (upserts.length === 0 && removedIds.length === 0) {
      return inFlightPersistRef.current ?? Promise.resolve();
    }
    const saveSource = sourceRef.current;
    const saveUri = saveSource.uri;
    const request = t.saveMarkdownAnnotations(saveSource, upserts, removedIds)
      .then((draft) => announceDocumentDraft(saveUri, draft))
      .catch((err: unknown) => schedulePersistRetry(saveUri, 'save', err));
    inFlightPersistRef.current = request;
    const settle = () => {
      if (inFlightPersistRef.current === request) {
        inFlightPersistRef.current = null;
      }
    };
    request.then(settle, settle);
    return request;
  }, [getTransport, schedulePersistRetry]);
  persistNowRef.current = persistNow;

  const scheduleSave = useCallback(() => {
    if (!hasHydratedRef.current) {
      return;
    }
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
    }
    saveTimerRef.current = setTimeout(() => {
      saveTimerRef.current = null;
      persistNow();
    }, ANNOTATION_SAVE_DEBOUNCE_MS);
  }, [persistNow]);

  const flushPendingSave = useCallback((): Promise<void> => {
    if (saveTimerRef.current === null) {
      // No armed debounce — but a save whose debounce already fired may still
      // be mid-round-trip; await it so callers observe a settled draft.
      return inFlightPersistRef.current ?? Promise.resolve();
    }
    clearTimeout(saveTimerRef.current);
    saveTimerRef.current = null;
    return persistNow();
  }, [persistNow]);


  const paintPending = useCallback(
    (next: PendingSelection): void => {
      const root = rootRef.current;
      const painter = ensurePainter();
      if (!root || !painter) {
        return;
      }
      const blockEl = root.querySelector(`[data-block-id="${next.anchor.blockId}"]`);
      const range = blockEl ? resolveDomRange(blockEl, next.anchor.start, next.anchor.end) : null;
      if (range) {
        painter.paint(PENDING_PAINT_ID, range, 'comment');
      }
    },
    [ensurePainter, rootRef],
  );

  const refreshAndPaint = useCallback(
    (nextContent: string, saveMovedAnchors = true) => {
      const root = rootRef.current;
      const painter = ensurePainter();
      if (!root || !painter) {
        return;
      }
      if (rafRef.current !== null) {
        frames.cancel(rafRef.current);
        rafRef.current = null;
      }
      if (pendingClearRafRef.current !== null) {
        frames.cancel(pendingClearRafRef.current);
        pendingClearRafRef.current = null;
      }
      // Stale Ranges reference detached nodes after the body remount: always
      // clear before repainting.
      painter.clearAll();
      rangesRef.current.clear();

      const blocks = extractBlockTexts(nextContent, reviewChromeRef.current);
      blocksRef.current = blocks;

      const next: Annotation[] = [];
      const nextOrphans = new Map<string, AnnotationOrphanReason>();
      let rebasedAny = false;

      const paintOne = (annotation: Annotation): boolean => {
        const anchor = annotation.anchor!;
        const blockEl = root.querySelector(`[data-block-id="${anchor.blockId}"]`);
        const range = blockEl ? resolveDomRange(blockEl, anchor.start, anchor.end) : null;
        if (!range) {
          return false;
        }
        painter.paint(annotation.id, range, paintKindFor(annotation));
        rangesRef.current.set(annotation.id, range);
        return true;
      };

      for (const annotation of annotationsRef.current) {
        if (!annotation.anchor) {
          next.push(annotation);
          continue;
        }
        const result = resolveOrRebase(nextContent, annotation.anchor, blocks);
        if (result.state === 'orphan') {
          nextOrphans.set(annotation.id, result.reason);
          next.push(annotation);
          continue;
        }
        let updated = annotation;
        if (result.state === 'rebased') {
          updated = { ...annotation, anchor: result.anchor };
          rebasedAny = true;
        }
        if (blocks.find((b) => b.blockId === result.blockId)?.nonPaintable) {
          // Valid in text space but the DOM renders an svg (mermaid): keep
          // the record, skip the paint.
          nextOrphans.set(updated.id, 'non-paintable-block');
          next.push(updated);
          continue;
        }
        next.push(updated);
        if (!paintOne(updated)) {
          nextOrphans.set(updated.id, 'unpaintable');
        }
      }

      annotationsRef.current = next;
      orphansRef.current = nextOrphans;
      setAnnotations(next);
      setOrphans(nextOrphans);
      if (rebasedAny && saveMovedAnchors) {
        scheduleSave();
      }

      // Shiki seam: async highlighting swaps <pre> innards after commit,
      // detaching any Range painted inside. One deferred repaint pass.
      const inPreIds = next
        .filter((a) => {
          if (!a.anchor || nextOrphans.has(a.id)) {
            return false;
          }
          const el = root.querySelector(`[data-block-id="${a.anchor.blockId}"]`);
          return !!el && (el.tagName === 'PRE' || el.querySelector('pre') !== null);
        })
        .map((a) => a.id);
      if (inPreIds.length > 0) {
        rafRef.current = frames.request(() => {
          rafRef.current = null;
          // A late paint failure must publish a FRESH orphan map: mutating the
          // committed one bypasses React and hides the orphan badge.
          const failed: string[] = [];
          for (const id of inPreIds) {
            const annotation = annotationsRef.current.find((a) => a.id === id);
            if (annotation && !paintOne(annotation)) {
              painter.clear(id);
              rangesRef.current.delete(id);
              failed.push(id);
            }
          }
          if (failed.length > 0) {
            const republished = new Map(orphansRef.current);
            for (const id of failed) {
              republished.set(id, 'unpaintable');
            }
            orphansRef.current = republished;
            setOrphans(republished);
          }
        });
      }
    },
    [ensurePainter, frames, rootRef, scheduleSave],
  );

  useEffect(() => {
    if (!enabled) {
      return;
    }
    refreshAndPaint(content);
    return () => {
      if (rafRef.current !== null) {
        frames.cancel(rafRef.current);
        rafRef.current = null;
      }
      if (pendingClearRafRef.current !== null) {
        frames.cancel(pendingClearRafRef.current);
        pendingClearRafRef.current = null;
      }
      painterRef.current?.clearAll();
      rangesRef.current.clear();
    };
  }, [content, enabled, frames, refreshAndPaint, reviewChrome, rootRef, tileWindow]);


  const adoptDraft = useCallback(
    ({ annotations: wire, generation }: MarkdownAnnotationsDraft, saveMovedAnchors = false): number => {
      if (generation < generationRef.current) {
        return annotationsRef.current.length;
      }
      generationRef.current = generation;
      const draft = wire.map(annotationFromWire).filter((a): a is Annotation => a !== null);
      const rebased = rebaseOnDraft(annotationsRef.current, syncedRef.current, draft);
      syncedRef.current = syncedMarks(draft);
      const { upserts, removedIds } = unsavedChanges(rebased, syncedRef.current);
      if (!sameMarks(rebased, annotationsRef.current)) {
        annotationsRef.current = rebased;
        // Two readers on different versions of a document would otherwise answer
        // each other's moved anchors for as long as the versions differ.
        refreshAndPaint(contentRef.current, saveMovedAnchors);
        if (pendingRef.current) {
          paintPending(pendingRef.current);
        }
        if (selectedIdRef.current !== null && !rebased.some((a) => a.id === selectedIdRef.current)) {
          selectedIdRef.current = null;
          setSelectedId(null);
        }
      }
      if ((upserts.length > 0 || removedIds.length > 0) && saveTimerRef.current === null) {
        scheduleSave();
      }
      return rebased.length;
    },
    [paintPending, refreshAndPaint, scheduleSave],
  );

  const hydrate = useCallback(async () => {
    const token = ++hydrateTokenRef.current;
    if (hydrateRetryTimerRef.current) {
      clearTimeout(hydrateRetryTimerRef.current);
      hydrateRetryTimerRef.current = null;
    }
    hasHydratedRef.current = false;
    const t = getTransport();
    if (!t) {
      hasHydratedRef.current = true;
      return;
    }
    try {
      const draft = await t.getMarkdownAnnotations(sourceRef.current);
      if (hydrateTokenRef.current !== token) {
        return;
      }
      hasHydratedRef.current = true;
      adoptDraft(draft, true);
    } catch (err) {
      if (hydrateTokenRef.current !== token) {
        return;
      }
      // Keep saves SUPPRESSED (hasHydratedRef stays false) and retry: saving before
      // the stored marks are known would treat them as removed by nobody.
      console.warn(`[md-annotations] hydrate failed for ${sourceRef.current.uri}; retrying`, err);
      hydrateRetryTimerRef.current = setTimeout(() => {
        hydrateRetryTimerRef.current = null;
        if (hydrateTokenRef.current === token && mountedRef.current) {
          void hydrateRef.current?.();
        }
      }, ANNOTATION_HYDRATE_RETRY_MS);
    }
  }, [adoptDraft, getTransport]);
  const hydrateRef = useRef<typeof hydrate | null>(null);
  hydrateRef.current = hydrate;

  useEffect(() => {
    if (!enabled) {
      return;
    }
    sourceRef.current = sourceKind === 'file'
      ? {
        kind: 'file',
        uri: sourceUri,
        path: sourcePath,
        ...(sourceHost && { host: sourceHost }),
      }
      : {
        kind: 'seed',
        uri: sourceUri as `attn://seed/${string}`,
        seedId: sourceSeedId,
      };
    generationRef.current = 0;
    syncedRef.current = new Map();
    annotationsRef.current = [];
    orphansRef.current = new Map();
    setAnnotations([]);
    setOrphans(new Map());
    setSelectedId(null);
    selectedIdRef.current = null;
    pendingRef.current = null;
    setPending(null);
    void hydrate();
    const stopListening = listenToDocumentDraft(sourceUri, (draft) => {
      if (hasHydratedRef.current) {
        adoptDraft(draft);
      }
    });
    return () => {
      stopListening();
      flushPendingSave();
      hydrateTokenRef.current += 1;
      if (hydrateRetryTimerRef.current) {
        clearTimeout(hydrateRetryTimerRef.current);
        hydrateRetryTimerRef.current = null;
      }
      hasHydratedRef.current = false;
    };
  }, [
    sourceUri,
    sourceKind,
    sourcePath,
    sourceHost,
    sourceSeedId,
    enabled,
    hydrate,
    adoptDraft,
    flushPendingSave,
  ]);

  useEffect(() => {
    if (enabled && draftGeneration !== undefined && hasHydratedRef.current && draftGeneration > generationRef.current) {
      void hydrate();
    }
  }, [draftGeneration, enabled, hydrate]);

  useEffect(() => {
    if (!enabled) {
      return;
    }
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') {
        flushPendingSave();
      }
    };
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('pagehide', flushPendingSave);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      window.removeEventListener('pagehide', flushPendingSave);
    };
  }, [enabled, flushPendingSave]);


  const clearPendingSelection = useCallback(() => {
    const painter = painterRef.current;
    const clearNativeSelection = () => {
      try {
        tileWindow.getSelection()?.removeAllRanges();
      } catch {
      }
    };
    painter?.clear(PENDING_PAINT_ID);
    pendingRef.current = null;
    setPending(null);
    clearNativeSelection();
    if (pendingClearRafRef.current !== null) {
      frames.cancel(pendingClearRafRef.current);
    }
    // WKWebView can retain a same-frame selection paint until its next commit.
    pendingClearRafRef.current = frames.request(() => {
      pendingClearRafRef.current = null;
      painter?.clear(PENDING_PAINT_ID);
      clearNativeSelection();
    });
  }, [frames, tileWindow]);


  const handleSelectionChange = useCallback(
    (selection: Selection | null): PendingSelection | null => {
      const root = rootRef.current;
      if (!root) {
        return null;
      }
      const blocks = (blocksRef.current ??= extractBlockTexts(contentRef.current, reviewChromeRef.current));
      const next = evaluateSelection(root, selection, contentRef.current, blocks);
      if (!next) {
        clearPendingSelection();
        return null;
      }
      painterRef.current?.clear(PENDING_PAINT_ID);
      paintPending(next);
      pendingRef.current = next;
      setPending(next);
      return next;
    },
    [clearPendingSelection, paintPending, rootRef],
  );

  const beginBlockSelection = useCallback(
    (blockId: string): PendingSelection | null => {
      const root = rootRef.current;
      const blocks = (blocksRef.current ??= extractBlockTexts(contentRef.current, reviewChromeRef.current));
      const block = blocks.find((b) => b.blockId === blockId);
      if (!root || !block || block.nonPaintable || block.text.trim() === '') {
        return null;
      }
      const anchor = createAnchor(contentRef.current, blockId, 0, block.text.length, blocks);
      if (!anchor) {
        return null;
      }
      const blockEl = root.querySelector(`[data-block-id="${anchor.blockId}"]`);
      let rect: DOMRect | null = null;
      try {
        rect = blockEl?.getBoundingClientRect() ?? null;
      } catch {
        rect = null;
      }
      const next: PendingSelection = {
        anchor,
        selectionText: anchor.exact,
        clamped: false,
        blockId: anchor.blockId,
        isCodeBlock: true,
        rect,
      };
      painterRef.current?.clear(PENDING_PAINT_ID);
      paintPending(next);
      pendingRef.current = next;
      setPending(next);
      // Same focus claim as the mouseup path: macOS WebKit does not focus buttons on
      // click, so the gesture must pull keyboard focus in for type-to-comment.
      try {
        root.focus({ preventScroll: true });
      } catch {
      }
      return next;
    },
    [paintPending, rootRef],
  );


  useEffect(() => {
    if (!enabled) {
      return;
    }
    const root = rootRef.current;
    if (!root) {
      return;
    }

    const onDocMouseUp = (event: MouseEvent) => {
      lastMousePosRef.current = { x: event.clientX, y: event.clientY };
    };

    const onRootMouseUp = (event: MouseEvent) => {
      const target = event.target;
      if (isDomInstance(target, 'Element') && target.closest('.md-selection-toolbar, .md-annotation-popover, .md-quick-label-picker, .md-annotations-sidebar')) {
        return;
      }
      if (popoverOpenRef.current) {
        return;
      }
      const next = handleSelectionChange(tileWindow.getSelection());
      if (next) {
        // WebKit does not move focus on mousedown in non-focusable content; without
        // this the terminal's hidden input keeps focus and keys leak to the shell.
        try {
          root.focus({ preventScroll: true });
        } catch {
        }
      }
    };

    const onRootClick = (event: MouseEvent) => {
      const selection = tileWindow.getSelection?.();
      if (selection && !selection.isCollapsed) {
        return;
      }
      // Mark-fallback mode: the paint split the text nodes and collapsed the
      // Ranges cached below, but the wrapper spans carry the annotation id.
      const markEl =
        isDomInstance(event.target, 'Element') ? event.target.closest('[data-md-mark]') : null;
      const markId = markEl?.getAttribute('data-md-mark');
      if (markId && annotationsRef.current.some((a) => a.id === markId)) {
        selectedIdRef.current = markId;
        setSelectedId(markId);
        return;
      }
      for (const [id, range] of rangesRef.current) {
        for (const rect of range.getClientRects()) {
          if (
            event.clientX >= rect.left &&
            event.clientX <= rect.right &&
            event.clientY >= rect.top &&
            event.clientY <= rect.bottom
          ) {
            selectedIdRef.current = id;
            setSelectedId(id);
            return;
          }
        }
      }
    };

    const tileDocument = tileWindow.document;
    tileDocument.addEventListener('mouseup', onDocMouseUp, true);
    root.addEventListener('mouseup', onRootMouseUp);
    root.addEventListener('click', onRootClick);
    return () => {
      tileDocument.removeEventListener('mouseup', onDocMouseUp, true);
      root.removeEventListener('mouseup', onRootMouseUp);
      root.removeEventListener('click', onRootClick);
    };
  }, [enabled, handleSelectionChange, rootRef, tileWindow]);


  const addAnnotation = useCallback(
    (annotation: Annotation): Annotation => {
      const next = [...annotationsRef.current, annotation];
      annotationsRef.current = next;
      setAnnotations(next);
      justCreatedIdRef.current = annotation.id;
      if (annotation.anchor) {
        const root = rootRef.current;
        const painter = ensurePainter();
        const blockEl = root?.querySelector(`[data-block-id="${annotation.anchor.blockId}"]`);
        const range = blockEl
          ? resolveDomRange(blockEl, annotation.anchor.start, annotation.anchor.end)
          : null;
        if (painter && range) {
          painter.paint(annotation.id, range, paintKindFor(annotation));
          rangesRef.current.set(annotation.id, range);
        }
      }
      scheduleSave();
      return annotation;
    },
    [ensurePainter, rootRef, scheduleSave],
  );

  const createFromPending = useCallback(
    (build: (pendingSelection: PendingSelection) => Omit<Annotation, 'id' | 'createdAt'>): Annotation | null => {
      const pendingSelection = pendingRef.current;
      if (!pendingSelection) {
        return null;
      }
      const annotation: Annotation = {
        id: crypto.randomUUID(),
        createdAt: Date.now(),
        ...build(pendingSelection),
      };
      clearPendingSelection();
      return addAnnotation(annotation);
    },
    [addAnnotation, clearPendingSelection],
  );

  const addDeletion = useCallback(
    () => createFromPending((p) => ({ type: 'deletion', anchor: p.anchor })),
    [createFromPending],
  );

  const submitComment = useCallback(
    (text: string) =>
      createFromPending((p) => ({ type: 'comment', text, anchor: p.anchor })),
    [createFromPending],
  );

  const applyQuickLabel = useCallback(
    (label: QuickLabel) =>
      createFromPending((p) => ({
        type: 'comment',
        anchor: p.anchor,
        quickLabelId: label.id,
        ...(label.tip !== undefined ? { quickLabelTip: label.tip } : {}),
        // Display text is snapshotted at creation: the daemon-side send-payload
        // formatter has no copy of the label set.
        quickLabelText: `${label.emoji} ${label.text}`,
      })),
    [createFromPending],
  );

  const addGlobalComment = useCallback(
    (text: string): Annotation | null => {
      if (text.trim() === '') {
        return null;
      }
      return addAnnotation({
        id: crypto.randomUUID(),
        type: 'global',
        text,
        createdAt: Date.now(),
      });
    },
    [addAnnotation],
  );

  const addFileMark = useCallback(
    (type: 'comment' | 'question', text: string, anchor: FileAnchor): Annotation | null => {
      if (text.trim() === '') {
        return null;
      }
      return addAnnotation({ id: crypto.randomUUID(), type, text, fileAnchor: anchor, createdAt: Date.now() });
    },
    [addAnnotation],
  );

  const addEditMark = useCallback(
    (anchor: FileAnchor, edit: AnnotationEdit): Annotation | null =>
      addAnnotation({ id: crypto.randomUUID(), type: 'edit', fileAnchor: anchor, edit, createdAt: Date.now() }),
    [addAnnotation],
  );

  const addBlockMark = useCallback(
    (type: 'comment' | 'question', text: string, blockId: string): Annotation | null => {
      if (text.trim() === '' || !beginBlockSelection(blockId)) {
        return null;
      }
      return createFromPending((p) => ({ type, text, anchor: p.anchor }));
    },
    [beginBlockSelection, createFromPending],
  );

  const deleteAnnotation = useCallback(
    (id: string) => {
      const next = annotationsRef.current.filter((a) => a.id !== id);
      if (next.length === annotationsRef.current.length) {
        return;
      }
      annotationsRef.current = next;
      setAnnotations(next);
      painterRef.current?.clear(id);
      rangesRef.current.delete(id);
      if (orphansRef.current.has(id)) {
        const nextOrphans = new Map(orphansRef.current);
        nextOrphans.delete(id);
        orphansRef.current = nextOrphans;
        setOrphans(nextOrphans);
      }
      if (selectedIdRef.current === id) {
        selectedIdRef.current = null;
        setSelectedId(null);
      }
      scheduleSave();
    },
    [scheduleSave],
  );

  const clearAll = useCallback(() => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
    }
    annotationsRef.current = [];
    orphansRef.current = new Map();
    setAnnotations([]);
    setOrphans(new Map());
    selectedIdRef.current = null;
    setSelectedId(null);
    painterRef.current?.clearAll();
    rangesRef.current.clear();
    pendingRef.current = null;
    setPending(null);
    void persistNow();
  }, [persistNow]);

  const adoptSentDraft = useCallback((uri: string, draft: MarkdownAnnotationsDraft): number => {
    announceDocumentDraft(uri, draft);
    return annotationsRef.current.length;
  }, []);

  const isHydrated = useCallback(() => hasHydratedRef.current, []);


  const selectAnnotation = useCallback((id: string | null) => {
    selectedIdRef.current = id;
    setSelectedId(id);
  }, []);

  const focusAnnotation = useCallback(
    (id: string) => {
      selectAnnotation(id);
      const painter = painterRef.current;
      // Re-resolve from the anchor rather than the cached Range: in mark-fallback mode
      // the annotation's own paint split the text nodes and collapsed it.
      const annotation = annotationsRef.current.find((a) => a.id === id);
      let range: Range | null = null;
      if (annotation?.anchor) {
        const blockEl = rootRef.current?.querySelector(`[data-block-id="${annotation.anchor.blockId}"]`);
        range = blockEl ? resolveDomRange(blockEl, annotation.anchor.start, annotation.anchor.end) : null;
      }
      if (!range) {
        range = rangesRef.current.get(id) ?? null;
      }
      if (!painter || !range) {
        return;
      }
      rangesRef.current.set(id, range);
      if (focusTimerRef.current) {
        clearTimeout(focusTimerRef.current);
      }
      painter.paint(FOCUS_PAINT_ID, range, 'focus');
      focusTimerRef.current = setTimeout(() => {
        focusTimerRef.current = null;
        painterRef.current?.clear(FOCUS_PAINT_ID);
      }, FOCUS_GLOW_MS);
      if (justCreatedIdRef.current === id) {
        justCreatedIdRef.current = null;
        return;
      }
      const container = range.startContainer;
      const el = isDomInstance(container, 'Element') ? container : container.parentElement;
      el?.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
    },
    [selectAnnotation],
  );


  useEffect(() => {
    if (!enabled) {
      return;
    }
    const handle = {
      getState(): MarkdownAnnotationsAutomationState {
        return {
          available: true,
          mode: painterRef.current?.mode ?? 'none',
          uri: sourceRef.current.uri,
          path: sourceRef.current.kind === 'file' ? sourceRef.current.path : '',
          generation: generationRef.current,
          hydrated: hasHydratedRef.current,
          pendingSelection: pendingRef.current !== null,
          selectedId: selectedIdRef.current,
          annotations: annotationsRef.current.map((a) => ({
            id: a.id,
            type: a.type,
            text: a.text ?? null,
            quickLabelId: a.quickLabelId ?? null,
            orphaned: orphansRef.current.has(a.id),
            orphanReason: orphansRef.current.get(a.id) ?? null,
            exact: a.anchor?.exact ?? null,
            blockId: a.anchor?.blockId ?? null,
            startLine: a.anchor?.startLine ?? null,
            endLine: a.anchor?.endLine ?? null,
            start: a.anchor?.start ?? null,
            end: a.anchor?.end ?? null,
          })),
        };
      },
    };
    // Registry (not a single slot): unregistering THIS handle on unmount must
    // never blind the bridge to another still-open annotating tile.
    return registerMarkdownAnnotationsAutomationHandle(handle);
  }, [enabled]);

  // Unmount marker: save/hydrate timers must not re-arm after the hook is gone.
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    return () => {
      if (focusTimerRef.current) {
        clearTimeout(focusTimerRef.current);
      }
    };
  }, []);

  return useMemo(
    () => ({
      annotations,
      orphans,
      selectedId,
      pending,
      handleSelectionChange,
      beginBlockSelection,
      clearPendingSelection,
      addDeletion,
      submitComment,
      applyQuickLabel,
      addGlobalComment,
      addFileMark,
      addBlockMark,
      addEditMark,
      deleteAnnotation,
      clearAll,
      flushPendingSave,
      isHydrated,
      adoptSentDraft,
      selectAnnotation,
      focusAnnotation,
      justCreatedIdRef,
      lastMousePosRef,
      popoverOpenRef,
      painterMode: painterRef.current?.mode ?? 'none',
    }),
    [
      annotations,
      orphans,
      selectedId,
      pending,
      handleSelectionChange,
      beginBlockSelection,
      clearPendingSelection,
      addDeletion,
      submitComment,
      applyQuickLabel,
      addGlobalComment,
      addFileMark,
      addBlockMark,
      addEditMark,
      deleteAnnotation,
      clearAll,
      flushPendingSave,
      isHydrated,
      adoptSentDraft,
      selectAnnotation,
      focusAnnotation,
    ],
  );
}
