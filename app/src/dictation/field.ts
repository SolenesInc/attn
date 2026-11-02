import { writableField, type TextField } from '../utils/textFields';

export function missingTargetReason(element: Element | null): string {
  if (!element || element === document.body) return 'Click into a text field first, then start dictation.';
  if (element.closest('.cm-editor')) return 'This editor is read-only right now.';
  if (element.closest('.ghostty-terminal')) return 'This terminal is not connected to a running session, so it cannot take dictation.';
  if (element instanceof HTMLInputElement && element.type === 'password') return 'Dictation does not type into password fields.';
  if ((element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) && (element.readOnly || element.disabled)) return 'This field is read-only right now.';
  return 'The focused element does not take text. Click into a text field first.';
}

export interface Range {
  start: number;
  end: number;
  intact: boolean;
}

interface Edit {
  from: number;
  to: number;
  inserted: number;
}

function diff(before: string, after: string, caret: number | null): Edit {
  const growth = after.length - before.length;
  let prefix = 0;
  const shortest = Math.min(before.length, after.length);
  while (prefix < shortest && before[prefix] === after[prefix]) prefix++;
  if (caret !== null) prefix = Math.min(prefix, Math.max(0, caret - Math.max(0, growth)));
  let suffix = 0;
  while (suffix < shortest - prefix && before[before.length - 1 - suffix] === after[after.length - 1 - suffix]) suffix++;
  return { from: prefix, to: before.length - suffix, inserted: after.length - suffix - prefix };
}

function mapRange(range: Range, edit: Edit) {
  if (!range.intact) return;
  const delta = edit.inserted - (edit.to - edit.from);
  const insertion = edit.from === edit.to;
  if (edit.to < range.start || (edit.to === range.start && (edit.from < range.start || insertion))) {
    range.start += delta;
    range.end += delta;
  } else if (edit.from > range.end || (edit.from === range.end && range.start !== range.end)) {
    return;
  } else if (insertion && edit.from === range.end && range.start === range.end) {
    range.start += delta;
    range.end += delta;
  } else {
    range.intact = false;
  }
}

function mapOffset(offset: number, edit: Edit) {
  if (offset <= edit.from) return offset;
  if (offset >= edit.to) return offset + edit.inserted - (edit.to - edit.from);
  return edit.from + edit.inserted;
}

let composing = 0;
document.addEventListener('compositionstart', () => { composing++; }, true);
document.addEventListener('compositionend', () => { composing = Math.max(0, composing - 1); }, true);

export function isComposing() {
  return composing > 0;
}

export function afterComposition(): Promise<void> {
  if (!isComposing()) return Promise.resolve();
  return new Promise(resolve => {
    const done = () => { if (isComposing()) return; document.removeEventListener('compositionend', later, true); resolve(); };
    const later = () => queueMicrotask(done);
    document.addEventListener('compositionend', later, true);
  });
}

export type Insertion = 'inserted' | 'targetGone' | 'rangeEdited' | 'refused';

export class TrackedField {
  private value: string;
  private ranges = new Set<Range>();
  private applying: { range: Range; text: string } | null = null;
  private readonly onInput = () => this.observe('edited');

  constructor(readonly element: TextField) {
    this.value = element.value;
    element.addEventListener('input', this.onInput, true);
  }

  track(start: number, end: number): Range {
    this.observe();
    const range = { start, end, intact: true };
    this.ranges.add(range);
    return range;
  }

  release(range: Range) {
    this.ranges.delete(range);
    if (this.ranges.size === 0) this.element.removeEventListener('input', this.onInput, true);
  }

  get connected() {
    return this.element.isConnected && writableField(this.element) === this.element;
  }

  text(range: Range) {
    this.observe();
    return range.intact ? this.element.value.slice(range.start, range.end) : null;
  }

  private observe(cause: 'edited' | 'replaced' = 'replaced') {
    const after = this.element.value;
    if (after === this.value) return;
    if (this.applying) {
      const { range, text } = this.applying;
      const edit = { from: range.start, to: range.end, inserted: text.length };
      for (const other of this.ranges) if (other !== range) mapRange(other, edit);
      range.end = range.start + text.length;
    } else if (cause === 'edited') {
      const caret = document.activeElement === this.element ? this.element.selectionEnd : null;
      const edit = diff(this.value, after, caret);
      for (const range of this.ranges) mapRange(range, edit);
    } else {
      for (const range of this.ranges) range.intact = false;
    }
    this.value = after;
  }

  replace(range: Range, text: string): Insertion {
    this.observe();
    if (!this.connected) return 'targetGone';
    if (!range.intact) return 'rangeEdited';
    if (range.end > this.value.length) return 'rangeEdited';
    const element = this.element;
    const previous = document.activeElement;
    const own = previous === element;
    const selection = own ? [element.selectionStart ?? 0, element.selectionEnd ?? 0, element.selectionDirection ?? 'none'] as const : null;
    const edit = { from: range.start, to: range.end, inserted: text.length };
    if (!own) element.focus({ preventScroll: true });
    element.setSelectionRange(range.start, range.end);
    this.applying = { range, text };
    let inserted = false;
    try {
      inserted = document.execCommand('insertText', false, text);
      this.observe();
    } finally {
      this.applying = null;
    }
    if (!inserted || element.value.slice(edit.from, edit.from + text.length) !== text) {
      range.intact = false;
      return 'refused';
    }
    const atTarget = selection && selection[0] === edit.from && selection[1] === edit.to;
    if (atTarget) element.setSelectionRange(edit.from + text.length, edit.from + text.length);
    else if (selection) element.setSelectionRange(mapOffset(selection[0], edit), mapOffset(selection[1], edit), selection[2]);
    else if (previous instanceof HTMLElement && previous !== document.body) previous.focus({ preventScroll: true });
    else element.blur();
    return 'inserted';
  }
}
