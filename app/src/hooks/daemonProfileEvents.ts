import type {
  MigrationChangedMessage,
  ProfileActionResultMessage,
  ProfileArrangementChangedMessage,
  ProfileErrorCode,
  ProfilesChangedMessage,
} from '../types/generated';
import { useProfilesStore } from '../store/profiles';
import { pendingRequestKey, type PendingRequests } from './daemonPendingRequests';

export type ProfileActionResult = ProfileActionResultMessage;

export class ProfileCommandError extends Error {
  readonly code: ProfileErrorCode | undefined;
  readonly action: string;

  constructor(result: ProfileActionResult) {
    super(result.error || `${result.action} failed`);
    this.name = 'ProfileCommandError';
    this.code = result.error_code;
    this.action = result.action;
  }
}

type ProfileEvent =
  | ({ event: 'profile_action_result' } & ProfileActionResult)
  | ({ event: 'profiles_changed' } & ProfilesChangedMessage)
  | ({ event: 'profile_arrangement_changed' } & ProfileArrangementChangedMessage)
  | ({ event: 'migration_changed' } & MigrationChangedMessage)
  | { event?: string };

function settleProfileAction(pending: PendingRequests, result: ProfileActionResult): void {
  const key = pendingRequestKey(result.action, result.request_id);
  const waiter = pending.get(key);
  if (!waiter) return;
  pending.delete(key);
  if (result.success) {
    waiter.resolve(result);
  } else {
    waiter.reject(new ProfileCommandError(result));
  }
}

export function handleProfileDaemonEvent(data: ProfileEvent, pending: PendingRequests): boolean {
  switch (data.event) {
    case 'profile_action_result':
      settleProfileAction(pending, data as ProfileActionResult);
      return true;
    case 'profiles_changed':
      useProfilesStore.getState().profilesChanged((data as ProfilesChangedMessage).profiles ?? []);
      return true;
    case 'profile_arrangement_changed': {
      const message = data as ProfileArrangementChangedMessage;
      useProfilesStore.getState().arrangementArrived(message.profile, message.desktops ?? []);
      return true;
    }
    case 'migration_changed':
      useProfilesStore.getState().migrationPhaseChanged((data as MigrationChangedMessage).state.phase ?? null);
      return true;
    default:
      return false;
  }
}
