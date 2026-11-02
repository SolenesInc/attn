import { test, expect, type Page } from '@playwright/test';
import { answer, record, refineRequest, transcribe } from './dictationDriver';

test.use({ browserName: 'webkit' });

const editor = (page: Page) => page.locator('.cm-content');
const terminal = (page: Page) => page.getByRole('textbox', { name: 'Terminal input' });
const notes = (page: Page) => page.getByRole('textbox', { name: 'Notes' });
const docState = (page: Page) => page.getByTestId('doc-state');
const writes = (page: Page) => page.evaluate(() => window.__targets.ptyWrites());

async function open(page: Page) {
  await page.goto('/test-harness/?component=DictationTargets');
  await expect(editor(page)).toBeVisible();
  await expect(page.locator('[data-terminal-input-ready="1"]')).toHaveCount(1);
}

async function focusTerminal(page: Page) {
  await terminal(page).focus();
  await expect(terminal(page)).toBeFocused();
}

test.describe('editor', () => {
  test('Insert at cursor types raw text at the caret through the editor, then refinement is its own undo step', async ({ page }) => {
    await open(page);
    await editor(page).focus();
    await page.keyboard.press('ControlOrMeta+End');
    const recording = await record(page);
    await transcribe(page, recording, 'um the build is green');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\num the build is green');
    const request = await refineRequest(page, 0);
    expect(request).toMatchObject({ rawText: 'um the build is green', format: 'markdown', profileId: 'profile-a' });
    await answer(page, request.requestId, { ok: true, text: 'The build is green.' });
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\nThe build is green.');
    await expect(editor(page)).toBeFocused();
    await page.keyboard.type('!');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\nThe build is green.!');
    await page.keyboard.press('ControlOrMeta+z');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\nThe build is green.');
    await page.keyboard.press('ControlOrMeta+z');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\num the build is green');
    await page.keyboard.press('ControlOrMeta+z');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\n');
    await expect(page.getByTestId('dictation-layer')).toHaveCount(0);
  });

  test('refinement replaces only its own text while the person types elsewhere in the editor and then works in another field', async ({ page }) => {
    await open(page);
    await editor(page).focus();
    await page.keyboard.press('ControlOrMeta+End');
    const recording = await record(page);
    await page.keyboard.press('ControlOrMeta+Home');
    await page.keyboard.type('> ');
    await transcribe(page, recording, 'so the deploy is done');
    await expect(docState(page)).toHaveText('> # Notes\n\nFirst line.\nso the deploy is done');
    await notes(page).focus();
    await page.keyboard.type('elsewhere');
    const request = await refineRequest(page, 0);
    await answer(page, request.requestId, { ok: true, text: 'The deploy is done.' });
    await expect(docState(page)).toHaveText('> # Notes\n\nFirst line.\nThe deploy is done.');
    await expect(notes(page)).toBeFocused();
    await expect(notes(page)).toHaveValue('elsewhere');
    expect(await notes(page).evaluate((element: HTMLTextAreaElement) => element.selectionStart)).toBe('elsewhere'.length);
  });

  test('editing or undoing the dictated text before refinement answers turns refinement into an offer', async ({ page }) => {
    await open(page);
    await editor(page).focus();
    await page.keyboard.press('ControlOrMeta+End');
    const first = await record(page);
    await transcribe(page, first, 'ship it on friday');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\nship it on friday');
    await page.keyboard.press('ControlOrMeta+z');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\n');
    const request = await refineRequest(page, 0);
    await answer(page, request.requestId, { ok: true, text: 'Ship it on Friday.' });
    await expect(page.getByTestId('dictation-card')).toContainText('You edited the dictated text');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\n');
    await page.getByTestId('dictation-use-refined').click();
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\nShip it on Friday.');
    await expect(editor(page)).toBeFocused();

    const second = await record(page);
    await transcribe(page, second, 'alpha beta');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\nShip it on Friday.alpha beta');
    await page.keyboard.press('Backspace');
    const next = await refineRequest(page, 1);
    await answer(page, next.requestId, { ok: true, text: 'Alpha beta.' });
    await expect(page.getByTestId('dictation-card')).toBeVisible();
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\nShip it on Friday.alpha bet');
  });

  test('Compare first leaves the editor untouched and inserts only the chosen version at the captured caret', async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__dictation.setInteraction('compareFirst'));
    await editor(page).focus();
    await page.keyboard.press('ControlOrMeta+End');
    const recording = await record(page);
    await transcribe(page, recording, 'uh closing note');
    await expect(page.getByTestId('dictation-original')).toHaveText('uh closing note');
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\n');
    const request = await refineRequest(page, 0);
    await answer(page, request.requestId, { ok: true, text: 'Closing note.' });
    await page.getByTestId('dictation-use-original').click();
    await expect(docState(page)).toHaveText('# Notes\n\nFirst line.\nuh closing note');
    await expect(page.getByTestId('dictation-card')).toHaveCount(0);
  });
});

test.describe('terminal', () => {
  test('Insert at cursor waits for refined text and pastes it once, with no submit key', async ({ page }) => {
    await open(page);
    await focusTerminal(page);
    const recording = await record(page);
    await transcribe(page, recording, 'um list the files');
    await expect(page.getByTestId('dictation-pill')).toContainText('Enter pastes the original');
    const request = await refineRequest(page, 0);
    expect(request).toMatchObject({ format: 'plain' });
    expect(await writes(page)).toEqual([]);
    await answer(page, request.requestId, { ok: true, text: 'List the files.' });
    await expect.poll(() => writes(page)).toEqual([{ runtime: 'term-1', data: 'List the files.' }]);
    await expect(page.getByTestId('dictation-layer')).toHaveCount(0);
    await expect(terminal(page)).toBeFocused();
  });

  test('Enter in the captured terminal pastes the original once, is not sent, and the late refinement is ignored', async ({ page }) => {
    await open(page);
    await focusTerminal(page);
    const recording = await record(page);
    await transcribe(page, recording, 'git status');
    const request = await refineRequest(page, 0);
    await page.keyboard.press('Enter');
    await expect.poll(() => writes(page)).toEqual([{ runtime: 'term-1', data: 'git status' }]);
    expect(await page.evaluate(() => window.__dictation.refineCancels)).toEqual([request.requestId]);
    await answer(page, request.requestId, { ok: true, text: 'Git status.' });
    await page.keyboard.press('Enter');
    await expect.poll(() => writes(page)).toEqual([{ runtime: 'term-1', data: 'git status' }, { runtime: 'term-1', data: '\r' }]);
  });

  test('Enter elsewhere keeps its normal meaning, and the pill still pastes the original into the captured terminal', async ({ page }) => {
    await open(page);
    await focusTerminal(page);
    const recording = await record(page);
    await transcribe(page, recording, 'echo hi');
    await refineRequest(page, 0);
    await notes(page).focus();
    await page.keyboard.type('a');
    await page.keyboard.press('Enter');
    await page.keyboard.type('b');
    await expect(notes(page)).toHaveValue('a\nb');
    expect(await writes(page)).toEqual([]);
    await page.getByTestId('dictation-paste-original').click();
    await expect.poll(() => writes(page)).toEqual([{ runtime: 'term-1', data: 'echo hi' }]);
    await expect(notes(page)).toBeFocused();
  });

  test('Enter racing the refinement answer pastes exactly one result', async ({ page }) => {
    await open(page);
    await focusTerminal(page);
    const recording = await record(page);
    await transcribe(page, recording, 'make test');
    const request = await refineRequest(page, 0);
    await page.evaluate(id => {
      window.__dictation.answer(id, { ok: true, text: 'Make test.' });
      document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true }));
    }, request.requestId);
    await expect.poll(() => writes(page).then(list => list.length)).toBe(1);
    await expect(page.getByTestId('dictation-layer')).toHaveCount(0);
    const [write] = await writes(page);
    expect(['make test', 'Make test.']).toContain(write.data);
  });

  test('pastes use the terminal’s current paste mode: bracketed when the program asks, newlines as CR otherwise', async ({ page }) => {
    await open(page);
    await focusTerminal(page);
    const first = await record(page);
    await transcribe(page, first, 'line one\nline two');
    const plain = await refineRequest(page, 0);
    await answer(page, plain.requestId, { ok: true, text: 'Line one.\nLine two.' });
    await expect.poll(() => writes(page)).toEqual([{ runtime: 'term-1', data: 'Line one.\rLine two.' }]);

    await page.evaluate(() => window.__targets.output('\x1b[?2004h'));
    const second = await record(page);
    await transcribe(page, second, 'alpha\nbeta');
    const bracketed = await refineRequest(page, 1);
    await answer(page, bracketed.requestId, { ok: true, text: 'Alpha.\nBeta.' });
    await expect.poll(() => writes(page).then(list => list[1])).toEqual({ runtime: 'term-1', data: '\x1b[200~Alpha.\rBeta.\x1b[201~' });
  });

  test('a terminal whose runtime was replaced or whose outpost went offline receives nothing, and the text is offered', async ({ page }) => {
    await open(page);
    await focusTerminal(page);
    const first = await record(page);
    await transcribe(page, first, 'old runtime text');
    const replaced = await refineRequest(page, 0);
    await page.evaluate(() => window.__targets.replaceRuntime());
    await expect(page.locator('[data-terminal-input-ready="1"]')).toHaveCount(1);
    await answer(page, replaced.requestId, { ok: true, text: 'Old runtime text.' });
    await expect(page.getByTestId('dictation-card')).toContainText('nothing was pasted');
    expect(await writes(page)).toEqual([]);
    await page.getByTestId('dictation-discard').click();

    await focusTerminal(page);
    const second = await record(page);
    await transcribe(page, second, 'offline text');
    const offline = await refineRequest(page, 1);
    await page.evaluate(() => window.__targets.setReach('unreachable'));
    await answer(page, offline.requestId, { ok: true, text: 'Offline text.' });
    await expect(page.getByTestId('dictation-card')).toContainText('nothing was pasted');
    expect(await writes(page)).toEqual([]);
  });

  test('Compare first shows the choice card and pastes only the chosen version into the captured terminal', async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__dictation.setInteraction('compareFirst'));
    await focusTerminal(page);
    const recording = await record(page);
    await transcribe(page, recording, 'uh deploy now');
    const request = await refineRequest(page, 0);
    await page.keyboard.press('Enter');
    await expect.poll(() => writes(page)).toEqual([{ runtime: 'term-1', data: '\r' }]);
    await answer(page, request.requestId, { ok: true, text: 'Deploy now.' });
    await notes(page).focus();
    await page.getByTestId('dictation-use-refined').click();
    await expect.poll(() => writes(page)).toEqual([{ runtime: 'term-1', data: '\r' }, { runtime: 'term-1', data: 'Deploy now.' }]);
    await expect(notes(page)).toHaveValue('');
  });

  test('cancelling while the terminal waits pastes nothing and stops the refinement', async ({ page }) => {
    await open(page);
    await focusTerminal(page);
    const recording = await record(page);
    await transcribe(page, recording, 'rm nothing');
    const request = await refineRequest(page, 0);
    await page.getByTestId('dictation-cancel').click();
    expect(await page.evaluate(() => window.__dictation.refineCancels)).toEqual([request.requestId]);
    await answer(page, request.requestId, { ok: true, text: 'Rm nothing.' });
    await page.keyboard.press('Enter');
    await expect.poll(() => writes(page)).toEqual([{ runtime: 'term-1', data: '\r' }]);
  });
});
