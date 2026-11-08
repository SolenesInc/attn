import { test, expect, type Page } from '@playwright/test';

const SESSION = 'popped';

const printedOnScreen = (page: Page) => page.evaluate((sid) => {
  const trace = (window as Window & { __ATTN_TERMINAL_DIAG_DUMP?: () => Array<{ kind: string; pane?: string; modelPrintable: number }> })
    .__ATTN_TERMINAL_DIAG_DUMP?.() ?? [];
  const paints = trace.filter((entry) => entry.kind === 'paint' && entry.pane?.includes(sid));
  return paints.at(-1)?.modelPrintable ?? 0;
}, SESSION);

const emit = async (page: Page, data: string) => {
  await page.evaluate(({ id, data }) => window.__TEST_EMIT_PTY_DATA!(id, data), { id: SESSION, data });
  await page.evaluate(() => window.__POPPED_TERMINAL__.drain());
};

test('a popped terminal keeps painting and fitting its window while main shows another desktop', async ({ page }) => {
  await page.addInitScript(() => {
    (window as Window & { __ATTN_RENDER_TRACE_ON?: boolean }).__ATTN_RENDER_TRACE_ON = true;
  });
  await page.goto('/test-harness/?component=PoppedTerminal');
  await page.evaluate(() => window.__HARNESS_READY__);
  const opened = page.waitForEvent('popup');
  await page.evaluate(() => window.__POPPED_TERMINAL__.popOut());
  const tileWindow = await opened;
  await expect(tileWindow.locator('canvas')).toBeVisible();

  await page.evaluate(() => window.__POPPED_TERMINAL__.showMainElsewhere());

  const printedBefore = await printedOnScreen(page);
  await emit(page, 'output while main shows another desktop\r\n');
  await expect.poll(() => printedOnScreen(page)).toBeGreaterThan(printedBefore);

  const sizeBefore = await page.evaluate(() => window.__POPPED_TERMINAL__.size());
  await tileWindow.setViewportSize({ width: 450, height: 300 });
  await expect.poll(() => page.evaluate(() => window.__POPPED_TERMINAL__.size())).not.toEqual(sizeBefore);
  const fitted = await page.evaluate(() => window.__POPPED_TERMINAL__.size());
  await expect.poll(() => page.evaluate(() => window.__POPPED_TERMINAL__.ptyResizes().at(-1))).toEqual(fitted);
});
