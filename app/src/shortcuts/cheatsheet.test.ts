// app/src/shortcuts/cheatsheet.test.ts
import { describe, it, expect } from 'vitest';
import { buildCheatsheet } from './cheatsheet';
import { SHORTCUTS } from './registry';
import { SHORTCUT_META } from './metadata';
import { withNavigatorPlatform } from '../test/platformStub';

describe('buildCheatsheet', () => {
  it('produces categories with non-empty, fully-rendered combos', () => {
    const categories = buildCheatsheet();
    expect(categories.length).toBeGreaterThan(0);

    for (const category of categories) {
      expect(category.title).toBeTruthy();
      expect(category.rows.length).toBeGreaterThan(0);
      for (const row of category.rows) {
        expect(row.label).toBeTruthy();
        expect(row.combos.length).toBeGreaterThan(0);
        for (const combo of row.combos) {
          expect(combo.length).toBeGreaterThan(0);
          // No empty/undefined keycaps leaked from a missing registry id.
          expect(combo.every((token) => typeof token === 'string' && token.length > 0)).toBe(true);
        }
      }
    }
  });

  it('renders its hand-written combos in the platform vocabulary', () => {
    const jumpRow = (rows: ReturnType<typeof buildCheatsheet>) => rows
      .flatMap((c) => c.rows)
      .find((r) => r.label === 'Switch to desktop 1–9');
    expect(jumpRow(buildCheatsheet())?.combos[0]).toEqual(['⌘', '1–9']);
    withNavigatorPlatform('Linux aarch64', () => {
      expect(jumpRow(buildCheatsheet())?.combos[0]).toEqual(['Ctrl', 'Shift', '1–9']);
    });
  });

  it('renders Linux action and pane-focus chords from the Linux table', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      const rows = buildCheatsheet().flatMap((category) => category.rows);
      expect(rows.find((row) => row.label === 'Agent palette')?.combos[0])
        .toEqual(['Ctrl', 'Shift', 'K']);
      expect(rows.find((row) => row.label === 'Command palette')?.combos[0])
        .toEqual(['Ctrl', 'Alt', 'K']);
      expect(rows.find((row) => row.label === 'Move focus between panes')?.combos[0])
        .toEqual(['Ctrl', 'Shift', '←↑→↓']);
    });
  });

  it('lists desktop bindings next to the session bindings', () => {
    const rows = buildCheatsheet().flatMap((c) => c.rows);
    expect(rows.find((r) => r.label === 'New session on this desktop')?.combos[0]).toEqual(['⌘', 'N']);
    expect(rows.find((r) => r.label === 'Desktop overview')?.combos[0]).toEqual(['⌘', 'G']);
    expect(rows.some((r) => r.label === 'New workspace')).toBe(false);
  });

  it('includes history bindings and desktop step labels', () => {
    const rows = buildCheatsheet().flatMap((category) => category.rows);
    expect(rows.find((row) => row.label === 'Previous / next desktop')?.combos).toEqual([
      ['⌘', '↑'], ['⌘', '↓'],
    ]);
    expect(rows.find((row) => row.label === 'Back / forward through agent history')?.combos).toEqual([
      ['⌘', '['], ['⌘', ']'],
    ]);
    expect(SHORTCUT_META['session.prev'].label).toBe('Previous desktop');
    expect(SHORTCUT_META['session.next'].label).toBe('Next desktop');
    expect(SHORTCUT_META['session.historyBack'].label).toBe('Back through agent history');
    expect(SHORTCUT_META['session.historyForward'].label).toBe('Forward through agent history');
  });

  it('keeps editor metadata exhaustive with the shortcut registry', () => {
    expect(Object.keys(SHORTCUT_META).sort()).toEqual(Object.keys(SHORTCUTS).sort());
  });

  it('renders Linux history bindings with Ctrl+Shift', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      const rows = buildCheatsheet().flatMap((category) => category.rows);
      expect(rows.find((row) => row.label === 'Back / forward through agent history')?.combos).toEqual([
        ['Ctrl', 'Shift', '{'], ['Ctrl', 'Shift', '}'],
      ]);
    });
  });
});
