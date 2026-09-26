import type { MigrationDraftDesktop, MigrationGroup, MigrationState } from '../../types/generated';
import { collectLayoutLeaves, getNormalizedPaneBounds, leafSlotId, parseLayoutJSON } from '../../types/workspace';

export type DropEdge = 'left' | 'right' | 'top' | 'bottom';

export type PlanNode =
  | { group: string }
  | { direction: 'vertical' | 'horizontal'; ratio: number; children: [PlanNode, PlanNode] };

export interface Box {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface GroupLeaf extends Box {
  id: string;
  label: string;
  tile: boolean;
  status: string;
}

export interface GroupView {
  group: MigrationGroup;
  leaves: GroupLeaf[];
  agents: number;
  tiles: number;
  launching: boolean;
  failed: boolean;
}

export interface DraftDesktopView {
  desktop: MigrationDraftDesktop;
  label: string;
  tree: PlanNode | null;
  groupIds: string[];
}

export interface DraftView {
  groups: GroupView[];
  groupById: Map<string, GroupView>;
  slots: DraftDesktopView[];
  extras: DraftDesktopView[];
  desktopOfGroup: Map<string, DraftDesktopView>;
  unconfirmed: string[];
}

const SLOT_COUNT = 9;

function isGroupNode(node: PlanNode): node is { group: string } {
  return 'group' in node;
}

export function parsePlanTree(json: string): PlanNode | null {
  if (!json.trim()) return null;
  return JSON.parse(json) as PlanNode;
}

export function planGroupIds(node: PlanNode | null): string[] {
  if (!node) return [];
  return isGroupNode(node) ? [node.group] : node.children.flatMap(planGroupIds);
}

export function planWithout(node: PlanNode | null, groupId: string): PlanNode | null {
  if (!node) return null;
  if (isGroupNode(node)) return node.group === groupId ? null : node;
  const first = planWithout(node.children[0], groupId);
  const second = planWithout(node.children[1], groupId);
  if (!first) return second;
  if (!second) return first;
  return { ...node, children: [first, second] };
}

function joined(existing: PlanNode, groupId: string, edge: DropEdge, share: number): PlanNode {
  const incoming = { group: groupId };
  const direction = edge === 'left' || edge === 'right' ? 'vertical' : 'horizontal';
  return edge === 'left' || edge === 'top'
    ? { direction, ratio: share, children: [incoming, existing] }
    : { direction, ratio: 1 - share, children: [existing, incoming] };
}

export function planJoin(
  node: PlanNode | null,
  groupId: string,
  anchorGroupId: string | null,
  edge: DropEdge,
  share: number,
): PlanNode {
  if (!node) return { group: groupId };
  if (!anchorGroupId) return joined(node, groupId, edge, share);
  if (isGroupNode(node)) return node.group === anchorGroupId ? joined(node, groupId, edge, share) : node;
  return {
    ...node,
    children: [
      planJoin(node.children[0], groupId, anchorGroupId, edge, share),
      planJoin(node.children[1], groupId, anchorGroupId, edge, share),
    ],
  };
}

export function planBoxes(node: PlanNode | null): Map<string, Box> {
  const boxes = new Map<string, Box>();
  const walk = (current: PlanNode, box: Box) => {
    if (isGroupNode(current)) {
      boxes.set(current.group, box);
      return;
    }
    const ratio = current.ratio > 0 && current.ratio < 1 ? current.ratio : 0.5;
    if (current.direction === 'vertical') {
      const firstWidth = box.width * ratio;
      walk(current.children[0], { ...box, width: firstWidth });
      walk(current.children[1], { ...box, left: box.left + firstWidth, width: box.width - firstWidth });
    } else {
      const firstHeight = box.height * ratio;
      walk(current.children[0], { ...box, height: firstHeight });
      walk(current.children[1], { ...box, top: box.top + firstHeight, height: box.height - firstHeight });
    }
  };
  if (node) walk(node, { left: 0, top: 0, width: 1, height: 1 });
  return boxes;
}

function groupView(group: MigrationGroup): GroupView {
  const tree = parseLayoutJSON(group.tree_json);
  const bounds = tree ? getNormalizedPaneBounds(tree) : new Map<string, Box>();
  const paneById = new Map(group.panes.map((pane) => [pane.pane_id, pane]));
  const leaves = collectLayoutLeaves(tree).flatMap((leaf): GroupLeaf[] => {
    const id = leafSlotId(leaf);
    const box = bounds.get(id);
    if (!box) return [];
    const pane = leaf.type === 'pane' ? paneById.get(leaf.paneId) : undefined;
    return [{
      id,
      left: box.left,
      top: box.top,
      width: box.width,
      height: box.height,
      label: leaf.type === 'pane' ? pane?.title || 'Agent' : leaf.tileKind,
      tile: leaf.type !== 'pane',
      status: pane?.status ?? 'ready',
    }];
  });
  return {
    group,
    leaves,
    agents: leaves.filter((leaf) => !leaf.tile).length,
    tiles: leaves.filter((leaf) => leaf.tile).length,
    launching: group.panes.some((pane) => pane.status === 'spawning'),
    failed: group.panes.some((pane) => pane.status === 'failed'),
  };
}

export function draftView(state: MigrationState): DraftView {
  const groups = state.groups.map(groupView);
  const slots: DraftDesktopView[] = [];
  const extras: DraftDesktopView[] = [];
  for (const desktop of state.desktops) {
    const tree = parsePlanTree(desktop.tree_json);
    const view = { desktop, tree, groupIds: planGroupIds(tree), label: '' };
    if (desktop.shortcut_slot) {
      slots.push({ ...view, label: `Desktop ${desktop.shortcut_slot}` });
    } else {
      extras.push({ ...view, label: `Desktop ${SLOT_COUNT + extras.length + 1}` });
    }
  }
  slots.sort((a, b) => (a.desktop.shortcut_slot ?? 0) - (b.desktop.shortcut_slot ?? 0));
  const desktopOfGroup = new Map<string, DraftDesktopView>();
  for (const desktop of [...slots, ...extras]) {
    for (const id of desktop.groupIds) desktopOfGroup.set(id, desktop);
  }
  return {
    groups,
    groupById: new Map(groups.map((view) => [view.group.group_id, view])),
    slots,
    extras,
    desktopOfGroup,
    unconfirmed: state.groups.filter((group) => !group.confirmed).map((group) => group.group_id),
  };
}

export function nearestEdge(rect: Box, x: number, y: number): DropEdge {
  const fromLeft = (x - rect.left) / rect.width;
  const fromTop = (y - rect.top) / rect.height;
  const distances: Array<[DropEdge, number]> = [
    ['left', fromLeft],
    ['right', 1 - fromLeft],
    ['top', fromTop],
    ['bottom', 1 - fromTop],
  ];
  return distances.reduce((best, entry) => (entry[1] < best[1] ? entry : best))[0];
}

export function summarize(view: GroupView): string {
  const agents = view.agents === 0 ? 'No agents' : `${view.agents} agent${view.agents === 1 ? '' : 's'}`;
  if (view.tiles === 0) return agents;
  return `${agents} · ${view.tiles} tile${view.tiles === 1 ? '' : 's'}`;
}

export function allDraftDesktops(view: DraftView): DraftDesktopView[] {
  return [...view.slots, ...view.extras];
}

export function findDraftDesktop(view: DraftView, key: string): DraftDesktopView | undefined {
  return allDraftDesktops(view).find((entry) => entry.desktop.key === key);
}
