// Render shortcut definitions as human-readable key tokens. Modifier names come from the
// platform (⌘⌥⇧ on macOS, Ctrl/Alt/Shift elsewhere); matching lives in registry.ts.

import { ShortcutId, Binding, Combo, isChord } from './registry';
import { resolveBinding } from './resolver';
import { ModifierName, keyJoiner, modifierGlyphs } from './platform';

// Single-character keys are upper-cased; any other key name falls through unchanged.
const KEY_SYMBOLS: Record<string, string> = {
  ArrowLeft: '←',
  ArrowRight: '→',
  ArrowUp: '↑',
  ArrowDown: '↓',
  Enter: '⏎',
  Escape: 'Esc',
  ' ': 'Space',
};

function keyToken(key: string): string {
  if (KEY_SYMBOLS[key]) return KEY_SYMBOLS[key];
  return key.length === 1 ? key.toUpperCase() : key;
}

// A string id resolves through the override-aware resolver so display reflects rebinds; a passed
// Binding is used verbatim. Returns null when the id resolves to "unbound".
function resolve(idOrBinding: ShortcutId | Binding): Binding | null {
  return typeof idOrBinding === 'string' ? resolveBinding(idOrBinding) : idOrBinding;
}

function comboModifiers(combo: Combo): string[] {
  const glyphs = modifierGlyphs();
  const tokens: string[] = [];
  const push = (glyph: string) => { if (!tokens.includes(glyph)) tokens.push(glyph); };
  if (combo.meta) push(glyphs.accel);
  if (combo.ctrl) push(glyphs.ctrl);
  if (combo.alt) push(glyphs.alt);
  if (combo.shift) push(glyphs.shift);
  return tokens;
}

function comboTokens(combo: Combo): string[] {
  return [...comboModifiers(combo), keyToken(combo.key)];
}

export function modifierTokens(idOrBinding: ShortcutId | Binding): string[] {
  const binding = resolve(idOrBinding);
  if (!binding) return [];
  return comboModifiers(isChord(binding) ? binding.leader : binding);
}

export function shortcutTokens(idOrBinding: ShortcutId | Binding): string[] {
  const binding = resolve(idOrBinding);
  if (!binding) return [];
  if (isChord(binding)) {
    return [...comboTokens(binding.leader), 'then', ...comboTokens(binding.then)];
  }
  return comboTokens(binding);
}

export function formatShortcut(idOrBinding: ShortcutId | Binding): string {
  const binding = resolve(idOrBinding);
  if (!binding) return '';
  const join = keyJoiner();
  if (isChord(binding)) {
    return `${comboTokens(binding.leader).join(join)} then ${comboTokens(binding.then).join(join)}`;
  }
  return comboTokens(binding).join(join);
}

/** A combo written out for prose, e.g. keyCombo('accel', 'shift', 'T') → ⌘⇧T or Ctrl+Shift+T. */
export function keyCombo(...parts: Array<ModifierName | string>): string {
  const glyphs = modifierGlyphs();
  return parts
    .map((part) => glyphs[part as ModifierName] ?? part)
    .join(keyJoiner());
}

/** The tokens of `keyCombo`, for callers that render one keycap per token. */
export function keyComboTokens(...parts: Array<ModifierName | string>): string[] {
  const glyphs = modifierGlyphs();
  return parts.map((part) => glyphs[part as ModifierName] ?? part);
}
