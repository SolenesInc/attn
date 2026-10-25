import type {
  InstallCodexUpdateResultMessage,
  RestartSharedCodexResultMessage,
  SharedCodexChangedMessage,
} from '../types/generated';
import { useSharedCodexStore } from '../store/sharedCodex';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export const INSTALL_CODEX_UPDATE = 'install_codex_update';

export class CodexInstallUntracked extends Error {
  constructor() {
    super('The daemon disconnected before the Codex install finished');
    this.name = 'CodexInstallUntracked';
  }
}

export function handleSharedCodexDaemonEvent(event: { event?: string }, pending: PendingRequests): boolean {
  switch (event.event) {
    case 'shared_codex_changed': {
      const changed = event as SharedCodexChangedMessage;
      useSharedCodexStore.getState().received(changed.profile_id, changed.shared_codex ?? null);
      return true;
    }
    case 'restart_shared_codex_result':
      settlePendingRequest(pending, 'restart_shared_codex', event as RestartSharedCodexResultMessage, () => true, 'Restarting shared Codex failed');
      return true;
    case 'install_codex_update_result': {
      const result = event as InstallCodexUpdateResultMessage;
      useSharedCodexStore.getState().received(result.profile_id, result.shared_codex ?? null);
      settlePendingRequest(pending, INSTALL_CODEX_UPDATE, result, () => true, 'Installing the Codex update failed');
      return true;
    }
    default:
      return false;
  }
}
