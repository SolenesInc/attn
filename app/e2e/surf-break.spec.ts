import { expect, test, type Page } from '@playwright/test';

declare global {
  interface Window { surfAudioContexts: AudioContext[]; surfPaints: number }
}

test.setTimeout(60000);

async function openSurf(page: Page, paddleOut = false) {
  await page.addInitScript(() => {
    window.surfAudioContexts = [];
    window.surfPaints = 0;
    const OriginalAudioContext = window.AudioContext;
    window.AudioContext = class extends OriginalAudioContext {
      constructor(options?: AudioContextOptions) { super(options); window.surfAudioContexts.push(this); }
    };
    const fill = CanvasRenderingContext2D.prototype.fillRect;
    CanvasRenderingContext2D.prototype.fillRect = function (...args) {
      if (this.canvas.closest('.surf-break')) window.surfPaints++;
      return fill.apply(this, args);
    };
  });
  await page.goto('/test-harness/?component=SurfBreak');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await page.bringToFront();
  await page.getByRole('button', { name: 'Into the water' }).click();
  await expect(page.getByRole('dialog')).toHaveAttribute('data-playing', 'true');
  if (paddleOut) {
    await page.keyboard.down('ArrowRight');
    await expect.poll(async () => Number(await page.locator('canvas').getAttribute('data-world-x')), { timeout: 30000 }).toBeGreaterThan(650);
    await page.keyboard.up('ArrowRight');
  }
}

test('surfing survives incoming work and returns only on request', async ({ page }) => {
  await openSurf(page, true);
  await expect.poll(() => page.evaluate(() => window.surfAudioContexts[0]?.state)).toBe('running');
  const shirtTop = () => page.locator('canvas').evaluate(canvas => {
    const { data } = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height);
    for (let i = 0; i < data.length; i += 4) {
      if (data[i] === 233 && data[i + 1] === 119 && data[i + 2] === 103) return Math.floor(i / 4 / canvas.width);
    }
    throw new Error('Surfer shirt was not drawn');
  });
  await page.keyboard.down('ArrowLeft');
  await expect(page.getByRole('button', { name: 'f stand up' })).toBeEnabled();
  await page.keyboard.press('f');
  await expect(page.locator('canvas')).toHaveAttribute('data-posture', 'standing');
  const ridingTop = await shirtTop();
  await page.keyboard.press('Space');
  await expect.poll(shirtTop).toBeLessThan(ridingTop - 8);
  await page.keyboard.down('ArrowLeft');
  await expect.poll(() => page.evaluate(() => window.surfPaints)).toBeGreaterThan(8000);
  await page.keyboard.up('ArrowLeft');
  await page.evaluate(() => window.__HARNESS__.triggerRerender());
  await expect(page.getByRole('button', { name: '1 agent waiting' })).toBeVisible();
  await expect(page.getByRole('dialog')).toHaveAttribute('data-playing', 'true');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(await page.evaluate(() => window.__HARNESS__.getCalls('onReturnToWaiting'))).toHaveLength(1);
  await expect.poll(() => page.evaluate(() => window.surfAudioContexts.every(ctx => ctx.state === 'closed'))).toBe(true);
});

test('pause, mute, focus events and exit stop their resources', async ({ page }) => {
  await openSurf(page);
  await page.keyboard.press('p');
  await expect(page.getByRole('dialog')).toHaveAttribute('data-playing', 'false');
  await expect.poll(() => page.evaluate(() => window.surfAudioContexts[0]?.state)).toBe('suspended');
  const paints = await page.evaluate(() => window.surfPaints);
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  expect(await page.evaluate(() => window.surfPaints)).toBe(paints);
  await page.keyboard.press('p');
  await page.keyboard.press('m');
  await expect(page.getByRole('button', { name: 'Sound off' })).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.surfAudioContexts[0]?.state)).toBe('suspended');
  await page.keyboard.press('m');
  await expect.poll(() => page.evaluate(() => window.surfAudioContexts[0]?.state)).toBe('running');
  await page.evaluate(() => window.dispatchEvent(new Event('blur')));
  await expect(page.getByRole('dialog')).toHaveAttribute('data-playing', 'false');
  await expect.poll(() => page.evaluate(() => window.surfAudioContexts[0]?.state)).toBe('suspended');
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(page.getByRole('dialog')).toHaveAttribute('data-playing', 'true');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => window.surfAudioContexts.every(ctx => ctx.state === 'closed'))).toBe(true);
  const closedPaints = await page.evaluate(() => window.surfPaints);
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  expect(await page.evaluate(() => window.surfPaints)).toBe(closedPaints);
  await page.getByRole('button', { name: 'Go surfing' }).click();
  await expect(page.getByRole('button', { name: 'Into the water' })).toBeVisible();
});

test('dive holds the surfer underwater and releasing floats them back up', async ({ page }) => {
  await openSurf(page, true);
  const canvas = page.locator('canvas');
  await page.keyboard.down('ShiftLeft');
  await expect(canvas).toHaveAttribute('data-surf-state', 'submerged');
  await expect.poll(async () => Number(await canvas.getAttribute('data-depth'))).toBeGreaterThan(30);
  await page.keyboard.up('ShiftLeft');
  await expect(canvas).toHaveAttribute('data-surf-state', 'floating');
  await expect.poll(async () => Number(await canvas.getAttribute('data-depth'))).toBeLessThan(3);
  const dive = page.getByRole('button', { name: 'Hold to dive, release to float up' });
  const bounds = (await dive.boundingBox())!;
  await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
  await page.mouse.down();
  await expect(canvas).toHaveAttribute('data-surf-state', 'submerged');
  await page.mouse.move(30, 30);
  await page.mouse.up();
  await expect.poll(async () => Number(await canvas.getAttribute('data-depth'))).toBeLessThan(4);
  await page.keyboard.down('ShiftRight');
  await expect(canvas).toHaveAttribute('data-surf-state', 'submerged');
  await page.keyboard.press('p');
  await page.keyboard.up('ShiftRight');
  await page.keyboard.press('p');
  await expect.poll(async () => Number(await canvas.getAttribute('data-depth'))).toBeLessThan(4);
});
