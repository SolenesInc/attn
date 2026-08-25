import { test, expect, type Page, type Locator } from '@playwright/test';

// Shiki highlighting is async: every test waits for `__HARNESS__.ready` AND
// for real rendered `[data-line]` content before interacting.

const UNSEEDED = '/test-harness/?component=DiffView&seed=0';

async function openHarness(page: Page, url: string) {
  await page.goto(url);
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await page.waitForSelector('diffs-container');
  await page.locator('diffs-container [data-line-number-content]').first().waitFor();
}

async function openLargeDiff(page: Page) {
  await openHarness(page, UNSEEDED);
  await page.evaluate(() => {
    window.__HARNESS__.setUseLargeDiff(true);
    window.__HARNESS__.setExpandUnchanged(true);
  });
  await expect
    .poll(async () => page.locator('diffs-container [data-line]').count())
    .toBeGreaterThan(20);
}

async function scrollTopReport(page: Page): Promise<Record<string, number | null>> {
  return page.evaluate(() => {
    const report: Record<string, number | null> = {};
    const scroller = document.querySelector('.diff-view-scroller');
    report['.diff-view-scroller'] = scroller ? scroller.scrollTop : null;

    const container = document.querySelector('diffs-container');
    report['diffs-container (light DOM el)'] = container ? (container as HTMLElement).scrollTop : null;

    const shadow = container?.shadowRoot;
    if (shadow) {
      const shadowScrollers = shadow.querySelectorAll('*');
      let maxScrollTop = 0;
      let maxSelector = '';
      shadowScrollers.forEach((el) => {
        const st = (el as HTMLElement).scrollTop;
        if (st > maxScrollTop) {
          maxScrollTop = st;
          maxSelector = el.tagName + (el.className ? `.${String(el.className).replace(/\s+/g, '.')}` : '');
        }
      });
      report['diffs-container shadowRoot (max scrollTop element)'] = maxScrollTop;
      report['diffs-container shadowRoot (max scrollTop selector)'] = maxSelector as unknown as number;
    }
    return report;
  });
}

async function realScrollTop(page: Page): Promise<number> {
  return page.evaluate(() => {
    const scroller = document.querySelector('.diff-view-scroller') as HTMLElement | null;
    return scroller?.scrollTop ?? -1;
  });
}

function calls(page: Page, name: string) {
  return page.evaluate((n) => window.__HARNESS__.getCalls(n), name);
}

// A REAL wheel event, never a programmatic `scrollBy`: DiffView pins
// `.diff-view-scroller` to the top until real user input arrives.
async function scrollDown(page: Page, amount: number) {
  const box = await page.locator('.diff-view-scroller').boundingBox();
  if (box) {
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  }
  await page.mouse.wheel(0, amount);
  await page.waitForTimeout(150);
}

// `[data-gutter-utility-slot]` lives in `diffs-container`'s OPEN shadow root:
// `page.locator` pierces it, `document.querySelector` in `evaluate` does not.
async function gutterUtilityLineNumber(page: Page): Promise<number | null> {
  const slot = page.locator('diffs-container [data-gutter-utility-slot]');
  if ((await slot.count()) === 0) return null;
  return slot.first().evaluate((el) => {
    const numberCell = el.parentElement;
    const raw =
      numberCell?.getAttribute('data-column-number') ??
      numberCell?.closest('[data-column-number]')?.getAttribute('data-column-number');
    return raw != null ? Number(raw) : null;
  });
}

test.describe('DiffView scroll preservation', () => {
  test('gutter "+" on a scrolled-down visible line does not reset scroll position', async ({ page }) => {
    await openLargeDiff(page);

    await scrollDown(page, 800);
    const before = await realScrollTop(page);
    const report = await scrollTopReport(page);
    console.log('[scroll-jump/gutter] candidate scrollTops after scrolling down 800px:', report);
    console.log('[scroll-jump/gutter] before =', before);
    expect(before, 'sanity: scrolling down must actually move .diff-view-scroller').toBeGreaterThan(0);

    const scrollerBox = await page.locator('.diff-view-scroller').boundingBox();
    expect(scrollerBox).not.toBeNull();
    const lines = page.locator('diffs-container [data-line-index][data-column-number]');
    const count = await lines.count();
    let visibleLine: Locator | null = null;
    for (let i = 0; i < count; i++) {
      const candidate = lines.nth(i);
      const box = await candidate.boundingBox();
      if (!box || !scrollerBox) continue;
      const midY = box.y + box.height / 2;
      if (midY > scrollerBox.y + 20 && midY < scrollerBox.y + scrollerBox.height - 20) {
        visibleLine = candidate;
        break;
      }
    }
    expect(visibleLine, 'must find a line row visible within the scrolled viewport').not.toBeNull();

    await visibleLine!.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    const afterOpen = await realScrollTop(page);
    console.log('[scroll-jump/gutter] after opening draft, scrollTop =', afterOpen, '(before was', before, ')');

    await form.locator('textarea').click();
    await page.keyboard.type('Comment while scrolled', { delay: 10 });
    await form.locator('.save-btn').click();
    await expect(form).toHaveCount(0);
    const afterSave = await realScrollTop(page);
    console.log('[scroll-jump/gutter] after SAVING the comment, scrollTop =', afterSave, '(before was', before, ')');

    expect(Math.abs(afterOpen - before), 'scroll preserved on draft OPEN').toBeLessThanOrEqual(20);
    expect(Math.abs(afterSave - before), 'scroll preserved on comment SAVE').toBeLessThanOrEqual(20);
  });

  test('line-number click on a scrolled-down visible line does not reset scroll position', async ({ page }) => {
    await openLargeDiff(page);

    await scrollDown(page, 800);
    const before = await realScrollTop(page);
    console.log('[scroll-jump/line-number] before =', before);
    expect(before, 'sanity: scrolling down must actually move .diff-view-scroller').toBeGreaterThan(0);

    const scrollerBox = await page.locator('.diff-view-scroller').boundingBox();
    const lines = page.locator('diffs-container [data-line-index][data-column-number]');
    const count = await lines.count();
    let visibleLine: Locator | null = null;
    for (let i = 0; i < count; i++) {
      const candidate = lines.nth(i);
      const box = await candidate.boundingBox();
      if (!box || !scrollerBox) continue;
      const midY = box.y + box.height / 2;
      if (midY > scrollerBox.y + 20 && midY < scrollerBox.y + scrollerBox.height - 20) {
        visibleLine = candidate;
        break;
      }
    }
    expect(visibleLine, 'must find a line row visible within the scrolled viewport').not.toBeNull();

    await visibleLine!.click();
    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    await expect(page.locator('.diff-selection-popup')).toHaveCount(0);

    const after = await realScrollTop(page);
    console.log('[scroll-jump/line-number] after opening draft via line-number click, scrollTop =', after, '(before was', before, ')');

    expect(Math.abs(after - before)).toBeLessThanOrEqual(20);
  });

  test('refreshing content while a draft is open on a scrolled-down line does not reset scroll position', async ({ page }) => {
    await openLargeDiff(page);

    await scrollDown(page, 800);
    const scrolledBefore = await realScrollTop(page);
    console.log('[scroll-jump/refresh] scrollTop after initial scroll =', scrolledBefore);
    expect(scrolledBefore).toBeGreaterThan(0);

    const scrollerBox = await page.locator('.diff-view-scroller').boundingBox();
    const lines = page.locator('diffs-container [data-line-index][data-column-number]');
    const count = await lines.count();
    let visibleLine: Locator | null = null;
    for (let i = 0; i < count; i++) {
      const candidate = lines.nth(i);
      const box = await candidate.boundingBox();
      if (!box || !scrollerBox) continue;
      const midY = box.y + box.height / 2;
      if (midY > scrollerBox.y + 20 && midY < scrollerBox.y + scrollerBox.height - 20) {
        visibleLine = candidate;
        break;
      }
    }
    expect(visibleLine).not.toBeNull();

    await visibleLine!.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    const before = await realScrollTop(page);
    console.log('[scroll-jump/refresh] scrollTop with draft open (pre-refresh) =', before);

    await page.evaluate(() => window.__HARNESS__.refreshContent());
    await page.waitForTimeout(200);

    const after = await realScrollTop(page);
    console.log('[scroll-jump/refresh] scrollTop after refreshContent() with draft open =', after, '(before was', before, ')');

    expect(Math.abs(after - before)).toBeLessThanOrEqual(20);
  });
});

test.describe('DiffView pins scroll to the top until the user takes over', () => {
  test('a programmatic scroll before any user input snaps back to the top', async ({ page }) => {
    await openLargeDiff(page);

    await page.evaluate(() => {
      const scroller = document.querySelector('.diff-view-scroller') as HTMLElement | null;
      if (scroller) scroller.scrollTop = 500;
    });

    await expect.poll(() => realScrollTop(page)).toBe(0);
  });

  test('a real wheel scroll arms takeover, and later scroll changes are no longer pinned', async ({ page }) => {
    await openLargeDiff(page);

    await scrollDown(page, 300);
    const afterWheel = await realScrollTop(page);
    expect(afterWheel, 'the real wheel scroll itself must not be pinned').toBeGreaterThan(0);

    await page.evaluate(() => {
      const scroller = document.querySelector('.diff-view-scroller') as HTMLElement | null;
      if (scroller) scroller.scrollTop = 500;
    });
    await page.waitForTimeout(150);

    const after = await realScrollTop(page);
    expect(after, 'takeover must stick for later scroll changes too').toBe(500);
  });
});

test.describe('DiffView gutter hover follows pointer', () => {
// The "+" pins to the commented line because the library prefers a committed
// `selectedRange` over the hovered one (@pierre/diffs InteractionManager).
  test('hover "+" moves to a newly hovered line after a comment was added on a previous line', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const lineLocators = page.locator('diffs-container [data-line-index][data-column-number]');
    const count = await lineLocators.count();
    expect(count).toBeGreaterThan(6);

    const lineA = lineLocators.nth(3);
    const lineB = lineLocators.nth(6);

    const lineNumberOf = async (loc: Locator) =>
      Number(await loc.getAttribute('data-column-number'));
    const numberA = await lineNumberOf(lineA);
    const numberB = await lineNumberOf(lineB);
    console.log('[hover-death] line A number =', numberA, ' line B number =', numberB);

    await lineA.hover();
    await expect
      .poll(() => gutterUtilityLineNumber(page))
      .toBe(numberA);

    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    const textarea = form.locator('textarea');
    await textarea.click();
    await page.keyboard.type('Comment on line A', { delay: 10 });
    await form.locator('.save-btn').click();

    await expect.poll(() => page.evaluate(() => window.__HARNESS__.getCalls('addComment'))).toHaveLength(1);
    await expect(form).toHaveCount(0);

    await lineB.hover();
    await page.waitForTimeout(150);

    const pinnedLine = await gutterUtilityLineNumber(page);
    console.log('[hover-death] after hovering line B, "+" is attached to line number =', pinnedLine, '(expected', numberB, ')');

    expect(pinnedLine).toBe(numberB);
  });
});

test.describe('DiffView multiple simultaneous drafts', () => {
  async function openDraftViaGutter(page: Page, line: Locator) {
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();
  }

// Both rows on the additions side: `lineAnnotations` sorts open forms by
// `${side}:${line}`, so a mixed pair breaks the `forms.nth(n)` indexing below.
  function twoSameSideLines(page: Page): [Locator, Locator] {
    const lineLocators = page.locator('diffs-container [data-line-index][data-column-number]');
    return [lineLocators.nth(6), lineLocators.nth(7)];
  }

  test('hover "+" keeps following the pointer while a draft is open on another line', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const [lineA, lineB] = twoSameSideLines(page);
    const numberB = Number(await lineB.getAttribute('data-column-number'));

    await openDraftViaGutter(page, lineA);
    await expect(page.getByTestId('diff-comment-form')).toBeVisible();

    await lineB.hover();
    await page.waitForTimeout(150);
    await expect.poll(() => gutterUtilityLineNumber(page)).toBe(numberB);
  });

  test('clicking "+" on another line while a draft is open opens a second, independent draft box', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const [lineA, lineB] = twoSameSideLines(page);
    const forms = page.getByTestId('diff-comment-form');

    await openDraftViaGutter(page, lineA);
    await expect(forms).toHaveCount(1);
    await forms.nth(0).locator('textarea').fill('Comment A');

    await openDraftViaGutter(page, lineB);
    await expect(forms).toHaveCount(2);

    await expect(forms.nth(0).locator('textarea')).toHaveValue('Comment A');
    await forms.nth(1).locator('textarea').fill('Comment B');
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Comment A');
    await expect(forms.nth(1).locator('textarea')).toHaveValue('Comment B');
  });

  test('clicking an anchor that already has an open draft is a no-op, not an overwrite', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const lineA = page.locator('diffs-container [data-line-index][data-column-number]').nth(3);
    await openDraftViaGutter(page, lineA);
    const forms = page.getByTestId('diff-comment-form');
    await expect(forms).toHaveCount(1);
    await forms.nth(0).locator('textarea').fill('Do not lose this');

    await lineA.click();
    await expect(forms).toHaveCount(1);
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Do not lose this');
  });

  test('saves two open drafts independently, each with its own line args', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const [lineA, lineB] = twoSameSideLines(page);
    const forms = page.getByTestId('diff-comment-form');

    await openDraftViaGutter(page, lineA);
    await forms.nth(0).locator('textarea').fill('Comment A');
    await openDraftViaGutter(page, lineB);
    await expect(forms).toHaveCount(2);
    await forms.nth(1).locator('textarea').fill('Comment B');

    await forms.nth(0).locator('.save-btn').click();
    await expect.poll(() => calls(page, 'addComment')).toHaveLength(1);
    await expect(forms).toHaveCount(1);
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Comment B');

    await forms.nth(0).locator('.save-btn').click();
    await expect.poll(() => calls(page, 'addComment')).toHaveLength(2);
    await expect(forms).toHaveCount(0);

    const added = (await calls(page, 'addComment')) as Array<[number, number, string]>;
    const byContent = Object.fromEntries(added.map(([start, end, content]) => [content, [start, end]]));
    expect(byContent['Comment A'][0]).toBe(byContent['Comment A'][1]);
    expect(byContent['Comment B'][0]).toBe(byContent['Comment B'][1]);
    expect(byContent['Comment A'][0]).not.toBe(byContent['Comment B'][0]);
  });

  test('canceling one draft box leaves the other intact', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const [lineA, lineB] = twoSameSideLines(page);
    const forms = page.getByTestId('diff-comment-form');

    await openDraftViaGutter(page, lineA);
    await forms.nth(0).locator('textarea').fill('Keep me');
    await openDraftViaGutter(page, lineB);
    await forms.nth(1).locator('textarea').fill('Cancel me');

    await forms.nth(1).locator('.cancel-btn').click();
    await expect(forms).toHaveCount(1);
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Keep me');
    expect(await calls(page, 'addComment')).toHaveLength(0);
  });

  test('Escape closes the most-recently-opened draft first', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const [lineA, lineB] = twoSameSideLines(page);
    const forms = page.getByTestId('diff-comment-form');

    await openDraftViaGutter(page, lineA);
    await forms.nth(0).locator('textarea').fill('Opened first');
    await openDraftViaGutter(page, lineB);
    await forms.nth(1).locator('textarea').fill('Opened second');
    await expect(forms).toHaveCount(2);

    await page.keyboard.press('Escape');
    await expect(forms).toHaveCount(1);
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Opened first');

    await page.keyboard.press('Escape');
    await expect(forms).toHaveCount(0);
  });
});
