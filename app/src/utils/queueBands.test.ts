import { describe, expect, it } from 'vitest';
import { buildQueueBands, type QueueBandSession } from './queueBands';
import { buildWorkspaceViewModels } from './workspaceViewModels';

const NOW = Date.parse('2026-07-26T12:00:00Z');
const LATER_TODAY = '2026-07-26T13:00:00Z';
const LATER_STILL = '2026-07-26T18:00:00Z';

interface Workspace {
  id: string;
  title: string;
  directory: string;
  rank: string;
  pinned?: boolean;
  muted?: boolean;
}

const A: Workspace = { id: 'ws-a', title: 'A', directory: '/repo/a', rank: 'a' };
const B: Workspace = { id: 'ws-b', title: 'B', directory: '/repo/b', rank: 'b' };

interface Bands {
  chief?: string;
  turns?: string[];
  settled?: string[];
  pinned?: string[];
  crew?: string[];
  snoozed?: string[];
}

function session(id: string, workspaceId: string, fields: Partial<QueueBandSession> = {}): QueueBandSession {
  return { id, label: id, workspaceId, ...fields };
}

function owed(id: string, workspaceId: string, openedAt: string, fields: Partial<QueueBandSession> = {}): QueueBandSession {
  return session(id, workspaceId, { turnOwed: true, turnOpenedAt: openedAt, ...fields });
}

const at = (hour: number) => `2026-07-26T${String(hour).padStart(2, '0')}:00:00Z`;

const CASES: Array<[string, { workspaces?: Workspace[]; sessions: QueueBandSession[]; crewInQueue?: boolean }, Bands]> = [
  ['lists the oldest turn first, across workspaces', {
    sessions: [owed('newest', 'ws-a', at(12)), owed('oldest', 'ws-b', at(9)), owed('middle', 'ws-a', at(10))],
  }, { turns: ['oldest', 'middle', 'newest'] }],
  ['puts a new arrival below the rows already owed', {
    sessions: [owed('first', 'ws-a', at(9)), owed('second', 'ws-a', at(10)), owed('arrival', 'ws-b', at(11))],
  }, { turns: ['first', 'second', 'arrival'] }],
  ['orders by when a turn opened, whatever the agent is doing', {
    sessions: [owed('older', 'ws-a', at(9)), owed('steered', 'ws-a', at(10), { state: 'working' }), owed('newer', 'ws-a', at(11), { state: 'waiting_input' })],
  }, { turns: ['older', 'steered', 'newer'] }],
  ['ignores dispatcher fields', {
    sessions: [session('root', 'ws-a'), owed('newer', 'ws-a', at(11), { dispatcher_session_id: 'root', dispatcher_member: 'alder' }), owed('older', 'ws-b', at(9), { dispatcher_session_id: 'root' })],
  }, { turns: ['older', 'newer'], settled: ['root'] }],
  ['reads turn_owed rather than deriving it from state', {
    sessions: [session('waiting-but-settled', 'ws-a', { state: 'waiting_input', turnOwed: false }), owed('working-but-owed', 'ws-a', at(9), { state: 'working' })],
  }, { turns: ['working-but-owed'], settled: ['waiting-but-settled'] }],
  ['settles a row out of the turns into the settled band', {
    sessions: [owed('a', 'ws-a', at(9)), session('b', 'ws-a', { turnOpenedAt: at(10) }), owed('c', 'ws-a', at(11))],
  }, { turns: ['a', 'c'], settled: ['b'] }],
  ['puts everything not owed into settled', {
    sessions: [owed('owed', 'ws-a', at(9)), session('quiet', 'ws-a'), session('busy', 'ws-b', { state: 'working' })],
  }, { turns: ['owed'], settled: ['quiet', 'busy'] }],
  ['is empty when nothing is owed', {
    sessions: [session('a', 'ws-a', { state: 'working' })],
  }, { settled: ['a'] }],
  ['anchors the chief and never queues it', {
    sessions: [owed('chief', 'ws-a', at(9), { chiefOfStaff: true }), owed('agent', 'ws-a', at(10))],
  }, { chief: 'chief', turns: ['agent'] }],
  ['anchors the chief from a pinned workspace', {
    workspaces: [{ ...A, pinned: true }], sessions: [session('chief', 'ws-a', { chiefOfStaff: true })],
  }, { chief: 'chief' }],
  ['anchors the chief from a muted workspace', {
    workspaces: [{ ...A, muted: true }], sessions: [session('chief', 'ws-a', { chiefOfStaff: true })],
  }, { chief: 'chief' }],
  ['leaves automation sessions out of every band', {
    sessions: [owed('automation-run', 'ws-a', at(9), { automation: { definition_id: 'review-sol' } }), session('agent', 'ws-a')],
  }, { settled: ['agent'] }],
  ['keeps pinned and muted workspaces out of the bands', {
    workspaces: [{ ...A, pinned: true }, { ...B, muted: true }],
    sessions: [owed('pinned-owed', 'ws-a', at(9)), session('muted-quiet', 'ws-b')],
  }, {}],
  ['takes a snoozed agent into its own list', {
    sessions: [owed('owed', 'ws-a', at(9)), session('quiet', 'ws-a'), session('deferred', 'ws-b', { turnSnoozedUntil: LATER_TODAY })],
  }, { turns: ['owed'], settled: ['quiet'], snoozed: ['deferred'] }],
  ['orders snoozed agents by when they come back', {
    sessions: [session('late', 'ws-a', { turnSnoozedUntil: LATER_STILL }), session('soon', 'ws-b', { turnSnoozedUntil: LATER_TODAY })],
  }, { snoozed: ['soon', 'late'] }],
  ['returns a lapsed snooze to the settled band', {
    sessions: [session('woken', 'ws-a', { turnSnoozedUntil: at(11) })],
  }, { settled: ['woken'] }],
  ['keeps a snoozed agent out of the turns even while it is owed', {
    sessions: [owed('both', 'ws-a', at(9), { turnSnoozedUntil: LATER_TODAY })],
  }, { snoozed: ['both'] }],
  ['leaves a pinned workspace out of the snoozed list', {
    workspaces: [{ ...A, pinned: true }], sessions: [session('pinned', 'ws-a', { turnSnoozedUntil: LATER_TODAY })],
  }, {}],
  ['holds pinned sessions in pin order, out of every other band', {
    sessions: [
      owed('later', 'ws-a', at(9), { pinnedAt: at(11) }),
      session('earlier', 'ws-b', { pinnedAt: at(10) }),
      owed('unpinned', 'ws-a', at(8)),
    ],
  }, { pinned: ['earlier', 'later'], turns: ['unpinned'] }],
  ['keeps pin order whatever the pinned agents are doing', {
    sessions: [session('other', 'ws-a', { pinnedAt: at(9) }), session('held', 'ws-a', { state: 'working', pinnedAt: at(10) })],
  }, { pinned: ['other', 'held'] }],
  ['leaves a pinned session in a pinned workspace out of the bands', {
    workspaces: [{ ...A, pinned: true }], sessions: [session('both', 'ws-a', { pinnedAt: at(10) })],
  }, {}],
  ['keeps the chief out of the pinned band', {
    sessions: [session('chief', 'ws-a', { chiefOfStaff: true, pinnedAt: at(10) })],
  }, { chief: 'chief' }],
  ['lets a pin outrank a live snooze', {
    sessions: [session('both', 'ws-a', { pinnedAt: at(10), turnSnoozedUntil: '2100-01-01T00:00:00Z' })],
  }, { pinned: ['both'] }],
  ['gives no row to a shell whose agent is alive in its workspace', {
    sessions: [session('agent', 'ws-a'), session('shell', 'ws-a', { parentSessionId: 'agent' })],
  }, { settled: ['agent'] }],
  ['gives an orphaned shell its row back', {
    sessions: [session('shell', 'ws-a', { parentSessionId: 'closed-agent' })],
  }, { settled: ['shell'] }],
  ['gives a shell moved away from its agent its row back', {
    sessions: [session('agent', 'ws-a'), session('shell', 'ws-b', { parentSessionId: 'agent' })],
  }, { settled: ['agent', 'shell'] }],
  ['keeps a shell with no parent', {
    sessions: [session('shell', 'ws-a')],
  }, { settled: ['shell'] }],
  ['shows a pinned satellite in the pinned band', {
    sessions: [session('agent', 'ws-a'), session('shell', 'ws-a', { parentSessionId: 'agent', pinnedAt: at(10) })],
  }, { settled: ['agent'], pinned: ['shell'] }],
  ['takes crew members out of every other band', {
    sessions: [
      owed('sess-trellis', 'ws-a', at(9), { crewMember: 'trellis' }),
      session('sess-keel', 'ws-a', { crewMember: 'keel', pinnedAt: at(8) }),
      owed('worker', 'ws-b', '2026-07-26T09:30:00Z'),
    ],
  }, { crew: ['sess-keel', 'sess-trellis'], turns: ['worker'] }],
  ['lets crew members into the queue when crew queueing is on', {
    sessions: [owed('owed', 'ws-a', at(9), { crewMember: 'trellis' }), session('settled', 'ws-a', { crewMember: 'keel' })],
    crewInQueue: true,
  }, { turns: ['owed'], settled: ['settled'], crew: ['settled', 'owed'] }],
  ['keeps a crew member from a pinned workspace', {
    workspaces: [{ ...A, pinned: true }], sessions: [session('sess-alder', 'ws-a', { crewMember: 'alder' })],
  }, { crew: ['sess-alder'] }],
  ['leaves the chief the chief when it is a crew member', {
    sessions: [session('sess-chief', 'ws-a', { chiefOfStaff: true, crewMember: 'keel' })],
  }, { chief: 'sess-chief' }],
];

describe('buildQueueBands', () => {
  it.each(CASES)('%s', (_, { workspaces = [A, B], sessions, crewInQueue }, expected) => {
    const tree = buildWorkspaceViewModels(workspaces, sessions);
    const treeBefore = JSON.stringify(tree);

    const bands = buildQueueBands(tree, { now: NOW, crewInQueue });

    expect({
      chief: bands.chief?.session.id,
      turns: bands.turns.map((row) => row.session.id),
      settled: bands.settled.map((row) => row.session.id),
      pinned: bands.pinned.map((row) => row.session.id),
      crew: bands.crew.map((row) => row.session.id),
      snoozed: bands.snoozed.map((row) => row.session.id),
    }).toEqual({ chief: undefined, turns: [], settled: [], pinned: [], crew: [], snoozed: [], ...expected });
    expect(JSON.stringify(tree)).toBe(treeBefore);
  });
});
