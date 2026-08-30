import type {
  SeedArtifactTargetResult,
  SeedArtifactTransferResult,
} from '../types/generated';
import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';

type SeedArtifactEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  result?: SeedArtifactTargetResult | SeedArtifactTransferResult;
};

export function handleSeedArtifactDaemonEvent(
  event: SeedArtifactEvent,
  pending: PendingRequests,
): boolean {
  switch (event.event) {
    case 'seed_artifact_target_result':
      settlePendingRequest(
        pending,
        'seed_artifact_target',
        event,
        (value) => value.result as SeedArtifactTargetResult | undefined,
        'Seed artifact target resolution failed',
      );
      return true;
    case 'seed_artifact_transfer_result':
      settlePendingRequest(
        pending,
        'seed_artifact_transfer',
        event,
        (value) => value.result as SeedArtifactTransferResult | undefined,
        'Seed artifact transfer failed',
      );
      return true;
    default:
      return false;
  }
}
