import { test, expect, type Page } from '@playwright/test';

const SESSION = 'moving';
const CLOCK_GAP_MS = 3_000;

const emit = (page: Page, data: string) =>
  page.evaluate(({ id, data }) => window.__TEST_EMIT_PTY_DATA!(id, data), { id: SESSION, data });

const printedOnScreen = (page: Page) => page.evaluate((sid) => {
  const trace = (window as Window & { __ATTN_TERMINAL_DIAG_DUMP?: () => Array<{ kind: string; pane?: string; modelPrintable: number }> })
    .__ATTN_TERMINAL_DIAG_DUMP?.() ?? [];
  const paints = trace.filter((entry) => entry.kind === 'paint' && entry.pane?.includes(sid));
  return paints.at(-1)?.modelPrintable ?? 0;
}, SESSION);

// A child window's frame clock starts at zero when it opens; only a real browser has one.
test('terminal output paints as soon as it arrives after the tile changes windows', async ({ page }) => {
  await page.addInitScript(() => {
    (window as Window & { __ATTN_RENDER_TRACE_ON?: boolean }).__ATTN_RENDER_TRACE_ON = true;
  });
  await page.goto('/test-harness/?component=TerminalWindowMove');
  await page.evaluate(() => window.__HARNESS_READY__);
  await expect(page.locator('canvas').first()).toBeVisible();

  await page.waitForFunction((gap) => performance.now() > gap, CLOCK_GAP_MS);
  await emit(page, 'painted in main\r\n');
  await expect.poll(() => printedOnScreen(page)).toBeGreaterThan(0);
  const mainAgeAtMove = await page.evaluate(() => performance.now());

  for (const target of ['child', 'main', 'child'] as const) {
    await page.evaluate((to) => window.__TERMINAL_WINDOW_MOVE__.moveTo(to), target);
    for (const chunk of ['first', 'second']) {
      const before = await printedOnScreen(page);
      await emit(page, `${chunk} output after moving to ${target}\r\n`);
      await expect.poll(() => printedOnScreen(page)).toBeGreaterThan(before);
    }
    if (target === 'child') {
      const childAge = await page.evaluate(() => window.__TERMINAL_WINDOW_MOVE__.tileWindowNow());
      expect(childAge, 'child painted before its clock caught up with main').toBeLessThan(mainAgeAtMove);
    }
  }
});
