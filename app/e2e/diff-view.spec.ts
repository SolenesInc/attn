import { test, expect, type Page } from '@playwright/test';

const SEEDED = '/test-harness/?component=DiffView';
const UNSEEDED = '/test-harness/?component=DiffView&seed=0';

async function openHarness(page: Page, url: string) {
  await page.goto(url);
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await page.waitForSelector('diffs-container');
  await page.locator('diffs-container [data-line-number-content]').first().waitFor();
}

function calls(page: Page, name: string) {
  return page.evaluate((n) => window.__HARNESS__.getCalls(n), name);
}

test.describe('DiffView (@pierre/diffs)', () => {
  test('renders the diff with a line-number gutter', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await expect(page.locator('diffs-container')).toBeVisible();
    const numbers = page.locator('diffs-container [data-line-number-content]');
    expect(await numbers.count()).toBeGreaterThan(0);
  });

  test('slots a seeded comment into the native annotation framework', async ({ page }) => {
    await openHarness(page, SEEDED);

    const thread = page.getByTestId('diff-comment-thread');
    await expect(thread).toBeVisible();
    await expect(thread).toContainText('Seeded comment on an added line');
    await expect(thread).toContainText('You');

    const wrapper = page.locator('diffs-container > [slot^="annotation-"]');
    await expect(wrapper).toHaveAttribute('slot', 'annotation-additions-4');
  });

  test('escapes raw HTML in rendered comment markdown', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => window.__HARNESS__.seedHtmlComment());

    const thread = page.getByTestId('diff-comment-thread');
    await expect(thread).toContainText('<img src=x onerror=alert(1)>');
    await expect(thread.locator('img')).toHaveCount(0);
    await expect(thread.locator('strong')).toContainText('safe markdown');
  });

  test('preserves soft line breaks in rendered comment markdown', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => window.__HARNESS__.seedMultilineComment());

    const paragraph = page.locator('.diff-comment-content p');
    await expect(paragraph).toContainText('First line');
    await expect(paragraph).toContainText('Second line');
    await expect.poll(() => paragraph.evaluate((el) => getComputedStyle(el).whiteSpace)).toBe('pre-wrap');
  });

  test('resolves and unresolves a comment', async ({ page }) => {
    await openHarness(page, SEEDED);
    const comment = page.locator('.diff-comment');

    await comment.locator('.resolve-btn').click();
    await expect.poll(() => calls(page, 'resolveComment')).toEqual([['seeded-1', true]]);
    await expect(comment).toHaveClass(/resolved/);
    await expect(comment.locator('.resolve-btn')).toHaveText('Unresolve');

    await comment.locator('.resolve-btn').click();
    await expect.poll(() => calls(page, 'resolveComment')).toEqual([
      ['seeded-1', true],
      ['seeded-1', false],
    ]);
    await expect(comment).not.toHaveClass(/resolved/);
  });

  test('edits a comment through the inline form', async ({ page }) => {
    await openHarness(page, SEEDED);
    const comment = page.locator('.diff-comment');

    await comment.locator('.edit-btn').click();
    await expect.poll(() => calls(page, 'startEdit')).toEqual([['seeded-1']]);

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    await form.locator('textarea').fill('Edited comment body');
    await form.locator('.save-btn').click();

    await expect.poll(() => calls(page, 'editComment')).toEqual([['seeded-1', 'Edited comment body']]);
    await expect(page.getByTestId('diff-comment-thread')).toContainText('Edited comment body');
  });

  test('sends a comment to Claude Code', async ({ page }) => {
    await openHarness(page, SEEDED);
    await page.locator('.diff-comment .send-btn').click();

    const sent = (await calls(page, 'sendToClaude')) as string[][];
    expect(sent).toHaveLength(1);
    expect(sent[0][0]).toContain('@fileA.ts:L4');
    expect(sent[0][0]).toContain('Seeded comment on an added line');
  });

  test('deletes a comment, removing its thread', async ({ page }) => {
    await openHarness(page, SEEDED);
    await expect(page.getByTestId('diff-comment-thread')).toBeVisible();

    await page.locator('.diff-comment .delete-btn').click();
    await expect.poll(() => calls(page, 'deleteComment')).toEqual([['seeded-1']]);
    await expect(page.getByTestId('diff-comment-thread')).toHaveCount(0);
  });

  test('adds a comment by clicking a line number', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await page.locator('diffs-container [data-line-index][data-column-number]').nth(4).click();
    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    await expect(page.locator('.diff-selection-popup')).toHaveCount(0);

    await form.locator('textarea').fill('A brand new comment');
    await form.locator('.save-btn').click();

    const added = (await calls(page, 'addComment')) as Array<[number, number, string]>;
    expect(added).toHaveLength(1);
    expect(added[0][2]).toBe('A brand new comment');
    expect(added[0][0]).toBe(added[0][1]);
    expect(added[0][1]).toBeGreaterThan(0);
  });

  test('drag-selecting a range of line numbers opens a multi-line draft directly', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // Both rows must resolve to the same AnnotationSide: a drag across the
    // deletion/addition boundary is a mixed-side selection, which normalizeRange rejects.
    const lines = page.locator('diffs-container [data-line-index][data-column-number]');
    const count = await lines.count();
    const startBox = await lines.nth(count - 3).boundingBox();
    const endBox = await lines.nth(count - 1).boundingBox();
    expect(startBox).not.toBeNull();
    expect(endBox).not.toBeNull();

    await page.mouse.move(startBox!.x + startBox!.width / 2, startBox!.y + startBox!.height / 2);
    await page.mouse.down();
    await page.mouse.move(endBox!.x + endBox!.width / 2, endBox!.y + endBox!.height / 2, { steps: 5 });
    await page.mouse.up();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    await expect(page.locator('.diff-selection-popup')).toHaveCount(0);

    await form.locator('textarea').fill('Comment on a dragged range');
    await form.locator('.save-btn').click();

    const added = (await calls(page, 'addComment')) as Array<[number, number, string]>;
    expect(added).toHaveLength(1);
    expect(added[0][2]).toBe('Comment on a dragged range');
    expect(added[0][0]).not.toBe(added[0][1]);
  });

  test('keeps draft text when saving a new comment fails', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(4);
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const textarea = page.locator('.diff-comment-form textarea');
    await textarea.fill('Retry this after failure');
    await page.evaluate(() => window.__HARNESS__.failNextAddComment());
    await page.locator('.diff-comment-form .save-btn').click();

    await expect.poll(() => calls(page, 'addComment')).toHaveLength(1);
    await expect(textarea).toBeVisible();
    await expect(textarea).toHaveValue('Retry this after failure');
  });

  test('clicking the code area of a line does nothing', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await page.locator('diffs-container [data-line]').nth(4).click();
    await expect(page.getByTestId('diff-comment-form')).toHaveCount(0);
    await expect(page.locator('.diff-selection-popup')).toHaveCount(0);
    expect(await calls(page, 'addComment')).toHaveLength(0);
  });

  test('the gutter "+" opens the draft directly, without the action popup', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // The gutter "+" (enableGutterUtility) only exists under hover.
    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(4);
    await line.hover();

    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    await expect(page.locator('.diff-selection-popup')).toHaveCount(0);

    await form.locator('textarea').fill('Comment from the gutter plus');
    await form.locator('.save-btn').click();

    const added = (await calls(page, 'addComment')) as Array<[number, number, string]>;
    expect(added).toHaveLength(1);
    expect(added[0][2]).toBe('Comment from the gutter plus');
    expect(added[0][0]).toBe(added[0][1]);
    expect(added[0][1]).toBeGreaterThan(0);
  });

  test('switches back to a file already rendered earlier', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    const container = page.locator('diffs-container');

    await expect(container).toContainText('function example');

    await page.evaluate(() => window.__HARNESS__.switchFile('fileB.ts'));
    await expect(container).toContainText('class Calculator');
    await expect(container).not.toContainText('function example');

    await page.evaluate(() => window.__HARNESS__.switchFile('fileA.ts'));
    await expect(container).toContainText('function example');
    await expect(container).not.toContainText('class Calculator');
  });

  test('keeps an in-progress comment when switching away from a file and back', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(4);
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Draft before switching files');

    await page.evaluate(() => window.__HARNESS__.switchFile('fileB.ts'));
    await expect(page.locator('diffs-container')).toContainText('class Calculator');
    await expect(page.locator('.diff-comment-form textarea')).toHaveCount(0);

    await page.evaluate(() => window.__HARNESS__.switchFile('fileA.ts'));
    await expect(page.locator('diffs-container')).toContainText('function example');
    await expect(page.locator('.diff-comment-form textarea')).toHaveValue('Draft before switching files');
  });

  test("re-renders when the same file's content changes in place", async ({ page }) => {
    await openHarness(page, UNSEEDED);
    const container = page.locator('diffs-container');
    await page.evaluate(() => window.__HARNESS__.setExpandUnchanged(true));
    await expect(container).toContainText('function example');
    await expect(container).not.toContainText('refresh-1');

    await page.evaluate(() => window.__HARNESS__.refreshContent());
    await expect(container).toContainText('refresh-1');
  });

  test('keeps an in-progress comment (text + focus) and existing comments when a background change re-renders the diff', async ({ page }) => {
    await openHarness(page, SEEDED);
    await expect(page.getByTestId('diff-comment-thread')).toContainText('Seeded comment on an added line');

    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(6);
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Half-written thought');
    await expect(textarea).toBeFocused();

    await page.evaluate(() => window.__HARNESS__.addBackgroundComment());
    await expect(page.locator('.diff-comment[data-comment-id^="bg-"]')).toBeVisible();

    await expect(textarea).toHaveValue('Half-written thought');
    await expect(textarea).toBeFocused();
    await expect(page.getByText('Seeded comment on an added line')).toBeVisible();
  });

  test('keeps an in-progress edit (text + focus) when a background change re-renders the diff', async ({ page }) => {
    await openHarness(page, SEEDED);
    await page.locator('.diff-comment .edit-btn').click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Edited but not yet saved');
    await expect(textarea).toBeFocused();

    await page.evaluate(() => window.__HARNESS__.addBackgroundComment());
    await expect(page.locator('.diff-comment[data-comment-id^="bg-"]')).toBeVisible();

    await expect(textarea).toHaveValue('Edited but not yet saved');
    await expect(textarea).toBeFocused();
  });

  test('keeps an in-progress comment (text + focus) when the current file changes underneath', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(6);
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Mid-sentence when the file changed');
    await expect(textarea).toBeFocused();

    await page.evaluate(() => window.__HARNESS__.refreshContent());

    await expect(textarea).toHaveValue('Mid-sentence when the file changed');
    await expect(textarea).toBeFocused();
  });

  test('keeps an in-progress edit (text + focus) when the current file changes underneath', async ({ page }) => {
    await openHarness(page, SEEDED);
    await page.locator('.diff-comment .edit-btn').click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Editing while the file changes');
    await expect(textarea).toBeFocused();

    await page.evaluate(() => window.__HARNESS__.refreshContent());

    await expect(textarea).toHaveValue('Editing while the file changes');
    await expect(textarea).toBeFocused();
  });

  test('collapses stale comments (anchor line gone) at the top, expandable on click', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => window.__HARNESS__.seedStaleComment());

    const toggle = page.getByTestId('diff-stale-comments-toggle');
    await expect(toggle).toBeVisible();
    await expect(toggle).toContainText('1 comment not visible');

    await expect(page.getByText('Stale: this code is gone')).toHaveCount(0);
    await expect(page.locator('diffs-container > [slot^="annotation-"]')).toHaveCount(0);

    await toggle.click();
    await expect(page.getByText('Stale: this code is gone')).toBeVisible();
    await page.locator('.diff-stale-comments-body .delete-btn').click();
    await expect.poll(() => calls(page, 'deleteComment')).toEqual([['stale-1']]);
  });

  test('moves a comment into the stale banner when the code shrinks past its line', async ({ page }) => {
    await openHarness(page, SEEDED);
    await expect(page.getByTestId('diff-comment-thread')).toContainText('Seeded comment on an added line');
    await expect(page.getByTestId('diff-stale-comments-toggle')).toHaveCount(0);

    await page.evaluate(() => window.__HARNESS__.shrinkContent());
    const toggle = page.getByTestId('diff-stale-comments-toggle');
    await expect(toggle).toBeVisible();
    await expect(toggle).toContainText('1 comment not visible');

    await toggle.click();
    await expect(page.getByText('Seeded comment on an added line')).toBeVisible();
  });

  test('keeps comments on collapsed unchanged lines visible in the top banner', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    const container = page.locator('diffs-container');

    await page.evaluate(() => {
      window.__HARNESS__.setUseLargeDiff(true);
      window.__HARNESS__.setExpandUnchanged(true);
      window.__HARNESS__.seedCollapsedContextComment();
    });
    await expect(container).toContainText('section2 15');
    await expect(page.getByText('Collapsed context comment')).toBeVisible();
    await expect(page.getByTestId('diff-stale-comments-toggle')).toHaveCount(0);

    await page.evaluate(() => window.__HARNESS__.setExpandUnchanged(false));
    const toggle = page.getByTestId('diff-stale-comments-toggle');
    await expect(toggle).toBeVisible();
    await expect(toggle).toContainText('1 comment not visible');
    await expect(page.getByText('Collapsed context comment')).toHaveCount(0);

    await toggle.click();
    await expect(page.getByText('Collapsed context comment')).toBeVisible();
  });

  test('toggles between unified and split layout', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await expect(page.locator('diffs-container [data-code][data-unified]')).toHaveCount(1);

    await page.evaluate(() => window.__HARNESS__.setDiffStyle('split'));
    await expect(page.locator('diffs-container [data-code][data-unified]')).toHaveCount(0);
    await expect(page.locator('diffs-container [data-code]')).not.toHaveCount(0);
  });
});
