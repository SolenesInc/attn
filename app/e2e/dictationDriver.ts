import { expect, type Page } from '@playwright/test';

export async function record(page: Page) {
  const count = await page.evaluate(() => window.__dictation.started.length);
  await page.evaluate(() => window.__dictation.press());
  await expect.poll(() => page.evaluate(() => window.__dictation.started.length)).toBe(count + 1);
  const recording = count + 1;
  await page.evaluate(id => window.__dictation.update({ kind: 'listening', recording: id, at: Date.now() }), recording);
  await expect(page.getByTestId('dictation-pill').filter({ hasText: 'Listening' })).toBeVisible();
  return recording;
}

export async function transcribe(page: Page, recording: number, text: string) {
  await page.evaluate(({ recording, text }) => {
    const now = Date.now();
    window.__dictation.update({ kind: 'stopped', recording, reason: 'shortcut', at: now });
    window.__dictation.update({ kind: 'transcript', recording, text, speech: true, audioMs: 4000, reason: 'shortcut', timeline: { requested: now - 4000, stopped: now, transcribed: now }, helper: {} });
  }, { recording, text });
}

export async function refineRequest(page: Page, index: number) {
  await expect.poll(() => page.evaluate(() => window.__dictation.refines.length)).toBeGreaterThan(index);
  return page.evaluate(i => window.__dictation.refines[i], index);
}

export async function answer(page: Page, requestId: string, outcome: { ok: true; text: string } | { ok: false; error: string }) {
  await page.evaluate(({ requestId, outcome }) => window.__dictation.answer(requestId, outcome), { requestId, outcome });
}
