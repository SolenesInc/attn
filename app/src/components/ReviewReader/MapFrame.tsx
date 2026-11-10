import type { RefObject } from 'react';
import type { ReviewBundle, ReviewFlow, ReviewFlowMessage } from '../../types/generated';
import type { MapStop, Side } from './frames';
import { SequenceDiagram } from './SequenceDiagram';
import { StopBlock } from './StopBlock';

function participantLabel(flow: ReviewFlow, id: string): string {
  return flow.participants.find((participant) => participant.id === id)?.label ?? id;
}

export function MapFrame({
  bundle,
  flows,
  stops,
  at,
  side,
  sides,
  detailRef,
  onPick,
  onSide,
  onDetailClick,
}: {
  bundle: ReviewBundle;
  flows: readonly ReviewFlow[];
  stops: readonly MapStop[];
  at: number;
  side: Side;
  sides: boolean;
  detailRef: RefObject<HTMLDivElement | null>;
  onPick: (message: ReviewFlowMessage) => void;
  onSide: (side: Side) => void;
  onDetailClick: (event: React.MouseEvent) => void;
}) {
  const current = stops[at];
  const stop = current ? bundle.stops.find((candidate) => candidate.heading_line === current.stopLine) : undefined;
  return (
    <div className="review-map">
      <section className="review-map-diagrams" aria-label="Map">
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
      </section>
      <section className="review-map-detail" ref={detailRef} onClick={onDetailClick} aria-label="The message's code">
        {current ? (
          <header className="review-map-message">
            <span className="review-kicker">
              Message {at + 1} of {stops.length}{sides ? ` · ${side} the change` : ''}
            </span>
            <h2>{current.message.text}</h2>
            <span className="review-map-route">
              {participantLabel(current.flow, current.message.from)} → {participantLabel(current.flow, current.message.to)}
            </span>
          </header>
        ) : (
          <p className="review-map-empty">No message in this diagram is tied to a stop.</p>
        )}
        {stop ? <StopBlock stop={stop} /> : null}
      </section>
    </div>
  );
}
