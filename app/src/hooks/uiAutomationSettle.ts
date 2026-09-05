// A frame callback runs before layout, so the ResizeObserver that measures the new layout and
// the React commit it schedules land later: a read there reports a DOM no frame ever painted.

// A frame past a 60Hz budget: requestAnimationFrame never fires in a window the compositor is
// not painting (hidden, occluded, a stalled Xvfb display) and the bridge must still answer.
const FRAME_STALL_FALLBACK_MS = 50;

// Frames a settled read needs, measured by e2e/bridge-settled-read.spec.ts: one frame, even
// with the task after it, still reads the state from before the observer's React commit.
export const SETTLED_READ_FRAMES = 2;

export function nextAnimationFrame(): Promise<void> {
  return new Promise<void>((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      resolve();
    };
    const timeoutId = window.setTimeout(finish, FRAME_STALL_FALLBACK_MS);
    window.requestAnimationFrame(() => {
      window.clearTimeout(timeoutId);
      finish();
    });
  });
}

// The task after the current frame, once layout, paint and the observers following them have
// run. Every wait here ends with it, so the statement after a wait reads a painted DOM.
export function afterFramePaints(): Promise<void> {
  return new Promise<void>((resolve) => { window.setTimeout(resolve, 0); });
}

// The awaits must stay sequential: nextAnimationFrame() registers its callback when it
// is constructed, so building them all up front queues them onto the same frame.
export async function settleUi(frames = SETTLED_READ_FRAMES): Promise<void> {
  for (let index = 0; index < frames; index += 1) {
    await nextAnimationFrame();
  }
  await afterFramePaints();
}

// Settling costs two frames and these answer without them: `ping` is the liveness probe the
// harness polls before the frontend paints, and the perf commands choose their own settle.
const SYNCHRONOUS_BRIDGE_ACTIONS = new Set([
  'ping',
  'capture_perf_snapshot',
  'clear_perf_counters',
]);

// Called by every bridge dispatcher before it hands a request to a handler, so handlers read
// and click against a settled DOM rather than each waiting for one.
export async function settleBeforeBridgeRequest(action: string): Promise<void> {
  if (SYNCHRONOUS_BRIDGE_ACTIONS.has(action)) {
    return;
  }
  await settleUi(SETTLED_READ_FRAMES);
}
