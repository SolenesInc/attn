export function annotationSurfaceOwnsFocus(
  desktopId: string,
  active: Element | null = document.activeElement,
): boolean {
  return (
    active?.closest('.anno-popup, .anno-panel')?.getAttribute('data-desktop-id') === desktopId
  );
}
