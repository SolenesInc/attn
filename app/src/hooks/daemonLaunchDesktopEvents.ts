import type { BackgroundLaunchMessage, LaunchDesktopResultMessage } from '../types/generated';
import { useToastStore } from '../store/toasts';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export function handleLaunchDesktopEvent(event: { event?: string }, pending: PendingRequests): boolean {
  if (event.event === 'background_launch') {
    const arrival = event as BackgroundLaunchMessage;
    useToastStore.getState().append({
      id: arrival.session_id,
      message: arrival.name,
      source: arrival.requested_by,
      tone: 'notice',
      sessionId: arrival.session_id,
      launchKind: arrival.kind,
      desktopLabel: arrival.desktop_label,
    });
    return true;
  }
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
