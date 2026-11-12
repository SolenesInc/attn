import { memo, useMemo } from 'react';
import type { ReviewFlow, ReviewFlowGroup, ReviewFlowMessage, ReviewFlowParticipant } from '../../types/generated';

const CHAR_WIDTH = 6.6;
const CAPTION_CHAR_WIDTH = 5.9;
const LABEL_LIMIT = 48;
const LINE_LIMIT = 26;
const CAPTION_LIMIT = 34;
const CAPTION_LINES = 3;
const LINE_HEIGHT = 14;
const CAPTION_LINE_HEIGHT = 13;
const BOX_HEIGHT = 30;
const TWO_LINE_BOX_HEIGHT = 42;
const ROW_PAD = 16;
const GROUP_HEAD = 20;
const GROUP_FOOT = 8;
const GROUP_PAD = 10;
const SMALLEST_SCALE = 0.75;
const LARGEST_SCALE = 1.25;
const MARGIN = 16;
const SELF_LOOP = 28;

const SECTION_KINDS = new Set(['else', 'and', 'option']);

function clip(text: string, limit = LABEL_LIMIT): string {
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}

/** Words in at most `count` lines of about `limit` characters; the last line
 * takes the rest, cut short. */
function wrap(text: string, limit: number, count: number): string[] {
  const lines: string[] = [];
  for (const word of text.split(/\s+/).filter(Boolean)) {
    const last = lines[lines.length - 1];
    if (last !== undefined && last.length + 1 + word.length <= limit) lines[lines.length - 1] = `${last} ${word}`;
    else lines.push(word);
  }
  if (lines.length <= count) return lines.map((line) => clip(line, Math.max(limit, LABEL_LIMIT)));
  return [...lines.slice(0, count - 1), clip(lines.slice(count - 1).join(' '), limit)];
}

const labelLines = (text: string) => wrap(text, LINE_LIMIT, 2);
const captionLines = (text: string | undefined) => (text ? wrap(text, CAPTION_LIMIT, CAPTION_LINES) : []);
const linesWidth = (lines: readonly string[], charWidth: number) => Math.max(0, ...lines.map((line) => line.length * charWidth));

/** A participant's name and, after a `<br/>` in its alias, its second line. */
function participantLines(participant: ReviewFlowParticipant): [string, string] {
  const [name, ...rest] = participant.label.split(/<br\s*\/?>/i);
  return [clip(name.trim()), clip(rest.join(' ').trim())];
}

function TextLines({ lines, x, y, lineHeight, className, anchor }: {
  lines: readonly string[];
  x: number;
  y: number;
  lineHeight: number;
  className: string;
  anchor?: 'middle';
}) {
  return (
    <text className={className} x={x} y={y} textAnchor={anchor}>
      {lines.map((line, index) => <tspan key={index} x={x} dy={index === 0 ? 0 : lineHeight}>{line}</tspan>)}
    </text>
  );
}

interface Row {
  message: ReviewFlowMessage;
  top: number;
  arrowY: number;
  bottom: number;
  label: string[];
  caption: string[];
}

interface Box {
  group: ReviewFlowGroup;
  left: number;
  right: number;
  top: number;
  bottom: number;
}

interface Divider {
  group: ReviewFlowGroup;
  y: number;
  block: ReviewFlowGroup | undefined;
}

interface Layout {
  width: number;
  height: number;
  boxHeight: number;
  boxWidth: number[];
  x: Map<string, number>;
  rows: Row[];
  boxes: Box[];
  dividers: Divider[];
}

const isSection = (group: ReviewFlowGroup) => SECTION_KINDS.has(group.kind);

function groupTitle(group: ReviewFlowGroup): string {
  if (group.kind === 'rect') return '';
  return group.label ? `${group.kind}  ${group.label}` : group.kind;
}

/** Columns are as far apart as the widest label or caption between
 * neighbours needs; rows are as tall as their label and caption, with room
 * for the head of each group that opens on them. */
function layout(flow: ReviewFlow): Layout {
  const ids = flow.participants.map((participant) => participant.id);
  const column = new Map(ids.map((id, index) => [id, index]));
  const names = flow.participants.map(participantLines);
  const boxHeight = names.some(([, second]) => second) ? TWO_LINE_BOX_HEIGHT : BOX_HEIGHT;
  const boxWidth = names.map(([name, second]) => Math.min(220, Math.max(96, name.length * CHAR_WIDTH + 28, second.length * CAPTION_CHAR_WIDTH + 28)));
  const gaps = ids.slice(1).map((_, index) => (boxWidth[index] + boxWidth[index + 1]) / 2 + 28);
  const textWidth = (message: ReviewFlowMessage) => Math.max(
    linesWidth(labelLines(message.text), CHAR_WIDTH),
    linesWidth(captionLines(message.note), CAPTION_CHAR_WIDTH),
  );
  for (const message of flow.messages) {
    const from = column.get(message.from) ?? 0;
    const to = column.get(message.to) ?? 0;
    const needed = textWidth(message) + 36;
    if (from === to && from < gaps.length) gaps[from] = Math.max(gaps[from], needed + SELF_LOOP);
    else if (Math.abs(from - to) === 1) {
      const gap = Math.min(from, to);
      gaps[gap] = Math.max(gaps[gap], needed);
    }
  }
  const x = new Map<string, number>();
  let at = 0;
  ids.forEach((id, index) => {
    x.set(id, at);
    at += gaps[index] ?? 0;
  });

  const blocks = flow.groups.filter((group) => !isSection(group));
  const opens = new Map<number, ReviewFlowGroup[]>();
  const closes = new Map<number, ReviewFlowGroup[]>();
  for (const group of flow.groups) opens.set(group.first_message, [...(opens.get(group.first_message) ?? []), group]);
  for (const block of blocks) closes.set(block.last_message, [block, ...(closes.get(block.last_message) ?? [])]);
  const tops = new Map<ReviewFlowGroup, number>();
  const bottoms = new Map<ReviewFlowGroup, number>();
  const dividers: Divider[] = [];
  const rows: Row[] = [];
  let y = MARGIN + boxHeight + 18;
  for (const message of flow.messages) {
    for (const group of opens.get(message.line) ?? []) {
      if (isSection(group)) dividers.push({ group, y, block: blocks.find((block) => block.line === group.block) });
      else tops.set(group, y);
      y += GROUP_HEAD;
    }
    const label = labelLines(message.text);
    const caption = captionLines(message.note);
    const self = message.from === message.to;
    const top = y;
    const arrowY = y + Math.max(label.length * LINE_HEIGHT, self ? 18 : 0) + 6;
    y = arrowY + (caption.length > 0 ? 6 + caption.length * CAPTION_LINE_HEIGHT : 0) + ROW_PAD;
    rows.push({ message, top, arrowY, bottom: y, label, caption });
    for (const block of closes.get(message.line) ?? []) {
      y += GROUP_FOOT;
      bottoms.set(block, y);
    }
  }

  const extent = (row: Row): [number, number] => {
    const x1 = x.get(row.message.from) ?? 0;
    const x2 = x.get(row.message.to) ?? 0;
    const wide = Math.max(linesWidth(row.label, CHAR_WIDTH), linesWidth(row.caption, CAPTION_CHAR_WIDTH));
    if (x1 === x2) return [x1 - 4, x1 + SELF_LOOP + 6 + wide];
    const middle = (x1 + x2) / 2;
    return [Math.min(x1, x2, middle - wide / 2), Math.max(x1, x2, middle + wide / 2)];
  };
  const boxOf = new Map<ReviewFlowGroup, Box>();
  for (const block of [...blocks].reverse()) {
    const inside = rows.filter((row) => row.message.line >= block.first_message && row.message.line <= block.last_message);
    const top = tops.get(block);
    const bottom = bottoms.get(block);
    if (inside.length === 0 || top === undefined || bottom === undefined) continue;
    const spans = [
      ...inside.map(extent),
      ...[...boxOf.values()].filter((box) => box.top >= top && box.bottom <= bottom).map((box): [number, number] => [box.left, box.right]),
    ];
    const left = Math.min(...spans.map(([from]) => from)) - GROUP_PAD;
    const right = Math.max(left + groupTitle(block).length * CAPTION_CHAR_WIDTH + 2 * GROUP_PAD, Math.max(...spans.map(([, to]) => to)) + GROUP_PAD);
    boxOf.set(block, { group: block, left, right, top, bottom });
  }
  const boxes = blocks.flatMap((block) => (boxOf.has(block) ? [boxOf.get(block)!] : []));

  const lefts = [
    ...ids.map((id, index) => (x.get(id) ?? 0) - boxWidth[index] / 2),
    ...rows.map((row) => extent(row)[0]),
    ...boxes.map((box) => box.left),
  ];
  const rights = [
    ...ids.map((id, index) => (x.get(id) ?? 0) + boxWidth[index] / 2),
    ...rows.map((row) => extent(row)[1]),
    ...boxes.map((box) => box.right),
  ];
  const shift = MARGIN - Math.min(0, ...lefts);
  for (const id of ids) x.set(id, (x.get(id) ?? 0) + shift);
  for (const box of boxes) {
    box.left += shift;
    box.right += shift;
  }
  return {
    width: Math.max(0, ...rights) + shift + MARGIN,
    height: y,
    boxHeight,
    boxWidth,
    x,
    rows,
    boxes,
    dividers: dividers.filter((divider) => divider.block === undefined || boxOf.has(divider.block)),
  };
}

function ArrowHead({ arrow, x, y, direction }: { arrow: string; x: number; y: number; direction: number }) {
  if (arrow.endsWith('>>')) return <path className="review-seq-head" d={`M${x} ${y} l${-direction * 9} -4.5 v9 z`} />;
  if (arrow.endsWith(')')) return <path className="review-seq-open" d={`M${x - direction * 8} ${y - 5} L${x} ${y} L${x - direction * 8} ${y + 5}`} />;
  if (arrow.endsWith('x')) return <path className="review-seq-open" d={`M${x - direction * 9} ${y - 4.5} l8 9 m0 -9 l-8 9`} />;
  return null;
}

function Message({ row, current, geometry }: { row: Row; current: boolean; geometry: Layout }) {
  const { message, arrowY: y, label, caption } = row;
  const x1 = geometry.x.get(message.from) ?? 0;
  const x2 = geometry.x.get(message.to) ?? 0;
  const dashed = message.arrow.startsWith('--');
  const stop = message.stop_line !== undefined;
  const lost = message.arrow.endsWith('x');
  const className = `review-seq-message${stop ? ' review-seq-message--stop' : ''}${current ? ' review-seq-message--current' : ''}${lost ? ' review-seq-message--lost' : ''}`;
  const line = `review-seq-line${dashed ? ' review-seq-line--dashed' : ''}`;
  const labelTop = (bottom: number) => bottom - (label.length - 1) * LINE_HEIGHT;
  return (
    <g className={className} data-review-message={message.line} aria-current={current || undefined}>
      <title>{message.note ? `${message.text}\n${message.note}` : message.text}</title>
      <rect className="review-seq-hit" x={2} y={row.top - ROW_PAD / 2} width={geometry.width - 4} height={row.bottom - row.top} rx={4} />
      {x1 === x2 ? (
        <>
          <path className={line} d={`M${x1} ${y - 14} h${SELF_LOOP} v14 h${-SELF_LOOP + 2}`} />
          <ArrowHead arrow={message.arrow} x={x1} y={y} direction={-1} />
          <TextLines className="review-seq-label" lines={label} x={x1 + SELF_LOOP + 6} y={labelTop(y - 4)} lineHeight={LINE_HEIGHT} />
          <TextLines className="review-seq-caption" lines={caption} x={x1 + SELF_LOOP + 6} y={y + 6 + CAPTION_LINE_HEIGHT - 2} lineHeight={CAPTION_LINE_HEIGHT} />
        </>
      ) : (
        <>
          <line className={line} x1={x1} y1={y} x2={x2 - Math.sign(x2 - x1) * 2} y2={y} />
          <ArrowHead arrow={message.arrow} x={x2} y={y} direction={Math.sign(x2 - x1)} />
          <TextLines className="review-seq-label" lines={label} x={(x1 + x2) / 2} y={labelTop(y - 7)} lineHeight={LINE_HEIGHT} anchor="middle" />
          <TextLines className="review-seq-caption" lines={caption} x={(x1 + x2) / 2} y={y + 6 + CAPTION_LINE_HEIGHT - 2} lineHeight={CAPTION_LINE_HEIGHT} anchor="middle" />
        </>
      )}
    </g>
  );
}

/** The narrowest a diagram draws: its width at the smallest scale. */
export function smallestDiagramWidth(flow: ReviewFlow): number {
  return layout(flow).width * SMALLEST_SCALE;
}

/** A sequence diagram drawn from the bundle's parsed flow, with its caption
 * under it. Messages tied to a stop are the places the Map's cursor stands. */
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
    <figure className="review-seq-figure" data-review-flow={flow.line}>
      <svg
        className="review-seq"
        style={{ maxWidth: geometry.width * LARGEST_SCALE, minWidth: geometry.width * SMALLEST_SCALE }}
        viewBox={`0 0 ${geometry.width} ${geometry.height}`}
        role="group"
        aria-label="Sequence diagram"
        onClick={(event) => {
          const line = Number((event.target as Element).closest('[data-review-message]')?.getAttribute('data-review-message'));
          const message = flow.messages.find((candidate) => candidate.line === line);
          if (message?.stop_line !== undefined) onPick(message);
        }}
      >
        {geometry.boxes.map((box) => (
          <g key={box.group.line} className={`review-seq-group review-seq-group--${box.group.kind}`} data-review-group={box.group.line}>
            <rect x={box.left} y={box.top} width={box.right - box.left} height={box.bottom - box.top} rx={4} />
            {groupTitle(box.group) ? <text className="review-seq-group-label" x={box.left + 6} y={box.top + 13}>{groupTitle(box.group)}</text> : null}
          </g>
        ))}
        {geometry.dividers.map((divider) => {
          const box = geometry.boxes.find((candidate) => candidate.group === divider.block);
          const left = box?.left ?? MARGIN;
          const right = box?.right ?? geometry.width - MARGIN;
          return (
            <g key={divider.group.line} className="review-seq-group review-seq-divider" data-review-group={divider.group.line}>
              <line x1={left} y1={divider.y} x2={right} y2={divider.y} />
              <text className="review-seq-group-label" x={left + 6} y={divider.y + 13}>{groupTitle(divider.group)}</text>
            </g>
          );
        })}
        {flow.participants.map((participant, index) => {
          const x = geometry.x.get(participant.id) ?? 0;
          const width = geometry.boxWidth[index];
          const [name, second] = participantLines(participant);
          return (
            <g key={participant.id} className={`review-seq-participant${participant.actor ? ' review-seq-participant--actor' : ''}`}>
              <line className="review-seq-life" x1={x} y1={MARGIN + geometry.boxHeight} x2={x} y2={geometry.height} />
              <rect x={x - width / 2} y={MARGIN} width={width} height={geometry.boxHeight} rx={participant.actor ? geometry.boxHeight / 2 : 5} />
              <text x={x} y={MARGIN + (second ? 17 : geometry.boxHeight / 2 + 4)} textAnchor="middle">{name}</text>
              {second ? <text className="review-seq-participant-second" x={x} y={MARGIN + 32} textAnchor="middle">{second}</text> : null}
            </g>
          );
        })}
        {geometry.rows.map((row) => (
          <Message key={row.message.line} row={row} current={row.message.line === current} geometry={geometry} />
        ))}
      </svg>
      {flow.note ? <figcaption className="review-seq-caption-text">{flow.note}</figcaption> : null}
    </figure>
  );
});
