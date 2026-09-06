import { describe, expect, it, vi } from 'vitest';
import { handleSessionLedgerDaemonEvent } from './daemonSessionLedgerEvents';
import type { PendingRequests } from './daemonPendingRequests';
import { pendingRequestKey } from './daemonPendingRequests';

const reopen = {
  reopenable: true,
  actions: ['reopen'],
  checking: false,
  directory_state: 'present',
  workspace_id: 'ws-1',
  workspace_plan: 'reuse',
  pane_plan: 'add',
};

describe('session ledger daemon events for reopen', () => {
  it('hands a refreshed verdict to the surface by session id', () => {
    const onSessionReopenRefreshed = vi.fn();
    const handled = handleSessionLedgerDaemonEvent(
      { event: 'session_reopen_refreshed', session_id: 's1', reopen },
      { pending: new Map(), onSessionReopenRefreshed },
    );
    expect(handled).toBe(true);
    expect(onSessionReopenRefreshed).toHaveBeenCalledWith('s1', reopen);
  });

  it('settles the reopen request with the daemon result', () => {
    const pending: PendingRequests = new Map();
    const resolve = vi.fn();
    pending.set(pendingRequestKey('session_reopen', 'req-1'), { resolve, reject: vi.fn() });
    const result = { session_id: 's1', workspace_id: 'ws-1', directory: '/tmp/x', action: 'reopen' };
    handleSessionLedgerDaemonEvent(
      { event: 'session_reopen_result', request_id: 'req-1', success: true, result },
      { pending },
    );
    expect(resolve).toHaveBeenCalledWith(result);
  });

  it('rejects the reopen request with the refusal text', () => {
    const pending: PendingRequests = new Map();
    const reject = vi.fn();
    pending.set(pendingRequestKey('session_reopen', 'req-1'), { resolve: vi.fn(), reject });
    handleSessionLedgerDaemonEvent(
      { event: 'session_reopen_result', request_id: 'req-1', success: false, error: 's1 cannot be reopened: gone' },
      { pending },
    );
    expect(reject).toHaveBeenCalled();
    expect(reject.mock.calls[0][0].message).toBe('s1 cannot be reopened: gone');
  });
});
