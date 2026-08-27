import { describe, expect, it } from 'vitest';
import {
  seedOccurrenceAtCell,
  seedOccurrenceSegments,
  seedOccurrencesInLine,
} from './terminalSeedLinks';

function line(text: string, firstRow = 0, cols = 80) {
  return { text, firstRow, rowCount: Math.ceil(text.length / cols) || 1, cols };
}

describe('terminal seed links', () => {
  it('matches exact Crockford seed ids only when the garden knows them', () => {
    const known = new Set(['s-7k3f9m']);
    const occurrences = seedOccurrencesInLine(
      line('known s-7k3f9m; missing s-2m8q4v; invalid s-iiiiii'),
      known,
    );

    expect(occurrences.map((occurrence) => occurrence.seedId)).toEqual(['s-7k3f9m']);
  });

  it('does not match an id embedded in a larger word', () => {
    const known = new Set(['s-7k3f9m']);
    expect(seedOccurrencesInLine(line('xs-7k3f9m s-7k3f9more'), known)).toEqual([]);
  });

  it('hit-tests every cell of a known occurrence and nothing beside it', () => {
    const occurrences = seedOccurrencesInLine(line('see s-7k3f9m here'), new Set(['s-7k3f9m']));
    expect(seedOccurrenceAtCell(occurrences, { row: 0, col: 4 })?.seedId).toBe('s-7k3f9m');
    expect(seedOccurrenceAtCell(occurrences, { row: 0, col: 11 })?.seedId).toBe('s-7k3f9m');
    expect(seedOccurrenceAtCell(occurrences, { row: 0, col: 12 })).toBeNull();
  });

  it('projects a wrapped id into one marker per visible row', () => {
    const logical = line('  s-7k3f9m', 3, 6);
    const [occurrence] = seedOccurrencesInLine(logical, new Set(['s-7k3f9m']));

    expect(seedOccurrenceSegments(occurrence)).toEqual([
      { seedId: 's-7k3f9m', row: 3, startCol: 2, endCol: 6 },
      { seedId: 's-7k3f9m', row: 4, startCol: 0, endCol: 4 },
    ]);
    expect(seedOccurrenceAtCell([occurrence], { row: 4, col: 2 })?.seedId).toBe('s-7k3f9m');
  });
});
