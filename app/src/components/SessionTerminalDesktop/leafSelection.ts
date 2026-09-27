export interface PendingLeafFocus {
  leafId: string;
  fromActiveLeafId: string;
}

export function focusedLeafId(
  activeLeafId: string,
  leafIds: readonly string[],
  pending: PendingLeafFocus | null,
): string {
  const pendingStillAhead =
    pending &&
    leafIds.includes(pending.leafId) &&
    (activeLeafId === pending.fromActiveLeafId || activeLeafId === pending.leafId);
  const candidate = pendingStillAhead ? pending.leafId : activeLeafId;
  return leafIds.includes(candidate) ? candidate : (leafIds[0] ?? '');
}
