interface TileFocus {
  tileId: string;
  whileActivePaneId: string;
}

export function newestOpenedTile(
  ids: readonly string[],
  previous: ReadonlySet<string> | null,
): string | null {
  if (!previous) return null;
  for (let index = ids.length - 1; index >= 0; index -= 1) {
    if (!previous.has(ids[index])) return ids[index];
  }
  return null;
}

export function selectedTileId(
  activePaneId: string,
  tiles: ReadonlyMap<string, unknown>,
  automatic: TileFocus | null,
  selected: TileFocus | null,
): string | null {
  for (const focus of [automatic, selected]) {
    if (focus?.whileActivePaneId === activePaneId && tiles.has(focus.tileId)) return focus.tileId;
  }
  return null;
}

export function selectedPaneId(
  activePaneId: string,
  panes: ReadonlyMap<string, unknown>,
  pending: { leafId: string; fromActivePaneId: string } | null,
): string {
  if (!pending || !panes.has(pending.leafId)) return activePaneId;
  return activePaneId === pending.fromActivePaneId || activePaneId === pending.leafId
    ? pending.leafId
    : activePaneId;
}
