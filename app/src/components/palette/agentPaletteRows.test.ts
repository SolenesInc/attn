import { describe, expect, it } from 'vitest';
import type { TileLeaf } from '../../types/workspace';
import { buildQueueBands } from '../../utils/queueBands';
import type { WorkspaceWithSessions } from '../../utils/workspaceViewModels';
import { agentPaletteRows, selectableCount, type AgentPaletteRow, type PaletteSession } from './agentPaletteRows';

const NOW = Date.parse('2026-09-26T12:00:00Z');
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString();

function session(id: string, fields: Partial<PaletteSession> = {}): PaletteSession {
  return { id, label: id, state: 'idle', ...fields };
}

function desktop(
  id: string,
  sessions: PaletteSession[],
  tiles: TileLeaf[] = [],
): WorkspaceWithSessions<PaletteSession> {
  return {
    id,
    title: id,
    directory: '/tmp',
    sessions,
    children: [
      ...sessions.map((entry) => ({ kind: 'session' as const, id: entry.id, session: entry })),
      ...tiles.map((tile) => ({ kind: 'tile' as const, id: tile.tileId, tile })),
    ],
    firstSessionId: sessions[0]?.id ?? null,
    focusedSessionId: null,
    hasUnresolvedAgentPanes: false,
  };
}

function tile(tileId: string, tileParams: string): TileLeaf {
  return { type: 'tile', tileId, tileKind: 'markdown', tileParams };
}

function rows(
  workspaces: WorkspaceWithSessions<PaletteSession>[],
  query = '',
  { crewRoster = [] as string[], crewInQueue = false } = {},
) {
  return agentPaletteRows(
    {
      bands: buildQueueBands(workspaces, { crewInQueue, now: NOW }),
      crewRoster,
      workspaces,
      tileTitle: (_desktopId, leaf) => leaf.tileParams ?? '',
      now: NOW,
    },
    query,
  );
}

function describeRow(row: AgentPaletteRow<PaletteSession>): string {
  switch (row.kind) {
    case 'agent':
      return `${row.anchored ? '*' : ''}${row.session.id}${row.queueHead ? ' ⌘J' : ''}`;
    case 'member':
      return `asleep ${row.member}`;
    case 'tile':
      return `tile ${row.title}`;
    case 'divider':
      return '---';
    case 'runs':
      return `[${row.name}: ${row.needYou} need you, ${row.runs} runs]`;
  }
}

const automation = (definition_id: string, definition_name: string) => ({ definition_id, definition_name });

const fixture = [
  desktop(
    'd1',
    [
      session('chief', { chiefOfStaff: true, state: 'working' }),
      session('figgy-day', { label: 'figgy', crewMember: 'figgy', turnOwed: true, turnOpenedAt: minutesAgo(1) }),
      session('settled-agent'),
      session('newer-turn', { turnOwed: true, turnOpenedAt: minutesAgo(2) }),
      session('older-turn', { turnOwed: true, turnOpenedAt: minutesAgo(9) }),
      session('snoozed-agent', { turnSnoozedUntil: new Date(NOW + 3_600_000).toISOString() }),
      session('nightly-1', { automation: automation('nightly', 'Nightly'), turnOwed: true }),
      session('nightly-2', { automation: automation('nightly', 'Nightly') }),
    ],
    [tile('t1', 'release notes')],
  ),
  desktop('d2', [session('triage-1', { automation: automation('triage', 'Triage') })]),
];

describe('agentPaletteRows', () => {
  it('orders the crew block, then the queue bands, tiles and automation runs under their definition', () => {
    expect(rows(fixture, '', { crewRoster: ['figgy', 'gardener'] }).map(describeRow)).toEqual([
      '*chief',
      '*figgy-day',
      'asleep gardener',
      '---',
      'older-turn ⌘J',
      'newer-turn',
      'settled-agent',
      'snoozed-agent',
      'tile release notes',
      '[Nightly: 1 need you, 2 runs]',
      'nightly-1',
      'nightly-2',
      '[Triage: 0 need you, 1 runs]',
      'triage-1',
    ]);
  });

  it('keeps crew in their block when crew join the queue, and never tags a run with ⌘J', () => {
    const described = rows(fixture, '', { crewRoster: ['figgy'], crewInQueue: true }).map(describeRow);
    expect(described.filter((entry) => entry.includes('figgy'))).toEqual(['*figgy-day']);
    expect(described.filter((entry) => entry.includes('⌘J'))).toEqual(['older-turn ⌘J']);
  });

  it('filters every kind of row and drops the ⌘J tag and empty groups', () => {
    expect(rows(fixture, 'turn').map(describeRow)).toEqual(['older-turn', 'newer-turn']);
    expect(rows(fixture, 'garden', { crewRoster: ['gardener'] }).map(describeRow)).toEqual(['asleep gardener']);
    expect(rows(fixture, 'release').map(describeRow)).toEqual(['tile release notes']);
    expect(rows(fixture, 'nightly').map(describeRow)).toEqual([
      '[Nightly: 1 need you, 2 runs]',
      'nightly-1',
      'nightly-2',
    ]);
    expect(rows(fixture, 'triage-1').map(describeRow)).toEqual(['[Triage: 0 need you, 1 runs]', 'triage-1']);
  });

  it('counts only selectable rows', () => {
    const all = rows(fixture, '', { crewRoster: ['gardener'] });
    expect(selectableCount(all)).toBe(all.length - 3);
  });
});
