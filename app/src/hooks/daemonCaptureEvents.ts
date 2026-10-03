import type { CaptureResultMessage } from '../types/generated';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export class CaptureRequestError extends Error {
  constructor(message: string, readonly code?: string) { super(message); }
}

export const CAPTURE_COMMANDS = [
  'capture_send', 'capture_get', 'capture_list',
  'capture_attachment_put', 'capture_attachment_get', 'capture_attachment_discard',
] as const;

export function handleCaptureDaemonEvent(
  event: { event?: string; request_id?: unknown; success?: boolean; error?: string; error_code?: string; result?: CaptureResultMessage['result'] }, pending: PendingRequests, onChanged: () => void,
): boolean {
  if (event.event === 'capture_changed') { onChanged(); return true; }
  if (event.event !== 'capture_result') return false;
  for (const command of CAPTURE_COMMANDS) {
    if (settlePendingRequest(pending, command, event, message => message.result, 'Capture request failed', new CaptureRequestError(event.error || 'Capture request failed', event.error_code))) break;
  }
  return true;
}
