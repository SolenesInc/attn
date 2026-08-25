import { test, expect } from '@playwright/test';

// In a real browser: the card is a CodeMirror block widget and CM cannot mount under
// happy-dom.

test.describe('FrontmatterCard', () => {
  test('renders frontmatter as a card and hides the raw YAML', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const card = page.locator('.cm-md-frontmatter');
    await expect(card).toBeVisible();
    await expect(card.locator('.cm-md-fm-type')).toHaveText('area');
    await expect(card.locator('.cm-md-fm-tag')).toHaveCount(3);
    await expect(card.locator('.cm-md-fm-source')).toHaveText('/knowledge/areas/notebook.md');
    await expect(card.locator('.cm-md-fm-dates')).toContainText('created');
    await expect(card.locator('.cm-md-fm-title')).toHaveCount(0);
    await expect(page.locator('.cm-md-h1').first()).toHaveText('Context rail');
    await expect(page.locator('.cm-content')).not.toContainText('type: area');
    await page.screenshot({ path: 'test-results/frontmatter-card.png' });
  });

  test('clicking the card reveals raw YAML, clicking the body restores the card', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-frontmatter');

    await page.locator('.cm-md-frontmatter').click();
    await expect(page.locator('.cm-md-frontmatter')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('type: area');
    await page.screenshot({ path: 'test-results/frontmatter-revealed.png' });

    await page.locator('.cm-line', { hasText: 'Context rail' }).click();
    await expect(page.locator('.cm-md-frontmatter')).toBeVisible();
    await expect(page.locator('.cm-content')).not.toContainText('type: area');
  });

  test('opens frontmatter editing from the keyboard', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const card = page.getByRole('button', { name: 'Edit note properties' });
    await card.focus();
    await expect(card).toBeFocused();

    await page.keyboard.press('Enter');
    await expect(card).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('type: area');
    await expect(page.locator('.cm-content')).toBeFocused();
  });

  test('opens frontmatter editing from a synthesized semantic click', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const card = page.getByRole('button', { name: 'Edit note properties' });

    await card.evaluate((el) => (el as HTMLElement).click());
    await expect(card).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('type: area');
  });

  test('focusing the body never expands frontmatter under the pointer click', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-frontmatter');

    await page.evaluate(() => {
      const content = document.querySelector('.cm-content')!;
      (window as typeof window & { __rawFrontmatterSeen?: boolean }).__rawFrontmatterSeen = false;
      const observer = new MutationObserver(() => {
        if (content.textContent?.includes('type: area')) {
          (window as typeof window & { __rawFrontmatterSeen?: boolean }).__rawFrontmatterSeen = true;
        }
      });
      observer.observe(content, { childList: true, subtree: true, characterData: true });
    });

    await page.locator('.cm-line', { hasText: 'Context rail' }).click();
    await page.waitForTimeout(50);

    await expect(page.locator('.cm-md-frontmatter')).toBeVisible();
    expect(await page.evaluate(
      () => (window as typeof window & { __rawFrontmatterSeen?: boolean }).__rawFrontmatterSeen,
    )).toBe(false);
  });

  test('moves the caret out of hidden YAML when frontmatter editing loses focus', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const content = page.locator('.cm-content');

    await page.locator('.cm-md-frontmatter').click();
    await expect(content).toContainText('type: area');
    await content.evaluate((el) => (el as HTMLElement).blur());
    await expect(page.locator('.cm-md-frontmatter')).toBeVisible();

    await content.focus();
    await page.keyboard.type('VISIBLE_BODY_TOKEN');
    await page.waitForFunction(() => {
      const calls = window.__HARNESS__.getCalls('change');
      return calls.some((call) => String(call[0]).includes('VISIBLE_BODY_TOKEN'));
    });
    const changed = await page.evaluate(() => {
      const calls = window.__HARNESS__.getCalls('change');
      return String(calls[calls.length - 1][0]);
    });
    expect(changed.indexOf('VISIBLE_BODY_TOKEN')).toBeGreaterThan(changed.indexOf('\n---\n'));
    await expect(page.locator('.cm-md-frontmatter')).toBeVisible();
  });

  test('keeps long markdown rendered and ArrowUp local beyond character 3,000', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-frontmatter');

    const scroller = page.locator('.cm-scroller');
    const stage2 = page.locator('.cm-line', { hasText: 'Stage 2: Rendered after' });
    // CM virtualizes offscreen lines. The intermediate scroll is acknowledged first:
    // a raw jump to the end racing CM's first measure restores its still-top anchor.
    await scroller.evaluate((el) => { el.scrollTop = 1800; });
    await expect(page.locator('.cm-line', { hasText: 'Supporting paragraph 40 ' })).toBeAttached();
    await expect(stage2).toBeAttached();
    expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(500);

    // Beyond CM's initial 3,000-character parse window.
    await expect(stage2).not.toContainText('###');
    await stage2.click();

    // Chromium resolves this deep decorated-line click onto the following blank
    // line, so ArrowUp must move locally rather than to the frontmatter atom.
    await page.keyboard.press('ArrowUp');
    await expect(stage2).toContainText('###');
    expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(500);

    await page.keyboard.press('ArrowUp');
    await expect(stage2).not.toContainText('###');
    expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(500);

    const stage3 = page.locator('.cm-line', { hasText: 'Stage 3: Rendering remains stable' });
    await scroller.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    await expect(stage3).toBeAttached();
    await expect(stage3).not.toContainText('###');
    await expect(page.locator('.cm-line', { hasText: 'Stage complete when:' })).not.toContainText('**');
  });

  test('a frontmatter-only note stays raw (no body line to anchor the cursor)', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard&empty=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await expect(page.locator('.cm-md-frontmatter')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('type: area');
  });
});
