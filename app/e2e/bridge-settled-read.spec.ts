import { test, expect } from '@playwright/test';

test('DOM expectations follow insertion, focus and removal events', async ({ page }) => {
  await page.goto('/test-harness/?component=BridgeSettledRead');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  const result = await page.evaluate(async () => {
    const wait = window.__SETTLE_HARNESS__.waitForDom;
    const focused = wait({ selector: '#late-input:focus', timeoutMs: 10000 });
    const input = document.createElement('input');
    input.id = 'late-input';
    document.body.append(input);
    await Promise.resolve();
    input.focus();
    const focusResult = await focused;
    const removed = wait({ selector: '#late-input', absent: true, timeoutMs: 10000 });
    input.remove();
    return { focused: focusResult.matched, removed: (await removed).matched };
  });
  expect(result).toEqual({ focused: true, removed: true });
});

test('a settled bridge read carries the commit its frames were waiting for', async ({ page }) => {
  await page.goto('/test-harness/?component=BridgeSettledRead');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await expect(page.locator('[data-testid="committed-width"]')).toHaveText('200');

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
