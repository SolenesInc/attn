import { test, expect } from './fixtures';

async function injectLocalSession(
  page: import('@playwright/test').Page,
  session: { id: string; label: string; state: string; cwd?: string }
) {
  await page.evaluate((s) => {
    window.__TEST_INJECT_SESSION?.({
      id: s.id,
      label: s.label,
      state: s.state as 'working' | 'waiting_input' | 'idle',
      cwd: s.cwd || '/tmp/test',
    });
  }, session);
}

async function createSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (s: { id: string; label: string; state: string; directory?: string }) => Promise<void> },
  session: { id: string; label: string; state: string; cwd?: string }
) {
  const cwd = session.cwd || '/tmp/test';

  await injectLocalSession(page, { ...session, cwd });

  await daemon.injectSession({
    id: session.id,
    label: session.label,
    state: session.state,
    directory: cwd,
  });
}

test.describe('Session State Changes', () => {

  test('sidebar leads carry session state colors', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await createSession(page, daemon, { id: 's1', label: 'Working', state: 'working', cwd: '/tmp/test/s1' });
    await createSession(page, daemon, { id: 's2', label: 'Waiting', state: 'waiting_input', cwd: '/tmp/test/s2' });
    await createSession(page, daemon, { id: 's3', label: 'Idle', state: 'idle', cwd: '/tmp/test/s3' });

    await expect(page.locator('[data-testid="sidebar-session-s1"]')).toBeVisible();

    const workingLead = page.locator('[data-testid="sidebar-session-s1"] .session-lead');
    const waitingLead = page.locator('[data-testid="sidebar-session-s2"] .session-lead');
    const idleLead = page.locator('[data-testid="sidebar-session-s3"] .session-lead');

    await expect(workingLead).toHaveAttribute('data-state', 'working');
    await expect(waitingLead).toHaveAttribute('data-state', 'waiting_input');
    await expect(idleLead).toHaveAttribute('data-state', 'idle');
    await expect(workingLead).toHaveCSS('color', 'rgb(34, 197, 94)');
    await expect(workingLead.locator('.sidebar-harness-icon')).toHaveCSS('color', 'rgb(34, 197, 94)');
    await expect(workingLead).toHaveCSS('animation-name', 'none');
    await expect(waitingLead).toHaveCSS('color', 'rgb(245, 158, 11)');
    await expect(idleLead).toHaveCSS('color', 'rgb(107, 114, 128)');

    await page.locator('.sidebar').evaluate((sidebar) => sidebar.classList.add('sidebar--hide-harness-logos'));
    const workingDot = workingLead.getByTestId('state-indicator');
    await expect(workingDot).toBeVisible();
    await expect(workingDot).toHaveCSS('animation-name', 'none');
  });

});
