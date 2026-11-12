import { useEffect, useMemo, useState } from 'react';
import '../../src/App.css';
import { MarkdownReader } from '../../src/components/MarkdownReader';
import { fileMarkdownSource } from '../../src/components/MarkdownReader/documentSource';
import { setMarkdownAnnotationsTransport } from '../../src/components/MarkdownReader/annotations/transport';
import type { ReviewBundle, ReviewRow, ReviewStop } from '../../src/types/generated';
import type { HarnessProps } from '../types';

const SOURCE = fileMarkdownSource('test-harness', '/repo/review.md');
const LONG_PROSE = Array.from({ length: 14 }, (_, index) => `Paragraph ${index + 1} of the long step says why the store keeps both reads inside one transaction.`).join('\n\n');

const STOPS = [
  { path: 'poll.go', from: 10, to: 40 },
  { path: 'store.go', from: 100, to: 130 },
  { path: 'cache.go', from: 5, to: 35 },
];

function documentText(frame: string, editedAbove: boolean): string {
  return [
    '---', 'attn: review', `frame: ${frame}`, 'repository: /repo', 'base: 4be1c0d', 'head: 9f3a7e2', '---', '',
    '# Keep the poll inside one transaction', '',
    'The poll used to read and write the store in separate steps.', '',
    ...(editedAbove ? ['A paragraph added above every step after the review was read.', ''] : []),
    '## The poll asks for one write', '',
    'The poll hands every host to the store in one call.', '',
    `### ${STOPS[0].path}:${STOPS[0].from}-${STOPS[0].to}`, '',
    '## The store reads inside the write', '',
    LONG_PROSE, '',
    `### ${STOPS[1].path}:${STOPS[1].from}-${STOPS[1].to}`, '',
    'The cache follows from the same transaction.', '',
    `### ${STOPS[2].path}:${STOPS[2].from}-${STOPS[2].to}`, '',
    '## What stays the same', '',
    LONG_PROSE, '',
  ].join('\n');
}

function rowsOf(path: string, from: number, to: number): ReviewRow[] {
  return Array.from({ length: to - from + 1 }, (_, index) => ({ kind: index === 3 ? '+' : ' ', old: from + index, now: from + index, text: `\t${path.replace('.go', '')}Line${from + index}()` }));
}

function bundleOf(content: string, frame: string): ReviewBundle {
  const lines = content.split('\n');
  const stops: ReviewStop[] = STOPS.map(({ path, from, to }) => ({
    heading_line: lines.indexOf(`### ${path}:${from}-${to}`) + 1,
    path, from, to, now_from: from, now_to: to, changed_since_presented: false, file_hash: path,
    rows: rowsOf(path, from, to),
    notes: [],
  }));
  return {
    frame, repository: '/repo', base: '4be1c0d', head: '9f3a7e2', stops,
    files: STOPS.map(({ path }) => ({ path, status: 'modified', added: 1, removed: 0, binary: false })),
    flows: [], not_shown: [], navigation: [], problems: [],
  };
}

export function ReviewSteppingHarness({ onReady }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const frame = params.get('frame') ?? 'brief';
  const width = Number(params.get('width')) || 720;
  const height = Number(params.get('height')) || 480;
  const [editedAbove, setEditedAbove] = useState(false);
  const content = useMemo(() => documentText(frame, editedAbove), [editedAbove, frame]);
  const review = useMemo(() => bundleOf(content, frame), [content, frame]);
  useEffect(() => {
    setMarkdownAnnotationsTransport({
      getMarkdownAnnotations: async () => ({ annotations: [], generation: 0 }),
      saveMarkdownAnnotations: async (_source, upserts) => ({ annotations: upserts, generation: 1 }),
      submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
    });
    onReady();
    return () => setMarkdownAnnotationsTransport(null);
  }, [onReady]);
  return (
    <div>
      <section aria-label="Review tile" tabIndex={-1} className="desktop-dock-tile-body" style={{ width, height, position: 'relative' }}>
        <MarkdownReader content={content} source={SOURCE} review={review} annotationsEnabled />
      </section>
      <button type="button" onClick={() => setEditedAbove(true)}>Edit above</button>
    </div>
  );
}
