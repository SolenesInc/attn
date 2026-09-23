import { formatShortcut } from '../shortcuts/formatShortcut';
import type { ShortcutId } from '../shortcuts/registry';
import type { Desktop } from '../types/generated';

export const SHORTCUT_SLOTS = [1, 2, 3, 4, 5, 6, 7, 8, 9] as const;

function byOrderKey(a: Desktop, b: Desktop): number {
  return a.order_key < b.order_key ? -1 : a.order_key > b.order_key ? 1 : 0;
}

export function slottedDesktops(desktops: Desktop[]): Desktop[] {
  return desktops
    .filter((desktop) => desktop.shortcut_slot)
    .sort((a, b) => (a.shortcut_slot ?? 0) - (b.shortcut_slot ?? 0));
}

export function extraDesktops(desktops: Desktop[]): Desktop[] {
  return desktops.filter((desktop) => !desktop.shortcut_slot).sort(byOrderKey);
}

export function orderedDesktops(desktops: Desktop[]): Desktop[] {
  return [...slottedDesktops(desktops), ...extraDesktops(desktops)];
}

export function desktopNumber(desktop: Desktop, desktops: Desktop[]): number {
  if (desktop.shortcut_slot) return desktop.shortcut_slot;
  return SHORTCUT_SLOTS.length + extraDesktops(desktops).findIndex((entry) => entry.id === desktop.id) + 1;
}

export function desktopLabel(desktop: Desktop, desktops: Desktop[]): string {
  return `Desktop ${desktopNumber(desktop, desktops)}`;
}

export function desktopInSlot(desktops: Desktop[], slot: number): Desktop | undefined {
  return desktops.find((desktop) => desktop.shortcut_slot === slot);
}

export function firstFreeSlot(desktops: Desktop[]): number | null {
  return SHORTCUT_SLOTS.find((slot) => !desktopInSlot(desktops, slot)) ?? null;
}

export function isEmptyDesktop(desktop: Desktop): boolean {
  return desktop.tree_json.trim() === '';
}

export function slotShortcut(slot: number): string {
  return formatShortcut(`workspace.select${slot}` as ShortcutId);
}
