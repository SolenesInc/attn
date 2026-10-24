import type {
  ListAnalyticsRecordsResultMessage,
  SetVentDismissedResultMessage,
  VentsUpdatedMessage,
} from '../types/generated';
import { useProfilesStore } from '../store/profiles';
import { useVentsStore } from '../store/vents';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export interface AnalyticsRecordPage {
  records: { id: number; record: string }[];
  waiting: number;
}

export function handleAnalyticsDaemonEvent(event: { event?: string }, pending: PendingRequests): boolean {
  switch (event.event) {
    case 'vents_updated': {
      const update = event as VentsUpdatedMessage;
      if (update.profile_id === useProfilesStore.getState().selectedProfileId) {
        useVentsStore.getState().setVents(update.vents);
      }
      return true;
    }
    case 'set_vent_dismissed_result':
      settlePendingRequest(pending, 'set_vent_dismissed', event as SetVentDismissedResultMessage, () => true, 'Could not change the vent');
      return true;
    case 'list_analytics_records_result':
      settlePendingRequest(
        pending,
        'list_analytics_records',
        event as ListAnalyticsRecordsResultMessage,
        (result): AnalyticsRecordPage => ({ records: result.records ?? [], waiting: result.waiting }),
        'Could not read the analytics records',
      );
      return true;
    default:
      return false;
  }
}
