import {
  alignMessage,
  offsetsForSelection,
  quotesAnchor,
  rowsForOffsets,
  type MessageAlignment,
  type RowRange,
} from './terminalMessageAlign';

export interface MessageRowAccess {
  cols(): number;
  totalRows(): number;
  rowText(bufferRow: number): string;
  rowTextRange(bufferRow: number, startCol: number, endCol: number): string;
  hyperlinkUri?(bufferRow: number, col: number): string | null;
}

export interface AnnotatableMessage {
  key: string;
  markdown: string;
}

export interface TerminalAnnotation {
  id: string;
  messageKey: string;
  // Offsets into that message's markdown (UTF-16 code units).
  start: number;
  end: number;
  quote: string;
  quickLabelId: string;
  comment: string;
}

// A wash that passed the gate, in BUFFER rows — a viewport row shifts under
// scroll and must never be stored.
export interface AnnotationWash {
  annotationId: string;
  rows: RowRange[];
}

export interface MessageAnchor {
  messageKey: string;
  start: number;
  end: number;
  quote: string;
}

const ALIGN_WINDOW_ROWS = 2000;
const LOCAL_MARGIN_ROWS = 60;

interface AlignmentCache {
  alignment: MessageAlignment;
  writeGeneration: number;
  geometryGeneration: number;
  cols: number;
  totalRows: number;
  lastSpan: { firstRow: number; lastRow: number } | null;
}

function newAnnotationId(): string {
  const uuid = globalThis.crypto?.randomUUID?.();
  // A WebView without randomUUID must not hand every annotation the same id.
  return uuid ?? `anno-${Math.random().toString(36).slice(2)}-${Date.now().toString(36)}`;
}

export class TerminalAnnotationStore {
  private messages: AnnotatableMessage[] = [];
  private markdownByKey = new Map<string, string>();
  private annotations: TerminalAnnotation[] = [];
  private caches = new Map<string, AlignmentCache>();

  private writeGeneration = 0;
  private geometryGeneration = 0;

  setMessages(messages: readonly AnnotatableMessage[]): boolean {
    const same = messages.length === this.messages.length
      && messages.every((message, index) => message.key === this.messages[index].key
        && message.markdown === this.messages[index].markdown);
    if (same) return false;
    this.messages = messages.map((message) => ({ ...message }));
    this.markdownByKey = new Map(this.messages.map((message) => [message.key, message.markdown]));
    this.caches.clear();
    return true;
  }

  messageKeys(): string[] {
    return this.messages.map((message) => message.key);
  }

  markdownFor(key: string): string | null {
    return this.markdownByKey.get(key) ?? null;
  }

  hasMessages(): boolean {
    return this.messages.length > 0;
  }

  list(): readonly TerminalAnnotation[] {
    return this.annotations;
  }

  hydrate(annotations: readonly TerminalAnnotation[]): void {
    this.annotations = annotations.map((annotation) => ({ ...annotation }));
  }

  add(messageKey: string, start: number, end: number, quickLabelId = '', comment = ''): TerminalAnnotation | null {
    const markdown = this.markdownByKey.get(messageKey);
    if (markdown === undefined) return null;
    if (start < 0 || end > markdown.length || start >= end) return null;
    const annotation: TerminalAnnotation = {
      id: newAnnotationId(),
      messageKey,
      start,
      end,
      quote: markdown.slice(start, end),
      quickLabelId,
      comment,
    };
    this.annotations.push(annotation);
    return annotation;
  }

  update(id: string, patch: { quickLabelId?: string; comment?: string }): TerminalAnnotation | null {
    const annotation = this.annotations.find((entry) => entry.id === id);
    if (!annotation) return null;
    if (patch.quickLabelId !== undefined) annotation.quickLabelId = patch.quickLabelId;
    if (patch.comment !== undefined) annotation.comment = patch.comment;
    return annotation;
  }

  remove(id: string): boolean {
    const before = this.annotations.length;
    this.annotations = this.annotations.filter((entry) => entry.id !== id);
    return this.annotations.length !== before;
  }

  clear(): void {
    this.annotations = [];
  }

  reset(): void {
    this.caches.clear();
  }

  hasWork(): boolean {
    return this.annotations.length > 0 && this.messages.length > 0;
  }

  noteWrite(): void {
    this.writeGeneration += 1;
  }

  noteGeometryChange(): void {
    this.geometryGeneration += 1;
  }

  private searchWindow(
    lastSpan: { firstRow: number; lastRow: number } | null,
    totalRows: number,
  ): { base: number; end: number; local: boolean } {
    if (!lastSpan) {
      return { base: Math.max(0, totalRows - ALIGN_WINDOW_ROWS), end: totalRows, local: false };
    }
    const half = Math.max(LOCAL_MARGIN_ROWS, lastSpan.lastRow - lastSpan.firstRow + 1);
    return {
      base: Math.max(0, lastSpan.firstRow - half),
      end: Math.min(totalRows, lastSpan.lastRow + half + 1),
      local: true,
    };
  }

  private align(key: string, markdown: string, access: MessageRowAccess): AlignmentCache {
    const totalRows = access.totalRows();
    const cols = access.cols();

    // A span is only meaningful in the geometry it was measured in: seeding a bounded search
    // across a reflow resolves a window that clips the message.
    const previous = this.caches.get(key);
    let lastSpan = previous?.lastSpan ?? null;
    if (previous && (previous.cols !== cols || previous.geometryGeneration !== this.geometryGeneration)) {
      lastSpan = null;
    }

    const readRows = (from: number, to: number): string[] => {
      const out: string[] = [];
      for (let row = from; row < to; row += 1) out.push(access.rowText(row));
      return out;
    };

    let { base, end, local } = this.searchWindow(lastSpan, totalRows);
    let rows = readRows(base, end);
    let alignment = alignMessage(markdown, rows, base, access.hyperlinkUri);

    const missed = alignment.firstRow < 0;
    const atEdge = !missed
      && ((alignment.firstRow <= base + 1 && base > 0)
        || (alignment.lastRow >= end - 2 && end < totalRows));
    if (local && (missed || atEdge)) {
      base = Math.max(0, totalRows - ALIGN_WINDOW_ROWS);
      end = totalRows;
      local = false;
      rows = readRows(base, end);
      alignment = alignMessage(markdown, rows, base, access.hyperlinkUri);
    }

    const entry: AlignmentCache = {
      alignment,
      writeGeneration: this.writeGeneration,
      geometryGeneration: this.geometryGeneration,
      cols,
      totalRows,
      lastSpan: alignment.firstRow >= 0
        ? { firstRow: alignment.firstRow, lastRow: alignment.lastRow }
        : null,
    };
    this.caches.set(key, entry);
    return entry;
  }

  private currentAlignment(key: string, access: MessageRowAccess): MessageAlignment | null {
    const markdown = this.markdownByKey.get(key);
    if (markdown === undefined || markdown === '') return null;
    const cache = this.caches.get(key);
    if (
      cache
      && cache.writeGeneration === this.writeGeneration
      && cache.geometryGeneration === this.geometryGeneration
      && cache.cols === access.cols()
      && cache.totalRows === access.totalRows()
    ) {
      return cache.alignment;
    }
    return this.align(key, markdown, access).alignment;
  }

  project(access: MessageRowAccess): AnnotationWash[] {
    if (!this.hasWork()) return [];
    const washes: AnnotationWash[] = [];
    const alignments = new Map<string, MessageAlignment | null>();
    for (const annotation of this.annotations) {
      if (!alignments.has(annotation.messageKey)) {
        alignments.set(annotation.messageKey, this.currentAlignment(annotation.messageKey, access));
      }
      const alignment = alignments.get(annotation.messageKey);
      if (!alignment) continue;
      const rows = rowsForOffsets(alignment, annotation.start, annotation.end);
      if (rows.length === 0) continue;
      const painted = rows
        .map((range) => access.rowTextRange(range.row, range.startCol, range.endCol))
        .join('\n');
      if (!quotesAnchor(annotation.quote, painted)) continue;
      washes.push({ annotationId: annotation.id, rows });
    }
    return washes;
  }

  annotationAt(access: MessageRowAccess, bufferRow: number, col: number): string | null {
    let hit: string | null = null;
    for (const wash of this.project(access)) {
      for (const range of wash.rows) {
        if (range.row !== bufferRow) continue;
        if (col < range.startCol || col >= range.endCol) continue;
        hit = wash.annotationId;
      }
    }
    return hit;
  }

  anchorForSelection(
    access: MessageRowAccess,
    selection: { startRow: number; startCol: number; endRow: number; endCol: number },
  ): MessageAnchor | null {
    for (let index = this.messages.length - 1; index >= 0; index -= 1) {
      const { key, markdown } = this.messages[index];
      const alignment = this.currentAlignment(key, access);
      if (!alignment) continue;
      const span = offsetsForSelection(alignment, selection);
      if (!span) continue;
      const quote = markdown.slice(span.start, span.end);
      const rows = rowsForOffsets(alignment, span.start, span.end);
      const painted = rows
        .map((range) => access.rowTextRange(range.row, range.startCol, range.endCol))
        .join('\n');
      if (!quotesAnchor(quote, painted)) continue;
      return { messageKey: key, start: span.start, end: span.end, quote };
    }
    return null;
  }

  resolvedSpans(access: MessageRowAccess): Array<{ key: string; firstRow: number; lastRow: number }> {
    const spans: Array<{ key: string; firstRow: number; lastRow: number }> = [];
    for (let index = this.messages.length - 1; index >= 0; index -= 1) {
      const { key } = this.messages[index];
      const alignment = this.currentAlignment(key, access);
      if (!alignment || alignment.firstRow < 0) continue;
      spans.push({ key, firstRow: alignment.firstRow, lastRow: alignment.lastRow });
    }
    return spans;
  }
}
