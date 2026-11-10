import { expect, test } from '@playwright/test';

for (const [width, crowded] of [[360, true], [1600, false]] as const) {
  test(`the review key line keeps "? all keys" whole at its right edge at ${width}px`, async ({ page }) => {
    await page.goto(`/test-harness/?component=ReviewKeyLine&width=${width}`);
    const line = page.getByLabel('Review keys');
    const all = line.locator('.review-key-line-all');
    await expect(all).toHaveText('? all keys');
    const lineBox = (await line.boundingBox())!;
    const allBox = (await all.boundingBox())!;
    expect(allBox.x).toBeGreaterThanOrEqual(lineBox.x);
    expect(allBox.x + allBox.width).toBeLessThanOrEqual(lineBox.x + lineBox.width);
    expect(await all.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    const entries = line.locator('.review-key-line-entries');
    const entriesBox = (await entries.boundingBox())!;
    expect(entriesBox.x + entriesBox.width).toBeLessThanOrEqual(allBox.x);
    expect(await entries.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(crowded);
  });
}
