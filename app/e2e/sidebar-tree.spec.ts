import { expect, test, type Page } from '@playwright/test';

test.use({ viewport: { width: 1200, height: 600 }, reducedMotion: 'reduce' });

test.beforeEach(async ({ page }) => {
  await page.goto('/test-harness/?component=SidebarTree');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
});

async function geometry(page: Page) {
  return page.locator('.session-list').evaluate((list) => {
    const row = list.querySelector('[aria-current="true"]')!;
    const rowBox = row.getBoundingClientRect();
    const listBox = list.getBoundingClientRect();
    const desired = list.scrollTop + rowBox.top - listBox.top + rowBox.height / 2 - list.clientHeight / 2;
    return { actual: list.scrollTop, expected: Math.max(0, Math.min(desired, list.scrollHeight - list.clientHeight)) };
  });
}

async function expectCentered(page: Page) {
  const { actual, expected } = await geometry(page);
  expect(Math.abs(actual - expected)).toBeLessThanOrEqual(1);
  await expect(page.locator('.session-list [aria-current="true"]')).toHaveCount(1);
}

async function scrollTop(page: Page) {
  return page.locator('.session-list').evaluate((list) => list.scrollTop);
}

async function scrollAway(page: Page) {
  await page.locator('.session-list').evaluate((list) => { list.scrollTop = 0; });
}

test('centers agent and empty desktop jumps, clamping at the ends', async ({ page }) => {
  for (const action of ['Jump to agent', 'Jump to empty desktop', 'Jump to last', 'Jump to first']) {
    await page.getByRole('button', { name: action, exact: true }).click();
    await expectCentered(page);
  }
});

test('keeps the list still when a row click selection arrives later', async ({ page }) => {
  await page.getByRole('button', { name: 'Jump to agent', exact: true }).click();
  await scrollAway(page);
  const before = await scrollTop(page);
  await page.getByRole('button', { name: 'Open Agent 1.2', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Deliver selection' })).toBeEnabled();
  expect(await scrollTop(page)).toBe(before);
  await page.getByRole('button', { name: 'Deliver selection' }).click();
  await expect(page.getByTestId('sidebar-session-desk-1-agent-2')).toHaveAttribute('aria-current', 'true');
  expect(await scrollTop(page)).toBe(before);
});

test('matches a desktop click to its arriving agent, then centers an external jump', async ({ page }) => {
  await page.getByRole('button', { name: 'Jump to agent', exact: true }).click();
  await scrollAway(page);
  const before = await scrollTop(page);
  await page.getByRole('button', { name: 'Open Desktop 1', exact: true }).click();
  await page.getByRole('button', { name: 'Deliver selection' }).click();
  await expect(page.getByTestId('sidebar-session-desk-1-agent-1')).toHaveAttribute('aria-current', 'true');
  expect(await scrollTop(page)).toBe(before);
  await page.getByRole('button', { name: 'Jump to agent', exact: true }).click();
  await expectCentered(page);
});

test('centers keyboard activation in the list', async ({ page }) => {
  await page.getByRole('button', { name: 'Open Agent 1.4', exact: true }).focus();
  await page.keyboard.press('Enter');
  await page.getByRole('button', { name: 'Deliver selection' }).click();
  await expectCentered(page);
});

test('going Home leaves the list still and clears its reveal target', async ({ page }) => {
  await page.getByRole('button', { name: 'Jump to agent', exact: true }).click();
  const before = await scrollTop(page);
  await page.getByTestId('sidebar-home').click();
  await expect(page.locator('.session-list [aria-current="true"]')).toHaveCount(0);
  await expect(page.locator('.session-list .current, .session-list .selected')).toHaveCount(0);
  expect(await scrollTop(page)).toBe(before);
});

test('respects a scroll-away when another agent appears, but reveals after reorder', async ({ page }) => {
  await page.getByRole('button', { name: 'Jump to agent', exact: true }).click();
  await scrollAway(page);
  const before = await scrollTop(page);
  await page.getByRole('button', { name: 'Add agent above' }).click();
  await expect(page.getByTestId('sidebar-session-new-agent')).toBeVisible();
  expect(await scrollTop(page)).toBe(before);
  await page.getByRole('button', { name: 'Reorder selected to end' }).click();
  await expectCentered(page);
});

test('centers tile jumps and respects delayed tile clicks', async ({ page }) => {
  await page.getByRole('button', { name: 'Add tile', exact: true }).click();
  await page.getByRole('button', { name: 'Jump to tile', exact: true }).click();
  await expectCentered(page);
  await page.getByRole('button', { name: 'Jump to agent', exact: true }).click();
  const before = await scrollTop(page);
  await page.getByTestId('sidebar-tile-desk-7-notes').getByRole('button', { name: /^Open / }).click();
  await page.getByRole('button', { name: 'Deliver selection' }).click();
  await expect(page.getByTestId('sidebar-tile-desk-7-notes')).toHaveAttribute('aria-current', 'true');
  expect(await scrollTop(page)).toBe(before);
});

test('a click on the current desktop cannot suppress a later keyboard selection', async ({ page }) => {
  await page.getByRole('button', { name: 'Jump to agent', exact: true }).click();
  await page.getByRole('button', { name: 'Open Desktop 7', exact: true }).click();
  await page.getByRole('button', { name: 'Deliver selection' }).click();
  await page.getByRole('button', { name: 'Open Agent 7.3', exact: true }).focus();
  await page.keyboard.press('Enter');
  await page.getByRole('button', { name: 'Deliver selection' }).click();
  await expectCentered(page);
});

test('a canceled desktop drag click cannot suppress a later external selection', async ({ page }) => {
  await page.getByRole('button', { name: 'Jump to agent', exact: true }).click();
  await scrollAway(page);
  const source = (await page.locator('.desktop-rule', { has: page.getByRole('button', { name: 'Open Desktop 1', exact: true }) }).boundingBox())!;
  await page.mouse.move(source.x + 24, source.y + source.height / 2);
  await page.mouse.down();
  await page.mouse.move(source.x + 24, source.y + source.height + 12, { steps: 4 });
  await expect(page.locator('.session-list--reordering')).toBeVisible();
  await page.keyboard.press('Escape');
  await page.mouse.move(source.x + 24, source.y + source.height / 2);
  await page.mouse.up();
  await expect(page.getByRole('button', { name: 'Deliver selection' })).toBeDisabled();
  await page.locator('.session-list').evaluate((list) => { list.scrollTop = (list.scrollHeight - list.clientHeight) / 2; });
  await page.getByRole('button', { name: 'Jump to first', exact: true }).click();
  await expectCentered(page);
});

for (const rail of [false, true]) {
  test(`a desktop click from Home keeps the ${rail ? 'rail' : 'tree'} still through the intermediate selection`, async ({ page }) => {
    await page.setViewportSize({ width: 1200, height: 400 });
    await page.goto(`/test-harness/?component=SidebarTree${rail ? '&rail' : ''}`);
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const list = page.locator(rail ? '.rail-desktops' : '.session-list');
    await page.getByRole('button', { name: 'Jump to last', exact: true }).click();
    await (rail ? page.getByRole('button', { name: 'Home', exact: true }) : page.getByTestId('sidebar-home')).click();
    await list.evaluate((element) => { element.scrollTop = 0; });
    const before = await list.evaluate((element) => element.scrollTop);
    await list.getByRole('button', { name: rail ? 'Desktop 1 (⌘1)' : 'Open Desktop 1', exact: true }).click();
    expect(await list.evaluate((element) => element.scrollTop)).toBe(before);
    await page.getByRole('button', { name: 'Deliver selection' }).click();
    expect(await list.evaluate((element) => element.scrollTop)).toBe(before);
    await expect(list.locator('[aria-current="true"]')).toHaveCount(1);
  });
}
