import { flushSync } from 'react-dom';
import { isFixedAnchor, onScreenCalloutAnchors } from '../callouts/calloutAnchors';
import { useCalloutStore } from '../callouts/useCalloutStore';
import { CalloutResultMessageCmd, type CalloutAnchorsRequestMessage, type CalloutResultMessage, type CalloutShowRequestMessage } from '../types/generated';

export function handleCalloutDaemonEvent(event: { event?: string }, send: (result: CalloutResultMessage) => void): boolean {
  if (event.event !== 'callout_show_request' && event.event !== 'callout_anchors_request') return false;
  const request = event as CalloutShowRequestMessage | CalloutAnchorsRequestMessage;
  const onScreen = onScreenCalloutAnchors();
  const result: CalloutResultMessage = {
    cmd: CalloutResultMessageCmd.CalloutResult,
    request_id: request.request_id,
    on_screen: onScreen,
  };
  if (event.event === 'callout_show_request') {
    const show = request as CalloutShowRequestMessage;
    if (useCalloutStore.getState().overlayOpen) {
      send({ ...result, on_screen: [], missing: [...new Set(show.steps.map((step) => step.anchor))] });
      return true;
    }
    const missing = [...new Set(show.steps.map((step) => step.anchor)
      .filter(isFixedAnchor).filter((anchor) => !onScreen.includes(anchor)))];
    if (missing.length) result.missing = missing;
    else flushSync(() => useCalloutStore.getState().show(show.request_id, show.from, show.steps));
  }
  send(result);
  return true;
}
