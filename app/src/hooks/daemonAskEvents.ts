import type { AskAnswerResultMessage, AskImageResultMessage, AsksUpdatedMessage } from '../types/generated';
import { useAsksStore } from '../store/asks';
import { useProfilesStore } from '../store/profiles';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export function handleAskDaemonEvent(event: { event?: string }, pending: PendingRequests): boolean {
  if (event.event === 'asks_updated') {
    const update = event as AsksUpdatedMessage;
    if (update.profile_id === useProfilesStore.getState().selectedProfileId) {
      useAsksStore.getState().setAsks(update.asks);
    }
    return true;
  }
  if (event.event === 'ask_answer_result') {
    settlePendingRequest(pending, 'ask_answer', event as AskAnswerResultMessage, () => true, 'Could not send the answer');
    return true;
  }
  if (event.event === 'ask_image_result') {
    settlePendingRequest(pending, 'ask_image', event as AskImageResultMessage, (result) => result, 'Could not load the image');
    return true;
  }
  return false;
}
