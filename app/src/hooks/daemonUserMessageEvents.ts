import type { UserMessageResultMessage } from '../types/generated';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export class UserMessageRequestError extends Error {
  constructor(message: string, readonly code?: string) { super(message); }
}

export const USER_MESSAGE_COMMANDS = [
  'user_message_send', 'user_message_get', 'user_message_list',
  'user_message_attachment_put', 'user_message_attachment_get', 'user_message_attachment_discard',
] as const;

export function handleUserMessageDaemonEvent(
  event: { event?: string; request_id?: unknown; success?: boolean; error?: string; error_code?: string; result?: UserMessageResultMessage['result'] }, pending: PendingRequests, onChanged: () => void,
): boolean {
  if (event.event === 'user_message_changed') { onChanged(); return true; }
  if (event.event !== 'user_message_result') return false;
  for (const command of USER_MESSAGE_COMMANDS) {
    if (settlePendingRequest(pending, command, event, message => message.result, 'User message request failed', new UserMessageRequestError(event.error || 'User message request failed', event.error_code))) break;
  }
  return true;
}
