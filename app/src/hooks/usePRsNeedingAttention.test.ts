import { renderHook } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { usePRsNeedingAttention } from './usePRsNeedingAttention';
import type { DaemonPR } from './useDaemonSocket';
import { PRRole } from '../types/generated';

vi.mock('../store/daemonSessions', () => ({
  useDaemonStore: vi.fn(),
}));

import { useDaemonStore } from '../store/daemonSessions';

function createPR(overrides: Partial<DaemonPR> = {}): DaemonPR {
  return {
    id: 'pr-1',
    host: 'github.com',
    repo: 'org/repo',
    number: 1,
    title: 'Test PR',
    url: 'https://github.com/org/repo/pull/1',
    author: 'test-user',
    role: PRRole.Reviewer,
    state: 'waiting',
    reason: 'review_requested',
    last_updated: '2024-01-01T00:00:00Z',
    last_polled: '2024-01-01T00:00:00Z',
    muted: false,
    details_fetched: true,
    approved_by_me: false,
    has_new_changes: false,
    ...overrides,
  };
}

describe('usePRsNeedingAttention', () => {
  beforeEach(() => {
    vi.mocked(useDaemonStore).mockReturnValue({
      isRepoMuted: () => false,
      isAuthorMuted: () => false,
      repoStates: [],
      authorStates: [],
    } as ReturnType<typeof useDaemonStore>);
  });

  it('returns empty arrays when no PRs', () => {
    const { result } = renderHook(() => usePRsNeedingAttention([]));

    expect(result.current.activePRs).toEqual([]);
    expect(result.current.needsAttention).toEqual([]);
    expect(result.current.reviewRequested).toEqual([]);
    expect(result.current.yourPRs).toEqual([]);
  });

  it('includes non-muted PRs in activePRs', () => {
    const prs = [createPR({ id: 'pr-1' }), createPR({ id: 'pr-2' })];
    const { result } = renderHook(() => usePRsNeedingAttention(prs));

    expect(result.current.activePRs).toHaveLength(2);
  });

  it('filters out individually muted PRs', () => {
    const prs = [
      createPR({ id: 'pr-1', muted: false }),
      createPR({ id: 'pr-2', muted: true }),
    ];
    const { result } = renderHook(() => usePRsNeedingAttention(prs));

    expect(result.current.activePRs).toHaveLength(1);
    expect(result.current.activePRs[0].id).toBe('pr-1');
  });

  it('filters out repo-muted PRs', () => {
    vi.mocked(useDaemonStore).mockReturnValue({
      isRepoMuted: (repo: string) => repo === 'org/muted-repo',
      isAuthorMuted: () => false,
      repoStates: [],
      authorStates: [],
    } as ReturnType<typeof useDaemonStore>);

    const prs = [
      createPR({ id: 'pr-1', repo: 'org/active-repo' }),
      createPR({ id: 'pr-2', repo: 'org/muted-repo' }),
    ];
    const { result } = renderHook(() => usePRsNeedingAttention(prs));

    expect(result.current.activePRs).toHaveLength(1);
    expect(result.current.activePRs[0].id).toBe('pr-1');
  });

  it('filters out author-muted PRs', () => {
    vi.mocked(useDaemonStore).mockReturnValue({
      isRepoMuted: () => false,
      isAuthorMuted: (author: string) => author === 'muted-author',
      repoStates: [],
      authorStates: [],
    } as ReturnType<typeof useDaemonStore>);

    const prs = [
      createPR({ id: 'pr-1', author: 'active-author' }),
      createPR({ id: 'pr-2', author: 'muted-author' }),
    ];
    const { result } = renderHook(() => usePRsNeedingAttention(prs));

    expect(result.current.activePRs).toHaveLength(1);
    expect(result.current.activePRs[0].id).toBe('pr-1');
  });

  it('filters out hidden PRs', () => {
    const prs = [
      createPR({ id: 'pr-1' }),
      createPR({ id: 'pr-2' }),
    ];
    const hiddenPRs = new Set(['pr-2']);
    const { result } = renderHook(() => usePRsNeedingAttention(prs, hiddenPRs));

    expect(result.current.activePRs).toHaveLength(1);
    expect(result.current.activePRs[0].id).toBe('pr-1');
  });

  it('includes non-approved PRs in needsAttention', () => {
    const prs = [createPR({ id: 'pr-1', approved_by_me: false })];
    const { result } = renderHook(() => usePRsNeedingAttention(prs));

    expect(result.current.needsAttention).toHaveLength(1);
  });

  it('excludes approved PRs without new changes from needsAttention', () => {
    const prs = [
      createPR({ id: 'pr-1', approved_by_me: true, has_new_changes: false }),
    ];
    const { result } = renderHook(() => usePRsNeedingAttention(prs));

    expect(result.current.activePRs).toHaveLength(1);
    expect(result.current.needsAttention).toHaveLength(0);
  });

  it('includes approved PRs with new changes in needsAttention', () => {
    const prs = [
      createPR({ id: 'pr-1', approved_by_me: true, has_new_changes: true }),
    ];
    const { result } = renderHook(() => usePRsNeedingAttention(prs));

    expect(result.current.needsAttention).toHaveLength(1);
  });

  it('splits needsAttention by role', () => {
    const prs = [
      createPR({ id: 'pr-1', role: PRRole.Reviewer }),
      createPR({ id: 'pr-2', role: PRRole.Author }),
      createPR({ id: 'pr-3', role: PRRole.Reviewer }),
    ];
    const { result } = renderHook(() => usePRsNeedingAttention(prs));

    expect(result.current.reviewRequested).toHaveLength(2);
    expect(result.current.yourPRs).toHaveLength(1);
    expect(result.current.yourPRs[0].id).toBe('pr-2');
  });

  it('handles combined filters correctly', () => {
    vi.mocked(useDaemonStore).mockReturnValue({
      isRepoMuted: (repo: string) => repo === 'org/muted',
      isAuthorMuted: () => false,
      repoStates: [],
      authorStates: [],
    } as ReturnType<typeof useDaemonStore>);

    const prs = [
      createPR({ id: 'active-reviewer', role: PRRole.Reviewer }),
      createPR({ id: 'active-author', role: PRRole.Author }),
      createPR({ id: 'muted-pr', muted: true }),
      createPR({ id: 'muted-repo', repo: 'org/muted' }),
      createPR({ id: 'approved-no-changes', approved_by_me: true, has_new_changes: false }),
      createPR({ id: 'approved-with-changes', role: PRRole.Author, approved_by_me: true, has_new_changes: true }),
    ];
    const hiddenPRs = new Set(['hidden-pr']);
    const { result } = renderHook(() => usePRsNeedingAttention(prs, hiddenPRs));

    expect(result.current.activePRs).toHaveLength(4);
    expect(result.current.needsAttention).toHaveLength(3);
    expect(result.current.reviewRequested).toHaveLength(1);
    expect(result.current.yourPRs).toHaveLength(2);
  });

  describe('reactivity to store changes', () => {
    it('reacts immediately when repo mute state changes', () => {
      // isRepoMuted keeps one stable reference across a repoStates change, the
      // way zustand's selectors do; the hook still has to recalculate.
      const mutedRepos = new Set<string>();

      const stableIsRepoMuted = (repo: string) => mutedRepos.has(repo);

      vi.mocked(useDaemonStore).mockReturnValue({
        isRepoMuted: stableIsRepoMuted,
        isAuthorMuted: () => false,
        repoStates: [],
        authorStates: [],
      } as unknown as ReturnType<typeof useDaemonStore>);

      const prs = [createPR({ id: 'pr-1', repo: 'org/repo' })];
      const { result, rerender } = renderHook(() => usePRsNeedingAttention(prs));

      expect(result.current.activePRs).toHaveLength(1);
      expect(result.current.activePRs[0].id).toBe('pr-1');

      mutedRepos.add('org/repo');

      vi.mocked(useDaemonStore).mockReturnValue({
        isRepoMuted: stableIsRepoMuted,
        isAuthorMuted: () => false,
        repoStates: [{ repo: 'org/repo', muted: true, collapsed: false }],
        authorStates: [],
      } as unknown as ReturnType<typeof useDaemonStore>);

      rerender();

      expect(result.current.activePRs).toHaveLength(0);
    });

    it('reacts immediately when author mute state changes', () => {
      // Same stable-reference setup as the repo case, for authors.
      const mutedAuthors = new Set<string>();

      const stableIsAuthorMuted = (author: string) => mutedAuthors.has(author);

      vi.mocked(useDaemonStore).mockReturnValue({
        isRepoMuted: () => false,
        isAuthorMuted: stableIsAuthorMuted,
        repoStates: [],
        authorStates: [],
      } as unknown as ReturnType<typeof useDaemonStore>);

      const prs = [createPR({ id: 'pr-1', author: 'some-author' })];
      const { result, rerender } = renderHook(() => usePRsNeedingAttention(prs));

      expect(result.current.activePRs).toHaveLength(1);
      expect(result.current.activePRs[0].id).toBe('pr-1');

      mutedAuthors.add('some-author');

      vi.mocked(useDaemonStore).mockReturnValue({
        isRepoMuted: () => false,
        isAuthorMuted: stableIsAuthorMuted,
        repoStates: [],
        authorStates: [{ author: 'some-author', muted: true }],
      } as unknown as ReturnType<typeof useDaemonStore>);

      rerender();

      expect(result.current.activePRs).toHaveLength(0);
    });
  });
});
