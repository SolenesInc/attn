export type PieceLevel = 'full' | 'short' | 'icon' | 'hidden';
export type MeasuredLevel = Exclude<PieceLevel, 'hidden'>;
export type HeaderPiece = 'seed' | 'role' | 'host' | 'usage' | 'provenance' | 'settling' | 'ask';
export type RankStep = readonly [HeaderPiece, PieceLevel];
export type PieceWidths = { full: number } & Partial<Record<MeasuredLevel, number>>;
export type HeaderLevels = Partial<Record<HeaderPiece, PieceLevel>>;

export interface FitInput {
  available: number;
  gap: number;
  fixed: readonly number[];
  widths: Partial<Record<HeaderPiece, PieceWidths>>;
  rank: readonly RankStep[];
  keep?: readonly HeaderPiece[];
}

export const TITLE_MINIMUM = 120;

export const HEADLINE_RANK: readonly RankStep[] = [
  ['settling', 'icon'],
  ['ask', 'short'],
  ['ask', 'icon'],
];

export const SUBLINE_RANK: readonly RankStep[] = [
  ['usage', 'hidden'],
  ['provenance', 'icon'],
  ['host', 'short'],
  ['role', 'icon'],
  ['seed', 'short'],
  ['seed', 'icon'],
  ['provenance', 'hidden'],
  ['role', 'hidden'],
  ['host', 'hidden'],
  ['seed', 'hidden'],
];

export function fitHeaderRow({ available, gap, fixed, widths, rank, keep = [] }: FitInput): HeaderLevels {
  const present = Object.keys(widths) as HeaderPiece[];
  const levels: HeaderLevels = Object.fromEntries(present.map((piece) => [piece, 'full']));
  const used = () => {
    const shown = present.filter((piece) => levels[piece] !== 'hidden');
    const pieces = shown.reduce((total, piece) => total + widths[piece]![levels[piece] as MeasuredLevel]!, 0);
    const items = fixed.length + shown.length;
    return fixed.reduce((total, width) => total + width, 0) + pieces + gap * Math.max(0, items - 1);
  };
  for (const [piece, level] of rank) {
    if (used() <= available) break;
    const pieceWidths = widths[piece];
    if (!pieceWidths) continue;
    const allowed = level === 'hidden' ? !keep.includes(piece) : pieceWidths[level] !== undefined;
    if (allowed) levels[piece] = level;
  }
  return levels;
}

export function hostMark(hostName: string): string {
  const digits = hostName.match(/\d+$/)?.[0] ?? '';
  const initials = hostName
    .slice(0, hostName.length - digits.length)
    .split('-')
    .filter(Boolean)
    .map((part) => part[0])
    .join('');
  return `${initials}${digits}`.toLowerCase() || hostName;
}

const normalizeTitle = (title: string) => title.toLowerCase().replace(/[^a-z0-9 ]/g, '').replace(/\s+/g, ' ').trim();

export function titlesRepeat(a: string, b: string): boolean {
  const x = normalizeTitle(a);
  const y = normalizeTitle(b);
  if (!x || !y) return false;
  return x === y || x.startsWith(y) || y.startsWith(x);
}
