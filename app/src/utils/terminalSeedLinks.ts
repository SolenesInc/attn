import {
  logicalIndexForCell,
  spanFromLogicalRange,
  type LogicalLine,
  type LogicalSpan,
} from './terminalLinks';

const SEED_ID_RE = /\bs-[0-9a-hjkmnp-tv-z]{6}\b/g;

export interface TerminalSeedOccurrence {
  seedId: string;
  line: LogicalLine;
  startIndex: number;
  endIndex: number;
  span: LogicalSpan;
}

export interface TerminalSeedSegment {
  seedId: string;
  row: number;
  startCol: number;
  endCol: number;
}

export function seedOccurrencesInLine(
  line: LogicalLine,
  knownSeedIds: ReadonlySet<string>,
): TerminalSeedOccurrence[] {
  const occurrences: TerminalSeedOccurrence[] = [];
  for (const match of line.text.matchAll(SEED_ID_RE)) {
    const seedId = match[0];
    const startIndex = match.index ?? -1;
    if (startIndex < 0 || !knownSeedIds.has(seedId)) continue;
    const endIndex = startIndex + seedId.length;
    occurrences.push({
      seedId,
      line,
      startIndex,
      endIndex,
      span: spanFromLogicalRange(line, startIndex, endIndex),
    });
  }
  return occurrences;
}

export function seedOccurrenceAtCell(
  occurrences: readonly TerminalSeedOccurrence[],
  cell: { row: number; col: number } | null,
): TerminalSeedOccurrence | null {
  if (!cell) return null;
  for (const occurrence of occurrences) {
    const index = logicalIndexForCell(occurrence.line, cell.row, cell.col);
    if (index !== null && index >= occurrence.startIndex && index < occurrence.endIndex) {
      return occurrence;
    }
  }
  return null;
}

export function seedOccurrenceSegments(
  occurrence: TerminalSeedOccurrence,
): TerminalSeedSegment[] {
  const { span, line, seedId } = occurrence;
  const segments: TerminalSeedSegment[] = [];
  for (let row = span.startRow; row <= span.endRow; row += 1) {
    const startCol = row === span.startRow ? span.startCol : 0;
    const endCol = row === span.endRow ? span.endCol : line.cols;
    if (endCol > startCol) segments.push({ seedId, row, startCol, endCol });
  }
  return segments;
}
