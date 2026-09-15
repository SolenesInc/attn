import { test, expect } from './fixtures';

type WorkspaceSessionFixture = {
  id: string;
  label: string;
  paneId: string;
  cwd: string;
};

async function injectWorkspace(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (session: { id: string; label: string; agent?: 'shell'; state: string; directory?: string; workspace_id?: string }) => Promise<void> },
  workspaceId: string,
  sessions: WorkspaceSessionFixture[],
  activePaneId = sessions[0]?.paneId || ''
) {
  await page.evaluate(({ workspaceId, sessions, activePaneId }) => {
    for (const session of sessions) {
      window.__TEST_INJECT_SESSION?.({
        id: session.id,
        label: session.label,
        state: 'working',
        cwd: session.cwd,
        agent: 'shell',
        workspaceId,
      });
    }

    const workspace = {
      agents: sessions.map((session) => ({
        id: session.paneId,
        runtimeId: session.id,
        sessionId: session.id,
        title: session.label,
      })),
      layoutTree: sessions.length === 1
        ? { type: 'pane', paneId: sessions[0].paneId }
        : {
            type: 'split',
            splitId: `${workspaceId}-root`,
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: sessions[0].paneId },
              { type: 'pane', paneId: sessions[1].paneId },
            ],
          },
    };

    for (const session of sessions) {
      window.__TEST_SET_SESSION_WORKSPACE?.(session.id, workspace, activePaneId);
    }
  }, { workspaceId, sessions, activePaneId });

  for (const session of sessions) {
    await daemon.injectSession({
      id: session.id,
      label: session.label,
      agent: 'shell',
      state: 'working',
      directory: session.cwd,
      workspace_id: workspaceId,
    });
  }
}

test.describe('Workspace Sessions', () => {
  test('leaves agent focus mode when selecting another workspace', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectWorkspace(page, daemon, 'workspace-a', [
      { id: 'a1', label: 'alpha-one', paneId: 'pane-a1', cwd: '/tmp/workspace-a' },
    ]);
    await injectWorkspace(page, daemon, 'workspace-b', [
      { id: 'b1', label: 'beta-one', paneId: 'pane-b1', cwd: '/tmp/workspace-b' },
    ]);
    await page.getByTestId('session-a1').click();
    await page.getByTestId('focus-pane-pane-a1').click();
    const workspace = page.locator('[data-session-terminal-workspace="workspace-a"]');
    await expect(workspace).toHaveClass(/agent-focus-mode/);
    await page.keyboard.press('Meta+2');
    await expect(page.locator('[data-session-terminal-workspace="workspace-b"]')).toBeVisible();
    await page.keyboard.press('Meta+1');
    await expect(workspace).toBeVisible();
    await expect(workspace).not.toHaveClass(/agent-focus-mode/);
    await expect(page.locator('.sidebar')).toBeVisible();
  });

  test('switches workspaces and Cmd+number jumps to the first session', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await injectWorkspace(page, daemon, 'workspace-a', [
      { id: 'a1', label: 'alpha-one', paneId: 'pane-a1', cwd: '/tmp/workspace-a' },
      { id: 'a2', label: 'alpha-two', paneId: 'pane-a2', cwd: '/tmp/workspace-a' },
    ], 'pane-a2');
    await injectWorkspace(page, daemon, 'workspace-b', [
      { id: 'b1', label: 'beta-one', paneId: 'pane-b1', cwd: '/tmp/workspace-b' },
      { id: 'b2', label: 'beta-two', paneId: 'pane-b2', cwd: '/tmp/workspace-b' },
    ], 'pane-b2');

    await expect(page.locator('[data-testid="sidebar-workspace-workspace-a"]')).toBeVisible();
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-b"]')).toBeVisible();

    await page.locator('[data-testid="session-a1"]').click();
    await expect(page.locator('[data-session-terminal-workspace="workspace-a"]')).toBeVisible();

    await page.locator('[data-testid="sidebar-session-a2"]').click();
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-a"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="sidebar-session-a2"]')).toHaveClass(/selected/);
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a1"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a2"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b1"]')).toHaveCount(0);

    await page.locator('[data-testid="sidebar-workspace-workspace-b"] .workspace-group-header').click();
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-b"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="sidebar-session-b1"]')).toHaveClass(/selected/);
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b1"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b2"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a1"]')).toHaveCount(0);

    await page.keyboard.press('Meta+1');
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-a"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="sidebar-session-a1"]')).toHaveClass(/selected/);
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a1"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-a2"]')).toBeVisible();

    await page.keyboard.press('Meta+2');
    await expect(page.locator('[data-testid="sidebar-workspace-workspace-b"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="sidebar-session-b1"]')).toHaveClass(/selected/);
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b1"]')).toBeVisible();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-b2"]')).toBeVisible();
  });

  test('Cmd+T opens the new-workspace picker while Cmd+N opens new-session picker', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await injectWorkspace(page, daemon, 'workspace-shortcuts', [
      { id: 'shortcut-a', label: 'shortcut-a', paneId: 'pane-shortcut-a', cwd: '/tmp/workspace-shortcuts' },
    ]);

    await page.locator('[data-testid="session-shortcut-a"]').click();
    await expect(page.locator('.terminal-wrapper.active [data-pane-id="pane-shortcut-a"]')).toBeVisible();

    await page.keyboard.press('Meta+n');
    await expect(page.locator('.location-picker-overlay')).toBeVisible();
    await expect(page.locator('.picker-title')).toHaveText('New Session Location');
    await page.keyboard.press('Escape');
    await expect(page.locator('.location-picker-overlay')).toHaveCount(0);

    await page.keyboard.press('Meta+t');
    await expect(page.locator('.location-picker-overlay')).toBeVisible();
    await expect(page.locator('.picker-title')).toHaveText('New Workspace Location');
  });

  test('clicking and keyboard navigation focus panes across sessions in one workspace', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await injectWorkspace(page, daemon, 'workspace-focus', [
      { id: 'focus-agent', label: 'focus-agent', paneId: 'pane-focus-agent', cwd: '/tmp/workspace-focus' },
      { id: 'focus-shell', label: 'focus-shell', paneId: 'pane-focus-shell', cwd: '/tmp/workspace-focus' },
    ], 'pane-focus-agent');

    await page.locator('[data-testid="session-focus-agent"]').click();
    const activeWorkspace = page.locator('[data-session-terminal-workspace="workspace-focus"]');
    await expect(activeWorkspace).toBeVisible();
    await expect(page.locator('[data-testid="sidebar-session-focus-agent"]')).toHaveClass(/selected/);
    await expect(activeWorkspace).toHaveAttribute('data-active-pane-id', 'pane-focus-agent');

    await activeWorkspace.locator('[data-pane-id="pane-focus-shell"]').click();
    await expect(page.locator('[data-testid="sidebar-session-focus-shell"]')).toHaveClass(/selected/);
    await expect(activeWorkspace).toHaveAttribute('data-active-pane-id', 'pane-focus-shell');

    await page.keyboard.press('Meta+Alt+ArrowLeft');
    await expect(page.locator('[data-testid="sidebar-session-focus-agent"]')).toHaveClass(/selected/);
    await expect(activeWorkspace).toHaveAttribute('data-active-pane-id', 'pane-focus-agent');
  });

  test('focus mode gives one agent the shell and restores the workspace on exit', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await injectWorkspace(page, daemon, 'workspace-focus-mode', [
      { id: 'focus-main', label: 'focus-main', paneId: 'pane-focus-main', cwd: '/tmp/workspace-focus-mode' },
      { id: 'focus-peer', label: 'focus-peer', paneId: 'pane-focus-peer', cwd: '/tmp/workspace-focus-mode' },
    ], 'pane-focus-main');

    await page.locator('[data-testid="session-focus-main"]').click();
    const workspace = page.locator('[data-session-terminal-workspace="workspace-focus-mode"]');

    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(workspace.locator('[data-pane-id="pane-focus-main"]')).toBeVisible();
    await expect(workspace.locator('[data-pane-id="pane-focus-peer"]')).toBeVisible();

    await workspace.locator('[data-pane-id="pane-focus-main"] .workspace-pane-header').hover();
    await workspace.locator('[data-testid="focus-pane-pane-focus-main"]').click();

    await expect(page.locator('.sidebar')).toBeHidden();
    await expect(workspace.locator('[data-pane-id="pane-focus-main"]')).toBeVisible();
    await expect(workspace.locator('[data-pane-id="pane-focus-peer"]')).toHaveCount(0);
    await expect(workspace.getByRole('button', { name: 'Return to split' })).toBeVisible();

    await workspace.getByRole('button', { name: 'Return to split' }).click();

    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(workspace.locator('[data-pane-id="pane-focus-main"]')).toBeVisible();
    await expect(workspace.locator('[data-pane-id="pane-focus-peer"]')).toBeVisible();
  });
  test('sidebar selection, row actions, and settings have independent keyboard targets', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectWorkspace(page, daemon, 'workspace-keyboard', [
      { id: 'keyboard-one', label: 'keyboard-one', paneId: 'pane-keyboard-one', cwd: '/tmp/workspace-keyboard' },
      { id: 'keyboard-two', label: 'keyboard-two', paneId: 'pane-keyboard-two', cwd: '/tmp/workspace-keyboard' },
    ]);
    const first = page.getByTestId('sidebar-session-keyboard-one');
    const second = page.getByTestId('sidebar-session-keyboard-two');
    const icon = await first.getByRole('img', { name: 'Shell' }).boundingBox();
    expect(icon).not.toBeNull();
    await page.mouse.click(icon!.x + icon!.width / 2, icon!.y + icon!.height / 2);
    await expect(first).toHaveClass(/selected/);
    await expect(page.locator('[data-pane-id="pane-keyboard-one"]').getByRole('textbox', { name: 'Terminal input' })).toBeFocused();
    await second.getByRole('button', { name: 'Open keyboard-two' }).focus();
    await expect(second.getByRole('button', { name: 'Open keyboard-two' })).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(second).toHaveClass(/selected/);
    await first.hover();
    await first.getByRole('button', { name: 'Actions for keyboard-one' }).click();
    await expect(page.getByRole('menu', { name: 'Actions for keyboard-one' })).toBeVisible();
    await expect(second).toHaveClass(/selected/);
    await page.keyboard.press('Escape');
    const settings = page.getByRole('button', { name: 'Sidebar settings', exact: true });
    await settings.focus();
    await page.keyboard.press('Enter');
    const dialog = page.getByRole('dialog', { name: 'Sidebar settings' });
    await expect(dialog.getByRole('switch', { name: 'Agent queue', exact: true })).toBeFocused();
    const sidebar = await page.locator('.sidebar').boundingBox();
    const popup = await dialog.boundingBox();
    expect(popup!.x).toBeGreaterThanOrEqual(sidebar!.x);
    expect(popup!.x + popup!.width).toBeLessThanOrEqual(sidebar!.x + sidebar!.width);
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(settings).toBeFocused();
    await expect(page.locator('.sidebar button button')).toHaveCount(0);
  });

});
