import type { ReviewBundle, ReviewFlow, ReviewFlowMessage } from '../../types/generated';

export type Frame = 'brief' | 'walkthrough' | 'map';
export type Side = 'before' | 'after';

export const FRAME_KEYS: ReadonlyArray<[string, Frame, string]> = [
  ['1', 'brief', 'Brief'],
  ['2', 'walkthrough', 'Walkthrough'],
  ['3', 'map', 'Map'],
];

/** A flow message the cursor can stand on: one tied to a stop. */
export interface MapStop {
  flow: ReviewFlow;
  message: ReviewFlowMessage;
  stopLine: number;
}

function drawnFlows(bundle: ReviewBundle): ReviewFlow[] {
  return bundle.flows.filter((flow) => flow.drawn);
}

function hasStop(flow: ReviewFlow): boolean {
  return flow.messages.some((message) => message.stop_line !== undefined);
}

/** The frames a document can fill: Walkthrough needs a stop, Map a drawn
 * sequence diagram with a message tied to a stop. */
export function offeredFrames(bundle: ReviewBundle): Frame[] {
  return [
    'brief',
    ...(bundle.stops.length > 0 ? ['walkthrough' as const] : []),
    ...(drawnFlows(bundle).some(hasStop) ? ['map' as const] : []),
  ];
}

export function openingFrame(bundle: ReviewBundle): Frame {
  const offered = offeredFrames(bundle);
  return offered.find((frame) => frame === bundle.frame) ?? 'brief';
}

/** Whether the Map has both a before and an after diagram, so b switches. */
export function hasSides(bundle: ReviewBundle): boolean {
  const flows = drawnFlows(bundle);
  return flows.some((flow) => flow.side === 'before') && flows.some((flow) => flow.side === 'after');
}

/** The diagrams the Map shows for a side: every drawn diagram, except the
 * other side's when the document has both. */
export function mapFlows(bundle: ReviewBundle, side: Side): ReviewFlow[] {
  const flows = drawnFlows(bundle);
  if (!hasSides(bundle)) return flows;
  return flows.filter((flow) => flow.side === undefined || flow.side === side);
}

export function mapStops(flows: readonly ReviewFlow[]): MapStop[] {
  return flows.flatMap((flow) => flow.messages.flatMap((message) => (
    message.stop_line === undefined ? [] : [{ flow, message, stopLine: message.stop_line }]
  )));
}
