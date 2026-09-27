import { useCallback, useEffect, type SetStateAction } from 'react';
import { useDesktopFocus } from '../../store/desktopFocus';

export function useFocusedLeaf(
  desktopId: string,
  leafIds: ReadonlySet<string>,
  agentPanes: ReadonlyMap<string, { sessionId: string }>,
  selectedSessionId: string | null,
) {
  const focusedLeafId = useDesktopFocus((state) => state.focusedLeafByDesktop[desktopId] ?? null);
  const setFocusedLeaf = useDesktopFocus((state) => state.setFocusedLeaf);
  const focusedSessionId = focusedLeafId ? agentPanes.get(focusedLeafId)?.sessionId : undefined;
  const invalid =
    focusedLeafId !== null &&
    (!leafIds.has(focusedLeafId) ||
      (focusedSessionId !== undefined && focusedSessionId !== selectedSessionId));
  useEffect(() => {
    if (invalid) setFocusedLeaf(desktopId, null);
  }, [invalid, desktopId, setFocusedLeaf]);
  const effectiveLeafId = invalid ? null : focusedLeafId;
  const setFocusedLeafId = useCallback(
    (update: SetStateAction<string | null>) =>
      setFocusedLeaf(desktopId, typeof update === 'function' ? update(effectiveLeafId) : update),
    [desktopId, effectiveLeafId, setFocusedLeaf],
  );
  return [effectiveLeafId, setFocusedLeafId] as const;
}
