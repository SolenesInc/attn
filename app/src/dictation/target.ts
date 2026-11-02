import { DictationTextFormat } from '../types/generated';
import { writableField } from '../utils/textFields';
import { EditorSpan } from './codemirror';
import { RichEditorSelection } from './richEditor';
import { TrackedField, type Insertion } from './field';
import { terminalTarget } from './terminal';

export interface EditTarget {
  kind: 'edit';
  format: DictationTextFormat;
  replace(text: string): Insertion | Promise<Insertion>;
  release(): void;
}

export type Paste = 'inserted' | 'targetGone';

interface PasteInto {
  kind: 'paste';
  format: DictationTextFormat;
  paste(text: string): Paste | Promise<Paste>;
  release(): void;
}

export interface TerminalPaste extends PasteInto {
  place: 'terminal';
  claimEnter(onEnter: () => void): void;
}

export interface RichEditorPaste extends PasteInto {
  place: 'richEditor';
}

export type PasteTarget = TerminalPaste | RichEditorPaste;

export type DictationTarget = EditTarget | PasteTarget;

export type TargetPlace = 'field' | PasteTarget['place'];

export const placeOf = (target: DictationTarget): TargetPlace => target.kind === 'edit' ? 'field' : target.place;

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

export function richEditorTarget(selection: RichEditorSelection): RichEditorPaste {
  return {
    kind: 'paste',
    place: 'richEditor',
    format: DictationTextFormat.Plain,
    paste: text => selection.insert(text),
    release: () => {},
  };
}

export function captureTarget(element: Element | null): DictationTarget | null {
  const span = EditorSpan.capture(element);
  if (span) return editorTarget(span);
  const terminal = terminalTarget(element);
  if (terminal) return terminal;
  const field = writableField(element);
  if (field) return fieldTarget(field);
  const rich = RichEditorSelection.capture(element);
  return rich ? richEditorTarget(rich) : null;
}
