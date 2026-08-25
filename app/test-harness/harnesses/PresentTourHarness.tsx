import { useCallback, useEffect, useMemo, useState } from 'react';
import { PresentTour } from '../../src/components/PresentTour';
import type { ReviewComment } from '../../src/types/generated';
import type { HarnessProps } from '../types';

function generateLines(count: number, prefix: string): string {
  return Array.from({ length: count }, (_, i) => `  console.log('${prefix} ${i + 1}');`).join('\n');
}

// Several separate hunks per file so the tour has real scroll range even with
// expandUnchanged=false collapsing unchanged context.
const FILE_A_ORIGINAL = `function alpha() {
  // Section 1
${generateLines(12, 'alpha-section1')}
  console.log('deleted A1');
  console.log('deleted A2');
  // Section 2
${generateLines(12, 'alpha-section2')}
  console.log('deleted A3');
  // Section 3
${generateLines(12, 'alpha-section3')}
}`;
const FILE_A_MODIFIED = `function alpha() {
  // Section 1
${generateLines(12, 'alpha-section1')}
  // Section 2
${generateLines(12, 'alpha-section2')}
  // Section 3
${generateLines(12, 'alpha-section3')}
  console.log('new alpha tail');
}`;

const FILE_B_ORIGINAL = `function beta() {
  // Section 1
${generateLines(12, 'beta-section1')}
  console.log('deleted B1');
  // Section 2
${generateLines(12, 'beta-section2')}
  console.log('deleted B2');
  console.log('deleted B3');
  // Section 3
${generateLines(12, 'beta-section3')}
}`;
const FILE_B_MODIFIED = `function beta() {
  // Section 1
${generateLines(12, 'beta-section1')}
  // Section 2
${generateLines(12, 'beta-section2')}
  // Section 3
${generateLines(12, 'beta-section3')}
  console.log('new beta tail');
}`;

const FILE_C_ORIGINAL = `function gamma() {
  // Section 1
${generateLines(12, 'gamma-section1')}
  console.log('deleted C1');
  // Section 2
${generateLines(12, 'gamma-section2')}
}`;
const FILE_C_MODIFIED = `function gamma() {
  // Section 1
${generateLines(12, 'gamma-section1')}
  // Section 2
${generateLines(12, 'gamma-section2')}
  console.log('new gamma tail');
}`;

const FILES = [
  { path: 'src/alpha.ts', original: FILE_A_ORIGINAL, modified: FILE_A_MODIFIED, note: undefined as string | undefined },
  { path: 'src/beta.ts', original: FILE_B_ORIGINAL, modified: FILE_B_MODIFIED, note: 'Beta needs a second look.' },
  { path: 'src/gamma.ts', original: FILE_C_ORIGINAL, modified: FILE_C_MODIFIED, note: undefined },
];

function makeComment(overrides: Partial<ReviewComment>): ReviewComment {
  return {
    id: `comment-${Math.round(performance.now() * 1000)}-${Math.floor(Math.random() * 1e6)}`,
    review_id: 'harness-review',
    filepath: 'src/alpha.ts',
    // Must anchor to an actual hunk line: `expandUnchanged: false` collapses the unchanged
    // sections, and a comment seeded there silently never renders.
    line_start: 41,
    line_end: 41,
    content: 'Seeded comment',
    author: 'user',
    resolved: false,
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

export function PresentTourHarness({ onReady }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const seed = params.get('seed') !== '0';
  const deferred = params.get('deferred') === '1';

  const [comments, setComments] = useState<ReviewComment[]>(
    seed ? [makeComment({ id: 'seeded-1', content: 'Seeded comment on alpha' })] : []
  );
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);
  const [scrollToPath, setScrollToPath] = useState<string | null>(null);
  const [scrollNonce, setScrollNonce] = useState(0);
  const [activePath, setActivePath] = useState<string | null>(null);
  const [reviewedPaths, setReviewedPaths] = useState<Set<string>>(new Set());
  const [summaryCollapsed, setSummaryCollapsed] = useState(false);
  useEffect(() => {
    if (activePath !== null) setSummaryCollapsed(true);
  }, [activePath]);
  // `deferred=1` starts files loading so tests can issue scroll requests before CodeView
  // mounts; `settleDiffs()` then supplies content and lets the tour finish loading.
  const [diffsSettled, setDiffsSettled] = useState(!deferred);
  const failNextAddRef = useMemo(() => ({ current: false }), []);

  const onAddComment = useCallback((filepath: string, lineStart: number, lineEnd: number, content: string) => {
    window.__HARNESS__.recordCall('addComment', [filepath, lineStart, lineEnd, content]);
    if (failNextAddRef.current) {
      failNextAddRef.current = false;
      throw new Error('Harness add comment failure');
    }
    setComments((prev) => [
      ...prev,
      makeComment({
        id: `comment-${prev.length + 1}`,
        filepath,
        line_start: lineStart,
        line_end: lineEnd,
        content,
      }),
    ]);
  }, [failNextAddRef]);

  const onEditComment = useCallback((id: string, content: string) => {
    window.__HARNESS__.recordCall('editComment', [id, content]);
    setComments((prev) => prev.map((c) => (c.id === id ? { ...c, content } : c)));
    setEditingCommentId(null);
  }, []);

  const onStartEdit = useCallback((id: string) => {
    window.__HARNESS__.recordCall('startEdit', [id]);
    setEditingCommentId(id);
  }, []);

  const onCancelEdit = useCallback(() => {
    window.__HARNESS__.recordCall('cancelEdit', []);
    setEditingCommentId(null);
  }, []);

  const onResolveComment = useCallback((id: string, resolved: boolean) => {
    window.__HARNESS__.recordCall('resolveComment', [id, resolved]);
    setComments((prev) => prev.map((c) => (c.id === id ? { ...c, resolved, resolved_by: resolved ? 'user' : '' } : c)));
  }, []);

  const onDeleteComment = useCallback((id: string) => {
    window.__HARNESS__.recordCall('deleteComment', [id]);
    setComments((prev) => prev.filter((c) => c.id !== id));
  }, []);

  const onSendToClaude = useCallback((reference: string) => {
    window.__HARNESS__.recordCall('sendToClaude', [reference]);
  }, []);

  const onActivePathChange = useCallback((path: string) => {
    window.__HARNESS__.recordCall('activePathChange', [path]);
    setActivePath(path);
  }, []);

  const onToggleReviewed = useCallback((path: string) => {
    window.__HARNESS__.recordCall('toggleReviewed', [path]);
    setReviewedPaths((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => onReady(), 400);
    return () => clearTimeout(timer);
  }, [onReady]);

  useEffect(() => {
    const api = window.__HARNESS__ as unknown as Record<string, unknown>;
    api.scrollToFile = (path: string) => {
      setScrollToPath(path);
      setScrollNonce((n) => n + 1);
    };
    api.failNextAddComment = () => {
      failNextAddRef.current = true;
    };
    api.getActivePath = () => activePath;
    api.settleDiffs = () => setDiffsSettled(true);
    api.getReviewedPaths = () => Array.from(reviewedPaths);
  }, [activePath, failNextAddRef, reviewedPaths]);

  const files = FILES.map((f) => ({
    path: f.path,
    note: f.note,
    diff: diffsSettled ? { loading: false, original: f.original, modified: f.modified } : { loading: true },
  }));

  const readOnlyCommentIds = useMemo(() => new Set<string>(), []);

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
        <PresentTour
          summary={`## Harness summary

Three files, reading order alpha -> beta -> gamma.`}
          summaryVisible={!summaryCollapsed}
          onSummaryVisibleChange={(visible) => setSummaryCollapsed(!visible)}
          files={files}
          comments={comments}
          editingCommentId={editingCommentId}
          readOnlyCommentIds={readOnlyCommentIds}
          onAddComment={onAddComment}
          onEditComment={onEditComment}
          onStartEdit={onStartEdit}
          onCancelEdit={onCancelEdit}
          onResolveComment={onResolveComment}
          onDeleteComment={onDeleteComment}
          onSendToClaude={onSendToClaude}
          scrollToPath={scrollToPath}
          scrollNonce={scrollNonce}
          reviewedPaths={reviewedPaths}
          onToggleReviewed={onToggleReviewed}
          onActivePathChange={onActivePathChange}
        />
      </div>
    </div>
  );
}
