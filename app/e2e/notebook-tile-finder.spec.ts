import { test, expect } from '@playwright/test';

test.describe('NotebookTile finder', () => {
  test('Cmd+P opens the finder; typing filters; Enter opens the note', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookTile');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    await expect(page.locator('.cm-editor')).toBeVisible();
    await expect(page.locator('.notebook-finder')).toHaveCount(0);

    await page.locator('.notebook-surface-tile').click();
    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();
    await expect(page.locator('.notebook-finder-input')).toBeFocused();

    await expect(page.locator('.notebook-finder-option')).toHaveCount(2);

    await page.locator('.notebook-finder-input').fill('journal');
    await expect(page.locator('.notebook-finder-option')).toHaveCount(1);
    await expect(page.locator('.notebook-finder-option-path')).toHaveText('journal/2026-06-20.md');

    await page.keyboard.press('Enter');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    const opened = await page.evaluate(() => window.__HARNESS__.getCalls('openFile').map((c) => c[0]));
    expect(opened).toContain('journal/2026-06-20.md');
  });

  test('Escape dismisses the finder back to the note', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookTile');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await expect(page.locator('.cm-editor')).toBeVisible();

    await page.locator('.notebook-surface-tile').click();
    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await expect(page.locator('.cm-editor')).toBeVisible();
  });

  test('Cmd+P re-summons the finder after Esc without re-focusing the tile', async ({ page }) => {
    // Focus must stay inside the tile after Esc: on <body> the tile-scoped keydown never
    // fires and Cmd+P cannot re-summon.
    await page.goto('/test-harness/?component=NotebookTile&initialPath=');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    await expect(page.locator('.notebook-finder')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);

    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();
  });

  test('a fresh tile (no seed) auto-opens the finder on its empty screen', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookTile&initialPath=');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    await expect(page.locator('.notebook-finder')).toBeVisible();
    await expect(page.locator('.notebook-finder-input')).toBeFocused();

    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await expect(page.getByText('Nothing selected')).toBeVisible();
    const reopen = page.locator('.notebook-finder-open-button');
    await expect(reopen).toBeVisible();

    await reopen.click();
    await expect(page.locator('.notebook-finder')).toBeVisible();
  });
});
