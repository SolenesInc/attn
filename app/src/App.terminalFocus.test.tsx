import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import { WARM_WORKSPACE_LIMIT_STORAGE_KEY } from './utils/terminalVirtualization';

function renderSessions() {
  localStorage.setItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY, '0');
  return renderApp({
    initialState: {
      sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' })],
      workspaces: [agentWorkspace('s1'), agentWorkspace('s2')],
    },
  });
}

describe('App terminal focus', () => {
  it('gives the opened session’s terminal the keyboard once it is ready, when nothing else holds it', async () => {
    const { daemon } = await renderSessions();

    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    (document.activeElement as HTMLElement).blur();
    await daemon.idle();

    expect(screen.getByRole('textbox', { name: 'Terminal input' })).toHaveFocus();
  });

  it('leaves the keyboard on a control the user moved to before the terminal was ready', async () => {
    const { daemon } = await renderSessions();

    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    const otherRow = screen.getByRole('button', { name: 'Open s2' });
    otherRow.focus();
    await daemon.idle();

    expect(screen.getByRole('textbox', { name: 'Terminal input' })).toBeInTheDocument();
    expect(otherRow).toHaveFocus();
  });
});
