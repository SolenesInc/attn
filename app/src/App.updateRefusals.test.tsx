import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openActionMenu } from './test/appFixtures';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const REFUSAL = 'The repository /work/attn is gone. Run make install from a checkout.';
const SELF_UPDATE = { self_update_enabled: 'true', update_install_kind: 'source' };

function refuseUpdates(daemon: ScriptedDaemon) {
  daemon.on('start_update', () => ({ event: 'start_update_result', success: false, error: REFUSAL }));
  daemon.on('decline_update', () => ({ event: 'decline_update_result', success: false, error: REFUSAL }));
}

describe('refused update actions', () => {
  it('shows a refused Update attn from the command palette', async () => {
    const { daemon } = await renderApp({ initialState: { settings: SELF_UPDATE }, script: refuseUpdates });
    const palette = await openActionMenu(daemon);
    await gesture(daemon, () => fireEvent.change(palette, { target: { value: '>Update attn' } }));
    await gesture(daemon, () => fireEvent.keyDown(palette, { key: 'Enter' }));

    expect(screen.getByText(REFUSAL)).toBeInTheDocument();
  });

  it('shows a refused Retry on a failed update', async () => {
    const { daemon } = await renderApp({
      initialState: {
        settings: SELF_UPDATE,
        update_run: { id: 'run-1', install_kind: 'source', log_path: '/tmp/run-1.log', outcome: 'The build failed.', phase: 'failed', requesters: [], outposts: [], target_build: 'abc' },
      },
      script: refuseUpdates,
    });
    await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('update-run-banner')).getByRole('button', { name: 'Retry' })));

    expect(screen.getByText(REFUSAL)).toBeInTheDocument();
  });

  it.each(['Update', 'Decline'])('shows a refused %s on an update request notification', async (label) => {
    const { daemon } = await renderApp({ initialState: { settings: SELF_UPDATE }, script: refuseUpdates });
    daemon.on('notification_list', () => ({
      event: 'notification_list_result',
      success: true,
      unread_count: 1,
      unread_critical_count: 0,
      notifications: [{
        id: 'update_requested:r1', kind: 'update_requested', severity: 'info', title: 'Update attn?', body: '', detail: '',
        trigger: 'An agent asked to update attn.', impact: '', cause: '', diagnostic: '', source_kind: 'update', source_id: 'r1',
        created_at: '2026-08-15T09:00:00Z', read_at: '',
        actions: [{ kind: 'start_update', label: 'Update', target_id: 'r1' }, { kind: 'decline_update', label: 'Decline', target_id: 'r1' }],
      }],
    }));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Show Notifications' })));
    const panel = within(screen.getByRole('dialog', { name: 'Notifications' }));
    await gesture(daemon, () => fireEvent.click(panel.getByRole('button', { name: /Update attn\?/ })));
    await gesture(daemon, () => fireEvent.click(panel.getByRole('button', { name: label })));

    expect(screen.getByText(REFUSAL)).toBeInTheDocument();
  });
});
