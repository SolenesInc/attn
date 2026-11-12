import { expect, test, type Locator, type Page } from '@playwright/test';

const TILE_WIDTH = 716;
const LONG_LINE_END = 'the end of the long line */';

async function openReview(page: Page, server: 'ready' | 'failed') {
  await page.goto(`/test-harness/?component=ReviewReader&width=${TILE_WIDTH}&server=${server}`);
  await page.evaluate(() => window.__HARNESS_READY__);
  const tile = page.getByRole('region', { name: 'Review tile' });
  await expect(tile.locator('.review-stop[data-path="poll.go"]')).toBeVisible();
  await tile.focus();
  return tile;
}

interface Entry { text: string; title: string | null; left: number; right: number; top: number; bottom: number; clipped: boolean; state: boolean }

async function keyLineEntries(tile: Locator): Promise<{ line: DOMRect; tile: DOMRect; entries: Entry[] }> {
  return tile.evaluate((element) => {
    const line = element.querySelector<HTMLElement>('.review-key-line')!;
    const leaves: HTMLElement[] = [];
    const collect = (parent: Element) => {
      for (const child of Array.from(parent.children) as HTMLElement[]) {
        if (getComputedStyle(child).display === 'contents') collect(child);
        else leaves.push(child);
      }
    };
    collect(line.querySelector('.review-key-line-entries')!);
    leaves.push(line.querySelector<HTMLElement>('.review-key-line-all')!);
    const entries = leaves.map((leaf) => {
      const box = leaf.getBoundingClientRect();
      return {
        text: leaf.textContent ?? '',
        title: leaf.getAttribute('title'),
        left: box.left,
        right: box.right,
        top: box.top,
        bottom: box.bottom,
        clipped: leaf.scrollWidth > leaf.clientWidth,
        state: leaf.classList.contains('review-nav-state'),
      };
    });
    return { line: line.getBoundingClientRect().toJSON(), tile: element.getBoundingClientRect().toJSON(), entries };
  });
}

async function expectEveryKeyShown(tile: Locator, expected: readonly string[], server: 'ready' | 'failed') {
  const { line, tile: tileBox, entries } = await keyLineEntries(tile);
  expect(line.left).toBeGreaterThanOrEqual(tileBox.left);
  expect(line.right).toBeLessThanOrEqual(tileBox.right);
  expect(line.bottom).toBeLessThanOrEqual(tileBox.bottom);
  for (const entry of entries) {
    expect(entry.left, entry.text).toBeGreaterThanOrEqual(line.left);
    expect(entry.right, entry.text).toBeLessThanOrEqual(line.right);
    expect(entry.top, entry.text).toBeGreaterThanOrEqual(line.top);
    expect(entry.bottom, entry.text).toBeLessThanOrEqual(line.bottom);
    if (!entry.state) expect(entry.clipped, entry.text).toBe(false);
  }
  const texts = entries.map((entry) => entry.text);
  for (const key of [...expected, 'frh definition, uses, type', '? all keys']) expect(texts).toContain(key);
  const state = entries.find((entry) => entry.state);
  expect(state).toBeDefined();
  if (server === 'failed') expect(texts).toEqual(expect.arrayContaining(['s set up', 'x not here']));
  else expect(state?.clipped).toBe(false);
}

const BRIEF_KEYS = ['1Brief2Walkthrough3Map', '↑↓ line', '←→ stop', 'c note', 'a ask', 'i edit', '⌘↵ send', '↵ open the file', 'e more lines', 'z fill the tile', '/ search'];
const WALKTHROUGH_KEYS = ['↑↓ line', '←→ stop', '⇧←⇧→ sideways', 'z hide the script', '/ search'];
const MAP_KEYS = ['↑↓ line', '←→ message', '↵ open the file', 'e more lines', '/ search'];
const FILE_KEYS = ['↑↓ line', '←→ change', '⇧←⇧→ sideways', 'z fill the tile', '/ search', '⌃o back', 'esc close'];

async function cursorToLongLine(page: Page, tile: Locator) {
  await page.keyboard.press('ArrowRight');
  for (let row = 0; row < 3; row += 1) await page.keyboard.press('ArrowDown');
  const cursor = tile.locator('.review-stop .review-cursor');
  await expect(cursor).toHaveAttribute('data-line', '3');
  await expect(cursor).toHaveAttribute('data-kind', '+');
  return cursor;
}

/** The long row reads in full inside the tile, its wrapped part starting after the line numbers and the sign. */
async function expectWrapped(row: Locator) {
  await expect(row).toContainText(LONG_LINE_END);
  const receipt = await row.evaluate((element) => {
    const text = element.querySelector<HTMLElement>('.review-row-text')!;
    const sign = element.querySelector<HTMLElement>('.review-row-sign')!;
    const range = element.ownerDocument.createRange();
    range.selectNodeContents(text);
    const lines = Array.from(range.getClientRects());
    const tile = element.closest<HTMLElement>('[aria-label="Review tile"]')!.getBoundingClientRect();
    return {
      textLeft: text.getBoundingClientRect().left,
      signRight: sign.getBoundingClientRect().right,
      lineLefts: lines.map((rect) => rect.left),
      lineRights: lines.map((rect) => rect.right),
      tops: new Set(lines.map((rect) => Math.round(rect.top))).size,
      tileRight: tile.right,
      clipped: text.scrollWidth > text.clientWidth,
    };
  });
  expect(receipt.tops).toBeGreaterThan(1);
  expect(receipt.clipped).toBe(false);
  expect(receipt.textLeft).toBeGreaterThanOrEqual(receipt.signRight);
  for (const left of receipt.lineLefts) expect(left).toBeGreaterThanOrEqual(receipt.textLeft);
  for (const right of receipt.lineRights) expect(right).toBeLessThanOrEqual(receipt.tileRight);
}

for (const server of ['ready', 'failed'] as const) {
  test(`every key of each frame shows at tile width with a ${server} server`, async ({ page }) => {
    const tile = await openReview(page, server);
    await expectEveryKeyShown(tile, BRIEF_KEYS, server);

    await cursorToLongLine(page, tile);
    await page.keyboard.press('Enter');
    await expect(tile.getByLabel('File view')).toBeVisible();
    await expectEveryKeyShown(tile, FILE_KEYS, server);
    await page.keyboard.press('Escape');
    await expect(tile.getByLabel('File view')).toHaveCount(0);

    await page.keyboard.press('2');
    await expect(tile.locator('.review-file-view--stage')).toBeVisible();
    await expectEveryKeyShown(tile, WALKTHROUGH_KEYS, server);

    await page.keyboard.press('3');
    await expect(tile.locator('.review-map')).toBeVisible();
    await expectEveryKeyShown(tile, MAP_KEYS, server);
  });
}

test('a long line wraps in a stop, with its note, mark and name labels on its row', async ({ page }) => {
  const tile = await openReview(page, 'ready');
  const row = await cursorToLongLine(page, tile);
  await expectWrapped(row);

  await page.keyboard.press('c');
  const composer = page.getByRole('dialog', { name: 'Write a note' });
  expect((await composer.boundingBox())!.y + (await composer.boundingBox())!.height).toBeLessThanOrEqual((await tile.locator('.review-key-line').boundingBox())!.y);
  await composer.locator('textarea').fill('Check the options.');
  await page.keyboard.press('Enter');
  const underRow = (selector: string) => row.evaluate((element, selector) => {
    const siblings = Array.from(element.parentElement!.children);
    const below = siblings.slice(siblings.indexOf(element) + 1);
    const nextRow = below.findIndex((sibling) => sibling.classList.contains('review-row'));
    return below.slice(0, nextRow < 0 ? below.length : nextRow).some((sibling) => sibling.matches(selector));
  }, selector);
  expect(await underRow('.review-note--look')).toBe(true);
  expect(await underRow('.review-mark')).toBe(true);

  await page.keyboard.press('f');
  const labels = row.locator('.review-name-label');
  await expect(labels.first()).toBeAttached();
  const labelsInRow = await row.evaluate((element) => {
    const box = element.getBoundingClientRect();
    return Array.from(element.querySelectorAll('.review-name-label')).every((label) => {
      const at = label.getBoundingClientRect();
      return at.top >= box.top && at.bottom <= box.bottom && at.left >= box.left && at.right <= box.right;
    });
  });
  expect(labelsInRow).toBe(true);
  await page.keyboard.press('Escape');

  await page.keyboard.press('3');
  const mapRow = tile.locator('.review-map-detail .review-row[data-line="3"][data-kind="+"]');
  await expectWrapped(mapRow);
});

/** ⇧→ scrolls the file sideways by half the view until the long line's end shows, every row colored to that end, and ⇧← scrolls back. */
async function expectSidewaysScroll(page: Page, body: Locator) {
  const longRow = body.locator('.review-row[data-line="3"][data-kind="+"]');
  await expect(longRow).toContainText(LONG_LINE_END);
  const shortRow = body.locator('.review-row[data-line="5"]');
  expect((await longRow.boundingBox())!.height).toBe((await shortRow.boundingBox())!.height);

  const { clientWidth, scrollWidth } = await body.evaluate((element) => ({ clientWidth: element.clientWidth, scrollWidth: element.scrollWidth }));
  const farthest = scrollWidth - clientWidth;
  expect(farthest).toBeGreaterThan(0);
  const step = clientWidth / 2;
  const scrollLeft = () => body.evaluate((element) => element.scrollLeft);

  await page.keyboard.press('Shift+ArrowRight');
  await expect.poll(scrollLeft).toBeCloseTo(Math.min(step, farthest), 0);
  const presses = Math.ceil(farthest / step);
  for (let press = 1; press < presses; press += 1) await page.keyboard.press('Shift+ArrowRight');
  await expect.poll(scrollLeft).toBeCloseTo(farthest, 0);
  const end = await longRow.evaluate((element) => {
    const text = element.querySelector('.review-row-text')!;
    const range = element.ownerDocument.createRange();
    range.selectNodeContents(text);
    const view = element.closest('.review-file-view-body')!.getBoundingClientRect();
    const rect = range.getBoundingClientRect();
    const removed = element.closest('.review-file-view-rows')!.querySelector('.review-row[data-kind="-"]')!;
    return { right: rect.right, viewRight: view.right, shortRowRight: removed.getBoundingClientRect().right };
  });
  expect(end.right).toBeLessThanOrEqual(end.viewRight);
  expect(end.shortRowRight).toBeGreaterThanOrEqual(end.right);

  for (let press = 0; press < presses; press += 1) await page.keyboard.press('Shift+ArrowLeft');
  await expect.poll(scrollLeft).toBe(0);
}

test('the file view and the Walkthrough keep one row a line and scroll sideways by half their width', async ({ page }) => {
  const tile = await openReview(page, 'ready');
  await cursorToLongLine(page, tile);
  await page.keyboard.press('Enter');
  const body = tile.getByLabel('File view').locator('.review-file-view-body');
  await expectSidewaysScroll(page, body);
  await expect(body.locator('.review-cursor')).toHaveAttribute('data-row-index', '3');

  await page.keyboard.press('Escape');
  await page.keyboard.press('2');
  const stage = tile.locator('.review-file-view--stage .review-file-view-body');
  await expectSidewaysScroll(page, stage);
  await expect(tile.locator('.review-stop--current')).toHaveAttribute('data-path', 'poll.go');
});
