import { expect, test, type Page } from '@playwright/test';

const WIDE_TILE = 1800;
const NARROW_TILE = 600;
const READING_MEASURE = 760;
const PROSE = ':scope > :is(h1, p:not(.md-figure), ul)';
const WIDE_BLOCKS = ':scope > :is(.md-frontmatter, .md-table-wrap, .md-codeblock)';
const DRAWINGS = '.markdown-mermaid svg, .md-reader-image';

interface Box { left: number; width: number }
interface Layout { tile: number; text: Box; prose: Box[]; wide: Box[]; drawings: Box[] }

async function openAt(page: Page, width: number): Promise<Layout> {
  await page.setViewportSize({ width: width + 40, height: 1400 });
  await page.goto(`/test-harness/?component=MarkdownReaderWidth&width=${width}`);
  await page.evaluate(() => window.__HARNESS_READY__);
  const tile = page.getByRole('region', { name: 'Markdown tile' });
  await expect(tile.locator('.md-codeblock')).toBeVisible();
  await expect(tile.locator('.markdown-mermaid svg')).toBeVisible();
  await expect(tile.locator('.md-reader-image')).toBeVisible();
  await page.screenshot({ path: `test-results/markdown-reader-width-${width}.png`, fullPage: true });
  return tile.evaluate((element, [prose, wide, drawings]) => {
    const card = element.querySelector<HTMLElement>('.md-reader-card')!;
    const style = getComputedStyle(card);
    const cardBox = card.getBoundingClientRect();
    const left = cardBox.left + card.clientLeft + parseFloat(style.paddingLeft);
    const boxOf = (block: Element) => {
      const box = block.getBoundingClientRect();
      return { left: box.left - left, width: box.width };
    };
    return {
      tile: element.clientWidth,
      text: { left: 0, width: card.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight) },
      prose: Array.from(card.querySelectorAll(prose)).map(boxOf),
      wide: Array.from(card.querySelectorAll(wide)).map(boxOf),
      drawings: Array.from(card.querySelectorAll(drawings)).map(boxOf),
    };
  }, [PROSE, WIDE_BLOCKS, DRAWINGS]);
}

function expectCentered(boxes: Box[], text: Box) {
  for (const box of boxes) expect(box.left + box.width / 2).toBeCloseTo(text.width / 2, 0);
}

test('on a wide tile prose keeps its reading measure and every block is centered on the card', async ({ page }) => {
  const layout = await openAt(page, WIDE_TILE);
  expect(layout.tile - layout.text.width).toBeLessThan(200);
  expect(layout.prose).toHaveLength(3);
  for (const box of layout.prose) expect(box.width).toBe(READING_MEASURE);
  expect(layout.wide).toHaveLength(3);
  for (const box of layout.wide) expect(box.width).toBeCloseTo(layout.text.width, 0);
  expect(layout.drawings).toHaveLength(2);
  for (const box of layout.drawings) expect(box.width).toBeLessThan(READING_MEASURE);
  expectCentered([...layout.prose, ...layout.wide, ...layout.drawings], layout.text);
});

test('on a narrow tile prose and wide blocks share the card width', async ({ page }) => {
  const layout = await openAt(page, NARROW_TILE);
  for (const box of [...layout.prose, ...layout.wide]) expect(box.width).toBeCloseTo(layout.text.width, 0);
  expectCentered(layout.drawings, layout.text);
});
