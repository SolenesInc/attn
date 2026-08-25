import { test, expect } from '@playwright/test';

test.describe('NotebookTile (workspace tile)', () => {
  test('renders the live surface and folds its rail then tree as it narrows', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookTile');

    const editor = page.locator('.cm-editor');
    await expect(editor).toBeVisible();
    await expect(page.locator('.notebook-browser-list')).toBeVisible();

    const body = page.locator('.notebook-browser-body');
    await expect(body).toHaveClass(/has-rail/);
    await expect(body).not.toHaveClass(/tree-folded/);
    await expect(body).not.toHaveClass(/rail-folded/);

    await page.evaluate(() => window.__setTileWidth?.(760));
    await expect(body).toHaveClass(/rail-folded/);
    await expect(body).not.toHaveClass(/tree-folded/);

    await page.evaluate(() => window.__setTileWidth?.(520));
    await expect(body).toHaveClass(/tree-folded/);
    await expect(body).toHaveClass(/rail-folded/);
    // Polled past the 180ms fold transition for the settled width.
    await expect(page.locator('.notebook-browser-list')).toHaveCount(1);
    await expect.poll(
      () => page.locator('.notebook-browser-list').evaluate((el) => el.getBoundingClientRect().width),
    ).toBeLessThan(2);

    await page.evaluate(() => window.__setTileWidth?.(1100));
    await expect(body).not.toHaveClass(/tree-folded/);
    await expect(body).not.toHaveClass(/rail-folded/);
  });
});

declare global {
  interface Window {
    __setTileWidth?: (px: number) => void;
  }
}
