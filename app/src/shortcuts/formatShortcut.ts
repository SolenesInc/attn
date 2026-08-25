// Render shortcut definitions as human-readable key tokens. attn ships as a macOS app, so
// shortcuts always render with Mac glyphs; keystroke *matching* lives in registry.ts.

import { ShortcutId, Binding, Combo, isChord } from './registry';
import { resolveBinding } from './resolver';

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

function comboTokens(combo: Combo): string[] {
  const tokens: string[] = [];
  if (combo.meta) tokens.push('⌘');
  if (combo.ctrl) tokens.push('⌃');
  if (combo.alt) tokens.push('⌥');
  if (combo.shift) tokens.push('⇧');
  tokens.push(keyToken(combo.key));
  return tokens;
}

export function modifierTokens(idOrBinding: ShortcutId | Binding): string[] {
  const binding = resolve(idOrBinding);
  if (!binding) return [];
  const combo = isChord(binding) ? binding.leader : binding;
  const tokens: string[] = [];
  if (combo.meta) tokens.push('⌘');
  if (combo.ctrl) tokens.push('⌃');
  if (combo.alt) tokens.push('⌥');
  if (combo.shift) tokens.push('⇧');
  return tokens;
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
  if (isChord(binding)) {
    return `${comboTokens(binding.leader).join('')} then ${comboTokens(binding.then).join('')}`;
  }
  return comboTokens(binding).join('');
}
