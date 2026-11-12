import { expect, test, type Locator, type Page } from '@playwright/test';

interface Box { left: number; right: number; top: number; bottom: number }

const boxOf = (locator: Locator): Promise<Box> => locator.evaluate((element) => {
  const { left, right, top, bottom } = element.getBoundingClientRect();
  return { left, right, top, bottom };
});

async function open(page: Page, width: number) {
  await page.goto(`/test-harness/?component=ReviewMap&width=${width}&height=900`);
  await page.evaluate(() => window.__HARNESS_READY__);
  const tile = page.getByRole('region', { name: 'Review tile' });
  await expect(tile.locator('.review-map-message')).toContainText('Arrow 1 of 8');
  await tile.focus();
  return {
    tile,
    diagrams: tile.locator('.review-map-diagrams'),
    svg: tile.locator('.review-map-diagrams svg.review-seq'),
    detail: tile.locator('.review-frame--map > .review-doc'),
  };
}

for (const width of [716, 1100]) test(`at ${width} px, where the diagram does not fit beside the step, the Map stacks it above and shows every participant without a sideways scroll`, async ({ page }) => {
  const { tile, diagrams, svg, detail } = await open(page, width);

  expect(await diagrams.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(0);
  const column = await boxOf(diagrams);
  const participants = svg.locator('.review-seq-participant rect');
  await expect(participants).toHaveCount(4);
  for (const participant of await participants.all()) {
    const box = await boxOf(participant);
    expect(box.left).toBeGreaterThanOrEqual(column.left);
    expect(box.right).toBeLessThanOrEqual(column.right);
  }
  expect((await boxOf(diagrams)).bottom).toBeLessThanOrEqual((await boxOf(tile.locator('.review-map-message'))).top + 1);
  expect((await boxOf(tile.locator('.review-map-message'))).bottom).toBeLessThanOrEqual((await boxOf(detail)).top + 1);
  await expect(detail.getByRole('heading', { level: 2, name: 'The poll asks the store what changed' })).toBeVisible();
});

test('wide, the Map puts the diagram beside the step and scales it to fill its column', async ({ page }) => {
  const { diagrams, svg, detail } = await open(page, 1454);

  const column = await boxOf(diagrams);
  expect(column.right).toBeLessThanOrEqual((await boxOf(detail)).left + 1);
  const inner = await diagrams.evaluate((element) => {
    const style = getComputedStyle(element);
    return element.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
  });
  const drawn = await boxOf(svg);
  expect(drawn.right - drawn.left).toBeGreaterThanOrEqual(inner - 1);
});

test('→ in the Map puts the step\'s lead-in at the top of the detail pane and keeps the cursor in view', async ({ page }) => {
  const { tile, detail } = await open(page, 716);
  const cursor = detail.locator('.review-cursor');
  const expectInside = async (element: Locator) => {
    const [view, inner] = await Promise.all([boxOf(detail), boxOf(element)]);
    expect(inner.top).toBeGreaterThanOrEqual(view.top - 1);
    expect(inner.bottom).toBeLessThanOrEqual(view.bottom + 1);
  };
  const expectAtTop = async (element: Locator) => {
    const [view, first] = await Promise.all([boxOf(detail), boxOf(element)]);
    expect(Math.abs(first.top - view.top)).toBeLessThanOrEqual(1);
  };

  await page.keyboard.press('ArrowRight');
  await expect(tile.locator('.review-map-message')).toContainText('Arrow 2 of 8');
  await expect(cursor.locator('xpath=ancestor::section[contains(@class, "review-stop")]')).toHaveAttribute('data-path', 'internal/store/store.go');
  await expectAtTop(detail.getByRole('heading', { level: 2, name: 'The replace reads before and after, inside its transaction' }));
  await expectInside(cursor);
  await expect(detail.getByRole('heading', { level: 2, name: 'The poll asks the store what changed' })).toBeHidden();

  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await expect(tile.locator('.review-map-message')).toContainText('Arrow 4 of 8');
  await expectAtTop(detail.getByText('The second read runs before the commit.'));
  await expectInside(cursor);
  await expect(tile.locator('.review-map-diagrams [aria-current="true"]')).toBeInViewport();
});

test('wide, → puts the lead-in of a step\'s last stop at the top too', async ({ page }) => {
  const { tile, detail } = await open(page, 1454);
  for (let step = 0; step < 3; step += 1) await page.keyboard.press('ArrowRight');
  await expect(tile.locator('.review-map-message')).toContainText('Arrow 4 of 8');
  const [view, first] = await Promise.all([boxOf(detail), boxOf(detail.getByText('The second read runs before the commit.'))]);
  expect(Math.abs(first.top - view.top)).toBeLessThanOrEqual(1);
});
