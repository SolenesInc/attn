import { describe, expect, it, vi } from 'vitest';
import { SessionReopenRefusal, handleSessionLedgerDaemonEvent } from './daemonSessionLedgerEvents';
import { reopenVerdictView } from '../components/sessionsLedger';
import type { SessionReopen } from '../types/generated';
import type { PendingRequests } from './daemonPendingRequests';
import { pendingRequestKey } from './daemonPendingRequests';

const offer = {
  reopenable: false,
  reason: 'the directory is gone',
  actions: ['start_fresh_elsewhere'],
  directory_state: 'missing',
  workspace_id: 'ws-1',
  workspace_plan: 'reuse',
  pane_plan: 'add',
} as SessionReopen;

function pendingReopen() {
  const pending: PendingRequests = new Map();
  const resolve = vi.fn();
  const reject = vi.fn();
  pending.set(pendingRequestKey('session_reopen', 'req-1'), { resolve, reject });
  return { pending, resolve, reject };
}

describe('session ledger daemon events for reopen', () => {
  it('settles the reopen request with the daemon result', () => {
    const { pending, resolve } = pendingReopen();
    const result = { session_id: 's1', workspace_id: 'ws-1', directory: '/tmp/x', action: 'reopen' };
    handleSessionLedgerDaemonEvent(
      { event: 'session_reopen_result', request_id: 'req-1', success: true, result },
      { pending },
    );
    expect(resolve).toHaveBeenCalledWith(result);
  });

  it('rejects the reopen request with the refusal text', () => {
    const { pending, reject } = pendingReopen();
    handleSessionLedgerDaemonEvent(
      { event: 'session_reopen_result', request_id: 'req-1', success: false, error: 's1 cannot be reopened: gone' },
      { pending },
    );
    expect(reject.mock.calls[0][0]).not.toBeInstanceOf(SessionReopenRefusal);
    expect(reject.mock.calls[0][0].message).toBe('s1 cannot be reopened: gone');
  });

  it('rejects a refused reopen with what the session offers instead', () => {
    const { pending, reject } = pendingReopen();
    const error = 's1 cannot be reopened with reopen: the directory is gone. Offered instead: start_fresh_elsewhere';
    handleSessionLedgerDaemonEvent(
      { event: 'session_reopen_result', request_id: 'req-1', success: false, error, reopen: offer },
      { pending },
    );
    const refusal = reject.mock.calls[0][0];
    expect(refusal).toBeInstanceOf(SessionReopenRefusal);
    expect(refusal.message).toBe(error);
    expect(refusal.verdict).toEqual(reopenVerdictView(offer));
  });
});
