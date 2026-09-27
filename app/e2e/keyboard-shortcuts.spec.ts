import { test, expect } from './fixtures';

async function injectLocalSession(
  page: import('@playwright/test').Page,
  session: { id: string; label: string; state: string; cwd?: string; isWorktree?: boolean; branch?: string }
) {
  await page.evaluate((s) => {
    const paneId = `pane-${s.id}`;
    const workspaceId = `workspace-${s.id}`;
    window.__TEST_INJECT_SESSION?.({
      id: s.id,
      label: s.label,
      state: s.state as 'working' | 'waiting_input' | 'idle',
      cwd: s.cwd || '/tmp/test',
      workspaceId,
      ...(s.isWorktree !== undefined ? { isWorktree: s.isWorktree } : {}),
      ...(s.branch ? { branch: s.branch } : {}),
    });
    window.__TEST_SET_SESSION_WORKSPACE?.(s.id, {
      agents: [{ id: paneId, runtimeId: s.id, sessionId: s.id, title: s.label }],
      layoutTree: { type: 'pane', paneId },
    }, paneId);
  }, session);
}

async function createSession(
  page: import('@playwright/test').Page,
  daemon: {
    injectSession: (s: {
      id: string;
      label: string;
      state: string;
      directory?: string;
      workspace_id?: string;
      is_worktree?: boolean;
      branch?: string;
      main_repo?: string;
    }) => Promise<void>;
  },
  session: {
    id: string;
    label: string;
    state: string;
    cwd?: string;
    is_worktree?: boolean;
    branch?: string;
    main_repo?: string;
  }
) {
  const cwd = session.cwd || '/tmp/test';
  await injectLocalSession(page, {
    ...session,
    cwd,
    ...(session.is_worktree !== undefined ? { isWorktree: session.is_worktree } : {}),
  });
  await daemon.injectSession({
    id: session.id,
    label: session.label,
    state: session.state,
    directory: cwd,
    workspace_id: `workspace-${session.id}`,
    is_worktree: session.is_worktree,
    branch: session.branch,
    main_repo: session.main_repo,
  });
}

test.describe('Keyboard Shortcuts', () => {
  test.describe('Terminal Workspace', () => {
    test('⌘D creates a session-backed shell split in the current workspace', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's-shell', label: 'Root', state: 'working', cwd: '/tmp/test/shell-session' });
      await expect(page.locator('[data-testid="session-s-shell"]')).toBeVisible();

      await page.locator('[data-testid="session-s-shell"]').click();
      await expect(page.locator('[data-session-terminal-workspace="workspace-s-shell"]')).toBeVisible();
      await page.locator('.terminal-wrapper.active .terminal-container').click();

      await page.keyboard.press('Meta+d');

      const selectedWorkspaceSessions = page.locator('.workspace-group.selected .session-item');
      await expect(selectedWorkspaceSessions).toHaveCount(2);
      await expect(page.locator('.workspace-group.selected')).toContainText('shell');
      await expect(page.locator('.terminal-wrapper.active [data-pane-kind="agent"]')).toHaveCount(2);
    });

    test('⌘⇧D creates a session-backed horizontal shell split in the current workspace', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's-horizontal', label: 'Root', state: 'working', cwd: '/tmp/test/horizontal-session' });
      await expect(page.locator('[data-testid="session-s-horizontal"]')).toBeVisible();

      await page.locator('[data-testid="session-s-horizontal"]').click();
      await expect(page.locator('[data-session-terminal-workspace="workspace-s-horizontal"]')).toBeVisible();
      await page.locator('.terminal-wrapper.active .terminal-container').click();

      await page.keyboard.press('Meta+Shift+d');

      const selectedWorkspaceSessions = page.locator('.workspace-group.selected .session-item');
      await expect(selectedWorkspaceSessions).toHaveCount(2);
      await expect(page.locator('.workspace-group.selected')).toContainText('shell');
      await expect(page.locator('.terminal-wrapper.active [data-pane-kind="agent"]')).toHaveCount(2);
      await expect(page.locator('.terminal-wrapper.active [data-split-direction="horizontal"]')).toHaveCount(1);
    });

    test('⌘⇧N creates a picked session on a horizontal split in the current workspace', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's-picked-horizontal', label: 'Root', state: 'working', cwd: '/tmp/test/picked-horizontal-session' });
      await expect(page.locator('[data-testid="session-s-picked-horizontal"]')).toBeVisible();

      await page.locator('[data-testid="session-s-picked-horizontal"]').click();
      await expect(page.locator('[data-session-terminal-workspace="workspace-s-picked-horizontal"]')).toBeVisible();

      await page.keyboard.press('Meta+Shift+n');
      await expect(page.locator('.location-picker-overlay')).toBeVisible();
      await expect(page.locator('.picker-title')).toHaveText('New Session Location');
      await expect(page.locator('.workspace-group.selected .session-item')).toHaveCount(1);

      const enabledAgent = page.locator('.agent-option:not(:disabled)').first();
      await expect(enabledAgent).toBeVisible();
      await enabledAgent.click();
      await page.locator('[data-testid="location-picker-path-input"]').fill('/tmp');
      await page.keyboard.press('Enter');

      const selectedWorkspaceSessions = page.locator('.workspace-group.selected .session-item');
      await expect(selectedWorkspaceSessions).toHaveCount(2);
      await expect(page.locator('.workspace-group.selected')).toContainText('tmp');
      await expect(page.locator('.terminal-wrapper.active [data-pane-kind="agent"]')).toHaveCount(2);
      await expect(page.locator('.terminal-wrapper.active [data-split-direction="horizontal"]')).toHaveCount(1);
    });

    test('⌘⇧Z zooms toward the active pane without hiding the others', async ({ page, daemon }) => {
      await daemon.start();
      await page.goto('/');
      await page.waitForSelector('.dashboard');

      await createSession(page, daemon, { id: 's-zoom', label: 'Zoom', state: 'working', cwd: '/tmp/test/zoom' });
      await expect(page.locator('[data-testid="session-s-zoom"]')).toBeVisible();

      await page.locator('[data-testid="session-s-zoom"]').click();
      await expect(page.locator('.terminal-wrapper.active')).toBeVisible();

      await page.evaluate(() => {
        window.__TEST_SET_SESSION_WORKSPACE?.('s-zoom', {
          agents: [
            { id: 'pane-session', runtimeId: 's-zoom', sessionId: 's-zoom', title: 'Zoom' },
            { id: 'pane-shell-1', runtimeId: 'runtime-shell-1', sessionId: 's-zoom', title: 'Shell 1' },
          ],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: 'pane-session' },
              { type: 'pane', paneId: 'pane-shell-1' },
            ],
          },
        }, 'pane-shell-1');
      });
      await expect(page.locator('[data-pane-session-id="s-zoom"][data-pane-id="pane-shell-1"]')).toBeVisible();

      const workspace = page.locator('[data-session-terminal-workspace="workspace-s-zoom"]');
      const mainPane = page.locator('[data-pane-session-id="s-zoom"][data-pane-id="pane-session"]');
      const utilityPane = page.locator('[data-pane-session-id="s-zoom"][data-pane-id="pane-shell-1"]').first();
      const rootSplit = page.locator('[data-split-id="root"]');
      // Dock chips render their key tokens in a styled child span, so match the chip by its label.
      const zoomHint = page.locator('.shortcut-hint', { hasText: 'zoom' });

      await expect(zoomHint).toHaveAttribute('data-active', 'false');

      const mainBefore = await mainPane.boundingBox();
      const utilityBefore = await utilityPane.boundingBox();
      expect(mainBefore?.width).toBeTruthy();
      expect(utilityBefore?.width).toBeTruthy();

      await utilityPane.click();
      await page.keyboard.press('Meta+Shift+z');
      await expect(workspace).toHaveAttribute('data-zoomed-pane-id', 'pane-shell-1');
      await expect(rootSplit).toHaveAttribute('data-split-ratio', '0.240');
      await expect(zoomHint).toHaveAttribute('data-active', 'true');

      await expect.poll(async () => (await utilityPane.boundingBox())?.width ?? 0)
        .toBeGreaterThan(utilityBefore!.width);
      await expect.poll(async () => (await mainPane.boundingBox())?.width ?? 0)
        .toBeLessThan(mainBefore!.width);

      const mainAfterZoom = await mainPane.boundingBox();
      const utilityAfterZoom = await utilityPane.boundingBox();
      expect(mainAfterZoom).not.toBeNull();
      expect(utilityAfterZoom).not.toBeNull();
      expect(utilityAfterZoom!.width).toBeGreaterThan(utilityBefore!.width);
      expect(mainAfterZoom!.width).toBeLessThan(mainBefore!.width);
      await expect(mainPane).toBeVisible();
      await expect(utilityPane).toBeVisible();

      await mainPane.click();
      await expect(workspace).toHaveAttribute('data-zoomed-pane-id', 'pane-session');
      await expect(rootSplit).toHaveAttribute('data-split-ratio', '0.760');
      await expect(zoomHint).toHaveAttribute('data-active', 'true');

      await expect.poll(async () => (await mainPane.boundingBox())?.width ?? 0)
        .toBeGreaterThan(mainAfterZoom!.width);
      await expect.poll(async () => (await utilityPane.boundingBox())?.width ?? 0)
        .toBeLessThan(utilityAfterZoom!.width);

      const mainRetargeted = await mainPane.boundingBox();
      const utilityRetargeted = await utilityPane.boundingBox();
      expect(mainRetargeted).not.toBeNull();
      expect(utilityRetargeted).not.toBeNull();
      expect(mainRetargeted!.width).toBeGreaterThan(mainAfterZoom!.width);
      expect(utilityRetargeted!.width).toBeLessThan(utilityAfterZoom!.width);
    });
  });
});
