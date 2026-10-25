import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { test, expect } from './fixtures';
import { resolveAttnBinaryPath } from './instanceEnv';

test('a long check tally has an ellipsis inside the narrow plot column', async ({ page, daemon }, testInfo) => {
  const { socketPath } = await daemon.start();
  const dataDir = path.dirname(socketPath);
  const cli = (args: string[], input?: string) => execFileSync(resolveAttnBinaryPath(), args, {
    input,
    encoding: 'utf8',
    env: {
      ...process.env,
      ATTN_INSTANCE: '',
      ATTN_TERMINAL_ID: '',
      ATTN_SESSION_ID: '',
      CODEX_THREAD_ID: '',
      ATTN_DATA_DIR: dataDir,
      ATTN_CONFIG_PATH: path.join(dataDir, 'config.json'),
      ATTN_SOCKET_PATH: socketPath,
      ATTN_DB_PATH: path.join(dataDir, 'attn.db'),
    },
  });
  const plot = JSON.parse(cli(['seed', 'plot', '--json'], JSON.stringify({
    title: 'Verify the check tallies',
    body: 'Keep the full tally available when the column is narrow.',
    children: [{
      title: 'Prove the layout',
      body: 'Read the tally in the plot column.',
      checks: Array.from({ length: 7 }, (_, i) => ({ id: `C${i + 1}`, text: `Proof ${i + 1}` })),
    }],
  }))) as { crown: { id: string }; children: { id: string }[] };
  const child = plot.children[0].id;
  for (const check of ['C1', 'C2', 'C3']) cli(['seed', 'check', 'confirm', child, check]);
  cli(['seed', 'check', 'report', child, 'C4', '-m', 'Ready to judge.']);
  cli(['seed', 'check', 'reject', child, 'C5', '-m', 'Needs more evidence.']);

  await page.goto('/');
  await page.locator('.dashboard').waitFor();
  await page.getByRole('button', { name: 'Show the garden' }).click();
  const garden = page.getByTestId('garden-frame');
  await garden.getByRole('button', { name: 'Expand the garden' }).click();
  await expect(garden).toHaveClass(/is-full/);
  await garden.getByRole('button', { name: 'list', exact: true }).click();
  await garden.locator(`[data-seed-row="${plot.crown.id}"]`).click();
  const column = garden.locator('.garden-column').first();
  const tally = column.locator('.seed-checks-tally-compact');
  await expect(tally).toHaveText('✓3/7 · 1 to judge · 1 rejected');
  await expect(tally).toHaveAttribute('title', 'checks: 3 of 7 confirmed · 1 to judge · 1 rejected');
  await page.evaluate(() => document.fonts.ready);
  const bounds = await tally.evaluate((element) => ({
    right: element.getBoundingClientRect().right,
    availableRight: element.parentElement!.getBoundingClientRect().right,
    width: element.clientWidth,
    textWidth: element.scrollWidth,
  }));
  const screenshot = testInfo.outputPath('plot-tally.png');
  await column.screenshot({ path: screenshot });
  await testInfo.attach('plot-tally', { path: screenshot, contentType: 'image/png' });
  expect(bounds.width).toBeLessThan(bounds.textWidth);
  expect(bounds.right).toBeLessThanOrEqual(bounds.availableRight);
  await expect(tally).toHaveCSS('overflow-x', 'hidden');
  await expect(tally).toHaveCSS('text-overflow', 'ellipsis');
});
