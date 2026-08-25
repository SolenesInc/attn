import { describe, expect, it } from 'vitest';
import { staticStateEmphasis } from './WebGlGridRenderer';

describe('static state emphasis', () => {
  it('keeps waiting input more prominent than scheduled work without a repaint clock', () => {
    const waiting = staticStateEmphasis('waiting_input');
    const scheduled = staticStateEmphasis('scheduled');

    expect(waiting).toBe(1);
    expect(scheduled).toBe(0.5);
    expect(waiting).toBeGreaterThan(scheduled);
    expect(staticStateEmphasis('working')).toBe(0);
  });
});
