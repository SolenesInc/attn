import { readFileSync } from 'node:fs';
import { describe, it, expect } from 'vitest';

// vitest stubs stylesheets away, so only the file on disk can be asserted on.
const css = readFileSync('src/components/Sidebar.css', 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

function ruleBody(selector) {
  for (const [, selectors, body] of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    if (selectors.split(',').some((one) => one.trim() === selector)) return body;
  }
  return null;
}

describe('sidebar pull request reveal', () => {
  it('costs the name nothing while the row is at rest', () => {
    const atRest = ruleBody('.sidebar-session-pr');
    expect(atRest).toBeTruthy();
    expect(atRest).toMatch(/max-width:\s*0\s*;/);
    expect(atRest).toMatch(/margin-left:\s*0\s*;/);
    expect(atRest).toMatch(/opacity:\s*0\s*;/);
  });

  it('never shrinks, so the number cannot be clipped mid-glyph', () => {
    expect(ruleBody('.sidebar-session-pr')).toMatch(/flex:\s*none\s*;/);
    expect(ruleBody('.sidebar-session-headline > .session-label')).toMatch(/flex:\s*0 1 auto\s*;/);
  });

  it('appears on the row hover, the same gate the actions use', () => {
    const hovered = ruleBody('.session-item:hover .sidebar-session-pr');
    expect(hovered).toBeTruthy();
    expect(hovered).toMatch(/opacity:\s*1\s*;/);
    expect(hovered).not.toMatch(/max-width:\s*0\s*;/);
    expect(hovered).toMatch(/max-width:/);
  });
});
