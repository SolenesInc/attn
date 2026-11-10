import { useEffect, useRef, useState } from 'react';
import type { Annotation } from '../MarkdownReader/annotations/types';
import type { EditUnitKind } from './editUnit';

export type EditKind = EditUnitKind | 'block';

/** An edit being written: the lines of a file it takes, 0-based, read with the file's hash. */
export interface OpenEdit {
  path: string;
  hash: string;
  lines: readonly string[];
  kind: EditKind;
  from: number;
  to: number;
  docLine: number;
  problem?: string;
  saving?: boolean;
}

export function editText(edit: Pick<OpenEdit, 'lines' | 'from' | 'to'>): string {
  return edit.lines.slice(edit.from, edit.to + 1).join('\n');
}

function describe(edit: OpenEdit): string {
  const count = edit.to - edit.from + 1;
  switch (edit.kind) {
    case 'block': return 'this block, as markdown';
    case 'string': return `this string, ${count} lines`;
    case 'comment': return `this comment, ${count} lines`;
    default: return count > 1 ? `lines ${edit.from + 1}–${edit.to + 1}` : `line ${edit.from + 1}`;
  }
}

/** The 0-based line where an edit's new text stands in the file now: at the
 * line it was saved at, else at its only occurrence. Null when the text is
 * not there once. */
export function locateEdit(lines: readonly string[], edit: Annotation): number | null {
  if (!edit.edit || !edit.fileAnchor) return null;
  const wanted = edit.edit.now.split('\n');
  const standsAt = (at: number) => at >= 0 && wanted.every((line, offset) => lines[at + offset] === line);
  const saved = edit.fileAnchor.from - 1;
  if (standsAt(saved)) return saved;
  const found: number[] = [];
  for (let at = 0; at + wanted.length <= lines.length; at += 1) {
    if (standsAt(at)) found.push(at);
  }
  return found.length === 1 ? found[0] : null;
}

export function EditBox({
  edit,
  onSave,
  onTakeLine,
}: {
  edit: OpenEdit;
  onSave: (text: string) => void;
  onTakeLine: (direction: -1 | 1) => void;
}) {
  const original = editText(edit);
  const [text, setText] = useState(original);
  const takenRef = useRef(original);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => {
    if (takenRef.current === original) return;
    const before = takenRef.current;
    takenRef.current = original;
    setText((current) => {
      if (original.endsWith(before)) return original.slice(0, original.length - before.length) + current;
      if (original.startsWith(before)) return current + original.slice(before.length);
      return original;
    });
  }, [original]);
  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus({ preventScroll: true });
    input.setSelectionRange(input.value.length, input.value.length);
  }, []);
  const lineWise = edit.kind !== 'block';
  const several = edit.to > edit.from || edit.kind === 'block';
  return (
    <div className="review-composer review-editor" role="dialog" aria-label="Edit the file">
      <div className="review-composer-head">
        <span className="review-kicker">Editing</span>
        <span className="review-composer-place">{edit.path}: {describe(edit)}</span>
        <span className="review-grow" />
        <span className="review-composer-hint">Saved to the file and listed in the review</span>
      </div>
      <textarea
        ref={inputRef}
        className="review-composer-input review-editor-input"
        value={text}
        spellCheck={edit.kind === 'block'}
        rows={Math.min(16, text.split('\n').length + 1)}
        aria-label="Edit"
        readOnly={edit.saving}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing) return;
          if (lineWise && event.altKey && (event.key === 'ArrowUp' || event.key === 'ArrowDown')) {
            event.preventDefault();
            event.stopPropagation();
            onTakeLine(event.key === 'ArrowUp' ? -1 : 1);
            return;
          }
          const submit = event.key === 'Enter' && !event.shiftKey && !event.altKey && (several ? event.metaKey || event.ctrlKey : true);
          if (submit) {
            event.preventDefault();
            event.stopPropagation();
            if (!edit.saving) onSave(text);
          }
        }}
      />
      {edit.problem ? <div className="review-editor-problem" role="alert">{edit.problem}</div> : null}
      <div className="review-composer-keys">
        <kbd>{several ? '⌘↵' : '↵'}</kbd> save
        {lineWise ? <> <kbd>⌥↑</kbd><kbd>⌥↓</kbd> take in a line</> : null}
        {' '}<kbd>esc</kbd> cancel
      </div>
    </div>
  );
}
