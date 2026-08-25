
import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';
import { labelByEmoji } from '../annotations/quickLabels';

type SessionAnnotationEvent = {
  event: string;
  session_id?: unknown;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  stale?: unknown;
  status?: unknown;
  detail?: unknown;
  messages?: unknown;
  truncated?: unknown;
  annotations?: unknown;
  note?: unknown;
  generation?: unknown;
};

export interface DaemonSessionMessage {
  key: string;
  markdown: string;
}

export type SessionMessageWindowStatus = 'discovering' | 'ready' | 'unavailable';

function toMessageWindowStatus(raw: unknown): SessionMessageWindowStatus | undefined {
  switch (raw) {
    case 'discovering':
    case 'ready':
    case 'unavailable':
      return raw;
    default:
      return undefined;
  }
}

export interface DaemonSessionAnnotation {
  id: string;
  messageKey: string;
  start: number;
  end: number;
  quote: string;
  quickLabelId: string;
  comment: string;
}

function toMessages(raw: unknown): DaemonSessionMessage[] {
  if (!Array.isArray(raw)) return [];
  return raw.map((entry) => ({
    key: String((entry as { key?: unknown })?.key ?? ''),
    markdown: String((entry as { markdown?: unknown })?.markdown ?? ''),
  })).filter((message) => message.key !== '' && message.markdown !== '');
}

function toAnnotations(raw: unknown): DaemonSessionAnnotation[] {
  if (!Array.isArray(raw)) return [];
  return raw.map((entry) => {
    const record = entry as Record<string, unknown>;
    return {
      id: String(record?.id ?? ''),
      messageKey: String(record?.message_key ?? ''),
      start: Number(record?.start ?? 0),
      end: Number(record?.end ?? 0),
      quote: String(record?.quote ?? ''),
      quickLabelId: typeof record?.quick_label_id === 'string'
        ? record.quick_label_id
        : labelByEmoji(String(record?.emoji ?? ''))?.id ?? '',
      comment: String(record?.comment ?? ''),
    };
  }).filter((annotation) => annotation.id !== '' && annotation.messageKey !== '');
}

export function annotationToWire(annotation: DaemonSessionAnnotation): Record<string, unknown> {
  return {
    id: annotation.id,
    message_key: annotation.messageKey,
    start: annotation.start,
    end: annotation.end,
    quote: annotation.quote,
    quick_label_id: annotation.quickLabelId,
    comment: annotation.comment,
  };
}

export function handleSessionAnnotationDaemonEvent(
  event: SessionAnnotationEvent,
  pending: PendingRequests,
  onMessagesChanged: (sessionId: string) => void,
): boolean {
  switch (event.event) {
    case 'session_messages_changed': {
      const sessionId = typeof event.session_id === 'string' ? event.session_id.trim() : '';
      if (sessionId !== '') onMessagesChanged(sessionId);
      return true;
    }

    case 'session_messages_get_result':
      settlePendingRequest(
        pending,
        'session_messages_get',
        event,
        (e) => {
          const status = toMessageWindowStatus(e.status);
          if (!status) return undefined;
          return {
            messages: toMessages(e.messages),
            status,
            detail: typeof e.detail === 'string' ? e.detail : undefined,
            truncated: e.truncated === true,
          };
        },
        'Session message fetch failed',
      );
      return true;

    case 'session_annotations_get_result':
      settlePendingRequest(
        pending,
        'session_annotations_get',
        event,
        (e) => ({
          annotations: toAnnotations(e.annotations),
          note: typeof e.note === 'string' ? e.note : '',
          generation: typeof e.generation === 'number' ? e.generation : 0,
        }),
        'Session annotation fetch failed',
      );
      return true;

    case 'session_annotations_save_result': {
      if (!event.success && event.stale === true) {
        settlePendingRequest(
          pending,
          'session_annotations_save',
          { ...event, success: true },
          () => ({ stale: true }),
          'Session annotation save failed',
        );
        return true;
      }
      settlePendingRequest(
        pending,
        'session_annotations_save',
        event,
        () => ({ stale: false }),
        'Session annotation save failed',
      );
      return true;
    }

    case 'session_annotations_clear_result':
      settlePendingRequest(
        pending,
        'session_annotations_clear',
        event,
        (e) => ({ generation: typeof e.generation === 'number' ? e.generation : 0 }),
        'Session annotation clear failed',
      );
      return true;

    case 'session_annotations_submit_result': {
      const skipped = !event.success && event.status === 'skipped_pending_approval';
      settlePendingRequest(
        pending,
        'session_annotations_submit',
        skipped ? { ...event, success: true } : event,
        (e) => ({ status: String(e.status ?? 'error') }),
        'Session annotation send failed',
      );
      return true;
    }

    default:
      return false;
  }
}
