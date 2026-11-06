import { ATTENTION_MIN_WIDTH } from './SessionTerminalDesktop/attentionLayout';

export const SIDEBAR_WIDTH = { min: 200, default: 240, max: 600 } as const;

export function storedSidebarPx(setting: string | undefined): number {
  return Number(setting) || SIDEBAR_WIDTH.default;
}

export function widestSidebarPx(windowPx: number): number {
  return Math.min(SIDEBAR_WIDTH.max, windowPx - ATTENTION_MIN_WIDTH);
}

export function clampSidebarPx(px: number, windowPx: number): number {
  return Math.max(SIDEBAR_WIDTH.min, Math.min(Math.round(px), widestSidebarPx(windowPx)));
}

export function renderedSidebarWidth(px: number): string {
  return `min(${px}px, calc(100vw - ${ATTENTION_MIN_WIDTH}px))`;
}

export function sidebarWidthLabel(px: number, windowPx: number): string {
  if (px <= SIDEBAR_WIDTH.min) return `${px}px is the narrowest`;
  if (px < widestSidebarPx(windowPx)) return `${px}px`;
  return px === SIDEBAR_WIDTH.max ? `${px}px is the widest` : `${px}px is the widest: tiles keep ${ATTENTION_MIN_WIDTH}px`;
}
