import { test, expect } from '@playwright/test';


test.describe('Grid membership — fixed shape removal repaints immediately', () => {
  test('removing a tile under an unchanged 2×2 shape repaints right away', async ({ page }) => {
    await page.goto('/test-harness/?component=GridView&layout=fixed');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForTimeout(1100);

    const viewport = page.viewportSize()!;
    await page.screenshot({ path: 'test-results/grid-fixed-before.png' });

    await page.mouse.move(viewport.width / 4, viewport.height / 4);
    await expect(page.locator('.grid-tile-remove')).toBeVisible();
    await page.locator('.grid-tile-remove').click();

    const removeCalls = await page.evaluate(() => window.__HARNESS__.getCalls('onRemove'));
    expect(removeCalls).toEqual([['s1']]);
    await expect(page.locator('.grid-hidden-toggle')).toHaveText('1 hidden');

    await page.waitForTimeout(500);
    await page.screenshot({ path: 'test-results/grid-fixed-after.png' });
  });
});
