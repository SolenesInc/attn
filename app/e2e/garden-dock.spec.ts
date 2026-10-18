import { test, expect } from './fixtures';

test('the docked garden opens beside a drawer that is already open', async ({ page, daemon }) => {
  await daemon.start();
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  await page.keyboard.press('Meta+Shift+p');
  const drawer = page.locator('.side-panel-shell.is-open .attention-drawer');
  await expect(drawer).toBeVisible();
  await page.getByRole('button', { name: 'Show the garden' }).click();
  const garden = page.getByTestId('garden-frame');
  await expect(garden).toHaveClass(/is-dock/);

  await expect.poll(async () => {
    const [gardenBox, drawerBox] = await Promise.all([garden.boundingBox(), drawer.boundingBox()]);
    return gardenBox && drawerBox ? Math.round(gardenBox.x + gardenBox.width - drawerBox.x) : null;
  }).toBe(0);
});
