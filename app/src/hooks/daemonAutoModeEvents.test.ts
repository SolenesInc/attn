import { describe, expect, it, vi } from 'vitest';
import { handleAutoModeDaemonEvent } from './daemonAutoModeEvents';
import { pendingRequestKey, type PendingRequests } from './daemonPendingRequests';

describe('legacy pattern dismissal results', () => {
  it.each([true, false])('settles the pending dismissal when success=%s', (success) => {
    const resolve = vi.fn();
    const reject = vi.fn();
    const pending: PendingRequests = new Map([
      [pendingRequestKey('automode_legacy_dismiss', 'dismiss-1'), { resolve, reject }],
    ]);
    const config = { legacy_patterns: [] };

    expect(handleAutoModeDaemonEvent({
      event: 'automode_config_result',
      request_id: 'dismiss-1',
      success,
      config,
      error: success ? undefined : 'The pattern no longer exists',
    }, pending)).toBe(true);

    expect(pending.size).toBe(0);
    if (success) {
      expect(resolve).toHaveBeenCalledWith({ config: expect.objectContaining(config) });
      expect(reject).not.toHaveBeenCalled();
    } else {
      expect(reject).toHaveBeenCalledWith(new Error('The pattern no longer exists'));
      expect(resolve).not.toHaveBeenCalled();
    }
  });
});
