import type { SenderPairDecideResultMessage, SenderUnpairResultMessage, SendersUpdatedMessage } from '../types/generated';
import { useSendersStore } from '../store/senders';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export function handleSenderDaemonEvent(event: { event?: string }, pending: PendingRequests): boolean {
  switch (event.event) {
    case 'senders_updated':
      useSendersStore.getState().received(event as SendersUpdatedMessage);
      return true;
    case 'sender_pair_decide_result':
      settlePendingRequest(pending, 'sender_pair_decide', event as SenderPairDecideResultMessage, () => true, 'Deciding the pairing failed');
      return true;
    case 'sender_unpair_result':
      settlePendingRequest(pending, 'sender_unpair', event as SenderUnpairResultMessage, () => true, 'Removing the sender failed');
      return true;
    default:
      return false;
  }
}
