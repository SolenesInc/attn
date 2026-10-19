import { expect, test } from '@playwright/test';

test('queue rows share the tree geometry and reveal actions on hover', async ({ page }) => {
  await page.goto('/test-harness/?component=QueueSidebarFit');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const sidebar = page.getByTestId('queue-sidebar');
  const chief = page.getByTestId('queue-crew-chief-member');
  const awakeCrew = page.getByTestId('queue-crew-alder');
  const turn = page.getByTestId('queue-turn-owed-1');
  const sun = page.getByTestId('queue-crew-wake-birch');
  const actions = page.getByTestId('session-actions-chief').locator('..');

  await expect(sidebar).toHaveCSS('width', '240px');
  await expect(chief).toHaveCSS('display', 'grid');
  await expect(chief).toHaveCSS('height', '32px');
  await expect(turn).toHaveCSS('height', '32px');
  await expect(awakeCrew).toHaveCSS('height', '32px');
  await expect(page.getByTestId('queue-crew-birch')).toHaveCSS('border-left-width', '0px');
  await expect(chief.locator('.session-lead')).toHaveCount(1);
  await expect(chief.locator('.queue-lead-badge')).toHaveText('1');
  await expect(awakeCrew.locator('.queue-lead-badge')).toHaveText('1');
  await expect(sun).toBeVisible();
  await expect(actions).toHaveCSS('opacity', '0');

  await chief.hover();
  await expect(actions).toHaveCSS('opacity', '1');
  await chief.locator('.queue-row-select').focus();
  await expect(actions).toHaveCSS('opacity', '1');
  await expect(page.getByTestId('session-actions-chief')).toHaveCSS('opacity', '1');

  await sun.click();
  await expect(page.getByTestId('queue-crew-birch')).toHaveAttribute('data-crew-wake', 'armed');
  await expect(page.getByTestId('queue-crew-birch').locator('.session-actions'))
    .toHaveCSS('background-image', /linear-gradient/);
});

const rowKinds = {
  desktop: ['sidebar-home', 'chief-slot', 'queue-crew-birch', 'sidebar-desktop-overview', 'sidebar-session-owed-1'],
  queue: ['sidebar-home', 'chief-slot', 'queue-crew-birch', 'queue-crew-alder', 'queue-turn-owed-1'],
};
const rowIcon = '> :not(button)';

for (const flow of ['desktop', 'queue'] as const) {
  for (const chief of ['awake', 'sleeping', 'unconfigured']) {
    test(`${flow} flow rows share one inset and icon column with the chief ${chief}`, async ({ page }) => {
      await page.goto(`/test-harness/?component=QueueSidebarFit&chief=${chief}${flow === 'desktop' ? '&desktop' : ''}`);
      await page.waitForFunction(() => window.__HARNESS__?.ready === true);

      const geometry = async (testId: string) => {
        const slot = page.getByTestId(testId);
        const row = testId === 'chief-slot' ? slot.locator('> .session-item') : slot;
        const box = await row.boundingBox();
        const icon = await row.locator(rowIcon).first().boundingBox();
        return { testId, x: box?.x, width: box?.width, iconX: icon?.x };
      };
      const [first, ...rest] = await Promise.all(rowKinds[flow].map(geometry));
      for (const row of rest) {
        expect({ ...row, testId: first.testId }).toEqual(first);
      }
      if (flow === 'desktop') {
        const header = await page.locator('.desktop-rule').first().boundingBox();
        expect({ x: header?.x, width: header?.width }).toEqual({ x: first.x, width: first.width });
      }
    });
  }
}
