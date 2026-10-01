import type { KeptConversationListResult } from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

interface ConversationDaemonEvent {
  event?: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  kept_conversation_list_result?: KeptConversationListResult;
}

export function handleConversationDaemonEvent(
  event: ConversationDaemonEvent,
  pending: PendingRequests,
  onChanged: () => void,
): boolean {
  switch (event.event) {
    case 'kept_conversation_list_result':
      settlePendingRequest(pending, 'kept_conversation_list', event,
        (result) => result.kept_conversation_list_result, 'Listing conversations failed');
      return true;
    case 'kept_conversation_keep_result':
    case 'kept_conversation_forget_result':
      settlePendingRequest(pending, event.event.slice(0, -7), event,
        (result) => result.success ? true : undefined, 'Changing the kept conversation failed');
      return true;
    case 'kept_conversations_changed':
      onChanged();
      return true;
    default:
      return false;
  }
}
