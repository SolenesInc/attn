import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, daemonSession, daemonWorkspace } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

// One session shown by two panes, each pane its own terminal.
async function renderSharedSession() {
  const id = 'workspace-s1';
  const panes = [
    { ...agentPane('s1', id, 'terminal-left'), pane_id: 'pane-left' },
    { ...agentPane('s1', id, 'terminal-right'), pane_id: 'pane-right' },
  ];
  const { daemon } = await renderApp({
    initialState: {
      sessions: [daemonSession('s1')],
      workspaces: [daemonWorkspace(id, {
        root: {
          type: 'split', split_id: 'split-s1', direction: 'vertical', ratio: 0.5,
          children: [{ type: 'pane', pane_id: 'pane-left' }, { type: 'pane', pane_id: 'pane-right' }],
        },
        panes,
      }, { title: 's1', directory: '/tmp/s1' })],
    },
  });
  await daemon.idle();
  return daemon;
}

function sessionActions(item: RegExp) {
  fireEvent.click(screen.getByRole('button', { name: 'Actions for s1' }));
  fireEvent.click(screen.getByRole('menuitem', { name: item }));
}

describe('a session shown in several panes', () => {
  it('closes nothing when a terminal exits after its pane is gone', async () => {
    const daemon = await renderSharedSession();

    daemon.emit({ event: 'session_exited', id: 'terminal-gone', session_id: 's1', exit_code: 0 });
    await daemon.idle();

    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([]);
    expect(daemon.sentOf('unregister')).toEqual([]);
  });

  it('closes only the pane whose terminal exited', async () => {
    const daemon = await renderSharedSession();

    daemon.emit({ event: 'session_exited', id: 'terminal-right', session_id: 's1', exit_code: 0 });
    await daemon.idle();

    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([
      expect.objectContaining({ workspace_id: 'workspace-s1', pane_id: 'pane-right' }),
    ]);
  });

  it('closes the whole session from the sidebar', async () => {
    const daemon = await renderSharedSession();

    sessionActions(/Close session/);
    await daemon.idle();

    expect(daemon.sentOf('unregister')).toEqual([expect.objectContaining({ id: 's1' })]);
    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([]);
  });

  it('reloads the terminal of the pane it reloads from', async () => {
    const daemon = await renderSharedSession();
    daemon.on('reload_session', () => undefined);

    sessionActions(/Reload session/);
    await daemon.idle();

    expect(daemon.sentOf('reload_session')).toEqual([
      expect.objectContaining({ id: 's1', terminal: 'terminal-left' }),
    ]);
  });
});
