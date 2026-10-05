import type { LaunchDesktopResultMessage } from '../types/generated';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export function handleLaunchDesktopEvent(event: { event?: string }, pending: PendingRequests): boolean {
  if (event.event === 'background_launch') return true;
  if (event.event !== 'launch_desktop_result') return false;
  const result = event as LaunchDesktopResultMessage;
  settlePendingRequest(
    pending,
    result.action,
    result,
    (reply) => (reply.success ? reply : undefined),
    result.error || 'The launch desktop was not saved',
  );
  return true;
}
