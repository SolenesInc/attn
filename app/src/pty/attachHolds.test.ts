import { describe, expect, it } from 'vitest';
import { createAttachHolds } from './attachHolds';

describe('attachHolds', () => {
  it('counts distinct holders per runtime and reports who still holds', () => {
    const holds = createAttachHolds();
    const first = {};
    const second = {};

    holds.hold('runtime-1', first);
    holds.hold('runtime-1', first);
    holds.hold('runtime-1', second);
    expect(holds.holderCount('runtime-1')).toBe(2);
    expect(holds.holds('runtime-1', first)).toBe(true);

    expect(holds.release('runtime-1', first)).toBe(1);
    expect(holds.holds('runtime-1', first)).toBe(false);
    expect(holds.holds('runtime-1', second)).toBe(true);
    expect(holds.release('runtime-1', second)).toBe(0);
    expect(holds.holderCount('runtime-1')).toBe(0);
  });

  it('ignores a release from a holder that never held the runtime', () => {
    const holds = createAttachHolds();
    const holder = {};
    holds.hold('runtime-1', holder);

    expect(holds.release('runtime-1', {})).toBe(1);
    expect(holds.release('runtime-2', holder)).toBe(0);
    expect(holds.holds('runtime-1', holder)).toBe(true);
  });
});
