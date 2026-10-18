import { test, expect } from '@playwright/test';

const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jR1sAAAAASUVORK5CYII=';
const records = Array.from({ length: 6 }, (_, index) => ({
  id: `message-${index}`, content: `Saved message ${index}`, mailbox: { kind: 'chief' },
  created_at: `2026-10-0${1 + Math.floor(index / 2)}T1${index % 2}:00:00Z`,
  attachments: [0, 1].map(file => ({ id: `image-${index}-${file}`, name: `Preview ${index}-${file}`, media_type: 'image/png', bytes: 68 })),
}));

test('Recent loads newest visible previews serially across close/reopen and releases offscreen images', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 560, height: 300 });
  const requests: string[] = [];
  let imageFailed = false;
  let releaseFirst!: () => void;
  let firstSeen!: () => void;
  const firstRequest = new Promise<void>(resolve => { firstSeen = resolve; });
  const firstReply = new Promise<void>(resolve => { releaseFirst = resolve; });
  await page.route('**/quick-capture-history-wire', async route => {
    const command = route.request().postDataJSON();
    if (command.cmd === 'quick_capture_list') {
      await route.fulfill({ json: { list: { items: records.slice(0, 4).reverse(), draft_assets: [] } } }); return;
    }
    expect(command.cmd).toBe('quick_capture_attachment_get');
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
  await page.getByRole('button', { name: /Back to draft/ }).click();
  await page.getByRole('button', { name: 'Open Recent' }).click();
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  expect(requests).toEqual(['image-3-0']);
  releaseFirst();
  await expect(page.getByRole('img', { name: 'Preview 3-0' })).toBeVisible();
  await expect(page.getByRole('alert')).toContainText('Image temporarily unavailable');
  await page.getByRole('button', { name: 'Retry Preview 3-1' }).click();
  await expect(page.getByRole('img', { name: 'Preview 3-1' })).toBeVisible();
  expect(requests).toEqual(['image-3-0', 'image-3-0', 'image-3-1', 'image-3-1']);
  await page.screenshot({ path: testInfo.outputPath('recent-visible-previews.png') });
  await page.locator('.capture-history-image').first().scrollIntoViewIfNeeded();
  await expect(page.getByRole('img', { name: 'Preview 0-0' })).toBeVisible();
  await expect(page.getByRole('img', { name: 'Preview 3-0' })).toHaveCount(0);
  await expect(page.getByRole('img', { name: 'Preview 3-1' })).toHaveCount(0);
  await page.getByRole('button', { name: /Back to draft/ }).click();
  await expect(page.getByRole('img')).toHaveCount(0);
});

test('Recent preserves selected messages and scroll across older pages and read updates', async ({ page }, testInfo) => {
  await page.clock.setFixedTime(new Date('2026-10-03T12:00:00Z'));
  await page.setViewportSize({ width: 560, height: 440 });
  const messages = records.map((record, index) => ({ ...record, attachments: index === 5
    ? [{ id: 'pdf', name: 'launch-notes.pdf', media_type: 'application/pdf', bytes: 2202009 }]
    : [], ...(index === 4 && { content: 'Full message\n' + 'Keep the whole launch checklist. '.repeat(12) }) }));
  const pages: (string | undefined)[] = [];
  let olderRead = false;
  let releaseOlder!: () => void;
  let olderSeen!: () => void;
  const olderRequest = new Promise<void>(resolve => { olderSeen = resolve; });
  const olderReply = new Promise<void>(resolve => { releaseOlder = resolve; });
  await page.route('**/quick-capture-history-wire', async route => {
    const command = route.request().postDataJSON();
    expect(command.cmd).toBe('quick_capture_list');
    pages.push(command.cursor);
    if (command.cursor && !olderRead) { olderSeen(); await olderReply; }
    const items = command.cursor ? messages.slice(0, 2) : messages.slice(2);
    await route.fulfill({ json: { list: { items: items.map(item => olderRead && item.id === 'message-0'
      ? { ...item, read_at: '2026-10-03T12:00:00Z' } : item).reverse(), draft_assets: [], ...(!command.cursor && { next_cursor: 'older' }) } } });
  });
  await page.goto('/test-harness/?component=QuickCaptureHistory');
  const rows = page.getByRole('listitem');
  await expect(rows).toHaveCount(4);
  const selected = page.locator('.capture-history-item[aria-current=true]');
  await expect(selected).toHaveAttribute('data-capture-id', 'message-5');
  await expect(page.getByText('launch-notes.pdf')).toBeVisible();
  await expect(page.getByText('PDF', { exact: true })).toBeVisible();
  await expect(page.getByText('2.1 MiB')).toBeVisible();
  const region = page.getByRole('region', { name: 'Recent messages' });
  await region.press('ArrowUp');
  await expect(selected).toHaveAttribute('data-capture-id', 'message-4');
  await expect(page.getByText(messages[4].content, { exact: true })).toBeVisible();
  await expect(page.locator('.capture-history-text').nth(2)).toHaveText(messages[4].content);
  await region.press('ArrowDown');
  await expect(selected).toHaveAttribute('data-capture-id', 'message-5');
  const older = page.getByRole('button', { name: 'Show older messages' });
  await older.scrollIntoViewIfNeeded();
  const anchor = page.locator('[data-capture-id="message-2"]');
  await anchor.click();
  await older.scrollIntoViewIfNeeded();
  const offset = () => anchor.evaluate(el => el.getBoundingClientRect().top - el.closest('.capture-history-list')!.getBoundingClientRect().top);
  const before = await offset();
  const initialPages = pages.length;
  await older.click(); await olderRequest;
  await page.getByRole('button', { name: 'Refresh receipts' }).click();
  expect(pages.slice(initialPages)).toEqual(['older']);
  releaseOlder();
  await expect(rows).toHaveCount(6);
  await expect(selected).toHaveAttribute('data-capture-id', 'message-2');
  await expect(page.getByText('6 messages')).toBeVisible();
  expect(await offset()).toBeCloseTo(before, 0);
  await expect(page.getByText('Today', { exact: true })).toBeAttached();
  await expect(page.getByText('Yesterday', { exact: true })).toBeAttached();
  await expect(page.getByText('Oct 1', { exact: true })).toBeAttached();
  olderRead = true;
  await page.getByRole('button', { name: 'Refresh receipts' }).click();
  await expect(page.locator('[data-capture-id="message-0"] .capture-history-status')).toHaveText(/^Read /);
  await expect(rows).toHaveCount(6);
  await expect(selected).toHaveAttribute('data-capture-id', 'message-2');
  expect(await offset()).toBeCloseTo(before, 0);
  await page.screenshot({ path: testInfo.outputPath('recent-sent-messages-light.png') });
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.screenshot({ path: testInfo.outputPath('recent-sent-messages-dark.png') });
  await region.press('Escape');
  await expect(region).toHaveCount(0);
});
