import { expect, it, vi } from 'vitest';
import { pendingRequestKey, type PendingRequests } from './daemonPendingRequests';
import { handlePullRequestDaemonEvent } from './daemonPullRequestEvents';

it('settles only the matching unwatch request with the daemon error', () => {
  const pending: PendingRequests = new Map();
  const resolve = vi.fn();
  const reject = vi.fn();
  pending.set(pendingRequestKey('pull_request_unwatch', 'mine'), { resolve, reject });

  expect(handlePullRequestDaemonEvent({
    event: 'pull_request_unwatch_result', request_id: 'other', success: false, error: 'wrong request',
  }, pending)).toBe(true);
  expect(reject).not.toHaveBeenCalled();

  handlePullRequestDaemonEvent({
    event: 'pull_request_unwatch_result', request_id: 'mine', success: false, error: 'watch persistence failed',
  }, pending);
  expect(reject.mock.calls[0][0].message).toBe('watch persistence failed');
  expect(resolve).not.toHaveBeenCalled();
});
