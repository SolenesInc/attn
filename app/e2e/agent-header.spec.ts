import { expect, test, type Page } from '@playwright/test';

const TILES = 4;

async function fitted(page: Page) {
  await page.evaluate(async () => {
    await window.__HARNESS_READY__;
    await document.fonts.ready;
    const rows = document.querySelectorAll('.desktop-pane-headline, .desktop-pane-subline');
    if (rows.length === 0) throw new Error('no agent header rows to wait for');
    await new Promise<void>((resolve) => {
      const observer = new ResizeObserver(() => {
        observer.disconnect();
        resolve();
      });
      for (const row of rows) observer.observe(row);
    });
    await new Promise((resolve) => requestAnimationFrame(resolve));
  });
}

const WIDTHS = [180, 200, 240, 299, 340, 400, 480, 560, 640, 760, 900, 1050, 1200];

async function setTileWidth(page: Page, width: number) {
  await page.getByTestId('agent-wall').evaluate((element, target) => element.style.setProperty('--tile-width', `${target}px`), width);
  const widths = await page.locator('.desktop-pane').evaluateAll((panes) => panes.map((pane) => Math.round(pane.getBoundingClientRect().width)));
  expect(widths).toEqual(Array(TILES).fill(width));
}

interface HeaderReceipt {
  id: string;
  height: number;
  dot: boolean;
  titleWidth: number;
  signals: string[];
  overflow: string[];
  host: boolean;
  seedTitle: string | null;
  seedState: boolean;
  roleLevel: string | null;
  roleDrawn: boolean;
}

function readHeaders(page: Page): Promise<HeaderReceipt[]> {
  return page.locator('.desktop-pane').evaluateAll((panes) => panes.map((pane) => {
    const header = pane.querySelector<HTMLElement>('.desktop-pane-header')!;
    const shown = (element: Element | null) => {
      if (!element) return false;
      const box = element.getBoundingClientRect();
      return box.width > 0 && box.height > 0 && getComputedStyle(element).visibility !== 'hidden';
    };
    const overflow: string[] = [];
    for (const row of header.querySelectorAll<HTMLElement>('.desktop-pane-headline, .desktop-pane-subline')) {
      const bounds = row.getBoundingClientRect();
      for (const piece of row.querySelectorAll<HTMLElement>(':scope > *, .desktop-pane-signals > *')) {
        if (!shown(piece)) continue;
        const box = piece.getBoundingClientRect();
        if (box.left < bounds.left - 0.5 || box.right > bounds.right + 0.5) {
          overflow.push(`${piece.dataset.fitPiece ?? piece.className} ${Math.round(box.left)}-${Math.round(box.right)} in ${Math.round(bounds.left)}-${Math.round(bounds.right)}`);
        }
      }
    }
    const seedChip = header.querySelector('.pane-seed-chip');
    const role = header.querySelector<HTMLElement>('.desktop-pane-piece[data-fit-piece="role"]');
    const roleTrigger = role?.querySelector('.delegation-chain-trigger');
    return {
      id: pane.getAttribute('data-pane-id')!,
      height: header.getBoundingClientRect().height,
      dot: shown(header.querySelector('.state-indicator')),
      titleWidth: header.querySelector('.desktop-pane-title')!.getBoundingClientRect().width,
      signals: Array.from(header.querySelectorAll('.settling-header, .pane-ask-chip'), (signal) => (shown(signal) ? signal.className : 'hidden')),
      overflow,
      host: shown(header.querySelector('.desktop-pane-piece[data-fit-piece="host"]')),
      seedTitle: seedChip?.querySelector('.pane-seed-chip-title')?.textContent ?? null,
      seedState: shown(seedChip?.querySelector('.seed-state-label') ?? null),
      roleLevel: roleTrigger ? role!.dataset.level ?? null : null,
      roleDrawn: roleTrigger ? Array.from(roleTrigger.children).some(shown) : false,
    };
  }));
}

test('agent headers keep the dot, title and signals from 180px to 1200px, in one height, inside their rows', async ({ page }) => {
  await page.setViewportSize({ width: 1200 * TILES + 200, height: 360 });
  await page.goto('/test-harness/?component=AgentHeader');
  await fitted(page);
  expect(await page.locator('.desktop-pane-header').count()).toBe(TILES);

  const customRoleLevels = new Set<string | null>();
  for (const width of WIDTHS) {
    await setTileWidth(page, width);
    await fitted(page);
    const headers = await readHeaders(page);
    const receipt = `${width}px ${JSON.stringify(headers)}`;
    expect(new Set(headers.map((header) => header.height)).size, receipt).toBe(1);
    for (const header of headers) {
      expect(header.dot, receipt).toBe(true);
      expect(header.titleWidth, receipt).toBeGreaterThan(40);
      expect(header.signals, receipt).not.toContain('hidden');
      expect(header.overflow, receipt).toEqual([]);
      if (header.roleLevel && header.roleLevel !== 'hidden') expect(header.roleDrawn, receipt).toBe(true);
    }
    const byId = Object.fromEntries(headers.map((header) => [header.id, header]));
    expect(byId['pane-release'].signals, receipt).toHaveLength(2);
    expect(byId['pane-sync'].signals, receipt).toHaveLength(1);
    expect(byId['pane-proto'].host, receipt).toBe(true);
    customRoleLevels.add(byId['pane-proto'].roleLevel);
  }

  expect(customRoleLevels).toContain('icon');
  const wide = Object.fromEntries((await readHeaders(page)).map((header) => [header.id, header]));
  expect(wide['pane-proto'].seedTitle).toBeNull();
  expect(wide['pane-proto'].seedState).toBe(true);
  expect(wide['pane-release'].seedTitle).toBe('Keep header controls aligned across every agent tile');
});

test('a usage receipt requested for a narrow tile shows the badge and opens beneath it', async ({ page }) => {
  await page.setViewportSize({ width: 180 * TILES + 200, height: 600 });
  await page.goto('/test-harness/?component=AgentHeader&width=180&usage=release');
  await fitted(page);
  const badge = page.getByTestId('session-usage-release');
  const receipt = page.locator('.session-usage-popover');
  expect(await badge.isVisible()).toBe(true);
  expect(await receipt.isVisible()).toBe(true);
  const badgeBox = (await badge.boundingBox())!;
  const receiptBox = (await receipt.boundingBox())!;
  expect(receiptBox.y).toBeGreaterThanOrEqual(badgeBox.y + badgeBox.height);
  expect(receiptBox.y).toBeLessThan(badgeBox.y + badgeBox.height + 12);
  const overflow = await page.locator('[data-pane-id="pane-release"] .desktop-pane-subline').evaluate((row) => {
    const bounds = row.getBoundingClientRect();
    return Array.from(row.children, (piece) => piece.getBoundingClientRect())
      .filter((box) => box.width > 0)
      .some((box) => box.right > bounds.right + 0.5);
  });
  expect(overflow).toBe(false);
});

for (const selection of ['rail', 'spotlight'] as const) {
  test(`inactive agent tiles dim the body and subline and keep signals lit under ${selection}`, async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto(`/test-harness/?component=AgentHeader&selection=${selection}`);
    await fitted(page);
    expect(await page.locator(`.session-terminal-desktop.desktop-selection--${selection}.multi-leaf`).count()).toBe(1);
    expect(await page.locator('.desktop-pane[data-pane-kind="agent"]').count()).toBe(TILES);
    expect(await page.locator('.desktop-pane.active').getAttribute('data-pane-id')).toBe('pane-release');

    const look = (paneId: string) => page.locator(`[data-pane-id="${paneId}"]`).evaluate((pane) => {
      const opacity = (element: Element | null) => {
        if (!element) return null;
        let product = 1;
        for (let node: Element | null = element; node; node = node.parentElement) product *= Number(getComputedStyle(node).opacity);
        return Math.round(product * 100) / 100;
      };
      const header = pane.querySelector('.desktop-pane-header')!;
      const top = header.getBoundingClientRect().top;
      const box = (selector: string) => {
        const element = header.querySelector(selector);
        if (!element) return null;
        const bounds = element.getBoundingClientRect();
        return [Math.round(bounds.top - top), Math.round(bounds.height)];
      };
      const swatch = document.createElement('span');
      swatch.style.color = 'var(--color-text-secondary)';
      pane.append(swatch);
      const secondary = getComputedStyle(swatch).color;
      swatch.remove();
      return {
        dot: opacity(header.querySelector('.state-indicator')),
        askOpacity: opacity(header.querySelector('.pane-ask-chip')),
        settling: opacity(header.querySelector('.settling-header')),
        body: opacity(pane.querySelector('.desktop-pane-body')),
        subline: opacity(header.querySelector('.desktop-pane-subline')),
        titleIsSecondary: getComputedStyle(header.querySelector('.desktop-pane-title')!).color === secondary,
        geometry: {
          header: Math.round(header.getBoundingClientRect().height),
          dot: box('.state-indicator'),
          title: box('.desktop-pane-title'),
          subline: box('.desktop-pane-subline'),
        },
        askBox: box('.pane-ask-chip'),
      };
    });

    const active = await look('pane-release');
    expect(active).toMatchObject({
      dot: 1, askOpacity: 1, settling: 1, body: 1, subline: 1, titleIsSecondary: false,
    });
    const inactive = Object.fromEntries(await Promise.all(['pane-sync', 'pane-bare', 'pane-proto'].map(async (paneId) => [paneId, await look(paneId)] as const)));
    for (const [paneId, tile] of Object.entries(inactive)) {
      expect(tile, paneId).toMatchObject({ dot: 1, body: 0.58, subline: 0.7, titleIsSecondary: true });
    }
    expect(inactive['pane-sync'].askOpacity).toBe(1);
    expect(inactive['pane-sync'].askBox).toEqual(active.askBox);
    for (const paneId of ['pane-sync', 'pane-bare']) {
      expect(inactive[paneId].geometry, paneId).toEqual(active.geometry);
    }
  });
}

test('a pull request collapsed to its icon still opens its details from the keyboard', async ({ page }) => {
  await page.setViewportSize({ width: 480 * TILES + 200, height: 600 });
  await page.goto('/test-harness/?component=AgentHeader&width=480');
  await fitted(page);
  const pane = page.locator('[data-pane-id="pane-release"]');
  expect(await pane.locator('.desktop-pane-piece[data-fit-piece="provenance"]').getAttribute('data-level')).toBe('icon');
  const details = pane.getByRole('button', { name: 'Pull request attn#412 details' });
  expect(await details.isVisible()).toBe(true);
  await details.focus();
  await page.keyboard.press('Enter');
  expect(await page.locator('.session-pr-popover').isVisible()).toBe(true);
});

for (const scale of ['1', '1.5']) {
  test(`the status dot sits on the title's vertical center at text size ${scale}`, async ({ page }) => {
    await page.setViewportSize({ width: 480 * TILES + 200, height: 600 });
    await page.goto('/test-harness/?component=AgentHeader&width=480');
    await fitted(page);
    await page.evaluate((value) => document.documentElement.style.setProperty('--ui-scale', value), scale);
    await fitted(page);
    const offsets = await page.locator('.desktop-pane-headline').evaluateAll((rows) => rows.map((row) => {
      const middle = (selector: string) => {
        const box = row.querySelector(selector)!.getBoundingClientRect();
        return box.top + box.height / 2;
      };
      return Math.abs(middle('.state-indicator') - middle('.desktop-pane-title'));
    }));
    expect(offsets).toHaveLength(TILES);
    for (const offset of offsets) expect(offset).toBeLessThanOrEqual(0.5);
  });
}

test('agent headers refit when the app text size changes', async ({ page }) => {
  await page.setViewportSize({ width: 480 * TILES + 200, height: 600 });
  await page.goto('/test-harness/?component=AgentHeader&width=480');
  await fitted(page);
  await page.evaluate(() => document.documentElement.style.setProperty('--ui-scale', '1.5'));
  await fitted(page);
  const headers = await readHeaders(page);
  const receipt = JSON.stringify(headers);
  for (const header of headers) expect(header.overflow, receipt).toEqual([]);
  const release = page.locator('[data-pane-id="pane-release"]');
  const row = (await release.locator('.desktop-pane-subline').boundingBox())!;
  const details = (await release.getByRole('button', { name: 'Pull request attn#412 details' }).boundingBox())!;
  expect(details.width, receipt).toBeGreaterThan(0);
  expect(details.x + details.width, receipt).toBeLessThanOrEqual(row.x + row.width + 0.5);
});
