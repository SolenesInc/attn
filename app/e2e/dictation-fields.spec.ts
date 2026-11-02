import { test, expect, type Page } from '@playwright/test';
import { answer, record, refineRequest, transcribe } from './dictationDriver';

test.use({ browserName: 'webkit' });

const message = (page: Page) => page.getByRole('textbox', { name: 'Message' });
const search = (page: Page) => page.getByRole('textbox', { name: 'Search' });
const state = (page: Page, id: string) => page.getByTestId(id);

async function open(page: Page) {
  await page.goto('/test-harness/?component=DictationFields');
  await expect(message(page)).toBeVisible();
}

async function typeInto(page: Page, field: ReturnType<typeof message>, text: string, caret?: number) {
  await field.focus();
  if (caret !== undefined) await field.evaluate((element: HTMLTextAreaElement, at) => element.setSelectionRange(at, at), caret);
  await page.keyboard.type(text);
}

test('Insert at cursor puts the transcript where dictation started, through the field’s own change handler, while the user keeps typing elsewhere', async ({ page }) => {
  await open(page);
  await typeInto(page, message(page), 'Dear team, . Thanks');
  await message(page).evaluate((element: HTMLTextAreaElement) => element.setSelectionRange(11, 11));
  const changesBefore = Number(await state(page, 'message-changes').textContent());
  const recording = await record(page);
  await page.keyboard.press('Meta+ArrowLeft');
  await page.keyboard.type('>> ');
  await transcribe(page, recording, 'the build is green');
  await expect(message(page)).toHaveValue('>> Dear team, the build is green. Thanks');
  await expect(state(page, 'message-state')).toHaveText('>> Dear team, the build is green. Thanks');
  expect(Number(await state(page, 'message-changes').textContent())).toBeGreaterThan(changesBefore + 3);
  await expect(message(page)).toBeFocused();
  expect(await message(page).evaluate((element: HTMLTextAreaElement) => element.selectionStart)).toBe(3);
  const request = await refineRequest(page, 0);
  expect(request).toMatchObject({ rawText: 'the build is green', recordingDurationMs: 4000, profileId: 'profile-a' });
  await expect.poll(() => page.evaluate(() => window.__dictation.receipts().length)).toBe(1);
});

test('refinement replaces only its own text, keeping outside edits, the caret and focus elsewhere', async ({ page }) => {
  await open(page);
  await typeInto(page, message(page), 'Note: ');
  const recording = await record(page);
  await transcribe(page, recording, 'um so the the deploy is done');
  await expect(message(page)).toHaveValue('Note: um so the the deploy is done');
  await page.keyboard.type('!');
  await search(page).focus();
  await page.keyboard.type('query');
  const request = await refineRequest(page, 0);
  await answer(page, request.requestId, { ok: true, text: 'The deploy is done.' });
  await expect(message(page)).toHaveValue('Note: The deploy is done.!');
  await expect(state(page, 'message-state')).toHaveText('Note: The deploy is done.!');
  await expect(search(page)).toBeFocused();
  await expect(search(page)).toHaveValue('query');
  await expect(page.getByTestId('dictation-layer')).toHaveCount(0);
});

test('an edit inside the dictated text turns the refinement into an offer and never overwrites it', async ({ page }) => {
  await open(page);
  await message(page).focus();
  const recording = await record(page);
  await transcribe(page, recording, 'ship it on friday');
  await expect(message(page)).toHaveValue('ship it on friday');
  await message(page).evaluate((element: HTMLTextAreaElement) => element.setSelectionRange(8, 10));
  await page.keyboard.type('before');
  await expect(message(page)).toHaveValue('ship it before friday');
  const request = await refineRequest(page, 0);
  await answer(page, request.requestId, { ok: true, text: 'Ship it on Friday.' });
  const card = page.getByTestId('dictation-card');
  await expect(card).toContainText('You edited the dictated text');
  await expect(message(page)).toHaveValue('ship it before friday');
  await message(page).evaluate((element: HTMLTextAreaElement) => element.setSelectionRange(element.value.length, element.value.length));
  await page.getByTestId('dictation-use-refined').click();
  await expect(message(page)).toHaveValue('ship it before fridayShip it on Friday.');
  await expect(message(page)).toBeFocused();
});

test('deleting the transcript before refinement answers turns the refinement into an offer', async ({ page }) => {
  await open(page);
  await typeInto(page, message(page), 'Hi ');
  const recording = await record(page);
  await transcribe(page, recording, 'there friend');
  await expect(message(page)).toHaveValue('Hi there friend');
  for (let index = 0; index < 'there friend'.length; index++) await page.keyboard.press('Backspace');
  await expect(message(page)).toHaveValue('Hi ');
  const request = await refineRequest(page, 0);
  await answer(page, request.requestId, { ok: true, text: 'there, friend' });
  await expect(page.getByTestId('dictation-card')).toBeVisible();
  await expect(message(page)).toHaveValue('Hi ');
});

test('Compare first leaves the field untouched until the user picks a version, then inserts it where dictation started', async ({ page }) => {
  await open(page);
  await page.evaluate(() => window.__dictation.setInteraction('compareFirst'));
  await typeInto(page, message(page), 'A B');
  await message(page).evaluate((element: HTMLTextAreaElement) => element.setSelectionRange(2, 2));
  const recording = await record(page);
  await transcribe(page, recording, 'uh middle');
  const card = page.getByTestId('dictation-card');
  await expect(page.getByTestId('dictation-original')).toHaveText('uh middle');
  await expect(page.getByTestId('dictation-refined')).toHaveText('Refining…');
  await expect(message(page)).toHaveValue('A B');
  const request = await refineRequest(page, 0);
  await answer(page, request.requestId, { ok: true, text: 'middle ' });
  await expect(card.locator('del')).toHaveText('uh ');
  await expect(message(page)).toHaveValue('A B');
  await page.getByTestId('dictation-use-refined').click();
  await expect(message(page)).toHaveValue('A middle B');
  await expect(state(page, 'message-state')).toHaveText('A middle B');
  await expect(card).toHaveCount(0);
});

test('refinement failures show their text, Retry asks again with a fresh request, Keep original cancels and a late answer is ignored', async ({ page }) => {
  await open(page);
  await message(page).focus();
  const recording = await record(page);
  await transcribe(page, recording, 'raw words');
  const first = await refineRequest(page, 0);
  await answer(page, first.requestId, { ok: false, error: 'Refinement did not answer within 16 s for this 4 s recording. Original kept.' });
  await expect(page.getByTestId('dictation-pill')).toHaveText(/Refinement did not answer within 16 s for this 4 s recording\. Original kept\./);
  await expect(message(page)).toHaveValue('raw words');
  await page.getByTestId('dictation-retry').click();
  const second = await refineRequest(page, 1);
  expect(second.requestId).not.toBe(first.requestId);
  await page.getByTestId('dictation-keep-original').click();
  expect(await page.evaluate(() => window.__dictation.refineCancels)).toEqual([second.requestId]);
  await answer(page, second.requestId, { ok: true, text: 'Refined words.' });
  await expect(message(page)).toHaveValue('raw words');
  await expect(page.getByTestId('dictation-layer')).toHaveCount(0);
  expect(JSON.parse((await state(page, 'sent').textContent())!)).toEqual([]);
});

test('a replaced field gets an offer instead of an insert, and a profile change discards pending dictation', async ({ page }) => {
  await open(page);
  await message(page).focus();
  const first = await record(page);
  await page.getByRole('button', { name: 'Replace message field' }).click();
  await transcribe(page, first, 'lost target');
  await expect(page.getByTestId('dictation-card')).toContainText('nothing was inserted');
  await expect(message(page)).toHaveValue('');
  await message(page).focus();
  await page.getByTestId('dictation-use-original').click();
  await expect(message(page)).toHaveValue('lost target');
  await search(page).focus();
  const second = await record(page);
  await page.evaluate(() => window.__dictation.setProfile('profile-b'));
  await expect(page.getByTestId('dictation-layer')).toHaveCount(0);
  expect(await page.evaluate(() => window.__dictation.canceled)).toContain(second);
});

test('a transcript that arrives during IME composition waits until the composition ends', async ({ page }) => {
  await open(page);
  await typeInto(page, message(page), 'x');
  const recording = await record(page);
  await message(page).dispatchEvent('compositionstart', { data: '' });
  await transcribe(page, recording, 'later');
  await expect(page.getByTestId('dictation-pill')).toHaveText(/Transcribing/);
  await expect(message(page)).toHaveValue('x');
  await message(page).dispatchEvent('compositionend', { data: '' });
  await expect(message(page)).toHaveValue('xlater');
});

test('Escape cancels a recording without inserting anything, leaving the field or closing what was open', async ({ page }) => {
  await open(page);
  await typeInto(page, message(page), 'Draft');
  await page.getByRole('button', { name: 'Open panel' }).click();
  await expect(message(page)).toBeFocused();
  const recording = await record(page);
  await page.keyboard.press('Escape');
  await expect.poll(() => page.evaluate(() => window.__dictation.canceled)).toContain(recording);
  await page.evaluate(id => window.__dictation.update({ kind: 'canceled', recording: id }), recording);
  await expect(page.getByTestId('dictation-pill')).toHaveCount(0);
  await expect(message(page)).toHaveValue('Draft');
  await expect(message(page)).toBeFocused();
  await expect(page.getByRole('region', { name: 'Panel' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('region', { name: 'Panel' })).toHaveCount(0);
});

test('dictation explains a missing target instead of recording', async ({ page }) => {
  await open(page);
  await page.getByRole('textbox', { name: 'Read-only' }).focus();
  await page.evaluate(() => window.__dictation.press());
  await expect(page.getByTestId('dictation-pill')).toHaveText(/read-only/);
  expect(await page.evaluate(() => window.__dictation.started)).toEqual([]);
});

test('a second dictation records while the first still refines, and each refinement lands in its own text', async ({ page }) => {
  await open(page);
  await message(page).focus();
  const first = await record(page);
  await transcribe(page, first, 'alpha one');
  await page.keyboard.type(' | ');
  const second = await record(page);
  await transcribe(page, second, 'beta two');
  await expect(message(page)).toHaveValue('alpha one | beta two');
  const [a, b] = [await refineRequest(page, 0), await refineRequest(page, 1)];
  await answer(page, b.requestId, { ok: true, text: 'Beta 2.' });
  await answer(page, a.requestId, { ok: true, text: 'Alpha 1.' });
  await expect(message(page)).toHaveValue('Alpha 1. | Beta 2.');
  await expect(state(page, 'message-state')).toHaveText('Alpha 1. | Beta 2.');
});

test.describe('browser page fields', () => {
  test('a press in a browser tile captures the page’s focused field through the browser host, inserts raw text and then refines it there', async ({ page }) => {
    await open(page);
    await typeInto(page, search(page), 'find ');
    await page.evaluate(() => window.__dictation.press('browser-tile'));
    await expect.poll(() => page.evaluate(() => window.__dictation.started.length)).toBe(1);
    await page.evaluate(() => window.__dictation.update({ kind: 'listening', recording: 1, at: Date.now() }));
    await transcribe(page, 1, 'um open tickets');
    await expect(search(page)).toHaveValue('find um open tickets');
    await expect(state(page, 'search-state')).toHaveText('find um open tickets');
    const request = await refineRequest(page, 0);
    expect(request.format).toBe('plain');
    await answer(page, request.requestId, { ok: true, text: 'open tickets' });
    await expect(search(page)).toHaveValue('find open tickets');
    await expect(page.getByTestId('dictation-layer')).toHaveCount(0);
    expect(await page.evaluate(() => window.__dictation.browserCalls)).toEqual([
      { label: 'browser-tile', action: 'capture' },
      { label: 'browser-tile', action: 'replace' },
      { label: 'browser-tile', action: 'replace' },
      { label: 'browser-tile', action: 'release' },
    ]);
  });

  test('a browser page with no writable field focused explains why and does not record', async ({ page }) => {
    await open(page);
    await page.getByRole('textbox', { name: 'Read-only' }).focus();
    await page.evaluate(() => window.__dictation.press('browser-tile'));
    await expect(page.getByTestId('dictation-pill')).toHaveText(/read-only/);
    expect(await page.evaluate(() => window.__dictation.started)).toEqual([]);
  });

  test('a page that navigated or replaced its frame gets an offer instead of an insert', async ({ page }) => {
    await open(page);
    await message(page).focus();
    await page.evaluate(() => window.__dictation.press('browser-tile'));
    await expect.poll(() => page.evaluate(() => window.__dictation.started.length)).toBe(1);
    await page.evaluate(() => { window.__dictation.browserGone = true; });
    await transcribe(page, 1, 'lost page');
    await expect(page.getByTestId('dictation-card')).toContainText('nothing was inserted');
    await expect(message(page)).toHaveValue('');
  });
});
