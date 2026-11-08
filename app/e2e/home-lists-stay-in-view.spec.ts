import { test, expect } from './fixtures';

test('Home lists return to their left edge after a sideways displacement', async ({ page, daemon }) => {
  await daemon.start();
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const lists = page.locator('.card-body');
  await expect(lists).toHaveCount(2);

  for (const list of await lists.all()) {
    const displaced = await list.evaluate((element) => {
      element.scrollLeft = 500;
      return element.scrollLeft;
    });
    expect(displaced).toBeGreaterThan(0);
    await expect.poll(() => list.evaluate((element) => element.scrollLeft)).toBe(0);
  }
});
