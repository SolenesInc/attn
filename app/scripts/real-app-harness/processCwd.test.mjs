import { describe, expect, it } from 'vitest';
import { processCwd } from './processCwd.mjs';

describe('processCwd', () => {
  it('reads the working directory of a live process', () => {
    expect(processCwd(process.pid)).toBe(process.cwd());
  });
});
