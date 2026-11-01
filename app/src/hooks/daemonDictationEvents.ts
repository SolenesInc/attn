import type { DictationRefineResultMessage } from '../types/generated';
import { pendingRequestKey, type PendingRequests } from './daemonPendingRequests';

export const DICTATION_REFINE = 'dictation_refine';

export function dictationRefineKey(requestId: string) {
  return pendingRequestKey(DICTATION_REFINE, requestId);
}

export function handleDictationDaemonEvent(event: { event?: string; request_id?: unknown }, pending: PendingRequests): boolean {
  if (event.event !== 'dictation_refine_result') return false;
  if (typeof event.request_id !== 'string') return true;
  const key = dictationRefineKey(event.request_id);
  const waiter = pending.get(key);
  pending.delete(key);
  waiter?.resolve(event as DictationRefineResultMessage);
  return true;
}
