import { emptyDesktop, type DaemonDesktop, type DaemonProfile } from './daemonFixtures';
import type { CommandMessage } from './protocol';
import type { Reply, ScriptedDaemon } from './scriptedDaemon';

interface Node {
  type: string;
  pane_id?: string;
  tile_id?: string;
  tile_kind?: string;
  tile_params?: string;
  tile_session_id?: string;
  split_id?: string;
  direction?: string;
  ratio?: number;
  children?: Node[];
}

function parse(desktop: DaemonDesktop): Node | null {
  return desktop.tree_json ? (JSON.parse(desktop.tree_json) as Node) : null;
}

function leafId(node: Node): string | undefined {
  return node.type === 'pane' ? node.pane_id : node.type === 'tile' ? node.tile_id : undefined;
}

function without(node: Node | null, leaf: string): Node | null {
  if (!node) return null;
  if (leafId(node) === leaf) return null;
  if (node.type !== 'split' || !node.children) return node;
  const children = node.children.map((child) => without(child, leaf)).filter((child): child is Node => child !== null);
  if (children.length === 0) return null;
  if (children.length === 1) return children[0];
  return { ...node, children };
}

function mapTiles(node: Node | null, tileId: string, change: (tile: Node) => Node): Node | null {
  if (!node) return null;
  if (node.type === 'tile' && node.tile_id === tileId) return change(node);
  if (!node.children) return node;
  return { ...node, children: node.children.map((child) => mapTiles(child, tileId, change)!) };
}

function mapSplit(node: Node | null, splitId: string, change: (split: Node) => Node): Node | null {
  if (!node) return null;
  if (node.type === 'split' && node.split_id === splitId) return change(node);
  if (!node.children) return node;
  return { ...node, children: node.children.map((child) => mapSplit(child, splitId, change)!) };
}

function findLeaf(node: Node | null, leaf: string): Node | null {
  if (!node) return null;
  if (leafId(node) === leaf) return node;
  for (const child of node.children ?? []) {
    const found = findLeaf(child, leaf);
    if (found) return found;
  }
  return null;
}

function hasLeaf(node: Node | null, leaf: string): boolean {
  if (!node) return false;
  if (leafId(node) === leaf) return true;
  return (node.children ?? []).some((child) => hasLeaf(child, leaf));
}

export class Arrangement {
  constructor(
    public profiles: DaemonProfile[],
    public desktops: DaemonDesktop[],
    public selectedProfileId: string,
  ) {}

  get profile(): DaemonProfile {
    return this.profiles.find((profile) => profile.id === this.selectedProfileId) ?? this.profiles[0];
  }

  desktop(id: string): DaemonDesktop | undefined {
    return this.desktops.find((desktop) => desktop.id === id);
  }

  replace(desktop: DaemonDesktop) {
    this.desktops = this.desktops.map((existing) =>
      existing.id === desktop.id ? { ...desktop, revision: existing.revision + 1 } : existing,
    );
  }

  withTree(desktop: DaemonDesktop, tree: Node | null, panes = desktop.panes): DaemonDesktop {
    const kept = panes.filter((pane) => hasLeaf(tree, pane.pane_id));
    const active = hasLeaf(tree, desktop.active_pane_id) ? desktop.active_pane_id : (kept[0]?.pane_id ?? '');
    return { ...desktop, tree_json: tree ? JSON.stringify(tree) : '', panes: kept, active_pane_id: active };
  }

  place(sessionId: string, paneId: string, desktopId = this.profile.current_desktop_id) {
    const desktop = this.desktop(desktopId);
    if (!desktop) return;
    const pane = { type: 'pane', pane_id: paneId };
    const current = parse(desktop);
    const tree = current ? { type: 'split', split_id: `split-${paneId}`, direction: 'vertical', ratio: 0.5, children: [current, pane] } : pane;
    const placed = this.withTree(desktop, tree, [
      ...desktop.panes,
      { pane_id: paneId, session_id: sessionId, runtime_id: sessionId, desktop_id: desktop.id, kind: 'agent', status: 'ready', title: sessionId } as DaemonDesktop['panes'][number],
    ]);
    this.replace({ ...placed, active_pane_id: paneId });
  }

  show(desktopId: string, leafId: string): boolean {
    const desktop = this.desktop(desktopId);
    if (!desktop || !hasLeaf(parse(desktop), leafId)) return false;
    this.desktops = this.desktops.map((existing) => (existing.id === desktopId ? { ...existing, active_pane_id: leafId } : existing));
    this.selectedProfileId = desktop.profile_id;
    this.profiles = this.profiles.map((profile) =>
      profile.id === desktop.profile_id ? { ...profile, current_desktop_id: desktopId } : profile,
    );
    return true;
  }

  placementOf(sessionId: string): { desktopId: string; paneId: string } | null {
    for (const desktop of this.desktops) {
      const pane = desktop.panes.find((entry) => entry.session_id === sessionId);
      if (pane) return { desktopId: desktop.id, paneId: pane.pane_id };
    }
    return null;
  }

  changed(): Reply {
    return { event: 'profile_arrangement_changed', profile: this.profile, desktops: this.desktops.filter((desktop) => desktop.profile_id === this.profile.id) };
  }

  // The requester's own copy, sent before its result like the daemon does.
  answer(command: { request_id?: string }): Reply {
    return { ...(this.changed() as object), request_id: command.request_id ?? '' } as Reply;
  }
}

type ProfileCommand = CommandMessage & { request_id?: string };

function accepted(command: ProfileCommand, arrangement: Arrangement, paneId?: string): Reply[] {
  return [
    arrangement.answer(command),
    { event: 'profile_action_result', action: command.cmd, success: true, request_id: command.request_id ?? '', ...(paneId ? { pane_id: paneId } : {}) } as Reply,
  ];
}

function refused(command: ProfileCommand, code: string, error: string): Reply[] {
  return [{ event: 'profile_action_result', action: command.cmd, success: false, error_code: code, error, request_id: command.request_id ?? '' } as Reply];
}

export function serveArrangement(daemon: ScriptedDaemon, arrangement: Arrangement) {
  daemon.on('profile_select', (command) => {
    arrangement.selectedProfileId = command.profile_id;
    return accepted(command, arrangement);
  });
  daemon.on('desktop_set_current', (command) => {
    arrangement.profiles = arrangement.profiles.map((profile) =>
      profile.id === command.profile_id ? { ...profile, current_desktop_id: command.desktop_id, revision: profile.revision + 1 } : profile,
    );
    return accepted(command, arrangement);
  });
  daemon.on('desktop_set_active_pane', (command) => {
    const desktop = arrangement.desktop(command.desktop_id);
    if (desktop) arrangement.replace({ ...desktop, active_pane_id: command.pane_id });
    return accepted(command, arrangement);
  });
  daemon.on('desktop_show_session', (command) => {
    let placed = arrangement.placementOf(command.session_id);
    if (!placed) {
      const paneId = `pane-${command.session_id}`;
      arrangement.place(command.session_id, paneId);
      placed = arrangement.placementOf(command.session_id);
    }
    if (!placed || !arrangement.show(placed.desktopId, placed.paneId)) {
      return refused(command, 'not_found', `session ${command.session_id} could not be shown`);
    }
    return accepted(command, arrangement, placed.paneId);
  });
  daemon.on('desktop_show_leaf', (command) => {
    if (!arrangement.show(command.desktop_id, command.leaf_id)) {
      return refused(command, 'not_found', `leaf ${command.leaf_id} does not belong to desktop ${command.desktop_id}`);
    }
    return accepted(command, arrangement, command.leaf_id);
  });
  daemon.on('desktop_create', (command) => {
    const held = (candidate: number) => arrangement.desktops.some((desktop) => desktop.profile_id === command.profile_id && desktop.shortcut_slot === candidate);
    const slot = command.shortcut_slot ?? [1, 2, 3, 4, 5, 6, 7, 8, 9].find((candidate) => !held(candidate));
    const holder = arrangement.desktops.find((desktop) => desktop.profile_id === command.profile_id && slot && desktop.shortcut_slot === slot);
    if (holder) return refused(command, 'slot_taken', `shortcut slot ${slot} is held by desktop ${holder.id}`);
    const id = slot ? `${command.profile_id}/desktop_${slot}` : `desktop-new-${arrangement.desktops.length + 1}`;
    const created = emptyDesktop(id, { profile_id: command.profile_id, shortcut_slot: slot, order_key: 'z' });
    arrangement.desktops = [...arrangement.desktops, created];
    const [changed, result] = accepted(command, arrangement);
    return [changed, { ...(result as object), desktops: [created] } as Reply];
  });
  // Like the daemon, a leaf whose id the target already holds lands under a new id.
  daemon.on('desktop_move_leaf', (command) => {
    const source = arrangement.desktop(command.source_desktop_id);
    const target = arrangement.desktop(command.target_desktop_id);
    const leaf = source ? findLeaf(parse(source), command.leaf_id) : null;
    if (!source || !target || !leaf) {
      return refused(command, 'not_found', `leaf ${command.leaf_id} does not belong to desktop ${command.source_desktop_id}`);
    }
    const finalId = hasLeaf(parse(target), command.leaf_id) ? `${command.leaf_id}-moved` : command.leaf_id;
    const landed: Node = leaf.type === 'pane' ? { ...leaf, pane_id: finalId } : { ...leaf, tile_id: finalId };
    const current = parse(target);
    const tree = current ? { type: 'split', split_id: `split-${finalId}`, direction: 'vertical', ratio: 0.5, children: [current, landed] } : landed;
    const pane = source.panes.find((entry) => entry.pane_id === command.leaf_id);
    const panes = pane ? [...target.panes, { ...pane, pane_id: finalId, desktop_id: target.id }] : target.panes;
    arrangement.replace(arrangement.withTree(source, without(parse(source), command.leaf_id)));
    arrangement.replace({ ...arrangement.withTree(target, tree, panes), active_pane_id: finalId });
    const moved = { from_desktop_id: source.id, from_leaf_id: command.leaf_id, to_desktop_id: target.id, to_leaf_id: finalId };
    const [changed, result] = accepted(command, arrangement, finalId);
    return [{ ...(changed as object), moved_leaf: moved } as Reply, result];
  });
  daemon.on('desktop_remove_leaf', (command) => {
    const desktop = arrangement.desktop(command.desktop_id);
    if (desktop) arrangement.replace(arrangement.withTree(desktop, without(parse(desktop), command.leaf_id)));
    return accepted(command, arrangement);
  });
  daemon.on('desktop_update_tile', (command) => {
    const desktop = arrangement.desktop(command.desktop_id);
    if (desktop) {
      const tree = mapTiles(parse(desktop), command.tile_id, (tile) => ({
        ...tile,
        ...(command.tile_params !== undefined ? { tile_params: command.tile_params } : {}),
        ...(command.tile_session_id !== undefined ? { tile_session_id: command.tile_session_id } : {}),
      }));
      arrangement.replace(arrangement.withTree(desktop, tree));
    }
    return accepted(command, arrangement);
  });
  daemon.on('desktop_dock_tile', (command) => {
    const desktop = arrangement.desktop(command.desktop_id);
    if (desktop) {
      const current = without(parse(desktop), command.tile_id);
      const tile: Node = {
        type: 'tile',
        tile_id: command.tile_id,
        tile_kind: command.tile_kind,
        ...(command.tile_params !== undefined ? { tile_params: command.tile_params } : {}),
        ...(command.tile_session_id !== undefined ? { tile_session_id: command.tile_session_id } : {}),
      };
      const tree = current ? { type: 'split', split_id: `split-${command.tile_id}`, direction: 'vertical', ratio: 0.5, children: [current, tile] } : tile;
      arrangement.replace({ ...arrangement.withTree(desktop, tree), active_pane_id: command.tile_id });
    }
    return accepted(command, arrangement);
  });
  daemon.on('desktop_set_split_ratio', (command) => {
    const desktop = arrangement.desktop(command.desktop_id);
    if (desktop) arrangement.replace(arrangement.withTree(desktop, mapSplit(parse(desktop), command.split_id, (split) => ({ ...split, ratio: command.ratio }))));
    return accepted(command, arrangement);
  });
  daemon.on('desktop_place_session', (command) => {
    arrangement.place(command.session_id, `pane-${command.session_id}`, command.desktop_id);
    return accepted(command, arrangement);
  });
  daemon.on('desktop_rename', (command) => {
    const desktop = arrangement.desktop(command.desktop_id);
    if (desktop) arrangement.replace({ ...desktop, name: command.name });
    return accepted(command, arrangement);
  });
}
