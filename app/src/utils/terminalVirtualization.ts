// A mounted pane costs ~32 MiB of atlas + GPU texture plus scrollback, and every
// workspace stays mounted — so only the N most-recently-used are kept warm.

export const WARM_WORKSPACE_LIMIT_STORAGE_KEY = 'attn.perf.warmWorkspaceLimit';
export const DEFAULT_WARM_WORKSPACE_LIMIT = 3;

export function readWarmWorkspaceLimit(): number {
  try {
    const raw = window.localStorage.getItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY);
    if (raw === null) return DEFAULT_WARM_WORKSPACE_LIMIT;
    const parsed = Number.parseInt(raw, 10);
    return Number.isFinite(parsed) ? parsed : DEFAULT_WARM_WORKSPACE_LIMIT;
  } catch {
    return DEFAULT_WARM_WORKSPACE_LIMIT;
  }
}

export function writeWarmWorkspaceLimit(limit: number): void {
  try {
    window.localStorage.setItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY, String(limit));
  } catch {
  }
}

export function computeWarmWorkspaceIds(
  allWorkspaceIds: string[],
  recentWorkspaceIds: string[],
  activeWorkspaceId: string | null,
  limit: number,
  requiredWorkspaceIds: string[] = [],
): Set<string> | null {
  if (limit < 0) return null;
  const budget = limit + 1;
  const present = new Set(allWorkspaceIds);
  const warm = new Set<string>();
  for (const id of requiredWorkspaceIds) {
    if (present.has(id)) warm.add(id);
  }
  if (activeWorkspaceId && present.has(activeWorkspaceId)) warm.add(activeWorkspaceId);
  for (const id of recentWorkspaceIds) {
    if (warm.size >= budget) break;
    if (present.has(id)) warm.add(id);
  }
  return warm;
}
