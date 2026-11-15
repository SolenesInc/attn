import type { ReviewBundle, ReviewFlow, ReviewFlowMessage, ReviewStop } from '../../types/generated';

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

/** An arrow the Brief's cursor stands on: its cursor key and the stop it opens. */
export interface ArrowTarget {
  key: string;
  stop: ReviewStop;
}

/** The arrows of a drawn diagram that take the Brief's cursor, by message
 * line. A key names the arrow by its side, its diagram's place among that
 * side's diagrams, the stop it shows, what it says, and how many arrows like
 * it come before it, so an edit elsewhere in the document keeps it. */
export function arrowTargets(bundle: ReviewBundle, flow: ReviewFlow): Map<number, ArrowTarget> {
  const diagram = bundle.flows.filter((candidate) => candidate.drawn && candidate.side === flow.side).indexOf(flow);
  const seen = new Map<string, number>();
  const targets = new Map<number, ArrowTarget>();
  for (const message of flow.messages) {
    const stop = bundle.stops.find((candidate) => candidate.heading_line === message.stop_line);
    if (!stop) continue;
    const base = `message:${flow.side ?? ''}:${diagram}:${stop.path}:${stop.from}:${message.text}`;
    const before = seen.get(base) ?? 0;
    seen.set(base, before + 1);
    targets.set(message.line, { key: `${base}:${before}`, stop });
  }
  return targets;
}

/** Where the Map stands, held by what the message says and the stop it
 * shows, so an edit of the diagram or the document above it keeps it. */
export interface MapPlace {
  text: string;
  path?: string;
  from?: number;
  index: number;
}

export function mapPlaceOf(bundle: ReviewBundle, list: readonly MapStop[], index: number): MapPlace {
  const at = list[index];
  const stop = at ? bundle.stops.find((candidate) => candidate.heading_line === at.stopLine) : undefined;
  return { text: at?.message.text ?? '', path: stop?.path, from: stop?.from, index };
}

export function mapIndexOf(bundle: ReviewBundle, list: readonly MapStop[], place: MapPlace): number {
  const showsStop = (at: MapStop) => {
    const stop = bundle.stops.find((candidate) => candidate.heading_line === at.stopLine);
    return stop !== undefined && stop.path === place.path && stop.from === place.from;
  };
  const nearest = (matches: (at: MapStop) => boolean) => list
    .map((at, index) => (matches(at) ? index : -1))
    .filter((index) => index >= 0)
    .sort((a, b) => Math.abs(a - place.index) - Math.abs(b - place.index))[0];
  return nearest((at) => at.message.text === place.text && showsStop(at))
    ?? nearest(showsStop)
    ?? nearest((at) => at.message.text === place.text)
    ?? Math.min(place.index, Math.max(0, list.length - 1));
}
