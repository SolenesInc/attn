import { test, expect } from '@playwright/test';
test.describe('group', () => {
  test('passes', async () => { expect(1).toBe(1); });
  test('fails', async () => { expect(1).toBe(2); });
  test('skips', async () => { test.skip(true, 'needs a display'); });
  test.fixme('is fixme', async () => {});
  test('fails as expected', async () => { test.fail(); expect(1).toBe(2); });
  test('expected to fail but passes', async () => { test.fail(); });
  test('flaky', async ({}, info) => { expect(info.retry).toBe(1); });
  test('times out', async () => { test.setTimeout(200); await new Promise(r => setTimeout(r, 1000)); });
  test('expected to fail but times out', async () => { test.fail(); test.setTimeout(200); await new Promise(r => setTimeout(r, 1000)); });
});
