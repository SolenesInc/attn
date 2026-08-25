import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

interface AppDaemonEvent {
  event?: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  error_code?: unknown;
  reconcile?: unknown;
  payload?: unknown;
}

export class AppCommandError extends Error {
  readonly code: string;
  readonly reconcile: unknown;

  constructor(message: string, code: string, reconcile: unknown) {
    super(message);
    this.name = 'AppCommandError';
    this.code = code;
    this.reconcile = reconcile;
  }
}

export interface AppCommandResult {
  value: unknown;
}

export function handleAppDaemonEvent(event: AppDaemonEvent, pending: PendingRequests): boolean {
  if (event.event !== 'app_command_result') return false;
  if (event.success === false && typeof event.error_code === 'string' && event.error_code !== '') {
    settlePendingRequest(
      pending,
      'app_command',
      { ...event, success: false },
      () => undefined,
      'The command was refused',
      new AppCommandError(
        event.error || 'The command was refused',
        event.error_code,
        event.reconcile,
      ),
    );
    return true;
  }
  settlePendingRequest(
    pending,
    'app_command',
    event,
    (settled): AppCommandResult | undefined => {
      if (typeof settled.payload !== 'string') return { value: undefined };
      try {
        return { value: JSON.parse(settled.payload) };
      } catch {
        return undefined;
      }
    },
    'The command answered with something that is not JSON',
  );
  return true;
}
