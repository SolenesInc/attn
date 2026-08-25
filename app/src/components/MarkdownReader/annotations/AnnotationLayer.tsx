import { useCallback, useEffect, useRef, useState } from 'react';
import type { RefObject } from 'react';
import { resolveDomRange } from '../anchoring/domRange';
import { AnnotationPopover } from './AnnotationPopover';
import { AnnotationSidebar } from './AnnotationSidebar';
import { SelectionToolbar } from './SelectionToolbar';
import type { PendingSelection } from './selection';
import type { QuickLabel } from './quickLabels';
import type { UseAnnotationsApi } from './useAnnotations';
import type { MarkdownDocumentSource } from '../documentSource';

const HOVER_HIDE_GRACE_MS = 200;

type PopoverState =
  | {
      kind: 'selection';
      /** Snapshot at open time — the quote and draft key must not shift. */
      pending: PendingSelection;
      initialText?: string;
    }
  | { kind: 'global'; anchorEl: HTMLElement };

interface HoverBlock {
  blockId: string;
  /** The .md-codeblock wrapper used as the toolbar anchor. */
  element: HTMLElement;
}

export interface AnnotationLayerProps {
  api: UseAnnotationsApi;
  rootRef: RefObject<HTMLElement | null>;
  source: MarkdownDocumentSource;
}

function pendingDraftKey(documentUri: string, pending: PendingSelection): string {
  const { anchor } = pending;
  return `${documentUri}#${anchor.blockId}:${anchor.start}:${anchor.end}`;
}

export function AnnotationLayer({ api, rootRef, source }: AnnotationLayerProps) {
  const { pending, annotations, orphans, selectedId } = api;
  const [popover, setPopover] = useState<PopoverState | null>(null);
  const [hoverBlock, setHoverBlock] = useState<HoverBlock | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const autoOpenedRef = useRef(false);
  const hoverHideTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (annotations.length > 0 && !autoOpenedRef.current) {
      autoOpenedRef.current = true;
      setSidebarOpen(true);
    }
  }, [annotations.length]);

  useEffect(() => {
    if (selectedId !== null) {
      setSidebarOpen(true);
    }
  }, [selectedId]);

  // Scope the hook's mouseup guard to THIS tile, not a document-wide popover query.
  useEffect(() => {
    api.popoverOpenRef.current = popover !== null;
  }, [api.popoverOpenRef, popover]);

  // NOT inside CodeBlock.tsx: it lives behind the memo gate, where new props break the
  // gate contract.

  const cancelHoverHide = useCallback(() => {
    if (hoverHideTimerRef.current) {
      clearTimeout(hoverHideTimerRef.current);
      hoverHideTimerRef.current = null;
    }
  }, []);

  const scheduleHoverHide = useCallback(() => {
    cancelHoverHide();
    hoverHideTimerRef.current = setTimeout(() => {
      hoverHideTimerRef.current = null;
      setHoverBlock(null);
    }, HOVER_HIDE_GRACE_MS);
  }, [cancelHoverHide]);

  useEffect(() => {
    const root = rootRef.current;
    if (!root) {
      return;
    }
    const onPointerOver = (event: Event) => {
      const target = event.target;
      if (!(target instanceof Element)) {
        return;
      }
      const wrapper = target.closest('.md-codeblock');
      if (!wrapper || !root.contains(wrapper)) {
        return;
      }
      const blockEl = wrapper.querySelector('[data-block-id]') ?? wrapper.closest('[data-block-id]');
      const blockId = blockEl?.getAttribute('data-block-id');
      if (!blockId) {
        return;
      }
      cancelHoverHide();
      setHoverBlock((prev) =>
        prev?.blockId === blockId ? prev : { blockId, element: wrapper as HTMLElement },
      );
    };
    const onPointerOut = (event: Event) => {
      const target = event.target;
      const related = (event as PointerEvent).relatedTarget;
      if (!(target instanceof Element) || !target.closest('.md-codeblock')) {
        return;
      }
      if (related instanceof Element && related.closest('.md-codeblock')) {
        return;
      }
      scheduleHoverHide();
    };
    root.addEventListener('pointerover', onPointerOver);
    root.addEventListener('pointerout', onPointerOut);
    return () => {
      root.removeEventListener('pointerover', onPointerOver);
      root.removeEventListener('pointerout', onPointerOut);
      cancelHoverHide();
    };
  }, [rootRef, cancelHoverHide, scheduleHoverHide]);


  const pendingRef = useRef(pending);
  pendingRef.current = pending;
  /** Live rect for the pending selection: re-resolve the DOM range so the
      toolbar tracks scroll (the stored rect is a snapshot). */
  const getPendingRect = useCallback((): DOMRect | null => {
    const current = pendingRef.current;
    if (!current) {
      return null;
    }
    const root = rootRef.current;
    const blockEl = root?.querySelector(`[data-block-id="${current.anchor.blockId}"]`);
    if (blockEl) {
      try {
        const range = resolveDomRange(blockEl, current.anchor.start, current.anchor.end);
        const rect = range?.getBoundingClientRect();
        if (rect) {
          return rect;
        }
      } catch {

      }
    }
    return current.rect;
  }, [rootRef]);

  const hoverBlockRef = useRef(hoverBlock);
  hoverBlockRef.current = hoverBlock;
  const getHoverRect = useCallback((): DOMRect | null => {
    return hoverBlockRef.current?.element.getBoundingClientRect() ?? null;
  }, []);

  const getCursorHint = useCallback(
    () => api.lastMousePosRef.current,
    [api.lastMousePosRef],
  );


  /** Hover-toolbar actions first turn the hovered block into a whole-block
      pending selection; selection-toolbar actions use the existing pending. */
  const ensurePending = useCallback((): PendingSelection | null => {
    if (pendingRef.current) {
      return pendingRef.current;
    }
    const hover = hoverBlockRef.current;
    return hover ? api.beginBlockSelection(hover.blockId) : null;
  }, [api]);

  const closeToolbar = useCallback(() => {
    api.clearPendingSelection();
    setHoverBlock(null);
  }, [api]);

  const handleDelete = useCallback(() => {
    if (!ensurePending()) {
      return;
    }
    api.addDeletion();
    setHoverBlock(null);
  }, [api, ensurePending]);

  const handleQuickLabel = useCallback(
    (label: QuickLabel) => {
      if (!ensurePending()) {
        return;
      }
      api.applyQuickLabel(label);
      setHoverBlock(null);
    },
    [api, ensurePending],
  );

  const handleRequestComment = useCallback(
    (initialChar?: string) => {
      const p = ensurePending();
      if (!p) {
        return;
      }
      setPopover({ kind: 'selection', pending: p, initialText: initialChar });
      setHoverBlock(null);
    },
    [ensurePending],
  );

  const handleGlobalComment = useCallback((anchorEl: HTMLElement) => {
    setPopover({ kind: 'global', anchorEl });
  }, []);


  const popoverRef = useRef(popover);
  popoverRef.current = popover;
  const getPopoverRect = useCallback((): DOMRect | null => {
    const state = popoverRef.current;
    if (!state) {
      return null;
    }
    if (state.kind === 'global') {
      return state.anchorEl.isConnected ? state.anchorEl.getBoundingClientRect() : null;
    }
    return getPendingRect() ?? state.pending.rect;
  }, [getPendingRect]);

  const handlePopoverSubmit = useCallback(
    (text: string) => {
      const state = popoverRef.current;
      if (!state) {
        return;
      }
      if (state.kind === 'global') {
        api.addGlobalComment(text);
      } else {
        api.submitComment(text);
      }
      setPopover(null);
    },
    [api],
  );

  const handlePopoverClose = useCallback(() => {
    const state = popoverRef.current;
    setPopover(null);
    if (state?.kind === 'selection') {
      api.clearPendingSelection();
    }
  }, [api]);


  const showSelectionToolbar = pending !== null && popover === null;
  const showHoverToolbar = !showSelectionToolbar && popover === null && hoverBlock !== null;

  return (
    <>
      {showSelectionToolbar && pending && (
        <SelectionToolbar
          getAnchorRect={getPendingRect}
          positionMode={pending.isCodeBlock ? 'top-right' : 'center-above'}
          onDelete={handleDelete}
          onRequestComment={handleRequestComment}
          onQuickLabel={handleQuickLabel}
          onClose={closeToolbar}
          closeOnScrollOut
          getCursorHint={getCursorHint}
        />
      )}
      {showHoverToolbar && hoverBlock && (
        <SelectionToolbar
          getAnchorRect={getHoverRect}
          positionMode="top-right"
          onDelete={handleDelete}
          onRequestComment={handleRequestComment}
          onQuickLabel={handleQuickLabel}
          onClose={closeToolbar}
          closeOnScrollOut
          getCursorHint={getCursorHint}
          onMouseEnter={cancelHoverHide}
          onMouseLeave={scheduleHoverHide}
        />
      )}
      {popover && (
        <AnnotationPopover
          getAnchorRect={getPopoverRect}
          quote={popover.kind === 'selection' ? popover.pending.selectionText : ''}
          isGlobal={popover.kind === 'global'}
          initialText={popover.kind === 'selection' ? popover.initialText : undefined}
          draftKey={
            popover.kind === 'global'
              ? `${source.uri}#global`
              : pendingDraftKey(source.uri, popover.pending)
          }
          onSubmit={handlePopoverSubmit}
          onClose={handlePopoverClose}
        />
      )}
      {sidebarOpen ? (
        <AnnotationSidebar
          annotations={annotations}
          orphans={orphans}
          selectedId={selectedId}
          onCardClick={api.focusAnnotation}
          onDelete={api.deleteAnnotation}
          onClearAll={api.clearAll}
          onGlobalComment={handleGlobalComment}
          onToggle={() => setSidebarOpen(false)}
        />
      ) : (
        <button
          type="button"
          className="md-sidebar-rail"
          title="Show annotations"
          onClick={() => setSidebarOpen(true)}
        >
          <span className="md-sidebar-rail-count">{annotations.length}</span>
          <span className="md-sidebar-rail-label">Annotations</span>
        </button>
      )}
    </>
  );
}
