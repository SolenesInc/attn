/** Notebook daemon events: the `notebook_backlinks_result` answer. Kept out
 * of `useDaemonSocket.ts` so grepping a `notebook_` wire name lands in a notebook file. */

import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';

/** The event shapes this module reads, loosely typed off the wire union. */
type NotebookEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  entries?: unknown;
};

export function handleNotebookDaemonEvent(event: NotebookEvent, pending: PendingRequests): boolean {
  switch (event.event) {
    case 'notebook_backlinks_result':
      settlePendingRequest(
        pending,
        'notebook_backlinks',
        event,
        (e) => e.entries || [],
        'Notebook request failed',
      );
      return true;

    default:
      return false;
  }
}
