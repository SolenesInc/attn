import type {
  MarkdownAnnotation as WireAnnotation,
  MarkdownAnnotationAnchor as WireAnchor,
} from '../../../types/generated';
import type { AnchorRecord } from '../anchoring';

export type { WireAnnotation, WireAnchor };

export type AnnotationType = 'comment' | 'deletion' | 'global' | 'question' | 'edit';

/** A mark on a review's code: lines of a file now, quoted as the reader saw them. */
export interface FileAnchor {
  /** Relative to the review's repository. */
  path: string;
  from: number;
  to: number;
  /** The document line the mark reads at, for reading order. */
  docLine: number;
  quote: string;
}

/** An edit saved to a review's file: its lines before and after, and where it
 * stands: between the nearest lines at the review's head above and below it
 * when it was saved, each 0 when there was none. */
export interface AnnotationEdit {
  was: string;
  now: string;
  afterHead: number;
  beforeHead: number;
}

export interface Annotation {
  id: string; // crypto.randomUUID()
  type: AnnotationType;
  /** Comment body (comment/global). Absent for deletion and pure quick-labels. */
  text?: string;
  /** Anchor into the document. A mark has this, a fileAnchor, or neither for type 'global'. */
  anchor?: AnchorRecord;
  fileAnchor?: FileAnchor;
  /** The agent's answer to a question. */
  answer?: string;
  /** An edit saved to a review's file. */
  edit?: AnnotationEdit;
  /** Structured quick-label reference (NOT baked into text). */
  quickLabelId?: string;
  /** Tip text snapshotted at creation so the payload survives label-set edits. */
  quickLabelTip?: string;
  /** Display text ("👍 Looks good") snapshotted at creation; the daemon-side
   * formatter falls back to the raw quickLabelId for drafts that lack it. */
  quickLabelText?: string;
  createdAt: number; // epoch ms
}

export function anchorToWire(anchor: AnchorRecord): WireAnchor {
  return {
    block_id: anchor.blockId,
    start_line: anchor.startLine,
    end_line: anchor.endLine,
    exact: anchor.exact,
    prefix: anchor.prefix,
    suffix: anchor.suffix,
    start: anchor.start,
    end: anchor.end,
    content_hash: anchor.contentHash,
  };
}

export function anchorFromWire(wire: WireAnchor): AnchorRecord {
  return {
    blockId: wire.block_id,
    startLine: wire.start_line,
    endLine: wire.end_line,
    exact: wire.exact,
    prefix: wire.prefix,
    suffix: wire.suffix,
    start: wire.start,
    end: wire.end,
    contentHash: wire.content_hash,
  };
}

export function fileAnchorPlace(anchor: FileAnchor): string {
  return anchor.to > anchor.from ? `${anchor.path}:${anchor.from}–${anchor.to}` : `${anchor.path}:${anchor.from}`;
}

function fileAnchorToWire(anchor: FileAnchor): WireAnchor {
  return {
    block_id: '',
    start_line: anchor.docLine,
    end_line: anchor.docLine,
    exact: anchor.quote,
    prefix: '',
    suffix: '',
    start: 0,
    end: 0,
    content_hash: '',
    file: anchor.path,
    file_start_line: anchor.from,
    file_end_line: anchor.to,
  };
}

function fileAnchorFromWire(wire: WireAnchor): FileAnchor | null {
  if (!wire.file || wire.file_start_line === undefined) return null;
  return {
    path: wire.file,
    from: wire.file_start_line,
    to: wire.file_end_line ?? wire.file_start_line,
    docLine: wire.start_line,
    quote: wire.exact,
  };
}

export function annotationToWire(annotation: Annotation): WireAnnotation {
  const anchor = annotation.anchor ? anchorToWire(annotation.anchor) : annotation.fileAnchor ? fileAnchorToWire(annotation.fileAnchor) : null;
  return {
    id: annotation.id,
    type: annotation.type,
    ...(annotation.text !== undefined ? { text: annotation.text } : {}),
    ...(anchor ? { anchor } : {}),
    ...(annotation.quickLabelId !== undefined ? { quick_label_id: annotation.quickLabelId } : {}),
    ...(annotation.quickLabelTip !== undefined ? { quick_label_tip: annotation.quickLabelTip } : {}),
    ...(annotation.quickLabelText !== undefined ? { quick_label_text: annotation.quickLabelText } : {}),
    ...(annotation.answer !== undefined ? { answer: annotation.answer } : {}),
    ...(annotation.edit ? { edit_was: annotation.edit.was, edit_now: annotation.edit.now, edit_after_head: annotation.edit.afterHead, edit_before_head: annotation.edit.beforeHead } : {}),
    created_at: annotation.createdAt,
  };
}

function isAnnotationType(value: string): value is AnnotationType {
  return value === 'comment' || value === 'deletion' || value === 'global' || value === 'question' || value === 'edit';
}

/** Wire → frontend. Returns null for records this build cannot represent, so
 * hydration drops forward-version drafts instead of crashing. */
export function annotationFromWire(wire: WireAnnotation): Annotation | null {
  if (!isAnnotationType(wire.type)) {
    return null;
  }
  if (wire.type !== 'global' && !wire.anchor) {
    return null;
  }
  const fileAnchor = wire.anchor?.file ? fileAnchorFromWire(wire.anchor) : null;
  if (wire.anchor?.file && !fileAnchor) {
    return null;
  }
  return {
    id: wire.id,
    type: wire.type,
    ...(typeof wire.text === 'string' ? { text: wire.text } : {}),
    ...(fileAnchor ? { fileAnchor } : wire.anchor ? { anchor: anchorFromWire(wire.anchor) } : {}),
    ...(typeof wire.quick_label_id === 'string' ? { quickLabelId: wire.quick_label_id } : {}),
    ...(typeof wire.quick_label_tip === 'string' ? { quickLabelTip: wire.quick_label_tip } : {}),
    ...(typeof wire.quick_label_text === 'string' ? { quickLabelText: wire.quick_label_text } : {}),
    ...(typeof wire.answer === 'string' ? { answer: wire.answer } : {}),
    ...(typeof wire.edit_was === 'string' && typeof wire.edit_now === 'string'
      ? { edit: { was: wire.edit_was, now: wire.edit_now, afterHead: wire.edit_after_head ?? 0, beforeHead: wire.edit_before_head ?? 0 } }
      : {}),
    createdAt: wire.created_at,
  };
}
