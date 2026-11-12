import type { ReactNode } from 'react';
import type { ReviewRow } from '../../types/generated';
import type { LabeledName } from './navigation';
import type { TokenLine } from './tokenize';

/** The line's text with a label at the start of each name, keeping the tokens' colors. */
function LabeledText({ text, tokens, labels }: { text: string; tokens?: TokenLine; labels: readonly LabeledName[] }) {
  const pieces = tokens ?? [{ content: text }];
  const nodes: ReactNode[] = [];
  let next = 0;
  let offset = 0;
  pieces.forEach((token, index) => {
    let cut = 0;
    while (next < labels.length && labels[next].character < offset + token.content.length) {
      const at = labels[next].character - offset;
      if (at > cut) nodes.push(<span key={`${index}:${cut}`} style={token.style}>{token.content.slice(cut, at)}</span>);
      nodes.push(<span key={`label:${labels[next].label}`} className="review-name-label" data-label={labels[next].label} />);
      cut = at;
      next += 1;
    }
    nodes.push(<span key={`${index}:${cut}`} style={token.style}>{token.content.slice(cut)}</span>);
    offset += token.content.length;
  });
  return <>{nodes}</>;
}

export function CodeText({ text, tokens, labels }: { text: string; tokens?: TokenLine; labels?: readonly LabeledName[] }) {
  if (labels && labels.length > 0) return <LabeledText text={text} tokens={tokens} labels={labels} />;
  if (!tokens) return <>{text || ' '}</>;
  return (
    <>
      {tokens.map((token, index) => (
        <span key={index} style={token.style}>{token.content}</span>
      ))}
      {tokens.length === 0 ? ' ' : null}
    </>
  );
}

const SIGN: Record<string, string> = { '+': '+', '-': '−', ' ': '' };
const KIND_CLASS: Record<string, string> = { '+': ' review-row--added', '-': ' review-row--removed', ' ': '' };

export function rowKey(row: ReviewRow): string {
  return `${row.old ?? ''}:${row.now ?? ''}`;
}

export function CodeRow({
  row,
  tokens,
  path,
  inRange,
  cursorKey,
  cursor,
  labels,
  asked,
}: {
  row: ReviewRow;
  tokens?: TokenLine;
  path: string;
  inRange?: boolean;
  cursorKey?: string;
  cursor?: boolean;
  labels?: readonly LabeledName[];
  asked?: boolean;
}) {
  return (
    <div
      className={`review-row${KIND_CLASS[row.kind] ?? ''}${inRange ? ' review-row--in-range' : ''}${cursor ? ' review-cursor' : ''}${asked ? ' review-row--asked' : ''}`}
      data-review-cursor={cursorKey ? 'row' : undefined}
      data-cursor-key={cursorKey}
      data-path={path}
      data-line={row.now ?? undefined}
      data-old={row.old ?? undefined}
      data-head={row.head ?? undefined}
      data-kind={row.kind}
    >
      <span className="review-row-number">{row.old ?? ''}</span>
      <span className="review-row-number">{row.now ?? ''}</span>
      <span className="review-row-sign">{SIGN[row.kind] ?? ''}</span>
      <span className="review-row-text md-shiki"><CodeText text={row.text} tokens={tokens} labels={labels} /></span>
    </div>
  );
}
