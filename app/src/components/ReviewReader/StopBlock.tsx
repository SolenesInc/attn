import { Fragment, useMemo } from 'react';
import type { ReviewNote, ReviewStop } from '../../types/generated';
import { CodeRow, rowKey } from './CodeRows';
import { expansionKey, useReviewReader, type FileTarget, type RowLabels, type StopExpansion } from './reviewContext';
import { MarkRow, markKey } from './ReviewMarks';
import { useTokens } from './tokenize';
import { DocStopRows, isDocumentPath } from './DocStop';
import type { NavLine } from './navigation';

function labelsAt(labels: RowLabels | null | undefined, cursorKey: string) {
  return labels && 'cursorKey' in labels.at && labels.at.cursorKey === cursorKey ? labels.names : undefined;
}

function askedAt(asked: NavLine['at'] | null | undefined, cursorKey: string) {
  return asked !== null && asked !== undefined && 'cursorKey' in asked && asked.cursorKey === cursorKey;
}

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

function stopRange(stop: ReviewStop): string {
  return stop.now_from ? `L${stop.now_from}–${stop.now_to}` : `L${stop.from}–${stop.to} at head`;
}

/** The lines a stop shows: its own range, or the lines around it that e added. */
function shownRange(stop: ReviewStop, expansion: StopExpansion | undefined): string {
  const lines = expansion?.rows.flatMap((row) => (row.now === undefined ? [] : [row.now])) ?? [];
  return lines.length > 0 ? `L${lines[0]}–${lines[lines.length - 1]}` : stopRange(stop);
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

export function stopTarget(stop: ReviewStop, line?: number): FileTarget {
  const range: [number, number] | undefined = stop.now_from ? [stop.now_from, stop.now_to] : undefined;
  return { path: stop.path, line: line ?? range?.[0], range, notes: stop.notes };
}

function noteCovers(note: ReviewNote, line: number | undefined): boolean {
  return line !== undefined && note.now_from > 0 && line >= note.now_from && line <= note.now_to;
}

/** A stop in the Walkthrough's script: where it is and what the agent says;
 * the code is on the stage beside it. */
function ScriptStop({ stop }: { stop: ReviewStop }) {
  const reader = useReviewReader();
  const stage = reader?.stage;
  const current = stage?.stopLine === stop.heading_line;
  return (
    <section
      className={`review-stop review-stop--script${current ? ' review-stop--current' : ''}`}
      data-review-stop-head={stop.heading_line}
      data-path={stop.path}
      aria-current={current || undefined}
      onClick={(event) => {
        if (current || event.currentTarget.ownerDocument.getSelection()?.isCollapsed === false) return;
        reader?.showStop(stop);
      }}
    >
      <header className="review-stop-header">
        <PathLabel path={stop.path} />
        <span className="review-stop-range">{stopRange(stop)}</span>
        {stop.changed_since_presented ? <span className="review-badge">changed since presented</span> : null}
      </header>
      {stop.notes.length > 0 ? (
        <div className="review-rows">
          {stop.notes.map((note) => (
            <div key={note.line} className={current && noteCovers(note, stage?.line) ? 'review-note-at' : undefined}>
              <NoteRow note={note} />
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}

export function StopBlock({ stop }: { stop: ReviewStop }) {
  const reader = useReviewReader();
  if (reader?.frame === 'walkthrough') return <ScriptStop stop={stop} />;
  return isDocumentPath(stop.path) ? <DocStop stop={stop} /> : <CodeStop stop={stop} />;
}

function DocStop({ stop }: { stop: ReviewStop }) {
  const reader = useReviewReader();
  const expansion = reader?.expansions.get(expansionKey(stop));
  return (
    <section className="review-stop review-stop--doc" data-review-stop-head={stop.heading_line} data-path={stop.path}>
      <header className="review-stop-header">
        <PathLabel path={stop.path} />
        <span className="review-stop-range">{shownRange(stop, expansion)}</span>
        {stop.changed_since_presented ? <span className="review-badge">changed since presented</span> : null}
        <span className="review-stop-actions">
          {reader?.openInEditor ? <button type="button" tabIndex={-1} onClick={() => reader.openInEditor?.(stop.path)}>editor <kbd>o</kbd></button> : null}
          <button type="button" tabIndex={-1} onClick={() => reader?.openFile(stopTarget(stop))}>source <kbd>↵</kbd></button>
        </span>
      </header>
      {expansion?.error ? <div className="review-stop-error" role="alert">{expansion.error}</div> : null}
      <DocStopRows stop={stop} />
    </section>
  );
}

function CodeStop({ stop }: { stop: ReviewStop }) {
  const reader = useReviewReader();
  const expansion = reader?.expansions.get(expansionKey(stop));
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
  const open = () => reader?.openFile(stopTarget(stop));

  return (
    <section className="review-stop" data-review-stop-head={stop.heading_line} data-path={stop.path}>
      <header className="review-stop-header">
        <PathLabel path={stop.path} />
        <span className="review-stop-range">{shownRange(stop, expansion)}</span>
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
              labels={labelsAt(reader?.labels, `stop:${stop.path}:${rowKey(row)}`)}
              asked={askedAt(reader?.asked, `stop:${stop.path}:${rowKey(row)}`)}
            />
            {row.now !== undefined ? notesAfter.get(row.now)?.map((note) => <NoteRow key={note.line} note={note} />) : null}
            {row.now !== undefined ? reader?.marksByLine.get(markKey(stop.path, row.now))?.map((mark) => (
              <MarkRow key={mark.id} mark={mark} asking={reader.asking.get(mark.id)} onRemove={reader.removeMark ? () => reader.removeMark?.(mark.id) : undefined} />
            )) : null}
          </Fragment>
        ))}
        {rows.length === 0 ? <div className="review-stop-empty">The file is gone or empty now.</div> : null}
      </div>
    </section>
  );
}
