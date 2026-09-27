import { describe, expect, it } from 'vitest';
import { AUTO_LAYOUT, MAX_GRID_COLS, MAX_GRID_ROWS, resolveGridLayout, type GridLayout } from './gridLayout';

describe('resolveGridLayout', () => {
  it.each<[string, number, GridLayout, { rows: number; cols: number; capacity: number }]>([
    ['no tiles on auto', 0, AUTO_LAYOUT, { rows: 1, cols: 1, capacity: 0 }],
    ['one tile on auto', 1, AUTO_LAYOUT, { rows: 1, cols: 1, capacity: 1 }],
    ['a square count on auto', 4, AUTO_LAYOUT, { rows: 2, cols: 2, capacity: 4 }],
    ['one past a square on auto', 5, AUTO_LAYOUT, { rows: 2, cols: 3, capacity: 5 }],
    ['one short of a square on auto', 8, AUTO_LAYOUT, { rows: 3, cols: 3, capacity: 8 }],
    ['nine tiles on auto', 9, AUTO_LAYOUT, { rows: 3, cols: 3, capacity: 9 }],
    ['twenty-five tiles on auto', 25, AUTO_LAYOUT, { rows: 5, cols: 5, capacity: 25 }],
    ['more tiles than a fixed shape holds', 8, { mode: 'fixed', rows: 2, cols: 3 }, { rows: 2, cols: 3, capacity: 6 }],
    ['a fixed shape beyond the picker', 40, { mode: 'fixed', rows: 9, cols: 9 }, { rows: MAX_GRID_ROWS, cols: MAX_GRID_COLS, capacity: MAX_GRID_ROWS * MAX_GRID_COLS }],
  ])('sizes %s', (_, tiles, layout, expected) => {
    expect(resolveGridLayout(tiles, layout)).toEqual(expected);
  });
});
