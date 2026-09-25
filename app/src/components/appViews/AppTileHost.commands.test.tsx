import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import { Button, useCommand } from '@victorarias/attn-app';
import { AppTileHost } from './AppTileHost';
import { renderWithDaemon } from '../../test/renderApp';
import type { Reply } from '../../test/scriptedDaemon';

const loadAppView = vi.hoisted(() => vi.fn());
vi.mock('./loadAppView', async () => {
  const actual = await vi.importActual<typeof import('./loadAppView')>('./loadAppView');
  return { ...actual, loadAppView };
});

function ActingView() {
  const approve = useCommand('approve');
  const refresh = useCommand('refresh');
  const [answer, setAnswer] = useState('none yet');
  const show = (outcome: Awaited<ReturnType<typeof approve>>) => {
    if (outcome.ok) setAnswer(JSON.stringify(outcome.value) ?? 'nothing');
  };
  return (
    <>
      <Button onClick={() => approve({ id: 'tk-1' }).then(show)}>Approve</Button>
      <Button onClick={() => refresh().then(show)}>Refresh</Button>
      <div data-testid="command-answer">{answer}</div>
      {approve.error && <div data-testid="command-error">{approve.error}</div>}
    </>
  );
}

async function renderHost(answer: Reply) {
  loadAppView.mockResolvedValue(ActingView);
  const { daemon } = await renderWithDaemon(
    <AppTileHost
      app="reviewer"
      view="approvals"
      workspaceId="ws-1"
      sessionId={null}
      tileId="tile-7"
      params=""
    />,
    {
      initialState: {
        apps: [{
          name: 'reviewer',
          enabled: true,
          version_id: 7,
          content_hash: 'a'.repeat(64),
          views: [{ name: 'approvals', kind: 'tile', title: 'Pending approvals' }],
        }],
      },
    },
  );
  daemon.on('app_command', () => answer);
  await daemon.idle();
  return daemon;
}

beforeEach(() => {
  loadAppView.mockReset();
});

describe('a view invoking a command', () => {
  it('is addressed to the app the host mounted, not to one the view named', async () => {
    const daemon = await renderHost({ event: 'app_command_result', success: true, payload: JSON.stringify({ approved: true }) });

    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    await daemon.idle();

    expect(daemon.sentOf('app_command')).toEqual([
      expect.objectContaining({ app: 'reviewer', command: 'approve', payload: JSON.stringify({ id: 'tk-1' }) }),
    ]);
    expect(screen.getByTestId('command-answer')).toHaveTextContent('{"approved":true}');
    expect(screen.queryByTestId('command-error')).not.toBeInTheDocument();
  });

  it('carries no payload for a command that takes none, and answers nothing when the handler returns nothing', async () => {
    const daemon = await renderHost({ event: 'app_command_result', success: true });

    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await daemon.idle();

    const [sent] = daemon.sentOf('app_command');
    expect(sent).toMatchObject({ app: 'reviewer', command: 'refresh' });
    expect(sent.payload).toBeUndefined();
    expect(screen.getByTestId('command-answer')).toHaveTextContent('nothing');
  });

  it('shows the daemon’s own refusal instead of throwing it away', async () => {
    const daemon = await renderHost({
      event: 'app_command_result',
      success: false,
      error: 'reviewer is disabled, so it runs nothing; `attn app enable reviewer` turns it back on',
    });

    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    await daemon.idle();

    expect(screen.getByTestId('command-error').textContent).toContain('attn app enable reviewer');
  });
});
