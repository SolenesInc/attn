import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PresentRoot } from './index';
import type { Presentation, PresentationComment, PresentationRound } from '../../types/generated';
import type { ReplyHandler } from '../../test/scriptedDaemon';
import { installScriptedDaemon, type Reply, type ScriptedDaemon } from '../../test/scriptedDaemon';
import { diffRendererScrolls } from '../../test/codeViewStub';

vi.mock('@pierre/diffs/react', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@pierre/diffs/react')>()),
  CodeView: (await import('../../test/codeViewStub')).CodeViewStub,
}));

const mockHide = vi.fn();
vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => ({ hide: mockHide }),
}));

function setSearch(search: string) {
  window.history.replaceState({}, '', `/?${search}`);
}

type DiffReply = ReplyHandler<'get_file_diff'>;

interface LoadOptions {
  round?: PresentationRound;
  repoHeadSha?: string;
  comments?: PresentationComment[];
  diff?: DiffReply;
}

async function openPresentation(search: string, script: (daemon: ScriptedDaemon) => void = () => {}) {
  setSearch(search);
  const daemon = installScriptedDaemon();
  script(daemon);
  render(<PresentRoot />);
  await daemon.idle();
  return daemon;
}

function roundResult(options: LoadOptions = {}): Reply {
  return {
    event: 'get_presentation_round_result',
    success: true,
    presentation,
    round: options.round ?? round,
    comments: options.comments ?? [],
    ...(options.repoHeadSha !== undefined && { repo_head_sha: options.repoHeadSha }),
  };
}

async function loadRound(options: LoadOptions = {}): Promise<ScriptedDaemon> {
  const daemon = await openPresentation('window=present&presentation=pres-1', (scripted) => {
    scripted.on('get_presentation_round', () => roundResult(options));
    if (options.diff) scripted.on('get_file_diff', options.diff);
  });
  expect(screen.getByText('My presentation')).toBeInTheDocument();
  return daemon;
}

const DIFFS: Record<string, [string, string]> = {
  'src/foo.ts': ['old content\nold 2\nold 3\nold 4\nold 5\n', 'new content\nnew 2\nnew 3\nnew 4\nnew 5\n'],
  'src/foo.test.ts': ['test old\n', 'test new\n'],
};

const serveDiffs: DiffReply = (command) => ({
  event: 'file_diff_result',
  success: true,
  directory: command.directory,
  path: command.path,
  original: DIFFS[command.path]?.[0] ?? '',
  modified: DIFFS[command.path]?.[1] ?? '',
});

function fileDiffRequests(daemon: ScriptedDaemon, path?: string) {
  return daemon.sentOf('get_file_diff').filter((command) => path === undefined || command.path === path);
}

function latestFileDiffRequestId(daemon: ScriptedDaemon, path: string): string {
  const requests = fileDiffRequests(daemon, path);
  const requestId = requests[requests.length - 1]?.request_id;
  if (!requestId) throw new Error(`no get_file_diff request_id found for ${path}`);
  return requestId;
}

function roundFetches(daemon: ScriptedDaemon) {
  return daemon.sentOf('get_presentation_round');
}

function comment(
  fields: Partial<PresentationComment> & Pick<PresentationComment, 'id' | 'content' | 'filepath' | 'line_start' | 'line_end'>,
): PresentationComment {
  return { side: 'new', author: 'user', created_at: '2026-07-01T00:00:00Z', round_id: 'round-0', ...fields };
}

const tourFile = (path: string) => screen.getByRole('region', { name: path });
const railRow = (path: string) => screen.getByText(path, { selector: 'code.present-root-file-path' }).closest('li')!;
const summaryToggle = () => screen.getByTestId('present-tour-summary-toggle');

async function settle(daemon: ScriptedDaemon) {
  await daemon.idle();
  await act(() => vi.advanceTimersByTimeAsync(50));
  await daemon.idle();
}

async function writeComment(daemon: ScriptedDaemon, path: string, line: string, text: string) {
  fireEvent.click(screen.getByRole('button', { name: `Comment on ${path} ${line}` }));
  const form = screen.getByTestId('diff-comment-form');
  fireEvent.change(within(form).getByPlaceholderText('Add a comment...'), { target: { value: text } });
  fireEvent.click(within(form).getByRole('button', { name: 'Save' }));
  await settle(daemon);
}

function threadWith(text: string): HTMLElement {
  const thread = screen.getByText(text).closest<HTMLElement>('[data-testid="diff-comment-thread"]');
  if (!thread) throw new Error(`no comment thread shows ${text}`);
  return thread;
}

const round: PresentationRound = {
  id: 'round-1',
  presentation_id: 'pres-1',
  seq: 1,
  base_sha: 'a1b2c3d4e5f6',
  head_sha: '00112233445566',
  created_at: '2026-07-01T00:00:00Z',
  manifest: {
    title: 'My change',
    summary: 'Adds the thing.',
    files: [
      { path: 'src/foo.ts', note: 'Core logic' },
      { path: 'src/foo.test.ts' },
    ],
    skip: [] as string[],
  },
};

const roundWithSkip = {
  ...round,
  manifest: {
    ...round.manifest,
    skip: ['src/generated.ts', 'src/vendor.ts'],
  },
};

const roundWithStats = {
  ...round,
  manifest: {
    ...round.manifest,
    files: [
      { path: 'src/foo.ts', note: 'Core logic', additions: 12, deletions: 3 },
      { path: 'src/foo.test.ts' },
    ],
  },
};

const roundWithAnnotations = {
  ...round,
  manifest: {
    ...round.manifest,
    files: [
      {
        path: 'src/foo.ts',
        note: 'Core logic',
        annotations: [
          { line_start: 2, line_end: 2, comments: ['why this line?'] },
          { line_start: 4, line_end: 5, comments: ['first note', 'second note'] },
        ],
      },
      { path: 'src/foo.test.ts' },
    ],
  },
};

const roundWithChangedFiles = {
  ...roundWithSkip,
  changed_files: [
    { path: 'src/foo.ts', additions: 12, deletions: 3 },
    { path: 'src/foo.test.ts', additions: 1, deletions: 0 },
    { path: 'src/extra.ts', additions: 5, deletions: 1 },
    { path: 'src/generated.ts', additions: 0, deletions: 40 },
    { path: 'src/vendor.ts' },
  ],
};

const presentation: Presentation = {
  id: 'pres-1',
  created_at: '2026-07-01T00:00:00Z',
  kind: 'pr',
  latest_round_seq: 1,
  latest_round_submitted: false,
  repo_path: '/repo/path',
  session_id: 'session-1',
  status: 'open',
  title: 'My presentation',
};

const { summary: _summary, ...manifestWithoutSummary } = round.manifest;
const roundWithoutSummary: PresentationRound = { ...round, manifest: manifestWithoutSummary };

describe('PresentRoot', () => {
  beforeEach(() => {
    const loadingScreen = document.createElement('div');
    loadingScreen.id = 'loading-screen';
    document.body.appendChild(loadingScreen);
    window.localStorage.clear();
    diffRendererScrolls.length = 0;
    mockHide.mockClear();
  });

  afterEach(() => {
    document.getElementById('loading-screen')?.remove();
  });

  it('hides the boot splash on mount, even before any data has loaded', async () => {
    await openPresentation('window=present&presentation=pres-1');

    expect(document.getElementById('loading-screen')).toHaveClass('hidden');
  });

  it('renders round info from a get_presentation_round result', async () => {
    const daemon = await loadRound({ diff: serveDiffs });
    await settle(daemon);

    expect(await daemon.received('get_presentation_round')).toMatchObject({ presentation_id: 'pres-1' });
    expect(screen.getByTestId('present-tour-summary-body')).toHaveTextContent('Adds the thing.');
    expect(railRow('src/foo.ts')).toBeInTheDocument();
    expect(tourFile('src/foo.ts')).toHaveTextContent('Core logic');
    expect(screen.getByText(/Round 1/)).toBeInTheDocument();
    expect(screen.getByText('a1b2c3d…0011223')).toBeInTheDocument();
  });

  it('shows an error state for an unknown presentation id', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const daemon = await openPresentation('window=present&presentation=missing-id', (scripted) => {
      scripted.on('get_presentation_round', () => ({
        event: 'get_presentation_round_result',
        success: false,
        error: 'presentation not found',
      }));
    });

    expect(await daemon.received('get_presentation_round')).toMatchObject({ presentation_id: 'missing-id' });
    expect(screen.getByText('presentation not found')).toBeInTheDocument();
  });

  it('shows an error state when no presentation id is given', async () => {
    const daemon = await openPresentation('window=present');

    expect(screen.getByText('No presentation specified.')).toBeInTheDocument();
    expect(roundFetches(daemon)).toEqual([]);
  });

  it('renders the file list in manifest order with a note marker', async () => {
    await loadRound();

    const paths = screen.getAllByText(/^src\//, { selector: 'code.present-root-file-path' }).map((el) => el.textContent);
    expect(paths).toEqual(['src/foo.ts', 'src/foo.test.ts']);

    expect(railRow('src/foo.ts').querySelector('.present-root-file-note-marker')).not.toBeNull();
    expect(railRow('src/foo.test.ts').querySelector('.present-root-file-note-marker')).toBeNull();
  });

  it('renders per-file ± stats when the round carries them, and omits them otherwise', async () => {
    await loadRound({ round: roundWithStats });

    const statsEl = railRow('src/foo.ts').querySelector('.present-root-file-stats');
    expect(statsEl).not.toBeNull();
    expect(statsEl?.querySelector('.adds')?.textContent).toBe('+12');
    expect(statsEl?.querySelector('.dels')?.textContent).toBe('−3');

    expect(railRow('src/foo.test.ts').querySelector('.present-root-file-stats')).toBeNull();
  });

  it('shows a comment-count chip on rows with submitted comments or drafts, sized to the count', async () => {
    const daemon = await loadRound({
      diff: serveDiffs,
      comments: [
        comment({ id: 'submitted-1', content: 'from a prior round', filepath: 'src/foo.ts', line_start: 2, line_end: 2 }),
        comment({ id: 'submitted-2', content: 'a second one', filepath: 'src/foo.ts', line_start: 4, line_end: 4 }),
      ],
    });
    await settle(daemon);

    expect(railRow('src/foo.ts').querySelector('.present-root-file-comment-chip')?.textContent).toBe('2');
    expect(railRow('src/foo.test.ts').querySelector('.present-root-file-comment-chip')).toBeNull();

    await writeComment(daemon, 'src/foo.test.ts', 'line 1', 'draft comment');
    expect(railRow('src/foo.test.ts').querySelector('.present-root-file-comment-chip')?.textContent).toBe('1');
  });

  it('the pinned Summary row scrolls to the top and a file row click still works afterward', async () => {
    const scrolledToTop = vi.spyOn(HTMLElement.prototype, 'scrollTo').mockImplementation(() => {});
    const daemon = await loadRound({ diff: serveDiffs });
    await settle(daemon);

    fireEvent.click(railRow('src/foo.ts'));
    expect(railRow('src/foo.ts')).toHaveClass('selected');

    scrolledToTop.mockClear();
    fireEvent.click(screen.getByTestId('present-root-summary-row'));
    await settle(daemon);
    expect(screen.getByTestId('present-root-summary-row')).toHaveClass('selected');
    expect(railRow('src/foo.ts')).not.toHaveClass('selected');
    expect(scrolledToTop).toHaveBeenCalledWith(expect.objectContaining({ top: 0 }));

    diffRendererScrolls.length = 0;
    fireEvent.click(railRow('src/foo.test.ts'));
    await settle(daemon);
    expect(railRow('src/foo.test.ts')).toHaveClass('selected');
    expect(screen.getByTestId('present-root-summary-row')).not.toHaveClass('selected');
    expect(diffRendererScrolls).toEqual([expect.objectContaining({ id: 'src/foo.test.ts' })]);
  });

  it('defaults the active stop to the pinned Summary row when the round has a summary', async () => {
    await loadRound();

    expect(screen.getByTestId('present-root-summary-row')).toHaveClass('selected');
    expect(railRow('src/foo.ts')).not.toHaveClass('selected');
  });

  it('defaults the active stop to the first file when the round has no summary', async () => {
    await loadRound({ round: roundWithoutSummary });

    expect(railRow('src/foo.ts')).toHaveClass('selected');
    expect(screen.getByTestId('present-root-summary-row')).not.toHaveClass('selected');
  });

  it('fetches every manifest file’s diff up front, exactly once per round', async () => {
    const daemon = await loadRound({ diff: serveDiffs });
    await settle(daemon);

    const requests = fileDiffRequests(daemon);
    expect(requests.map((m) => m.path).sort()).toEqual(['src/foo.test.ts', 'src/foo.ts']);
    for (const m of requests) {
      expect(m).toMatchObject({ directory: '/repo/path', base_ref: 'a1b2c3d4e5f6', head_ref: '00112233445566' });
    }
    expect(screen.getByTestId('present-tour-summary-body')).toHaveTextContent('Adds the thing.');
    expect(tourFile('src/foo.ts')).toHaveTextContent('Core logic');
    expect(tourFile('src/foo.ts')).toHaveTextContent('old content');
    expect(tourFile('src/foo.test.ts')).toHaveTextContent('test old');
    expect(fileDiffRequests(daemon)).toHaveLength(2);
  });

  it('clicking a rail file makes it the active/highlighted file without refetching', async () => {
    const daemon = await loadRound();
    expect(fileDiffRequests(daemon)).toHaveLength(2);

    fireEvent.click(railRow('src/foo.test.ts'));

    expect(railRow('src/foo.test.ts')).toHaveClass('selected');
    expect(fileDiffRequests(daemon)).toHaveLength(2);
  });

  it('moves the selection with j/k keyboard shortcuts', async () => {
    await loadRound();
    expect(screen.getByTestId('present-root-summary-row')).toHaveClass('selected');

    fireEvent.keyDown(window, { key: 'j' });
    expect(railRow('src/foo.ts')).toHaveClass('selected');

    fireEvent.keyDown(window, { key: 'j' });
    expect(railRow('src/foo.test.ts')).toHaveClass('selected');

    fireEvent.keyDown(window, { key: 'k' });
    expect(railRow('src/foo.ts')).toHaveClass('selected');
  });

  it('k from the first file reaches the pinned Summary stop when the round has a summary', async () => {
    const scrolledToTop = vi.spyOn(HTMLElement.prototype, 'scrollTo').mockImplementation(() => {});
    const daemon = await loadRound({ diff: serveDiffs });
    await settle(daemon);

    fireEvent.click(railRow('src/foo.ts'));
    expect(railRow('src/foo.ts')).toHaveClass('selected');

    fireEvent.keyDown(window, { key: 'k' });
    await settle(daemon);
    expect(screen.getByTestId('present-root-summary-row')).toHaveClass('selected');

    scrolledToTop.mockClear();
    diffRendererScrolls.length = 0;
    fireEvent.keyDown(window, { key: 'k' });
    await settle(daemon);
    expect(screen.getByTestId('present-root-summary-row')).toHaveClass('selected');
    expect(scrolledToTop).not.toHaveBeenCalled();
    expect(diffRendererScrolls).toEqual([]);
  });

  it('k from the first file is a no-op when the round has no summary', async () => {
    await loadRound({ round: roundWithoutSummary });
    expect(railRow('src/foo.ts')).toHaveClass('selected');

    fireEvent.keyDown(window, { key: 'k' });

    expect(railRow('src/foo.ts')).toHaveClass('selected');
    expect(screen.getByTestId('present-root-summary-row')).not.toHaveClass('selected');
  });

  it('shows a drift pill (with the long explanation in its title) iff repoHeadSha differs from the pinned round head, and it dismisses', async () => {
    await loadRound({ repoHeadSha: 'deadbeef000000' });

    const pill = screen.getByRole('status');
    expect(pill.textContent).toContain('deadbee');
    expect(pill.getAttribute('title')).toContain('The repo has moved on since this round was pinned');

    fireEvent.click(screen.getByLabelText('Dismiss'));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('shows no drift pill when repoHeadSha matches the pinned round head', async () => {
    await loadRound({ repoHeadSha: round.head_sha });

    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('shows the summary while on the pinned Summary stop, and folds it after navigating to a file', async () => {
    await loadRound();
    expect(summaryToggle()).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(railRow('src/foo.ts'));

    expect(summaryToggle()).toHaveAttribute('aria-expanded', 'false');
  });

  it('unfolds the summary when the rail Summary row is clicked after navigating away', async () => {
    await loadRound();

    fireEvent.click(railRow('src/foo.ts'));
    expect(summaryToggle()).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(screen.getByTestId('present-root-summary-row'));
    expect(summaryToggle()).toHaveAttribute('aria-expanded', 'true');
  });

  it('folds and unfolds the summary from its own toggle while still on the Summary stop', async () => {
    await loadRound();
    expect(summaryToggle()).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(summaryToggle());
    expect(summaryToggle()).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(summaryToggle());
    expect(summaryToggle()).toHaveAttribute('aria-expanded', 'true');
  });

  it('shows an inline error when a diff fetch fails, without blanking the window', async () => {
    const daemon = await loadRound({
      diff: (command) => ({
        event: 'file_diff_result',
        success: false,
        directory: command.directory,
        path: command.path,
        original: '',
        modified: '',
        error: 'git show failed',
      }),
    });
    await settle(daemon);

    expect(tourFile('src/foo.ts')).toHaveTextContent('git show failed');
    expect(screen.getByText('My presentation')).toBeInTheDocument();
  });

  it('lists skipped files dimmed under a Skipped section header, and still clickable', async () => {
    await loadRound({ round: roundWithSkip });

    expect(screen.getByText('Skipped · 2')).toBeInTheDocument();
    expect(railRow('src/generated.ts')).toHaveClass('present-root-file-skipped');
    expect(railRow('src/vendor.ts')).toBeInTheDocument();

    fireEvent.click(railRow('src/generated.ts'));
    expect(railRow('src/generated.ts')).toHaveClass('selected');
  });

  it('shows a Tour section header sized to the manifest file count', async () => {
    await loadRound();
    expect(screen.getByText('Tour · 2')).toBeInTheDocument();
  });

  describe('file grouping (Tour / Other / Skipped)', () => {
    it('renders changed_files paths not in the manifest under an alphabetical Other section', async () => {
      await loadRound({ round: roundWithChangedFiles });

      expect(screen.getByText('Other · 1')).toBeInTheDocument();
      expect(screen.getByText('Skipped · 2')).toBeInTheDocument();

      const paths = screen.getAllByText(/^src\//, { selector: 'code.present-root-file-path' }).map((el) => el.textContent);
      expect(paths).toEqual(['src/foo.ts', 'src/foo.test.ts', 'src/extra.ts', 'src/generated.ts', 'src/vendor.ts']);
    });

    it('shows no Other section when the round carries no changed_files', async () => {
      await loadRound({ round: roundWithSkip });
      expect(screen.queryByText(/^Other ·/)).not.toBeInTheDocument();
    });

    it('shows ± stats and a comment chip on an Other row', async () => {
      await loadRound({
        round: roundWithChangedFiles,
        comments: [comment({ id: 'c1', content: 'note on the extra file', filepath: 'src/extra.ts', line_start: 1, line_end: 1 })],
      });

      const row = railRow('src/extra.ts');
      const statsEl = row.querySelector('.present-root-file-stats');
      expect(statsEl?.querySelector('.adds')?.textContent).toBe('+5');
      expect(statsEl?.querySelector('.dels')?.textContent).toBe('−1');
      expect(row.querySelector('.present-root-file-comment-chip')?.textContent).toBe('1');
    });

    it('counts progress over tour + other only, excluding skipped', async () => {
      await loadRound({ round: roundWithChangedFiles });
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/3');
    });

    it('the coverage advisory in the submit dialog excludes skipped files', async () => {
      await loadRound({ round: roundWithChangedFiles });

      fireEvent.keyDown(window, { key: 's' });

      const coverage = screen.getByTestId('present-root-submit-coverage').textContent ?? '';
      expect(coverage).toContain('src/foo.ts');
      expect(coverage).toContain('src/foo.test.ts');
      expect(coverage).toContain('src/extra.ts');
      expect(coverage).not.toContain('src/generated.ts');
      expect(coverage).not.toContain('src/vendor.ts');
    });

    it('j leaving a skipped file does not auto-mark it, even though j still walks through it', async () => {
      await loadRound({ round: roundWithChangedFiles });

      for (let i = 0; i < 5; i++) {
        fireEvent.keyDown(window, { key: 'j' });
      }

      expect(railRow('src/vendor.ts')).toHaveClass('selected');
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('3/3');
      expect(railRow('src/generated.ts')).not.toHaveClass('reviewed');
      expect(railRow('src/vendor.ts')).not.toHaveClass('reviewed');
    });
  });

  async function loadRoundWithDiff(options: Pick<LoadOptions, 'comments' | 'round'> = {}): Promise<ScriptedDaemon> {
    const daemon = await loadRound({ ...options, diff: serveDiffs });
    await settle(daemon);
    expect(tourFile('src/foo.ts')).toHaveTextContent('old content');
    return daemon;
  }

  function submitFrom(button: string) {
    fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));
    fireEvent.click(screen.getByRole('button', { name: button }));
  }

  it('shows a locally-added draft in the tour at the line it was written on', async () => {
    const daemon = await loadRoundWithDiff();

    await writeComment(daemon, 'src/foo.ts', 'line 3', 'looks off');

    expect(within(tourFile('src/foo.ts')).getByText('looks off')).toBeInTheDocument();
  });

  it('keeps submitted comments read-only while leaving drafts editable', async () => {
    const daemon = await loadRoundWithDiff({
      comments: [comment({ id: 'submitted-1', content: 'from a prior round', filepath: 'src/foo.ts', line_start: 2, line_end: 2 })],
    });

    await writeComment(daemon, 'src/foo.ts', 'line 3', 'looks off');

    expect(within(threadWith('from a prior round')).queryByRole('button', { name: 'Edit' })).toBeNull();
    expect(within(threadWith('looks off')).getByRole('button', { name: 'Edit' })).toBeInTheDocument();
  });

  it('sends the correct wire shape when submitting new-side and old-side drafts', async () => {
    const daemon = await loadRoundWithDiff();

    await writeComment(daemon, 'src/foo.ts', 'line 3', 'new-side comment');
    await writeComment(daemon, 'src/foo.ts', 'old line 4', 'old-side comment');
    submitFrom('Submit feedback');

    const request = await daemon.received('present_submit_round');
    expect(request).toMatchObject({ round_id: 'round-1', verdict: 'feedback', handback: true });
    expect(request.comments).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ filepath: 'src/foo.ts', line_start: 3, line_end: 3, side: 'new', content: 'new-side comment' }),
        expect.objectContaining({ filepath: 'src/foo.ts', line_start: 4, line_end: 4, side: 'old', content: 'old-side comment' }),
      ]),
    );
  });

  it('clears drafts and refetches the round after a successful submit', async () => {
    const daemon = await loadRoundWithDiff();
    daemon.on('present_submit_round', () => ({ event: 'present_submit_round_result', success: true, round_id: 'round-1' }));

    await writeComment(daemon, 'src/foo.ts', 'line 3', 'looks off');
    submitFrom('Submit feedback');
    await settle(daemon);

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByText('Submit review')).toBeInTheDocument();
    expect(screen.queryByText('looks off')).toBeNull();
    expect(roundFetches(daemon)).toHaveLength(2);
  });

  it('hides the presentation window after a successful submit', async () => {
    const daemon = await loadRoundWithDiff();
    daemon.on('present_submit_round', () => ({ event: 'present_submit_round_result', success: true, round_id: 'round-1' }));

    submitFrom('Submit feedback');
    await daemon.idle();

    expect(mockHide).toHaveBeenCalledTimes(1);
  });

  it('keeps drafts and shows an inline error when submit fails', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const daemon = await loadRoundWithDiff();
    daemon.on('present_submit_round', () => ({
      event: 'present_submit_round_result', success: false, round_id: 'round-1', error: 'daemon unreachable',
    }));

    await writeComment(daemon, 'src/foo.ts', 'line 3', 'looks off');
    submitFrom('Submit feedback');
    await daemon.idle();

    expect(screen.getByText('daemon unreachable')).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(within(tourFile('src/foo.ts')).getByText('looks off')).toBeInTheDocument();
  });

  it('sends verdict "approved" when the Approve button is clicked', async () => {
    const daemon = await loadRoundWithDiff();

    submitFrom('Approve');

    expect(await daemon.received('present_submit_round')).toMatchObject({ verdict: 'approved', handback: true });
  });

  it('closes the presentation without submitting the round when Close review is clicked', async () => {
    const daemon = await loadRoundWithDiff();
    daemon.on('present_close', () => ({ event: 'present_close_result', success: true, presentation_id: 'pres-1' }));

    await writeComment(daemon, 'src/foo.ts', 'line 3', 'a draft to be discarded');
    submitFrom('Close review');
    await daemon.idle();

    expect(await daemon.received('present_close')).toMatchObject({ presentation_id: 'pres-1' });
    expect(daemon.sentOf('present_submit_round')).toEqual([]);
    expect(mockHide).toHaveBeenCalledTimes(1);
  });

  it('shows Approve, Submit feedback, and Close review actions in the submit dialog', async () => {
    await loadRoundWithDiff();

    fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));

    expect(screen.getByRole('button', { name: 'Approve' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Submit feedback' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Close review' })).toBeInTheDocument();
  });

  it('keeps a draft on one file visible in the tour after navigating to another file', async () => {
    const daemon = await loadRoundWithDiff();

    await writeComment(daemon, 'src/foo.ts', 'line 3', 'on foo.ts');
    expect(within(tourFile('src/foo.ts')).getByText('on foo.ts')).toBeInTheDocument();

    fireEvent.click(railRow('src/foo.test.ts'));
    await settle(daemon);

    expect(within(tourFile('src/foo.ts')).getByText('on foo.ts')).toBeInTheDocument();
  });

  it('does not apply a stale round’s late file_diff_result to a newer round for the same path', async () => {
    const daemon = await loadRound();
    const round1RequestId = latestFileDiffRequestId(daemon, 'src/foo.ts');

    const round2 = { ...round, id: 'round-2', seq: 2, base_sha: 'fedcba098765', head_sha: '998877665544' };
    daemon.on('get_presentation_round', () => roundResult({ round: round2 }));
    daemon.emit({ event: 'presentation_updated', presentation });
    await daemon.idle();

    expect(roundFetches(daemon)).toHaveLength(2);
    expect(fileDiffRequests(daemon, 'src/foo.ts')).toHaveLength(2);
    const round2RequestId = latestFileDiffRequestId(daemon, 'src/foo.ts');
    expect(round2RequestId).not.toBe(round1RequestId);

    daemon.emit({
      event: 'file_diff_result',
      success: true,
      directory: '/repo/path',
      path: 'src/foo.ts',
      request_id: round1RequestId,
      original: 'STALE round-1 original',
      modified: 'STALE round-1 modified',
    });
    await settle(daemon);
    expect(tourFile('src/foo.ts')).not.toHaveTextContent('STALE round-1');

    daemon.emit({
      event: 'file_diff_result',
      success: true,
      directory: '/repo/path',
      path: 'src/foo.ts',
      request_id: round2RequestId,
      original: 'FRESH round-2 original',
      modified: 'FRESH round-2 modified',
    });
    await settle(daemon);
    expect(tourFile('src/foo.ts')).toHaveTextContent('FRESH round-2 original');
  });

  describe('review progress + keyboard model', () => {
    it('toggling reviewed from the tour updates the rail count and row styling', async () => {
      const daemon = await loadRoundWithDiff();
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');

      fireEvent.click(within(tourFile('src/foo.ts')).getByRole('button', { name: /Mark reviewed/ }));
      await settle(daemon);

      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
      expect(railRow('src/foo.ts')).toHaveClass('reviewed');
      expect(within(tourFile('src/foo.ts')).getByRole('button', { name: /Reviewed/ })).toBeInTheDocument();
    });

    it('r toggles reviewed on the active file', async () => {
      await loadRound();

      fireEvent.keyDown(window, { key: 'j' });
      expect(railRow('src/foo.ts')).toHaveClass('selected');

      fireEvent.keyDown(window, { key: 'r' });
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
      expect(railRow('src/foo.ts')).toHaveClass('reviewed');

      fireEvent.keyDown(window, { key: 'r' });
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');
    });

    it('j marks the file being left as reviewed (auto-mark-on-leave), k never marks', async () => {
      await loadRound();
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');

      fireEvent.keyDown(window, { key: 'j' });
      expect(railRow('src/foo.ts')).toHaveClass('selected');
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');

      fireEvent.keyDown(window, { key: 'j' });
      expect(railRow('src/foo.test.ts')).toHaveClass('selected');
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
      expect(railRow('src/foo.ts')).toHaveClass('reviewed');
      expect(railRow('src/foo.test.ts')).not.toHaveClass('reviewed');

      fireEvent.keyDown(window, { key: 'k' });
      expect(railRow('src/foo.ts')).toHaveClass('selected');
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
    });

    it('does not intercept single-letter shortcuts while typing a comment', async () => {
      const daemon = await loadRoundWithDiff();

      fireEvent.click(screen.getByRole('button', { name: 'Comment on src/foo.ts line 3' }));
      const textarea = within(screen.getByTestId('diff-comment-form')).getByPlaceholderText('Add a comment...');

      fireEvent.keyDown(textarea, { key: 'r' });
      fireEvent.keyDown(textarea, { key: 'j' });
      fireEvent.keyDown(textarea, { key: 's' });
      await settle(daemon);

      expect(screen.getByTestId('present-root-summary-row')).toHaveClass('selected');
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('s opens the submit dialog', async () => {
      await loadRound();

      fireEvent.keyDown(window, { key: 's' });

      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('shows an advisory, non-blocking coverage line in the submit dialog for unreviewed files', async () => {
      await loadRound();

      fireEvent.keyDown(window, { key: 'j' });
      fireEvent.keyDown(window, { key: 'r' });
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');

      fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));

      expect(screen.getByTestId('present-root-submit-coverage').textContent).toContain('src/foo.test.ts');
      expect(screen.getByRole('button', { name: 'Submit feedback' })).not.toBeDisabled();
    });

    it('shows no coverage line once every file is reviewed', async () => {
      await loadRound();

      fireEvent.keyDown(window, { key: 'j' });
      fireEvent.keyDown(window, { key: 'r' });
      fireEvent.keyDown(window, { key: 'j' });
      fireEvent.keyDown(window, { key: 'r' });
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('2/2');

      fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));

      expect(screen.queryByTestId('present-root-submit-coverage')).not.toBeInTheDocument();
    });

    it('persists reviewed marks in localStorage scoped to the presentation and round', async () => {
      await loadRound();

      fireEvent.keyDown(window, { key: 'j' });
      fireEvent.keyDown(window, { key: 'r' });
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');

      const raw = window.localStorage.getItem('attn.present.reviewed.pres-1.round-1');
      expect(JSON.parse(raw!)).toEqual(['src/foo.ts']);
    });
  });

  describe('manifest author annotations', () => {
    const reviewerReply = comment({
      id: 'submitted-1', content: 'a reviewer reply', filepath: 'src/foo.ts', line_start: 2, line_end: 2,
    });

    it('shows manifest annotations as read-only comments by Claude, ahead of the reviewer comments at their line', async () => {
      await loadRoundWithDiff({ round: roundWithAnnotations, comments: [reviewerReply] });

      const lineTwo = threadWith('why this line?');
      const bodies = Array.from(lineTwo.querySelectorAll('.diff-comment-content')).map((el) => el.textContent);
      expect(bodies).toEqual(['why this line?', 'a reviewer reply']);
      expect(lineTwo).toHaveTextContent('Claude');

      const lineFour = threadWith('first note');
      expect(lineFour).toHaveTextContent('second note');
      expect(within(lineFour).queryByRole('button', { name: 'Edit' })).toBeNull();
      expect(within(lineFour).queryByRole('button', { name: 'Delete' })).toBeNull();
    });

    it('merges annotation counts into the same rail comment chip as reviewer comments', async () => {
      await loadRound({ round: roundWithAnnotations, comments: [reviewerReply] });

      expect(railRow('src/foo.ts').querySelector('.present-root-file-comment-chip')?.textContent).toBe('4');
    });

    it('shows the N/P hint in the drive bar when the round has annotations', async () => {
      await loadRound({ round: roundWithAnnotations });
      expect(screen.getByTestId('present-drive-bar').textContent).toContain('annotations');
    });

    it('leaves the N/P hint out of the drive bar when the round has no annotations', async () => {
      await loadRound();
      expect(screen.getByTestId('present-drive-bar').textContent).not.toContain('annotations');
    });

    it('n/p hop across every annotation anchor in document order and wrap', async () => {
      const centered = vi.spyOn(HTMLElement.prototype, 'scrollIntoView').mockImplementation(() => {});
      const daemon = await loadRoundWithDiff({ round: roundWithAnnotations });
      const centeredThread = () => centered.mock.contexts[centered.mock.contexts.length - 1] as HTMLElement;

      const hop = async (key: 'n' | 'p') => {
        fireEvent.keyDown(window, { key });
        await settle(daemon);
        return centeredThread().textContent;
      };

      expect(await hop('n')).toContain('why this line?');
      expect(await hop('n')).toContain('first note');
      expect(await hop('n')).toContain('why this line?');
      expect(await hop('p')).toContain('first note');
    });
  });
});
