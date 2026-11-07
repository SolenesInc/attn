import { TILE_MIN_WIDTH } from './SessionTerminalDesktop/desktopLayout';

export const SIDEBAR_WIDTH = { min: 200, default: 240, max: 600, step: 40 } as const;

export function storedSidebarPx(setting: string | undefined): number {
  return Number(setting) || SIDEBAR_WIDTH.default;
}

export function widestSidebarPx(windowPx: number): number {
  return Math.min(SIDEBAR_WIDTH.max, windowPx - TILE_MIN_WIDTH);
}

export function clampSidebarPx(px: number, windowPx: number): number {
  return Math.max(SIDEBAR_WIDTH.min, Math.min(Math.round(px), widestSidebarPx(windowPx)));
}

export function renderedSidebarWidth(px: number): string {
  return `min(${px}px, calc(100vw - ${TILE_MIN_WIDTH}px))`;
}

export function sidebarWidthLabel(px: number, windowPx: number): string {
  if (px <= SIDEBAR_WIDTH.min) return `${px}px is the narrowest`;
  if (px < widestSidebarPx(windowPx)) return `${px}px`;
  return px === SIDEBAR_WIDTH.max ? `${px}px is the widest` : `${px}px is the widest: tiles keep ${TILE_MIN_WIDTH}px`;
}

export type SidebarStep = { kind: 'step'; px: number } | { kind: 'limit'; message: string };

export function stepSidebarWidth(current: number, direction: 'wider' | 'narrower', windowPx: number): SidebarStep {
  const shownPx = clampSidebarPx(current, windowPx);
  const widestPx = widestSidebarPx(windowPx);
  if (direction === 'narrower') {
    if (shownPx <= SIDEBAR_WIDTH.min) return { kind: 'limit', message: `The sidebar can't go below ${SIDEBAR_WIDTH.min}px.` };
    return { kind: 'step', px: Math.max(shownPx - SIDEBAR_WIDTH.step, SIDEBAR_WIDTH.min) };
  }
  if (shownPx < widestPx) return { kind: 'step', px: Math.min(shownPx + SIDEBAR_WIDTH.step, widestPx) };
  return {
    kind: 'limit',
    message: widestPx === SIDEBAR_WIDTH.max
      ? `The sidebar can't pass ${widestPx}px.`
      : `The sidebar can't pass ${widestPx}px with the window at ${windowPx}px: tiles keep ${TILE_MIN_WIDTH}px.`,
  };
}
