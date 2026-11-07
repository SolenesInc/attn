import { isDomInstance } from '../utils/realm';
export type FocusedQueueRow = { kind: 'none' } | { kind: 'session'; sessionId: string; row: HTMLElement } | { kind: 'other' };

export function focusedQueueRow(): FocusedQueueRow {
  const active = document.activeElement;
  const row = isDomInstance(active, 'HTMLElement') ? active.closest<HTMLElement>('.queue-sidebar-body .session-item') : null;
  if (!row) return { kind: 'none' };
  const { sessionId } = row.dataset;
  return sessionId ? { kind: 'session', sessionId, row } : { kind: 'other' };
}
