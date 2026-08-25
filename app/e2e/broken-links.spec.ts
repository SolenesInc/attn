import { test, expect } from '@playwright/test';

// CM cannot mount under happy-dom, and the async existence-check → StateEffect → repaint path needs a real browser.

test.describe('LiveMarkdownEditor broken links', () => {
  test('flags a link to a missing note, leaves real and external links alone', async ({ page }) => {
    await page.goto('/test-harness/?component=BrokenLinks');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const broken = page.locator('.cm-md-link-broken');
    await expect(broken).toHaveCount(1);
    await expect(broken).toContainText('ghost');

    const real = page.locator('.cm-md-link', { hasText: 'real' });
    await expect(real).toBeVisible();
    await expect(real).not.toHaveClass(/cm-md-link-broken/);

    const color = await broken.evaluate((el) => getComputedStyle(el).color);
    const realColor = await real.evaluate((el) => getComputedStyle(el).color);
    expect(color).not.toBe(realColor);

    const checked = await page.evaluate(() => window.__HARNESS__.getCalls('existsFile').map((c) => c[0]));
    expect(checked).toContain('knowledge/areas/missing.md');
    expect(checked).toContain('knowledge/areas/real.md');
    expect(checked.some((p: string) => p.includes('example.com'))).toBe(false);

    await page.screenshot({ path: 'test-results/broken-links.png' });
  });

  test('checks and flags a newly-added broken link when the document changes', async ({ page }) => {
    await page.goto('/test-harness/?component=BrokenLinks');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');
    await expect(page.locator('.cm-md-link-broken')).toHaveCount(1);

    await page.getByTestId('add-missing-link').click();
    await expect(page.locator('.cm-md-link-broken')).toHaveCount(2);

    const checked = await page.evaluate(() => window.__HARNESS__.getCalls('existsFile').map((c) => c[0]));
    expect(checked).toContain('knowledge/areas/missing-extra.md');
  });
});
