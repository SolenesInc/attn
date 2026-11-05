export function pullRequestRef(id: string): { pullRequest: string; number: string } {
  const host = id.indexOf(':');
  const pullRequest = host === -1 ? id : id.slice(host + 1);
  const hash = pullRequest.lastIndexOf('#');
  return { pullRequest, number: hash === -1 ? pullRequest : pullRequest.slice(hash) };
}
