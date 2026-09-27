import { expect, test, type Locator, type Page } from '@playwright/test';

async function markWith(page: Page, tile: Locator, text: string, label: string) {
  await tile.evaluate((root, needle) => {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const index = (node as Text).data.indexOf(needle);
      if (index < 0) continue;
      const range = document.createRange();
      range.setStart(node, index);
      range.setEnd(node, index + needle.length);
      const selection = window.getSelection()!;
      selection.removeAllRanges();
      selection.addRange(range);
      node.parentElement!.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
      return;
    }
    throw new Error(`no text ${needle}`);
  }, text);
  await page.locator('.md-selection-toolbar').getByTitle(label).click();
}

function highlightedTexts(page: Page) {
  return page.evaluate(() => {
    const highlight = CSS.highlights.get('attn-md-comment');
    return highlight ? Array.from(highlight, (range) => (range as Range).toString()).sort() : [];
  });
}

test.describe('markdown annotations across tiles', () => {
  test('keeps every tile’s marks in the shared highlight registry while another tile repaints or closes', async ({ page }) => {
    await page.goto('/test-harness/?component=MarkdownAnnotationTiles');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const first = page.getByRole('region', { name: 'First tile' });
    const second = page.getByRole('region', { name: 'Second tile' });

    await markWith(page, first, 'target words', 'I agree');
    await expect.poll(() => highlightedTexts(page)).toEqual(['target words']);
    await markWith(page, second, 'plain prose', 'I agree');

    await expect.poll(() => highlightedTexts(page)).toEqual(['plain prose', 'target words']);

    await page.getByRole('button', { name: 'Close second tile' }).click();

    await expect.poll(() => highlightedTexts(page)).toEqual(['target words']);
  });
});
