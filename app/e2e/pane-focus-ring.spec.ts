import { test, expect } from '@playwright/test';

// This needs a real browser because jsdom/happy-dom do not apply the stylesheet.
test('active-pane selection markers paint above the split divider', async ({ page }) => {
  await page.goto('/test-harness/?component=PaneFocusRing');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const desktop = page.locator('[data-testid="desktop"]');
  const pane = page.locator('[data-testid="pane-active"]');
  const inactiveTile = page.locator('[data-testid="tile-inactive"]');
  const overlay = page.locator('[data-testid="split-divider"]');
  await expect(overlay).toBeVisible();

  const overlayZ = Number(await overlay.evaluate((el) => getComputedStyle(el).zIndex));

  for (const style of ['rail', 'spotlight']) {
    await desktop.evaluate((el, selectionStyle) => {
      el.classList.remove('desktop-selection--dim', 'desktop-selection--rail', 'desktop-selection--spotlight');
      el.classList.add(`desktop-selection--${selectionStyle}`);
    }, style);

    const markerZ = Number(
      await pane.evaluate((el) => getComputedStyle(el, '::after').zIndex),
    );
    expect(markerZ, `${style} marker z-index`).toBeGreaterThan(overlayZ);
    await expect(inactiveTile, `${style} inactive tile opacity`).toHaveCSS('opacity', '0.58');
    await expect(pane, `${style} active pane opacity`).toHaveCSS('opacity', '1');
  }

  await desktop.evaluate((el) => {
    el.classList.remove('desktop-selection--rail', 'desktop-selection--spotlight');
    el.classList.add('desktop-selection--dim');
  });

  expect(await pane.evaluate((el) => getComputedStyle(el, '::before').content)).toBe('none');
  expect(await pane.evaluate((el) => getComputedStyle(el, '::after').content)).toBe('none');
  await expect(pane).toHaveCSS('box-shadow', 'none');
  await expect(inactiveTile).toHaveCSS('opacity', '0.58');
  await expect(pane).toHaveCSS('opacity', '1');
});

test('arrival ring covers the pane in rail and spotlight selection', async ({ page }) => {
  await page.goto('/test-harness/?component=PaneFocusRing');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const desktop = page.getByTestId('desktop');
  const pane = page.getByTestId('pane-active');
  const dividerZ = Number(await page.getByTestId('split-divider').evaluate((el) => getComputedStyle(el).zIndex));

  for (const style of ['rail', 'spotlight']) {
    await desktop.evaluate((el, selectionStyle) => {
      el.classList.remove('desktop-selection--rail', 'desktop-selection--spotlight');
      el.classList.add(`desktop-selection--${selectionStyle}`);
    }, style);
    const ring = await pane.evaluate(async (el) => {
      el.classList.remove('leaf-arrival');
      void el.clientWidth;
      const started = new Promise<void>((resolve) => {
        el.addEventListener('animationstart', (event) => {
          if (event.animationName === 'leaf-arrival-pulse') resolve();
        }, { once: true });
      });
      el.classList.add('leaf-arrival');
      await started;
      const style = getComputedStyle(el, '::after');
      return {
        animationName: style.animationName,
        inset: [style.top, style.right, style.bottom, style.left],
        shadow: style.boxShadow,
        zIndex: Number(style.zIndex),
      };
    });
    expect(ring?.animationName).toBe('leaf-arrival-pulse');
    expect(ring?.inset).toEqual(['0px', '0px', '0px', '0px']);
    expect(ring?.shadow).not.toBe('none');
    expect(ring?.zIndex).toBeGreaterThan(dividerZ);
  }
});
