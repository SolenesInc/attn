export function annotationSurfaceOwnsFocus(
  workspaceId: string,
  active: Element | null = document.activeElement,
): boolean {
  return (
    active?.closest('.anno-popup, .anno-panel')?.getAttribute('data-workspace-id') === workspaceId
  );
}
