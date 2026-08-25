import { test, expect } from './fixtures';

const ESC = '';
const BSU = `${ESC}[?2026h`;
const ESU = `${ESC}[?2026l`;

// Mirrors `PaintSample` in app/src/utils/terminalDiagnosticsLog.ts.
interface RenderTraceEntry {
  at: number;
  kind: string;
  pane?: string;
  session?: string;
  force: boolean;
  offset: number;
  modelPrintable: number;
  quads: number | null;
  cellsArrayLen?: number | null;
  skipNull?: number | null;
  skipZeroWidth?: number | null;
  cols: number;
  rows: number;
}

function fullFrame(tag: string, rows = 50, cols = 140): string {
  let out = `${BSU}${ESC}[?25l${ESC}[2J${ESC}[H`;
  for (let r = 0; r < rows; r += 1) {
    out += `${ESC}[${r + 1};1H` + `${tag} line ${r} `.padEnd(cols, '.').slice(0, cols);
  }
  out += `${ESC}[H${ESU}`;
  return out;
}

async function emit(
  page: import('@playwright/test').Page,
  id: string,
  data: string,
) {
  await page.evaluate(({ id, data }) => window.__TEST_EMIT_PTY_DATA?.(id, data), { id, data });
}

async function readTrace(
  page: import('@playwright/test').Page,
  sessionId: string,
): Promise<RenderTraceEntry[]> {
  return page.evaluate((sid) => {
    const diagnostics = window as Window & {
      __ATTN_RENDER_TRACE?: RenderTraceEntry[];
      __ATTN_TERMINAL_DIAG_DUMP?: () => RenderTraceEntry[];
    };
    const all = diagnostics.__ATTN_TERMINAL_DIAG_DUMP?.() ?? diagnostics.__ATTN_RENDER_TRACE ?? [];
    return all.filter((entry) =>
      entry.kind === 'paint'
      && (entry.session === sid || (typeof entry.pane === 'string' && entry.pane.includes(sid))));
  }, sessionId) as Promise<RenderTraceEntry[]>;
}

test('agent pane stays painted after opening a shell split', async ({ page, daemon }) => {
  await daemon.start();
  await page.addInitScript(() => {
    (window as Window & { __ATTN_RENDER_TRACE_ON?: boolean; __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE_ON = true;
    (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = [];
  });
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const agentId = 's-agent-split';
  const terminal = await setupAgent(page, daemon, agentId);

  // Once the model is clean every further paint is a skip (`quads: null`) and
  // `null ?? 0` reads a painted surface as blank.
  await emitUntilVisible(page, agentId, fullFrame('OLD'), 'OLD line 0');
  await expect
    .poll(async () => lastRealDraw(await readTrace(page, agentId))?.quads ?? 0)
    .toBeGreaterThan(50);

  const sizeBefore = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);

  await page.evaluate(() => { (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = []; });

  await emit(page, agentId, `${BSU}${ESC}[?25l${ESC}[H${ESC}[21C${ESC}[40B`);

  await terminal.click({ position: { x: 80, y: 8 } });
  await page.keyboard.press('Meta+d');

  await expect
    .poll(async () => {
      const size = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);
      if (!size || !sizeBefore) return 'no-size';
      return size.rows !== sizeBefore.rows || size.cols !== sizeBefore.cols ? 'resized' : 'same';
    })
    .toBe('resized');

  const sizeAfter = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);

  await emit(page, agentId, fullFrame('NEW', Math.max(10, (sizeAfter?.rows ?? 24) - 1), Math.max(20, (sizeAfter?.cols ?? 80))));

  await page.waitForTimeout(800);

  const trace = await readTrace(page, agentId);
  const tail = trace.slice(-12);
  const draw = lastRealDraw(trace);

  console.log('=== SPLIT-BLANK REPRO DIAGNOSTICS ===');
  console.log('sizeBefore', JSON.stringify(sizeBefore), 'sizeAfter', JSON.stringify(sizeAfter));
  console.log('post-split render count:', trace.length);
  console.log('last real draw:', JSON.stringify(draw));
  console.log('tail renders:');
  for (const entry of tail) {
    console.log(`  force=${entry.force} offset=${entry.offset} modelPrintable=${entry.modelPrintable} quads=${entry.quads} ${entry.cols}x${entry.rows}`);
  }
  const modelText = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_TEXT?.(sid) ?? '', agentId);
  console.log('model contains NEW line 0:', modelText.includes('NEW line 0'));

  await terminal.screenshot({ path: 'test-results/split-blank-agent.png' }).catch(() => {});

  expect(modelText).toContain('NEW line 0');
  expect(draw, 'expected at least one agent draw after the split redraw').toBeTruthy();
  expect(draw!.quads ?? 0, `agent surface drew ${draw?.quads} quads while model held ${draw?.modelPrintable} printable cells`).toBeGreaterThan(50);
  expect({ cols: draw!.cols, rows: draw!.rows }, 'last draw must be at the post-split geometry (a later resize would have cleared the canvas)').toEqual({ cols: sizeAfter?.cols, rows: sizeAfter?.rows });
});

async function setupAgent(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (s: { id: string; label: string; state: string; directory?: string; workspace_id?: string }) => Promise<void> },
  agentId: string,
) {
  const workspaceId = `workspace-${agentId}`;
  await page.evaluate(({ sessionId, workspaceId }) => {
    window.__TEST_INJECT_SESSION?.({ id: sessionId, label: 'Agent Split', state: 'working', cwd: '/tmp/test/agent-split', workspaceId });
  }, { sessionId: agentId, workspaceId });
  await daemon.injectSession({ id: agentId, label: 'Agent Split', state: 'working', directory: '/tmp/test/agent-split', workspace_id: workspaceId });
  await page.locator(`[data-testid="session-${agentId}"]`).click();
  const terminal = page.locator(`[data-pane-session-id="${agentId}"][data-pane-kind="agent"] .terminal-container`);
  await expect(terminal).toBeVisible();
  await waitForPaneReady(page, agentId);
  return terminal;
}

// A single `__TEST_EMIT_PTY_DATA` can be lost while the pane is not fully wired.
// Scaffolding only: the post-split redraw must stay a single emit.
async function emitUntilVisible(
  page: import('@playwright/test').Page,
  sessionId: string,
  data: string,
  marker: string,
) {
  await expect
    .poll(async () => {
      await emit(page, sessionId, data);
      return page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_TEXT?.(sid) ?? '', sessionId);
    }, {
      message: `setup frame never reached the model for ${sessionId} (emit dropped before the pane was wired)`,
    })
    .toContain(marker);
}

// A visible terminal container is not a pane ready for PTY data: the handle is
// registered only once the Ghostty model loads, and an earlier emit is dropped.
async function waitForPaneReady(
  page: import('@playwright/test').Page,
  sessionId: string,
) {
  await expect
    .poll(async () => page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, sessionId))
    .not.toBeNull();
}

function chunks(value: string, size: number): string[] {
  const out: string[] = [];
  for (let i = 0; i < value.length; i += size) out.push(value.slice(i, i + size));
  return out;
}

// A `quads: null` entry is a renderer skip that leaves the canvas as the
// previous draw painted it, so asserting on it reads a healthy surface as blank.
function lastRealDraw(trace: RenderTraceEntry[]): RenderTraceEntry | undefined {
  for (let i = trace.length - 1; i >= 0; i -= 1) {
    if (trace[i].quads !== null) return trace[i];
  }
  return undefined;
}

function dumpTail(label: string, trace: RenderTraceEntry[]) {
  console.log(`=== ${label} ===`);
  console.log('post-split render count:', trace.length);
  for (const entry of trace.slice(-12)) {
    console.log(`  force=${entry.force} offset=${entry.offset} modelPrintable=${entry.modelPrintable} quads=${entry.quads} ${entry.cols}x${entry.rows}`);
  }
}

test('agent stays painted when split races a chunked redraw', async ({ page, daemon }) => {
  await daemon.start();
  await page.addInitScript(() => {
    (window as Window & { __ATTN_RENDER_TRACE_ON?: boolean; __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE_ON = true;
    (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = [];
  });
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const agentId = 's-agent-race';
  const terminal = await setupAgent(page, daemon, agentId);

  await emitUntilVisible(page, agentId, fullFrame('OLD'), 'OLD line 0');
  await page.evaluate(() => { (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = []; });

  await emit(page, agentId, `${BSU}${ESC}[?25l${ESC}[H${ESC}[21C${ESC}[40B`);

  // The redraw is deliberately not awaited against the split settling: that race
  // is the behaviour under test.
  await terminal.click({ position: { x: 80, y: 8 } });
  await page.keyboard.press('Meta+d');
  const redraw = fullFrame('NEW', 44, 75);
  for (const chunk of chunks(redraw, 180)) {
    await emit(page, agentId, chunk);
  }

  await page.waitForTimeout(1200);
  const trace = await readTrace(page, agentId);
  dumpTail('CHUNKED RACE', trace);
  const draw = lastRealDraw(trace);
  console.log('last real draw:', JSON.stringify(draw));
  await terminal.screenshot({ path: 'test-results/split-blank-race.png' }).catch(() => {});
  const finalSize = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);
  expect(draw, 'expected at least one agent draw after the split redraw').toBeTruthy();
  expect(draw!.quads ?? 0, `agent drew ${draw?.quads} quads, model had ${draw?.modelPrintable}`).toBeGreaterThan(50);
  expect({ cols: draw!.cols, rows: draw!.rows }, 'last draw must be at the final geometry (a later resize would have cleared the canvas)').toEqual({ cols: finalSize?.cols, rows: finalSize?.rows });
});

test('agent stays painted when split lands while scrolled up', async ({ page, daemon }) => {
  await daemon.start();
  await page.addInitScript(() => {
    (window as Window & { __ATTN_RENDER_TRACE_ON?: boolean; __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE_ON = true;
    (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = [];
  });
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const agentId = 's-agent-scroll';
  const terminal = await setupAgent(page, daemon, agentId);

  const scrollback = Array.from({ length: 200 }, (_, i) => `HIST line ${String(i).padStart(3, '0')}`).join('\r\n');
  await emitUntilVisible(page, agentId, `${ESC}[2J${ESC}[H${scrollback}`, 'HIST line 199');
  await emitUntilVisible(page, agentId, fullFrame('OLD'), 'OLD line 0');

  await terminal.hover({ position: { x: 80, y: 200 } });
  await page.mouse.wheel(0, -600);
  await page.evaluate(() => { (window as Window & { __ATTN_RENDER_TRACE?: unknown[] }).__ATTN_RENDER_TRACE = []; });
  await emit(page, agentId, `${BSU}${ESC}[?25l${ESC}[H${ESC}[21C${ESC}[40B`);

  await page.keyboard.press('Meta+d');
  await page.waitForTimeout(300);
  await emit(page, agentId, fullFrame('NEW', 44, 75));
  await page.waitForTimeout(1000);

  const trace = await readTrace(page, agentId);
  dumpTail('SCROLLED-UP SPLIT', trace);
  const draw = lastRealDraw(trace);
  console.log('last real draw:', JSON.stringify(draw));
  await terminal.screenshot({ path: 'test-results/split-blank-scrolled.png' }).catch(() => {});
  console.log('offset on last draw:', draw?.offset, 'force:', draw?.force, 'quads:', draw?.quads);
});

test('hidden split workspace defers paints until return after a window resize', async ({ page, daemon }) => {
  await daemon.start();
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto('/');
  await page.waitForSelector('.dashboard');

  const agentId = 's-agent-home-resize';
  const terminal = await setupAgent(page, daemon, agentId);
  await emit(page, agentId, fullFrame('BASELINE'));
  await expect
    .poll(async () => page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_TEXT?.(sid) ?? '', agentId))
    .toContain('BASELINE line 0');

  const sizeBeforeSplit = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);
  await terminal.click({ position: { x: 80, y: 8 } });
  await page.keyboard.press('Meta+d');
  await expect
    .poll(async () => {
      const size = await page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_SIZE?.(sid) ?? null, agentId);
      if (!size || !sizeBeforeSplit) return 'no-size';
      return size.rows !== sizeBeforeSplit.rows || size.cols !== sizeBeforeSplit.cols ? 'resized' : 'same';
    })
    .toBe('resized');
  await page.waitForTimeout(100);

  await page.keyboard.press('Meta+Shift+h');
  await expect(page.locator('.dashboard')).toBeVisible();
  await page.setViewportSize({ width: 900, height: 650 });
  await page.waitForTimeout(100);

  const paintsBeforeBurst = (await readTrace(page, agentId)).length;
  const hiddenFrame = fullFrame('HIDDEN', 35, 90);
  await page.evaluate(({ id, payloads }) => {
    for (const payload of payloads) {
      window.__TEST_EMIT_PTY_DATA?.(id, payload);
    }
  }, { id: agentId, payloads: chunks(hiddenFrame, 32) });
  await expect
    .poll(async () => page.evaluate((sid) => window.__TEST_GET_SESSION_PANE_TEXT?.(sid) ?? '', agentId))
    .toContain('HIDDEN line 0');
  await page.waitForTimeout(100);
  expect((await readTrace(page, agentId)).length).toBe(paintsBeforeBurst);

  await page.locator(`[data-testid="session-${agentId}"]`).click();
  await expect(terminal).toBeVisible();
  await expect
    .poll(async () => (await readTrace(page, agentId)).length)
    .toBeGreaterThan(paintsBeforeBurst);

  const trace = await readTrace(page, agentId);
  const postReturnPaints = trace
    .slice(paintsBeforeBurst)
    .filter((entry) => (entry.quads ?? 0) > 0);
  const substantivePaint = postReturnPaints[postReturnPaints.length - 1];
  expect(substantivePaint?.cols).toBeLessThan(100);
  expect(substantivePaint?.quads ?? 0).toBeGreaterThan(50);
});
