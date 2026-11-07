export const VIEWPORT_MARGIN = 8;

export interface ViewportPoint {
  top: number;
  left: number;
}

export function clampIntoViewport(at: ViewportPoint, size: { width: number; height: number }, view: Window): ViewportPoint {
  return {
    top: Math.max(VIEWPORT_MARGIN, Math.min(at.top, view.innerHeight - size.height - VIEWPORT_MARGIN)),
    left: Math.max(VIEWPORT_MARGIN, Math.min(at.left, view.innerWidth - size.width - VIEWPORT_MARGIN)),
  };
}
