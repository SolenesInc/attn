import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, daemonSession, daemonWorkspace, type DaemonSession } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

const WORKSPACE = 'workspace-main';
const FIRES_AT = '2999-01-01T00:00:00.000Z';

const splitWorkspace = daemonWorkspace(WORKSPACE, {
  root: {
    type: 'split',
    split_id: 'split-1',
    direction: 'horizontal',
    ratio: 0.5,
    children: [
      { type: 'pane', pane_id: 'pane-target' },
      { type: 'pane', pane_id: 'pane-other' },
    ],
  },
  panes: [agentPane('target', WORKSPACE), agentPane('other', WORKSPACE)],
});

async function renderNudge(target: Partial<DaemonSession>, { selected }: { selected: boolean }) {
  const view = await renderApp({
    initialState: {
      sessions: [
        daemonSession('target', { workspace_id: WORKSPACE, state: 'idle', ...target }),
        daemonSession('other', { workspace_id: WORKSPACE, state: 'idle' }),
      ],
      workspaces: [splitWorkspace],
    },
  });
  await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: `Open ${selected ? 'target' : 'other'}` })));
  return view.daemon;
}

const header = () => within(document.querySelector<HTMLElement>('[data-pane-id="pane-target"]')!);
const sidebarDeliver = () => screen.queryByRole('button', { name: 'Deliver the pending ticket nudge now' });
const counting = () => header().queryByRole('button', { name: /Incoming ticket nudge/ });
const deliverNow = () => header().queryByRole('button', { name: 'Deliver ticket nudge now' });

function shown(): 'none' | 'counting' | 'paused' | 'marker' {
  if (counting()) return 'counting';
  if (deliverNow()) return 'paused';
  if (header().queryByText('Unread ticket activity')) return 'marker';
  return 'none';
}

describe('App ticket nudge', () => {
  it.each([
    ['idle', false, false, undefined, 'none'],
    ['working', true, false, undefined, 'none'],
    ['idle', false, true, FIRES_AT, 'counting'],
    ['idle', true, true, FIRES_AT, 'paused'],
    ['waiting_input', true, true, undefined, 'paused'],
    ['working', true, true, undefined, 'paused'],
    ['working', false, true, undefined, 'marker'],
    ['pending_approval', true, true, undefined, 'marker'],
    ['unknown', true, true, undefined, 'paused'],
    ['unknown', false, true, undefined, 'marker'],
    ['idle', false, true, undefined, 'marker'],
  ] as const)('a %s session, selected %s, unread %s, counting to %s, shows %s', async (state, selected, unread, firesAt, expected) => {
    await renderNudge({ state, ticket_unread: unread, nudge_fires_at: firesAt }, { selected });

    expect(shown()).toBe(expected);
    expect(sidebarDeliver() !== null).toBe(expected === 'paused');
  });

  it('delivers from the sidebar strip without selecting or dragging the row', async () => {
    const daemon = await renderNudge({ ticket_unread: true }, { selected: true });
    const before = daemon.sent.length;

    fireEvent.pointerDown(sidebarDeliver()!);
    await gesture(daemon, () => fireEvent.click(sidebarDeliver()!));

    expect(daemon.sent.slice(before)).toEqual([{ cmd: 'trigger_nudge', session_id: 'target' }]);
  });

  it('delivers from the pane header without focusing the pane', async () => {
    const daemon = await renderNudge({ ticket_unread: true }, { selected: true });
    const before = daemon.sent.length;

    fireEvent.pointerDown(deliverNow()!);
    await gesture(daemon, () => fireEvent.click(deliverNow()!));

    expect(daemon.sent.slice(before)).toEqual([{ cmd: 'trigger_nudge', session_id: 'target' }]);
  });

  it('stops a counting nudge from its chip, naming the key that does the same, and never delivers it', async () => {
    const daemon = await renderNudge({ ticket_unread: true, nudge_fires_at: FIRES_AT }, { selected: false });
    expect(counting()).toHaveTextContent('⌘.');
    expect(counting()).toHaveTextContent('stop');
    const before = daemon.sent.length;

    fireEvent.pointerDown(counting()!);
    await gesture(daemon, () => fireEvent.click(counting()!));

    expect(daemon.sent.slice(before)).toEqual([{ cmd: 'cancel_countdown', session_id: 'target' }]);
  });
});
