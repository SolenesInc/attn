import { test, expect } from '@playwright/test';

// The membership flow against the real grid (WebGL renderer + ghostty, mock
// PTY) in the component harness — no daemon, no dev app.

test.describe('Grid membership (remove / restore)', () => {
  test('hover reveals the remove button; remove then restore round-trips', async ({ page }) => {
    await page.goto('/test-harness/?component=GridView');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    // ghostty has to load and the first paint has to settle.
    await page.waitForTimeout(900);

    const viewport = page.viewportSize()!;
    const third = viewport.width / 3;

    await expect(page.locator('.grid-tile-remove')).toHaveCount(0);

    await page.mouse.move(third / 2, viewport.height / 2);
    await expect(page.locator('.grid-tile-remove')).toBeVisible();
    await page.screenshot({ path: 'test-results/grid-hover-remove.png' });

    await page.locator('.grid-tile-remove').click();
    const removeCalls = await page.evaluate(() => window.__HARNESS__.getCalls('onRemove'));
    expect(removeCalls).toEqual([['s1']]);
    await expect(page.locator('.grid-hidden-toggle')).toHaveText('1 hidden');
    await page.screenshot({ path: 'test-results/grid-after-remove.png' });

    await page.locator('.grid-hidden-toggle').click();
    await expect(page.getByText('api server')).toBeVisible();
    await page.screenshot({ path: 'test-results/grid-restore-list.png' });
    await page.getByTitle('Restore api server').click();

    const restoreCalls = await page.evaluate(() => window.__HARNESS__.getCalls('onRestore'));
    expect(restoreCalls).toEqual([['s1']]);
    await expect(page.locator('.grid-hidden-toggle')).toHaveCount(0);
  });
});
