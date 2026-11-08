const TILE_HOST = '.tile-host-slot > .tile-host';

export const ROW_PANES = `.session-terminal-panes > .desktop-pane, .session-terminal-panes > ${TILE_HOST} > .desktop-pane`;

export function rowPaneElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(`:scope > [data-pane-id], :scope > ${TILE_HOST} > [data-pane-id]`));
}
