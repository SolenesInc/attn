import type {
  SetupActionResultMessage,
  SetupArrangementChangedMessage,
  SetupErrorCode,
  SetupsChangedMessage,
} from '../types/generated';
import { useSetupsStore } from '../store/setups';
import { pendingRequestKey, type PendingRequests } from './daemonPendingRequests';

export type SetupActionResult = SetupActionResultMessage;

export class SetupCommandError extends Error {
  readonly code: SetupErrorCode | undefined;
  readonly action: string;

  constructor(result: SetupActionResult) {
    super(result.error || `${result.action} failed`);
    this.name = 'SetupCommandError';
    this.code = result.error_code;
    this.action = result.action;
  }
}

type SetupEvent =
  | ({ event: 'setup_action_result' } & SetupActionResult)
  | ({ event: 'setups_changed' } & SetupsChangedMessage)
  | ({ event: 'setup_arrangement_changed' } & SetupArrangementChangedMessage)
  | { event?: string };

function settleSetupAction(pending: PendingRequests, result: SetupActionResult): void {
  if (result.success && result.action === 'setup_select' && result.setup) {
    useSetupsStore.getState().selectedSetupChanged(result.setup, result.desktops ?? []);
  }
  const key = pendingRequestKey(result.action, result.request_id);
  const waiter = pending.get(key);
  if (!waiter) return;
  pending.delete(key);
  if (result.success) {
    waiter.resolve(result);
  } else {
    waiter.reject(new SetupCommandError(result));
  }
}

export function handleSetupDaemonEvent(data: SetupEvent, pending: PendingRequests): boolean {
  switch (data.event) {
    case 'setup_action_result':
      settleSetupAction(pending, data as SetupActionResult);
      return true;
    case 'setups_changed':
      useSetupsStore.getState().setupsChanged((data as SetupsChangedMessage).setups ?? []);
      return true;
    case 'setup_arrangement_changed': {
      const message = data as SetupArrangementChangedMessage;
      useSetupsStore.getState().arrangementChanged(message.setup, message.desktops ?? [], message.deleted_desktop_ids);
      return true;
    }
    default:
      return false;
  }
}
