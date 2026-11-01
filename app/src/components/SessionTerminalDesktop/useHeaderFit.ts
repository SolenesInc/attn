import { useLayoutEffect, useState, type RefObject } from 'react';
import {
  fitHeaderRow,
  type HeaderLevels,
  type HeaderPiece,
  type MeasuredLevel,
  type PieceWidths,
  type RankStep,
} from './headerFit';

interface Measured {
  widths: Partial<Record<HeaderPiece, PieceWidths>>;
  fixed: number[];
}

function measurePieces(row: HTMLElement): Measured {
  const widths: Measured['widths'] = {};
  for (const element of row.querySelectorAll<HTMLElement>('[data-fit-piece]')) {
    const shown = element.dataset.level;
    const levels = (element.dataset.fitLevels ?? 'full').split(' ') as MeasuredLevel[];
    const measured: Partial<Record<MeasuredLevel, number>> = {};
    for (const level of levels) {
      element.dataset.level = level;
      measured[level] = element.getBoundingClientRect().width;
    }
    if (shown === undefined) delete element.dataset.level;
    else element.dataset.level = shown;
    if (measured.full) widths[element.dataset.fitPiece as HeaderPiece] = measured as PieceWidths;
  }
  const fixed = Array.from(row.querySelectorAll<HTMLElement>('[data-fit-fixed]'), (element) => element.getBoundingClientRect().width);
  return { widths, fixed };
}

function sameLevels(a: HeaderLevels, b: HeaderLevels): boolean {
  const keys = Object.keys(a) as HeaderPiece[];
  return keys.length === Object.keys(b).length && keys.every((key) => a[key] === b[key]);
}

export function useHeaderFit(
  rowRef: RefObject<HTMLElement | null>,
  rank: readonly RankStep[],
  { reserve = 0, keep = [] }: { reserve?: number; keep?: readonly HeaderPiece[] } = {},
): HeaderLevels {
  const [levels, setLevels] = useState<HeaderLevels>({});
  const keepKey = keep.join(' ');

  useLayoutEffect(() => {
    const row = rowRef.current;
    if (!row) return;
    const kept = keepKey ? (keepKey.split(' ') as HeaderPiece[]) : [];
    let measured: Measured | null = null;
    let disposed = false;

    const apply = (next: HeaderLevels) => setLevels((current) => (sameLevels(current, next) ? current : next));
    const fit = () => {
      const style = getComputedStyle(row);
      const available = row.clientWidth - (Number.parseFloat(style.paddingLeft) || 0) - (Number.parseFloat(style.paddingRight) || 0);
      if (available <= 0) {
        apply({});
        return;
      }
      measured ??= measurePieces(row);
      apply(fitHeaderRow({
        available,
        gap: Number.parseFloat(style.columnGap) || 0,
        fixed: reserve ? [...measured.fixed, reserve] : measured.fixed,
        widths: measured.widths,
        rank,
        keep: kept,
      }));
    };
    const remeasure = () => {
      if (disposed) return;
      measured = null;
      fit();
    };

    fit();
    const resizes = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(fit);
    resizes?.observe(row);
    const mutations = typeof MutationObserver === 'undefined' ? null : new MutationObserver(remeasure);
    mutations?.observe(row, { childList: true, subtree: true, characterData: true });
    document.fonts?.ready.then(remeasure);
    return () => {
      disposed = true;
      mutations?.disconnect();
      resizes?.disconnect();
    };
  }, [rowRef, rank, reserve, keepKey]);

  return levels;
}
