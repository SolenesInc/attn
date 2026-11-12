import { useEffect, useMemo } from 'react';
import '../../src/App.css';
import { MarkdownReader, type ReviewSend } from '../../src/components/MarkdownReader';
import { fileMarkdownSource } from '../../src/components/MarkdownReader/documentSource';
import { setMarkdownAnnotationsTransport } from '../../src/components/MarkdownReader/annotations/transport';
import type { ReviewSetup } from '../../src/components/ReviewReader/ReviewKeys';
import { DaemonApiProvider, type DaemonApi } from '../../src/contexts/DaemonApiContext';
import type { ReviewBundle, ReviewNavigation, ReviewRow } from '../../src/types/generated';
import type { HarnessProps } from '../types';

const DOC = '/notes/review.md';
const SOURCE = fileMarkdownSource('test-harness', DOC);

const LONG_LINE = '\tif err := store.ReplacePRsOfHosts(ctx, prsByHost, replaceOptions{keepClosed: true, keepDrafts: true, keepMerged: false}); err != nil { /* the end of the long line */';

const DOCUMENT = `---
attn: review
frame: brief
repository: /repo
base: 4be1c0d
head: 9f3a7e2
---

# Replace the rows in one call

The poll keeps every host's pull requests.

## The poll replaces the rows

### poll.go:1-4

- L3!: One call replaces every host's rows.

\`\`\`mermaid
sequenceDiagram
  participant poll as Poll
  participant store as Store
  poll->>store: ReplacePRsOfHosts(prsByHost)
  %% stop: poll.go:1-4
\`\`\`
`;

const row = (kind: string, old: number | undefined, now: number | undefined, text: string): ReviewRow => ({
  kind,
  ...(old !== undefined ? { old } : {}),
  ...(now !== undefined ? { now, head: now } : {}),
  text,
});

const STOP_ROWS = [
  row(' ', 1, 1, 'func poll(ctx context.Context, store *Store) error {'),
  row(' ', 2, 2, '\tprsByHost := fetch(ctx)'),
  row('-', 3, undefined, '\tstore.Replace(prsByHost)'),
  row('+', undefined, 3, LONG_LINE),
  row(' ', 4, 4, '}'),
];

const FILE_ROWS = [
  ...STOP_ROWS,
  ...Array.from({ length: 40 }, (_, index) => row(' ', 5 + index, 5 + index, `// line ${5 + index}`)),
];

function bundle(navigation: ReviewNavigation[]): ReviewBundle {
  return {
    frame: 'brief',
    repository: '/repo',
    base: '4be1c0d',
    head: '9f3a7e2',
    stops: [{
      heading_line: 15, path: 'poll.go', from: 1, to: 4, now_from: 1, now_to: 4, changed_since_presented: false, file_hash: 'h1',
      rows: STOP_ROWS,
      notes: [{ line: 17, from: 3, to: 3, now_from: 3, now_to: 3, look: true, text: "One call replaces every host's rows." }],
    }],
    files: [{ path: 'poll.go', status: 'modified', added: 1, removed: 1, binary: false }],
    flows: [{
      line: 19, drawn: true,
      participants: [{ id: 'poll', label: 'Poll', actor: false }, { id: 'store', label: 'Store', actor: false }],
      messages: [{ line: 23, from: 'poll', to: 'store', arrow: '->>', text: 'ReplacePRsOfHosts(prsByHost)', stop_line: 15 }],
    }],
    not_shown: [],
    navigation,
    problems: [],
  };
}

const NAVIGATION: Record<string, ReviewNavigation[]> = {
  ready: [{ language: 'go', state: 'ready', file: 'poll.go' }],
  failed: [{
    language: 'go',
    state: 'failed',
    file: 'poll.go',
    detail: 'gopls exited with status 2 before it answered initialize: go: cannot find main module, but found .git/config in /repo; to create a module there, run: go mod init',
  }],
};

const DAEMON: Partial<DaemonApi> = {
  sendReviewFile: async (_host, _document, path) => ({ event: 'review_file_result', success: true, path, rows: FILE_ROWS, hash: 'h1' }),
};

const SEND: ReviewSend = { ask: async () => null, send: () => {}, destination: 'the agent' };

const SETUP: ReviewSetup = {
  start: async () => null,
  runningSessionLabel: () => undefined,
  openSession: () => {},
  offered: true,
  setOffered: () => {},
};

export function ReviewReaderHarness({ onReady }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const width = Number(params.get('width')) || 716;
  const server = params.get('server') ?? 'ready';
  const review = useMemo(() => bundle(NAVIGATION[server]), [server]);

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
    <DaemonApiProvider api={DAEMON as DaemonApi}>
      <section aria-label="Review tile" className="desktop-dock-tile-body" tabIndex={-1} style={{ width, height: 600, position: 'relative', background: 'var(--color-bg-app)' }}>
        <MarkdownReader content={DOCUMENT} source={SOURCE} annotationsEnabled review={review} reviewSend={SEND} reviewSetup={SETUP} />
      </section>
    </DaemonApiProvider>
  );
}
