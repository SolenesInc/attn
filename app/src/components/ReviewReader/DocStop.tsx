import { Fragment, useMemo, type ReactNode } from 'react';
import type { ReviewNote, ReviewStop } from '../../types/generated';
import { Markdown } from '../Markdown';
import { diffWords, docBlocks, isRewording, renderedWords, type DocBlock, type Word, type WordOp } from './docBlocks';
import { MarkRow, markKey } from './ReviewMarks';
import { useReviewReader } from './reviewContext';

const PROSE = new Set(['paragraph', 'listItem', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6']);

export function isDocumentPath(path: string): boolean {
  return path.toLowerCase().endsWith('.md');
}

function WordView({ word }: { word: Word }) {
  let node: ReactNode = word.text;
  for (const mark of [...word.marks].reverse()) {
    if (mark === 'code') node = <code>{node}</code>;
    else if (mark === 'emphasis') node = <em>{node}</em>;
    else if (mark === 'strong') node = <strong>{node}</strong>;
    else if (mark === 'delete') node = <s>{node}</s>;
    else if (mark === 'link') node = <a href={word.href}>{node}</a>;
  }
  return <>{node}</>;
}

function WordDiff({ ops }: { ops: readonly WordOp[] }) {
  return (
    <div className="review-doc-words">
      {ops.map((entry, index) => {
        const word = <WordView word={entry.word} />;
        const spaced = index > 0 ? ' ' : '';
        if (entry.op === '+') return <Fragment key={index}>{spaced}<ins>{word}</ins></Fragment>;
        if (entry.op === '-') return <Fragment key={index}>{spaced}<del>{word}</del></Fragment>;
        return <Fragment key={index}>{spaced}{word}</Fragment>;
      })}
    </div>
  );
}

function BlockBody({ block }: { block: DocBlock }) {
  const ops = useMemo(() => {
    if (block.was === null || !PROSE.has(block.kind)) return null;
    const changes = diffWords(renderedWords(block.was), renderedWords(block.source));
    return changes && isRewording(changes) ? changes : null;
  }, [block]);
  if (ops) return <WordDiff ops={ops} />;
  return (
    <>
      {block.was !== null ? <del className="review-doc-was"><Markdown>{block.was}</Markdown></del> : null}
      <Markdown>{block.source}</Markdown>
    </>
  );
}

function NoteRow({ note }: { note: ReviewNote }) {
  return (
    <div className={`review-note${note.look ? ' review-note--look' : ''}`} data-review-note={note.line}>
      <span className="review-note-who">{note.look ? 'look here' : 'agent'}</span>
      <span className="review-note-range">{note.now_from === note.now_to ? `L${note.now_from}` : `L${note.now_from}–${note.now_to}`}</span>
      <span className="review-note-text">{note.text}</span>
    </div>
  );
}

/** A stop on a markdown file, rendered as the document it is: changed blocks
 * marked, a light rewording with its changed words, a rewrite with the old
 * text struck above the new. */
export function DocStopRows({ stop }: { stop: ReviewStop }) {
  const reader = useReviewReader();
  const rows = reader?.expansions.get(stop.heading_line)?.rows ?? stop.rows;
  const { blocks, removedAfter } = useMemo(() => docBlocks(rows), [rows]);
  const within = (block: DocBlock, line: number) => line >= block.from && line <= block.to;
  const unplaced = stop.notes.filter((note) => !blocks.some((block) => note.now_from > 0 && within(block, note.now_to)));
  return (
    <div className="review-rows review-doc-blocks">
      {unplaced.map((note) => <NoteRow key={note.line} note={note} />)}
      {blocks.map((block) => {
        const marks = [];
        for (let line = block.from; line <= block.to; line += 1) marks.push(...(reader?.marksByLine.get(markKey(stop.path, line)) ?? []));
        return (
          <Fragment key={block.from}>
            {block.deletedBefore !== null ? <del className="review-doc-was review-doc-block"><Markdown>{block.deletedBefore}</Markdown></del> : null}
            <div
              className={`review-doc-block review-doc-block--${block.kind}${block.changed ? ' review-doc-block--changed' : ''}${block.from <= stop.now_to && block.to >= stop.now_from ? ' review-row--in-range' : ''}`}
              data-review-cursor="block"
              data-cursor-key={`stop:${stop.path}:block:${block.from}`}
              data-path={stop.path}
              data-line={block.from}
              data-line-to={block.to}
            >
              <BlockBody block={block} />
            </div>
            {stop.notes.filter((note) => note.now_from > 0 && within(block, note.now_to)).map((note) => <NoteRow key={note.line} note={note} />)}
            {marks.map((mark) => (
              <MarkRow key={mark.id} mark={mark} asking={reader?.asking.get(mark.id)} onRemove={reader?.removeMark ? () => reader.removeMark?.(mark.id) : undefined} />
            ))}
          </Fragment>
        );
      })}
      {removedAfter !== null ? <del className="review-doc-was review-doc-block"><Markdown>{removedAfter}</Markdown></del> : null}
      {blocks.length === 0 && removedAfter === null ? <div className="review-stop-empty">The file is gone or empty now.</div> : null}
    </div>
  );
}
