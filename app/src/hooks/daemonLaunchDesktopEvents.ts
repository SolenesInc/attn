import type { BackgroundLaunchMessage, LaunchDesktopResultMessage } from '../types/generated';
import { useToastStore } from '../store/toasts';
import { useLaunchDesktopStore } from '../store/launchDesktops';
import { useSessionStore } from '../store/sessions';
import type { SessionShowRequestedMessage } from '../types/generated';
import { settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export function handleLaunchDesktopEvent(event: { event?: string }, pending: PendingRequests): boolean {
  if (event.event === 'session_show_requested') {
    useSessionStore.getState().selectAgent((event as SessionShowRequestedMessage).session_id);
    return true;
  }
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
  if (result.success && result.item && result.items) {
    useLaunchDesktopStore.getState().receiveItems(result.items, result.item.profile_id);
  }
  if (result.success && !result.item && result.action === 'launch_desktop_get') {
    useLaunchDesktopStore.getState().receiveCatalog(result.items ?? []);
  }
  settlePendingRequest(
    pending,
    result.action,
    result,
    (reply) => (reply.success ? reply : undefined),
    result.error || 'The launch desktop was not saved',
  );
  return true;
}
