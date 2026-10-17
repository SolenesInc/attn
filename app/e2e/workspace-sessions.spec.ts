import { test, expect, waitForMockPtyBanner } from './fixtures';

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
  test('cancels a sidebar workspace drag and can select it afterward', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectWorkspace(page, daemon, 'drag-workspace', [
      { id: 'drag-agent', label: 'drag-agent', paneId: 'drag-pane', cwd: '/tmp/drag-workspace' },
    ]);
    const group = page.getByTestId('sidebar-workspace-drag-workspace');
    const header = group.locator('.workspace-group-header > .sidebar-row-select');
    const box = await header.boundingBox();
    expect(box).not.toBeNull();
    await page.mouse.move(box!.x + box!.width / 2, box!.y + box!.height / 2);
    await page.mouse.down();
    await page.mouse.move(box!.x + box!.width / 2, box!.y + box!.height * 2);
    await expect(group).toHaveClass(/workspace-group--reorder-source/);
    await header.dispatchEvent('pointercancel', { pointerId: 1 });
    await expect(group).not.toHaveClass(/workspace-group--reorder-source/);
    await page.mouse.up();
    await page.getByTestId('sidebar-session-drag-agent').getByRole('button', { name: 'Open drag-agent' }).click();
    await expect(page.locator('[data-session-terminal-workspace="drag-workspace"]')).toBeVisible();
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

  test('frees a hidden workspace terminal\'s drawing buffer and repaints it unchanged on return', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectWorkspace(page, daemon, 'workspace-gpu', [
      { id: 'gpu-agent', label: 'gpu-agent', paneId: 'pane-gpu-agent', cwd: '/tmp/workspace-gpu' },
    ]);
    await injectWorkspace(page, daemon, 'workspace-other', [
      { id: 'other-agent', label: 'other-agent', paneId: 'pane-other-agent', cwd: '/tmp/workspace-other' },
    ]);
    await page.getByTestId('session-gpu-agent').click();
    await waitForMockPtyBanner(page, 'gpu-agent');
    await page.evaluate(() => window.__TEST_EMIT_PTY_DATA?.('gpu-agent', '\x1b[?25l\x1b[41mpainted before hiding\x1b[0m\r\nsecond row'));
    await expect
      .poll(() => page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('gpu-agent') ?? ''))
      .toContain('second row');
    const canvas = page.locator('[data-pane-id="pane-gpu-agent"] canvas').first();
    const before = await canvas.screenshot();
    const shown = await canvas.evaluate((element: HTMLCanvasElement) => [element.width, element.height]);
    expect(shown[0]).toBeGreaterThan(1);

    await page.getByTestId('sidebar-session-other-agent').getByRole('button', { name: 'Open other-agent' }).click();
    await expect(canvas).toHaveJSProperty('width', 1);
    await expect(canvas).toHaveJSProperty('height', 1);

    await page.getByTestId('sidebar-session-gpu-agent').getByRole('button', { name: 'Open gpu-agent' }).click();
    await expect(canvas).toHaveJSProperty('width', shown[0]);
    await expect(canvas).toHaveJSProperty('height', shown[1]);
    expect((await canvas.screenshot()).equals(before)).toBe(true);
  });

  test('draws terminal cells larger after the user increases the font size', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectWorkspace(page, daemon, 'workspace-font', [
      { id: 'font-agent', label: 'font-agent', paneId: 'pane-font-agent', cwd: '/tmp/workspace-font' },
    ]);
    await page.getByTestId('session-font-agent').click();
    await waitForMockPtyBanner(page, 'font-agent');
    const canvas = page.locator('[data-pane-id="pane-font-agent"] canvas').first();
    const cellWidth = () => canvas.evaluate((element: HTMLCanvasElement) => {
      const size = window.__TEST_GET_SESSION_PANE_SIZE?.('font-agent');
      return size ? element.width / size.cols : 0;
    });
    const before = await cellWidth();
    expect(before).toBeGreaterThan(0);

    await page.keyboard.press('Meta+Equal');

    await expect.poll(cellWidth).toBeGreaterThan(before);
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
    await expect(first.getByRole('img', { name: 'Shell' })).toBeVisible();
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
