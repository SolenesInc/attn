import type { TileLeaf } from '../../types/desktop';
import type { UISessionState } from '../../types/sessionState';
import { headOfQueue, type QueueBandSession, type QueueBands } from '../../utils/queueBands';
import { crewDisplayName } from '../../utils/crewName';
import { isSnoozed } from '../../utils/snoozeDurations';
import type { DesktopWithSessions } from '../../utils/desktopViewModels';
import { automationRunGroups, type AutomationRunSession } from '../../utils/automationRuns';

export interface PaletteSession extends Omit<QueueBandSession, 'automation'>, AutomationRunSession {
  state: UISessionState;
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
  desktops: readonly DesktopWithSessions<S>[];
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
  { bands, crewRoster, desktops, tileTitle, now }: AgentPaletteInput<S>,
  query: string,
): AgentPaletteRow<S>[] {
  const terms = queryTerms(query);
  const headId = terms.length === 0 ? headOfQueue(bands)?.session.id : undefined;
  const seen = new Set<string>();
  const agentRow = (session: S, anchored: boolean, matched: boolean): AgentPaletteRow<S>[] => {
    if (seen.has(session.id)) return [];
    seen.add(session.id);
    if (!matched) return [];
    return [{ kind: 'agent', key: `agent:${session.id}`, session, anchored, queueHead: session.id === headId }];
  };
  const bandRow = (session: S, anchored: boolean) =>
    agentRow(session, anchored, matches(terms, session.label, session.crewMember));

  const anchored: AgentPaletteRow<S>[] = bands.chief ? bandRow(bands.chief.session, true) : [];
  const awakeByMember = new Map<string, typeof bands.crew>();
  for (const row of bands.crew) {
    const member = row.session.crewMember ?? '';
    awakeByMember.set(member, [...(awakeByMember.get(member) ?? []), row]);
  }
  const members = [...new Set([...crewRoster, ...awakeByMember.keys()])].sort();
  for (const member of members) {
    const awake = awakeByMember.get(member);
    if (awake) {
      for (const row of awake) anchored.push(...bandRow(row.session, true));
    } else if (matches(terms, member, crewDisplayName(member))) {
      anchored.push({ kind: 'member', key: `member:${member}`, member });
    }
  }

  const rest = [...bands.turns, ...bands.settled, ...bands.snoozed].flatMap((row) => bandRow(row.session, false));

  const tiles: AgentPaletteRow<S>[] = [];
  for (const desktop of desktops) {
    for (const child of desktop.children) {
      if (child.kind !== 'tile') continue;
      const title = tileTitle(desktop.id, child.tile);
      if (!matches(terms, title)) continue;
      tiles.push({ kind: 'tile', key: `tile:${desktop.id}:${child.tile.tileId}`, desktopId: desktop.id, tile: child.tile, title });
    }
  }

  const runs: AgentPaletteRow<S>[] = [];
  for (const group of automationRunGroups(desktops, now)) {
    const byName = matches(terms, group.name);
    const shown = group.runs.flatMap((session) => agentRow(session, false, byName || matches(terms, session.label)));
    if (shown.length === 0) continue;
    runs.push({
      kind: 'runs',
      key: `runs:${group.id}`,
      name: group.name,
      runs: group.runs.length,
      needYou: group.needingYou.length,
    });
    runs.push(...shown);
  }

  const following = [...rest, ...tiles, ...runs];
  const divider: AgentPaletteRow<S>[] =
    anchored.length > 0 && following.length > 0 ? [{ kind: 'divider', key: 'divider' }] : [];
  return [...anchored, ...divider, ...following];
}
