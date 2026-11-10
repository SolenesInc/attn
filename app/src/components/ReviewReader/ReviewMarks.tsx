import { useEffect, useRef, useState } from 'react';
import type { Annotation, FileAnchor } from '../MarkdownReader/annotations/types';
import { fileAnchorPlace } from '../MarkdownReader/annotations/types';
import { sortAnnotations } from '../MarkdownReader/annotations/sortAnnotations';
import type { ReviewRow } from '../../types/generated';

export type MarkKind = 'comment' | 'question';
export type ReviewVerdict = 'approve' | 'request_changes' | 'comment';

/** What a review reader needs from the document's annotation draft and the tile's send. */
export interface ReviewMarks {
  annotations: Annotation[];
  addFileMark(type: MarkKind, text: string, anchor: FileAnchor): Annotation | null;
  addBlockMark(type: MarkKind, text: string, blockId: string): Annotation | null;
  addEditMark(anchor: FileAnchor, was: string, now: string): Annotation | null;
  deleteAnnotation(id: string): void;
  /** Delivers a question at once. Resolves to the reason it was not delivered, or null. */
  ask(id: string): Promise<string | null>;
  send(verdict: ReviewVerdict): void;
  /** Who a question or the review goes to, or null when the tile has no session. */
  destination: string | null;
}

export type MarkTarget =
  | { kind: 'file'; anchor: FileAnchor }
  | { kind: 'block'; blockId: string; quote: string };

export function markKey(path: string, line: number): string {
  return `${path}:${line}`;
}

/** File marks keyed by the file and the line they show under. Moved marks show elsewhere. */
export function marksByLine(annotations: readonly Annotation[], places: ReadonlyMap<string, MarkPlace>): ReadonlyMap<string, Annotation[]> {
  const byLine = new Map<string, Annotation[]>();
  for (const annotation of sortAnnotations([...annotations])) {
    const anchor = annotation.fileAnchor;
    const place = places.get(annotation.id);
    if (!anchor || place === undefined || place === 'moved') continue;
    const key = markKey(anchor.path, place);
    byLine.set(key, [...(byLine.get(key) ?? []), annotation]);
  }
  return byLine;
}

/**
 * The anchor for a mark on rows of a file: the lines at head the rows show. Rows head does
 * not have sit before the next row it has. With removed code in it, the quote keeps each
 * row's diff sign so it cannot pass for the code at those lines.
 */
export function fileRowsAnchor(path: string, picked: readonly ReviewRow[], following: readonly ReviewRow[], docLine: (line: number) => number): FileAnchor | null {
  const heads = picked.map((row) => row.head).filter((line): line is number => line !== undefined);
  const from = heads[0] ?? following.find((row) => row.head !== undefined)?.head;
  if (from === undefined) return null;
  const to = heads[heads.length - 1] ?? from;
  const diff = picked.some((row) => row.kind === '-');
  const quote = picked.map((row) => (diff ? `${row.kind}${row.text}` : row.text)).join('\n');
  return { path, from, to, docLine: docLine(from), quote };
}

/** Where a file mark shows: under a line now, or moved when its lines at head are gone. */
export type MarkPlace = number | 'moved';

/** Rows of a file, with the lines at head they cover. */
export interface FileRows {
  rows: readonly ReviewRow[];
  covers(head: number): boolean;
}

/**
 * Places a file mark by its lines at head: under the line now that holds its last line. It
 * is moved when rows that cover its lines show one of them gone, and null when no rows
 * cover them.
 */
export function placeFileMark(anchor: FileAnchor, lists: readonly FileRows[]): MarkPlace | null {
  for (const { rows, covers } of lists) {
    if (!covers(anchor.from) || !covers(anchor.to)) continue;
    const now = new Map(rows.flatMap((row) => (row.head !== undefined && row.now !== undefined ? [[row.head, row.now] as const] : [])));
    for (let line = anchor.from; line <= anchor.to; line++) if (!now.has(line)) return 'moved';
    return now.get(anchor.to) ?? 'moved';
  }
  return null;
}

function targetPlace(target: MarkTarget): string {
  return target.kind === 'file' ? fileAnchorPlace(target.anchor) : `“${target.quote.length > 80 ? `${target.quote.slice(0, 80)}…` : target.quote}”`;
}

function EditRow({ mark, range }: { mark: Annotation; range: string }) {
  return (
    <div className="review-mark review-mark--edit" data-review-mark={mark.id}>
      <span className="review-mark-who">you edited</span>
      <span className="review-note-range">{range}</span>
      <span className="review-mark-text">
        Saved to the file. It was: <code className="review-mark-was">{mark.edit?.was}</code>
      </span>
      <span className="review-composer-keys"><kbd>u</kbd> undo</span>
    </div>
  );
}

export function MarkRow({ mark, asking, onRemove }: { mark: Annotation; asking?: string; onRemove?: () => void }) {
  const question = mark.type === 'question';
  const anchor = mark.fileAnchor;
  const range = anchor ? (anchor.to > anchor.from ? `L${anchor.from}–${anchor.to}` : `L${anchor.from}`) : '';
  if (mark.type === 'edit') return <EditRow mark={mark} range={range} />;
  return (
    <div className={`review-mark${question ? ' review-mark--question' : ''}`} data-review-mark={mark.id}>
      <span className="review-mark-who">{question ? 'you asked' : 'you'}</span>
      <span className="review-note-range">{range}</span>
      <span className="review-mark-text">{mark.text}</span>
      {onRemove ? (
        <button type="button" className="review-mark-remove" tabIndex={-1} title="Remove" onClick={onRemove}>×</button>
      ) : null}
      {question ? (
        <span className={`review-mark-answer${mark.answer ? '' : ' review-mark-answer--waiting'}`}>
          {mark.answer ?? asking ?? 'Waiting for the answer'}
        </span>
      ) : null}
    </div>
  );
}

export function MarkComposer({
  kind,
  target,
  destination,
  onSave,
}: {
  kind: MarkKind;
  target: MarkTarget;
  destination: string | null;
  onSave: (text: string) => void;
}) {
  const [text, setText] = useState('');
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => inputRef.current?.focus({ preventScroll: true }), []);
  const asking = kind === 'question';
  const blocked = asking && destination === null;
  return (
    <div className="review-composer" role="dialog" aria-label={asking ? 'Ask a question' : 'Write a note'}>
      <div className="review-composer-head">
        <span className="review-kicker">{asking ? 'Question on' : 'Note on'}</span>
        <span className="review-composer-place">{targetPlace(target)}</span>
        <span className="review-grow" />
        <span className="review-composer-hint">
          {blocked
            ? 'Bind a session to this tile to ask'
            : asking
              ? `Sent to ${destination} at once`
              : 'Waits for the review'}
        </span>
      </div>
      <textarea
        ref={inputRef}
        className="review-composer-input"
        value={text}
        rows={2}
        placeholder={asking ? 'Ask the agent about these lines' : 'A note for the review'}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && !event.shiftKey && !event.altKey && !event.nativeEvent.isComposing) {
            event.preventDefault();
            event.stopPropagation();
            if (text.trim() !== '' && !blocked) onSave(text.trim());
          }
        }}
      />
      <div className="review-composer-keys"><kbd>↵</kbd> {asking ? 'ask' : 'save'} <kbd>⇧↵</kbd> new line <kbd>esc</kbd> cancel</div>
    </div>
  );
}

const VERDICTS: Array<{ verdict: ReviewVerdict; key: string; label: string }> = [
  { verdict: 'approve', key: 'a', label: 'Approve' },
  { verdict: 'request_changes', key: 'r', label: 'Request changes' },
  { verdict: 'comment', key: 'c', label: 'Comment' },
];

export function verdictForKey(key: string): ReviewVerdict | null {
  return VERDICTS.find((entry) => entry.key === key)?.verdict ?? null;
}

export function SendSheet({
  marks,
  verdict,
  destination,
  onPick,
  onSend,
}: {
  marks: readonly Annotation[];
  verdict: ReviewVerdict;
  destination: string | null;
  onPick: (verdict: ReviewVerdict) => void;
  onSend: () => void;
}) {
  const sorted = sortAnnotations(marks.filter((mark) => mark.type !== 'edit'));
  const edits = sortAnnotations(marks.filter((mark) => mark.type === 'edit'));
  return (
    <div className="review-send-sheet" role="dialog" aria-label="Send the review">
      <h3>Send the review{destination ? ` to ${destination}` : ''}</h3>
      {destination === null ? <p className="review-send-problem">Bind a session to this tile to send the review.</p> : null}
      {sorted.length === 0 && edits.length === 0 ? (
        <p className="review-send-empty">No notes: the verdict goes alone.</p>
      ) : sorted.length === 0 ? (
        null
      ) : (
        <ol className="review-send-marks">
          {sorted.map((mark) => (
            <li key={mark.id}>
              <span className="review-send-place">
                {mark.fileAnchor ? fileAnchorPlace(mark.fileAnchor) : mark.anchor ? `line ${mark.anchor.startLine}` : 'the document'}
              </span>
              <span className="review-send-kind">{mark.type === 'question' ? 'question' : mark.type === 'deletion' ? 'remove' : 'note'}</span>
              <span className="review-send-text">{mark.text ?? mark.anchor?.exact ?? ''}</span>
            </li>
          ))}
        </ol>
      )}
      {edits.length > 0 ? (
        <>
          <h4 className="review-send-heading">Your edits, saved to the files</h4>
          <ol className="review-send-marks review-send-edits">
            {edits.map((mark) => (
              <li key={mark.id}>
                <span className="review-send-place">{mark.fileAnchor ? fileAnchorPlace(mark.fileAnchor) : ''}</span>
                <span className="review-send-kind">edit</span>
                <span className="review-send-text">
                  <del>{mark.edit?.was}</del>
                  <ins>{mark.edit?.now}</ins>
                </span>
              </li>
            ))}
          </ol>
        </>
      ) : null}
      <div className="review-send-verdicts" role="radiogroup" aria-label="Verdict">
        {VERDICTS.map((entry) => (
          <button
            key={entry.verdict}
            type="button"
            tabIndex={-1}
            role="radio"
            aria-checked={verdict === entry.verdict}
            className={verdict === entry.verdict ? 'review-send-verdict--picked' : undefined}
            onClick={() => onPick(entry.verdict)}
          >
            <kbd>{entry.key}</kbd> {entry.label}
          </button>
        ))}
      </div>
      <div className="review-send-actions">
        <button type="button" tabIndex={-1} disabled={destination === null} onClick={onSend}>Send <kbd>↵</kbd></button>
        <span className="review-composer-keys"><kbd>esc</kbd> keep reviewing</span>
      </div>
    </div>
  );
}
