import { describe, expect, it } from 'vitest';
import { listenPtyEvents, ptyAttach } from './bridge';

describe('mock PTY bridge', () => {
  it('delivers forced attach geometry before later output', async () => {
    const events: Array<{ event: string; id: string; cols?: number; rows?: number }> = [];
    const dispose = await listenPtyEvents(({ payload }) => events.push(payload));

    await ptyAttach({
      args: {
        id: 'forced-attach-resize',
        cols: 106,
        rows: 42,
        policy: 'fresh_spawn',
      },
      forceResizeBeforeAttach: true,
    });

    expect(events).toEqual([{
      event: 'local_resize',
      id: 'forced-attach-resize',
      cols: 106,
      rows: 42,
    }]);
    dispose();
  });
});
