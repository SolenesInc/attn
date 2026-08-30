export function isMacLikePlatform(): boolean {
  if (typeof navigator === 'undefined') return false;

  const platform = String(navigator.platform || '').toLowerCase();
  if (platform.includes('mac')) return true;

  const ua = String(navigator.userAgent || '').toLowerCase();
  return ua.includes('mac os') || ua.includes('macintosh');
}

export type ModifierBearingEvent = Pick<KeyboardEvent, 'metaKey' | 'ctrlKey'>;

export function isAccelKeyPressed(e: ModifierBearingEvent): boolean {
  // Non-mac accepts Ctrl or Meta: CI/Playwright sends Meta even on Linux runners.
  return isMacLikePlatform() ? e.metaKey : e.ctrlKey || e.metaKey;
}

export type ModifierName = 'accel' | 'ctrl' | 'alt' | 'shift';

export type ModifierGlyphs = Record<ModifierName, string>;

const MAC_GLYPHS: ModifierGlyphs = { accel: '⌘', ctrl: '⌃', alt: '⌥', shift: '⇧' };
const PC_GLYPHS: ModifierGlyphs = { accel: 'Ctrl', ctrl: 'Ctrl', alt: 'Alt', shift: 'Shift' };

export function modifierGlyphs(): ModifierGlyphs {
  return isMacLikePlatform() ? MAC_GLYPHS : PC_GLYPHS;
}

export function keyJoiner(): string {
  return isMacLikePlatform() ? '' : '+';
}

export type TerminalClipboardChord = 'copy' | 'copyCommand' | 'paste' | null;

export interface ClipboardChordEvent extends ModifierBearingEvent {
  key: string;
  code: string;
  shiftKey: boolean;
  altKey: boolean;
}

function clipboardLetter(e: ClipboardChordEvent): 'c' | 'v' | null {
  if (e.key.toLowerCase() === 'c' || e.code === 'KeyC') return 'c';
  if (e.key.toLowerCase() === 'v' || e.code === 'KeyV') return 'v';
  return null;
}

// Off-mac Ctrl+C is the PTY's interrupt, so the terminal's clipboard also takes Ctrl+Shift,
// and block copy takes Ctrl+Alt+C (GTK leaves it unclaimed). The Mac Meta chords answer everywhere.
export function terminalClipboardChord(e: ClipboardChordEvent): TerminalClipboardChord {
  const letter = clipboardLetter(e);
  if (!letter) return null;
  if (e.metaKey) {
    if (letter === 'v') return 'paste';
    return e.shiftKey ? 'copyCommand' : 'copy';
  }
  if (isMacLikePlatform() || !e.ctrlKey) return null;
  if (e.altKey) return letter === 'c' && !e.shiftKey ? 'copyCommand' : null;
  if (!e.shiftKey) return null;
  return letter === 'v' ? 'paste' : 'copy';
}
