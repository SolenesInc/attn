import { expect, test } from '@playwright/test';

for (const [name, options, width] of [
  ['seed and identity', 'seed&role&usage', 1280],
  ['identity without seed', 'role&usage', 1280],
  ['seed and provenance in split panes', 'seed&role&usage&provenance&split', 1280],
  ['narrow header', 'seed&role&usage', 480],
  ['role without usage', 'seed&role', 760],
  ['usage without role', 'seed&usage', 760],
] as const) {
  test(`vertically centers ${name}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 560 });
    await page.goto(`/test-harness/?component=AgentHeader&${options}`);
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const headers = page.locator('.workspace-pane-header');
    await expect(headers).toHaveCount(options.includes('split') ? 2 : 1);
    for (const header of await headers.all()) {
      await expect(header.locator('.workspace-pane-title')).toBeVisible();
      await expect(header.locator('.pane-seed-chip')).toHaveCount(options.includes('seed') ? 1 : 0);
      await expect(header.locator('.delegation-chain-trigger')).toHaveCount(options.includes('role') ? 1 : 0);
      await expect(header.locator('.workspace-pane-usage')).toHaveCount(options.includes('usage') ? 1 : 0);
      const centers = await header.evaluate((element) => {
        const center = (item: Element) => {
          const bounds = item.getBoundingClientRect();
          return Math.round(bounds.y + bounds.height / 2);
        };
        return {
          headline: Array.from(element.querySelector('.workspace-pane-identity-main')!.children, center),
          groups: Array.from(element.children, center),
        };
      });
      expect(new Set(centers.headline).size, JSON.stringify(centers)).toBe(1);
      expect(new Set(centers.groups).size, JSON.stringify(centers)).toBe(1);
    }
  });
}
