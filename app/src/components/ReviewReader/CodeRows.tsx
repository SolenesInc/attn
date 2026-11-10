import type { ReviewRow } from '../../types/generated';
import type { TokenLine } from './tokenize';

export function CodeText({ text, tokens }: { text: string; tokens?: TokenLine }) {
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
}: {
  row: ReviewRow;
  tokens?: TokenLine;
  path: string;
  inRange?: boolean;
  cursorKey?: string;
  cursor?: boolean;
}) {
  return (
    <div
      className={`review-row${KIND_CLASS[row.kind] ?? ''}${inRange ? ' review-row--in-range' : ''}${cursor ? ' review-cursor' : ''}`}
      data-review-cursor={cursorKey ? 'row' : undefined}
      data-cursor-key={cursorKey}
      data-path={path}
      data-line={row.now ?? undefined}
      data-old={row.old ?? undefined}
      data-kind={row.kind}
    >
      <span className="review-row-number">{row.old ?? ''}</span>
      <span className="review-row-number">{row.now ?? ''}</span>
      <span className="review-row-sign">{SIGN[row.kind] ?? ''}</span>
      <span className="review-row-text md-shiki"><CodeText text={row.text} tokens={tokens} /></span>
    </div>
  );
}
