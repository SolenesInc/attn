import { test, expect } from '@playwright/test';

// The receipt behind SETTLED_READ_FRAMES: a real browser, because the gap only exists where
// layout, the ResizeObserver measuring it and React's commit are separate steps of a frame.
test('a settled bridge read carries the commit its frames were waiting for', async ({ page }) => {
  await page.goto('/test-harness/?component=BridgeSettledRead');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await expect(page.locator('[data-testid="committed-width"]')).toHaveText('200');

  // Every read starts from the same DOM change and the same frame, so they differ only in
  // where they land: inside a frame callback, or past the commit a later frame carries.
  const reads = await page.evaluate(async () => {
    const harness = window.__SETTLE_HARNESS__;
    harness.resize(520);
    const [oneFrame, oneFrameOneTask, settled] = await Promise.all([
      harness.readAfter(1, 0),
      harness.readAfter(1, 1),
      harness.readSettled(),
    ]);
    return { oneFrame, oneFrameOneTask, settled };
  });

  expect(reads.oneFrame, 'a read inside the frame callback reports a width no frame painted').toBe(200);
  expect(reads.oneFrameOneTask, 'one frame lands before the observer-driven React commit').toBe(200);
  expect(reads.settled, 'a settled read reports the committed width').toBe(520);
});
