import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SETTLED_READ_FRAMES, settleBeforeBridgeRequest, settleUi } from './uiAutomationSettle';

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['requestAnimationFrame', 'cancelAnimationFrame', 'setTimeout', 'clearTimeout'] });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('bridge read settling', () => {
  it.each([
    ['a direct settled read', () => settleUi()],
    ['an ordinary bridge action', () => settleBeforeBridgeRequest('get_state')],
  ])('waits sequential frames and then a task for %s', async (_name, settle) => {
    const frames = vi.spyOn(window, 'requestAnimationFrame');
    let finished = false;
    const settled = settle().then(() => { finished = true; });

    for (let index = 0; index < SETTLED_READ_FRAMES; index += 1) {
      expect(frames).toHaveBeenCalledTimes(index + 1);
      vi.advanceTimersToNextFrame();
      await Promise.resolve();
      expect(finished).toBe(false);
    }

    expect(frames).toHaveBeenCalledTimes(SETTLED_READ_FRAMES);
    expect(vi.getTimerCount()).toBe(1);
    await vi.advanceTimersToNextTimerAsync();
    await settled;
    expect(finished).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('answers the named synchronous actions without a frame or task', async () => {
    const frames = vi.spyOn(window, 'requestAnimationFrame');
    for (const action of ['ping', 'capture_perf_snapshot', 'clear_perf_counters']) {
      await settleBeforeBridgeRequest(action);
      expect(frames, action).not.toHaveBeenCalled();
      expect(vi.getTimerCount(), action).toBe(0);
    }
  });
});
