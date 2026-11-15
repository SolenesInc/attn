import { useEffect, useRef, useState } from 'react';
import { keyCombo } from '../../shortcuts/formatShortcut';
import type { ReviewRow } from '../../types/generated';
import type { AnnotationEdit } from '../MarkdownReader/annotations/types';
import type { EditUnitKind } from './editUnit';

export type EditKind = EditUnitKind | 'block';

/** An edit being written: the lines of a file it takes, 0-based, read with the file's hash. */
export interface OpenEdit {
  path: string;
  hash: string;
  lines: readonly string[];
  heads: readonly (number | undefined)[];
  kind: EditKind;
  from: number;
  to: number;
  docLine: number;
  taken?: -1 | 1;
  problem?: string;
  saving?: boolean;
}

/** A file's lines as the edit box shows them, without the carriage return of a
 * CRLF file, which the daemon writes back around the edited lines, and the line
 * at head each one is. */
export function fileLines(rows: readonly ReviewRow[] | undefined): { lines: string[]; heads: Array<number | undefined> } {
  const now = (rows ?? []).filter((row) => row.now !== undefined);
  return { lines: now.map((row) => row.text.replace(/\r$/, '')), heads: now.map((row) => row.head) };
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

/** Where an edit of the 0-based lines `from` to `to` stands: between the
 * nearest lines head has above and below it, 0 where there is none. */
export function whereEditStands(heads: readonly (number | undefined)[], from: number, to: number): Pick<AnnotationEdit, 'afterHead' | 'beforeHead'> {
  let above = from - 1;
  while (above >= 0 && heads[above] === undefined) above -= 1;
  let below = to + 1;
  while (below < heads.length && heads[below] === undefined) below += 1;
  return { afterHead: above >= 0 ? heads[above]! : 0, beforeHead: below < heads.length ? heads[below]! : 0 };
}

/**
 * The line now where an edit's text starts. It is looked for only between the
 * lines head has that hold it in, found the way marks are: the nearest still
 * there at or above the line it followed and at or below the line it
 * preceded. Moved when the text is not there exactly once; null when the rows
 * do not reach that span and the whole file is not read.
 */
export function editStart(rows: readonly ReviewRow[], edit: AnnotationEdit, whole: boolean): number | 'moved' | null {
  const textOfNow = new Map<number, string>();
  let low = 0;
  let high = Infinity;
  let last = 0;
  for (const row of rows) {
    if (row.now === undefined) continue;
    textOfNow.set(row.now, row.text.replace(/\r$/, ''));
    last = Math.max(last, row.now);
    if (row.head === undefined) continue;
    if (row.head <= edit.afterHead) low = Math.max(low, row.now);
    if (edit.beforeHead > 0 && row.head >= edit.beforeHead) high = Math.min(high, row.now);
  }
  const unseen = whole ? 'moved' : null;
  const end = high === Infinity ? (whole ? last + 1 : Infinity) : high;
  if (end === Infinity || (!whole && edit.afterHead > 0 && low === 0)) return unseen;
  const wanted = edit.now.split('\n');
  const found: number[] = [];
  for (let start = low + 1; start + wanted.length <= end; start += 1) {
    const texts = wanted.map((_, index) => textOfNow.get(start + index));
    if (texts.some((text) => text === undefined)) return unseen;
    if (texts.every((text, index) => text === wanted[index])) found.push(start);
  }
  return found.length === 1 ? found[0] : 'moved';
}

/** Where an edit's mark shows: under the line now that holds its last line. */
export function placeEdit(edit: AnnotationEdit, rows: readonly ReviewRow[], whole: boolean): number | 'moved' | null {
  const start = editStart(rows, edit, whole);
  return typeof start === 'number' ? start + edit.now.split('\n').length - 1 : start;
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
    setText((current) => (edit.taken === -1
      ? original.slice(0, original.length - before.length) + current
      : current + original.slice(before.length)));
  }, [edit.taken, original]);
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
        <kbd>{several ? keyCombo('accel', '↵') : '↵'}</kbd> save
        {lineWise ? <> <kbd>⌥↑</kbd><kbd>⌥↓</kbd> take in a line</> : null}
        {' '}<kbd>esc</kbd> cancel
      </div>
    </div>
  );
}
