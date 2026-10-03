import { test, expect } from '@playwright/test';

test('Recent loads visible previews serially across close/reopen and releases offscreen images', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 240 });
  const requests: string[] = [];
  let imageFailed = false;
  let releaseFirst!: () => void;
  let firstSeen!: () => void;
  const firstRequest = new Promise<void>(resolve => { firstSeen = resolve; });
  const firstReply = new Promise<void>(resolve => { releaseFirst = resolve; });
  const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jR1sAAAAASUVORK5CYII=';
  await page.route('**/capture-image-wire', async route => {
    const command = route.request().postDataJSON();
    expect(command.cmd).toBe('capture_attachment_get');
    requests.push(command.attachment_id);
    if (requests.length === 1) { firstSeen(); await firstReply; }
    if (command.attachment_id === 'image-3-1' && !imageFailed) {
      imageFailed = true;
      await route.fulfill({ status: 503, body: 'Image temporarily unavailable' }); return;
    }
    await route.fulfill({ json: { download: { data_base64: png, next_offset: 68, eof: true } } });
  });
  await page.goto('/test-harness/?component=QuickCaptureHistory');
  await firstRequest;
  await page.getByRole('button', { name: /Back to note/ }).click();
  await page.getByRole('button', { name: 'Open Recent' }).click();
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  expect(requests).toEqual(['image-0-0']);
  releaseFirst();
  await expect(page.getByRole('img', { name: 'Preview 0-0' })).toBeVisible();
  await expect(page.getByRole('img', { name: 'Preview 0-1' })).toBeVisible();
  expect(requests).toEqual(['image-0-0', 'image-0-0', 'image-0-1']);
  await page.screenshot({ path: testInfo.outputPath('recent-visible-previews.png') });
  await page.locator('.capture-history-image').last().scrollIntoViewIfNeeded();
  await expect(page.getByRole('alert')).toContainText('Image temporarily unavailable');
  await page.getByRole('button', { name: 'Retry Preview 3-1' }).click();
  await expect(page.getByRole('img', { name: 'Preview 3-1' })).toBeVisible();
  await expect(page.getByRole('img', { name: 'Preview 0-0' })).toHaveCount(0);
  await expect(page.getByRole('img', { name: 'Preview 0-1' })).toHaveCount(0);
  await page.getByRole('button', { name: /Back to note/ }).click();
  await expect(page.getByRole('img')).toHaveCount(0);
});
