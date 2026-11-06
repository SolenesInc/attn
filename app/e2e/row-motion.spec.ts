import { test, expect, type Page } from '@playwright/test';

type Screen = { row: string[]; first: number; shown: number; paused?: boolean; shelfOff?: boolean };

async function open(page: Page) {
  await page.goto('/test-harness/?component=RowMotion');
  await page.getByTestId('pane-a').waitFor();
}

const show = (page: Page, screen: Screen) => page.evaluate((next) => window.__rowMotionShow!(next), screen);

const animations = (page: Page) => page.evaluate(() => document.getAnimations().flatMap((animation) => {
  const effect = animation.effect as KeyframeEffect;
  const target = effect.target as HTMLElement;
  const properties = [...new Set(effect.getKeyframes().flatMap((frame) => Object.keys(frame)))]
    .filter((key) => !['offset', 'computedOffset', 'easing', 'composite'].includes(key));
  return properties.map((property) => ({ pane: target.dataset.paneId ?? 'held', property }));
}));

const emptyDesktop: Screen = { row: [], first: 0, shown: 0 };
const onlyTile = (id: string): Screen => ({ row: [id], first: 0, shown: 1 });

const heldPanes = (page: Page) => page.evaluate(() => document.querySelectorAll('.desktop-pane:not([data-pane-id])').length);

async function atRest(page: Page) {
  await expect.poll(() => animations(page)).toEqual([]);
  expect(await heldPanes(page)).toBe(0);
  const rest = await page.evaluate(() => Array.from(document.querySelectorAll<HTMLElement>('.desktop-pane')).map((pane) => {
    const style = getComputedStyle(pane);
    return { transform: style.transform, willChange: style.willChange, zIndex: pane.style.zIndex };
  }));
  for (const pane of rest) expect(pane).toEqual({ transform: 'none', willChange: 'auto', zIndex: '' });
}

test.describe('Row motion', () => {
  test('a slide moves the whole screen with transform only and leaves nothing behind', async ({ page }) => {
    await open(page);
    await show(page, { row: ['a', 'b', 'c', 'd'], first: 1, shown: 2 });
    const running = await animations(page);
    expect(new Set(running.map((animation) => animation.property))).toEqual(new Set(['transform']));
    expect(new Set(running.map((animation) => animation.pane))).toEqual(new Set(['held', 'b', 'c']));
    expect(await heldPanes(page)).toBe(1);
    await atRest(page);
    await expect(page.locator('.desktop-pane')).toHaveCount(2);
  });

  test('a new tile grows and fades in, and a closed tile shrinks and fades out', async ({ page }) => {
    await open(page);
    await show(page, { row: ['a', 'n', 'b', 'c', 'd'], first: 0, shown: 3 });
    const entering = (await animations(page)).filter((animation) => animation.pane === 'n').map((animation) => animation.property);
    expect(entering.sort()).toEqual(['opacity', 'transform']);
    await atRest(page);

    await show(page, { row: ['a', 'b', 'c', 'd'], first: 0, shown: 2 });
    const leaving = (await animations(page)).filter((animation) => animation.pane === 'held').map((animation) => animation.property);
    expect(leaving.sort()).toEqual(['opacity', 'transform']);
    await atRest(page);
  });

  test('a held pane is out of the layout and cannot take input', async ({ page }) => {
    await open(page);
    await show(page, { row: ['a', 'b', 'c', 'd'], first: 1, shown: 2 });
    const held = await page.evaluate(() => {
      const pane = document.querySelector<HTMLElement>('.desktop-pane:not([data-pane-id])')!;
      return { inert: pane.inert, hidden: pane.getAttribute('aria-hidden'), text: pane.textContent };
    });
    expect(held).toEqual({ inert: true, hidden: 'true', text: 'a' });
  });

  test('a divider drag or focus mode never animates', async ({ page }) => {
    await open(page);
    await show(page, { row: ['a', 'b', 'c', 'd'], first: 1, shown: 2, paused: true });
    expect(await animations(page)).toEqual([]);
    expect(await heldPanes(page)).toBe(0);
    await show(page, { row: ['a', 'b', 'c', 'd'], first: 2, shown: 2 });
    expect(await animations(page)).toEqual([]);
    await show(page, { row: ['a', 'b', 'c', 'd'], first: 1, shown: 2 });
    expect((await animations(page)).length).toBeGreaterThan(0);
    await atRest(page);
  });

  test('prefers-reduced-motion skips motion', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await open(page);
    await show(page, { row: ['a', 'b', 'c', 'd'], first: 1, shown: 2 });
    expect(await animations(page)).toEqual([]);
    expect(await heldPanes(page)).toBe(0);
    await show(page, emptyDesktop);
    await show(page, onlyTile('n'));
    expect(await animations(page)).toEqual([]);
    expect(await heldPanes(page)).toBe(0);
  });

  test('a new tile on an emptied desktop enters alone, without the tile that left', async ({ page }) => {
    await open(page);
    await show(page, onlyTile('a'));
    await atRest(page);
    await show(page, emptyDesktop);
    expect(await animations(page)).toEqual([]);
    await show(page, onlyTile('n'));
    expect(await heldPanes(page)).toBe(0);
    const running = await animations(page);
    expect(new Set(running.map((animation) => animation.pane))).toEqual(new Set(['n']));
    expect(running.map((animation) => animation.property).sort()).toEqual(['opacity', 'transform']);
    await atRest(page);
    await expect(page.locator('.desktop-pane')).toHaveCount(1);
  });

  test('a desktop emptied during a transition ends it', async ({ page }) => {
    await open(page);
    await show(page, { row: ['a', 'b'], first: 0, shown: 2 });
    await atRest(page);
    await show(page, onlyTile('b'));
    expect(await heldPanes(page)).toBe(1);
    await show(page, emptyDesktop);
    expect(await animations(page)).toEqual([]);
    await show(page, onlyTile('n'));
    expect(await heldPanes(page)).toBe(0);
    await atRest(page);
  });

  test('a desktop emptied out of view still lets its next tile enter', async ({ page }) => {
    await open(page);
    await show(page, { ...emptyDesktop, paused: true });
    await show(page, emptyDesktop);
    await show(page, onlyTile('n'));
    expect(await heldPanes(page)).toBe(0);
    expect((await animations(page)).map((animation) => animation.property).sort()).toEqual(['opacity', 'transform']);
    await atRest(page);
  });

  test('turning the shelf on shows the tiles without an entrance', async ({ page }) => {
    await open(page);
    await show(page, { row: ['a', 'b'], first: 0, shown: 2 });
    await atRest(page);
    await show(page, { row: ['a', 'b'], first: 0, shown: 2, shelfOff: true });
    await show(page, { row: ['a', 'b'], first: 0, shown: 2 });
    expect(await animations(page)).toEqual([]);
  });

  test('a tile whose terminal never painted can leave the screen', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await open(page);
    await show(page, { row: ['never-painted', 'b', 'c'], first: 0, shown: 2 });
    await page.getByTestId('pane-never-painted').waitFor();
    await show(page, { row: ['never-painted', 'b', 'c'], first: 1, shown: 2 });
    await page.getByTestId('pane-c').waitFor();
    expect(errors).toEqual([]);
    await atRest(page);
  });
});
