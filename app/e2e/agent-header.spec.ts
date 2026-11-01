import { expect, test } from '@playwright/test';

for (const [name, options, width] of [
  ['seed and identity', 'seed&role&usage', 1280],
  ['identity without seed', 'role&usage', 1280],
  ['seed and provenance in split panes', 'seed&role&usage&provenance&split', 1280],
  ['narrow header', 'seed&role&usage', 480],
  ['role without usage', 'seed&role', 760],
  ['usage without role', 'seed&usage', 760],
] as const) {
  test(`vertically centers ${name}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 560 });
    await page.goto(`/test-harness/?component=AgentHeader&${options}`);
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    const headers = page.locator('.desktop-pane-header');
    await expect(headers).toHaveCount(options.includes('split') ? 2 : 1);
    for (const header of await headers.all()) {
      await expect(header.locator('.desktop-pane-title')).toBeVisible();
      await expect(header.locator('.pane-seed-chip')).toHaveCount(options.includes('seed') ? 1 : 0);
      await expect(header.locator('.delegation-chain-trigger')).toHaveCount(options.includes('role') ? 1 : 0);
      await expect(header.locator('.desktop-pane-usage')).toHaveCount(options.includes('usage') ? 1 : 0);
      const centers = await header.evaluate((element) => {
        const center = (item: Element) => {
          const bounds = item.getBoundingClientRect();
          return Math.round(bounds.y + bounds.height / 2);
        };
        return {
          headline: Array.from(element.querySelector('.desktop-pane-identity-main')!.children, center),
          groups: Array.from(element.children, center),
        };
      });
      expect(new Set(centers.headline).size, JSON.stringify(centers)).toBe(1);
      expect(new Set(centers.groups).size, JSON.stringify(centers)).toBe(1);
    }
  });
}

for (const selection of ['rail', 'spotlight'] as const) {
  test(`inactive agent tiles dim the body and keep signals lit under ${selection}`, async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto(`/test-harness/?component=AgentHeader&three&signals&seed&role&usage&selection=${selection}`);
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await expect(page.locator(`.session-terminal-desktop.desktop-selection--${selection}.multi-leaf`)).toHaveCount(1);
    const panes = page.locator('.desktop-pane[data-pane-kind="agent"]');
    await expect(panes).toHaveCount(3);
    await expect(page.locator('.desktop-pane.active')).toHaveAttribute('data-pane-id', 'pane-agent');

    const look = (paneId: string) => page.locator(`[data-pane-id="${paneId}"]`).evaluate((pane) => {
      const opacity = (element: Element | null) => {
        let product = 1;
        for (let node = element; node; node = node.parentElement) product *= Number(getComputedStyle(node).opacity);
        return Math.round(product * 100) / 100;
      };
      const header = pane.querySelector('.desktop-pane-header')!;
      const top = header.getBoundingClientRect().top;
      const box = (selector: string) => {
        const bounds = header.querySelector(selector)!.getBoundingClientRect();
        return [Math.round(bounds.top - top), Math.round(bounds.height)];
      };
      const swatch = document.createElement('span');
      swatch.style.color = 'var(--color-text-secondary)';
      pane.append(swatch);
      const secondary = getComputedStyle(swatch).color;
      swatch.remove();
      return {
        dot: opacity(header.querySelector('.state-indicator')),
        ask: opacity(header.querySelector('.pane-ask-chip')),
        settling: opacity(header.querySelector('.settling-header')),
        body: opacity(pane.querySelector('.desktop-pane-body')),
        seed: opacity(header.querySelector('.pane-seed-chip')),
        titleIsSecondary: getComputedStyle(header.querySelector('.desktop-pane-title')!).color === secondary,
        geometry: {
          header: Math.round(header.getBoundingClientRect().height),
          dot: box('.state-indicator'),
          title: box('.desktop-pane-title'),
          ask: box('.pane-ask-chip'),
          settling: box('.settling-header'),
        },
      };
    });

    await expect.poll(() => look('pane-agent')).toMatchObject({
      dot: 1, ask: 1, settling: 1, body: 1, seed: 1, titleIsSecondary: false,
    });
    const active = await look('pane-agent');
    for (const paneId of ['pane-peer', 'pane-third']) {
      await expect.poll(() => look(paneId), paneId).toMatchObject({
        dot: 1, ask: 1, settling: 1, body: 0.58, seed: 0.7, titleIsSecondary: true,
      });
      expect((await look(paneId)).geometry, paneId).toEqual(active.geometry);
    }
  });
}
