import { isolateHistory } from '@codemirror/commands';
import { Annotation, Transaction } from '@codemirror/state';
import { EditorView, type ViewUpdate } from '@codemirror/view';
import type { Insertion } from './field';

const dictationEdit = Annotation.define<EditorSpan>();
const spans = new WeakMap<EditorView, Set<EditorSpan>>();

function follow(update: ViewUpdate) {
  const tracked = spans.get(update.view);
  if (!tracked) return;
  for (const transaction of update.transactions) {
    if (!transaction.docChanged) continue;
    const own = transaction.annotation(dictationEdit);
    for (const span of tracked) if (span !== own) span.map(transaction);
  }
}

const tracking = EditorView.updateListener.of(follow);

export function dictationTracking() {
  return tracking;
}

export class EditorSpan {
  private intact = true;
  private destroyed = false;

  private constructor(private readonly view: EditorView, private start: number, private end: number) {
    let tracked = spans.get(view);
    if (!tracked) spans.set(view, tracked = new Set());
    tracked.add(this);
  }

  static capture(element: Element | null): EditorSpan | null {
    const dom = element?.closest('.cm-editor');
    const view = dom instanceof HTMLElement ? EditorView.findFromDOM(dom) : null;
    if (!view || !view.state.facet(EditorView.updateListener).includes(follow)) return null;
    if (view.state.readOnly || !view.state.facet(EditorView.editable)) return null;
    const { from, to } = view.state.selection.main;
    return new EditorSpan(view, from, to);
  }

  map(transaction: Transaction) {
    if (!this.intact) return;
    const empty = this.start === this.end;
    transaction.changes.iterChangedRanges((from, to) => {
      if (empty ? from <= this.start && to > this.start : from < this.end && to > this.start) this.intact = false;
    });
    if (!this.intact) return;
    this.start = transaction.changes.mapPos(this.start, 1);
    this.end = empty ? this.start : transaction.changes.mapPos(this.end, -1);
  }

  release() {
    this.destroyed = true;
    spans.get(this.view)?.delete(this);
  }

  replace(text: string): Insertion {
    const { view } = this;
    if (this.destroyed || !view.dom.isConnected || EditorView.findFromDOM(view.dom) !== view) return 'targetGone';
    if (view.state.readOnly || !view.state.facet(EditorView.editable)) return 'targetGone';
    if (!this.intact || this.end > view.state.doc.length) return 'rangeEdited';
    const { start, end } = this;
    const selection = view.state.selection.main;
    const caretAtTarget = selection.from === start && selection.to === end;
    view.dispatch({
      changes: { from: start, to: end, insert: text },
      selection: caretAtTarget ? { anchor: start + text.length } : undefined,
      annotations: [dictationEdit.of(this), Transaction.userEvent.of('input.dictation'), isolateHistory.of('full')],
    });
    if (view.state.sliceDoc(start, start + text.length) !== text) { this.intact = false; return 'refused'; }
    this.end = start + text.length;
    return 'inserted';
  }
}

