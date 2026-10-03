import { test, expect, waitForMockPtyBanner } from './fixtures';

test('wake forces a full paint of visible panes', async ({ page, daemon }) => {
  await daemon.start();
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const sessionId = 's-wake-paint';
  await page.evaluate(({ sessionId }) => {
    window.__TEST_INJECT_SESSION?.({
      id: sessionId,
      label: 'Wake paint',
      state: 'working',
      cwd: '/tmp/test/wake-paint',
    });
  }, { sessionId });
  await daemon.injectSession({
    id: sessionId,
    label: 'Wake paint',
    state: 'working',
    directory: '/tmp/test/wake-paint',
  });
  await page.locator(`[data-testid="session-${sessionId}"]`).click();
  await expect(page.locator(`[data-pane-session-id="${sessionId}"] .terminal-container`)).toBeVisible();
  await waitForMockPtyBanner(page, sessionId);

  const fullPaints = () => page.evaluate((id) => {
    const pane = window.__ATTN_TERMINAL_PERF_DUMP?.().find((entry) => entry.sessionId === id);
    return pane?.renderFullCount ?? null;
  }, sessionId);
  const beforeWake = await fullPaints();
  expect(beforeWake).not.toBeNull();

  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await expect.poll(fullPaints).toBeGreaterThan(beforeWake!);

  const beforeFocus = await fullPaints();
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect.poll(fullPaints).toBeGreaterThan(beforeFocus!);
});
