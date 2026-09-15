import type {
  AutomationProvenance as AutomationProvenanceValue,
  SessionPullRequest,
} from '../types/generated';
import { describeSessionPullRequest, sessionPullRequestRepositoryName } from './sessionPullRequest';

export function automationProvenanceDescription(provenance: AutomationProvenanceValue): string {
  const parts = [`Automation: ${provenance.definition_name}`];
  const pr = provenance.pull_request;
  if (pr) {
    parts.push(`${pr.repository}#${pr.number}`);
    if (pr.title) parts.push(pr.title);
  }
  return parts.join(' · ');
}

export function sessionPullRequestDescription(pr: SessionPullRequest): string {
  const parts = [`PR ${sessionPullRequestRepositoryName(pr.repository)}#${pr.number}`];
  parts.push(describeSessionPullRequest(pr).label);
  if (pr.title) parts.push(pr.title);
  return parts.join(' · ');
}
