import { expect, test } from '@playwright/test';

test('fits waiting rows to the available sidebar height and counts only hidden agents', async ({ page }) => {
  await page.setViewportSize({ width: 1200, height: 1000 });
  await page.goto('/test-harness/?component=QueueSidebarFit');
  await page.evaluate(() => window.__HARNESS_READY__);

  const lead = page.locator('.queue-waiting-lead [data-testid^="queue-turn-"]');
  const toggle = page.getByTestId('queue-agents-toggle');
  const counts = page.getByTestId('queue-agents-counts');
  await expect(lead).toHaveCount(5);
  await expect(toggle).toContainText('3 more agents');
  await expect(counts).toHaveText('1 working2 snoozed');

  await page.setViewportSize({ width: 1200, height: 450 });
  await expect(lead).toHaveCount(3);
  await expect(toggle).toContainText('5 more agents');
  await expect(toggle).toBeInViewport();
  await expect(counts).toHaveText('2 waiting1 working2 snoozed');
});

for (const flow of ['queue', 'desktop']) {
  test(`a between-sessions member's start stays clickable beside hover actions in ${flow} flow`, async ({ page }) => {
    await page.goto(`/test-harness/?component=QueueSidebarFit${flow === 'desktop' ? '&desktop' : ''}`);
    await page.evaluate(() => window.__HARNESS_READY__);
    const row = page.getByTestId('queue-crew-birch');
    const sun = page.getByTestId('queue-crew-start-birch');
    await row.hover();
    await sun.click();
    await expect(row).toHaveAttribute('data-crew-start', 'armed');
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('start'))).toEqual([]);
    await sun.click();
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('start'))).toEqual([['birch']]);
    await expect(page.getByRole('menu', { name: 'Actions for Birch' })).toHaveCount(0);
    await row.getByRole('button', { name: 'Actions for Birch' }).click();
    await expect(page.getByRole('menu', { name: 'Actions for Birch' })).toBeVisible();
  });
}

test('scrolls crew between sessions and desktops together in a short desktop window', async ({ page }) => {
  await page.setViewportSize({ width: 1200, height: 500 });
  await page.goto('/test-harness/?component=QueueSidebarFit&desktop&largeCrew');
  await page.evaluate(() => window.__HARNESS_READY__);
  const home = page.getByTestId('sidebar-home');
  await expect(home).toBeInViewport({ ratio: 1 });
  const pinned = await home.boundingBox();
  const lastCrew = page.getByTestId('queue-crew-start-resting-snoozed-2');
  await lastCrew.scrollIntoViewIfNeeded();
  await expect(lastCrew).toBeInViewport({ ratio: 1 });
  await lastCrew.click();
  await lastCrew.click();
  expect(await page.evaluate(() => window.__HARNESS__.getCalls('start'))).toEqual([['resting-snoozed-2']]);
  const session = page.getByTestId('sidebar-session-snoozed-2');
  await session.scrollIntoViewIfNeeded();
  await expect(session).toBeInViewport({ ratio: 1 });
  expect(await home.boundingBox()).toEqual(pinned);
});
