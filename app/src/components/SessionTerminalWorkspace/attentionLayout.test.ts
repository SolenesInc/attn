import { describe, expect, it } from 'vitest';
import { getNormalizedPaneBounds, type TerminalLayoutNode } from '../../types/workspace';
import {
  projectAttentionLayout,
  reconcileAttentionSuspension,
  swapSuspendedLeaf,
} from './attentionLayout';

function twoAgentsAndDocument(direction: 'vertical' | 'horizontal' = 'vertical'): TerminalLayoutNode {
  return {
    type: 'split',
    splitId: 'outer',
    direction,
    ratio: 0.5,
    children: [
      {
        type: 'split',
        splitId: 'document-split',
        direction,
        ratio: 0.68,
        children: [
          { type: 'pane', paneId: 'agent-a' },
          { type: 'tile', tileId: 'document', tileKind: 'markdown' },
        ],
      },
      { type: 'pane', paneId: 'agent-b' },
    ],
  };
}

describe('workspace attention layout', () => {
  it('gives a markdown document more room while preserving the split topology', () => {
    const projected = projectAttentionLayout(twoAgentsAndDocument(), new Set(), { width: 1600, height: 700 });
    const bounds = getNormalizedPaneBounds(projected);
    expect(bounds.get('document')!.width).toBeGreaterThan(bounds.get('agent-a')!.width);
    expect(bounds.get('document')!.width).toBeGreaterThan(bounds.get('agent-b')!.width);
    expect(projected.type).toBe('split');
  });

  it('suspends the least recently focused peer as a vertical sliver when width is tight', () => {
    const tree = twoAgentsAndDocument();
    const suspended = reconcileAttentionSuspension(
      tree,
      new Set(),
      { width: 840, height: 700 },
      'document',
      ['agent-a', 'agent-b'],
    );
    expect(suspended).toContain('agent-b');
    expect(suspended).not.toContain('document');
    const bounds = getNormalizedPaneBounds(projectAttentionLayout(tree, suspended, { width: 840, height: 700 }));
    expect(bounds.get('agent-b')!.width * 840).toBeCloseTo(34, 0);
  });

  it('bends the weights at pane minimums before suspending anything', () => {
    const tree = twoAgentsAndDocument();
    const suspended = reconcileAttentionSuspension(
      tree,
      new Set(),
      { width: 1600, height: 700 },
      'agent-a',
      ['document', 'agent-b'],
    );
    expect(suspended).toEqual(new Set());
    const bounds = getNormalizedPaneBounds(projectAttentionLayout(tree, suspended, { width: 1600, height: 700 }));
    expect(bounds.get('agent-a')!.width * 1600).toBeCloseTo(480, 0);
    expect(bounds.get('agent-b')!.width * 1600).toBeCloseTo(480, 0);
    expect(bounds.get('document')!.width * 1600).toBeCloseTo(640, 0);
  });

  it('uses a horizontal state sliver for a horizontal split', () => {
    const tree = twoAgentsAndDocument('horizontal');
    const suspended = reconcileAttentionSuspension(
      tree,
      new Set(),
      { width: 1000, height: 520 },
      'document',
      ['agent-b', 'agent-a'],
    );
    expect(suspended.size).toBeGreaterThan(0);
    const suspendedId = [...suspended][0];
    const bounds = getNormalizedPaneBounds(projectAttentionLayout(tree, suspended, { width: 1000, height: 520 }));
    expect(bounds.get(suspendedId)!.height * 520).toBeCloseTo(34, 0);
  });

  it('suspends one of four horizontal leaves at the live window height', () => {
    const tree: TerminalLayoutNode = {
      type: 'split',
      splitId: 'fourth-leaf',
      direction: 'horizontal',
      ratio: 0.75,
      children: [
        twoAgentsAndDocument('horizontal'),
        { type: 'tile', tileId: 'second-document', tileKind: 'markdown' },
      ],
    };
    const suspended = reconcileAttentionSuspension(
      tree,
      new Set(),
      { width: 1816, height: 1258 },
      'second-document',
      ['document', 'agent-b', 'agent-a'],
    );
    expect(suspended.size).toBe(1);
    const bounds = getNormalizedPaneBounds(projectAttentionLayout(tree, suspended, { width: 1816, height: 1258 }));
    expect(bounds.get([...suspended][0])!.height * 1258).toBeCloseTo(34, 0);
  });

  it('reserves every sliver before giving a too-narrow remainder to the protected leaf', () => {
    let tree: TerminalLayoutNode = { type: 'pane', paneId: 'agent' };
    for (let index = 1; index <= 6; index += 1) {
      tree = {
        type: 'split',
        splitId: `split-${index}`,
        direction: 'vertical',
        ratio: 0.68,
        children: [
          tree,
          { type: 'tile', tileId: `document-${index}`, tileKind: 'markdown' },
        ],
      };
    }
    const viewport = { width: 560, height: 568 };
    const suspended = reconcileAttentionSuspension(tree, new Set(), viewport, 'agent', []);
    const bounds = getNormalizedPaneBounds(projectAttentionLayout(tree, suspended, viewport));

    expect(suspended.size).toBe(6);
    expect(bounds.get('agent')!.width * viewport.width).toBeCloseTo(356, 0);
    for (const documentId of suspended) {
      expect(bounds.get(documentId)!.width * viewport.width).toBeCloseTo(34, 0);
    }
  });

  it('restores a clicked sliver and hands its previous peer to the ring', () => {
    expect(swapSuspendedLeaf(new Set(['agent-b']), 'agent-b', 'document')).toEqual(new Set(['document']));
  });

  it('honors a manually resized split without dropping weighting elsewhere', () => {
    const tree = twoAgentsAndDocument();
    const projected = projectAttentionLayout(
      tree,
      new Set(),
      { width: 1600, height: 600 },
      new Set(['document-split']),
    );
    if (projected.type !== 'split') throw new Error('expected outer split');
    expect(projected.ratio).toBeCloseTo(0.7, 5);
    if (projected.children[0].type !== 'split') throw new Error('expected document split');
    expect(projected.children[0].ratio).toBeCloseTo(0.68, 5);
  });
});
