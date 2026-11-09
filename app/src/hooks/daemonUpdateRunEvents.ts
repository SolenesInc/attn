import type { DeclineUpdateResultMessage, StartUpdateResultMessage, UpdateChangedMessage, UpdateRun } from '../types/generated';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export const START_UPDATE = 'start_update';
export const DECLINE_UPDATE = 'decline_update';

export function handleUpdateRunDaemonEvent(
  event: { event?: string },
  pending: PendingRequests,
  setUpdateRun: (run: UpdateRun) => void,
): boolean {
  switch (event.event) {
    case 'update_changed':
      setUpdateRun((event as UpdateChangedMessage).run);
      return true;
    case 'start_update_result': {
      const result = event as StartUpdateResultMessage;
      if (result.run) setUpdateRun(result.run);
      settlePendingRequest(pending, START_UPDATE, result, () => true, 'Starting the update failed');
      return true;
    }
    case 'decline_update_result':
      settlePendingRequest(pending, DECLINE_UPDATE, event as DeclineUpdateResultMessage, () => true, 'Declining the update failed');
      return true;
    default:
      return false;
  }
}
