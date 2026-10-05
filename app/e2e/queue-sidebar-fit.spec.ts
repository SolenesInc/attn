import { expect, test } from '@playwright/test';

test('fits waiting rows to the available sidebar height and counts only hidden agents', async ({ page }) => {
  await page.setViewportSize({ width: 1200, height: 1000 });
  await page.goto('/test-harness/?component=QueueSidebarFit');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const lead = page.locator('.queue-waiting-lead [data-testid^="queue-turn-"]');
  const toggle = page.getByTestId('queue-agents-toggle');
  const counts = page.getByTestId('queue-agents-counts');
  await expect(lead).toHaveCount(5);
  await expect(toggle).toContainText('3 more agents');
  await expect(counts).toHaveText('1 working2 snoozed');

  await page.setViewportSize({ width: 1200, height: 500 });
  await expect(lead).toHaveCount(3);
  await expect(toggle).toContainText('5 more agents');
  await expect(toggle).toBeInViewport();
  await expect(counts).toHaveText('2 waiting1 working2 snoozed');
});

for (const flow of ['queue', 'desktop']) {
  test(`sleeping crew wake remains clickable beside hover actions in ${flow} flow`, async ({ page }) => {
    await page.goto(`/test-harness/?component=QueueSidebarFit${flow === 'desktop' ? '&desktop' : ''}`);
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const row = page.getByTestId('queue-crew-birch');
    const sun = page.getByTestId('queue-crew-wake-birch');
    await row.hover();
    await sun.click();
    await expect(row).toHaveAttribute('data-crew-wake', 'armed');
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('wake'))).toEqual([]);
    await sun.click();
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('wake'))).toEqual([['birch']]);
    await expect(page.getByRole('menu', { name: 'Actions for Birch' })).toHaveCount(0);
    await row.getByRole('button', { name: 'Actions for Birch' }).click();
    await expect(page.getByRole('menu', { name: 'Actions for Birch' })).toBeVisible();
  });
}
