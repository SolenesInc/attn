import type { HarvestWhen } from '../types/generated';

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
  const host = id.indexOf(':');
  const pullRequest = host === -1 ? id : id.slice(host + 1);
  const hash = pullRequest.lastIndexOf('#');
  const number = hash === -1 ? pullRequest : pullRequest.slice(hash);
  return {
    pullRequest,
    sentence: `harvests when ${pullRequest} merges`,
    marker: `harvests on ${number}`,
    url: condition?.url?.trim() ?? '',
  };
}
