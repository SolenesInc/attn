import { useState } from 'react';

export function useFocusedLeaf(
  leafIds: ReadonlySet<string>,
  agentPanes: ReadonlyMap<string, { sessionId: string }>,
  selectedSessionId: string | null,
) {
  const [focusedLeafId, setFocusedLeafId] = useState<string | null>(null);
  const focusedSessionId = focusedLeafId ? agentPanes.get(focusedLeafId)?.sessionId : undefined;
  const invalid =
    focusedLeafId !== null &&
    (!leafIds.has(focusedLeafId) ||
      (focusedSessionId !== undefined && focusedSessionId !== selectedSessionId));
  if (invalid) setFocusedLeafId(null);
  return [invalid ? null : focusedLeafId, setFocusedLeafId] as const;
}
