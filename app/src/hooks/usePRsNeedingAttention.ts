import { useMemo } from 'react';
import { DaemonPR } from './useDaemonSocket';
import { useDaemonStore } from '../store/daemonSessions';

interface FilteredPRs {
  activePRs: DaemonPR[];
  needsAttention: DaemonPR[];
  reviewRequested: DaemonPR[];
  yourPRs: DaemonPR[];
}

export function usePRsNeedingAttention(
  prs: DaemonPR[],
  hiddenPRs?: Set<string>
): FilteredPRs {
  const { isRepoMuted, isAuthorMuted, repoStates, authorStates } = useDaemonStore();

  return useMemo(() => {
    const activePRs = prs.filter(
      (p) => !p.muted &&
             !isRepoMuted(p.repo) &&
             !isAuthorMuted(p.author) &&
             (!hiddenPRs || !hiddenPRs.has(p.id))
    );

    const needsAttention = activePRs.filter(
      (p) => !p.approved_by_me || p.has_new_changes
    );

    const reviewRequested = needsAttention.filter((p) => p.role === 'reviewer');
    const yourPRs = needsAttention.filter((p) => p.role === 'author');

    return { activePRs, needsAttention, reviewRequested, yourPRs };
  }, [prs, isRepoMuted, isAuthorMuted, repoStates, authorStates, hiddenPRs]);
}
