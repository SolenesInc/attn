import { describe, expect, it } from 'vitest';
import {
  buildQueueBands,
  headOfQueue,
  type QueueBandSession,
} from './queueBands';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop } from '../types/generated';
import { buildDesktopViewModels } from './desktopViewModels';

type PlacedSession = QueueBandSession & { desktopId?: string };

function desktop(id: string, slot: number, sessions: PlacedSession[]): Desktop {
  const holds = sessions.filter((session) => session.desktopId === id);
  return {
    id,
    profile_id: 'profile',
    name: '',
    shortcut_slot: slot,
    order_key: id,
    tree_json: '',
    active_pane_id: '',
    revision: 1,
    panes: holds.map((session) => ({
      pane_id: `pane-${session.id}`,
      desktop_id: id,
      session_id: session.id,
      kind: LayoutPaneKind.Agent,
      title: session.label,
      status: LayoutPaneStatus.Ready,
    })),
  };
}

function views(sessions: PlacedSession[]) {
  return buildDesktopViewModels([desktop('ws-a', 1, sessions), desktop('ws-b', 2, sessions)], sessions);
}

describe('buildQueueBands', () => {

  it('lists the oldest turn first, across desktops', () => {
    const bands = buildQueueBands(views([
      { id: 'newest', label: 'newest', desktopId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T12:00:00Z' },
      { id: 'oldest', label: 'oldest', desktopId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'middle', label: 'middle', desktopId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T10:00:00Z' },
    ]));

    expect(bands.turns.map((row) => row.session.id)).toEqual(['oldest', 'middle', 'newest']);
    expect(bands.turns.map((row) => row.desktopTitle)).toEqual(['Desktop 2', 'Desktop 1', 'Desktop 1']);
  });

  it('leaves the desktop tree untouched — it is not an output of the queue', () => {
    const sessions: PlacedSession[] = [
      { id: 'a', label: 'a', desktopId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'b', label: 'b', desktopId: 'ws-b' },
    ];
    const tree = views(sessions);
    buildQueueBands(tree);

    expect(tree.map((desktop) => desktop.sessions.map((session) => session.id))).toEqual([['a'], ['b']]);
  });

});

describe('headOfQueue', () => {
  it('is the turn owed longest, not the first one listed by the desktop tree', () => {
    const bands = buildQueueBands(views([
      { id: 'newer', label: 'newer', desktopId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T12:00:00Z' },
      { id: 'older', label: 'older', desktopId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
    ]));

    expect(headOfQueue(bands)?.session.id).toBe('older');
  });

});

describe('satellite shells', () => {
  it('gives no row to a shell whose agent is alive in the same desktop', () => {
    const bands = buildQueueBands(views([
      { id: 'agent', label: 'agent', desktopId: 'ws-a' },
      { id: 'shell', label: 'shell', desktopId: 'ws-a', parentSessionId: 'agent' },
    ]));

    expect(bands.settled.map((row) => row.session.id)).toEqual(['agent']);
  });

});
