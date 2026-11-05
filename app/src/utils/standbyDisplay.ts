import type { Seed } from '../hooks/useDaemonSocket';
import { pullRequestRef } from './pullRequestRef';
import { formatWakeTimeShort } from './snoozeDurations';

export interface StandbyPullRequest {
  id: string;
  label: string;
  word: string;
  title: string;
}

export interface StandbyDisplay {
  note: string;
  pullRequests: StandbyPullRequest[];
  reminderAt: string;
  wakeBlocked: string;
}

export function standbyDisplay(seed: Seed, now: number): StandbyDisplay | null {
  const standby = seed.standby;
  if (!standby) return null;
  return {
    note: standby.note,
    pullRequests: standby.pull_requests.map((pr) => {
      const { pullRequest, number } = pullRequestRef(pr.pr_id);
      const word = pr.state === 'open' && pr.readiness_state ? pr.readiness_state : pr.state;
      return { id: pr.pr_id, label: number, word, title: `${pullRequest} ${word}` };
    }),
    reminderAt: formatWakeTimeShort(seed.reminder?.at, now),
    wakeBlocked: standby.wake_blocked ?? '',
  };
}
