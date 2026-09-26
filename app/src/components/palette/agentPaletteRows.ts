import type { TileLeaf } from '../../types/workspace';
import type { UISessionState } from '../../types/sessionState';
import { headOfQueue, type QueueBandSession, type QueueBands } from '../../utils/queueBands';
import { crewDisplayName } from '../../utils/crewName';
import { isSnoozed } from '../../utils/snoozeDurations';
import type { WorkspaceWithSessions } from '../../utils/workspaceViewModels';
import { groupAutomationSessions } from '../sidebarModel';

export interface PaletteSession extends QueueBandSession {
  state: UISessionState;
  automation?: { definition_id: string; definition_name: string };
}

export type AgentPaletteRow<S extends PaletteSession> =
  | { kind: 'agent'; key: string; session: S; anchored: boolean; queueHead: boolean }
  | { kind: 'member'; key: string; member: string }
  | { kind: 'tile'; key: string; desktopId: string; tile: TileLeaf; title: string }
  | { kind: 'divider'; key: string }
  | { kind: 'runs'; key: string; name: string; runs: number; needYou: number };

export interface AgentPaletteInput<S extends PaletteSession> {
  bands: QueueBands<S>;
  crewRoster: readonly string[];
  workspaces: readonly WorkspaceWithSessions<S>[];
  tileTitle: (desktopId: string, tile: TileLeaf) => string;
  now: number;
}

export type AgentStatus = 'chief' | 'snoozed' | 'approval' | 'waiting' | 'working' | 'idle';

export function agentStatus(session: PaletteSession, now: number): AgentStatus {
  if (session.chiefOfStaff) return 'chief';
  if (isSnoozed(session.turnSnoozedUntil, now)) return 'snoozed';
  if (session.turnOwed) return session.state === 'pending_approval' ? 'approval' : 'waiting';
  if (session.state === 'working' || session.state === 'launching') return 'working';
  return 'idle';
}

export function runNeedsYou(session: PaletteSession, now: number): boolean {
  return Boolean(session.turnOwed) && !isSnoozed(session.turnSnoozedUntil, now);
}

export function isSelectableRow<S extends PaletteSession>(row: AgentPaletteRow<S>): boolean {
  return row.kind === 'agent' || row.kind === 'member' || row.kind === 'tile';
}

export function selectableCount<S extends PaletteSession>(rows: readonly AgentPaletteRow<S>[]): number {
  return rows.filter(isSelectableRow).length;
}

function queryTerms(query: string): string[] {
  return query.toLowerCase().split(/\s+/).filter(Boolean);
}

function matches(terms: readonly string[], ...texts: (string | undefined)[]): boolean {
  if (terms.length === 0) return true;
  const haystack = texts.filter(Boolean).join(' ').toLowerCase();
  return terms.every((term) => haystack.includes(term));
}

export function agentPaletteRows<S extends PaletteSession>(
  { bands, crewRoster, workspaces, tileTitle, now }: AgentPaletteInput<S>,
  query: string,
): AgentPaletteRow<S>[] {
  const terms = queryTerms(query);
  const headId = terms.length === 0 ? headOfQueue(bands)?.session.id : undefined;
  const seen = new Set<string>();
  const agentRow = (session: S, anchored: boolean): AgentPaletteRow<S>[] => {
    if (seen.has(session.id)) return [];
    seen.add(session.id);
    if (!matches(terms, session.label, session.crewMember)) return [];
    return [{ kind: 'agent', key: `agent:${session.id}`, session, anchored, queueHead: session.id === headId }];
  };

  const anchored: AgentPaletteRow<S>[] = bands.chief ? agentRow(bands.chief.session, true) : [];
  const awakeByMember = new Map<string, typeof bands.crew>();
  for (const row of bands.crew) {
    const member = row.session.crewMember ?? '';
    awakeByMember.set(member, [...(awakeByMember.get(member) ?? []), row]);
  }
  const members = [...new Set([...crewRoster, ...awakeByMember.keys()])].sort();
  for (const member of members) {
    const awake = awakeByMember.get(member);
    if (awake) {
      for (const row of awake) anchored.push(...agentRow(row.session, true));
    } else if (matches(terms, member, crewDisplayName(member))) {
      anchored.push({ kind: 'member', key: `member:${member}`, member });
    }
  }

  const rest = [...bands.turns, ...bands.settled, ...bands.snoozed].flatMap((row) => agentRow(row.session, false));

  const tiles: AgentPaletteRow<S>[] = [];
  for (const workspace of workspaces) {
    for (const child of workspace.children) {
      if (child.kind !== 'tile') continue;
      const title = tileTitle(workspace.id, child.tile);
      if (!matches(terms, title)) continue;
      tiles.push({ kind: 'tile', key: `tile:${workspace.id}:${child.tile.tileId}`, desktopId: workspace.id, tile: child.tile, title });
    }
  }

  const runs: AgentPaletteRow<S>[] = [];
  for (const group of groupAutomationSessions(workspaces)) {
    const byName = matches(terms, group.name);
    const shown = byName ? group.sessions : group.sessions.filter((session) => matches(terms, session.label));
    if (shown.length === 0) continue;
    runs.push({
      kind: 'runs',
      key: `runs:${group.id}`,
      name: group.name,
      runs: group.sessions.length,
      needYou: group.sessions.filter((session) => runNeedsYou(session, now)).length,
    });
    for (const session of shown) runs.push(...agentRow(session, false));
  }

  const following = [...rest, ...tiles, ...runs];
  const divider: AgentPaletteRow<S>[] =
    anchored.length > 0 && following.length > 0 ? [{ kind: 'divider', key: 'divider' }] : [];
  return [...anchored, ...divider, ...following];
}
