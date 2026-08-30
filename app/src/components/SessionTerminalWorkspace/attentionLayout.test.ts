import { describe, expect, it } from 'vitest';
import { getNormalizedPaneBounds, type TerminalLayoutNode } from '../../types/workspace';
import { releaseSuspendedLeaf, resolveWorkspaceLayout } from './attentionLayout';

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

function resolve(
  sourceTree: TerminalLayoutNode,
  options: {
    width: number;
    height: number;
    activeLeafId: string;
    focusOrder?: readonly string[];
    previousSuspendedLeafIds?: ReadonlySet<string>;
    pendingRatioOverrides?: ReadonlyMap<string, number>;
  },
) {
  return resolveWorkspaceLayout({
    sourceTree,
    viewport: { width: options.width, height: options.height },
    activeLeafId: options.activeLeafId,
    focusOrder: options.focusOrder ?? [],
    previousSuspendedLeafIds: options.previousSuspendedLeafIds ?? new Set(),
    pendingRatioOverrides: options.pendingRatioOverrides,
  });
}

describe('workspace attention layout', () => {
  it('gives a markdown document more room while preserving the split topology', () => {
    const plan = resolve(twoAgentsAndDocument(), {
      width: 1600,
      height: 700,
      activeLeafId: 'document',
    });
    const bounds = getNormalizedPaneBounds(plan.renderedTree);
    expect(bounds.get('document')!.width).toBeGreaterThan(bounds.get('agent-a')!.width);
    expect(bounds.get('document')!.width).toBeGreaterThan(bounds.get('agent-b')!.width);
    expect(plan.renderedTree.type).toBe('split');
  });

  it('suspends the least recently focused peer as a vertical sliver when width is tight', () => {
    const plan = resolve(twoAgentsAndDocument(), {
      width: 840,
      height: 700,
      activeLeafId: 'document',
      focusOrder: ['agent-a', 'agent-b'],
    });
    expect(plan.suspendedLeafIds).toContain('agent-b');
    expect(plan.suspendedLeafIds).not.toContain('document');
    const bounds = getNormalizedPaneBounds(plan.renderedTree);
    expect(bounds.get('agent-b')!.width * 840).toBeCloseTo(34, 0);
  });

  it('bends automatic weights at pane minimums before suspending anything', () => {
    const plan = resolve(twoAgentsAndDocument(), {
      width: 1600,
      height: 700,
      activeLeafId: 'agent-a',
      focusOrder: ['document', 'agent-b'],
    });
    expect(plan.suspendedLeafIds).toEqual(new Set());
    const bounds = getNormalizedPaneBounds(plan.renderedTree);
    expect(bounds.get('agent-a')!.width * 1600).toBeCloseTo(480, 0);
    expect(bounds.get('agent-b')!.width * 1600).toBeCloseTo(480, 0);
    expect(bounds.get('document')!.width * 1600).toBeCloseTo(640, 0);
  });

  it('uses a horizontal state sliver for a horizontal split', () => {
    const plan = resolve(twoAgentsAndDocument('horizontal'), {
      width: 1000,
      height: 520,
      activeLeafId: 'document',
      focusOrder: ['agent-b', 'agent-a'],
    });
    expect(plan.suspendedLeafIds.size).toBeGreaterThan(0);
    const suspendedId = [...plan.suspendedLeafIds][0];
    const bounds = getNormalizedPaneBounds(plan.renderedTree);
    expect(bounds.get(suspendedId)!.height * 520).toBeCloseTo(34, 0);
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
    const plan = resolve(tree, { ...viewport, activeLeafId: 'agent' });
    const bounds = getNormalizedPaneBounds(plan.renderedTree);

    expect(plan.suspendedLeafIds.size).toBe(6);
    expect(bounds.get('agent')!.width * viewport.width).toBeCloseTo(356, 0);
    for (const documentId of plan.suspendedLeafIds) {
      expect(bounds.get(documentId)!.width * viewport.width).toBeCloseTo(34, 0);
    }
  });

  it('treats a preferred ratio as a target and keeps visible leaves viable', () => {
    const tree: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio: 0.8,
      ratioMode: 'preferred',
      children: [
        { type: 'pane', paneId: 'agent' },
        { type: 'tile', tileId: 'document', tileKind: 'markdown' },
      ],
    };
    const plan = resolve(tree, { width: 1000, height: 700, activeLeafId: 'agent' });

    expect(plan.suspendedLeafIds).toEqual(new Set());
    expect(plan.renderedTree).toMatchObject({ ratio: 0.52 });
  });

  it('uses persisted preferred sizing after the source layout is rebuilt', () => {
    const tree = twoAgentsAndDocument();
    if (tree.type !== 'split' || tree.children[0].type !== 'split') {
      throw new Error('expected nested split');
    }
    tree.children[0] = {
      ...tree.children[0],
      ratio: 0.68,
      ratioMode: 'preferred',
    };
    const plan = resolve(tree, { width: 3000, height: 700, activeLeafId: 'document' });
    if (plan.renderedTree.type !== 'split' || plan.renderedTree.children[0].type !== 'split') {
      throw new Error('expected nested split');
    }

    expect(plan.renderedTree.children[0].ratio).toBeCloseTo(0.68, 5);
  });

  it('restores a preferred ratio after capacity returns', () => {
    const tree: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio: 0.8,
      ratioMode: 'preferred',
      children: [
        { type: 'pane', paneId: 'agent' },
        { type: 'tile', tileId: 'document', tileKind: 'markdown' },
      ],
    };
    const crowded = resolve(tree, {
      width: 900,
      height: 700,
      activeLeafId: 'agent',
      focusOrder: ['document'],
    });
    expect(crowded.suspendedLeafIds).toEqual(new Set(['document']));

    const roomy = resolve(tree, {
      width: 2600,
      height: 700,
      activeLeafId: 'agent',
      focusOrder: ['document'],
      previousSuspendedLeafIds: crowded.suspendedLeafIds,
    });
    expect(roomy.suspendedLeafIds).toEqual(new Set());
    expect(roomy.renderedTree).toMatchObject({ ratio: 0.8 });
  });

  it('makes a suspended leaf an exact sliver after a preferred divider resize', () => {
    const tree: TerminalLayoutNode = {
      type: 'split',
      splitId: 'outer',
      direction: 'vertical',
      ratio: 0.8048036550110133,
      ratioMode: 'preferred',
      children: [
        {
          type: 'split',
          splitId: 'middle',
          direction: 'vertical',
          ratio: 0.67,
          children: [
            {
              type: 'split',
              splitId: 'inner',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'agent' },
                { type: 'tile', tileId: 'notes', tileKind: 'markdown' },
              ],
            },
            { type: 'tile', tileId: 'design', tileKind: 'markdown' },
          ],
        },
        { type: 'tile', tileId: 'readme', tileKind: 'markdown' },
      ],
    };
    const viewport = { width: 1816, height: 1258 };
    const collapsed = resolve(tree, {
      ...viewport,
      activeLeafId: 'design',
      focusOrder: ['agent', 'notes', 'readme'],
    });
    const collapsedBounds = getNormalizedPaneBounds(collapsed.renderedTree);

    expect(collapsed.suspendedLeafIds).toEqual(new Set(['readme']));
    expect(collapsedBounds.get('readme')!.width * viewport.width).toBeCloseTo(34, 0);

    const releasedIds = releaseSuspendedLeaf(collapsed.suspendedLeafIds, 'readme');
    const expanded = resolve(tree, {
      ...viewport,
      activeLeafId: 'readme',
      focusOrder: ['design', 'notes', 'agent'],
      previousSuspendedLeafIds: releasedIds,
    });
    const expandedBounds = getNormalizedPaneBounds(expanded.renderedTree);

    expect(expanded.suspendedLeafIds).toEqual(new Set(['agent']));
    expect(expandedBounds.get('agent')!.width * viewport.width).toBeCloseTo(34, 0);
    expect(expandedBounds.get('readme')!.width * viewport.width).toBeGreaterThanOrEqual(479.5);
  });

  it('uses a pending drag as preferred intent without weakening constraints', () => {
    const tree = twoAgentsAndDocument();
    const plan = resolve(tree, {
      width: 1600,
      height: 600,
      activeLeafId: 'document',
      pendingRatioOverrides: new Map([['document-split', 0.8]]),
    });
    if (plan.renderedTree.type !== 'split' || plan.renderedTree.children[0].type !== 'split') {
      throw new Error('expected nested split');
    }

    expect(plan.renderedTree.children[0].ratio).toBeLessThan(0.8);
    const bounds = getNormalizedPaneBounds(plan.renderedTree);
    expect(bounds.get('agent-a')!.width * 1600).toBeGreaterThanOrEqual(479.5);
    expect(bounds.get('document')!.width * 1600).toBeGreaterThanOrEqual(479.5);
  });

  it('releases only the clicked sliver, never folding a peer by itself', () => {
    expect(releaseSuspendedLeaf(new Set(['agent-b', 'document']), 'agent-b')).toEqual(new Set(['document']));
    const untouched = new Set(['document']);
    expect(releaseSuspendedLeaf(untouched, 'agent-b')).toBe(untouched);
  });

  function threeAgents(): TerminalLayoutNode {
    return {
      type: 'split',
      splitId: 'outer',
      direction: 'vertical',
      ratio: 1 / 3,
      children: [
        { type: 'pane', paneId: 'agent-a' },
        {
          type: 'split',
          splitId: 'inner',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', paneId: 'agent-b' },
            { type: 'pane', paneId: 'agent-c' },
          ],
        },
      ],
    };
  }

  it('restores a suspended leaf the moment the viewport fits it again', () => {
    // 1470px fits three 480px agents; whatever suspended one earlier (a tile
    // since closed, a window since grown) must not keep it collapsed.
    const plan = resolve(threeAgents(), {
      width: 1470,
      height: 800,
      activeLeafId: 'agent-b',
      focusOrder: ['agent-a', 'agent-c'],
      previousSuspendedLeafIds: new Set(['agent-c']),
    });

    expect(plan.suspendedLeafIds).toEqual(new Set());
  });

  it('folds never-focused leaves before anything in the focus order', () => {
    const plan = resolve(threeAgents(), {
      width: 1100,
      height: 800,
      activeLeafId: 'agent-c',
      focusOrder: ['agent-a'],
    });

    expect(plan.suspendedLeafIds).toEqual(new Set(['agent-b']));
  });

  it('re-aims the boundary beside a middle sliver at the split of its visible neighbors', () => {
    // agent-b collapses in the middle: [agent-a | sliver | agent-c].
    const viewport = { width: 1100, height: 800 };
    const plan = resolve(threeAgents(), {
      ...viewport,
      activeLeafId: 'agent-c',
      focusOrder: ['agent-a'],
    });

    expect(plan.suspendedLeafIds).toEqual(new Set(['agent-b']));
    const outerDividers = plan.dividers.filter((divider) => divider.splitId === 'outer');
    expect(plan.dividers).toHaveLength(2);
    expect(outerDividers).toHaveLength(2);
    const grab = outerDividers.find((divider) => divider.grabRatio != null)!;
    const own = outerDividers.find((divider) => divider.grabRatio == null)!;
    // The grab handle sits one sliver past the target split's own boundary.
    expect(grab.grabRatio! * viewport.width).toBeCloseTo(own.ratio * viewport.width + 34, 0);
    expect(grab.ratio).toBeCloseTo(own.ratio, 5);
  });

  it('gives a boundary with only viewport edge beyond the sliver no divider', () => {
    // agent-c collapses at the far right: nothing visible beyond it to trade.
    const plan = resolve(threeAgents(), {
      width: 1100,
      height: 800,
      activeLeafId: 'agent-a',
      focusOrder: ['agent-b'],
    });

    expect(plan.suspendedLeafIds).toEqual(new Set(['agent-c']));
    expect(plan.dividers).toHaveLength(1);
    expect(plan.dividers[0].splitId).toBe('outer');
    expect(plan.dividers[0].grabRatio).toBeUndefined();
  });
});
