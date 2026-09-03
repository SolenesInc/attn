import { expect, test } from '@playwright/test';

test.beforeEach(async ({ page }) => {
  await page.goto('/test-harness/?component=SeedHeader');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
});

test('lifecycle remains visible when the agent releases its seed, with a useful keyboard preview', async ({ page }) => {
  const chip = page.getByTestId('seed-chip-garden-agent');
  for (const state of ['dormant', 'harvested', 'withered', 'planted', 'growing']) {
    await page.getByRole('button', { name: state, exact: true }).click();
    await expect(chip).toHaveAttribute('data-status', state);
    await expect(chip.locator('.seed-state-label')).toHaveText(new RegExp(state, 'i'));
  }
  await chip.focus();
  await page.keyboard.press('ArrowDown');
  const preview = page.getByRole('dialog', { name: 'Seed context' });
  await expect(preview).toContainText('The silhouettes work at 24px.');
  await expect(preview).toContainText('This agent reports to this seed.');
  await page.keyboard.press('Escape');
  await expect(preview).toHaveCount(0);
  await expect(chip).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('opened')).toHaveText('s-growing');
});

test('hover motion finishes and context fits a narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 380, height: 560 });
  await page.getByTestId('seed-header').evaluate((element) => { element.style.width = '300px'; });
  const chip = page.getByTestId('seed-chip-garden-agent');
  await expect.poll(() => page.evaluate(() => document.getAnimations().length)).toBe(0);
  await chip.hover();
  await expect.poll(() => chip.evaluate((element) => element.getAnimations({ subtree: true }).filter((animation) => animation.playState === 'running').length)).toBeGreaterThan(0);
  const preview = page.getByRole('dialog');
  await expect(preview).toContainText('The silhouettes work at 24px.');
  const bounds = await preview.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(8);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(372);
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(552);
  await preview.hover();
  await expect(preview).toBeVisible();
  await expect.poll(() => page.evaluate(() => document.getAnimations().length)).toBe(0);
  await preview.getByRole('button', { name: /Open seed/ }).click();
  await expect(page.getByTestId('opened')).toHaveText('s-growing');
});

test('reduced motion keeps every state still, including on hover', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  for (const state of ['planted', 'growing', 'dormant', 'harvested', 'withered']) {
    await page.getByRole('button', { name: state, exact: true }).click();
    await page.getByTestId('seed-chip-garden-agent').hover();
    expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
  }
});

test('plot counts stay distinct and arrow keys open the selected seed', async ({ page }) => {
  await page.getByTestId('seed-chip-plot').click();
  const list = page.getByRole('listbox');
  await expect(list).toContainText('Useful previews');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('opened')).toHaveText('s-garden');
});

test('pinning an already visible preview preserves its placement', async ({ page }) => {
  const chip = page.getByTestId('seed-chip-plot');
  await chip.hover();
  const preview = page.getByRole('listbox');
  await expect(preview).toBeVisible();
  const before = await preview.boundingBox();
  await chip.click();
  await expect(preview).toBeFocused();
  await expect.poll(async () => (await preview.boundingBox())?.x).toBe(before!.x);
  await page.setViewportSize({ width: 380, height: 560 });
  await expect.poll(async () => {
    const bounds = await preview.boundingBox();
    return bounds!.x + bounds!.width;
  }).toBeLessThanOrEqual(372);
});
