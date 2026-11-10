import { Fragment, useMemo } from 'react';
import type { ReviewNote, ReviewStop } from '../../types/generated';
import { CodeRow, rowKey } from './CodeRows';
import { useReviewReader } from './reviewContext';
import { useTokens } from './tokenize';

function splitPath(path: string): { dir: string; name: string } {
  const at = path.lastIndexOf('/');
  return { dir: path.slice(0, at + 1), name: path.slice(at + 1) };
}

export function PathLabel({ path }: { path: string }) {
  const { dir, name } = splitPath(path);
  return (
    <span className="review-path">
      <span className="review-path-dir">{dir}</span>
      {name}
    </span>
  );
}

function noteRange(note: ReviewNote): string {
  const from = note.now_from || note.from;
  const to = note.now_from ? note.now_to : note.to;
  return from === to ? `L${from}` : `L${from}–${to}`;
}

function NoteRow({ note }: { note: ReviewNote }) {
  return (
    <div className={`review-note${note.look ? ' review-note--look' : ''}`} data-review-note={note.line}>
      <span className="review-note-who">{note.look ? 'look here' : 'agent'}</span>
      <span className="review-note-range">{noteRange(note)}</span>
      <span className="review-note-text">{note.text}</span>
    </div>
  );
}

export function StopBlock({ stop }: { stop: ReviewStop }) {
  const reader = useReviewReader();
  const expansion = reader?.expansions.get(stop.heading_line);
  const rows = expansion?.rows ?? stop.rows;
  const tokens = useTokens(useMemo(() => rows.map((row) => row.text), [rows]), stop.path);
  const notesAfter = useMemo(() => {
    const byLine = new Map<number, ReviewNote[]>();
    for (const note of stop.notes) {
      const line = note.now_to;
      byLine.set(line, [...(byLine.get(line) ?? []), note]);
    }
    return byLine;
  }, [stop.notes]);
  const shownLines = new Set(rows.map((row) => row.now).filter((line): line is number => line !== undefined));
  const unplaced = stop.notes.filter((note) => !shownLines.has(note.now_to));
  const nowRange = stop.now_from ? `L${stop.now_from}–${stop.now_to}` : `L${stop.from}–${stop.to} at head`;
  const open = () => reader?.openFile({ path: stop.path, line: stop.now_from || undefined, range: stop.now_from ? [stop.now_from, stop.now_to] : undefined });

  return (
    <section className="review-stop" data-review-stop-head={stop.heading_line} data-path={stop.path}>
      <header className="review-stop-header">
        <PathLabel path={stop.path} />
        <span className="review-stop-range">{nowRange}</span>
        {stop.changed_since_presented ? <span className="review-badge">changed since presented</span> : null}
        <span className="review-stop-actions">
          <button type="button" tabIndex={-1} onClick={open}>file <kbd>↵</kbd></button>
        </span>
      </header>
      {expansion?.error ? <div className="review-stop-error" role="alert">{expansion.error}</div> : null}
      <div className="review-rows">
        {unplaced.map((note) => <NoteRow key={note.line} note={note} />)}
        {rows.map((row, index) => (
          <Fragment key={rowKey(row)}>
            <CodeRow
              row={row}
              tokens={tokens?.[index]}
              path={stop.path}
              inRange={row.now !== undefined && row.now >= stop.now_from && row.now <= stop.now_to}
              cursorKey={`stop:${stop.path}:${rowKey(row)}`}
            />
            {row.now !== undefined ? notesAfter.get(row.now)?.map((note) => <NoteRow key={note.line} note={note} />) : null}
          </Fragment>
        ))}
        {rows.length === 0 ? <div className="review-stop-empty">The file is gone or empty now.</div> : null}
      </div>
    </section>
  );
}
