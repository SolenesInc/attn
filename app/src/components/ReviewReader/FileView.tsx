import { useLayoutEffect, useMemo, useState, type CSSProperties, type RefObject } from 'react';
import type { ReviewNote, ReviewRow } from '../../types/generated';
import { CodeRow, rowKey } from './CodeRows';
import type { FileTarget } from './reviewContext';
import { NoteRow, PathLabel } from './StopBlock';
import { useResizeWatch } from '../../tileWindow';
import { useCachedTokens, type TokenCache } from './tokenize';
import { useReviewReader } from './reviewContext';
import { MarkRow, markKey } from './ReviewMarks';

export const FILE_ROW_HEIGHT = 20;
const OVERSCAN_ROWS = 30;
const TAB_SIZE = 8;

function columnsOf(text: string): number {
  let columns = 0;
  for (const character of text) columns = character === '\t' ? columns + TAB_SIZE - (columns % TAB_SIZE) : columns + 1;
  return columns;
}

function widestLine(rows: readonly ReviewRow[]): number {
  return rows.reduce((widest, row) => Math.max(widest, columnsOf(row.text)), 0);
}

export interface OpenFile {
  id: number;
  target: FileTarget;
  rows: ReviewRow[] | null;
  /** The content hash of the file the rows were read from. */
  hash?: string;
  error?: string;
  problem?: string;
  cursor: number;
  /** The other end of a selection that ends at the cursor. */
  selectFrom?: number;
  /** The line an edit made here changed: the next read puts the cursor on it. */
  edited?: number;
}

export function rowIndexOf(rows: readonly ReviewRow[], line: number | undefined): number {
  if (line === undefined) return 0;
  const exact = rows.findIndex((row) => row.now === line);
  if (exact >= 0) return exact;
  const after = rows.findIndex((row) => row.now !== undefined && row.now > line);
  return after >= 0 ? after : Math.max(0, rows.length - 1);
}

/** The row of a file's new rows that the cursor stood on in its old rows. A row
 * that exists in the base is found by its base line, which an edit of the file
 * never moves. An added row is found among the added rows after the nearest
 * base row above it, by its text nearest where it stood in that run, else by its place in it. */
export function rowIndexAfterRefresh(rows: readonly ReviewRow[], before: OpenFile): number {
  const was = before.rows?.[before.cursor];
  if (!before.rows || !was) return rowIndexOf(rows, before.target.line);
  if (was.head !== undefined) {
    const same = rows.findIndex((row) => row.head === was.head);
    if (same >= 0) return same;
  }
  if (was.old !== undefined) {
    const same = rows.findIndex((row) => row.old === was.old);
    return same >= 0 ? same : rowIndexOf(rows, was.now);
  }
  let anchor = before.cursor;
  while (anchor >= 0 && before.rows[anchor].old === undefined) anchor -= 1;
  const newAnchor = anchor < 0 ? -1 : rows.findIndex((row) => row.old === before.rows![anchor].old);
  if (anchor >= 0 && newAnchor < 0) return rowIndexOf(rows, was.now);
  let end = newAnchor + 1;
  while (end < rows.length && rows[end].old === undefined) end += 1;
  const run = rows.slice(newAnchor + 1, end);
  if (run.length === 0) return rowIndexOf(rows, was.now);
  const expected = Math.min(run.length - 1, before.cursor - anchor - 1);
  const sameText = run.map((row, at) => (row.text === was.text ? at : -1)).filter((at) => at >= 0);
  const nearest = sameText.reduce((best, at) => (Math.abs(at - expected) < Math.abs(best - expected) ? at : best), sameText[0] ?? expected);
  return newAnchor + 1 + nearest;
}

/** The first row of each run of changed rows. */
export function changeStarts(rows: readonly ReviewRow[]): number[] {
  const starts: number[] = [];
  rows.forEach((row, index) => {
    if (row.kind !== ' ' && (index === 0 || rows[index - 1].kind === ' ')) starts.push(index);
  });
  return starts;
}

/** The lines agent notes point at, and whether any of them is the place to look. */
function notedLines(notes: readonly ReviewNote[]): ReadonlyMap<number, boolean> {
  const noted = new Map<number, boolean>();
  for (const note of notes) {
    if (note.now_from === 0) continue;
    for (let line = note.now_from; line <= note.now_to; line += 1) noted.set(line, (noted.get(line) ?? false) || note.look);
  }
  return noted;
}

/** Which rows stand inside a range of lines now: a removed row stands
 * inside when a line next to it does. */
function rowsInside(rows: readonly ReviewRow[], range: [number, number]): boolean[] {
  const inside = (line: number | undefined) => line !== undefined && line >= range[0] && line <= range[1];
  const result = rows.map((row) => inside(row.now));
  let previous: number | undefined;
  rows.forEach((row, index) => {
    if (row.now !== undefined) previous = row.now;
    else if (inside(previous)) result[index] = true;
  });
  let next: number | undefined;
  for (let index = rows.length - 1; index >= 0; index -= 1) {
    if (rows[index].now !== undefined) next = rows[index].now;
    else if (inside(next)) result[index] = true;
  }
  return result;
}

export function FileView({
  file,
  tokenCache,
  filled,
  bodyRef,
  stage = false,
  canGoBack,
  onBack,
  onClose,
  onPick,
}: {
  file: OpenFile;
  tokenCache: TokenCache;
  filled: boolean;
  bodyRef: RefObject<HTMLDivElement | null>;
  stage?: boolean;
  canGoBack: boolean;
  onBack: () => void;
  onClose?: () => void;
  onPick: (index: number) => void;
}) {
  const [view, setView] = useState({ top: 0, height: 0 });
  const rows = file.rows;
  const lines = useMemo(() => rows?.map((row) => row.text) ?? null, [rows]);
  const { tokens, earlier } = useCachedTokens(tokenCache, file.target.path, file.hash, lines);
  const range = file.target.range;
  const reader = useReviewReader();
  const stops = reader?.bundle.stops;
  const path = file.target.path;
  const outside = file.target.outside;
  const notes = useMemo(() => (outside ? [] : (stops ?? []).filter((stop) => stop.path === path).flatMap((stop) => stop.notes)), [outside, path, stops]);
  const noted = useMemo(() => notedLines(notes), [notes]);
  const inside = useMemo(() => (stage && range && rows ? rowsInside(rows, range) : null), [range, rows, stage]);
  const widest = useMemo(() => (rows ? widestLine(rows) : 0), [rows]);
  const resizeWatch = useResizeWatch();
  const labels = reader?.labels;
  const asked = reader?.asked;
  const marked = (row: ReviewRow) => row.now !== undefined && (reader?.marksByLine.has(markKey(path, row.now)) ?? false);
  const cursorLine = rows?.[file.cursor]?.now;
  const cursorMarks = cursorLine !== undefined ? reader?.marksByLine.get(markKey(path, cursorLine)) ?? [] : [];
  const cursorNotes = cursorLine !== undefined ? notes.filter((note) => note.now_from > 0 && cursorLine >= note.now_from && cursorLine <= note.now_to) : [];
  const selectFrom = Math.min(file.selectFrom ?? file.cursor, file.cursor);
  const selectTo = Math.max(file.selectFrom ?? file.cursor, file.cursor);

  useLayoutEffect(() => {
    const element = bodyRef.current;
    if (!element) return undefined;
    const measure = () => setView({ top: element.scrollTop, height: element.clientHeight });
    measure();
    resizeWatch.observe(element, measure);
    return () => resizeWatch.disconnect();
  }, [bodyRef, resizeWatch]);

  useLayoutEffect(() => {
    const element = bodyRef.current;
    if (!element || !rows) return;
    const rowTop = file.cursor * FILE_ROW_HEIGHT;
    const height = element.clientHeight;
    if (rowTop < element.scrollTop || rowTop + FILE_ROW_HEIGHT > element.scrollTop + height) {
      element.scrollTop = Math.max(0, rowTop - height / 3);
      setView({ top: element.scrollTop, height });
    }
  }, [bodyRef, file.cursor, rows]);

  const first = Math.max(0, Math.floor(view.top / FILE_ROW_HEIGHT) - OVERSCAN_ROWS);
  const last = rows ? Math.min(rows.length, Math.ceil((view.top + view.height) / FILE_ROW_HEIGHT) + OVERSCAN_ROWS) : 0;

  return (
    <aside
      className={`review-file-view${stage ? ' review-file-view--stage' : filled ? ' review-file-view--filled' : ''}`}
      aria-label="File view"
      data-review-file-view=""
    >
      <header className="review-file-view-header">
        <button type="button" tabIndex={-1} disabled={!canGoBack} onClick={onBack}>← back <kbd>⌃o</kbd></button>
        <PathLabel path={file.target.path} />
        {file.target.outside ? <span className="review-badge" title="A definition outside the review's repository, shown as it is on disk">outside the repository · read-only</span> : null}
        {rows ? <span className="review-file-view-lines">{rows.filter((row) => row.now !== undefined).length} lines</span> : null}
        <span className="review-grow" />
        {onClose ? <button type="button" tabIndex={-1} onClick={onClose}>close <kbd>esc</kbd></button> : null}
      </header>
      {file.problem ? <div className="review-file-view-problem" role="status">{file.problem}</div> : null}
      <div className="review-file-view-body" ref={bodyRef} onScroll={(event) => setView({ top: event.currentTarget.scrollTop, height: event.currentTarget.clientHeight })}>
        {file.error ? <div className="review-file-view-message" role="alert">{file.error}</div> : null}
        {!rows && !file.error ? <div className="review-file-view-message">Reading {file.target.path}…</div> : null}
        {rows ? (
          <div className="review-file-view-rows" style={{ height: rows.length * FILE_ROW_HEIGHT, '--review-file-columns': widest } as CSSProperties}>
            {rows.slice(first, last).map((row, offset) => {
              const index = first + offset;
              const look = row.now === undefined ? undefined : noted.get(row.now);
              const mark = look === undefined ? '' : look ? ' review-file-view-row--look' : ' review-file-view-row--noted';
              return (
                <div
                  key={rowKey(row)}
                  className={`review-file-view-row${mark}${index === file.cursor ? ' review-cursor' : ''}${file.selectFrom !== undefined && index >= selectFrom && index <= selectTo ? ' review-selected' : ''}${marked(row) ? ' review-file-view-row--marked' : ''}${inside && !inside[index] ? ' review-file-view-row--outside' : ''}`}
                  style={{ top: index * FILE_ROW_HEIGHT, height: FILE_ROW_HEIGHT }}
                  data-row-index={index}
                  onClick={() => onPick(index)}
                >
                  <CodeRow
                    row={row}
                    tokens={tokens[index] ?? earlier?.get(row.text)}
                    path={file.target.path}
                    inRange={range !== undefined && row.now !== undefined && row.now >= range[0] && row.now <= range[1]}
                    labels={labels && 'fileId' in labels.at && labels.at.fileId === file.id && labels.at.row === index ? labels.names : undefined}
                    asked={asked !== null && asked !== undefined && 'fileId' in asked && asked.fileId === file.id && asked.row === index}
                  />
                </div>
              );
            })}
          </div>
        ) : null}
      </div>
      {cursorNotes.length > 0 || cursorMarks.length > 0 ? (
        <div className="review-file-view-marks" aria-label="Marks on this line">
          {cursorNotes.map((note) => <NoteRow key={note.line} note={note} />)}
          {cursorMarks.map((mark) => (
            <MarkRow key={mark.id} mark={mark} asking={reader?.asking.get(mark.id)} onRemove={reader?.removeMark ? () => reader.removeMark?.(mark.id) : undefined} />
          ))}
        </div>
      ) : null}
    </aside>
  );
}
