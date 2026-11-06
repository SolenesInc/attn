import { expect, test, type Page } from '@playwright/test';

const layouts = [
  { frame: 'full', width: 1440 },
  { frame: 'full', width: 1120 },
  { frame: 'full', width: 901 },
  { frame: 'dock', width: 800 },
];
const longOutpost = 'build-farm-eu-north-long-machine-name-07';

async function open(page: Page, frame: string, width: number, chief: boolean, outpost = 'ai-sandbox') {
  await page.setViewportSize({ width, height: 800 });
  const chiefParam = chief ? '&chief' : '';
  await page.goto(`/test-harness/?component=GardenReviewActions&frame=${frame}&outpost=${outpost}${chiefParam}`);
  await page.evaluate(() => window.__HARNESS_READY__);
  await expect(page.getByRole('button', { name: `Resume on ${outpost}` })).toBeVisible();
}

function actionGeometry(page: Page) {
  return page.locator('.garden-review__action-row').evaluate((row) => {
    const box = (element: Element) => {
      const { left, right, top, bottom } = element.getBoundingClientRect();
      return { left, right, top, bottom };
    };
    return [...row.querySelectorAll('button')].map((button) => ({
      name: button.getAttribute('aria-label') ?? button.textContent,
      button: box(button),
      parts: [...button.querySelectorAll('span, svg')].map(box),
    }));
  });
}

async function expectActionsToStayInsideThemselves(page: Page) {
  const actions = await actionGeometry(page);
  for (const [index, action] of actions.entries()) {
    for (const part of action.parts) {
      expect(part.left, `${action.name} left`).toBeGreaterThanOrEqual(action.button.left);
      expect(part.right, `${action.name} right`).toBeLessThanOrEqual(action.button.right);
    }
    for (const other of actions.slice(index + 1)) {
      const apart = other.button.left >= action.button.right || other.button.right <= action.button.left
        || other.button.top >= action.button.bottom || other.button.bottom <= action.button.top;
      expect(apart, `${action.name} and ${other.name} overlap`).toBe(true);
    }
  }
}

async function clickOutpostName(page: Page) {
  const box = await page.locator('.garden-resume-split__name').boundingBox();
  await page.mouse.click(box!.x + box!.width / 2, box!.y + box!.height / 2);
}

for (const { frame, width } of layouts) {
  for (const chief of [true, false]) {
    test(`clicking the outpost name resumes: ${frame} at ${width}px ${chief ? 'with' : 'without'} a chief`, async ({ page }) => {
      await open(page, frame, width, chief);
      await expectActionsToStayInsideThemselves(page);
      await clickOutpostName(page);
      await expect(page.getByTestId('chosen')).toHaveText('resume');
    });
  }

  test(`a long outpost name stays inside Resume: ${frame} at ${width}px`, async ({ page }) => {
    await open(page, frame, width, true, longOutpost);
    await expectActionsToStayInsideThemselves(page);
    await expect(page.getByRole('button', { name: `Resume on ${longOutpost}` })).toHaveAttribute('title', longOutpost);
    await clickOutpostName(page);
    await expect(page.getByTestId('chosen')).toHaveText('resume');
  });
}

test('Resume on an outpost works from the keyboard', async ({ page }) => {
  await open(page, 'full', 1120, true);
  await page.getByRole('button', { name: 'Resume on ai-sandbox' }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('chosen')).toHaveText('resume');
});
