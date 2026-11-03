import type { ReactNode } from 'react';
import type { HeaderLevels, HeaderPiece, MeasuredLevel } from './headerFit';

export function FitPiece({ piece, levels, fitLevels, children }: {
  piece: HeaderPiece;
  levels: HeaderLevels;
  fitLevels: readonly MeasuredLevel[];
  children: ReactNode;
}) {
  return (
    <span
      className="desktop-pane-piece"
      data-fit-piece={piece}
      data-fit-levels={fitLevels.join(' ')}
      data-level={levels[piece] ?? 'full'}
    >
      {children}
    </span>
  );
}
