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
  it('hands a terminal verdict to the surface by close generation', () => {
    const onSessionReopenResolved = vi.fn();
    const handled = handleSessionLedgerDaemonEvent(
      { event: 'session_reopen_resolved', session_id: 's1', closed_at: '2026-09-05T10:00:00Z', success: true, reopen },
      { pending: new Map(), onSessionReopenResolved },
    );
    expect(handled).toBe(true);
    expect(onSessionReopenResolved).toHaveBeenCalledWith({
      sessionId: 's1',
      closedAt: '2026-09-05T10:00:00Z',
      success: true,
      reopen,
    });
  });

  it('hands a terminal failure to the surface', () => {
    const onSessionReopenResolved = vi.fn();
    handleSessionLedgerDaemonEvent(
      { event: 'session_reopen_resolved', session_id: 's1', closed_at: '2026-09-05T10:00:00Z', success: false, error: 'git unavailable' },
      { pending: new Map(), onSessionReopenResolved },
    );
    expect(onSessionReopenResolved).toHaveBeenCalledWith({
      sessionId: 's1',
      closedAt: '2026-09-05T10:00:00Z',
      success: false,
      error: 'git unavailable',
    });
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
