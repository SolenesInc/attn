import { expect, test } from '@playwright/test';

interface HeaderReceipt {
  clientWidth: number;
  scrollWidth: number;
  titleWidth: number;
  overallDisplay: string;
  notesLabelDisplay: string;
  notesIconDisplay: string;
  focusLabelDisplay: string;
  focusIconDisplay: string;
  targetWidth: number;
  overlaps: boolean;
}

for (const notes of [1, 12]) {
  test(`review notes stay readable and scroll within the tile with ${notes} notes`, async ({ page }) => {
    await page.setViewportSize({ width: 1900, height: 700 });
    await page.goto(`/test-harness/?component=TileHeader&notes=${notes}`);
    const tile = page.locator('.workspace-dock-tile').first();
    await tile.getByRole('button', { name: `Notes ${notes}` }).click();
    const dialog = tile.getByRole('dialog', { name: 'Review notes' });
    const cards = dialog.locator('.md-annotation-card');
    await expect(cards).toHaveCount(notes);

    for (const width of [1816, 960]) {
      await page.getByTestId('three-leaf-workspace').evaluate((element, nextWidth) => {
        element.style.width = `${nextWidth}px`;
      }, width);
      const list = dialog.locator('.md-sidebar-list');
      await list.evaluate((element) => { element.scrollTop = 0; });
      const first = await cards.first().boundingBox();
      const viewport = await list.boundingBox();
      expect(first!.y).toBeGreaterThanOrEqual(viewport!.y);
      expect(first!.y + first!.height).toBeLessThanOrEqual(viewport!.y + viewport!.height);
      await list.evaluate((element) => { element.scrollTop = element.scrollHeight; });
      const last = await cards.last().boundingBox();
      expect(last!.y).toBeGreaterThanOrEqual(viewport!.y);
      expect(last!.y + last!.height).toBeLessThanOrEqual(viewport!.y + viewport!.height);
      const popup = await dialog.boundingBox();
      const tileBounds = await tile.boundingBox();
      expect(popup!.x).toBeGreaterThanOrEqual(tileBounds!.x);
      expect(popup!.x + popup!.width).toBeLessThanOrEqual(tileBounds!.x + tileBounds!.width);
      expect(popup!.y + popup!.height).toBeLessThanOrEqual(tileBounds!.y + tileBounds!.height);
    }
    await dialog.getByTitle('Close review notes').click();
    await expect(dialog).not.toBeVisible();
  });
}

test('three-leaf markdown headers switch modes before their controls collide', async ({ page }) => {
  await page.setViewportSize({ width: 1900, height: 700 });
  await page.goto('/test-harness/?component=TileHeader');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const workspace = page.getByTestId('three-leaf-workspace');
  const tiles = page.locator('.workspace-dock-tile');
  await expect(tiles).toHaveCount(2);
  await expect(tiles.first().getByRole('button', { name: 'Send 1 to Codex alpha' })).toBeVisible();
  await expect(tiles.nth(1).getByRole('button', { name: 'Send 1 to Claude beta' })).toBeVisible();

  const readHeader = async (index: number): Promise<HeaderReceipt> => (
    tiles.nth(index).locator('.workspace-dock-tile-header').evaluate((header) => {
      const element = header as HTMLElement;
      const visibleDirectChildren = Array.from(element.children)
        .map((child) => child as HTMLElement)
        .filter((child) => getComputedStyle(child).display !== 'none')
        .map((child) => child.getBoundingClientRect());
      return {
        clientWidth: element.clientWidth,
        scrollWidth: element.scrollWidth,
        titleWidth: element.querySelector<HTMLElement>('.workspace-dock-tile-title')!.getBoundingClientRect().width,
        overallDisplay: getComputedStyle(element.querySelector('.workspace-dock-tile-review-button--overall')!).display,
        notesLabelDisplay: getComputedStyle(element.querySelector('.workspace-dock-tile-review-label')!).display,
        notesIconDisplay: getComputedStyle(element.querySelector('.workspace-dock-tile-review-icon')!).display,
        focusLabelDisplay: getComputedStyle(element.querySelector('.workspace-dock-tile-focus-label')!).display,
        focusIconDisplay: getComputedStyle(element.querySelector('.workspace-dock-tile-focus-icon')!).display,
        targetWidth: element.querySelector<HTMLElement>('.workspace-dock-tile-send-target-name')!.getBoundingClientRect().width,
        overlaps: visibleDirectChildren.some((rect, childIndex) => (
          childIndex > 0 && rect.left < visibleDirectChildren[childIndex - 1].right
        )),
      };
    })
  );

  for (let index = 0; index < 2; index += 1) {
    const compact = await readHeader(index);
    expect(compact.clientWidth).toBeGreaterThanOrEqual(605);
    expect(compact.scrollWidth).toBe(compact.clientWidth);
    expect(compact.titleWidth).toBeGreaterThanOrEqual(96);
    expect(compact.overallDisplay).toBe('none');
    expect(compact.notesLabelDisplay).not.toBe('none');
    expect(compact.notesIconDisplay).toBe('none');
    expect(compact.focusLabelDisplay).toBe('none');
    expect(compact.focusIconDisplay).not.toBe('none');
    expect(compact.targetWidth).toBeGreaterThan(20);
    expect(compact.overlaps).toBe(false);
  }

  await workspace.evaluate((element) => { element.style.width = '1440px'; });

  for (let index = 0; index < 2; index += 1) {
    const tight = await readHeader(index);
    expect(tight.clientWidth).toBe(480);
    expect(tight.scrollWidth).toBe(tight.clientWidth);
    expect(tight.titleWidth).toBeGreaterThanOrEqual(72);
    expect(tight.notesLabelDisplay).toBe('none');
    expect(tight.notesIconDisplay).not.toBe('none');
    expect(tight.targetWidth).toBeGreaterThan(20);
    expect(tight.overlaps).toBe(false);
  }

  const firstTile = tiles.first();
  await firstTile.getByRole('button', { name: 'Change annotation destination' }).click();
  const beta = firstTile.getByRole('menuitemradio', { name: 'Claude beta approval' });
  await expect(beta).toHaveAttribute('aria-checked', 'false');
  await beta.click();
  await expect(firstTile.getByRole('button', { name: 'Send 1 to Claude beta' })).toBeVisible();
});
