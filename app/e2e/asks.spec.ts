import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures';
import { E2E_CLIENT_TOKEN, resolveAttnBinaryPath } from './instanceEnv';

const CACHE_STORE_ASK = `Deploys empty the cache, so the first requests after one are slow.

:::question
Which store should keep the cache?
- [ ] Postgres — already deployed
- [ ] Redis — faster, one more service
Recommended: Postgres
:::
`;

const paced = process.env.ATTN_E2E_PACE === '1';

function pause(page: Page, ms: number) {
  return paced ? page.waitForTimeout(ms) : Promise.resolve();
}

function attnCli(socketPath: string, args: string[], input?: string): string {
  const dataDir = path.dirname(socketPath);
  return execFileSync(resolveAttnBinaryPath(), args, {
    input,
    encoding: 'utf8',
    env: {
      ...process.env,
      ATTN_INSTANCE: '',
      ATTN_DATA_DIR: dataDir,
      ATTN_SOCKET_PATH: socketPath,
      ATTN_DB_PATH: path.join(dataDir, 'attn.db'),
    },
  });
}

async function spawnClaude(wsUrl: string, spawn: { id: string; cwd: string; label: string }) {
  const ws = new WebSocket(wsUrl);
  try {
    const result = await new Promise<{ success: boolean; error?: string }>((resolve, reject) => {
      ws.onerror = () => reject(new Error('websocket error'));
      ws.onclose = (event) => reject(new Error(`websocket closed code=${event.code} reason=${event.reason}`));
      ws.onopen = () => ws.send(JSON.stringify({
        cmd: 'client_hello',
        client_kind: 'e2e-test',
        version: 'e2e',
        capabilities: [],
        client_token: E2E_CLIENT_TOKEN,
      }));
      ws.onmessage = (event) => {
        if (typeof event.data !== 'string') return;
        const message = JSON.parse(event.data);
        if (message.event === 'initial_state') {
          ws.send(JSON.stringify({
            cmd: 'spawn_session',
            id: spawn.id,
            profile_id: message.selected_profile_id,
            cwd: spawn.cwd,
            agent: 'claude',
            label: spawn.label,
            cols: 80,
            rows: 24,
          }));
        } else if (message.event === 'command_error') {
          reject(new Error(`spawn refused: ${message.error}`));
        } else if (message.event === 'spawn_result' && message.id === spawn.id) {
          resolve(message);
        }
      };
    });
    expect(result.success, result.error).toBe(true);
  } finally {
    ws.close();
  }
}

test.describe('Asks', () => {
  test('an agent asks through attn ask and the answer picked in the drawer closes the ask', async ({ page, daemon }) => {
    test.slow(paced, 'ATTN_E2E_PACE adds pauses for a recording');
    const { wsUrl, socketPath } = await daemon.start();
    const cwd = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-asks-'));
    const sessionId = 'asks-cache-agent';
    const label = 'cache-agent';
    const note = 'Postgres until the load says otherwise; no new services this quarter.';
    let spawned = false;
    try {
      await page.goto('/');
      await page.waitForSelector('.dashboard');
      await pause(page, 1000);

      await page.keyboard.press('Meta+,');
      await page
        .getByRole('navigation', { name: 'Settings sections' })
        .getByRole('button', { name: /^Experimental/ })
        .click();
      const asksSwitch = page.getByRole('switch', { name: 'Asks' });
      await pause(page, 600);
      await asksSwitch.click();
      await expect(asksSwitch).toHaveAttribute('aria-checked', 'true');
      await pause(page, 800);
      await page.keyboard.press('Escape');
      await expect(asksSwitch).toHaveCount(0);

      await spawnClaude(wsUrl, { id: sessionId, cwd, label });
      spawned = true;
      const row = page.getByTestId(`sidebar-session-${sessionId}`);
      await expect(row).toBeVisible();
      await pause(page, 800);

      const asked = attnCli(socketPath, ['ask', '--session', sessionId, '--subject', 'Cache store'], CACHE_STORE_ASK);
      const askId = /^Asked (\S+): "Cache store"\./.exec(asked)?.[1];
      expect(askId, asked).toBeTruthy();

      const marker = row.getByTestId(`ask-marker-${sessionId}`);
      await expect(marker).toHaveText('?');
      await pause(page, 1200);

      await page.keyboard.press('Meta+Shift+r');
      const drawer = page.getByRole('region', { name: 'Asks' });
      await expect(drawer.getByRole('list', { name: 'Open asks' }).getByRole('button').first())
        .toHaveAttribute('data-testid', `asks-row-${askId}`);
      const ask = drawer.getByTestId(`ask-${askId}`);
      await expect(ask).toHaveAttribute('aria-label', 'Cache store');
      await expect(ask.getByText(label, { exact: true })).toBeVisible();
      const postgres = ask.getByRole('radio', { name: /Postgres/ });
      await expect(postgres).toBeFocused();
      await pause(page, 1200);

      await postgres.click();
      await expect(postgres).toBeChecked();
      await pause(page, 600);
      await ask.getByRole('textbox', { name: 'Note' }).click();
      await page.keyboard.type(note, { delay: paced ? 35 : 0 });
      await pause(page, 800);
      await page.keyboard.press('Meta+Enter');

      await expect(page.getByText('No open asks.')).toBeVisible();
      await expect(ask).toHaveCount(0);
      await expect(marker).toHaveCount(0);
      await pause(page, 1500);

      expect(attnCli(socketPath, ['ask', 'list', '--session', sessionId])).toBe('No open asks.\n');
    } finally {
      if (spawned) {
        try {
          attnCli(socketPath, ['agent', 'close', sessionId, '-m', 'answered', '--source-session', sessionId]);
        } catch (error) {
          console.warn(`[asks] could not close ${sessionId}; its stub agent outlives the daemon: ${error}`);
        }
      }
      fs.rmSync(cwd, { recursive: true, force: true });
    }
  });
});
