export const DICTATION_SHORTCUT_SETTING = 'dictation.shortcut';
export const DEFAULT_DICTATION_SHORTCUT = 'Control+Shift+Space';
export const DICTATION_INTERACTION_SETTING = 'dictation.interaction';

export type DictationInteraction = 'insertAtCursor' | 'compareFirst';

export function dictationInteraction(settings: Record<string, string>): DictationInteraction {
  return settings[DICTATION_INTERACTION_SETTING] === 'compare_first' ? 'compareFirst' : 'insertAtCursor';
}

export function dictationShortcut(settings: Record<string, string>): string | null {
  const stored = settings[DICTATION_SHORTCUT_SETTING];
  if (stored === undefined) return DEFAULT_DICTATION_SHORTCUT;
  return stored || null;
}

const SYMBOLS: Record<string, string> = { Super: '⌘', Control: '⌃', Alt: '⌥', Shift: '⇧' };

export function formatNativeShortcut(binding: string) {
  return binding.split('+').map(part => SYMBOLS[part] ?? part.replace(/^Key/, '').replace(/^Digit/, '')).join('');
}
