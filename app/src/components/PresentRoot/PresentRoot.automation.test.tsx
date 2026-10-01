import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { emit, listen } from '@tauri-apps/api/event';
import { PresentRoot } from './index';
import { installScriptedDaemon } from '../../test/scriptedDaemon';

const mockHide = vi.fn();
vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => ({ hide: mockHide }),
}));

describe('PresentRoot automation', () => {
  beforeEach(() => {
    vi.mocked(listen).mockClear();
    vi.mocked(emit).mockClear();
    window.history.replaceState({}, '', '/?window=present&presentation=pres-1');
    Object.assign(window, { __ATTN_AUTOMATION_ENABLED: true });
    vi.mocked(isTauri).mockReturnValueOnce(true);
    mockHide.mockClear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    delete (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED;
  });

  it.each(['feedback', 'approve', 'close'])('automates %s with compositor frames withheld and the clock advancing after the click', async (action) => {
    const daemon = installScriptedDaemon();
    daemon.on('get_presentation_round', () => ({
      event: 'get_presentation_round_result',
      success: true,
      presentation: {
        id: 'pres-1', created_at: '2026-07-01T00:00:00Z', kind: 'changes',
        latest_round_seq: 1, latest_round_submitted: false, repo_path: '/repo',
        session_id: 'session-1', status: 'open', title: 'Automation review',
      },
      round: {
        id: 'round-1', presentation_id: 'pres-1', seq: 1,
        base_sha: 'a1b2c3d4e5f6', head_sha: '00112233445566', created_at: '2026-07-01T00:00:00Z',
        manifest: { title: 'Automation review', files: [], skip: [] },
      },
      comments: [],
    }));
    daemon.on('present_submit_round', () => ({ event: 'present_submit_round_result', success: true, round_id: 'round-1' }));
    daemon.on('present_close', () => ({ event: 'present_close_result', success: true, presentation_id: 'pres-1' }));
    render(<PresentRoot />);
    await daemon.idle();

    const listener = vi.mocked(listen).mock.calls.find(([name]) => name === 'attn://ui-automation/request')?.[1];
    expect(listener).toBeDefined();
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 0);
    screen.getByRole('button', { name: /Submit review/ }).addEventListener('click', () => {
      queueMicrotask(() => vi.setSystemTime(Date.now() + 1001));
    }, { once: true });
    await act(async () => {
      const completion = listener!({ event: 'attn://ui-automation/request', id: 1, payload: {
        request_id: 'submit-delayed-frame', action: 'present_window_submit', payload: { action },
      } });
      // Two 50ms frame fallbacks, the next task (1ms), and the old dialog poll (50ms).
      await vi.advanceTimersByTimeAsync(151);
      await completion;
      await daemon.idle();
    });

    expect(emit).toHaveBeenCalledWith('attn://ui-automation/response', {
      request_id: 'submit-delayed-frame', ok: true, result: { submitted: true, action },
    });
    if (action === 'close') {
      expect(daemon.sentOf('present_close')).toMatchObject([{ presentation_id: 'pres-1' }]);
      expect(daemon.sentOf('present_submit_round')).toHaveLength(0);
    } else {
      expect(daemon.sentOf('present_submit_round')).toMatchObject([
        { round_id: 'round-1', verdict: action === 'approve' ? 'approved' : 'feedback', handback: true },
      ]);
    }
    expect(mockHide).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
