import { describe, it, expect } from 'vitest';
import {
  isAccelKeyPressed,
  isMacLikePlatform,
  keyJoiner,
  modifierGlyphs,
  terminalClipboardChord,
} from './platform';
import { withNavigatorPlatform } from '../test/platformStub';

// Not a real KeyboardEvent: jsdom reports AltGraph pressed whenever altKey is set,
// so the AltGr distinction only exists on a hand-built event.
function chordEvent(init: Partial<KeyboardEventInit> & { key: string; code: string }) {
  return {
    key: init.key,
    code: init.code,
    metaKey: !!init.metaKey,
    ctrlKey: !!init.ctrlKey,
    altKey: !!init.altKey,
    shiftKey: !!init.shiftKey,
    getModifierState: (name: string) => name === 'AltGraph' && !!init.modifierAltGraph,
  };
}

describe('modifier glyphs', () => {
  it('names the accelerator ⌘ on macOS and Ctrl elsewhere', () => {
    withNavigatorPlatform('MacIntel', () => {
      expect(isMacLikePlatform()).toBe(true);
      expect(modifierGlyphs()).toEqual({ accel: '⌘', ctrl: '⌃', alt: '⌥', shift: '⇧' });
      expect(keyJoiner()).toBe('');
    });
    withNavigatorPlatform('Linux aarch64', () => {
      expect(isMacLikePlatform()).toBe(false);
      expect(modifierGlyphs()).toEqual({ accel: 'Ctrl', ctrl: 'Ctrl', alt: 'Alt', shift: 'Shift' });
      expect(keyJoiner()).toBe('+');
    });
  });
});

describe('isAccelKeyPressed', () => {
  it('accepts only Meta on macOS, Meta or Ctrl elsewhere', () => {
    const meta = { metaKey: true, ctrlKey: false };
    const ctrl = { metaKey: false, ctrlKey: true };
    withNavigatorPlatform('MacIntel', () => {
      expect(isAccelKeyPressed(meta)).toBe(true);
      expect(isAccelKeyPressed(ctrl)).toBe(false);
    });
    withNavigatorPlatform('Linux aarch64', () => {
      expect(isAccelKeyPressed(meta)).toBe(true);
      expect(isAccelKeyPressed(ctrl)).toBe(true);
    });
  });
});

describe('terminalClipboardChord', () => {
  it('uses the Command chords on macOS', () => {
    withNavigatorPlatform('MacIntel', () => {
      expect(terminalClipboardChord(chordEvent({ key: 'c', code: 'KeyC', metaKey: true })))
        .toBe('copy');
      expect(terminalClipboardChord(
        chordEvent({ key: 'c', code: 'KeyC', metaKey: true, shiftKey: true }),
      )).toBe('copyCommand');
      expect(terminalClipboardChord(chordEvent({ key: 'v', code: 'KeyV', metaKey: true })))
        .toBe('paste');
      expect(terminalClipboardChord(chordEvent({ key: 'c', code: 'KeyC', ctrlKey: true })))
        .toBeNull();
      expect(terminalClipboardChord(
        chordEvent({ key: 'c', code: 'KeyC', ctrlKey: true, altKey: true }),
      )).toBeNull();
    });
  });

  it('leaves Ctrl+C and Ctrl+V to the PTY off-mac and takes Ctrl+Shift instead', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      expect(terminalClipboardChord(chordEvent({ key: 'c', code: 'KeyC', ctrlKey: true })))
        .toBeNull();
      expect(terminalClipboardChord(chordEvent({ key: 'v', code: 'KeyV', ctrlKey: true })))
        .toBeNull();
      expect(terminalClipboardChord(
        chordEvent({ key: 'C', code: 'KeyC', ctrlKey: true, shiftKey: true }),
      )).toBe('copy');
      expect(terminalClipboardChord(
        chordEvent({ key: 'V', code: 'KeyV', ctrlKey: true, shiftKey: true }),
      )).toBe('paste');
    });
  });

  it('takes Ctrl+Alt+C for block copy off-mac', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      expect(terminalClipboardChord(
        chordEvent({ key: 'c', code: 'KeyC', ctrlKey: true, altKey: true }),
      )).toBe('copyCommand');
      expect(terminalClipboardChord(
        chordEvent({ key: 'v', code: 'KeyV', ctrlKey: true, altKey: true }),
      )).toBeNull();
      expect(terminalClipboardChord(
        chordEvent({ key: 'c', code: 'KeyC', ctrlKey: true, altKey: true, shiftKey: true }),
      )).toBeNull();
      expect(terminalClipboardChord(
        chordEvent({ key: 'c', code: 'KeyC', altKey: true }),
      )).toBeNull();
      expect(terminalClipboardChord(
        chordEvent({ key: 'c', code: 'KeyC', ctrlKey: true, altKey: true, modifierAltGraph: true }),
      )).toBeNull();
    });
  });

  it('also answers the Mac chords off-mac, since Meta is never a PTY key', () => {
    withNavigatorPlatform('Linux aarch64', () => {
      expect(terminalClipboardChord(chordEvent({ key: 'c', code: 'KeyC', metaKey: true })))
        .toBe('copy');
      expect(terminalClipboardChord(
        chordEvent({ key: 'c', code: 'KeyC', metaKey: true, shiftKey: true }),
      )).toBe('copyCommand');
      expect(terminalClipboardChord(chordEvent({ key: 'v', code: 'KeyV', metaKey: true })))
        .toBe('paste');
    });
  });
});
