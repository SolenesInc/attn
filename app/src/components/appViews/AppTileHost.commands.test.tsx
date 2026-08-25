import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { Button, useCommand } from '@victorarias/attn-app';
import { AppTileHost } from './AppTileHost';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useDaemonStore } from '../../store/daemonSessions';
import type { AppRegistryEntry } from '../../hooks/useDaemonSocket';


const loadAppView = vi.hoisted(() => vi.fn());
vi.mock('./loadAppView', async () => {
  const actual = await vi.importActual<typeof import('./loadAppView')>('./loadAppView');
  return { ...actual, loadAppView };
});

function ActingView() {
  const approve = useCommand('approve');
  return (
    <>
      <Button onClick={() => approve({ id: 'tk-1' })}>Approve</Button>
      {approve.error && <div data-testid="command-error">{approve.error}</div>}
    </>
  );
}

function renderHost(sendAppCommand: DaemonApi['sendAppCommand']) {
  loadAppView.mockResolvedValue(ActingView);
  act(() => {
    useDaemonStore.getState().setApps([{
      name: 'reviewer',
      enabled: true,
      version_id: 7,
      content_hash: 'a'.repeat(64),
      views: [{ name: 'approvals', kind: 'tile', title: 'Pending approvals' }],
    } as AppRegistryEntry]);
  });
  const api = { sendAppCommand, sendAppViewCrash: vi.fn() } as unknown as DaemonApi;
  render(
    <DaemonApiProvider api={api}>
      <AppTileHost
        app="reviewer"
        view="approvals"
        workspaceId="ws-1"
        sessionId={null}
        tileId="tile-7"
        params=""
      />
    </DaemonApiProvider>,
  );
}

beforeEach(() => {
  loadAppView.mockReset();
  act(() => {
    useDaemonStore.getState().setApps([]);
  });
});

describe('a view invoking a command', () => {
  it('is addressed to the app the host mounted, not to one the view named', async () => {
    const sendAppCommand = vi.fn().mockResolvedValue({ approved: true });
    renderHost(sendAppCommand);
    const button = await screen.findByRole('button', { name: 'Approve' });

    // The runner settles its own pending state after the answer, so asserting
    // before the await is asserting mid-update.
    await act(async () => {
      fireEvent.click(button);
    });

    expect(sendAppCommand).toHaveBeenCalledWith('reviewer', 'approve', { id: 'tk-1' });
  });

  it('shows the daemon’s own refusal instead of throwing it away', async () => {
    const sendAppCommand = vi.fn().mockRejectedValue(
      new Error('reviewer is disabled, so it runs nothing; `attn app enable reviewer` turns it back on'),
    );
    renderHost(sendAppCommand);

    fireEvent.click(await screen.findByRole('button', { name: 'Approve' }));

    const shown = await screen.findByTestId('command-error');
    expect(shown.textContent).toContain('attn app enable reviewer');
  });
});
