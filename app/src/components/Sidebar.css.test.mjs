import { readFileSync } from 'node:fs';
import { describe, it, expect } from 'vitest';

// vitest stubs stylesheets away, so only the file on disk can be asserted on.
const css = readFileSync('src/components/Sidebar.css', 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
const chainCss = readFileSync('src/components/DelegationChain.css', 'utf8');

function ruleBody(selector, source = css) {
  for (const [, selectors, body] of source.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
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
  it('removes the dispatcher subtitle', () => {
    expect(ruleBody('.sidebar-dispatcher')).toBeNull();
  });

  it('does not highlight relatives in other rows', () => {
    expect(css).not.toMatch(/\.kin-(up|down)/);
    expect(chainCss).not.toMatch(/\.kin-(up|down)/);
    expect(ruleBody('.delegation-chain-trigger:hover', chainCss)).toMatch(/background:\s*var\(--color-bg-button\)/);
  });

  it('replaces the count chip with a compact role cue', () => {
    expect(ruleBody('.sidebar-delegate-count')).toBeNull();
    const cue = ruleBody('.delegation-chain-trigger--sidebar', chainCss);
    expect(cue).toMatch(/width:\s*22px\s*;/);
    expect(cue).toMatch(/height:\s*22px\s*;/);
    expect(ruleBody('.delegation-chain-trigger', chainCss)).toMatch(/flex-shrink:\s*0\s*;/);
  });
});

describe('sidebar harness logos', () => {
  it('removes every harness mark when the sidebar preference is off', () => {
    expect(ruleBody('.sidebar--hide-harness-logos .sidebar-harness-icon'))
      .toMatch(/display:\s*none\s*;/);
  });
});
