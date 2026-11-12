import { useEffect, useMemo } from 'react';
import '../../src/App.css';
import { MarkdownReader } from '../../src/components/MarkdownReader';
import { fileMarkdownSource } from '../../src/components/MarkdownReader/documentSource';
import { setMarkdownAnnotationsTransport } from '../../src/components/MarkdownReader/annotations/transport';
import type { ReviewBundle, ReviewFlowMessage, ReviewRow, ReviewStop } from '../../src/types/generated';
import type { HarnessProps } from '../types';

const SOURCE = fileMarkdownSource('test-harness', '/repo/review.md');

/** The pull request poll review the code review comparison measured, with
 * captions, a group and second participant lines in its after diagram. */
const DOCUMENT = `---
attn: review
frame: map
repository: /repo
base: 6ffc961
head: b12e2e9
---

# Decide pull request changes inside the poll's replace

A poll worked out which pull requests changed by reading the table before its write and again after it.

\`\`\`mermaid after
sequenceDiagram
  participant poll as PR poll<br/>daemon.go
  participant store as Store<br/>store.go
  participant detail as Detail refresh<br/>another goroutine
  participant bus as Event bus<br/>facts
  Note over poll,bus: After: both reads sit inside the write transaction. A detail refresh runs before or after it, never inside.
  poll->>store: ReplacePRsOfHosts(prsByHost)
  %% stop: internal/daemon/daemon.go:3775-3787
  Note over poll,store: one call does all of it
  critical one transaction, holding s.mu
    store->>store: queryPRRecords, before
    %% stop: internal/store/store.go:1314-1335
    Note right of store: first read
    store->>store: savePR, DELETE
    %% stop: internal/store/store.go:1336-1375
    Note right of store: unchanged
    store->>store: queryPRRecords, after, Commit
    %% stop: internal/store/store.go:1376-1388
    Note right of store: second read, then compare
  end
  store-->>poll: PRChanges
  %% stop: internal/store/store.go:1267-1271
  Note over poll,store: three lists of ids
  poll->>bus: pr.appeared, pr.updated, pr.disappeared
  %% stop: internal/daemon/ws_pr.go:153-165
  Note over poll,bus: only what the replace changed
  detail->>store: UpdatePRDetails(#1)
  %% stop: internal/daemon/daemon.go:3807-3815
  Note over store,detail: waits for s.mu
  detail->>bus: pr.details.changed
  %% stop: internal/daemon/pr_dashboard_wire_test.go:206-246
  Note over detail,bus: the only fact for this change
\`\`\`

## The poll asks the store what changed

\`saveObservedPRs\` used to read every pull request, replace them, and read them all again. Both reads are gone. \`ReplacePRsOfHosts\` returns the changes and the poll passes them on.

### internal/daemon/daemon.go:3775-3787

## The replace reads before and after, inside its transaction

\`ReplacePRsOfHosts\` already held \`s.mu\` and a transaction for the write. The two reads now happen there too, so no other writer fits between them.

### internal/store/store.go:1314-1335

### internal/store/store.go:1336-1375

The second read runs before the commit. The comparison runs on the two reads, after the commit succeeds.

### internal/store/store.go:1376-1388

## The comparison moved, and it compares differently

\`comparePRRecords\` is the body of the daemon's old \`publishPRSetChanges\`, returning ids where the old code published facts.

### internal/store/store.go:1267-1271

## The daemon publishes the list as it is

\`publishPRChanges\` keeps the \`coalesceSnapshots\` wrapper, so a poll that changes many pull requests still sends one snapshot.

### internal/daemon/ws_pr.go:153-165

## The test lands a detail refresh at the worst moment

A new pause point holds the poll right before its save.

### internal/daemon/pr_dashboard_wire_test.go:206-246

### internal/daemon/daemon.go:3807-3815

## Not shown

- internal/store/github_hosts_migration_test.go: a call site that ignores the new first return value.
- internal/pausepoint/pausepoint.go: the name of the new pause point.
`;

const lines = DOCUMENT.split('\n');
const lineOf = (text: string) => lines.findIndex((line) => line.includes(text)) + 1;

function rowsOf(path: string, from: number, to: number): ReviewRow[] {
  const name = path.split('/').pop()!.replace(/\.go$/, '');
  return Array.from({ length: to - from + 1 }, (_, index) => ({ kind: index % 5 === 2 ? '+' : ' ', old: from + index, now: from + index, head: from + index, text: `\t${name}Line${from + index}()` }));
}

function stopOf(heading: string): ReviewStop {
  const [, path, from, to] = /^### (.+):(\d+)-(\d+)$/.exec(heading)!;
  return {
    heading_line: lineOf(heading), path, from: Number(from), to: Number(to), now_from: Number(from), now_to: Number(to),
    changed_since_presented: false, file_hash: path, rows: rowsOf(path, Number(from), Number(to)), notes: [],
  };
}

function message(source: string, stop: string, note: string): ReviewFlowMessage {
  const [, from, arrow, to, text] = /^\s*(\w+)(-->>|->>)(\w+): (.+)$/.exec(source)!;
  return { line: lineOf(source), from, to, arrow, text, stop_line: lineOf(`### ${stop}`), note };
}

const STOPS = lines.filter((line) => line.startsWith('### ')).map(stopOf);

const BUNDLE: ReviewBundle = {
  frame: 'map', repository: '/repo', base: '6ffc961', head: 'b12e2e9',
  stops: STOPS,
  files: [...new Set(STOPS.map((stop) => stop.path))].map((path) => ({ path, status: 'modified', added: 4, removed: 2, binary: false })),
  flows: [{
    line: lineOf('```mermaid after'), side: 'after', drawn: true,
    participants: [
      { id: 'poll', label: 'PR poll<br/>daemon.go', actor: false },
      { id: 'store', label: 'Store<br/>store.go', actor: false },
      { id: 'detail', label: 'Detail refresh<br/>another goroutine', actor: false },
      { id: 'bus', label: 'Event bus<br/>facts', actor: false },
    ],
    note: 'After: both reads sit inside the write transaction. A detail refresh runs before or after it, never inside.',
    messages: [
      message('  poll->>store: ReplacePRsOfHosts(prsByHost)', 'internal/daemon/daemon.go:3775-3787', 'one call does all of it'),
      message('    store->>store: queryPRRecords, before', 'internal/store/store.go:1314-1335', 'first read'),
      message('    store->>store: savePR, DELETE', 'internal/store/store.go:1336-1375', 'unchanged'),
      message('    store->>store: queryPRRecords, after, Commit', 'internal/store/store.go:1376-1388', 'second read, then compare'),
      message('  store-->>poll: PRChanges', 'internal/store/store.go:1267-1271', 'three lists of ids'),
      message('  poll->>bus: pr.appeared, pr.updated, pr.disappeared', 'internal/daemon/ws_pr.go:153-165', 'only what the replace changed'),
      message('  detail->>store: UpdatePRDetails(#1)', 'internal/daemon/daemon.go:3807-3815', 'waits for s.mu'),
      message('  detail->>bus: pr.details.changed', 'internal/daemon/pr_dashboard_wire_test.go:206-246', 'the only fact for this change'),
    ],
    groups: [{
      line: lineOf('critical one transaction'), kind: 'critical', label: 'one transaction, holding s.mu',
      first_message: lineOf('queryPRRecords, before'), last_message: lineOf('queryPRRecords, after, Commit'),
    }],
  }],
  not_shown: [
    { line: lineOf('- internal/store/github_hosts'), path: 'internal/store/github_hosts_migration_test.go', reason: 'a call site that ignores the new first return value.' },
    { line: lineOf('- internal/pausepoint'), path: 'internal/pausepoint/pausepoint.go', reason: 'the name of the new pause point.' },
  ],
  navigation: [],
  problems: [],
};

export function ReviewMapHarness({ onReady }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const width = Number(params.get('width')) || 716;
  const height = Number(params.get('height')) || 900;
  const review = useMemo(() => BUNDLE, []);
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
    <section aria-label="Review tile" tabIndex={-1} className="desktop-dock-tile-body" style={{ width, height, position: 'relative' }}>
      <MarkdownReader content={DOCUMENT} source={SOURCE} review={review} annotationsEnabled />
    </section>
  );
}
