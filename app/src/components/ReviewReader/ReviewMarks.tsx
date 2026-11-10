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

/** File marks keyed by the file and the last line they cover, where they show. */
export function marksByLastLine(annotations: readonly Annotation[]): ReadonlyMap<string, Annotation[]> {
  const byLine = new Map<string, Annotation[]>();
  for (const annotation of sortAnnotations([...annotations])) {
    const anchor = annotation.fileAnchor;
    if (!anchor) continue;
    const key = markKey(anchor.path, anchor.to);
    byLine.set(key, [...(byLine.get(key) ?? []), annotation]);
  }
  return byLine;
}

/**
 * The anchor for a mark on rows of a file. It covers the head lines the rows show; rows
 * that only show removed code sit before the next head line. With removed code in it,
 * the quote keeps each row's diff sign so it cannot pass for the code at those lines.
 */
export function fileRowsAnchor(path: string, picked: readonly ReviewRow[], following: readonly ReviewRow[], docLine: (line: number) => number): FileAnchor | null {
  const lines = picked.map((row) => row.now).filter((line): line is number => line !== undefined);
  const line = lines[0] ?? following.find((row) => row.now !== undefined)?.now ?? [...picked].reverse().find((row) => row.old !== undefined)?.old;
  if (line === undefined) return null;
  const diff = picked.some((row) => row.kind === '-');
  const quote = picked.map((row) => (diff ? `${row.kind}${row.text}` : row.text)).join('\n');
  return { path, from: line, to: lines[lines.length - 1] ?? line, docLine: docLine(line), quote };
}

function targetPlace(target: MarkTarget): string {
  return target.kind === 'file' ? fileAnchorPlace(target.anchor) : `“${target.quote.length > 80 ? `${target.quote.slice(0, 80)}…` : target.quote}”`;
}

export function MarkRow({ mark, asking, onRemove }: { mark: Annotation; asking?: string; onRemove?: () => void }) {
  const question = mark.type === 'question';
  const anchor = mark.fileAnchor;
  const range = anchor ? (anchor.to > anchor.from ? `L${anchor.from}–${anchor.to}` : `L${anchor.from}`) : '';
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
  onCancel,
}: {
  kind: MarkKind;
  target: MarkTarget;
  destination: string | null;
  onSave: (text: string) => void;
  onCancel: () => void;
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
          if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            onCancel();
          } else if (event.key === 'Enter' && !event.shiftKey && !event.altKey && !event.nativeEvent.isComposing) {
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
  const sorted = sortAnnotations([...marks]);
  return (
    <div className="review-send-sheet" role="dialog" aria-label="Send the review">
      <h3>Send the review{destination ? ` to ${destination}` : ''}</h3>
      {destination === null ? <p className="review-send-problem">Bind a session to this tile to send the review.</p> : null}
      {sorted.length === 0 ? (
        <p className="review-send-empty">No notes: the verdict goes alone.</p>
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
