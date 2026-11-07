
import { pendingRequestKey, takeKeyedRequest, type PendingKeyedRequests } from './daemonPendingRequests';

type MarkdownAnnotationEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  status?: unknown;
  generation?: unknown;
  annotations?: unknown;
};

export function handleMarkdownAnnotationDaemonEvent(
  event: MarkdownAnnotationEvent,
  pending: PendingKeyedRequests,
): boolean {
  switch (event.event) {
    case 'markdown_annotations_get_result':
    case 'markdown_annotations_save_result': {
      const op = event.event === 'markdown_annotations_get_result' ? 'get' : 'save';
      const waiter = takeKeyedRequest(pending, pendingRequestKey(op, String(event.request_id)), event.request_id);
      if (!waiter) {
        return true;
      }
      if (event.success) {
        waiter.resolve({
          annotations: Array.isArray(event.annotations) ? event.annotations : [],
          generation: typeof event.generation === 'number' ? event.generation : 0,
        });
      } else {
        waiter.reject(new Error(event.error || `markdown_annotations_${op} failed`));
      }
      return true;
    }

    case 'markdown_annotations_submit_result': {
      const waiter = takeKeyedRequest(pending, pendingRequestKey('submit', String(event.request_id)), event.request_id);
      if (!waiter) {
        return true;
      }
      if (event.success || event.status === 'skipped_pending_approval') {
        // A delivered result may still carry `error` (delivery succeeded, draft
        // update failed) — never re-deliver in that case.
        waiter.resolve({
          status: typeof event.status === 'string' && event.status !== '' ? event.status : 'delivered',
          // The wire omits an empty optional list: a send can leave nothing behind.
          ...(typeof event.generation === 'number'
            ? { draft: { annotations: Array.isArray(event.annotations) ? event.annotations : [], generation: event.generation } }
            : {}),
          ...(typeof event.error === 'string' && event.error !== '' ? { error: event.error } : {}),
        });
      } else {
        waiter.reject(new Error(event.error || 'markdown_annotations_submit failed'));
      }
      return true;
    }

    default:
      return false;
  }
}
