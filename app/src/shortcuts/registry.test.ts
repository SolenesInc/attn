import { describe, it, expect } from 'vitest';
import {
  MAC_SHORTCUTS,
  LINUX_SHORTCUTS,
  ShortcutDef,
  Chord,
  matchesShortcut,
  bindingsConflict,
  combosConflict,
  validateNoConflicts,
  type ShortcutId,
} from './registry';
import { withNavigatorPlatform } from '../test/platformStub';

const MAC = 'MacIntel';
const WINDOWS = 'Win32';
const LINUX = 'Linux aarch64';

describe('shortcut registry', () => {
  it.each<[string, string, KeyboardEventInit, ShortcutDef, boolean]>([
    ['Cmd+N on macOS', MAC, { key: 'n', metaKey: true }, { key: 'n', meta: true }, true],
    ['Ctrl+N standing in for Cmd+N on Windows', WINDOWS, { key: 'n', ctrlKey: true }, { key: 'n', meta: true }, true],
    ['Ctrl+N standing in for Cmd+N on Linux', LINUX, { key: 'n', ctrlKey: true }, { key: 'n', meta: true }, true],
    ['Ctrl+W for Cmd+W on macOS', MAC, { key: 'w', ctrlKey: true }, { key: 'w', meta: true }, false],
    ['Cmd+Shift+W on macOS', MAC, { key: 'W', metaKey: true, shiftKey: true }, { key: 'w', meta: true, shift: true }, true],
    ['Shift+~ with no primary modifier', MAC, { key: '~', shiftKey: true }, { key: '~', shift: true }, true],
    ['N without the required Cmd', MAC, { key: 'n' }, { key: 'n', meta: true }, false],
    ['Cmd+W without the required Shift', MAC, { key: 'w', metaKey: true }, { key: 'w', meta: true, shift: true }, false],
    ['Cmd+Shift+N for Cmd+N', MAC, { key: 'N', metaKey: true, shiftKey: true }, { key: 'n', meta: true }, false],
    ['Cmd+Alt+N for Cmd+N', MAC, { key: 'n', metaKey: true, altKey: true }, { key: 'n', meta: true }, false],
    ['Cmd+M for Cmd+N', MAC, { key: 'm', metaKey: true }, { key: 'n', meta: true }, false],
    ['Cmd+n for a binding written Cmd+N', MAC, { key: 'n', metaKey: true }, { key: 'N', meta: true }, true],
    ['Cmd+` on macOS', MAC, { key: '`', metaKey: true }, { key: '`', meta: true }, true],
    ['Cmd+Shift+{ on macOS', MAC, { key: '{', metaKey: true, shiftKey: true }, { key: '{', meta: true, shift: true }, true],
    ['Cmd+Alt+ArrowLeft on macOS', MAC, { key: 'ArrowLeft', metaKey: true, altKey: true }, { key: 'ArrowLeft', meta: true, alt: true }, true],
    ['Cmd+2 typed as ™ by its physical key', MAC, { key: '™', code: 'Digit2', metaKey: true }, { key: '2', code: 'Digit2', meta: true }, true],
    ['Cmd+[ as history back on macOS', MAC, { key: '[', code: 'BracketLeft', metaKey: true }, MAC_SHORTCUTS['session.historyBack'], true],
    ['Cmd+] as history forward on macOS', MAC, { key: ']', code: 'BracketRight', metaKey: true }, MAC_SHORTCUTS['session.historyForward'], true],
    ['Ctrl+Shift+{ as history back on Linux', LINUX, { key: '{', code: 'BracketLeft', ctrlKey: true, shiftKey: true }, LINUX_SHORTCUTS['session.historyBack'], true],
    ['Ctrl+Shift+} as history forward on Linux', LINUX, { key: '}', code: 'BracketRight', ctrlKey: true, shiftKey: true }, LINUX_SHORTCUTS['session.historyForward'], true],
    ['Ctrl+[ as history back on Linux', LINUX, { key: '[', code: 'BracketLeft', ctrlKey: true }, LINUX_SHORTCUTS['session.historyBack'], false],
  ])('%s on %s matches: %s', (_name, platform, init, def, matches) => {
    withNavigatorPlatform(platform, () => {
      expect(matchesShortcut(new KeyboardEvent('keydown', init), def)).toBe(matches);
    });
  });

  describe('platform defaults', () => {
    it('binds every shortcut id on both platforms and validates both tables', () => {
      expect(Object.keys(LINUX_SHORTCUTS)).toEqual(Object.keys(MAC_SHORTCUTS));
      expect(() => validateNoConflicts(MAC_SHORTCUTS)).not.toThrow();
      expect(() => validateNoConflicts(LINUX_SHORTCUTS)).not.toThrow();
    });

    it('keeps every plain Ctrl+letter free on Linux', () => {
      for (const [id, binding] of Object.entries(LINUX_SHORTCUTS) as Array<[ShortcutId, ShortcutDef]>) {
        if (!/^[a-z]$/i.test(binding.key)) continue;
        const event = new KeyboardEvent('keydown', {
          key: binding.key,
          code: `Key${binding.key.toUpperCase()}`,
          ctrlKey: true,
        });
        withNavigatorPlatform('Linux aarch64', () => {
          expect(matchesShortcut(event, binding), id).toBe(false);
        });
      }
    });

    it('puts every Linux default behind Shift or Alt', () => {
      for (const [id, binding] of Object.entries(LINUX_SHORTCUTS) as Array<[ShortcutId, ShortcutDef]>) {
        expect(binding.meta, id).toBe(true);
        expect(Boolean(binding.shift || binding.alt), id).toBe(true);
      }
    });

    it('avoids Ubuntu 24.04 desktop-reserved Ctrl+Alt chords', () => {
      const reserved = new Set([
        't',
        'delete',
        'tab',
        'escape',
        'arrowleft',
        'arrowright',
        'arrowup',
        'arrowdown',
      ]);
      for (const [id, binding] of Object.entries(LINUX_SHORTCUTS) as Array<[ShortcutId, ShortcutDef]>) {
        if (!binding.alt || binding.shift) continue;
        expect(reserved.has(binding.key.toLowerCase()), id).toBe(false);
      }
    });

    it('adds Shift to every macOS Cmd+letter action that had no Shift', () => {
      for (const [id, mac] of Object.entries(MAC_SHORTCUTS) as Array<[ShortcutId, ShortcutDef]>) {
        if (!mac.meta || mac.shift || !/^[a-z]$/i.test(mac.key)) continue;
        const linux: ShortcutDef = LINUX_SHORTCUTS[id];
        expect(linux.key, id).toBe(mac.key);
        expect(linux.meta, id).toBe(true);
        expect(linux.shift, id).toBe(true);
        expect(Boolean(linux.alt), id).toBe(Boolean(mac.alt));
      }
    });

    it('moves the former Cmd+Shift letter actions to Ctrl+Alt', () => {
      const rehomed: ShortcutId[] = [
        'terminal.splitHorizontal',
        'terminal.toggleZoom',
        'session.newHorizontal',
        'session.goToDashboard',
        'session.settle',
        'session.snooze',
        'session.toggleSidebar',
        'dock.attention',
        'board.open',
      ];
      for (const id of rehomed) {
        const linux: ShortcutDef = LINUX_SHORTCUTS[id];
        expect(linux, id).toMatchObject({ meta: true, alt: true });
        expect(linux.shift, id).not.toBe(true);
      }
    });
  });

  const leaderK: Chord = { leader: { key: 'k', meta: true }, then: { key: 'd' } };

  it.each<[string, ShortcutDef | Chord, ShortcutDef | Chord, boolean]>([
    ['the same combo', { key: 'g', meta: true }, { key: 'g', meta: true }, true],
    ['combos that differ by Shift', { key: 'g', meta: true }, { key: 'g', meta: true, shift: true }, false],
    ['different keys', { key: 'a' }, { key: 'b' }, false],
    ['different keys on the same physical key', { key: '1', code: 'Digit1', meta: true }, { key: '&', code: 'Digit1', meta: true }, true],
    ['chords sharing a leader with different follow keys', leaderK, { leader: { key: 'k', meta: true }, then: { key: 'g' } }, false],
    ['chords sharing a leader and follow key', leaderK, { leader: { key: 'k', meta: true }, then: { key: 'd' } }, true],
    ['a chord and a combo equal to its leader', leaderK, { key: 'k', meta: true }, true],
    ['a chord and a combo equal to its follow key', leaderK, { key: 'd' }, false],
  ])('%s conflict: %s', (_name, a, b, conflicts) => {
    expect(bindingsConflict(a, b)).toBe(conflicts);
    expect(bindingsConflict(b, a)).toBe(conflicts);
    if (!('leader' in a) && !('leader' in b)) expect(combosConflict(a, b)).toBe(conflicts);
  });
});
