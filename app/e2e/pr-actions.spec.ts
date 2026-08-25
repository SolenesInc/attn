import { test, expect } from './fixtures';

test.describe('PR Actions', () => {
  test('approve PR via UI', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // PRs must be added before the daemon starts: it polls once on startup.
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 42,
      title: 'Test PR',
      role: 'reviewer',
    });

    const daemonInfo = await startDaemonWithPRs();
    console.log('Daemon started with WS at', daemonInfo.wsUrl);

    await page.goto('/');

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Test PR' });
    await expect(prCard).toBeVisible();

    // The row's action buttons only exist under the CSS hover state.
    await prCard.hover();
    const approveButton = prCard.locator('[data-testid="approve-button"]');
    await approveButton.click();

    await page.waitForTimeout(1000);

    expect(mockGitHub.hasApproveRequest('test/repo', 42)).toBe(true);
  });

  test('merge PR via UI', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 43,
      title: 'Merge Test PR',
      role: 'author',
    });

    const daemonInfo = await startDaemonWithPRs();
    console.log('Daemon started with WS at', daemonInfo.wsUrl);

    await page.goto('/');

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Merge Test PR' });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    const mergeButton = prCard.locator('[data-testid="merge-button"]');
    await mergeButton.click();

    const confirmButton = page.locator('.modal-btn-primary', { hasText: 'Merge' });
    await expect(confirmButton).toBeVisible();
    await confirmButton.click();

    await page.waitForTimeout(1000);

    expect(mockGitHub.hasMergeRequest('test/repo', 43)).toBe(true);
  });

  test('mute PR via UI', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 44,
      title: 'Mute Test PR',
      role: 'reviewer',
    });

    await startDaemonWithPRs();

    await page.goto('/');

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Mute Test PR' });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    const muteButton = prCard.locator('[data-testid="mute-button"]');
    await muteButton.click();

    await expect(prCard).not.toBeVisible();
  });

  test('multiple PRs from same repo', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 50,
      title: 'First PR',
      role: 'reviewer',
    });
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 51,
      title: 'Second PR',
      role: 'author',
    });

    await startDaemonWithPRs();

    await page.goto('/');

    await page.waitForSelector('[data-testid="pr-card"]', { timeout: 15000 });

    const firstPR = page.locator('[data-testid="pr-card"]').filter({ hasText: 'First PR' });
    const secondPR = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Second PR' });

    await expect(firstPR).toBeVisible();
    await expect(secondPR).toBeVisible();

    await firstPR.hover();
    const approveButton = firstPR.locator('[data-testid="approve-button"]');
    await approveButton.click();

    await page.waitForTimeout(1000);

    await expect(secondPR).toBeVisible();

    expect(mockGitHub.hasApproveRequest('test/repo', 50)).toBe(true);
    expect(mockGitHub.hasApproveRequest('test/repo', 51)).toBe(false);
  });

  test('mute repo via UI hides all PRs from that repo', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/mute-repo',
      number: 60,
      title: 'PR to be muted via repo',
      role: 'reviewer',
    });
    mockGitHub.addPR({
      repo: 'test/mute-repo',
      number: 61,
      title: 'Another PR to be muted',
      role: 'author',
    });
    mockGitHub.addPR({
      repo: 'test/other-repo',
      number: 70,
      title: 'PR from other repo',
      role: 'reviewer',
    });

    await startDaemonWithPRs();

    await page.goto('/');

    await page.waitForSelector('[data-testid="pr-card"]', { timeout: 15000 });

    const pr60 = page.locator('[data-testid="pr-card"]').filter({ hasText: 'PR to be muted via repo' });
    const pr61 = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Another PR to be muted' });
    const pr70 = page.locator('[data-testid="pr-card"]').filter({ hasText: 'PR from other repo' });

    await expect(pr60).toBeVisible();
    await expect(pr61).toBeVisible();
    await expect(pr70).toBeVisible();

    const repoHeader = page.locator('.repo-header').filter({ hasText: 'mute-repo' });
    const muteRepoButton = repoHeader.locator('.repo-mute-btn');
    await muteRepoButton.click();

    await expect(pr60).not.toBeVisible();
    await expect(pr61).not.toBeVisible();

    await expect(pr70).toBeVisible();
  });

  test('undo mute PR restores the PR', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 80,
      title: 'Undo Test PR',
      role: 'reviewer',
    });

    await startDaemonWithPRs();

    await page.goto('/');

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Undo Test PR' });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    const muteButton = prCard.locator('[data-testid="mute-button"]');
    await muteButton.click();

    await expect(prCard).not.toBeVisible();

    const undoToast = page.locator('.undo-toast');
    await expect(undoToast).toBeVisible();

    const undoButton = undoToast.locator('.toast-undo-btn');
    await undoButton.click();

    await expect(prCard).toBeVisible();
  });
});
