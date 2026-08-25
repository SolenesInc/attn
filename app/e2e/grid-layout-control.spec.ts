import { test, expect } from '@playwright/test';


test.describe('Grid layout picker', () => {
  test('opens, highlights the hovered rectangle, and commits a fixed shape', async ({ page }) => {
    await page.goto('/test-harness/?component=GridLayoutControl');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    await expect(page.locator('.grid-layout-popover')).toHaveCount(0);
    await page.getByRole('button', { name: 'Grid layout' }).click();
    await expect(page.locator('.grid-layout-popover')).toBeVisible();
    await expect(page.locator('.grid-layout-cell')).toHaveCount(25);
    await page.screenshot({ path: 'test-results/grid-picker-open.png' });

    await page.locator('[data-rc="2x3"]').hover();
    await expect(page.locator('.grid-layout-cell.on')).toHaveCount(6);
    await expect(page.locator('.grid-layout-label')).toHaveText('2 × 3');
    await page.screenshot({ path: 'test-results/grid-picker-hover-2x3.png' });

    await page.locator('[data-rc="2x3"]').click();
    await expect(page.locator('.grid-layout-popover')).toHaveCount(0);
    const calls = await page.evaluate(() => window.__HARNESS__.getCalls('onSelect'));
    expect(calls).toHaveLength(1);
    expect(calls[0][0]).toEqual({ mode: 'fixed', rows: 2, cols: 3 });
  });

  test('shows the saved selection at rest and offers Auto', async ({ page }) => {
    await page.goto('/test-harness/?component=GridLayoutControl&mode=fixed');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    await page.getByRole('button', { name: 'Grid layout' }).click();
    await expect(page.locator('.grid-layout-cell.on')).toHaveCount(6);
    await expect(page.locator('.grid-layout-label')).toHaveText('2 × 3');
    await page.screenshot({ path: 'test-results/grid-picker-saved-fixed.png' });

    await page.getByText('Auto', { exact: true }).first().click();
    const calls = await page.evaluate(() => window.__HARNESS__.getCalls('onSelect'));
    expect(calls[calls.length - 1][0]).toEqual({ mode: 'auto' });
  });
});
