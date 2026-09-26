import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, agentWorkspace, daemonSession, daemonWorkspace, type DaemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const ORCHESTRATOR = { name: 'Orchestrator', builtin: 'orchestrator' } as const;
const BUILDER = { name: 'Builder', builtin: 'builder' } as const;

const team = [
  daemonSession('root', { label: 'Coordinate identity', delegation_role: ORCHESTRATOR }),
  daemonSession('build', { label: 'Build navigator', agent: 'codex', dispatcher_session_id: 'root', delegation_role: BUILDER }),
  daemonSession('review', { label: 'Review behavior', agent: 'codex', dispatcher_session_id: 'root' }),
];

function renderTeam(sessions: DaemonSession[] = team) {
  return renderApp({ initialState: { sessions, workspaces: sessions.map((session) => agentWorkspace(session.id)) } });
}

const chain = () => screen.queryByRole('dialog', { name: 'Delegation chain' });
const trigger = (label: string) => screen.getAllByRole('button', { name: new RegExp(`Show delegation chain for ${label}$`) })[0];

function chainRows() {
  return within(within(chain()!).getByRole('list', { name: 'Agents in delegation chain' })).getAllByRole('listitem')
    .map((item) => [within(item).getByRole('button').textContent?.match(/^[↳]?(.*?)(Claude|Codex)/)?.[1] ?? '', item.getAttribute('aria-level')]);
}

describe('App delegation chain', () => {
  it('marks a session with a standalone role, and leaves an unrelated roleless session unmarked', async () => {
    await renderTeam([
      daemonSession('lead', { label: 'Coordinate identity', delegation_role: ORCHESTRATOR }),
      daemonSession('plain', { label: 'plain' }),
    ]);

    expect(trigger('Coordinate identity')).toHaveAccessibleName('Orchestrator · Show delegation chain for Coordinate identity');
    expect(screen.queryByRole('button', { name: /Show delegation chain for plain/ })).toBeNull();
  });

  it('offers the chain on a dispatcher in the queue because of the delegates it sent', async () => {
    await renderApp({
      initialState: {
        settings: { queue_mode_enabled: 'true' },
        sessions: [
          daemonSession('root', { label: 'root session' }),
          daemonSession('child', { label: 'child', dispatcher_session_id: 'root' }),
          daemonSession('alone', { label: 'alone' }),
        ],
        workspaces: ['root', 'child', 'alone'].map(agentWorkspace),
      },
    });
    const queue = within(screen.getByTestId('sidebar-queue'));

    expect(queue.getByRole('button', { name: 'Show delegation chain for root session' })).toBeInTheDocument();
    expect(queue.queryByRole('button', { name: 'Show delegation chain for alone' })).toBeNull();
  });

  it('walks the whole connected tree from its root, whichever agent it is opened from', async () => {
    await renderTeam([
      daemonSession('root', { label: 'root session' }),
      daemonSession('child', { label: 'child', dispatcher_session_id: 'root' }),
      daemonSession('grandchild', { label: 'grandchild', dispatcher_session_id: 'child' }),
      daemonSession('peer', { label: 'peer', dispatcher_session_id: 'root' }),
      daemonSession('unrelated', { label: 'unrelated' }),
    ]);

    fireEvent.click(trigger('grandchild'));

    expect(chainRows()).toEqual([['root session', '1'], ['child', '2'], ['grandchild', '3'], ['peer', '2']]);
  });

  it('names a dispatcher that has ended as unavailable text rather than a session to open', async () => {
    await renderTeam([
      daemonSession('child', { label: 'child', dispatcher_session_id: 'ended', dispatcher_member: 'alder' }),
      daemonSession('peer', { label: 'peer', dispatcher_session_id: 'ended', dispatcher_member: 'alder' }),
    ]);

    fireEvent.click(trigger('child'));

    expect(within(chain()!).getByText('↑ Alder · unavailable')).toBeInTheDocument();
    expect(within(chain()!).queryByRole('button', { name: /Alder/ })).toBeNull();
    expect(chainRows().map(([label]) => label)).toEqual(['child', 'peer']);
  });

  it('never crosses to another endpoint or loops around a dispatch cycle', async () => {
    await renderTeam([
      daemonSession('a', { label: 'first', dispatcher_session_id: 'b' }),
      daemonSession('b', { label: 'second', dispatcher_session_id: 'a' }),
      daemonSession('remote', { label: 'remote dispatcher', endpoint_id: 'ep-1' }),
      daemonSession('c', { label: 'local delegate', dispatcher_session_id: 'remote' }),
    ]);

    fireEvent.click(trigger('second'));
    expect(chainRows().map(([label]) => label).sort()).toEqual(['first', 'second']);

    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.click(trigger('local delegate'));
    expect(chainRows().map(([label]) => label)).toEqual(['local delegate']);
  });

  it('takes the user from a delegate to the dispatcher that is still running, and nowhere once it has ended', async () => {
    const { daemon } = await renderTeam([
      daemonSession('root', { label: 'root session' }),
      daemonSession('child', { label: 'child', dispatcher_session_id: 'root' }),
      daemonSession('orphan', { label: 'orphan', dispatcher_session_id: 'ended', dispatcher_member: 'alder' }),
    ]);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /^Open child/ })));

    await gesture(daemon, () => pressShortcut('session.orchestrator'));
    expect(daemon.sentOf('session_selected').pop()).toEqual({ cmd: 'session_selected', id: 'root' });

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /^Open orphan/ })));
    await gesture(daemon, () => pressShortcut('session.orchestrator'));
    expect(daemon.sentOf('session_selected').pop()).toEqual({ cmd: 'session_selected', id: 'orphan' });
  });

  it('does not reopen under a pointer that has not moved since the user dismissed it', async () => {
    await renderTeam();
    const builder = trigger('Build navigator');
    fireEvent.pointerMove(window, { clientX: 80, clientY: 40 });
    fireEvent.pointerEnter(builder);
    expect(chain()).not.toBeNull();

    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    fireEvent.pointerEnter(builder);
    expect(chain()).toBeNull();

    fireEvent.pointerMove(window, { clientX: 81, clientY: 40 });
    fireEvent.pointerEnter(builder);
    expect(chain()).not.toBeNull();
  });

  describe('beside a terminal pane', () => {
    const workspace = daemonWorkspace('ws', {
      root: {
        type: 'split',
        split_id: 'split-1',
        direction: 'vertical',
        ratio: 0.5,
        children: [{ type: 'pane', pane_id: 'pane-root' }, { type: 'pane', pane_id: 'pane-build' }],
      },
      panes: [agentPane('root', 'ws'), agentPane('build', 'ws')],
    }, { title: 'ws' });

    async function renderPanes(): Promise<ScriptedDaemon> {
      const { daemon } = await renderApp({
        initialState: { sessions: team.slice(0, 2).map((session) => ({ ...session, workspace_id: 'ws' })), workspaces: [workspace] },
      });
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /^Open Coordinate identity/ })));
      return daemon;
    }

    const buildPane = () => document.querySelector<HTMLElement>('[data-pane-id="pane-build"]')!;

    it('keeps presses on the pane header’s chain and on the chain itself from selecting the pane', async () => {
      const daemon = await renderPanes();
      const selectedBefore = daemon.sentOf('session_selected').length;
      const headerTrigger = within(buildPane()).getByRole('button', { name: /Show delegation chain for Build navigator/ });

      fireEvent.mouseDown(headerTrigger);
      await gesture(daemon, () => fireEvent.click(headerTrigger));
      const current = within(chain()!).getByRole('button', { name: /Build navigator/ });
      expect(current).toHaveFocus();
      await gesture(daemon, () => fireEvent.mouseDown(current));

      expect(daemon.sentOf('session_selected').slice(selectedBefore)).toEqual([]);
    });

    it('closes on a pointer press in the terminal pane', async () => {
      const daemon = await renderPanes();
      fireEvent.click(within(buildPane()).getByRole('button', { name: /Show delegation chain for Build navigator/ }));
      expect(chain()).not.toBeNull();

      await gesture(daemon, () => fireEvent.pointerDown(document.querySelector<HTMLElement>('[data-pane-id="pane-root"]')!));

      expect(chain()).toBeNull();
    });
  });
});
