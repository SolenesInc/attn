
import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../../../hooks/useEscapeStack';
import {
  clearAnnotationDraft,
  readAnnotationDraft,
  writeAnnotationDraft,
} from './annotationDrafts';
import { formatShortcut } from '../../../shortcuts/formatShortcut';

export interface AnnotationPopoverProps {
  getAnchorRect: () => DOMRect | null;
  quote: string;
  isGlobal: boolean;
  initialText?: string;
  draftKey: string;
  onSubmit: (text: string) => void;
  onClose: () => void;
}

const MAX_POPOVER_WIDTH = 384;
const GAP = 8;
const FLIP_SPACE = 280;

function computePosition(anchorRect: DOMRect): {
  top: number;
  left: number;
  flipAbove: boolean;
  width: number;
} {
  const spaceBelow = window.innerHeight - anchorRect.bottom;
  const flipAbove = spaceBelow < FLIP_SPACE;
  const width = Math.min(MAX_POPOVER_WIDTH, window.innerWidth - 32);
  const top = flipAbove ? anchorRect.top - GAP : anchorRect.bottom + GAP;
  let left = anchorRect.left + anchorRect.width / 2 - width / 2;
  left = Math.max(16, Math.min(left, window.innerWidth - width - 16));
  return { top, left, flipAbove, width };
}

export function AnnotationPopover({
  getAnchorRect,
  quote,
  isGlobal,
  initialText = '',
  draftKey,
  onSubmit,
  onClose,
}: AnnotationPopoverProps) {
  const [text, setText] = useState(() => readAnnotationDraft(draftKey) ?? initialText);
  const [position, setPosition] = useState<ReturnType<typeof computePosition> | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const hasUnsavedContent = text.trim().length > 0;
  const hasUnsavedContentRef = useRef(hasUnsavedContent);
  hasUnsavedContentRef.current = hasUnsavedContent;

  useEffect(() => {
    if (text.trim().length > 0) {
      writeAnnotationDraft(draftKey, text);
    } else {
      clearAnnotationDraft(draftKey);
    }
  }, [draftKey, text]);

  useEffect(() => {
    const update = () => {
      const rect = getAnchorRect();
      if (rect) {
        setPosition(computePosition(rect));
      }
    };
    update();
    window.addEventListener('scroll', update, true);
    window.addEventListener('resize', update);
    return () => {
      window.removeEventListener('scroll', update, true);
      window.removeEventListener('resize', update);
    };
  }, [getAnchorRect]);

  // Autofocus via ref-callback + setTimeout(0) (WKWebView commit-order trap).
  const focusOnMountRef = useCallback((el: HTMLTextAreaElement | null) => {
    textareaRef.current = el;
    if (!el) {
      return;
    }
    setTimeout(() => {
      if (!el.isConnected) {
        return;
      }
      el.focus();
      el.selectionStart = el.selectionEnd = el.value.length;
    }, 0);
  }, []);

  useEffect(() => {
    const handlePointerDown = (e: PointerEvent) => {
      const target = e.target as Node | null;
      if (!target || popoverRef.current?.contains(target)) {
        return;
      }
      if (hasUnsavedContentRef.current) {
        return;
      }
      onClose();
    };
    document.addEventListener('pointerdown', handlePointerDown, true);
    return () => document.removeEventListener('pointerdown', handlePointerDown, true);
  }, [onClose]);

  const handleSubmit = useCallback(() => {
    if (!hasUnsavedContentRef.current) {
      return;
    }
    clearAnnotationDraft(draftKey);
    onSubmit(text);
  }, [draftKey, onSubmit, text]);

  useEscapeStack(onClose, true);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.nativeEvent.isComposing) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const headerLabel = isGlobal
    ? 'Overall Note'
    : quote
      ? `"${quote.length > 50 ? `${quote.slice(0, 50)}...` : quote}"`
      : 'Comment';

  if (!position) {
    return null;
  }

  return createPortal(
    <div
      ref={popoverRef}
      className={`md-annotation-popover ${position.flipAbove ? 'md-annotation-popover--above' : ''}`.trim()}
      style={{ top: position.top, left: position.left, width: position.width }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <div className="md-popover-header">
        <span className="md-popover-quote">{headerLabel}</span>
        <button type="button" className="md-popover-close" title="Close" onClick={onClose}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </div>
      <textarea
        ref={focusOnMountRef}
        className="md-popover-textarea"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={isGlobal ? 'Add an overall note...' : 'Add a comment...'}
        rows={3}
      />
      <div className="md-popover-footer">
        <span className="md-popover-hint">{formatShortcut('markdown.sendAnnotations')} to save</span>
        <button
          type="button"
          className="md-popover-submit"
          disabled={!hasUnsavedContent}
          onClick={handleSubmit}
        >
          {isGlobal ? 'Add' : 'Save'}
        </button>
      </div>
    </div>,
    document.body,
  );
}
