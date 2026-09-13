import { expect, test } from '@playwright/test';

test.beforeEach(async ({ page }) => {
  await page.goto('/test-harness/?component=DelegationChain');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
});

for (const [platform, shortcut] of [['MacIntel', 'Meta+k'], ['Linux x86_64', 'Control+Shift+k']]) {
  test(`Action menu hands focus to the chain, arrows and Enter open an agent, Escape restores the terminal (${platform})`, async ({ page }) => {
    await page.addInitScript((platform) => {
      Object.defineProperty(navigator, 'platform', { value: platform, configurable: true });
      Object.defineProperty(navigator, 'userAgent', { value: platform, configurable: true });
    }, platform);
    await page.reload();
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const terminal = page.getByRole('textbox', { name: 'Terminal keyboard target' });
    await terminal.focus();
    await page.getByTestId('row-builder').getByRole('button').hover();
    await expect(page.getByRole('dialog', { name: 'Delegation chain' })).toBeVisible();
    await page.keyboard.press(shortcut);
    await page.getByRole('textbox', { name: 'Search actions' }).fill('delegation chain');
    await page.keyboard.press('Enter');
    const popup = page.getByRole('dialog', { name: 'Delegation chain' });
    await expect(popup.getByRole('button', { name: /Build chain navigator/ })).toBeFocused();
    await expect(page.getByRole('dialog', { name: 'Action menu', exact: true })).toHaveCount(0);
    await page.keyboard.press('ArrowUp');
    await expect(popup.getByRole('button', { name: /Coordinate role identity/ })).toBeFocused();
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('ArrowDown');
    await expect(popup.getByRole('button', { name: /Check keyboard flow/ })).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('selected-agent')).toHaveText('child');
    await expect(popup).toHaveCount(0);
    await expect(terminal).toBeFocused();
    await page.keyboard.press(shortcut);
    await page.getByRole('textbox', { name: 'Search actions' }).fill('delegation chain');
    await page.keyboard.press('Enter');
    await expect(popup.getByRole('button', { name: /Check keyboard flow/ })).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(popup).toHaveCount(0);
    await expect(terminal).toBeFocused();
  });
}

test('hover remains reachable without stealing focus or lighting other rows; click pins and Tab stays inside', async ({ page }) => {
  const terminal = page.getByRole('textbox', { name: 'Terminal keyboard target' });
  await terminal.focus();
  const trigger = page.getByTestId('row-builder').getByRole('button');
  await trigger.hover();
  const popup = page.getByRole('dialog', { name: 'Delegation chain' });
  await expect(popup).toBeVisible();
  await expect(terminal).toBeFocused();
  await expect(page.locator('.kin-up, .kin-down')).toHaveCount(0);
  await popup.hover();
  await expect(popup).toBeVisible();
  await expect(terminal).toBeFocused();
  await trigger.click();
  await expect(popup.getByRole('button', { name: /Build chain navigator/ })).toBeFocused();
  await terminal.hover();
  await expect(popup).toBeVisible();
  await page.keyboard.press('End');
  await page.keyboard.press('Tab');
  await expect(popup.getByRole('button', { name: 'Close delegation chain' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(popup).toHaveCount(0);
  await expect(trigger).toBeFocused();
});

test('header names the role; the popup fits a narrow viewport and has no idle animations', async ({ page }) => {
  await page.setViewportSize({ width: 380, height: 560 });
  const header = page.getByTestId('agent-header').getByRole('button');
  await expect(header).toHaveText('Builder');
  await header.click();
  const popup = page.getByRole('dialog', { name: 'Delegation chain' });
  await expect(popup).toBeVisible();
  const bounds = await popup.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(8);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(372);
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(552);
  expect(await popup.evaluate((element) => element.getAnimations({ subtree: true }).length)).toBe(0);
});
