import * as fs from 'fs';
import { test, expect } from './fixtures';

const viewports = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 1024, height: 820 },
  { name: 'mobile', width: 430, height: 860 },
] as const;

const sections = ['general', 'desktop', 'connectivity', 'plugins', 'agents', 'hygiene'] as const;

test.describe('Settings visual harness', () => {
  for (const viewport of viewports) {
    test(`captures settings workbench at ${viewport.name} size`, async ({ page, startDaemonWithPRs }, testInfo) => {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await startDaemonWithPRs();
      await page.goto('/');

      await expect(page.locator('.dashboard')).toBeVisible();
      await page.getByTestId('settings-button').click();

      const modal = page.getByTestId('settings-modal');
      await expect(modal).toBeVisible();

      const modalBox = await modal.boundingBox();
      expect(modalBox?.width ?? 0).toBeGreaterThan(Math.min(300, viewport.width - 80));
      expect(modalBox?.height ?? 0).toBeGreaterThan(Math.min(320, viewport.height - 120));

      const body = page.getByTestId('settings-body');
      for (const section of sections) {
        const navItem = modal.getByTestId(`settings-nav-${section}`);
        await navItem.click();
        await expect(navItem).toHaveClass(/\bactive\b/);
        await body.evaluate((node) => {
          node.scrollTop = 0;
        });

        const sectionPath = testInfo.outputPath(`settings-modal-${viewport.name}-${section}.png`);
        await modal.screenshot({ path: sectionPath, animations: 'disabled' });
        expect(fs.existsSync(sectionPath)).toBe(true);
      }

      const agentsNavItem = modal.getByTestId('settings-nav-agents');
      await agentsNavItem.click();
      await expect(agentsNavItem).toHaveClass(/\bactive\b/);
      await body.evaluate((node) => {
        node.scrollTop = node.scrollHeight;
      });

      const bottomPath = testInfo.outputPath(`settings-modal-${viewport.name}-agents-bottom.png`);
      await modal.screenshot({ path: bottomPath, animations: 'disabled' });
      expect(fs.existsSync(bottomPath)).toBe(true);
    });
  }
});
