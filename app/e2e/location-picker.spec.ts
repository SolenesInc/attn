import { test, expect } from './fixtures';
import { execSync } from 'child_process';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';

function createLocationPickerRepo(worktreeBranches: string[]) {
  const parentDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-location-picker-repo-'));
  const repoPath = path.join(parentDir, 'exsin');
  fs.mkdirSync(repoPath);

  execSync('git init -b main', { cwd: repoPath, stdio: 'pipe' });
  execSync('git config user.email "test@example.com"', { cwd: repoPath, stdio: 'pipe' });
  execSync('git config user.name "Test User"', { cwd: repoPath, stdio: 'pipe' });
  fs.writeFileSync(path.join(repoPath, 'README.md'), '# exsin\n');
  execSync('git add README.md', { cwd: repoPath, stdio: 'pipe' });
  execSync('git commit -m "initial"', { cwd: repoPath, stdio: 'pipe' });

  const worktrees = worktreeBranches.map((branch) => {
    const worktreePath = `${repoPath}--${branch}`;
    execSync(`git branch ${branch}`, { cwd: repoPath, stdio: 'pipe' });
    execSync(`git worktree add ${JSON.stringify(worktreePath)} ${branch}`, { cwd: repoPath, stdio: 'pipe' });
    return { branch, path: worktreePath };
  });

  return {
    repoPath,
    worktrees,
    cleanup() {
      fs.rmSync(parentDir, { recursive: true, force: true });
    },
  };
}

test.describe('LocationPicker', () => {
  test.describe('Keyboard Navigation', () => {
    test('arrow keys scroll the selected directory into view', async ({ page, daemon }) => {
      await daemon.start();
      const parentDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-location-picker-scroll-'));
      for (let index = 0; index < 40; index += 1) {
        fs.mkdirSync(path.join(parentDir, `dir-${String(index).padStart(2, '0')}`));
      }

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');
        await page.keyboard.press('Meta+t');
        await expect(page.locator('.location-picker-overlay')).toBeVisible();

        await page.locator('[data-testid="location-picker-path-input"]').focus();
        await page.keyboard.type(`${parentDir}/`);
        const last = page.locator('.picker-item', { hasText: 'dir-39' });
        await expect(last).toBeAttached();
        await expect(last).not.toBeInViewport();

        for (let index = 0; index < 40; index += 1) await page.keyboard.press('ArrowDown');

        const selected = page.locator('.picker-item.selected');
        await expect(selected).toContainText('dir-39');
        await expect(selected).toBeInViewport();
      } finally {
        fs.rmSync(parentDir, { recursive: true, force: true });
      }
    });
  });

  test.describe('Regression Cases', () => {
    test('preselects the exact worktree row for typed worktree paths with and without trailing slash', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-images']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');

        for (const typedPath of [repo.worktrees[0].path, `${repo.worktrees[0].path}/`]) {
          await page.keyboard.press('Meta+t');
          await expect(page.locator('.location-picker-overlay')).toBeVisible();

          const input = page.locator('[data-testid="location-picker-path-input"]');
          await input.focus();
          await page.keyboard.type(typedPath);
          await page.keyboard.press('Enter');

          await expect(page.locator('[data-testid="repo-options"]')).toBeVisible();
          await expect(page.locator('[data-testid="repo-option-1"]')).toHaveClass(/selected/);

          await page.keyboard.press('Escape');
          await expect(page.locator('[data-testid="location-picker-path-input"]')).toBeVisible();
          await page.keyboard.press('Escape');
          await expect(page.locator('.location-picker-overlay')).not.toBeVisible();
        }
      } finally {
        repo.cleanup();
      }
    });

    test('hovering a chooser row does not change the Enter target', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-images']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');
        const cdp = await page.context().newCDPSession(page);
        // Sixfold CPU throttling reproduced the first ArrowDown reaching the body.
        await cdp.send('Emulation.setCPUThrottlingRate', { rate: 6 });
        await page.keyboard.press('Meta+t');
        await expect(page.locator('.location-picker-overlay')).toBeVisible();

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await page.keyboard.type(repo.repoPath);
        await expect(input).toHaveValue(repo.repoPath);
        await page.keyboard.press('Enter');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(input).toBeVisible();
        await page.keyboard.press('Meta+a');
        await page.keyboard.type(repo.repoPath);
        await expect(input).toHaveValue(repo.repoPath);
        await page.keyboard.press('Enter');
        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible();
        // The repo root lands on the create form; step into the destination list.
        await page.keyboard.press('ArrowDown');
        await expect(page.locator('[data-testid="repo-option-0"]')).toHaveClass(/selected/);

        await page.locator('[data-testid="repo-option-1"]').hover();
        await expect(page.locator('[data-testid="repo-option-0"]')).toHaveClass(/selected/);

        await page.keyboard.press('Enter');
        await expect(page.locator('.location-picker-overlay')).not.toBeVisible();
        await expect(page.locator('.session-name', { hasText: 'exsin' }).first()).toBeVisible();
        await expect(page.locator('.session-name', { hasText: 'exsin--feat-images' })).toHaveCount(0);
      } finally {
        repo.cleanup();
      }
    });

    test('creates a new worktree and opens it immediately', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-images']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');
        await page.keyboard.press('Meta+t');
        await expect(page.locator('.location-picker-overlay')).toBeVisible();

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await input.focus();
        await page.keyboard.type(repo.worktrees[0].path);
        await page.keyboard.press('Enter');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible();
        await page.locator('[data-testid="repo-new-worktree-form"]').click();
        await expect(page.locator('[data-testid="repo-new-worktree-input"]')).toBeVisible();
        await expect(page.getByText('Start from feat-images')).toBeVisible();

        await page.locator('[data-testid="repo-new-worktree-input"]').focus();
        await page.keyboard.press('Meta+a');
        await page.keyboard.type('feat-more');
        await page.keyboard.press('Enter');

        await expect(page.locator('.location-picker-overlay')).not.toBeVisible();
        await expect(page.locator('.session-name', { hasText: 'exsin--feat-more' }).first()).toBeVisible();
      } finally {
        repo.cleanup();
      }
    });

    test('creates a worktree from the generated name with a single Enter', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-images']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');
        await page.keyboard.press('Meta+t');
        await expect(page.locator('.location-picker-overlay')).toBeVisible();

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await input.focus();
        await page.keyboard.type(repo.repoPath);
        await page.keyboard.press('Enter');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible();
        const nameField = page.locator('[data-testid="repo-new-worktree-input"]');
        await expect(nameField).toBeFocused();
        const generated = await nameField.inputValue();
        expect(generated).toMatch(/^[a-z]+-[a-z]+$/);

        await page.keyboard.press('Enter');

        await expect(page.locator('.location-picker-overlay')).not.toBeVisible();
        await expect(page.locator('.session-name', { hasText: `exsin--${generated}` }).first()).toBeVisible();
      } finally {
        repo.cleanup();
      }
    });

    test('keeps adjacent selection after deleting a worktree', async ({ page, daemon }) => {
      await daemon.start();
      const repo = createLocationPickerRepo(['feat-a', 'feat-b']);

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');
        await page.keyboard.press('Meta+t');
        await expect(page.locator('.location-picker-overlay')).toBeVisible();

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await input.focus();
        await page.keyboard.type(repo.worktrees[1].path);
        await page.keyboard.press('Enter');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible();
        await expect(page.locator('[data-testid="repo-option-2"]')).toHaveClass(/selected/);

        await page.locator('[data-testid="repo-options"]').press('D');
        await page.locator('[data-testid="repo-options"]').press('y');

        await expect(page.locator('[data-testid="repo-options"]')).toBeVisible();
        await expect(page.locator('[data-testid="repo-option-1"]')).toHaveClass(/selected/);
      } finally {
        repo.cleanup();
      }
    });

    test('does not implicitly open a child directory such as .claude', async ({ page, daemon }) => {
      await daemon.start();
      page.on('dialog', async (dialog) => {
        await dialog.dismiss();
      });

      const parentDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-location-picker-'));
      const projectDir = path.join(parentDir, 'project-with-hidden-child');
      fs.mkdirSync(projectDir);
      fs.mkdirSync(path.join(projectDir, '.claude'));

      try {
        await page.goto('/');
        await page.waitForSelector('.dashboard');

        await page.keyboard.press('Meta+t');
        await expect(page.locator('.location-picker-overlay')).toBeVisible();

        const input = page.locator('[data-testid="location-picker-path-input"]');
        await input.focus();
        await page.keyboard.type(`${projectDir}/`);
        await page.keyboard.press('Enter');

        await expect(page.locator('.location-picker-overlay')).not.toBeVisible();
        await expect(page.locator('.session-name', { hasText: 'project-with-hidden-child' }).first()).toBeVisible();
        await expect(page.locator('.session-name', { hasText: '.claude' })).toHaveCount(0);
      } finally {
        fs.rmSync(parentDir, { recursive: true, force: true });
      }
    });
  });
});
