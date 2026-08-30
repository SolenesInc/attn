// app/src/shortcuts/formatShortcut.test.ts
import { describe, it, expect } from 'vitest';
import { formatShortcut, keyCombo, shortcutTokens, modifierTokens } from './formatShortcut';
import { withNavigatorPlatform } from '../test/platformStub';

describe('formatShortcut', () => {
  it('renders modifiers as Mac glyphs in ⌘ ⌥ ⇧ order', () => {
    expect(formatShortcut('session.new')).toBe('⌘N');
    expect(formatShortcut('session.newHorizontal')).toBe('⌘⇧N');
    expect(formatShortcut('session.newWorkspace')).toBe('⌘T');
    expect(formatShortcut('terminal.focusLeft')).toBe('⌘⌥←');
  });

  it('renders the close-session binding as ⌘W (guards against ⌘⇧W drift)', () => {
    expect(formatShortcut('session.close')).toBe('⌘W');
  });

  it('maps non-printable keys to symbols', () => {
    expect(formatShortcut('terminal.toggleMaximize')).toBe('⌘⇧⏎');
    expect(formatShortcut('terminal.focusDown')).toBe('⌘⌥↓');
  });

  it('exposes tokens as one entry per keycap', () => {
    expect(shortcutTokens('session.newHorizontal')).toEqual(['⌘', '⇧', 'N']);
    expect(modifierTokens('terminal.focusLeft')).toEqual(['⌘', '⌥']);
  });

  describe('chords', () => {
    const chord = { leader: { key: 'k', meta: true }, then: { key: 'd' } };

    it('renders a chord as "leader then follow"', () => {
      expect(formatShortcut(chord)).toBe('⌘K then D');
    });

    it('renders a chord with a modified follow key', () => {
      expect(formatShortcut({ leader: { key: 'k', meta: true }, then: { key: 'd', meta: true } }))
        .toBe('⌘K then ⌘D');
    });

    it('joins chord steps with a literal "then" token for keycap renderers', () => {
      expect(shortcutTokens(chord)).toEqual(['⌘', 'K', 'then', 'D']);
    });

    it('reports the leader modifiers for a chord', () => {
      expect(modifierTokens(chord)).toEqual(['⌘']);
    });
  });
});

describe('off-mac rendering', () => {
  it('writes the accelerator as Ctrl and joins tokens with +', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      expect(formatShortcut('session.new')).toBe('Ctrl+N');
      expect(formatShortcut('session.newHorizontal')).toBe('Ctrl+Shift+N');
      expect(formatShortcut('notebook.openFullscreen')).toBe('Ctrl+Alt+Shift+N');
      expect(formatShortcut('terminal.toggleMaximize')).toBe('Ctrl+Shift+⏎');
    });
  });

  it('gives one keycap per modifier and folds ctrl into the accelerator', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      expect(shortcutTokens('terminal.focusLeft')).toEqual(['Ctrl', 'Alt', '←']);
      expect(shortcutTokens({ key: 'k', meta: true, ctrl: true })).toEqual(['Ctrl', 'K']);
      expect(keyCombo('accel', 'shift', 'T')).toBe('Ctrl+Shift+T');
    });
    withNavigatorPlatform('MacIntel', () => {
      expect(keyCombo('accel', 'shift', 'T')).toBe('⌘⇧T');
    });
  });
});
