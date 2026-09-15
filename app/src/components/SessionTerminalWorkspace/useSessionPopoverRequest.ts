import { useCallback, useState } from 'react';

export function useSessionPopoverRequest(request?: { sessionId: string; nonce: number }) {
  const [dismissed, setDismissed] = useState<typeof request>();
  const dismiss = useCallback(() => setDismissed(request), [request]);
  return [request && request !== dismissed ? request.sessionId : null, dismiss] as const;
}
