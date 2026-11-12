import { useLayoutEffect, useRef } from 'react';
import type { ReviewBundle, ReviewFlow, ReviewFlowMessage } from '../../types/generated';
import type { MapStop, Side } from './frames';
import { ReviewNotShown } from './ReviewLists';
import { SequenceDiagram, smallestDiagramWidth } from './SequenceDiagram';

const DIAGRAM_SHARE = 0.44;
const DIAGRAM_PADDING = 25;

/** Whether the Map's diagram column, beside the step, holds the widest of the
 * document's diagrams at its smallest scale. */
export function mapFitsBeside(bundle: ReviewBundle, frameWidth: number): boolean {
  const widest = Math.max(0, ...bundle.flows.filter((flow) => flow.drawn).map(smallestDiagramWidth));
  return frameWidth * DIAGRAM_SHARE - DIAGRAM_PADDING >= widest;
}

export const MAP_COLUMNS = `minmax(0, ${DIAGRAM_SHARE * 100}%) minmax(0, 1fr)`;

function participantLabel(flow: ReviewFlow, id: string): string {
  const label = flow.participants.find((participant) => participant.id === id)?.label ?? id;
  return label.split(/<br\s*\/?>/i)[0].trim();
}

/** The Map's diagrams, the Not shown list under them, and the line that says
 * which arrow the document beside them is narrowed to. */
export function MapFrame({
  bundle,
  flows,
  stops,
  at,
  side,
  sides,
  onPick,
  onSide,
}: {
  bundle: ReviewBundle;
  flows: readonly ReviewFlow[];
  stops: readonly MapStop[];
  at: number;
  side: Side;
  sides: boolean;
  onPick: (message: ReviewFlowMessage) => void;
  onSide: (side: Side) => void;
}) {
  const current = stops[at];
  const diagramsRef = useRef<HTMLElement | null>(null);
  const currentLine = current?.message.line;
  useLayoutEffect(() => {
    diagramsRef.current?.querySelector('[aria-current="true"][data-review-message]')?.scrollIntoView?.({ block: 'nearest' });
  }, [currentLine, side]);
  return (
    <div className="review-map">
      <section className="review-map-diagrams" ref={diagramsRef} aria-label="Map">
        {sides ? (
          <div className="review-map-sides">
            {(['before', 'after'] as const).map((value) => (
              <button key={value} type="button" tabIndex={-1} aria-pressed={side === value} onClick={() => onSide(value)}>
                {value === 'before' ? 'Before' : 'After'}
              </button>
            ))}
            <kbd>b</kbd>
          </div>
        ) : null}
        {flows.map((flow) => (
          <SequenceDiagram key={flow.line} flow={flow} current={current?.flow === flow ? current.message.line : null} onPick={onPick} />
        ))}
        {bundle.not_shown.length > 0 ? (
          <section className="review-map-not-shown" aria-label="Not shown">
            <h3>Not shown</h3>
            <ReviewNotShown bundle={bundle} />
          </section>
        ) : null}
      </section>
      <header className="review-map-message" aria-label="The arrow">
        {current ? (
          <>
            <span className="review-kicker">
              Arrow {at + 1} of {stops.length}{sides ? ` · ${side} the change` : ''}
            </span>
            <span className="review-map-arrow">{current.message.text}</span>
            <span className="review-map-route">
              {participantLabel(current.flow, current.message.from)} → {participantLabel(current.flow, current.message.to)}
            </span>
          </>
        ) : (
          <p className="review-map-empty">No arrow in this diagram is tied to a stop.</p>
        )}
      </header>
    </div>
  );
}
