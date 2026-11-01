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
  elements: HTMLElement[];
}

function measurePieces(row: HTMLElement): Measured {
  const widths: Measured['widths'] = {};
  const pieces = Array.from(row.querySelectorAll<HTMLElement>('[data-fit-piece]'));
  for (const element of pieces) {
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
  const fixedElements = Array.from(row.querySelectorAll<HTMLElement>('[data-fit-fixed]'));
  const fixed = fixedElements.map((element) => element.getBoundingClientRect().width);
  return { widths, fixed, elements: [...pieces, ...fixedElements] };
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
    const observed = new WeakSet<HTMLElement>();

    const apply = (next: HeaderLevels) => setLevels((current) => (sameLevels(current, next) ? current : next));
    const fit = () => {
      const style = getComputedStyle(row);
      const available = row.clientWidth - (Number.parseFloat(style.paddingLeft) || 0) - (Number.parseFloat(style.paddingRight) || 0);
      if (available <= 0) {
        apply({});
        return;
      }
      if (!measured) {
        measured = measurePieces(row);
        for (const element of measured.elements) {
          if (observed.has(element)) continue;
          observed.add(element);
          resizes?.observe(element);
        }
      }
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

    const resizes = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver((entries) => {
      if (entries.every((entry) => entry.target === row)) fit();
      else remeasure();
    });
    fit();
    resizes?.observe(row);
    const mutations = typeof MutationObserver === 'undefined' ? null : new MutationObserver(remeasure);
    mutations?.observe(row, { childList: true, subtree: true, characterData: true });
    return () => {
      disposed = true;
      mutations?.disconnect();
      resizes?.disconnect();
    };
  }, [rowRef, rank, reserve, keepKey]);

  return levels;
}
