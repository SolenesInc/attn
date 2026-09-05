import { describe, expect, it } from 'vitest';
import { SETTLED_READ_FRAMES, settleBeforeBridgeRequest, settleUi } from './uiAutomationSettle';

// Counts frames and reports whether the task after the last one has run — the place an
// unsettled read lands is inside a frame callback, before that task.
function frameWatcher() {
  const state = { frames: 0, ranAfterFrame: false };
  const observe = () => {
    requestAnimationFrame(() => {
      state.frames += 1;
      state.ranAfterFrame = false;
      setTimeout(() => { state.ranAfterFrame = true; }, 0);
      observe();
    });
  };
  observe();
  return state;
}

describe('bridge read settling', () => {
  it('waits the measured frames and the task that follows', async () => {
    const frame = frameWatcher();
    await settleUi();
    expect(frame.frames).toBeGreaterThanOrEqual(SETTLED_READ_FRAMES);
    expect(frame.ranAfterFrame).toBe(true);
  });

  it('settles an ordinary action', async () => {
    const frame = frameWatcher();
    await settleBeforeBridgeRequest('get_state');
    expect(frame.frames).toBeGreaterThanOrEqual(SETTLED_READ_FRAMES);
  });

  it('answers the named synchronous actions without a frame', async () => {
    for (const action of ['ping', 'capture_perf_snapshot', 'clear_perf_counters']) {
      const frame = frameWatcher();
      await settleBeforeBridgeRequest(action);
      expect(frame.frames, action).toBe(0);
    }
  });
});
