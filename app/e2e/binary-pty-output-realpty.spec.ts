import { test, expect } from './fixtures';
import { E2E_CLIENT_TOKEN } from './profileEnv';

const realPtyEnabled = process.env.VITE_FORCE_REAL_PTY === '1';

// Rendering alone cannot catch a silent fallback from binary frames to
// base64-in-JSON, so the transport used is asserted from the pty perf snapshot.
test.describe('Binary PTY output transport', () => {
  test('shell output arrives via binary frames and renders', async ({ page, daemon }) => {
    test.skip(!realPtyEnabled, 'Requires real PTY');

    const daemonInfo = await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    const sessionId = 'binary-pty-1';
    await page.evaluate((id) => {
      window.__TEST_INJECT_SESSION?.({
        id,
        label: 'Binary PTY',
        state: 'working',
        cwd: '/tmp',
        workspaceId: `workspace-${id}`,
      });
    }, sessionId);
    await daemon.injectSession({
      id: sessionId,
      label: 'Binary PTY',
      state: 'working',
      directory: '/tmp',
    });

    const spawnResult = await page.evaluate(
      ({ wsUrl, id, clientToken }) =>
        new Promise<{ success?: boolean; error?: string }>((resolve, reject) => {
          const workspaceId = `workspace-${id}`;
          const ws = new WebSocket(wsUrl);
          const timer = window.setTimeout(() => {
            ws.close();
            reject(new Error('spawn timeout'));
          }, 15000);
          ws.onerror = () => {
            window.clearTimeout(timer);
            reject(new Error('websocket error'));
          };
          ws.onclose = (e) => {
            window.clearTimeout(timer);
            reject(new Error(`websocket closed code=${e.code} reason=${e.reason}`));
          };
          ws.onmessage = (event) => {
            if (typeof event.data !== 'string') return;
            const data = JSON.parse(event.data);
            if (data.event === 'error') {
              window.clearTimeout(timer);
              ws.close();
              reject(new Error(`daemon error: ${data.error ?? JSON.stringify(data)}`));
              return;
            }
            if (data.event !== 'spawn_result' || data.id !== id) return;
            window.clearTimeout(timer);
            ws.close();
            resolve(data);
          };
          ws.onopen = () => {
            ws.send(JSON.stringify({
              cmd: 'client_hello',
              client_kind: 'e2e-test',
              version: 'e2e',
              capabilities: ['workspace_sessions'],
              client_token: clientToken,
            }));
            ws.send(JSON.stringify({
              cmd: 'register_workspace',
              id: workspaceId,
              title: 'Binary PTY',
              directory: '/tmp',
            }));
            ws.send(JSON.stringify({
              cmd: 'spawn_session',
              id,
              workspace_id: workspaceId,
              cwd: '/tmp',
              agent: 'shell',
              cols: 80,
              rows: 24,
              label: 'Binary PTY',
            }));
          };
        }),
      { wsUrl: daemonInfo.wsUrl, id: sessionId, clientToken: E2E_CLIENT_TOKEN }
    );
    expect(spawnResult.success, JSON.stringify(spawnResult)).toBe(true);

    await page.locator(`[data-testid="session-${sessionId}"]`).click();
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible();

    await page.waitForFunction((id) => {
      const events = (window as any).__TEST_PTY_EVENTS as Array<{ event: string; id?: string }> | undefined;
      return Boolean(events?.some((evt) => evt.event === 'data' && evt.id === id));
    }, sessionId, { timeout: 15000 });

    const transport = await page.evaluate((id) => {
      const dump = (window as any).__ATTN_PTY_PERF_DUMP?.();
      if (!dump) return null;
      const ptyEvents = dump.recentEvents.filter(
        (e: { event: string | null; runtimeId: string | null }) =>
          e.event === 'pty_output' && e.runtimeId === id,
      );
      return {
        binaryCount: ptyEvents.filter((e: { source: string | null }) => e.source === 'binary').length,
        jsonCount: ptyEvents.filter((e: { source: string | null }) => e.source !== 'binary').length,
        base64Chars: dump.ptyOutputBase64Chars,
      };
    }, sessionId);

    expect(transport).not.toBeNull();
    expect(transport!.binaryCount).toBeGreaterThan(0);
    expect(transport!.jsonCount).toBe(0);
    expect(transport!.base64Chars).toBe(0);
  });
});
