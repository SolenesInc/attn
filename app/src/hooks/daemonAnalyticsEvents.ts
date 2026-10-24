import type {
  ClearAnalyticsRecordsResultMessage,
  GetAnalyticsStatusResultMessage,
  ListAnalyticsRecordsResultMessage,
  PlantVentSeedResultMessage,
  SetVentDismissedResultMessage,
  UnplantVentSeedResultMessage,
  VentsUpdatedMessage,
} from '../types/generated';
import { useProfilesStore } from '../store/profiles';
import { useVentsStore } from '../store/vents';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export interface AnalyticsRecordPage {
  records: { id: number; record: string }[];
  waiting: number;
}

export interface AnalyticsCleared {
  records: number;
  vents: number;
}

export interface AnalyticsStatus {
  records: number;
  bytes: number;
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
    case 'plant_vent_seed_result':
      settlePendingRequest(pending, 'plant_vent_seed', event as PlantVentSeedResultMessage, (result) => result.seed, 'Could not plant a seed from the vent');
      return true;
    case 'unplant_vent_seed_result':
      settlePendingRequest(pending, 'unplant_vent_seed', event as UnplantVentSeedResultMessage, () => true, 'Could not undo the plant');
      return true;
    case 'get_analytics_status_result':
      settlePendingRequest(
        pending,
        'get_analytics_status',
        event as GetAnalyticsStatusResultMessage,
        ({ records, bytes, waiting }): AnalyticsStatus => ({ records, bytes, waiting }),
        'Could not read the analytics status',
      );
      return true;
    case 'clear_analytics_records_result':
      settlePendingRequest(
        pending,
        'clear_analytics_records',
        event as ClearAnalyticsRecordsResultMessage,
        ({ records, vents }): AnalyticsCleared => ({ records, vents }),
        'Could not clear the analytics records',
      );
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
