import { expect, test } from '@playwright/test';

test('keeps every bar control on screen at 800px with long profile, waiting and automation names', async ({ page }) => {
  await page.setViewportSize({ width: 800, height: 600 });
  await page.goto('/test-harness/?component=QueueBar');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const controls = [
    'queue-bar-show-sidebar',
    'queue-profile-pill',
    'queue-bar-pill',
    'queue-bar-runs',
    'queue-bar-desktops',
  ];
  for (const testId of controls) {
    const box = (await page.getByTestId(testId).boundingBox())!;
    expect(box.x, testId).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width, testId).toBeLessThanOrEqual(800);
  }
  const truncated = await page.evaluate(() =>
    ['.queue-bar-profile strong', '.queue-bar-crumb', '.critical-strip-text'].map((selector) => {
      const element = document.querySelector(selector)!;
      return element.scrollWidth > element.clientWidth;
    }),
  );
  expect(truncated).toEqual([true, true, true]);

  await page.getByTestId('queue-bar-runs').hover();
  const panel = (await page.locator('[data-testid=queue-bar-runs-peek] .queue-bar-peek-panel').boundingBox())!;
  expect(panel.x).toBeGreaterThanOrEqual(0);
  expect(panel.x + panel.width).toBeLessThanOrEqual(800);
  const name = page.locator('.queue-bar-peek-group-name').first();
  expect(await name.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
  await expect(page.locator('.queue-bar-peek-group-count').first()).toBeInViewport();
});

test('lets the grid cover the bar without moving what sits under it', async ({ page }) => {
  await page.setViewportSize({ width: 800, height: 600 });
  await page.goto('/test-harness/?component=QueueBar');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  const barBefore = (await page.getByTestId('queue-bar').boundingBox())!;

  await page.goto('/test-harness/?component=QueueBar&grid');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  const bar = (await page.getByTestId('queue-bar').boundingBox())!;
  expect(bar).toEqual(barBefore);
  const hit = await page.evaluate(
    ({ x, y }) => document.elementFromPoint(x, y)?.closest('[data-testid]')?.getAttribute('data-testid'),
    { x: bar.x + bar.width / 2, y: bar.y + bar.height / 2 },
  );
  expect(hit).toBe('grid-stand-in');
});
