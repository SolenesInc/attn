import { readFileSync } from 'node:fs';
import { describe, it, expect } from 'vitest';

// jsdom does not resolve custom properties across elements, so only the
// stylesheet can catch an accent declared on a sibling instead of a parent.
describe('SettlingIndicator stylesheet', () => {
  // Comments would otherwise be swallowed into the selector list below.
  // Relative to the vitest root (app/); import.meta.url is not a file: URL here.
  const css = readFileSync('src/components/SettlingIndicator.css', 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '');

  const declaringRoots = (() => {
    const match = css.match(/([^{}]+)\{[^}]*--settling-accent:/);
    return match ? match[1].split(',').map((s) => s.trim()).filter(Boolean) : [];
  })();

  it('declares the palette', () => {
    expect(declaringRoots.length).toBeGreaterThan(0);
  });

  it.each([
    ['.settling-header', '.settling-header'],
    ['.settling-header-track', '.settling-header-track'],
    ['.settling-header-track-fill', '.settling-header-track'],
    ['.settling-sidebar-bar', '.settling-sidebar-bar'],
    ['.settling-sidebar-bar-fill', '.settling-sidebar-bar'],
    // The kept chip carries .settling-header too, so it inherits from there.
    ['.settling-header--kept', '.settling-header'],
    ['.settling-kept-mark', '.settling-header'],
  ])('%s can resolve --settling-accent via %s', (user, root) => {
    expect(css, `${user} has no rule`).toMatch(new RegExp(`\\${user}\\s*[,{]`));
    expect(declaringRoots).toContain(root);
  });
});
