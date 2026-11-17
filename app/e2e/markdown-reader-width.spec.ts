import { expect, test, type Page } from '@playwright/test';

const WIDE_TILE = 1800;
const NARROW_TILE = 600;
const READING_MEASURE = 760;
const PROSE = 'h1, p, ul';
const WIDE_BLOCKS = '.md-frontmatter, .md-table-wrap, .md-codeblock';

interface Layout { tile: number; text: number; prose: number[]; wide: number[] }

async function openAt(page: Page, width: number): Promise<Layout> {
  await page.setViewportSize({ width: width + 40, height: 1000 });
  await page.goto(`/test-harness/?component=MarkdownReaderWidth&width=${width}`);
  await page.evaluate(() => window.__HARNESS_READY__);
  const tile = page.getByRole('region', { name: 'Markdown tile' });
  await expect(tile.locator('.md-codeblock')).toBeVisible();
  await page.screenshot({ path: `test-results/markdown-reader-width-${width}.png`, fullPage: true });
  return tile.evaluate((element, [prose, wide]) => {
    const card = element.querySelector<HTMLElement>('.md-reader-card')!;
    const style = getComputedStyle(card);
    const widthOf = (selector: string) =>
      Array.from(card.querySelectorAll<HTMLElement>(`:scope > :is(${selector})`)).map((block) => block.getBoundingClientRect().width);
    return {
      tile: element.clientWidth,
      text: card.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight),
      prose: widthOf(prose),
      wide: widthOf(wide),
    };
  }, [PROSE, WIDE_BLOCKS]);
}

test('on a wide tile the card fills the tile, wide blocks fill the card and prose keeps its reading measure', async ({ page }) => {
  const layout = await openAt(page, WIDE_TILE);
  expect(layout.tile - layout.text).toBeLessThan(200);
  expect(layout.wide).toHaveLength(3);
  for (const width of layout.wide) expect(width).toBeCloseTo(layout.text, 0);
  expect(layout.prose).toHaveLength(3);
  for (const width of layout.prose) expect(width).toBe(READING_MEASURE);
});

test('on a narrow tile prose and wide blocks share the card width', async ({ page }) => {
  const layout = await openAt(page, NARROW_TILE);
  for (const width of [...layout.prose, ...layout.wide]) expect(width).toBeCloseTo(layout.text, 0);
});
