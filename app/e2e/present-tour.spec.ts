import { test, expect, type Page, type Locator } from '@playwright/test';

const UNSEEDED = '/test-harness/?component=PresentTour&seed=0';

async function openHarness(page: Page, url: string) {
  await page.goto(url);
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await page.waitForSelector('diffs-container');
  await page.locator('diffs-container [data-line-number-content]').first().waitFor();
}

function fileContainers(page: Page): Locator {
  return page.locator('diffs-container');
}

async function titleOf(container: Locator): Promise<string | null> {
  const title = container.locator('[data-title]');
  if ((await title.count()) === 0) return null;
  return title.first().textContent();
}

function calls(page: Page, name: string) {
  return page.evaluate((n) => window.__HARNESS__.getCalls(n), name);
}

/** A REAL wheel event: PresentTour pins `.present-tour-scroller` to the top
 * until real user input arrives, so a JS-driven scroll snaps back to 0. */
async function scrollDown(page: Page, amount: number) {
  const box = await page.locator('.present-tour-scroller').boundingBox();
  if (box) {
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  }
  await page.mouse.wheel(0, amount);
  await page.waitForTimeout(200);
}

async function scrollerScrollTop(page: Page): Promise<number> {
  return page.evaluate(() => document.querySelector('.present-tour-scroller')?.scrollTop ?? -1);
}

/** `scrollToFile` scrolls smoothly, so the virtualizer keeps remounting
 * `<diffs-container>`s and anything located mid-animation detaches. */
async function waitForScrollSettle(page: Page) {
  let previous = await scrollerScrollTop(page);
  for (let i = 0; i < 20; i++) {
    await page.waitForTimeout(100);
    const current = await scrollerScrollTop(page);
    if (current === previous) return;
    previous = current;
  }
}

async function findContainerByTitle(page: Page, path: string): Promise<Locator | null> {
  const count = await fileContainers(page).count();
  for (let i = 0; i < count; i++) {
    const candidate = fileContainers(page).nth(i);
    if ((await titleOf(candidate)) === path) return candidate;
  }
  return null;
}

/** The gutter "+" detaches between resolving and clicking, so the gesture is
 * retried; the short budgets inside end a failed attempt, `toPass` bounds it. */
async function openDraftViaGutter(container: Locator, line: Locator) {
  const page = container.page();
  const forms = page.getByTestId('diff-comment-form');
  const before = await forms.count();
  await expect(async () => {
    if ((await forms.count()) > before) return;
    await line.hover();
    await container.locator('[data-utility-button]').click({ timeout: 2000 });
    await expect(forms).toHaveCount(before + 1, { timeout: 1000 });
  }).toPass({ timeout: 15_000 });
}

/** Collapse the summary first: its ~0.18s scroll-triggered fold shifts the
 * whole scroller and can fire mid-gesture. The fold has its own spec. */
async function collapseSummaryFirst(page: Page) {
  const summary = page.locator('.present-tour-summary');
  if ((await summary.count()) === 0) return;
  await page.getByTestId('present-tour-summary-toggle').click();
  await expect(summary).toHaveClass(/collapsed/);
  await expect(page.getByTestId('present-tour-summary-body')).toHaveCSS('max-height', '0px');
}

test.describe('PresentTour rendering', () => {
  test('renders every manifest file as a card inside one scroller, in reading order', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await expect(page.locator('.present-tour-scroller')).toHaveCount(1);

    const containers = fileContainers(page);
    await expect(containers.first()).toBeVisible();
    expect(await titleOf(containers.first())).toBe('src/alpha.ts');

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/gamma.ts'));
    await expect.poll(async () => {
      const last = containers.last();
      return titleOf(last);
    }).toBe('src/gamma.ts');
  });

  test('renders the per-file note as a callout under that file only', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));
    await expect(page.locator('.present-tour-file-note')).toContainText('Beta needs a second look.');
  });
});

test.describe('PresentTour reviewed toggle', () => {
  test('clicking the header Reviewed toggle flips state and calls back to the parent', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const alpha = await findContainerByTitle(page, 'src/alpha.ts');
    expect(alpha).not.toBeNull();
    const toggle = alpha!.locator('.present-tour-reviewed-toggle');
    await expect(toggle).toBeVisible();
    await expect(toggle).not.toHaveClass(/is-reviewed/);

    await toggle.click();

    await expect(toggle).toHaveClass(/is-reviewed/);
    expect(await page.evaluate(() => window.__HARNESS__.getReviewedPaths())).toEqual(['src/alpha.ts']);

    await toggle.click();
    await expect(toggle).not.toHaveClass(/is-reviewed/);
    expect(await page.evaluate(() => window.__HARNESS__.getReviewedPaths())).toEqual([]);
  });

  test('reviewed state is independent per file', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const alpha = await findContainerByTitle(page, 'src/alpha.ts');
    await alpha!.locator('.present-tour-reviewed-toggle').click();

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));
    const beta = await findContainerByTitle(page, 'src/beta.ts');
    await expect(beta!.locator('.present-tour-reviewed-toggle')).not.toHaveClass(/is-reviewed/);

    expect(await page.evaluate(() => window.__HARNESS__.getReviewedPaths())).toEqual(['src/alpha.ts']);
  });
});

test.describe('PresentTour scroll pin', () => {
  test('a programmatic scroll before any user input snaps back to the top', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await page.evaluate(() => {
      const scroller = document.querySelector('.present-tour-scroller') as HTMLElement | null;
      if (scroller) scroller.scrollTop = 500;
    });

    await expect.poll(() => scrollerScrollTop(page)).toBe(0);
  });

  test('a real wheel scroll arms takeover, and later programmatic scroll changes are no longer pinned', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await scrollDown(page, 300);
    const afterWheel = await scrollerScrollTop(page);
    expect(afterWheel, 'the real wheel scroll itself must not be pinned').toBeGreaterThan(0);

    await page.evaluate(() => {
      const scroller = document.querySelector('.present-tour-scroller') as HTMLElement | null;
      if (scroller) scroller.scrollTop = 500;
    });
    await page.waitForTimeout(150);

    const after = await scrollerScrollTop(page);
    expect(after, 'takeover must stick for later scroll changes too').toBe(500);
  });
});

test.describe('PresentTour rail-driven scroll', () => {
  test('a scroll-to-path request (rail click / j-k) scrolls the tour to that file', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    expect(await titleOf(fileContainers(page).first())).toBe('src/alpha.ts');

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));

    await expect.poll(() => scrollerScrollTop(page)).toBeGreaterThan(0);
    await expect.poll(async () => {
      const count = await fileContainers(page).count();
      for (let i = 0; i < count; i++) {
        if ((await titleOf(fileContainers(page).nth(i))) === 'src/beta.ts') return true;
      }
      return false;
    }).toBe(true);
  });

  test('re-requesting the same path still scrolls (nonce forces a re-scroll)', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/gamma.ts'));
    await expect.poll(() => scrollerScrollTop(page)).toBeGreaterThan(0);

    await scrollDown(page, -100000);
    await page.waitForTimeout(200);
    const afterUp = await scrollerScrollTop(page);

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/gamma.ts'));
    await expect.poll(() => scrollerScrollTop(page)).toBeGreaterThan(afterUp);
  });
});

test.describe('PresentTour multiple simultaneous drafts across files', () => {
  test('opening a draft on one file and one on another keeps both open independently', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await collapseSummaryFirst(page);

    const alphaContainer = fileContainers(page).first();
    const alphaLine = alphaContainer.locator('[data-line-index][data-column-number]').nth(4);
    await openDraftViaGutter(alphaContainer, alphaLine);

    const forms = page.getByTestId('diff-comment-form');
    await expect(forms).toHaveCount(1);
    await forms.nth(0).locator('textarea').fill('Comment on alpha');

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));
    await waitForScrollSettle(page);
    let betaContainer: Locator | null = null;
    await expect.poll(async () => {
      betaContainer = await findContainerByTitle(page, 'src/beta.ts');
      return betaContainer !== null;
    }).toBe(true);
    expect(betaContainer).not.toBeNull();
    const betaLine = betaContainer!.locator('[data-line-index][data-column-number]').nth(4);
    await openDraftViaGutter(betaContainer!, betaLine);

    await expect(forms).toHaveCount(2);
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Comment on alpha');
    await forms.nth(1).locator('textarea').fill('Comment on beta');
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Comment on alpha');
    await expect(forms.nth(1).locator('textarea')).toHaveValue('Comment on beta');

    await forms.nth(1).locator('.save-btn').click();
    await expect.poll(() => calls(page, 'addComment')).toHaveLength(1);
    await forms.nth(0).locator('.save-btn').click();
    await expect.poll(() => calls(page, 'addComment')).toHaveLength(2);

    const added = (await calls(page, 'addComment')) as Array<[string, number, number, string]>;
    const byContent = Object.fromEntries(added.map(([filepath, start, , content]) => [content, filepath]));
    expect(byContent['Comment on beta']).toBe('src/beta.ts');
    expect(byContent['Comment on alpha']).toBe('src/alpha.ts');
  });

  test('Escape closes the most-recently-opened draft first, across files', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await collapseSummaryFirst(page);

    const alphaContainer = fileContainers(page).first();
    const alphaLine = alphaContainer.locator('[data-line-index][data-column-number]').nth(4);
    await openDraftViaGutter(alphaContainer, alphaLine);
    const forms = page.getByTestId('diff-comment-form');
    await forms.nth(0).locator('textarea').fill('Opened first (alpha)');

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));
    await waitForScrollSettle(page);
    let betaContainer: Locator | null = null;
    await expect.poll(async () => {
      betaContainer = await findContainerByTitle(page, 'src/beta.ts');
      return betaContainer !== null;
    }).toBe(true);
    expect(betaContainer).not.toBeNull();
    const betaLine = betaContainer!.locator('[data-line-index][data-column-number]').nth(4);
    await openDraftViaGutter(betaContainer!, betaLine);
    await forms.nth(1).locator('textarea').fill('Opened second (beta)');
    await expect(forms).toHaveCount(2);

    await page.keyboard.press('Escape');
    await expect(forms).toHaveCount(1);
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Opened first (alpha)');

    await page.keyboard.press('Escape');
    await expect(forms).toHaveCount(0);
    expect(await calls(page, 'addComment')).toHaveLength(0);
  });
});

test.describe('PresentTour draft and comment gutter interactions', () => {
  test('gutter "+" on a specific line opens a draft form anchored to that line', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await collapseSummaryFirst(page);

    const alphaContainer = fileContainers(page).first();
    const line = alphaContainer.locator('[data-line-index][data-column-number]').nth(3);
    const lineNumber = Number(await line.getAttribute('data-column-number'));

    await openDraftViaGutter(alphaContainer, line);
    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    await form.locator('textarea').fill('On the right line');
    await form.locator('.save-btn').click();
    await expect.poll(() => calls(page, 'addComment')).toHaveLength(1);

    const [[filepath, lineStart, lineEnd, content]] = (await calls(page, 'addComment')) as Array<
      [string, number, number, string]
    >;
    expect(filepath).toBe('src/alpha.ts');
    expect(content).toBe('On the right line');
    expect(Math.abs(lineStart)).toBe(lineNumber);
    expect(Math.abs(lineEnd)).toBe(lineNumber);
  });

  test('a seeded comment can be edited and deleted', async ({ page }) => {
    await openHarness(page, '/test-harness/?component=PresentTour');
    await collapseSummaryFirst(page);

    const thread = page.getByTestId('diff-comment-thread');
    await expect(thread).toContainText('Seeded comment on alpha');

    await thread.locator('.edit-btn').click();
    const editForm = page.getByTestId('diff-comment-form');
    await expect(editForm).toBeVisible();
    await editForm.locator('textarea').fill('Edited comment content');
    await editForm.locator('.save-btn').click();
    await expect.poll(() => calls(page, 'editComment')).toHaveLength(1);
    await expect(thread).toContainText('Edited comment content');

    await thread.locator('.delete-btn').click();
    await expect.poll(() => calls(page, 'deleteComment')).toHaveLength(1);
    await expect(page.getByTestId('diff-comment-thread')).toHaveCount(0);
  });
});

test.describe('PresentTour summary fold', () => {
  test('wheel-scrolling the diff folds the summary card; the toggle re-expands it', async ({ page }) => {
    await page.goto('/test-harness/?component=PresentTour&seed=0&deferred=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const summary = page.locator('.present-tour-summary');
    await expect(summary).toBeVisible();
    await expect(summary).not.toHaveClass(/collapsed/);

    await page.evaluate(() => (window.__HARNESS__ as unknown as { settleDiffs: () => void }).settleDiffs());
    await page.waitForSelector('diffs-container');
    await page.locator('diffs-container [data-line-number-content]').first().waitFor();

    await expect(async () => {
      const box = await page.locator('.present-tour-scroller').boundingBox();
      if (!box) throw new Error('scroller not laid out yet');
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      await page.mouse.wheel(0, 400);
      await expect(summary).toHaveClass(/collapsed/, { timeout: 1000 });
    }).toPass({ timeout: 15_000 });

    await page.getByTestId('present-tour-summary-toggle').click();
    await expect(summary).not.toHaveClass(/collapsed/);
  });
});

test.describe('PresentTour deferred-load scroll replay', () => {
  test('a scroll request issued while still loading is replayed once files settle', async ({ page }) => {
    await page.goto('/test-harness/?component=PresentTour&seed=0&deferred=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await expect.poll(() => fileContainers(page).count()).toBe(3);

    const gammaPlaceholder = await findContainerByTitle(page, 'src/gamma.ts');
    expect(gammaPlaceholder).not.toBeNull();
    await expect(gammaPlaceholder!).toHaveClass(/present-tour-card-pending/);

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/gamma.ts'));

    await page.evaluate(() => (window.__HARNESS__ as unknown as { settleDiffs: () => void }).settleDiffs());
    await page.waitForSelector('diffs-container');
    await page.locator('diffs-container [data-line-number-content]').first().waitFor();
    await waitForScrollSettle(page);

    let gammaContainer: Locator | null = null;
    await expect.poll(async () => {
      gammaContainer = await findContainerByTitle(page, 'src/gamma.ts');
      return gammaContainer !== null;
    }).toBe(true);
    expect(gammaContainer).not.toBeNull();

    await expect(gammaContainer!).toBeInViewport();
  });
});
