import { test, expect } from './fixtures';

test.describe('Settings', () => {
  test('muted repos appear in settings modal', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/settings-repo',
      number: 100,
      title: 'Settings Test PR',
      role: 'reviewer',
    });

    await startDaemonWithPRs();
    await page.goto('/');

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Settings Test PR' });
    await expect(prCard).toBeVisible();

    const repoHeader = page.locator('.repo-header').filter({ hasText: 'settings-repo' });
    await repoHeader.locator('.repo-mute-btn').click();

    await expect(prCard).not.toBeVisible();

    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible();
    await modal.getByTestId('settings-nav-hygiene').click();

    const mutedRepoItem = modal.getByTestId('settings-muted-repository-item').filter({ hasText: 'test/settings-repo' });
    await expect(mutedRepoItem).toBeVisible();
  });

  test('unmute repo from settings restores PRs', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/unmute-repo',
      number: 101,
      title: 'Unmute Test PR',
      role: 'reviewer',
    });

    await startDaemonWithPRs();
    await page.goto('/');

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Unmute Test PR' });
    await expect(prCard).toBeVisible();

    const repoHeader = page.locator('.repo-header').filter({ hasText: 'unmute-repo' });
    await repoHeader.locator('.repo-mute-btn').click();
    await expect(prCard).not.toBeVisible();

    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible();
    await modal.getByTestId('settings-nav-hygiene').click();

    const mutedRepoItem = modal.getByTestId('settings-muted-repository-item').filter({ hasText: 'test/unmute-repo' });
    await mutedRepoItem.getByTestId('settings-unmute-repository-button').click();

    await modal.getByTestId('settings-close').click();
    await expect(modal).not.toBeVisible();

    await expect(prCard).toBeVisible();
  });

  test('mute author hides PR and shows in settings', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/author-repo',
      number: 200,
      title: 'Dependabot PR',
      role: 'reviewer',
      author: 'dependabot',
    });

    await startDaemonWithPRs();
    await page.goto('/');

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Dependabot PR' });
    await expect(prCard).toBeVisible();

    const prRow = page.locator('.pr-row').filter({ hasText: 'Dependabot PR' });
    await prRow.hover();

    const muteAuthorBtn = prRow.locator('[data-testid="mute-author-button"]');
    await expect(muteAuthorBtn).toBeVisible();
    await muteAuthorBtn.click();

    await expect(prCard).not.toBeVisible();

    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible();
    await modal.getByTestId('settings-nav-hygiene').click();

    await expect(modal.locator('h3', { hasText: 'Muted Authors' })).toBeVisible();

    const mutedAuthorItem = modal.getByTestId('settings-muted-author-item').filter({ hasText: 'dependabot' });
    await expect(mutedAuthorItem).toBeVisible();
  });

  test('unmute author from settings restores PRs', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/author-repo',
      number: 201,
      title: 'Renovate PR',
      role: 'reviewer',
      author: 'renovate',
    });

    await startDaemonWithPRs();
    await page.goto('/');

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Renovate PR' });
    await expect(prCard).toBeVisible();

    const prRow = page.locator('.pr-row').filter({ hasText: 'Renovate PR' });
    await prRow.hover();
    await prRow.locator('[data-testid="mute-author-button"]').click();
    await expect(prCard).not.toBeVisible();

    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible();
    await modal.getByTestId('settings-nav-hygiene').click();

    const mutedAuthorItem = modal.getByTestId('settings-muted-author-item').filter({ hasText: 'renovate' });
    await mutedAuthorItem.getByTestId('settings-unmute-author-button').click();

    await modal.getByTestId('settings-close').click();
    await expect(modal).not.toBeVisible();

    await expect(prCard).toBeVisible();
  });
});
