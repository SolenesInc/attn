import { memo, useMemo } from 'react';
import type { ReviewFlow, ReviewFlowMessage } from '../../types/generated';

const CHAR_WIDTH = 6.6;
const LABEL_LIMIT = 48;
const LINE_LIMIT = 26;
const LINE_HEIGHT = 14;
const BOX_HEIGHT = 30;
const ROW_GAP = 52;
const SMALLEST_SCALE = 0.75;
const MARGIN = 16;
const SELF_LOOP = 28;

function clip(text: string): string {
  return text.length > LABEL_LIMIT ? `${text.slice(0, LABEL_LIMIT - 1)}…` : text;
}

function textWidth(text: string): number {
  return clip(text).length * CHAR_WIDTH;
}

/** A message label in at most two lines of about LINE_LIMIT characters, so a
 * long label widens the diagram less. */
function labelLines(text: string): string[] {
  const words = text.split(/\s+/).filter(Boolean);
  const lines: string[] = [];
  for (const word of words) {
    const last = lines[lines.length - 1];
    if (last !== undefined && last.length + 1 + word.length <= LINE_LIMIT) lines[lines.length - 1] = `${last} ${word}`;
    else lines.push(word);
  }
  if (lines.length <= 2) return lines.map(clip);
  return [clip(lines[0]), clip(lines.slice(1).join(' '))].map((line, index) => (
    index === 1 && line.length > LINE_LIMIT ? `${line.slice(0, LINE_LIMIT - 1)}…` : line
  ));
}

function labelWidth(text: string): number {
  return Math.max(0, ...labelLines(text).map((line) => line.length * CHAR_WIDTH));
}

function Label({ text, x, y, anchor }: { text: string; x: number; y: number; anchor?: 'middle' }) {
  const lines = labelLines(text);
  return (
    <text className="review-seq-label" x={x} y={y - (lines.length - 1) * LINE_HEIGHT} textAnchor={anchor}>
      {lines.map((line, index) => <tspan key={index} x={x} dy={index === 0 ? 0 : LINE_HEIGHT}>{line}</tspan>)}
    </text>
  );
}

interface Layout {
  width: number;
  height: number;
  boxWidth: number[];
  x: Map<string, number>;
  rowY: (index: number) => number;
}

/** Columns are as far apart as the widest label between neighbours needs. */
function layout(flow: ReviewFlow): Layout {
  const ids = flow.participants.map((participant) => participant.id);
  const column = new Map(ids.map((id, index) => [id, index]));
  const boxWidth = flow.participants.map((participant) => Math.min(220, Math.max(96, textWidth(participant.label) + 28)));
  const gaps = ids.slice(1).map((_, index) => (boxWidth[index] + boxWidth[index + 1]) / 2 + 28);
  let selfRoomAtEnd = 0;
  for (const message of flow.messages) {
    const from = column.get(message.from) ?? 0;
    const to = column.get(message.to) ?? 0;
    const needed = labelWidth(message.text) + 36;
    if (from === to) {
      if (from < gaps.length) gaps[from] = Math.max(gaps[from], needed + SELF_LOOP);
      else selfRoomAtEnd = Math.max(selfRoomAtEnd, needed + SELF_LOOP - boxWidth[from] / 2);
    } else if (Math.abs(from - to) === 1) {
      const gap = Math.min(from, to);
      gaps[gap] = Math.max(gaps[gap], needed);
    }
  }
  const x = new Map<string, number>();
  let at = MARGIN + (boxWidth[0] ?? 0) / 2;
  ids.forEach((id, index) => {
    x.set(id, at);
    at += gaps[index] ?? 0;
  });
  const last = ids.length - 1;
  const width = (x.get(ids[last]) ?? 0) + (boxWidth[last] ?? 0) / 2 + Math.max(0, selfRoomAtEnd) + MARGIN;
  const top = MARGIN + BOX_HEIGHT + 34 + LINE_HEIGHT;
  const rowY = (index: number) => top + index * ROW_GAP;
  return { width, height: rowY(flow.messages.length) - 6, boxWidth, x, rowY };
}

function ArrowHead({ arrow, x, y, direction }: { arrow: string; x: number; y: number; direction: number }) {
  if (arrow.endsWith('>>')) return <path className="review-seq-head" d={`M${x} ${y} l${-direction * 9} -4.5 v9 z`} />;
  if (arrow.endsWith(')')) return <path className="review-seq-open" d={`M${x - direction * 8} ${y - 5} L${x} ${y} L${x - direction * 8} ${y + 5}`} />;
  if (arrow.endsWith('x')) return <path className="review-seq-open" d={`M${x - direction * 9} ${y - 4.5} l8 9 m0 -9 l-8 9`} />;
  return null;
}

function Message({ message, index, current, geometry }: {
  message: ReviewFlowMessage;
  index: number;
  current: boolean;
  geometry: Layout;
}) {
  const y = geometry.rowY(index);
  const x1 = geometry.x.get(message.from) ?? 0;
  const x2 = geometry.x.get(message.to) ?? 0;
  const dashed = message.arrow.startsWith('--');
  const stop = message.stop_line !== undefined;
  const className = `review-seq-message${stop ? ' review-seq-message--stop' : ''}${current ? ' review-seq-message--current' : ''}`;
  return (
    <g className={className} data-review-message={message.line} aria-current={current || undefined}>
      <title>{message.text}</title>
      <rect className="review-seq-hit" x={2} y={y - ROW_GAP + 8} width={geometry.width - 4} height={ROW_GAP - 4} rx={4} />
      {x1 === x2 ? (
        <>
          <path className={`review-seq-line${dashed ? ' review-seq-line--dashed' : ''}`} d={`M${x1} ${y - 14} h${SELF_LOOP} v14 h${-SELF_LOOP + 2}`} />
          <ArrowHead arrow={message.arrow} x={x1} y={y} direction={-1} />
          <Label text={message.text} x={x1 + SELF_LOOP + 6} y={y - 4} />
        </>
      ) : (
        <>
          <line className={`review-seq-line${dashed ? ' review-seq-line--dashed' : ''}`} x1={x1} y1={y} x2={x2 - Math.sign(x2 - x1) * 2} y2={y} />
          <ArrowHead arrow={message.arrow} x={x2} y={y} direction={Math.sign(x2 - x1)} />
          <Label text={message.text} x={(x1 + x2) / 2} y={y - 7} anchor="middle" />
        </>
      )}
    </g>
  );
}

/** A sequence diagram drawn from the bundle's parsed flow. Messages tied to a
 * stop are the places the Map's cursor stands. */
export const SequenceDiagram = memo(function SequenceDiagram({
  flow,
  current,
  onPick,
}: {
  flow: ReviewFlow;
  current: number | null;
  onPick: (message: ReviewFlowMessage) => void;
}) {
  const geometry = useMemo(() => layout(flow), [flow]);
  return (
    <svg
      className="review-seq"
      style={{ maxWidth: geometry.width, minWidth: geometry.width * SMALLEST_SCALE }}
      viewBox={`0 0 ${geometry.width} ${geometry.height}`}
      role="group"
      aria-label="Sequence diagram"
      data-review-flow={flow.line}
      onClick={(event) => {
        const line = Number((event.target as Element).closest('[data-review-message]')?.getAttribute('data-review-message'));
        const message = flow.messages.find((candidate) => candidate.line === line);
        if (message?.stop_line !== undefined) onPick(message);
      }}
    >
      {flow.participants.map((participant, index) => {
        const x = geometry.x.get(participant.id) ?? 0;
        const width = geometry.boxWidth[index];
        return (
          <g key={participant.id} className={`review-seq-participant${participant.actor ? ' review-seq-participant--actor' : ''}`}>
            <line className="review-seq-life" x1={x} y1={MARGIN + BOX_HEIGHT} x2={x} y2={geometry.height} />
            <rect x={x - width / 2} y={MARGIN} width={width} height={BOX_HEIGHT} rx={participant.actor ? BOX_HEIGHT / 2 : 5} />
            <text x={x} y={MARGIN + BOX_HEIGHT / 2 + 4} textAnchor="middle">{clip(participant.label)}</text>
          </g>
        );
      })}
      {flow.messages.map((message, index) => (
        <Message key={message.line} message={message} index={index} current={message.line === current} geometry={geometry} />
      ))}
    </svg>
  );
});
