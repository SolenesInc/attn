import { describe, expect, it } from 'vitest';
import {
  findLeafInDirection,
  findPaneInDirection,
  getNormalizedPaneBounds,
  parseNotebookTileParams,
  serializeNotebookTileParams,
  type TerminalLayoutNode,
} from './workspace';

type Direction = 'left' | 'right' | 'up' | 'down';

const SIDE_BY_SIDE: TerminalLayoutNode = {
  type: 'split',
  splitId: 'root',
  direction: 'vertical',
  ratio: 0.5,
  children: [
    { type: 'pane', paneId: 'left' },
    { type: 'pane', paneId: 'right' },
  ],
};

const RIGHT_COLUMN: TerminalLayoutNode = {
  type: 'split',
  splitId: 'root',
  direction: 'vertical',
  ratio: 0.5,
  children: [
    { type: 'pane', paneId: 'left' },
    {
      type: 'split',
      splitId: 'right',
      direction: 'horizontal',
      ratio: 0.5,
      children: [
        { type: 'pane', paneId: 'top-right' },
        { type: 'pane', paneId: 'bottom-right' },
      ],
    },
  ],
};

const TOP_ROW: TerminalLayoutNode = {
  type: 'split',
  splitId: 'root',
  direction: 'horizontal',
  ratio: 0.5,
  children: [
    {
      type: 'split',
      splitId: 'top',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'pane', paneId: 'top-left' },
        { type: 'pane', paneId: 'top-right' },
      ],
    },
    { type: 'pane', paneId: 'bottom' },
  ],
};

const PANE_TILE_PANE: TerminalLayoutNode = {
  type: 'split',
  splitId: 'root',
  direction: 'vertical',
  ratio: 0.5,
  children: [
    { type: 'pane', paneId: 'a' },
    {
      type: 'split',
      splitId: 'inner',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'tile', tileId: 'md', tileKind: 'markdown' },
        { type: 'pane', paneId: 'b' },
      ],
    },
  ],
};

describe('directional focus', () => {
  it.each<[string, TerminalLayoutNode, string, Direction, string | null, string | null]>([
    ['across siblings', SIDE_BY_SIDE, 'left', 'right', 'right', 'right'],
    ['back across siblings', SIDE_BY_SIDE, 'right', 'left', 'left', 'left'],
    ['past the left edge', SIDE_BY_SIDE, 'left', 'left', null, null],
    ['past the right edge', SIDE_BY_SIDE, 'right', 'right', null, null],
    ['from a leaf not in the layout', SIDE_BY_SIDE, 'missing', 'right', null, null],
    ['down a nested split', RIGHT_COLUMN, 'top-right', 'down', 'bottom-right', 'bottom-right'],
    ['up a nested split', RIGHT_COLUMN, 'bottom-right', 'up', 'top-right', 'top-right'],
    ['to the nearest pane on the right', TOP_ROW, 'top-left', 'right', 'top-right', 'top-right'],
    ['to the nearest pane below', TOP_ROW, 'top-right', 'down', 'bottom', 'bottom'],
    ['over a tile to the next pane', PANE_TILE_PANE, 'a', 'right', 'b', 'md'],
    ['back over a tile', PANE_TILE_PANE, 'b', 'left', 'a', 'md'],
    ['out of a tile to the right', PANE_TILE_PANE, 'md', 'right', null, 'b'],
    ['out of a tile to the left', PANE_TILE_PANE, 'md', 'left', null, 'a'],
  ])('moves %s', (_, layout, from, direction, pane, leaf) => {
    expect(findPaneInDirection(layout, from, direction)).toBe(pane);
    expect(findLeafInDirection(layout, from, direction)).toBe(leaf);
  });
});

describe('getNormalizedPaneBounds', () => {
  it('gives tiles slots alongside panes', () => {
    const bounds = getNormalizedPaneBounds(PANE_TILE_PANE);

    expect(bounds.get('a')!.right).toBeCloseTo(0.5);
    expect(bounds.get('md')!.left).toBeCloseTo(0.5);
    expect(bounds.get('md')!.right).toBeCloseTo(0.75);
    expect(bounds.get('b')!.left).toBeCloseTo(0.75);
  });
});

describe('notebook tile params', () => {
  it.each<[string, string | null | undefined, { root?: string; path?: string }]>([
    ['nothing', undefined, {}],
    ['null', null, {}],
    ['an empty string', '', {}],
    ['a legacy bare path', 'knowledge/areas/foo.md', { path: 'knowledge/areas/foo.md' }],
    ['something that only looks like JSON', '{not valid json', { path: '{not valid json' }],
    ['a root and a path', '{"root":"/Users/victor/code/attn","path":"README.md"}', { root: '/Users/victor/code/attn', path: 'README.md' }],
    ['a root alone', '{"root":"/tmp/some-root"}', { root: '/tmp/some-root' }],
    ['unknown fields', '{"root":"/repo","path":"a.md","bogus":"nope"}', { root: '/repo', path: 'a.md' }],
  ])('reads %s', (_, raw, params) => {
    expect(parseNotebookTileParams(raw)).toEqual(params);
  });

  it.each<[string, { root?: string; path?: string }]>([
    ['a rootless tile', { path: 'knowledge/areas/foo.md' }],
    ['a root-bound tile', { root: '/Users/victor/code/attn', path: 'README.md' }],
    ['a root with nothing open', { root: '/tmp/some-root' }],
    ['a root after opening another file', { root: '/repo', path: 'b.md' }],
  ])('reads back what it wrote for %s', (_, params) => {
    expect(parseNotebookTileParams(serializeNotebookTileParams(params))).toEqual(params);
  });

  it('keeps writing the legacy bare path for a rootless tile', () => {
    expect(serializeNotebookTileParams({ path: 'knowledge/areas/foo.md' })).toBe('knowledge/areas/foo.md');
  });
});
