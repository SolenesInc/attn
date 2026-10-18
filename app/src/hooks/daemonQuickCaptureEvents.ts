import type { QuickCaptureResultMessage } from '../types/generated';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export class QuickCaptureRequestError extends Error {
  constructor(message: string, readonly code?: string) { super(message); }
}

export const QUICK_CAPTURE_COMMANDS = [
  'quick_capture_send', 'quick_capture_get', 'quick_capture_list',
  'quick_capture_attachment_put', 'quick_capture_attachment_get', 'quick_capture_attachment_discard',
] as const;

export function handleQuickCaptureDaemonEvent(
  event: { event?: string; request_id?: unknown; success?: boolean; error?: string; error_code?: string; result?: QuickCaptureResultMessage['result']; profile_id?: string; capture_id?: string; read_at?: string }, pending: PendingRequests, onRead: (receipt: { profileId: string; captureId: string; readAt: string }) => void,
): boolean {
  if (event.event === 'quick_capture_read') { onRead({ profileId: event.profile_id!, captureId: event.capture_id!, readAt: event.read_at! }); return true; }
  if (event.event !== 'quick_capture_result') return false;
  for (const command of QUICK_CAPTURE_COMMANDS) {
    if (settlePendingRequest(pending, command, event, message => message.result, 'Quick capture request failed', new QuickCaptureRequestError(event.error || 'Quick capture request failed', event.error_code))) break;
  }
  return true;
}
