import { test, expect } from './fixtures';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

for (const terminal of [false, true]) {
  test(`one-screen launcher browses a repo and opens its existing worktree${terminal ? ' as Terminal' : ''}`, async ({
    page,
    daemon,
  }) => {
    const parent = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-launcher-'));
    const repo = path.join(parent, 'shop');
    const worktree = path.join(parent, 'shop-feature');
    fs.mkdirSync(repo);
    const git = (...args: string[]) => execFileSync('git', args, { cwd: repo });
    git('init', '-b', 'main');
    git('config', 'user.email', 'test@example.com');
    git('config', 'user.name', 'Test');
    git('commit', '--allow-empty', '-m', 'initial');
    git('worktree', 'add', '-b', 'feature', worktree);
    try {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');
      await page.keyboard.press('Meta+,');
      await page.getByRole('searchbox', { name: /search settings/i }).fill('one-screen');
      await page
        .getByRole('navigation', { name: 'Settings sections' })
        .getByRole('button', { name: /^Experimental/ })
        .click();
      await page
        .getByRole('switch', {
          name: 'One-screen new-session launcher (experimental)',
        })
        .click();
      await page.keyboard.press('Escape');
      await page.keyboard.press(terminal ? 'Meta+t' : 'Meta+n');
      const input = page.getByTestId('location-picker-path-input');
      await expect(input).toHaveValue('');
      await expect(page.locator('[data-launcher="one-screen"]')).toBeVisible();
      await input.fill(repo);
      await page.keyboard.press('Tab');
      if (terminal) {
        await expect(page.getByRole('listbox', { name: 'Where' })).not.toBeVisible();
        await input.fill(worktree);
      } else {
        await expect(page.getByRole('listbox', { name: 'Where' })).toBeVisible();
        await input.fill('feature');
        await expect(page.getByRole('option', { selected: true })).toContainText('feature');
      }
      await page.keyboard.press('Enter');
      await expect(page.locator('[data-testid="location-picker-overlay"]')).not.toBeVisible();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible();
    } finally {
      fs.rmSync(parent, { recursive: true, force: true });
    }
  });
}
