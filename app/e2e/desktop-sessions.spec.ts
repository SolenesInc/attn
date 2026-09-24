import { test, expect } from './fixtures';

type DesktopSessionFixture = {
  id: string;
  label: string;
  cwd: string;
};

async function injectSessions(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (session: { id: string; label: string; agent?: 'shell'; state: string; directory?: string }) => Promise<void> },
  sessions: DesktopSessionFixture[],
) {
  await page.evaluate((entries) => {
    for (const session of entries) {
      window.__TEST_INJECT_SESSION?.({
        id: session.id,
        label: session.label,
        state: 'working',
        cwd: session.cwd,
        agent: 'shell',
        workspaceId: '',
      });
    }
  }, sessions);
  for (const session of sessions) {
    await daemon.injectSession({ id: session.id, label: session.label, agent: 'shell', state: 'working', directory: session.cwd });
  }
}

const currentDesktop = (page: import('@playwright/test').Page) =>
  page.locator('.terminal-wrapper.active [data-session-terminal-workspace]');

const paneOf = (sessionId: string) => `[data-pane-kind="agent"][data-pane-session-id="${sessionId}"]`;

async function openSide(page: import('@playwright/test').Page, first: string, second: string) {
  await page.locator(`[data-testid="session-${first}"]`).click();
  await expect(currentDesktop(page).locator(paneOf(first))).toBeVisible();
  await page.locator(`[data-testid="sidebar-session-${second}"]`).getByRole('button', { name: /^Open / }).click();
  await expect(currentDesktop(page).locator(paneOf(second))).toBeVisible();
}

test.describe('Desktop Sessions', () => {
  test('opening an agent places it beside the active pane of the current desktop', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectSessions(page, daemon, [
      { id: 'side-one', label: 'side-one', cwd: '/tmp/desktop-side' },
      { id: 'side-two', label: 'side-two', cwd: '/tmp/desktop-side' },
    ]);

    await openSide(page, 'side-one', 'side-two');

    await expect(currentDesktop(page).locator(paneOf('side-one'))).toBeVisible();
    await expect(page.locator('[data-testid="sidebar-session-side-two"]')).toHaveClass(/selected/);
  });

  test('clicking and keyboard navigation focus panes across agents on one desktop', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectSessions(page, daemon, [
      { id: 'focus-agent', label: 'focus-agent', cwd: '/tmp/desktop-focus' },
      { id: 'focus-shell', label: 'focus-shell', cwd: '/tmp/desktop-focus' },
    ]);

    await openSide(page, 'focus-agent', 'focus-shell');
    const desktop = currentDesktop(page);

    await desktop.locator(paneOf('focus-agent')).click();
    await expect(page.locator('[data-testid="sidebar-session-focus-agent"]')).toHaveClass(/selected/);

    await page.keyboard.press('Meta+Alt+ArrowRight');
    await expect(page.locator('[data-testid="sidebar-session-focus-shell"]')).toHaveClass(/selected/);

    await page.keyboard.press('Meta+Alt+ArrowLeft');
    await expect(page.locator('[data-testid="sidebar-session-focus-agent"]')).toHaveClass(/selected/);
  });

  test('focus mode gives one agent the desktop and restores the split on exit', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectSessions(page, daemon, [
      { id: 'focus-main', label: 'focus-main', cwd: '/tmp/desktop-focus-mode' },
      { id: 'focus-peer', label: 'focus-peer', cwd: '/tmp/desktop-focus-mode' },
    ]);

    await openSide(page, 'focus-peer', 'focus-main');
    const desktop = currentDesktop(page);
    await expect(page.locator('.sidebar')).toBeVisible();

    const mainPane = desktop.locator(paneOf('focus-main'));
    await mainPane.locator('.workspace-pane-header').hover();
    await mainPane.locator('[data-testid^="focus-pane-"]').click();

    await expect(page.locator('.sidebar')).toBeHidden();
    await expect(desktop.locator(paneOf('focus-main'))).toBeVisible();
    await expect(desktop.locator(paneOf('focus-peer'))).toHaveCount(0);

    await desktop.getByRole('button', { name: 'Return to split' }).click();

    await expect(page.locator('.sidebar')).toBeVisible();
    await expect(desktop.locator(paneOf('focus-main'))).toBeVisible();
    await expect(desktop.locator(paneOf('focus-peer'))).toBeVisible();
  });

  test('sidebar selection, row actions, and settings have independent keyboard targets', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');
    await injectSessions(page, daemon, [
      { id: 'keyboard-one', label: 'keyboard-one', cwd: '/tmp/desktop-keyboard' },
      { id: 'keyboard-two', label: 'keyboard-two', cwd: '/tmp/desktop-keyboard' },
    ]);
    const first = page.getByTestId('sidebar-session-keyboard-one');
    const second = page.getByTestId('sidebar-session-keyboard-two');
    await expect(first).toBeVisible();
    await expect(second).toBeVisible();
    const icon = await first.getByRole('img', { name: 'Shell' }).boundingBox();
    expect(icon).not.toBeNull();
    await page.mouse.click(icon!.x + icon!.width / 2, icon!.y + icon!.height / 2);
    await expect(first).toHaveClass(/selected/);
    await expect(page.locator(paneOf('keyboard-one')).getByRole('textbox', { name: 'Terminal input' })).toBeFocused();
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
