import { test, expect } from '@playwright/test';

declare global {
  interface Window {
    __NB_HARNESS__?: {
      fsChanged: (path?: string, content?: string, hash?: string) => void;
      getContent: (path: string) => string;
      forceConflict: (on: boolean) => void;
    };
  }
}

test.describe('NotebookBrowser (fs surface)', () => {
  test('opens the preferred note into a live editor with no view/edit toggle and autosaves edits', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    await expect(page.getByRole('treeitem', { name: 'knowledge' })).toBeVisible();
    await expect(page.getByRole('heading', { level: 2, name: 'index' })).toBeVisible();
    await page.waitForSelector('.cm-content');
    await expect(page.locator('.cm-md-h1').first()).toBeVisible();
    await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Save' })).toHaveCount(0);
    const rail = page.locator('.notebook-browser-rail');
    await expect(rail.getByRole('button', { name: '2026-06-20' })).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-browser-open.png' });

    await page.locator('.cm-content').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type(' Extra words.', { delay: 8 });

    await page.waitForFunction(() => window.__HARNESS__.getCalls('writeFile').length > 0, null, {
      timeout: 15000,
    });
    const writes = await page.evaluate(() => window.__HARNESS__.getCalls('writeFile'));
    const last = writes[writes.length - 1] as [string, string, string | undefined];
    expect(last[0]).toBe('knowledge/index.md');
    expect(last[1]).toContain('Extra words.');
    expect(last[2]).toBe('h1');
    await expect(page.getByText('Saved')).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-browser-saved.png' });
  });

  test('opens a text file from the tree in a plain editor (no markdown affordances)', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'notes.txt' }).click();

    await expect(page.getByRole('heading', { level: 2, name: 'notes.txt' })).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'File contents' })).toBeVisible();
    await expect(page.locator('.notebook-browser-rail')).toHaveCount(0);
  });

  test('lists the note outline in the context rail and scrolls the editor to a heading on click', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();
    await page.waitForSelector('.cm-content');

    const rail = page.locator('.notebook-browser-rail');
    await expect(rail.getByRole('button', { name: 'Knowledge index' })).toBeVisible();
    await expect(rail.getByRole('button', { name: 'Sections' })).toBeVisible();
    await expect(rail.getByRole('button', { name: 'Subsection detail' })).toBeVisible();

    const scrollTop = () =>
      page.locator('.cm-scroller').evaluate((el) => (el as HTMLElement).scrollTop);
    expect(await scrollTop()).toBeLessThan(40);
    await rail.getByRole('button', { name: 'Subsection detail' }).click();
    await expect.poll(scrollTop).toBeGreaterThan(150);
  });

  test('keeps the reader scrolled in place when the open note changes on disk (and ignores unrelated changes)', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();
    await page.waitForSelector('.cm-content');

    const scrollTop = () => page.locator('.cm-scroller').evaluate((el) => (el as HTMLElement).scrollTop);
    await page.locator('.cm-scroller').evaluate((el) => { (el as HTMLElement).scrollTop = 300; });
    const parked = await scrollTop();
    expect(parked).toBeGreaterThan(150);

    await page.evaluate(() => window.__NB_HARNESS__!.fsChanged('journal/2026-06-20.md', '# touched\n', 'h-x'));
    await page.waitForTimeout(150);
    expect(Math.abs((await scrollTop()) - parked)).toBeLessThanOrEqual(4);

    await page.evaluate(() => {
      const current = window.__NB_HARNESS__!.getContent('knowledge/index.md');
      window.__NB_HARNESS__!.fsChanged('knowledge/index.md', `${current}\nAppended by an agent while you were reading.\n`, 'h-appended');
    });
    await expect.poll(async () => {
      await page.locator('.cm-scroller').evaluate((el) => { (el as HTMLElement).scrollTop = (el as HTMLElement).scrollHeight; });
      return (await page.locator('.cm-content').textContent()) ?? '';
    }).toContain('Appended by an agent');

    await page.locator('.cm-scroller').evaluate((el) => { (el as HTMLElement).scrollTop = 300; });
    const reparked = await scrollTop();
    expect(reparked).toBeGreaterThan(150);
    await page.evaluate(() => {
      const current = window.__NB_HARNESS__!.getContent('knowledge/index.md');
      window.__NB_HARNESS__!.fsChanged('knowledge/index.md', `${current}\nA second agent edit.\n`, 'h-appended-2');
    });
    await page.waitForTimeout(200);
    expect(Math.abs((await scrollTop()) - reparked)).toBeLessThanOrEqual(4);
  });

  test('shows a read-only placeholder for a binary file', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'cover.png' }).click();

    await expect(page.getByRole('heading', { level: 2, name: 'Preview not available' })).toBeVisible();
    await expect(page.getByRole('textbox')).toHaveCount(0);
  });

  test('Cmd+P summons the fuzzy finder over the modal; typing filters; Enter opens the note', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await expect(page.locator('.notebook-finder')).toHaveCount(0);

    await page.locator('.cm-content').click();
    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();
    await expect(page.locator('.notebook-finder-input')).toBeFocused();

    await expect(page.locator('.notebook-finder-option')).toHaveCount(3);
    await page.locator('.notebook-finder-input').fill('journal');
    await expect(page.locator('.notebook-finder-option')).toHaveCount(1);
    await expect(page.locator('.notebook-finder-option-path')).toHaveText('journal/2026-06-20.md');

    await page.keyboard.press('Enter');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await expect(page.getByRole('heading', { level: 2, name: '2026-06-20' })).toBeVisible();
  });

  test('Esc closes the finder before the modal, and Cmd+P re-summons it', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await page.locator('.cm-content').click();
    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toBeVisible();
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('close').length)).toBe(0);

    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await page.keyboard.press('Escape');
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('close').length)).toBeGreaterThan(0);
  });

  test('Cmd+F opens the in-editor search panel; Esc closes it before the modal', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await page.locator('.cm-content').click();
    await page.keyboard.press('Meta+f');
    await expect(page.locator('.cm-panel.cm-search')).toBeVisible();
    await expect(page.locator('.cm-search input[name="search"]')).toBeFocused();

    await page.keyboard.type('distilled');
    await expect(page.locator('.cm-searchMatch').first()).toBeVisible();
    expect(await page.locator('.cm-searchMatch').count()).toBeGreaterThan(0);

    await page.keyboard.press('Escape');
    await expect(page.locator('.cm-panel.cm-search')).toHaveCount(0);
    await expect(page.locator('.notebook-browser')).toBeVisible();
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('close').length)).toBe(0);

    await page.keyboard.press('Escape');
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('close').length)).toBeGreaterThan(0);
  });

  test('renders stage-5 chrome and folds the tree to zero width via the edge handle', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await expect(page.locator('.notebook-browser-chief-pulse')).toContainText('chief: active');
    await expect(page.locator('.notebook-browser-kind-badge')).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-stage5-chrome.png' });

    const tree = page.locator('.notebook-browser-list');
    expect(await tree.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(100);
    await page.getByRole('button', { name: 'Hide file tree' }).click();
    await expect(page.locator('.notebook-browser-body')).toHaveClass(/tree-folded/);
    await expect.poll(() => tree.evaluate((el) => el.getBoundingClientRect().width)).toBeLessThan(2);
    await expect(tree).toBeAttached();

    await page.getByRole('button', { name: 'Show file tree' }).click();
    await expect.poll(() => tree.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(100);
  });

  test('highlights a code fence, and renders a blockquote and a horizontal rule', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'fences.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'fences' })).toBeVisible();

    await expect(page.locator('.cm-md-codeblock .tok-keyword')).toBeVisible();
    await expect(page.locator('.cm-md-blockquote')).toHaveCount(1);
    await expect(page.locator('.cm-md-hr')).toHaveCount(1);
  });

  test('renders a GFM table as a widget, revealing raw source when clicked', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'fences.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'fences' })).toBeVisible();

    const table = page.locator('.cm-md-table');
    await expect(table).toBeVisible();
    await expect(table.locator('th', { hasText: 'col a' })).toBeVisible();
    await expect(table.locator('td', { hasText: 'two' })).toBeVisible();
    await expect(page.getByText('| one', { exact: false })).not.toBeVisible();

    await table.locator('tbody tr').first().click();
    await expect(table).not.toBeVisible();
    await expect(page.locator('.cm-content')).toContainText('| one');
  });

  test('renders an inline image as a widget wired to readAsset, shows a broken placeholder for a missing asset, and reveals raw source on click', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'images.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'images' })).toBeVisible();

    const img = page.locator('.cm-md-image img');
    await expect(img).toBeVisible();
    await expect(img).toHaveAttribute('src', /^data:image\/png;base64,/);
    const checked = await page.evaluate(() => window.__HARNESS__.getCalls('readAsset').map((c) => c[0]));
    expect(checked).toContain('assets/tiny.png');

    const broken = page.locator('.cm-md-image-broken');
    await expect(broken).toBeVisible();
    await expect(broken).toContainText('gone');
    await expect(broken).toContainText('image not found');

    await page.locator('.cm-md-image').click();
    await expect(page.locator('.cm-md-image')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('![tiny](assets/tiny.png)');

    // The widget's eq() is position-blind (alt/src only), so the click handler must read
    // its position from the view at click time, not from build time.
    await page.locator('.cm-content').getByText('Images', { exact: true }).click();
    await page.keyboard.press('Home');
    await page.keyboard.type('\n\n');
    await expect(page.locator('.cm-md-image')).toBeVisible();

    await page.locator('.cm-md-image').click();
    await expect(page.locator('.cm-md-image')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('![tiny](assets/tiny.png)');
  });

  // CodeMirror renders each line as one text node, so a text-content locator cannot
  // isolate a single word for dblclick; find its rect via a DOM Range.
  async function dblclickWord(page: import('@playwright/test').Page, word: string) {
    const wordRect = await page.evaluate((needle) => {
      const walker = document.createTreeWalker(document.querySelector('.cm-content')!, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        const idx = node.textContent?.indexOf(needle) ?? -1;
        if (idx === -1) continue;
        const range = document.createRange();
        range.setStart(node, idx);
        range.setEnd(node, idx + needle.length);
        const rect = range.getBoundingClientRect();
        return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
      }
      return null;
    }, word);
    if (!wordRect) throw new Error(`could not locate "${word}" in the editor`);
    await page.mouse.dblclick(wordRect.x, wordRect.y);
  }

  test('Cmd+B/I/E toggle bold, italic, and inline code on a double-clicked word', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'fences.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'fences' })).toBeVisible();

    const content = page.locator('.cm-content');

    await dblclickWord(page, 'fenced');
    await page.keyboard.press('Meta+b');
    await expect(content).toContainText('**fenced**');
    await page.keyboard.press('Meta+b');
    await expect(content).not.toContainText('**fenced**');
    await expect(content).toContainText('A fenced code block');

    // basicSetup's defaultKeymap binds Mod-i with preventDefault, shadowing Cmd-i unless
    // formattingKeymap() is raised via Prec.high; only a real keydown catches this.
    await dblclickWord(page, 'blockquote');
    await page.keyboard.press('Meta+i');
    await expect(page.locator('.cm-md-em')).toBeVisible();
    await page.keyboard.press('Meta+i');
    await expect(page.locator('.cm-md-em')).toHaveCount(0);
    await expect(content).toContainText('a blockquote');

    await dblclickWord(page, 'horizontal');
    await page.keyboard.press('Meta+e');
    await expect(page.locator('.cm-md-code')).toBeVisible();
    await page.keyboard.press('Meta+e');
    await expect(page.locator('.cm-md-code')).toHaveCount(0);
    await expect(content).toContainText('a horizontal rule');
  });

  test('anchors the send-to-chief pill below the selection end, not over the line above it', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'fences.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'fences' })).toBeVisible();

    await dblclickWord(page, 'quoted');

    const pill = page.getByRole('button', { name: 'Send to chief' });
    await expect(pill).toBeVisible();

    const selectionBottom = await page.evaluate(() => {
      const sel = window.getSelection();
      const range = sel?.rangeCount ? sel.getRangeAt(0) : null;
      return range ? range.getBoundingClientRect().bottom : null;
    });
    expect(selectionBottom).not.toBeNull();

    const pillBox = await pill.boundingBox();
    expect(pillBox).not.toBeNull();
    expect(pillBox!.y).toBeGreaterThanOrEqual(selectionBottom!);
  });

  test('restores editor focus after resolving a save conflict via "Reload from disk"', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();
    await page.waitForSelector('.cm-content');

    await page.evaluate(() => window.__NB_HARNESS__!.forceConflict(true));
    await page.locator('.cm-content').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type(' more', { delay: 8 });

    const banner = page.locator('.notebook-browser-editor-conflict');
    await expect(banner).toBeVisible();

    await page.evaluate(() => window.__NB_HARNESS__!.forceConflict(false));
    await banner.getByRole('button', { name: 'Reload from disk' }).click();
    await expect(banner).toHaveCount(0);

    // Typed WITHOUT clicking back in: focus must have been restored off the removed button.
    await page.keyboard.type('typed after reload', { delay: 8 });
    await expect(page.locator('.cm-content')).toContainText('typed after reload');
  });

  test('mod-click on a bare-relative link navigates to the sibling note (resolved against the linking note\'s directory)', async ({ page }) => {
    await page.addInitScript(() => {
      (window as unknown as { __OPENED__: string[] }).__OPENED__ = [];
      window.open = ((url?: string | URL) => {
        (window as unknown as { __OPENED__: string[] }).__OPENED__.push(String(url));
        return null;
      }) as typeof window.open;
    });
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.locator('.cm-content').click();
    await page.keyboard.press('Meta+p');
    await page.locator('.notebook-finder-input').fill('foo');
    await expect(page.locator('.notebook-finder-option')).toHaveCount(1);
    await page.keyboard.press('Enter');
    await expect(page.getByRole('heading', { level: 2, name: 'foo' })).toBeVisible();
    await page.waitForSelector('.cm-md-link[data-href="bar.md"]');

    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.locator('.cm-md-link[data-href="bar.md"]').click({ modifiers: [modifier] });

    await expect(page.getByRole('heading', { level: 2, name: 'bar' })).toBeVisible();
    await expect(page.locator('.cm-content')).toContainText('Sibling of foo');
    expect(await page.evaluate(() => (window as unknown as { __OPENED__: string[] }).__OPENED__)).toEqual([]);
  });

  test('mod-click on a #heading link scrolls that heading into view', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.locator('.cm-content').click();
    await page.keyboard.press('Meta+p');
    await page.locator('.notebook-finder-input').fill('foo');
    await expect(page.locator('.notebook-finder-option')).toHaveCount(1);
    await page.keyboard.press('Enter');
    await expect(page.getByRole('heading', { level: 2, name: 'foo' })).toBeVisible();
    await page.waitForSelector('.cm-content');

    const scrollTop = () => page.locator('.cm-scroller').evaluate((el) => (el as HTMLElement).scrollTop);
    await page.locator('.cm-scroller').evaluate((el) => { (el as HTMLElement).scrollTop = 0; });
    await expect.poll(scrollTop).toBeLessThan(40);

    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.locator('.cm-md-link[data-href="#down-below"]').click({ modifiers: [modifier] });

    await expect.poll(scrollTop).toBeGreaterThan(150);
  });
});
