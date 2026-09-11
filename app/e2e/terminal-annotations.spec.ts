import { test, expect, type Page } from '@playwright/test';

async function openEditor(page: Page) {
  await page.goto('/test-harness/?component=TerminalAnnotations');
  await page.waitForFunction(() => window.__HARNESS__?.ready);
  await expect(page.getByTestId('workspace-first').locator('canvas').first()).toBeVisible();
  await page.getByTitle('Edit this annotation').click();
  const editor = page.getByRole('textbox', { name: 'Annotation comment', exact: true });
  await expect(editor).toBeFocused();
  return editor;
}

test('panel editing owns the keyboard through workspace updates', async ({ page }) => {
  const editor = await openEditor(page);
  await editor.pressSequentially('Draft before update.');
  await page.evaluate(() => window.__ANNOTATIONS__.refreshWorkspace());
  await expect(editor).toBeFocused();
  await page.keyboard.type(' Still editing.');
  await expect(editor).toHaveValue('Draft before update. Still editing.');
});

test('message delivery while editing preserves keyboard ownership', async ({ page }) => {
  const editor = await openEditor(page);
  await editor.fill('Unsaved thought');
  const initialCalls = await page.evaluate(() => window.__HARNESS__.getCalls('fetchMessages').length);
  await page.evaluate(() => window.__ANNOTATIONS__.requestMessages());
  await page.waitForFunction(count => window.__HARNESS__.getCalls('fetchMessages').length > count, initialCalls);
  await page.evaluate(() => window.__ANNOTATIONS__.deliverMessages());
  await expect(editor).toBeFocused();
  await page.keyboard.type(' continued');
  await expect(editor).toHaveValue('Unsaved thought continued');
});

test('another workspace can take focus while an annotation draft is open', async ({ page }) => {
  const editor = await openEditor(page);
  await editor.fill('Keep this draft');
  await page.evaluate(() => window.__ANNOTATIONS__.switchWorkspace());
  await expect(page.getByTestId('workspace-second').getByRole('textbox', { name: 'Terminal input' })).toBeFocused();
});

test('pointer focus moves between terminal and editor without losing text', async ({ page }) => {
  const editor = await openEditor(page);
  await editor.fill('Keep this thought');
  const terminal = page.getByTestId('workspace-first').getByRole('textbox', { name: 'Terminal input' });
  await terminal.click({ position: { x: 20, y: 20 } });
  await expect(terminal).toBeFocused();
  await expect(editor).toHaveValue('Keep this thought');
  await page.locator('.anno-popup-quote').click();
  await expect(editor).toBeFocused();
  await page.keyboard.type(' with context');
  await expect(editor).toHaveValue('Keep this thought with context');
});

async function expectPopupWithinPane(page: Page) {
  const popup = await page.getByRole('dialog', { name: 'Edit terminal annotation' }).boundingBox();
  const pane = await page.getByTestId('workspace-first').getByRole('textbox', { name: 'Terminal input' }).boundingBox();
  expect(popup).not.toBeNull();
  expect(pane).not.toBeNull();
  expect(popup!.x).toBeGreaterThanOrEqual(pane!.x);
  expect(popup!.y).toBeGreaterThanOrEqual(pane!.y);
  expect(popup!.x + popup!.width).toBeLessThanOrEqual(pane!.x + pane!.width);
  expect(popup!.y + popup!.height).toBeLessThanOrEqual(pane!.y + pane!.height);
}

test('popup placement respects its pane and panel', async ({ page }) => {
  await openEditor(page);
  await expectPopupWithinPane(page);
  const popup = (await page.getByRole('dialog', { name: 'Edit terminal annotation' }).boundingBox())!;
  const panel = (await page.getByTestId('annotation-panel').boundingBox())!;
  expect(popup.x + popup.width <= panel.x || panel.x + panel.width <= popup.x
    || popup.y + popup.height <= panel.y || panel.y + panel.height <= popup.y).toBe(true);
});

test('pointer dragging preserves the draft and pane bounds', async ({ page }) => {
  const editor = await openEditor(page);
  await editor.fill('A movable thought');
  const handle = page.getByTestId('annotation-popup-drag-handle');
  const before = (await handle.boundingBox())!;
  await page.mouse.move(before.x + before.width / 2, before.y + before.height / 2);
  await page.mouse.down();
  await page.mouse.move(before.x + before.width / 2 + 100, before.y + before.height / 2 - 100, { steps: 5 });
  await page.mouse.up();
  expect(await handle.boundingBox()).not.toEqual(before);
  await expectPopupWithinPane(page);
  await expect(editor).toHaveValue('A movable thought');
});

test('keyboard movement preserves pane bounds', async ({ page }) => {
  await openEditor(page);
  const handle = page.getByTestId('annotation-popup-drag-handle');
  await handle.focus();
  const before = await handle.boundingBox();
  await handle.press('ArrowLeft');
  expect(await handle.boundingBox()).not.toEqual(before);
  await handle.press('ArrowUp');
  await expectPopupWithinPane(page);
});

test('a wrapping comment keeps the remove control beside its row', async ({ page }) => {
  const editor = await openEditor(page);
  const comment = 'Please explain how the parser handles incomplete input before the next chunk arrives. '.repeat(4);
  await editor.fill(comment);
  await page.getByRole('button', { name: 'Comment', exact: true }).click();
  const row = page.locator('.anno-card');
  await expect(row.locator('.anno-card-comment')).toHaveText(comment.trim());
  const card = (await row.boundingBox())!;
  const remove = (await row.getByRole('button', { name: 'Remove annotation', exact: true }).boundingBox())!;
  expect(remove.x).toBeGreaterThanOrEqual(card.x);
  expect(remove.x + remove.width).toBeLessThanOrEqual(card.x + card.width);
  // The packaged regression measured a 5px offset; 12px detects a separate row.
  expect(remove.y - card.y).toBeLessThanOrEqual(12);
  expect(remove.y + remove.height).toBeLessThanOrEqual(card.y + card.height);
  await row.getByTitle('Edit this annotation').click();
  await expect(editor).toHaveValue(comment.trim());
  await expect(row).toHaveCount(1);
});

test('note editing preserves the marks', async ({ page }) => {
  await openEditor(page);
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  const note = page.getByTestId('annotation-note');
  await note.fill('Consider the whole response.');
  await page.evaluate(() => window.__ANNOTATIONS__.refreshWorkspace());
  await expect(note).toBeFocused();
  await expect(note).toHaveValue('Consider the whole response.');
  await expect(page.locator('.anno-card')).toHaveCount(1);
});

test('native pointer receipt ignores synthetic clicks and identifies the delivered target', async ({ page }) => {
  await openEditor(page);
  await page.evaluate(() => {
    window.__ANNOTATIONS__.armPointer('.anno-popup-quote');
    document.body.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
  });
  await page.locator('.anno-popup-quote').click();
  const receipt = await page.evaluate(() => window.__ANNOTATIONS__.waitPointer());
  expect(receipt.matches).toBe(true);
  expect(receipt.target).toContain('anno-popup-quote');
});

test('native pointer receipt reports delivery to the wrong target', async ({ page }) => {
  await openEditor(page);
  await page.evaluate(() => window.__ANNOTATIONS__.armPointer('.anno-popup-quote'));
  const receipt = page.evaluate(() => window.__ANNOTATIONS__.waitPointer());
  await page.getByTestId('workspace-first').getByRole('textbox', { name: 'Terminal input' }).click({ position: { x: 20, y: 20 } });
  expect(await receipt).toMatchObject({ matches: false, target: 'canvas.' });
});
