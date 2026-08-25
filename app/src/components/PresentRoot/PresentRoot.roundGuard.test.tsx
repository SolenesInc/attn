import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PresentTour } from '../PresentTour';
import type { PresentTourProps } from '../PresentTour';

vi.mock('../PresentTour', () => ({
  PresentTour: vi.fn(({ files }: PresentTourProps) => (
    <div data-testid="present-tour">
      {files.map((f) => (
        <div key={f.path} data-testid={`tour-file-${f.path}`}>
          {f.diff.original !== undefined && <div className="original">{f.diff.original}</div>}
          {f.diff.modified !== undefined && <div className="modified">{f.diff.modified}</div>}
        </div>
      ))}
    </div>
  )),
}));

vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => ({ hide: vi.fn() }),
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

const presentation = {
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

const roundOne = {
  id: 'round-1',
  presentation_id: 'pres-1',
  seq: 1,
  base_sha: 'aaaaaaaaaaaa',
  head_sha: 'bbbbbbbbbbbb',
  created_at: '2026-07-01T00:00:00Z',
  manifest: {
    title: 'My change',
    summary: 'Adds the thing.',
    files: [{ path: 'src/foo.ts' }],
    skip: [] as string[],
  },
};

const roundTwo = {
  ...roundOne,
  id: 'round-2',
  seq: 2,
  base_sha: 'cccccccccccc',
  head_sha: 'dddddddddddd',
};

type FileDiffResult = { success: true; original: string; modified: string };

let getPresentationRoundCalls: Array<{ resolve: (value: any) => void }>;
let sendGetFileDiffCalls: Array<{ resolve: (value: FileDiffResult) => void }>;
let capturedOnPresentationUpdated: ((p: { id: string }) => void) | undefined;

// Module scope, not re-created per useDaemonSocket() call: PresentRoot's effects
// depend on these function identities, so a fresh vi.fn() per render re-triggers them.
const mockGetPresentationRound = vi.fn(() => {
  const d = deferred<any>();
  getPresentationRoundCalls.push({ resolve: d.resolve });
  return d.promise;
});
const mockSendGetFileDiff = vi.fn(() => {
  const d = deferred<FileDiffResult>();
  sendGetFileDiffCalls.push({ resolve: d.resolve });
  return d.promise;
});

vi.mock('../../hooks/useDaemonSocket', () => ({
  useDaemonSocket: (options: { onPresentationUpdated?: (p: { id: string }) => void }) => {
    capturedOnPresentationUpdated = options.onPresentationUpdated;
    return {
      hasReceivedInitialState: true,
      connectionError: null,
      getPresentationRound: mockGetPresentationRound,
      sendGetFileDiff: mockSendGetFileDiff,
      submitPresentationRound: vi.fn(),
    };
  },
}));

function latestTourProps(): PresentTourProps | undefined {
  const calls = vi.mocked(PresentTour).mock.calls;
  return calls[calls.length - 1]?.[0] as unknown as PresentTourProps | undefined;
}

function foundFile(props: PresentTourProps | undefined) {
  return props?.files.find((f) => f.path === 'src/foo.ts');
}

function setSearch(search: string) {
  window.history.replaceState({}, '', `/?${search}`);
}

describe('PresentRoot diff-fetch round guard', () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    getPresentationRoundCalls = [];
    sendGetFileDiffCalls = [];
    capturedOnPresentationUpdated = undefined;
  });

  it('does not apply a stale round-1 diff response after a round-2 transition for the same file', async () => {
    getPresentationRoundCalls = [];
    sendGetFileDiffCalls = [];

    const { PresentRoot } = await import('./index');

    setSearch('window=present&presentation=pres-1');
    render(<PresentRoot />);

    await waitFor(() => expect(getPresentationRoundCalls).toHaveLength(1));
    act(() => {
      getPresentationRoundCalls[0].resolve({
        presentation,
        round: roundOne,
        comments: [],
        repoHeadSha: roundOne.head_sha,
      });
    });
    await waitFor(() => expect(screen.getByText('My presentation')).toBeInTheDocument());

    await waitFor(() => expect(sendGetFileDiffCalls).toHaveLength(1));

    expect(capturedOnPresentationUpdated).toBeDefined();
    act(() => {
      capturedOnPresentationUpdated!({ id: 'pres-1' });
    });
    await waitFor(() => expect(getPresentationRoundCalls).toHaveLength(2));
    act(() => {
      getPresentationRoundCalls[1].resolve({
        presentation,
        round: roundTwo,
        comments: [],
        repoHeadSha: roundTwo.head_sha,
      });
    });

    await waitFor(() => expect(sendGetFileDiffCalls).toHaveLength(2));

    act(() => {
      sendGetFileDiffCalls[0].resolve({
        success: true,
        original: 'STALE round-1 original',
        modified: 'STALE round-1 modified',
      });
    });

    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    const staleFile = foundFile(latestTourProps());
    expect(staleFile?.diff.original).not.toBe('STALE round-1 original');
    expect(staleFile?.diff.modified).not.toBe('STALE round-1 modified');

    act(() => {
      sendGetFileDiffCalls[1].resolve({
        success: true,
        original: 'FRESH round-2 original',
        modified: 'FRESH round-2 modified',
      });
    });
    await waitFor(() => {
      expect(foundFile(latestTourProps())?.diff.original).toBe('FRESH round-2 original');
    });
    expect(foundFile(latestTourProps())?.diff.modified).toBe('FRESH round-2 modified');
  });
});
