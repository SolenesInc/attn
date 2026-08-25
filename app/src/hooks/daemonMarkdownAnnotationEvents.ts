
import { takeKeyedRequest, type PendingKeyedRequests } from './daemonPendingRequests';

// `<op>:<uri>` — one in-flight request per document per operation.
export function markdownAnnotationKey(op: string, documentUri: string): string {
  return `${op}:${documentUri}`;
}

type MarkdownAnnotationEvent = {
  event: string;
  request_id?: unknown;
  document_uri?: unknown;
  success?: boolean;
  error?: string;
  stale?: unknown;
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
    case 'markdown_annotations_save_result':
    case 'markdown_annotations_clear_result': {
      const op =
        event.event === 'markdown_annotations_get_result'
          ? 'get'
          : event.event === 'markdown_annotations_save_result'
            ? 'save'
            : 'clear';
      const key = markdownAnnotationKey(op, String(event.document_uri ?? ''));
      const waiter = takeKeyedRequest(pending, key, event.request_id);
      if (!waiter) {
        return true;
      }
      if (op === 'save' && !event.success && event.stale) {
        waiter.resolve({ stale: true });
      } else if (!event.success) {
        waiter.reject(new Error(event.error || `markdown_annotations_${op} failed`));
      } else if (op === 'get') {
        waiter.resolve({
          annotations: Array.isArray(event.annotations) ? event.annotations : [],
          generation: typeof event.generation === 'number' ? event.generation : 0,
        });
      } else if (op === 'save') {
        waiter.resolve({ stale: false });
      } else {
        waiter.resolve({
          generation: typeof event.generation === 'number' ? event.generation : 0,
        });
      }
      return true;
    }

    case 'markdown_annotations_submit_result': {
      const key = markdownAnnotationKey('submit', String(event.document_uri ?? ''));
      const waiter = takeKeyedRequest(pending, key, event.request_id);
      if (!waiter) {
        return true;
      }
      if (event.success || event.status === 'skipped_pending_approval') {
        // A delivered result may still carry `error` (delivery succeeded, draft
        // clear failed) — never re-deliver in that case.
        waiter.resolve({
          status: typeof event.status === 'string' && event.status !== '' ? event.status : 'delivered',
          ...(typeof event.generation === 'number' ? { generation: event.generation } : {}),
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
