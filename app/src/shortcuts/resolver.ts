
import {
  SHORTCUTS,
  ShortcutDef,
  ShortcutId,
  Combo,
  Binding,
  bindingsConflict,
  isAllowedConflict,
  isChord,
} from './registry';
import { isMacLikePlatform } from './platform';

export interface DockConfig {
  collapsed: boolean;
  items: ShortcutId[];
}

export interface KeybindingsConfig {
  version: 1;
  overrides: Partial<Record<ShortcutId, Binding | null>>;
  dock: DockConfig;
}

export const DEFAULT_DOCK_ITEMS: ShortcutId[] = [
  'dock.attention',
  'terminal.splitVertical',
  'terminal.splitHorizontal',
  'session.newHorizontal',
  'terminal.toggleZoom',
  'session.toggleSidebar',
];

export const DEFAULT_DOCK: DockConfig = { collapsed: false, items: DEFAULT_DOCK_ITEMS };

export const EMPTY_KEYBINDINGS_CONFIG: KeybindingsConfig = {
  version: 1,
  overrides: {},
  dock: DEFAULT_DOCK,
};

export const KEYBINDINGS_SETTING_KEY = 'keybindings_config';

let overrides: Partial<Record<ShortcutId, Binding | null>> = {};

export function setShortcutOverrides(next: Partial<Record<ShortcutId, Binding | null>>): void {
  overrides = { ...next };
}

export function getShortcutOverrides(): Partial<Record<ShortcutId, Binding | null>> {
  return overrides;
}

export function resolveBinding(id: ShortcutId): Binding | null {
  if (Object.prototype.hasOwnProperty.call(overrides, id)) {
    const ov = overrides[id];
    return ov ?? null;
  }
  return SHORTCUTS[id];
}

export function isUnbound(id: ShortcutId): boolean {
  return resolveBinding(id) === null;
}

export function isCustomized(id: ShortcutId): boolean {
  return Object.prototype.hasOwnProperty.call(overrides, id);
}

export function resolvedShortcutEntries(): Array<[ShortcutId, Binding]> {
  const entries: Array<[ShortcutId, Binding]> = [];
  for (const id of Object.keys(SHORTCUTS) as ShortcutId[]) {
    const def = resolveBinding(id);
    if (def) entries.push([id, def]);
  }
  return entries;
}

export function findConflict(binding: Binding, excludeId: ShortcutId): ShortcutId | null {
  for (const [id, d] of resolvedShortcutEntries()) {
    if (id === excludeId) continue;
    // The allowed-conflict exemption holds only for two plain combos dispatch
    // disambiguates by target; a chord leader arms globally, so it still conflicts.
    if (!isChord(binding) && !isChord(d) && isAllowedConflict(excludeId, id)) continue;
    if (bindingsConflict(binding, d)) return id;
  }
  return null;
}

const MODIFIER_KEYS = new Set([
  'Meta', 'Control', 'Shift', 'Alt', 'AltGraph', 'OS', 'Hyper', 'Super',
  'CapsLock', 'Fn', 'FnLock', 'NumLock', 'ScrollLock', 'Dead',
]);

export type CaptureResult =
  | { kind: 'binding'; def: ShortcutDef }
  | { kind: 'ignored' }
  | { kind: 'error'; message: string };

// Control is rejected on macOS: the matcher treats it as the accelerator only
// off-Mac, so a Ctrl-only binding would never fire.
export function eventToBinding(e: KeyboardEvent): CaptureResult {
  if (!e.key || MODIFIER_KEYS.has(e.key)) return { kind: 'ignored' };

  if (isMacLikePlatform() && e.ctrlKey && !e.metaKey) {
    return {
      kind: 'error',
      message: 'Control isn’t available as a shortcut modifier on macOS. Use ⌘, ⌥, or ⇧.',
    };
  }

  const def: ShortcutDef = { key: e.key };
  // Off-mac Ctrl IS the accelerator, so it records as `meta`: one stored shape, both platforms.
  if (e.metaKey || e.ctrlKey) def.meta = true;
  if (e.altKey) def.alt = true;
  if (e.shiftKey) def.shift = true;
  // Keep code for keys whose `key` is layout/locale dependent (digits, named keys).
  if (/^Digit\d$/.test(e.code) || e.key.length !== 1) def.code = e.code;

  return { kind: 'binding', def };
}

export function isRiskyBinding(binding: Binding): boolean {
  const combo = isChord(binding) ? binding.leader : binding;
  return !combo.meta && !combo.ctrl && !combo.alt;
}

function sanitizeCombo(value: unknown): Combo | null {
  if (!value || typeof value !== 'object') return null;
  const v = value as Record<string, unknown>;
  if (typeof v.key !== 'string' || v.key.length === 0) return null;
  const def: Combo = { key: v.key };
  if (typeof v.code === 'string') def.code = v.code;
  if (v.meta === true) def.meta = true;
  if (v.ctrl === true) def.ctrl = true;
  if (v.alt === true) def.alt = true;
  if (v.shift === true) def.shift = true;
  if (v.editableTarget === 'native') def.editableTarget = 'native';
  return def;
}

function sanitizeBinding(value: unknown): Binding | null {
  if (value && typeof value === 'object' && ('leader' in value || 'then' in value)) {
    const v = value as Record<string, unknown>;
    const leader = sanitizeCombo(v.leader);
    const then = sanitizeCombo(v.then);
    if (!leader || !then) return null;
    return { leader, then };
  }
  return sanitizeCombo(value);
}

function emptyConfig(): KeybindingsConfig {
  return { version: 1, overrides: {}, dock: defaultDock() };
}

function defaultDock(): DockConfig {
  return { collapsed: false, items: [...DEFAULT_DOCK_ITEMS] };
}

function sanitizeDock(value: unknown): DockConfig {
  if (!value || typeof value !== 'object') return defaultDock();
  const v = value as Record<string, unknown>;
  if (!Array.isArray(v.items)) return defaultDock();
  const seen = new Set<string>();
  const items: ShortcutId[] = [];
  for (const id of v.items) {
    if (typeof id !== 'string') continue;
    if (!Object.prototype.hasOwnProperty.call(SHORTCUTS, id)) continue;
    if (seen.has(id)) continue;
    seen.add(id);
    items.push(id as ShortcutId);
  }
  return { collapsed: v.collapsed === true, items };
}

export function parseKeybindingsConfig(raw: string | undefined | null): KeybindingsConfig {
  if (!raw) return emptyConfig();
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return emptyConfig();
  }
  if (!parsed || typeof parsed !== 'object') return emptyConfig();

  const rawOverrides = (parsed as Record<string, unknown>).overrides;
  const overridesOut: Partial<Record<ShortcutId, Binding | null>> = {};
  if (rawOverrides && typeof rawOverrides === 'object') {
    for (const [id, value] of Object.entries(rawOverrides as Record<string, unknown>)) {
      if (!Object.prototype.hasOwnProperty.call(SHORTCUTS, id)) continue;
      if (value === null) {
        overridesOut[id as ShortcutId] = null;
        continue;
      }
      const binding = sanitizeBinding(value);
      if (binding) overridesOut[id as ShortcutId] = binding;
    }
  }

  return {
    version: 1,
    overrides: overridesOut,
    dock: sanitizeDock((parsed as Record<string, unknown>).dock),
  };
}

export function serializeKeybindingsConfig(config: KeybindingsConfig): string {
  return JSON.stringify(config);
}
