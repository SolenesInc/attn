import { expect, test } from '@playwright/test';

test('keeps the compact rail controls usable with eight desktops', async ({ page }) => {
  await page.setViewportSize({ width: 1200, height: 900 });
  await page.goto('/test-harness/?component=SidebarRail');
  await page.evaluate(() => window.__HARNESS_READY__);
  const rail = page.locator('.sidebar.collapsed');
  expect((await rail.boundingBox())!.width).toBe(44);
  const buttons = rail.locator('button');
  for (const button of await buttons.all()) {
    const box = (await button.boundingBox())!;
    expect(box.width).toBe(30);
    expect(box.height).toBe(30);
    await expect(button).toBeInViewport();
  }
  const second = page.getByRole('button', { name: 'Queue polish (⌘2)' });
  await second.hover();
  await second.click();
  await expect(second).toHaveAttribute('aria-current', 'true');
  await page.getByRole('button', { name: 'Sketches (⌘8)' }).focus();
  await page.keyboard.press('Enter');
  expect(await page.evaluate(() => window.__HARNESS__.getCalls('desktop'))).toEqual([['desk-2'], ['desk-8']]);
});

test('reveals the same current desktop after an arrangement update moves it to the end', async ({ page }) => {
  await page.setViewportSize({ width: 1200, height: 300 });
  await page.goto('/test-harness/?component=SidebarRail&update=reorder');
  await page.evaluate(() => window.__HARNESS_READY__);
  const current = page.getByRole('button', { name: 'Sidebar refinement (⌘1)' });
  await expect(current).toHaveAttribute('aria-current', 'true');
  await expect(current).toBeInViewport();
  await page.evaluate(() => window.__HARNESS__.triggerRerender());
  const reordered = page.getByRole('button', { name: 'Sidebar refinement (⌘8)' });
  await expect(reordered).toHaveAttribute('aria-current', 'true');
  await expect(reordered).toBeInViewport();
  expect(await page.evaluate(() => window.__HARNESS__.getCalls('desktop'))).toEqual([]);
});

test('keeps the current desktop visible when the window shrinks', async ({ page }) => {
  await page.setViewportSize({ width: 1200, height: 900 });
  await page.goto('/test-harness/?component=SidebarRail');
  await page.evaluate(() => window.__HARNESS_READY__);
  await page.evaluate(() => window.__HARNESS__.triggerRerender());
  const current = page.getByRole('button', { name: 'Sketches (⌘8)' });
  await expect(current).toHaveAttribute('aria-current', 'true');
  await expect(current).toBeInViewport({ ratio: 1 });
  await page.setViewportSize({ width: 1200, height: 300 });
  await expect(current).toBeInViewport({ ratio: 1 });
  await expect(current).toHaveAttribute('aria-current', 'true');
  expect(await page.evaluate(() => window.__HARNESS__.getCalls('desktop'))).toEqual([]);
});

test('pins Home at 300px while scrolling desktops and revealing the current one', async ({ page }) => {
  await page.setViewportSize({ width: 1200, height: 300 });
  await page.goto('/test-harness/?component=SidebarRail');
  await page.evaluate(() => window.__HARNESS_READY__);
  const home = page.getByRole('button', { name: 'Home' });
  await expect(home).toBeInViewport();
  const pinned = (await home.boundingBox())!;
  const last = page.getByRole('button', { name: 'Sketches (⌘8)' });
  await expect(last).not.toBeInViewport({ ratio: 1 });
  await page.getByRole('button', { name: 'Sidebar refinement (⌘1)' }).hover();
  await page.mouse.wheel(0, 600);
  await expect(last).toBeInViewport({ ratio: 1 });
  await last.click();
  await expect(last).toHaveAttribute('aria-current', 'true');
  expect(await home.boundingBox()).toEqual(pinned);

  await page.reload();
  await page.evaluate(() => window.__HARNESS_READY__);
  await page.evaluate(() => window.__HARNESS__.triggerRerender());
  await expect(last).toHaveAttribute('aria-current', 'true');
  await expect(last).toBeInViewport({ ratio: 1 });
});
