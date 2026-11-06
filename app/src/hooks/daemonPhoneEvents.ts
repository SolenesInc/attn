import type { PhonePairBeginResultMessage, PhonePairCancelResultMessage, PhonePairingRevokeResultMessage, PhonesUpdatedMessage } from '../types/generated';
import { usePhonesStore } from '../store/phones';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export function handlePhoneDaemonEvent(event: { event?: string }, pending: PendingRequests): boolean {
  switch (event.event) {
    case 'phones_updated':
      usePhonesStore.getState().received(event as PhonesUpdatedMessage);
      return true;
    case 'phone_pair_begin_result': {
      const result = event as PhonePairBeginResultMessage;
      settlePendingRequest(pending, 'phone_pair_begin', result, (r) => r.offer, 'Showing a pairing code failed');
      return true;
    }
    case 'phone_pair_cancel_result':
      settlePendingRequest(pending, 'phone_pair_cancel', event as PhonePairCancelResultMessage, () => true, 'Cancelling the pairing code failed');
      return true;
    case 'phone_pairing_revoke_result':
      settlePendingRequest(pending, 'phone_pairing_revoke', event as PhonePairingRevokeResultMessage, () => true, 'Revoking the phone failed');
      return true;
    default:
      return false;
  }
}
