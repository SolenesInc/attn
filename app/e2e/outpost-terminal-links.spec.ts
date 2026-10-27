import { test, expect, type Page } from '@playwright/test';

type ProbeWindow = Window & {
  __OPENED_PATHS?: string[];
  __EXISTS_ASKED?: string[];
  __TAURI_INTERNALS__?: { invoke: (command: string, args?: { path?: string }) => Promise<unknown> };
};

async function openTerminal(page: Page, query: string, output: string) {
  await page.addInitScript(() => {
    const probe = window as ProbeWindow;
    probe.__OPENED_PATHS = [];
    probe.__EXISTS_ASKED = [];
    probe.__TAURI_INTERNALS__ = {
      invoke: async (command, args) => {
        if (command === 'plugin:opener|open_path' && args?.path) probe.__OPENED_PATHS?.push(args.path);
        if (command === 'plugin:fs|exists') {
          probe.__EXISTS_ASKED?.push(args?.path ?? '');
          return true;
        }
        if (command === 'plugin:path|resolve_directory') return '/Users/test-user';
        return undefined;
      },
    };
  });
  await page.goto(`/test-harness/?component=OutpostTerminalLinks${query}`);
  await page.waitForFunction(() => window.__HARNESS__?.ready);
  const terminal = page.locator('.terminal-container').first();
  await expect(terminal.locator('canvas').first()).toBeVisible();
  await expect(terminal).toHaveAttribute('data-terminal-input-ready', '1');
  const runtime = query ? 'remote' : 'local';
  await page.evaluate(({ id, data }) => window.__TEST_EMIT_PTY_DATA?.(id, data), { id: runtime, data: `${output}\r\n` });
  return terminal;
}

async function cmdClick(page: Page, terminal: import('@playwright/test').Locator) {
  await terminal.hover({ position: { x: 20, y: 8 } });
  await page.keyboard.down('Meta');
  await expect(terminal).toHaveCSS('cursor', 'pointer');
  await terminal.click({ position: { x: 20, y: 8 } });
  await page.keyboard.up('Meta');
}

const refused = (page: Page) => page.evaluate(() => window.__HARNESS__.getCalls('pathRefused'));
const opened = (page: Page) => page.evaluate(() => ({
  paths: (window as ProbeWindow).__OPENED_PATHS ?? [],
  markdown: window.__HARNESS__.getCalls('openMarkdown'),
  existsAsked: (window as ProbeWindow).__EXISTS_ASKED ?? [],
}));

for (const [name, output, path] of [
  ['a path', '\u001b[2J\u001b[Hsrc/main.go:12:3 compiled', '/tmp/test/terminal-links/src/main.go'],
  ['a markdown path', '\u001b[2J\u001b[Hnotes.md', '/tmp/test/terminal-links/notes.md'],
  ['a file:// link', '\u001b[2J\u001b[H\u001b]8;;file://studio/etc/hosts\u001b\\hosts-file\u001b]8;;\u001b\\', '/etc/hosts'],
]) {
  test(`cmd+click on ${name} in an outpost terminal names the host and opens nothing on the Mac`, async ({ page }) => {
    const terminal = await openTerminal(page, '&host=Studio', output);
    await cmdClick(page, terminal);

    await expect.poll(() => refused(page)).toEqual([[`${path} is on Studio; opening files on an outpost isn't supported yet`]]);
    expect(await opened(page)).toEqual({ paths: [], markdown: [], existsAsked: [] });
  });
}

test('cmd+click on a path in a local terminal still opens it on the Mac', async ({ page }) => {
  const terminal = await openTerminal(page, '', '\u001b[2J\u001b[Hsrc/main.go:12:3 compiled');
  await cmdClick(page, terminal);

  await expect.poll(async () => (await opened(page)).paths).toEqual(['/tmp/test/terminal-links/src/main.go']);
  expect(await refused(page)).toEqual([]);
});

test('cmd+click on a markdown path in a local terminal still opens it in a tile', async ({ page }) => {
  const terminal = await openTerminal(page, '', '\u001b[2J\u001b[Hnotes.md');
  await cmdClick(page, terminal);

  await expect.poll(() => page.evaluate(() => window.__HARNESS__.getCalls('openMarkdown'))).toEqual([['/tmp/test/terminal-links/notes.md', 'local']]);
  expect(await refused(page)).toEqual([]);
});
