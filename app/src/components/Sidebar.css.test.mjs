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

describe('sidebar delegation links', () => {
  it('keeps the dispatcher line visible at rest', () => {
    const line = ruleBody('.sidebar-dispatcher');
    expect(line).toBeTruthy();
    expect(line).not.toMatch(/opacity:\s*0\s*;/);
    expect(line).not.toMatch(/max-width:\s*0\s*;/);
  });

  it('uses the chief blue going up and the accent going down', () => {
    expect(ruleBody('.session-item.kin-up')).toMatch(/rgba\(88,\s*166,\s*255,\s*0\.08\)/);
    expect(ruleBody('.session-item.kin-up .sidebar-session-headline > .session-label'))
      .toMatch(/color:\s*#79b8ff\s*;/);
    expect(ruleBody('.session-item.kin-down')).toMatch(/var\(--accent\)/);
    expect(ruleBody('.session-item.kin-down .sidebar-dispatcher'))
      .toMatch(/color:\s*var\(--accent\)\s*;/);
  });

  it('keeps the count chip compact', () => {
    const chip = ruleBody('.sidebar-delegate-count');
    expect(chip).toMatch(/min-width:\s*18px\s*;/);
    expect(chip).toMatch(/height:\s*18px\s*;/);
    expect(chip).toMatch(/border-radius:\s*999px\s*;/);
  });
});
