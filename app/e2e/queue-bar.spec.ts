import { expect, test } from '@playwright/test';

test('fits the session names from the queue popup and stays within a narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/test-harness/?component=QueueBar&popupNames&oneDesktop');
  await page.evaluate(() => window.__HARNESS_READY__);
  await page.getByTestId('queue-bar-waiting').hover();

  const names = page.locator('[data-testid=queue-bar-waiting-peek] .unified-palette-name');
  await expect(names).toHaveCount(16);
  expect(await names.evaluateAll((elements) => elements.every((element) => element.scrollWidth <= element.clientWidth))).toBe(true);
  await expect(page.getByTestId('queue-bar-pill')).not.toHaveAttribute('title');

  for (const width of [800, 520]) {
    await page.mouse.move(0, 400);
    await page.setViewportSize({ width, height: 900 });
    await page.getByTestId('queue-bar-waiting').hover();
    const panel = (await page.locator('[data-testid=queue-bar-waiting-peek] .queue-bar-peek-panel').boundingBox())!;
    expect(panel.x).toBeGreaterThanOrEqual(0);
    expect(panel.x + panel.width).toBeLessThanOrEqual(width);
  }
});

test('keeps every bar control on screen at 800px with long profile, waiting and automation names', async ({ page }) => {
  await page.setViewportSize({ width: 800, height: 600 });
  await page.goto('/test-harness/?component=QueueBar');
  await page.evaluate(() => window.__HARNESS_READY__);

  const controls = [
    'queue-bar-show-sidebar',
    'queue-profile-pill',
    'queue-bar-pill',
    'queue-bar-runs',
    'queue-bar-desktops',
  ];
  for (const testId of controls) {
    const box = (await page.getByTestId(testId).boundingBox())!;
    expect(box.x, testId).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width, testId).toBeLessThanOrEqual(800);
  }
  const truncated = await page.evaluate(() =>
    ['.queue-bar-profile strong', '.queue-bar-crumb', '.critical-strip-text'].map((selector) => {
      const element = document.querySelector(selector)!;
      return element.scrollWidth > element.clientWidth;
    }),
  );
  expect(truncated).toEqual([true, true, true]);

  await page.getByTestId('queue-bar-runs').hover();
  const panel = (await page.locator('[data-testid=queue-bar-runs-peek] .queue-bar-peek-panel').boundingBox())!;
  expect(panel.x).toBeGreaterThanOrEqual(0);
  expect(panel.x + panel.width).toBeLessThanOrEqual(800);
  const name = page.locator('.queue-bar-peek-group-name').first();
  expect(await name.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
  await expect(page.locator('.queue-bar-peek-group-count').first()).toBeInViewport();
});

test('keeps both peeks on screen when a long profile pushes the waiting pill right on a single desktop', async ({ page }) => {
  await page.setViewportSize({ width: 800, height: 600 });
  await page.goto('/test-harness/?component=QueueBar&oneDesktop');
  await page.evaluate(() => window.__HARNESS_READY__);

  const anchor = (await page.getByTestId('queue-bar-waiting').boundingBox())!;
  expect(anchor.x + 420).toBeGreaterThan(800);

  for (const [chip, peek] of [['queue-bar-waiting', 'queue-bar-waiting-peek'], ['queue-bar-runs', 'queue-bar-runs-peek']]) {
    await page.getByTestId(chip).hover();
    const panel = (await page.locator(`[data-testid=${peek}] .queue-bar-peek-panel`).boundingBox())!;
    expect(panel.x, peek).toBeGreaterThanOrEqual(0);
    expect(panel.x + panel.width, peek).toBeLessThanOrEqual(800);
    await page.mouse.move(400, 500);
    await expect(page.getByTestId(peek)).toBeHidden();
  }
});

test('an open peek shifts back on screen when the window narrows under it', async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 600 });
  await page.goto('/test-harness/?component=QueueBar&oneDesktop');
  await page.evaluate(() => window.__HARNESS_READY__);

  await page.getByTestId('queue-bar-waiting').hover();
  const panel = page.locator('[data-testid=queue-bar-waiting-peek] .queue-bar-peek-panel');
  const wide = (await panel.boundingBox())!;
  const pointerX = 750;
  expect(pointerX).toBeGreaterThan(wide.x);
  await page.mouse.move(pointerX, wide.y + 20);

  await page.setViewportSize({ width: 800, height: 600 });
  await expect(panel).toBeVisible();
  await expect.poll(async () => {
    const box = (await panel.boundingBox())!;
    return box.x + box.width;
  }).toBeLessThanOrEqual(800);
});

test('an open peek shifts back on screen when the UI scale grows under it', async ({ page }) => {
  await page.setViewportSize({ width: 800, height: 600 });
  await page.goto('/test-harness/?component=QueueBar&oneDesktop');
  await page.evaluate(() => window.__HARNESS_READY__);

  await page.getByTestId('queue-bar-waiting').hover();
  const panel = page.locator('[data-testid=queue-bar-waiting-peek] .queue-bar-peek-panel');
  const before = (await panel.boundingBox())!;
  await page.mouse.move(before.x + before.width / 2, before.y + 20);

  await page.evaluate(() => document.documentElement.style.setProperty('--ui-scale', '1.6'));
  await expect(panel).toBeVisible();
  await expect.poll(async () => (await page.getByTestId('queue-bar-waiting').boundingBox())!.x).toBeGreaterThan(before.x);
  await expect.poll(async () => {
    const box = (await panel.boundingBox())!;
    return box.x + box.width;
  }).toBeLessThanOrEqual(800);
});

test('the shelf leaves every desktop button clickable in a crowded bar at the smallest window', async ({ page }) => {
  await page.setViewportSize({ width: 800, height: 600 });
  await page.goto('/test-harness/?component=QueueBar&popupNames&desktops=8&shelf=1,1,1');
  await page.evaluate(() => window.__HARNESS_READY__);
  await expect(page.getByTestId('shelf-cap-before')).toBeVisible();
  await expect(page.getByTestId('shelf-cap-after')).toBeVisible();

  const clickCentre = async (slot: number) => {
    const button = page.getByTestId(`queue-bar-desktop-${slot}`);
    const box = (await button.boundingBox())!;
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
    await expect(button).toHaveClass(/is-current/);
  };
  for (const slot of [2, 3, 4, 5, 6, 7, 8, 1]) {
    await clickCentre(slot);
    await expect(page.getByTestId('shelf-menu-after')).toBeHidden();
    await expect(page.getByTestId('shelf-menu-before')).toBeHidden();
    if (slot !== 1) await clickCentre(1);
  }

  const shelf = (await page.getByTestId('desktop-shelf').boundingBox())!;
  const firstDesktop = (await page.getByTestId('queue-bar-desktop-1').boundingBox())!;
  for (const control of ['shelf-cap-before', 'shelf-live-dots', 'shelf-cap-after']) {
    const box = (await page.getByTestId(control).boundingBox())!;
    expect(box.x, control).toBeGreaterThanOrEqual(shelf.x);
    expect(box.x + box.width, control).toBeLessThanOrEqual(Math.min(shelf.x + shelf.width, firstDesktop.x));
  }
  expect((await page.getByTestId('queue-bar-pill').boundingBox())!.width).toBeGreaterThan(0);

  await page.getByTestId('shelf-cap-after').click();
  await expect(page.getByTestId('shelf-menu-after')).toBeVisible();
});
