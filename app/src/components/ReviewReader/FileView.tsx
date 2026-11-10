import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { ReviewRow } from '../../types/generated';
import { CodeRow, rowKey } from './CodeRows';
import type { FileTarget } from './reviewContext';
import { PathLabel } from './StopBlock';
import { useResizeWatch } from '../../tileWindow';
import { tokenizeLines, type TokenLine } from './tokenize';

export const FILE_ROW_HEIGHT = 20;
const OVERSCAN_ROWS = 30;

export interface OpenFile {
  id: number;
  target: FileTarget;
  rows: ReviewRow[] | null;
  error?: string;
  problem?: string;
  cursor: number;
}

export function rowIndexOf(rows: readonly ReviewRow[], line: number | undefined): number {
  if (line === undefined) return 0;
  const exact = rows.findIndex((row) => row.now === line);
  if (exact >= 0) return exact;
  const after = rows.findIndex((row) => row.now !== undefined && row.now > line);
  return after >= 0 ? after : Math.max(0, rows.length - 1);
}

/** The first row of each run of changed rows. */
export function changeStarts(rows: readonly ReviewRow[]): number[] {
  const starts: number[] = [];
  rows.forEach((row, index) => {
    if (row.kind !== ' ' && (index === 0 || rows[index - 1].kind === ' ')) starts.push(index);
  });
  return starts;
}

function useFileTokens(rows: ReviewRow[] | null, path: string): readonly (TokenLine | undefined)[] {
  const [tokens, setTokens] = useState<readonly (TokenLine | undefined)[]>([]);
  useEffect(() => {
    setTokens([]);
    if (!rows || rows.length === 0) return undefined;
    let cancelled = false;
    const filled: (TokenLine | undefined)[] = Array.from({ length: rows.length });
    void tokenizeLines(rows.map((row) => row.text), path, (start, chunk) => {
      chunk.forEach((line, index) => { filled[start + index] = line; });
      setTokens(filled.slice());
    }, () => cancelled);
    return () => { cancelled = true; };
  }, [path, rows]);
  return tokens;
}

/** The lines agent notes point at, and whether any of them is the place to look. */
function notedLines(notes: FileTarget['notes']): ReadonlyMap<number, boolean> {
  const noted = new Map<number, boolean>();
  for (const note of notes ?? []) {
    if (note.now_from === 0) continue;
    for (let line = note.now_from; line <= note.now_to; line += 1) noted.set(line, (noted.get(line) ?? false) || note.look);
  }
  return noted;
}

export function FileView({
  file,
  filled,
  stage = false,
  canGoBack,
  onBack,
  onClose,
  onPick,
}: {
  file: OpenFile;
  filled: boolean;
  stage?: boolean;
  canGoBack: boolean;
  onBack: () => void;
  onClose?: () => void;
  onPick: (index: number) => void;
}) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [view, setView] = useState({ top: 0, height: 0 });
  const rows = file.rows;
  const tokens = useFileTokens(rows, file.target.path);
  const range = file.target.range;
  const notes = file.target.notes;
  const noted = useMemo(() => notedLines(notes), [notes]);
  const resizeWatch = useResizeWatch();

  useLayoutEffect(() => {
    const element = scrollRef.current;
    if (!element) return undefined;
    const measure = () => setView({ top: element.scrollTop, height: element.clientHeight });
    measure();
    resizeWatch.observe(element, measure);
    return () => resizeWatch.disconnect();
  }, [resizeWatch]);

  useLayoutEffect(() => {
    const element = scrollRef.current;
    if (!element || !rows) return;
    const rowTop = file.cursor * FILE_ROW_HEIGHT;
    const height = element.clientHeight;
    if (rowTop < element.scrollTop || rowTop + FILE_ROW_HEIGHT > element.scrollTop + height) {
      element.scrollTop = Math.max(0, rowTop - height / 3);
      setView({ top: element.scrollTop, height });
    }
  }, [file.cursor, rows]);

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
        {rows ? <span className="review-file-view-lines">{rows.filter((row) => row.now !== undefined).length} lines</span> : null}
        <span className="review-grow" />
        {onClose ? <button type="button" tabIndex={-1} onClick={onClose}>close <kbd>esc</kbd></button> : null}
      </header>
      {file.problem ? <div className="review-file-view-problem" role="status">{file.problem}</div> : null}
      <div className="review-file-view-body" ref={scrollRef} onScroll={(event) => setView({ top: event.currentTarget.scrollTop, height: event.currentTarget.clientHeight })}>
        {file.error ? <div className="review-file-view-message" role="alert">{file.error}</div> : null}
        {!rows && !file.error ? <div className="review-file-view-message">Reading {file.target.path}…</div> : null}
        {rows ? (
          <div className="review-file-view-rows" style={{ height: rows.length * FILE_ROW_HEIGHT }}>
            {rows.slice(first, last).map((row, offset) => {
              const index = first + offset;
              const look = row.now === undefined ? undefined : noted.get(row.now);
              const mark = look === undefined ? '' : look ? ' review-file-view-row--look' : ' review-file-view-row--noted';
              return (
                <div
                  key={rowKey(row)}
                  className={`review-file-view-row${mark}${index === file.cursor ? ' review-cursor' : ''}`}
                  style={{ top: index * FILE_ROW_HEIGHT, height: FILE_ROW_HEIGHT }}
                  data-row-index={index}
                  onClick={() => onPick(index)}
                >
                  <CodeRow
                    row={row}
                    tokens={tokens[index]}
                    path={file.target.path}
                    inRange={range !== undefined && row.now !== undefined && row.now >= range[0] && row.now <= range[1]}
                  />
                </div>
              );
            })}
          </div>
        ) : null}
      </div>
    </aside>
  );
}
