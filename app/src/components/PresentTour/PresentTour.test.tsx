import { forwardRef, useImperativeHandle } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PresentTour, type PresentTourFile, type PresentTourProps } from './index';
import type { ReviewComment } from '../../types/generated';

const mermaidMock = vi.hoisted(() => ({
  render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  initialize: vi.fn(),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: mermaidMock.initialize,
    render: mermaidMock.render,
  },
}));

const parseDiffFromFileSpy = vi.hoisted(() => ({ fn: null as ReturnType<typeof vi.fn> | null }));
vi.mock('@pierre/diffs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@pierre/diffs')>();
  const spy = vi.fn(actual.parseDiffFromFile);
  parseDiffFromFileSpy.fn = spy;
  return { ...actual, parseDiffFromFile: spy };
});

const codeViewRenders = vi.hoisted(() => ({ calls: [] as Array<Array<Record<string, unknown>>> }));

const codeViewProps = vi.hoisted(() => ({ latest: null as Record<string, unknown> | null }));

vi.mock('@pierre/diffs/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@pierre/diffs/react')>();
  const MockCodeView = forwardRef((props: Record<string, unknown>, ref) => {
    codeViewProps.latest = props;
    useImperativeHandle(ref, () => ({
      // Derived from the mock's own rendered DOM rather than hardcoded: in jsdom `getBoundingClientRect()`
      // returns zeros, so the first item's top lands at 0, under handleScroll's 80px threshold.
      getInstance: () => ({
        getRenderedItems: () => {
          const container = document.querySelector('[data-testid="mock-codeview"]');
          if (!container) return [];
          return Array.from(container.querySelectorAll<HTMLElement>('[data-item-id]')).map((el) => ({
            id: el.getAttribute('data-item-id') as string,
            element: el,
          }));
        },
      }),
      scrollTo: () => {},
      getItem: () => undefined,
      updateItem: () => false,
      updateItemId: () => false,
      addItems: () => {},
      setSelectedLines: () => {},
      getSelectedLines: () => null,
      clearSelectedLines: () => {},
    }));
    const items = props.items as Array<Record<string, unknown>>;
    codeViewRenders.calls.push(items);
    const renderAnnotation = props.renderAnnotation as (annotation: unknown, item: unknown) => React.ReactNode;
    const renderHeaderPrefix = props.renderHeaderPrefix as ((item: unknown) => React.ReactNode) | undefined;
    const renderHeaderMetadata = props.renderHeaderMetadata as ((item: unknown) => React.ReactNode) | undefined;
    return (
      <div
        ref={props.containerRef as React.Ref<HTMLDivElement>}
        className={props.className as string}
        data-testid="mock-codeview"
      >
        {items.map((item) => (
          <div key={item.id as string} data-item-id={item.id as string}>
            {renderHeaderPrefix?.(item)}
            {renderHeaderMetadata?.(item)}
            {((item.annotations as unknown[]) ?? []).map((annotation: any) => (
              <div key={annotation.metadata.anchorKey}>{renderAnnotation(annotation, item)}</div>
            ))}
          </div>
        ))}
      </div>
    );
  });
  return { ...actual, CodeView: MockCodeView };
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  codeViewRenders.calls = [];
  codeViewProps.latest = null;
});

const noop = () => {};

function baseProps(overrides: Partial<PresentTourProps> = {}): PresentTourProps {
  return {
    files: [],
    comments: [],
    editingCommentId: null,
    readOnlyCommentIds: new Set(),
    onAddComment: noop,
    onEditComment: noop,
    onStartEdit: noop,
    onCancelEdit: noop,
    onResolveComment: noop,
    onDeleteComment: noop,
    reviewedPaths: new Set(),
    onToggleReviewed: noop,
    ...overrides,
  };
}

// A 10-line file with only line 10 changed. Verified against the real @pierre/diffs parser: one hunk
// with a visible additions range of [6, 10], so lines 1-5 are real content outside any hunk.
function tinyFile(path: string): PresentTourFile {
  const lines = Array.from({ length: 10 }, (_, i) => `line ${i + 1}`);
  const original = lines.join('\n') + '\n';
  const modified = original.replace('line 10', 'LINE 10');
  return { path, diff: { loading: false, original, modified } };
}

// Same path shape as tinyFile, but not yet settled — what a file looks like before its diff fetch resolves.
function loadingFile(path: string): PresentTourFile {
  return { path, diff: { loading: true } };
}

function annotationComment(overrides: Partial<ReviewComment>): ReviewComment {
  return {
    id: 'annot:x',
    content: 'a note',
    filepath: 'src/foo.ts',
    line_start: 8,
    line_end: 8,
    author: 'agent',
    resolved: false,
    created_at: '',
    review_id: '',
    ...overrides,
  };
}

async function waitForSettled() {
  await waitFor(() => {
    expect(screen.getByTestId('mock-codeview')).toBeInTheDocument();
  });
}

async function awaitAllReady(paths: string[]) {
  await waitFor(() => {
    const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1] ?? [];
    for (const path of paths) {
      const item = latest.find((i) => (i.id as string) === path) as
        | { type?: string; fileDiff?: { hunks: unknown[] } }
        | undefined;
      expect(item).toBeDefined();
      if (item!.type === 'file') continue;
      expect((item!.fileDiff?.hunks ?? []).length).toBeGreaterThan(0);
    }
  });
}

describe('PresentTour annotations', () => {
  it('renders an annotation as a read-only thread at its line, author shown as Claude', async () => {
    const comment = annotationComment({ id: 'annot:1', content: 'why this line?' });
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/foo.ts']);

    const thread = screen.getByTestId('diff-comment-thread');
    expect(thread.textContent).toContain('why this line?');
    expect(thread.textContent).toContain('Claude');
    expect(thread.querySelector('.edit-btn')).toBeNull();
    expect(thread.querySelector('.delete-btn')).toBeNull();
    expect(thread.querySelector('.resolve-btn')).toBeNull();
  });

  it('renders annotation comments before reviewer comments at a shared anchor', async () => {
    const annotation = annotationComment({ id: 'annot:1', content: 'author note', line_start: 8, line_end: 8 });
    const reply: ReviewComment = {
      id: 'reply-1',
      content: 'reviewer reply',
      filepath: 'src/foo.ts',
      line_start: 8,
      line_end: 8,
      author: 'user',
      resolved: false,
      created_at: '',
      review_id: '',
    };
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [annotation, reply],
          readOnlyCommentIds: new Set([annotation.id]),
          annotationCommentIds: new Set([annotation.id]),
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/foo.ts']);

    const thread = screen.getByTestId('diff-comment-thread');
    const bodies = Array.from(thread.querySelectorAll('.diff-comment-content')).map((el) => el.textContent);
    expect(bodies).toEqual([expect.stringContaining('author note'), expect.stringContaining('reviewer reply')]);
  });

  it('shows a Reply button on a read-only annotation thread; clicking opens a draft that submits on the same anchor', async () => {
    const comment = annotationComment({ id: 'annot:1', content: 'why this line?', line_start: 8, line_end: 8 });
    const onAddComment = vi.fn();
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
          onAddComment,
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/foo.ts']);

    const replyBtn = screen.getByRole('button', { name: 'Reply' });
    act(() => {
      replyBtn.click();
    });

    const form = await screen.findByTestId('diff-comment-form');
    const textarea = form.querySelector('textarea')!;
    fireEvent.change(textarea, { target: { value: 'my reply' } });
    const saveBtn = screen.getByRole('button', { name: 'Save' });
    act(() => {
      saveBtn.click();
    });

    expect(onAddComment).toHaveBeenCalledWith('src/foo.ts', 8, 8, 'my reply');
  });

  it('re-anchors an out-of-hunk annotation to the nearest visible line, with a caption', async () => {
    const comment = annotationComment({ id: 'annot:1', content: 'off in the weeds', line_start: 1, line_end: 1 });
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/foo.ts']);

    const thread = screen.getByTestId('diff-comment-thread');
    expect(thread.textContent).toContain('off in the weeds');
    expect(thread.textContent).toContain('refers to line 1, outside the visible diff');
  });

  it('still drops a non-annotation comment anchored outside the visible diff', async () => {
    const comment: ReviewComment = {
      id: 'reply-1',
      content: 'stray reply',
      filepath: 'src/foo.ts',
      line_start: 1,
      line_end: 1,
      author: 'user',
      resolved: false,
      created_at: '',
      review_id: '',
    };
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set(),
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/foo.ts']);

    expect(screen.queryByTestId('diff-comment-thread')).toBeNull();
  });

  it('reports annotation anchors in document order (files, then line) and dedupes shared anchors', async () => {
    const onAnnotationAnchorsChange = vi.fn();
    const fileB = tinyFile('src/b.ts');
    const commentsA = [
      annotationComment({ id: 'a1', filepath: 'src/foo.ts', line_start: 8, line_end: 8, content: 'a1' }),
    ];
    const commentsB = [
      annotationComment({ id: 'b1', filepath: 'src/b.ts', line_start: 8, line_end: 8, content: 'b1' }),
      annotationComment({ id: 'b2', filepath: 'src/b.ts', line_start: 8, line_end: 8, content: 'b2' }),
    ];
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts'), fileB],
          comments: [...commentsA, ...commentsB],
          readOnlyCommentIds: new Set(['a1', 'b1', 'b2']),
          annotationCommentIds: new Set(['a1', 'b1', 'b2']),
          onAnnotationAnchorsChange,
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/foo.ts', 'src/b.ts']);

    await waitFor(() => {
      expect(onAnnotationAnchorsChange).toHaveBeenCalled();
    });
    const lastCall = onAnnotationAnchorsChange.mock.calls[onAnnotationAnchorsChange.mock.calls.length - 1][0];
    expect(lastCall).toEqual([
      { path: 'src/foo.ts', anchorKey: 'src/foo.ts:additions:8' },
      { path: 'src/b.ts', anchorKey: 'src/b.ts:additions:8' },
    ]);
  });

  it('renders a file note as the first annotation on the first visible line, suppressing the header fallback', async () => {
    const file = tinyFile('src/foo.ts');
    file.note = 'a note about this file';
    const comment = annotationComment({ id: 'annot:1', content: 'a comment', line_start: 8, line_end: 8 });
    render(
      <PresentTour
        {...baseProps({
          files: [file],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/foo.ts']);

    const latestItems = codeViewRenders.calls[codeViewRenders.calls.length - 1];
    const item = latestItems[0] as { annotations: Array<{ metadata: Record<string, unknown> }> };
    expect(item.annotations[0].metadata).toMatchObject({ kind: 'note', side: 'additions', lineNumber: 6 });

    expect(screen.getAllByText('a note about this file')).toHaveLength(1);
  });

  it('excludes the file note from the N/P annotation-anchors payload', async () => {
    const onAnnotationAnchorsChange = vi.fn();
    const file = tinyFile('src/foo.ts');
    file.note = 'file note text';
    const comment = annotationComment({ id: 'annot:1', content: 'real annotation', line_start: 8, line_end: 8 });
    render(
      <PresentTour
        {...baseProps({
          files: [file],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
          onAnnotationAnchorsChange,
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/foo.ts']);

    await waitFor(() => {
      expect(onAnnotationAnchorsChange).toHaveBeenCalled();
    });
    const lastCall = onAnnotationAnchorsChange.mock.calls[onAnnotationAnchorsChange.mock.calls.length - 1][0];
    expect(lastCall).toEqual([{ path: 'src/foo.ts', anchorKey: 'src/foo.ts:additions:8' }]);
  });

  it('falls back to the header for a file note when its diff has no visible line to anchor to (errored diff)', async () => {
    const file: PresentTourFile = { path: 'src/broken.ts', note: 'note on a broken file', diff: { loading: false, error: 'boom' } };
    render(<PresentTour {...baseProps({ files: [file] })} />);
    await waitForSettled();

    expect(screen.getByText('note on a broken file')).toBeInTheDocument();
    const latestItems = codeViewRenders.calls[codeViewRenders.calls.length - 1];
    expect((latestItems[0] as { annotations?: unknown[] }).annotations).toBeUndefined();
  });
});

describe('PresentTour summary fold', () => {
  it('renders expanded (no collapsed class, body aria-hidden=false) when summaryVisible is omitted or true', async () => {
    render(<PresentTour {...baseProps({ files: [tinyFile('src/foo.ts')], summary: 'The summary text' })} />);
    await waitForSettled();

    const summaryEl = screen.getByTestId('present-tour-summary');
    expect(summaryEl).not.toHaveClass('collapsed');
    const bodyEl = screen.getByTestId('present-tour-summary-body');
    expect(bodyEl).toHaveAttribute('aria-hidden', 'false');
    expect(bodyEl.textContent).toContain('The summary text');
    expect(screen.getByTestId('present-tour-summary-toggle')).toBeEnabled();
  });

  it('applies the collapsed class and body aria-hidden=true when summaryVisible is false, without unmounting the card', async () => {
    render(
      <PresentTour
        {...baseProps({ files: [tinyFile('src/foo.ts')], summary: 'The summary text', summaryVisible: false })}
      />
    );
    await waitForSettled();

    const summaryEl = screen.getByTestId('present-tour-summary');
    expect(summaryEl).toHaveClass('collapsed');
    const bodyEl = screen.getByTestId('present-tour-summary-body');
    expect(bodyEl).toHaveAttribute('aria-hidden', 'true');
    expect(bodyEl.textContent).toContain('The summary text');
    expect(screen.getByTestId('present-tour-summary-toggle')).toBeEnabled();
  });

  it('clicking the toggle calls onSummaryVisibleChange with the opposite of summaryVisible', async () => {
    const onSummaryVisibleChange = vi.fn();
    const { rerender } = render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          summary: 'The summary text',
          summaryVisible: true,
          onSummaryVisibleChange,
        })}
      />
    );
    await waitForSettled();

    fireEvent.click(screen.getByTestId('present-tour-summary-toggle'));
    expect(onSummaryVisibleChange).toHaveBeenCalledWith(false);

    onSummaryVisibleChange.mockClear();
    rerender(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          summary: 'The summary text',
          summaryVisible: false,
          onSummaryVisibleChange,
        })}
      />
    );
    fireEvent.click(screen.getByTestId('present-tour-summary-toggle'));
    expect(onSummaryVisibleChange).toHaveBeenCalledWith(true);
  });

  it('wheel-down over an at-bottom (no further scroll) card body collapses; wheel-up does not', async () => {
    const onSummaryVisibleChange = vi.fn();
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          summary: 'The summary text',
          summaryVisible: true,
          onSummaryVisibleChange,
        })}
      />
    );
    await waitForSettled();

    const summaryEl = screen.getByTestId('present-tour-summary');
    // jsdom gives every element zero geometry, so scrollTop + clientHeight >= scrollHeight holds by
    // default — this exercises the "no more content to scroll" case without faking geometry.
    fireEvent.wheel(summaryEl, { deltaY: -50 });
    expect(onSummaryVisibleChange).not.toHaveBeenCalled();

    fireEvent.wheel(summaryEl, { deltaY: 50 });
    expect(onSummaryVisibleChange).toHaveBeenCalledWith(false);
  });

  it('wheel-down does not collapse while the card body still has content to scroll', async () => {
    const onSummaryVisibleChange = vi.fn();
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          summary: 'The summary text',
          summaryVisible: true,
          onSummaryVisibleChange,
        })}
      />
    );
    await waitForSettled();

    const body = screen.getByTestId('present-tour-summary-body');
    Object.defineProperty(body, 'scrollHeight', { value: 800, configurable: true });
    Object.defineProperty(body, 'clientHeight', { value: 200, configurable: true });
    Object.defineProperty(body, 'scrollTop', { value: 0, configurable: true });

    fireEvent.wheel(screen.getByTestId('present-tour-summary'), { deltaY: 50 });
    expect(onSummaryVisibleChange).not.toHaveBeenCalled();
  });
});

describe('PresentTour listener re-attach on deferred load (regression)', () => {
  it('arms passive scroll tracking once CodeView mounts after starting with zero manifest files', async () => {
    const onActivePathChange = vi.fn();
    const { rerender } = render(<PresentTour {...baseProps({ files: [], onActivePathChange })} />);

    expect(screen.getByText('Loading tour…')).toBeInTheDocument();

    rerender(<PresentTour {...baseProps({ files: [tinyFile('src/foo.ts')], onActivePathChange })} />);
    await waitForSettled();
    await awaitAllReady(['src/foo.ts']);

    fireEvent.wheel(screen.getByTestId('mock-codeview'));
    act(() => {
      (codeViewProps.latest!.onScroll as (scrollTop: number) => void)(0);
    });

    expect(onActivePathChange).toHaveBeenCalledWith('src/foo.ts');
  });
});

describe('PresentTour summary fold vs scroll', () => {
  function latestOnScroll(): (scrollTop: number) => void {
    return codeViewProps.latest!.onScroll as (scrollTop: number) => void;
  }

  it('does not report scroll position before any user gesture (mount/cold-window-pin noise)', async () => {
    const onActivePathChange = vi.fn();
    render(
      <PresentTour
        {...baseProps({ files: [tinyFile('src/foo.ts'), tinyFile('src/bar.ts')], onActivePathChange })}
      />
    );
    await waitForSettled();

    act(() => {
      latestOnScroll()(0);
    });
    act(() => {
      latestOnScroll()(50);
    });

    expect(onActivePathChange).not.toHaveBeenCalled();
  });

  it('reports the first file (never null) at the top of the scroller once the user has taken over', async () => {
    const onActivePathChange = vi.fn();
    render(
      <PresentTour
        {...baseProps({ files: [tinyFile('src/foo.ts'), tinyFile('src/bar.ts')], onActivePathChange })}
      />
    );
    await waitForSettled();

    fireEvent.wheel(screen.getByTestId('mock-codeview'));
    act(() => {
      latestOnScroll()(0);
    });

    expect(onActivePathChange).toHaveBeenCalledWith('src/foo.ts');
    expect(onActivePathChange).not.toHaveBeenCalledWith(null);
  });

  it('suppresses passive reporting while a programmatic scroll settles; user takeover restores it immediately', async () => {
    const onActivePathChange = vi.fn();
    const files = [tinyFile('src/foo.ts'), tinyFile('src/bar.ts')];
    const { rerender } = render(
      <PresentTour {...baseProps({ files, onActivePathChange, scrollToPath: null, scrollNonce: 0 })} />
    );
    await waitForSettled();

    const container = screen.getByTestId('mock-codeview');
    fireEvent.wheel(container);

    rerender(
      <PresentTour {...baseProps({ files, onActivePathChange, scrollToPath: 'src/bar.ts', scrollNonce: 1 })} />
    );

    act(() => {
      latestOnScroll()(120);
    });
    expect(onActivePathChange).not.toHaveBeenCalled();

    fireEvent.wheel(container);
    act(() => {
      latestOnScroll()(120);
    });
    expect(onActivePathChange).toHaveBeenCalled();
  });

  it('clears suppression after the quiet window elapses with no further scroll events', async () => {
    const onActivePathChange = vi.fn();
    const files = [tinyFile('src/foo.ts'), tinyFile('src/bar.ts')];
    const { rerender } = render(
      <PresentTour {...baseProps({ files, onActivePathChange, scrollToPath: null, scrollNonce: 0 })} />
    );
    await waitForSettled();

    fireEvent.wheel(screen.getByTestId('mock-codeview'));
    rerender(<PresentTour {...baseProps({ files, onActivePathChange, scrollToPath: 'src/bar.ts', scrollNonce: 1 })} />);

    act(() => {
      latestOnScroll()(120);
    });
    expect(onActivePathChange).not.toHaveBeenCalled();

    // Real ~250ms wait past the 200ms quiet window — fake timers fight this suite's use of `waitFor`,
    // so this is a genuine wall-clock wait.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 250));
    });

    act(() => {
      latestOnScroll()(80);
    });
    expect(onActivePathChange).toHaveBeenCalled();
  });
});

describe('PresentTour per-file item caching', () => {
  function openDraftOn(path: string, line = 6) {
    const onGutterUtilityClick = codeViewProps.latest!.options as { onGutterUtilityClick: (range: unknown, ctx: { item: { id: string } } ) => void };
    act(() => {
      onGutterUtilityClick.onGutterUtilityClick(
        { side: 'additions', start: line, end: line },
        { item: { id: path } }
      );
    });
  }

  function latestItemsByPath(): Map<string, Record<string, unknown>> {
    const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1];
    return new Map(latest.map((item) => [item.id as string, item]));
  }

  it('typing in a draft does not rebuild items or re-parse', async () => {
    const files = [tinyFile('src/a.ts'), tinyFile('src/b.ts'), tinyFile('src/c.ts')];
    render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();
    await awaitAllReady(['src/a.ts', 'src/b.ts', 'src/c.ts']);

    openDraftOn('src/a.ts');
    const form = await screen.findByTestId('diff-comment-form');
    const textarea = form.querySelector('textarea')!;

    const parseCountBefore = parseDiffFromFileSpy.fn!.mock.calls.length;
    const itemsBefore = latestItemsByPath();

    fireEvent.change(textarea, { target: { value: 'h' } });
    fireEvent.change(textarea, { target: { value: 'he' } });
    fireEvent.change(textarea, { target: { value: 'hel' } });
    fireEvent.change(textarea, { target: { value: 'hello' } });

    expect(parseDiffFromFileSpy.fn!.mock.calls.length).toBe(parseCountBefore);
    const itemsAfter = latestItemsByPath();
    for (const [path, before] of itemsBefore) {
      expect(itemsAfter.get(path)).toBe(before);
    }
    expect(textarea).toHaveValue('hello');
  });

  it('reviewed toggle bumps only that file', async () => {
    const files = [tinyFile('src/a.ts'), tinyFile('src/b.ts'), tinyFile('src/c.ts')];
    const { rerender } = render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();
    await awaitAllReady(['src/a.ts', 'src/b.ts', 'src/c.ts']);

    const parseCountBefore = parseDiffFromFileSpy.fn!.mock.calls.length;
    const itemsBefore = latestItemsByPath();

    rerender(<PresentTour {...baseProps({ files, reviewedPaths: new Set(['src/b.ts']) })} />);

    const itemsAfter = latestItemsByPath();
    expect(itemsAfter.get('src/b.ts')).not.toBe(itemsBefore.get('src/b.ts'));
    expect((itemsAfter.get('src/b.ts') as { version: number }).version).not.toBe(
      (itemsBefore.get('src/b.ts') as { version: number }).version
    );
    expect(itemsAfter.get('src/a.ts')).toBe(itemsBefore.get('src/a.ts'));
    expect(itemsAfter.get('src/c.ts')).toBe(itemsBefore.get('src/c.ts'));
    expect(parseDiffFromFileSpy.fn!.mock.calls.length).toBe(parseCountBefore);
  });

  it('opening a draft bumps only its file', async () => {
    const files = [tinyFile('src/a.ts'), tinyFile('src/b.ts')];
    render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();
    await awaitAllReady(['src/a.ts', 'src/b.ts']);

    const itemsBefore = latestItemsByPath();
    openDraftOn('src/a.ts');
    const itemsAfter = latestItemsByPath();

    expect(itemsAfter.get('src/a.ts')).not.toBe(itemsBefore.get('src/a.ts'));
    expect(itemsAfter.get('src/b.ts')).toBe(itemsBefore.get('src/b.ts'));
  });

  it("editing a comment's content bumps only its file", async () => {
    const commentB = annotationComment({ id: 'b1', filepath: 'src/b.ts', line_start: 8, line_end: 8, content: 'original' });
    const files = [tinyFile('src/a.ts'), tinyFile('src/b.ts')];
    const { rerender } = render(
      <PresentTour
        {...baseProps({
          files,
          comments: [commentB],
          readOnlyCommentIds: new Set([commentB.id]),
          annotationCommentIds: new Set([commentB.id]),
        })}
      />
    );
    await waitForSettled();
    await awaitAllReady(['src/a.ts', 'src/b.ts']);

    const parseCountBefore = parseDiffFromFileSpy.fn!.mock.calls.length;
    const itemsBefore = latestItemsByPath();

    const updatedCommentB = { ...commentB, content: 'changed' };
    rerender(
      <PresentTour
        {...baseProps({
          files,
          comments: [updatedCommentB],
          readOnlyCommentIds: new Set([commentB.id]),
          annotationCommentIds: new Set([commentB.id]),
        })}
      />
    );

    const itemsAfter = latestItemsByPath();
    expect(itemsAfter.get('src/b.ts')).not.toBe(itemsBefore.get('src/b.ts'));
    expect(itemsAfter.get('src/a.ts')).toBe(itemsBefore.get('src/a.ts'));
    expect(parseDiffFromFileSpy.fn!.mock.calls.length).toBe(parseCountBefore);
  });

  it('draft content survives an item rebuild (remount insurance)', async () => {
    const fileA = tinyFile('src/a.ts');
    const fileB = tinyFile('src/b.ts');
    const { rerender } = render(<PresentTour {...baseProps({ files: [fileA, fileB] })} />);
    await waitForSettled();
    await awaitAllReady(['src/a.ts', 'src/b.ts']);

    openDraftOn('src/a.ts');
    const form = await screen.findByTestId('diff-comment-form');
    const textarea = form.querySelector('textarea')!;
    fireEvent.change(textarea, { target: { value: 'typed text' } });
    expect(textarea).toHaveValue('typed text');

    rerender(<PresentTour {...baseProps({ files: [fileB] })} />);
    expect(screen.queryByTestId('diff-comment-form')).toBeNull();

    rerender(<PresentTour {...baseProps({ files: [fileA, fileB] })} />);
    const remountedForm = await screen.findByTestId('diff-comment-form');
    expect(remountedForm.querySelector('textarea')).toHaveValue('typed text');
  });
});

describe('PresentTour progressive load', () => {
  function latestItemsByPath(): Map<string, Record<string, unknown>> {
    const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1] ?? [];
    return new Map(latest.map((item) => [item.id as string, item]));
  }

  it('mounts CodeView with a zero-hunk placeholder per file before any diff settles', async () => {
    const files = [loadingFile('src/a.ts'), loadingFile('src/b.ts'), loadingFile('src/c.ts')];
    render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();

    const items = latestItemsByPath();
    expect(items.size).toBe(3);
    for (const path of ['src/a.ts', 'src/b.ts', 'src/c.ts']) {
      const item = items.get(path) as { type: string; fileDiff: { hunks: unknown[] } };
      expect(item.type).toBe('diff');
      expect(item.fileDiff.hunks).toHaveLength(0);
    }
  });

  it('swaps one file to its real item as it settles, leaving the others as untouched placeholders', async () => {
    const { rerender } = render(
      <PresentTour {...baseProps({ files: [loadingFile('src/a.ts'), loadingFile('src/b.ts'), loadingFile('src/c.ts')] })} />
    );
    await waitForSettled();
    const before = latestItemsByPath();
    const bBefore = before.get('src/b.ts');
    const cBefore = before.get('src/c.ts');

    rerender(
      <PresentTour {...baseProps({ files: [tinyFile('src/a.ts'), loadingFile('src/b.ts'), loadingFile('src/c.ts')] })} />
    );
    await awaitAllReady(['src/a.ts']);

    const after = latestItemsByPath();
    const aAfter = after.get('src/a.ts') as { type: string; fileDiff: { hunks: unknown[] }; version: number };
    expect(aAfter.fileDiff.hunks.length).toBeGreaterThan(0);
    expect(aAfter.version).not.toBe((before.get('src/a.ts') as { version: number }).version);
    expect(after.get('src/b.ts')).toBe(bBefore);
    expect(after.get('src/c.ts')).toBe(cBefore);
  });

  it('parses each settled file exactly once as sliced admission converges on a large batch', async () => {
    const paths = Array.from({ length: 12 }, (_, i) => `src/file${i}.ts`);
    const { rerender } = render(<PresentTour {...baseProps({ files: paths.map(loadingFile) })} />);
    await waitForSettled();

    rerender(<PresentTour {...baseProps({ files: paths.map(tinyFile) })} />);
    await awaitAllReady(paths);

    const realParseCalls = parseDiffFromFileSpy.fn!.mock.calls.filter(
      (call) => (call[0] as { contents: string }).contents.length > 0
    );
    expect(realParseCalls).toHaveLength(paths.length);
    for (const path of paths) {
      const callsForPath = realParseCalls.filter((call) => (call[0] as { name: string }).name === path);
      expect(callsForPath).toHaveLength(1);
    }
  });

  it('leaves a loading file’s placeholder identity and version untouched by an unrelated prop change', async () => {
    const files = [tinyFile('src/a.ts'), loadingFile('src/b.ts')];
    const { rerender } = render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();
    await awaitAllReady(['src/a.ts']);

    const before = latestItemsByPath().get('src/b.ts');
    rerender(<PresentTour {...baseProps({ files, reviewedPaths: new Set(['src/a.ts']) })} />);
    const after = latestItemsByPath().get('src/b.ts');

    expect(after).toBe(before);
  });

  it('renders an error card immediately, with no admission wait, alongside files still loading', async () => {
    const files = [
      loadingFile('src/a.ts'),
      { path: 'src/broken.ts', diff: { loading: false, error: 'boom' } },
      loadingFile('src/c.ts'),
    ];
    render(<PresentTour {...baseProps({ files })} />);
    await waitForSettled();

    const items = latestItemsByPath();
    const errored = items.get('src/broken.ts') as { type: string; file: { contents: string } };
    expect(errored.type).toBe('file');
    expect(errored.file.contents).toBe('boom');
    expect((items.get('src/a.ts') as { fileDiff: { hunks: unknown[] } }).fileDiff.hunks).toHaveLength(0);
  });

  it('re-parsing on a round reload is skipped when the file re-settles with identical content', async () => {
    const file = tinyFile('src/a.ts');
    const { rerender } = render(<PresentTour {...baseProps({ files: [file] })} />);
    await waitForSettled();
    await awaitAllReady(['src/a.ts']);
    const parseCountAfterFirstSettle = parseDiffFromFileSpy.fn!.mock.calls.length;

    rerender(<PresentTour {...baseProps({ files: [loadingFile('src/a.ts')] })} />);
    await waitFor(() => {
      const item = latestItemsByPath().get('src/a.ts') as { fileDiff: { hunks: unknown[] } };
      expect(item.fileDiff.hunks).toHaveLength(0);
    });

    rerender(<PresentTour {...baseProps({ files: [file] })} />);
    await waitFor(() => {
      const item = latestItemsByPath().get('src/a.ts') as { fileDiff: { hunks: unknown[] } };
      expect(item.fileDiff.hunks.length).toBeGreaterThan(0);
    });

    const realParseCallsAfter = parseDiffFromFileSpy.fn!.mock.calls.filter(
      (call) => (call[0] as { contents: string }).contents.length > 0
    );
    expect(realParseCallsAfter).toHaveLength(1);
    expect(parseDiffFromFileSpy.fn!.mock.calls.length).toBe(parseCountAfterFirstSettle + 1);
  });
});

describe('PresentTour diagram layout invalidation', () => {
  const mermaidNote = '```mermaid\ngraph TD;\nA-->B;\n```';

  it('bumps the items version CodeView receives when a file-note diagram finishes rendering', async () => {
    const file = tinyFile('src/foo.ts');
    file.note = mermaidNote;
    render(<PresentTour {...baseProps({ files: [file] })} />);
    await waitForSettled();

    const versionBefore = codeViewRenders.calls[0][0].version;

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    await waitFor(() => {
      const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1];
      expect(latest[0].version).not.toBe(versionBefore);
    });
  });

  it('bumps the items version CodeView receives when an annotation-body diagram finishes rendering', async () => {
    const comment = annotationComment({ id: 'annot:1', content: mermaidNote, line_start: 8, line_end: 8 });
    render(
      <PresentTour
        {...baseProps({
          files: [tinyFile('src/foo.ts')],
          comments: [comment],
          readOnlyCommentIds: new Set([comment.id]),
          annotationCommentIds: new Set([comment.id]),
        })}
      />
    );
    await waitForSettled();

    const versionBefore = codeViewRenders.calls[0][0].version;

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    await waitFor(() => {
      const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1];
      expect(latest[0].version).not.toBe(versionBefore);
    });
  });

  it('does not remount the diagram when a version bump re-renders CodeView (no infinite loop)', async () => {
    const file = tinyFile('src/foo.ts');
    file.note = mermaidNote;
    render(<PresentTour {...baseProps({ files: [file] })} />);
    await waitForSettled();

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    await waitFor(() => {
      const versionBefore = codeViewRenders.calls[0][0].version;
      const latest = codeViewRenders.calls[codeViewRenders.calls.length - 1];
      expect(latest[0].version).not.toBe(versionBefore);
    });

    expect(mermaidMock.render).toHaveBeenCalledTimes(1);
  });
});
