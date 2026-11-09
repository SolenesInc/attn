import { describe, expect, it } from 'vitest';
import {
  HEADLINE_RANK,
  SUBLINE_RANK,
  TITLE_MINIMUM,
  fitHeaderRow,
  hostMark,
  titlesRepeat,
  type HeaderLevels,
  type HeaderPiece,
  type PieceWidths,
} from './headerFit';

const GAP = 8;
const DOT = 6;
const HEADLINE_FIXED = [DOT, TITLE_MINIMUM];
const SETTLING: PieceWidths = { full: 150, icon: 24 };
const ASK: PieceWidths = { full: 200, short: 140, icon: 24 };

function headline(available: number, widths: Partial<Record<HeaderPiece, PieceWidths>>) {
  return fitHeaderRow({ available, gap: GAP, fixed: HEADLINE_FIXED, widths, rank: HEADLINE_RANK });
}

describe('fitHeaderRow on the headline', () => {
  const both = { settling: SETTLING, ask: ASK };
  const titleRoom = DOT + GAP + TITLE_MINIMUM;

  it.each<[string, number, HeaderLevels]>([
    ['everything fits', titleRoom + GAP + 150 + GAP + 200, { settling: 'full', ask: 'full' }],
    ['settling turns to an icon first', titleRoom + GAP + 150 + GAP + 200 - 1, { settling: 'icon', ask: 'full' }],
    ['then the ask subject is cut', titleRoom + GAP + 24 + GAP + 200 - 1, { settling: 'icon', ask: 'short' }],
    ['then the ask turns to an icon', titleRoom + GAP + 24 + GAP + 140 - 1, { settling: 'icon', ask: 'icon' }],
    ['the title minimum yields to signal icons, which stay', titleRoom, { settling: 'icon', ask: 'icon' }],
    ['no width at all still shows every signal', 0, { settling: 'icon', ask: 'icon' }],
  ])('%s', (_name, available, expected) => {
    expect(headline(available, both)).toEqual(expected);
  });

  it('keeps the title its minimum against a full or short ask', () => {
    expect(headline(titleRoom + GAP + 200 - 1, { ask: ASK })).toEqual({ ask: 'short' });
    expect(headline(titleRoom + GAP + 140 - 1, { ask: ASK })).toEqual({ ask: 'icon' });
  });

  it('never hides a signal at any width', () => {
    for (let available = 0; available <= 700; available += 7) {
      const levels = headline(available, both);
      expect(levels.settling).not.toBe('hidden');
      expect(levels.ask).not.toBe('hidden');
    }
  });

  it('gives a header without signals nothing to fit', () => {
    expect(headline(40, {})).toEqual({});
  });
});

describe('fitHeaderRow on the subline', () => {
  const widths: Record<'seed' | 'role' | 'host' | 'usage' | 'id' | 'provenance', PieceWidths> = {
    seed: { full: 220, short: 140, icon: 16 },
    role: { full: 80, icon: 22 },
    host: { full: 70, short: 24 },
    usage: { full: 40 },
    id: { full: 60 },
    provenance: { full: 160, icon: 14 },
  };
  const SUBLINE_GAP = 10;
  const fit = (available: number, keep: HeaderPiece[] = []) =>
    fitHeaderRow({ available, gap: SUBLINE_GAP, fixed: [], widths, rank: SUBLINE_RANK, keep });
  const width = (levels: HeaderLevels) => {
    const shown = (Object.keys(widths) as Array<keyof typeof widths>).filter((piece) => levels[piece] !== 'hidden');
    return shown.reduce((total, piece) => total + widths[piece][levels[piece] as 'full']!, 0)
      + SUBLINE_GAP * Math.max(0, shown.length - 1);
  };

  const steps: HeaderLevels[] = [
    { seed: 'full', role: 'full', host: 'full', usage: 'full', id: 'full', provenance: 'full' },
    { seed: 'full', role: 'full', host: 'full', usage: 'hidden', id: 'full', provenance: 'full' },
    { seed: 'full', role: 'full', host: 'full', usage: 'hidden', id: 'hidden', provenance: 'full' },
    { seed: 'full', role: 'full', host: 'full', usage: 'hidden', id: 'hidden', provenance: 'icon' },
    { seed: 'full', role: 'full', host: 'short', usage: 'hidden', id: 'hidden', provenance: 'icon' },
    { seed: 'full', role: 'icon', host: 'short', usage: 'hidden', id: 'hidden', provenance: 'icon' },
    { seed: 'short', role: 'icon', host: 'short', usage: 'hidden', id: 'hidden', provenance: 'icon' },
    { seed: 'icon', role: 'icon', host: 'short', usage: 'hidden', id: 'hidden', provenance: 'icon' },
    { seed: 'icon', role: 'icon', host: 'short', usage: 'hidden', id: 'hidden', provenance: 'hidden' },
    { seed: 'icon', role: 'hidden', host: 'short', usage: 'hidden', id: 'hidden', provenance: 'hidden' },
    { seed: 'icon', role: 'hidden', host: 'hidden', usage: 'hidden', id: 'hidden', provenance: 'hidden' },
    { seed: 'hidden', role: 'hidden', host: 'hidden', usage: 'hidden', id: 'hidden', provenance: 'hidden' },
  ];

  it.each(steps.slice(1).map((expected, index) => [index + 1, steps[index], expected] as const))(
    'step %i applies only once the previous levels overflow, and stops as soon as the row fits',
    (_step, previous, expected) => {
      expect(fit(width(previous))).toEqual(previous);
      expect(fit(width(previous) - 1)).toEqual(expected);
    },
  );

  it('keeps the host while the session is offline, as its short mark', () => {
    expect(fit(0, ['host'])).toEqual({ seed: 'hidden', role: 'hidden', host: 'short', usage: 'hidden', id: 'hidden', provenance: 'hidden' });
  });

  it('skips a level a piece does not have', () => {
    const levels = fitHeaderRow({
      available: 30, gap: SUBLINE_GAP, fixed: [], rank: SUBLINE_RANK,
      widths: { usage: { full: 40 }, role: { full: 80 } },
    });
    expect(levels).toEqual({ usage: 'hidden', role: 'hidden' });
  });
});

describe('hostMark', () => {
  it.each([
    ['ai-sandbox', 'as'],
    ['gpu-box-2', 'gb2'],
    ['studio', 's'],
    ['build12', 'b12'],
  ])('%s reads as %s', (host, mark) => {
    expect(hostMark(host)).toBe(mark);
  });
});

describe('titlesRepeat', () => {
  it.each<[string, string, boolean]>([
    ['Prototype options for the session pane header', 'Prototype options for the session pane header strip', true],
    ['Fix the  Queue-Sidebar fit!', 'fix the queuesidebar fit', true],
    ['Review the release', 'Coordinate the release', false],
    ['', 'Anything', false],
  ])('%s / %s → %s', (a, b, expected) => {
    expect(titlesRepeat(a, b)).toBe(expected);
  });
});
