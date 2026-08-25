import { test, expect, type Page } from '@playwright/test';

declare global {
  interface Window {
    __EDITOR_HARNESS__?: {
      applyExternal: (next: string) => void;
      swapValue: (next: string) => void;
    };
  }
}

// CM does not react to a raw `scrollTop =` write until its scroll listener fires a
// measure; a deep below-the-fold line being attached is the "CM has settled" signal.
async function scrollIntoLongNote(page: Page): Promise<number> {
  const scroller = page.locator('.cm-scroller');
  await scroller.evaluate((el) => { el.scrollTop = 600; });
  await expect(
    page.locator('.cm-line', { hasText: 'Paragraph line number 25 of the long note' }),
  ).toBeAttached();
  return scroller.evaluate((el) => el.scrollTop);
}

test.describe('LiveMarkdownEditor (live preview)', () => {
  test('renders markdown inline, hides syntax off the cursor line, and reveals it on it', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await expect(page.locator('.cm-md-h1').first()).toBeVisible();
    await expect(page.locator('.cm-md-h2').first()).toBeVisible();
    await expect(page.locator('.cm-md-strong').first()).toBeVisible();
    await expect(page.locator('.cm-md-em').first()).toBeVisible();
    await expect(page.locator('.cm-md-code').first()).toBeVisible();
    const link = page.locator('.cm-md-link').first();
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute('data-href', '/knowledge/areas/foo.md');

    const firstLine = page.locator('.cm-line').first();
    await expect(firstLine).toContainText('Notebook heading');
    await expect(firstLine).not.toContainText('#');
    await page.screenshot({ path: 'test-results/live-editor-preview.png' });

    await firstLine.click();
    await expect(firstLine).toContainText('#');
    await page.screenshot({ path: 'test-results/live-editor-active-line.png' });
  });

  test('renders a visible caret in the app theme (not CodeMirror\'s default black)', async ({ page }) => {
    // basicSetup's drawSelection hides the native caret and draws its own .cm-cursor,
    // whose default border is solid black — invisible on the dark pane.
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.locator('.cm-content').click();

    const cursor = page.locator('.cm-cursor').first();
    await cursor.waitFor();
    const borderColor = await cursor.evaluate((el) => getComputedStyle(el).borderLeftColor);
    expect(borderColor).not.toBe('rgb(0, 0, 0)');
    expect(borderColor).toBe('rgb(232, 232, 232)');
  });

  test('typing edits the document and reports changes', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor&empty=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await page.locator('.cm-content').click();
    await page.keyboard.type('# Fresh note', { delay: 8 });

    await page.waitForFunction(() => window.__HARNESS__.getCalls('change').length > 0);
    const last = await page.evaluate(() => {
      const calls = window.__HARNESS__.getCalls('change');
      return calls[calls.length - 1][0] as string;
    });
    expect(last).toBe('# Fresh note');
    await expect(page.locator('.cm-md-h1').first()).toBeVisible();
  });

  test('mod-click on a wiki link reports the href to follow', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-link');

    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.locator('.cm-md-link').first().click({ modifiers: [modifier] });

    const calls = await page.evaluate(() => window.__HARNESS__.getCalls('followLink'));
    expect(calls).toHaveLength(1);
    expect(calls[0][0]).toBe('/knowledge/areas/foo.md');
  });

  test('selecting text reports a non-empty selection', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await page.locator('.cm-line').first().click();
    await page.keyboard.press('Home');
    await page.keyboard.press('Shift+End');

    await page.waitForFunction(() => {
      const calls = window.__HARNESS__.getCalls('selectionChange');
      const last = calls[calls.length - 1]?.[0] as { text?: string } | null;
      return !!last && typeof last.text === 'string' && last.text.length > 0;
    });
    const last = await page.evaluate(() => {
      const calls = window.__HARNESS__.getCalls('selectionChange');
      return calls[calls.length - 1][0] as { text: string; top: number; left: number };
    });
    expect(last.text).toContain('Notebook heading');
  });

  test('keeps the reader scrolled in place when an on-disk change is applied (minimal edit)', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const scroller = page.locator('.cm-scroller');
    const before = await scrollIntoLongNote(page);
    expect(before).toBeGreaterThan(400);

    await page.evaluate(() => {
      const doc = Array.from({ length: 80 }, (_, i) => `Paragraph line number ${i + 1} of the long note.`);
      doc[79] = 'Paragraph line number 80 of the long note — EDITED BY AGENT.';
      window.__EDITOR_HARNESS__!.applyExternal(`# Long note\n\n${doc.join('\n\n')}\n`);
    });

    await page.waitForFunction(() => {
      const calls = window.__HARNESS__.getCalls('change');
      const last = calls[calls.length - 1]?.[0] as string | undefined;
      return !!last && last.includes('EDITED BY AGENT');
    });
    await expect
      .poll(() => scroller.evaluate((el, b) => Math.abs(el.scrollTop - b), before))
      .toBeLessThanOrEqual(4);
  });

  test('contrast: a full value swap snaps the scroller to the top (the bug the fix avoids)', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const scroller = page.locator('.cm-scroller');
    expect(await scrollIntoLongNote(page)).toBeGreaterThan(400);

    await page.evaluate(() => {
      const doc = Array.from({ length: 80 }, (_, i) => `Paragraph line number ${i + 1} of the long note.`);
      doc[79] = 'Paragraph line number 80 of the long note — EDITED BY AGENT.';
      window.__EDITOR_HARNESS__!.swapValue(`# Long note\n\n${doc.join('\n\n')}\n`);
    });

    await expect.poll(() => scroller.evaluate((el) => el.scrollTop)).toBeLessThan(50);
  });

  test('renders list bullets, task checkboxes, and a fenced code block, and toggles a checkbox on click', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await expect(page.locator('.cm-md-bullet').first()).toBeVisible();
    await expect(page.locator('.cm-md-checkbox')).toHaveCount(2);
    await expect(page.locator('.cm-md-checkbox.is-checked')).toHaveCount(1);
    await expect(page.locator('.cm-md-codeblock').first()).toBeVisible();
    await expect(page.locator('.cm-md-codefence').first()).toBeVisible();
    await expect(page.locator('.cm-md-codeinfo').first()).toBeVisible();
    await page.screenshot({ path: 'test-results/live-editor-polish.png' });

    await page.locator('.cm-md-checkbox:not(.is-checked)').first().click();
    await page.waitForFunction(() => {
      const calls = window.__HARNESS__.getCalls('change');
      const last = calls[calls.length - 1]?.[0] as string | undefined;
      return !!last && last.includes('- [x] an open task');
    });
    await expect(page.locator('.cm-md-checkbox.is-checked')).toHaveCount(2);
  });
});
