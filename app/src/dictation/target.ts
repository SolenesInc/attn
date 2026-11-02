import { DictationTextFormat } from '../types/generated';
import { writableField } from '../utils/textFields';
import { EditorSpan } from './codemirror';
import { TrackedField, type Insertion } from './field';
import { terminalTarget } from './terminal';

export interface EditTarget {
  kind: 'edit';
  format: DictationTextFormat;
  replace(text: string): Insertion | Promise<Insertion>;
  release(): void;
}

export type Paste = 'inserted' | 'targetGone';

export interface PasteTarget {
  kind: 'paste';
  format: DictationTextFormat;
  paste(text: string): Paste;
  claimEnter(onEnter: () => void): void;
  release(): void;
}

export type DictationTarget = EditTarget | PasteTarget;

function fieldTarget(element: NonNullable<ReturnType<typeof writableField>>): EditTarget {
  const field = new TrackedField(element);
  const range = field.track(element.selectionStart ?? element.value.length, element.selectionEnd ?? element.value.length);
  return {
    kind: 'edit',
    format: DictationTextFormat.Plain,
    replace: text => field.replace(range, text),
    release: () => field.release(range),
  };
}

function editorTarget(span: EditorSpan): EditTarget {
  return {
    kind: 'edit',
    format: DictationTextFormat.Markdown,
    replace: text => span.replace(text),
    release: () => span.release(),
  };
}

export function captureTarget(element: Element | null): DictationTarget | null {
  const span = EditorSpan.capture(element);
  if (span) return editorTarget(span);
  const terminal = terminalTarget(element);
  if (terminal) return terminal;
  const field = writableField(element);
  return field ? fieldTarget(field) : null;
}
