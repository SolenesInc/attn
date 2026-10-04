import type { GetCommandUsageResultMessage, RecordCommandUsageResultMessage } from '../types/generated';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export function handleCommandUsageEvent(event: { event?: string }, pending: PendingRequests): boolean {
  if (event.event === 'get_command_usage_result') {
    settlePendingRequest(pending, 'get_command_usage', event as GetCommandUsageResultMessage,
      (reply) => reply.entries, 'Could not read command history');
    return true;
  }
  if (event.event === 'record_command_usage_result') {
    settlePendingRequest(pending, 'record_command_usage', event as RecordCommandUsageResultMessage,
      () => true, 'Could not save command history');
    return true;
  }
  return false;
}
