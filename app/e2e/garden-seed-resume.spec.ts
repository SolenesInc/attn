import { expect, test, type Page } from '@playwright/test';

const reasonFor = (outpost: string) => `outpost "${outpost}" is unreachable; Resume returns when it is back`;
const longOutpost = 'build-farm-eu-north-long-machine-name-07';

async function tabUntilFocused(page: Page, target: ReturnType<Page['locator']>) {
  const stops = await page.locator('button, input, [tabindex="0"]').count();
  for (let stop = 0; stop <= stops; stop += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) return;
    await page.keyboard.press('Tab');
  }
  await expect(target).toBeFocused();
}

async function openSeedWithKeyboard(page: Page, frame: string, width: number, outpost = 'studio') {
  await page.setViewportSize({ width, height: 800 });
  await page.goto(`/test-harness/?component=GardenSeedResume&frame=${frame}&outpost=${outpost}`);
  await page.evaluate(() => window.__HARNESS_READY__);
  await tabUntilFocused(page, page.locator('[data-seed-row="s-outpost"]'));
  await page.keyboard.press('Enter');
  const resume = page.getByRole('button', { name: `Resume on ${outpost}` });
  await expect(resume).toBeVisible();
  return resume;
}

for (const { frame, width } of [{ frame: 'full', width: 1120 }, { frame: 'dock', width: 800 }]) {
  test(`Tab reaches the dimmed Resume and it says why: ${frame} at ${width}px`, async ({ page }) => {
    const resume = await openSeedWithKeyboard(page, frame, width);
    const reason = page.getByRole('tooltip');
    await expect(reason).toBeHidden();

    await tabUntilFocused(page, resume);

    await expect(reason).toBeVisible();
    await expect(reason).toHaveText(reasonFor('studio'));
    await expect(resume).toHaveAccessibleDescription(reasonFor('studio'));

    await page.keyboard.press('Enter');
    await page.keyboard.press('Space');
    await expect(page.getByTestId('resumes')).toHaveText('0');

    await page.keyboard.press('Tab');
    await expect(reason).toBeHidden();
  });

  test(`a long reason stays inside the panel: ${frame} at ${width}px`, async ({ page }) => {
    const resume = await openSeedWithKeyboard(page, frame, width, longOutpost);
    await tabUntilFocused(page, resume);
    const reason = page.getByRole('tooltip');
    await expect(reason).toBeVisible();
    const box = await reason.boundingBox();
    const panel = await page.getByRole('region', { name: 'The garden' }).boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(panel!.x);
    expect(box!.x + box!.width).toBeLessThanOrEqual(panel!.x + panel!.width);
    await page.screenshot({ path: test.info().outputPath('reason.png') });
  });
}
