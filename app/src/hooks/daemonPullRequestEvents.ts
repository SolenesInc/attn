import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

interface PullRequestDaemonEvent {
  event?: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
}

export function handlePullRequestDaemonEvent(event: PullRequestDaemonEvent, pending: PendingRequests): boolean {
  if (event.event !== 'pull_request_unwatch_result') return false;
  settlePendingRequest(
    pending,
    'pull_request_unwatch',
    event,
    () => true,
    'Could not stop watching the pull request',
  );
  return true;
}
