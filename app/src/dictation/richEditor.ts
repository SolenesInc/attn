import { writableField } from '../utils/textFields';
import type { Paste } from './target';

export function richEditorHost(element: Element | null): HTMLElement | null {
  return element instanceof HTMLElement && element.isContentEditable && !element.closest('.cm-editor') ? element : null;
}

function selectionIn(host: HTMLElement): Range | null {
  const selection = host.ownerDocument.getSelection();
  if (!selection?.rangeCount) return null;
  const range = selection.getRangeAt(0);
  return host.contains(range.startContainer) && host.contains(range.endContainer) ? range : null;
}

function select(doc: Document, range: Range) {
  const selection = doc.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function samePlace(a: Range, b: Range) {
  return a.compareBoundaryPoints(Range.START_TO_START, b) === 0 && a.compareBoundaryPoints(Range.END_TO_END, b) === 0;
}

export function holdFocus(doc: Document): () => void {
  const element = doc.hasFocus() ? doc.activeElement : null;
  const field = writableField(element);
  const start = field?.selectionStart ?? null;
  const end = field?.selectionEnd ?? null;
  const host = richEditorHost(element);
  const editorSelection = host ? selectionIn(host)?.cloneRange() ?? null : null;
  return () => {
    if (!(element instanceof HTMLElement) || !element.isConnected || doc.activeElement === element) return;
    element.focus({ preventScroll: true });
    if (field && start !== null && end !== null) field.setSelectionRange(start, end);
    if (editorSelection) select(doc, editorSelection);
  };
}

export class RichEditorSelection {
  private constructor(private host: HTMLElement, private range: Range) {}

  static capture(focused: Element | null): RichEditorSelection | null {
    const host = richEditorHost(focused);
    const range = host && selectionIn(host);
    return host && range ? new RichEditorSelection(host, range.cloneRange()) : null;
  }

  insert(text: string): Paste {
    const { host, range } = this;
    const doc = host.ownerDocument;
    if (!host.isConnected || !host.isContentEditable || !host.contains(range.startContainer) || !host.contains(range.endContainer)) return 'targetGone';
    const caret = doc.activeElement === host ? selectionIn(host)?.cloneRange() : null;
    const keepCaret = caret && !samePlace(caret, range) ? caret : null;
    const restoreFocus = holdFocus(doc);
    host.focus({ preventScroll: true });
    select(doc, range.cloneRange());
    const inserted = doc.execCommand('insertText', false, text);
    restoreFocus();
    if (keepCaret) select(doc, keepCaret);
    return inserted ? 'inserted' : 'targetGone';
  }
}
