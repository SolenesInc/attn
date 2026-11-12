import { expect, test, type Locator, type Page } from '@playwright/test';

interface Box { top: number; bottom: number }

async function boxOf(locator: Locator): Promise<Box> {
  return locator.evaluate((element) => {
    const { top, bottom } = element.getBoundingClientRect();
    return { top, bottom };
  });
}

async function open(page: Page, frame: string) {
  await page.goto(`/test-harness/?component=ReviewStepping&frame=${frame}&width=720&height=480`);
  const tile = page.getByRole('region', { name: 'Review tile' });
  const view = tile.locator('.review-doc');
  await expect(view.locator('[data-review-stop-head]')).toHaveCount(3);
  await tile.focus();
  return { tile, view };
}

async function expectAtTop(view: Locator, block: Locator) {
  const [scroller, first] = await Promise.all([boxOf(view), boxOf(block)]);
  expect(Math.abs(first.top - scroller.top)).toBeLessThanOrEqual(1);
}

async function expectInside(view: Locator, element: Locator) {
  const [scroller, inner] = await Promise.all([boxOf(view), boxOf(element)]);
  expect(inner.top).toBeGreaterThanOrEqual(scroller.top - 1);
  expect(inner.bottom).toBeLessThanOrEqual(scroller.bottom + 1);
}

const heading = (view: Locator, name: string) => view.getByRole('heading', { level: 2, name });
const between = (view: Locator) => view.getByText('The cache follows from the same transaction.');

test('the Brief steps with → and ← and keeps each stop\'s lead-in and the cursor in view', async ({ page }) => {
  const { view } = await open(page, 'brief');
  const cursor = view.locator('.review-cursor');

  await page.keyboard.press('ArrowRight');
  await expect(cursor).toHaveAttribute('data-path', 'poll.go');
  await expectAtTop(view, heading(view, 'The poll asks for one write'));
  await expectInside(view, view.getByText('The poll hands every host to the store in one call.'));
  await expectInside(view, cursor);

  await page.keyboard.press('ArrowRight');
  await expect(cursor).toHaveAttribute('data-path', 'store.go');
  await expectInside(view, cursor);
  const scroller = await boxOf(view);
  expect((await boxOf(heading(view, 'The store reads inside the write'))).top).toBeLessThan(scroller.top);

  await page.keyboard.press('ArrowRight');
  await expect(cursor).toHaveAttribute('data-path', 'cache.go');
  await expectAtTop(view, between(view));
  await expectInside(view, cursor);

  await page.getByRole('button', { name: 'Edit above' }).click();
  await page.getByRole('region', { name: 'Review tile' }).focus();
  await expect(view.getByText('A paragraph added above every step after the review was read.')).toBeVisible();
  await expect(cursor).toHaveAttribute('data-path', 'cache.go');

  await page.keyboard.press('ArrowLeft');
  await expect(cursor).toHaveAttribute('data-path', 'store.go');
  await expectInside(view, cursor);
  await page.keyboard.press('ArrowLeft');
  await expect(cursor).toHaveAttribute('data-path', 'poll.go');
  await expectAtTop(view, heading(view, 'The poll asks for one write'));
  await expectInside(view, cursor);
});

test('the Walkthrough steps with → and ← and keeps each stop\'s lead-in and its card in view', async ({ page }) => {
  const { view } = await open(page, 'walkthrough');
  const card = view.locator('.review-stop--current');
  await expect(card).toHaveAttribute('data-path', 'poll.go');

  await page.keyboard.press('ArrowRight');
  await expect(card).toHaveAttribute('data-path', 'store.go');
  await expectInside(view, card.locator('.review-stop-header'));

  await page.keyboard.press('ArrowRight');
  await expect(card).toHaveAttribute('data-path', 'cache.go');
  await expectAtTop(view, between(view));
  await expectInside(view, card.locator('.review-stop-header'));

  await page.getByRole('button', { name: 'Edit above' }).click();
  await page.getByRole('region', { name: 'Review tile' }).focus();
  await expect(view.getByText('A paragraph added above every step after the review was read.')).toBeAttached();
  await expect(card).toHaveAttribute('data-path', 'cache.go');

  await page.keyboard.press('ArrowLeft');
  await page.keyboard.press('ArrowLeft');
  await expect(card).toHaveAttribute('data-path', 'poll.go');
  await expectAtTop(view, heading(view, 'The poll asks for one write'));
  await expectInside(view, card.locator('.review-stop-header'));
});
