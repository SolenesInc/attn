import type { HarvestWhen } from '../types/generated';
import { pullRequestRef } from './pullRequestRef';

export interface HarvestWhenDisplay {
  /** owner/repo#n — the session pull request id without its host. */
  pullRequest: string;
  /** The whole condition, for a seed's detail. */
  sentence: string;
  /** #n alone, for a row that has room for one word. */
  marker: string;
  url: string;
}

export function harvestWhenDisplay(condition?: HarvestWhen): HarvestWhenDisplay | null {
  const id = condition?.pull_request?.trim() ?? '';
  if (!id) return null;
  const { pullRequest, number } = pullRequestRef(id);
  return {
    pullRequest,
    sentence: `harvests when ${pullRequest} merges`,
    marker: `harvests on ${number}`,
    url: condition?.url?.trim() ?? '',
  };
}
