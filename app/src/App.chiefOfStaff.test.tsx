import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

describe('App chief of staff', () => {
  it('moves the role after the user confirms, and closes the prompt once the daemon agrees', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('chief', { chief_of_staff: true }), daemonSession('s1', { state: 'idle' })],
        workspaces: [agentWorkspace('chief'), agentWorkspace('s1')],
      },
    });

    fireEvent.click(screen.getByRole('button', { name: 'Actions for s1' }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Make chief of staff/ }));
    expect(screen.getByRole('dialog', { name: 'Transfer chief of staff role?' })).toHaveTextContent(
      'chief currently holds the role. Move it to s1?',
    );

    fireEvent.click(screen.getByRole('button', { name: 'Transfer role' }));
    const request = await daemon.received('set_chief_of_staff');
    expect(request).toEqual({ cmd: 'set_chief_of_staff', session_id: 's1', chief_of_staff: true });
    expect(screen.getByRole('button', { name: 'Transferring…' })).toBeInTheDocument();

    daemon.emit({ event: 'chief_of_staff_result', session_id: 's1', chief_of_staff: true, success: true });
    await daemon.idle();

    expect(screen.queryByRole('dialog', { name: 'Transfer chief of staff role?' })).toBeNull();
  });
});
