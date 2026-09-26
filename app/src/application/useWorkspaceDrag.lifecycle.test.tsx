import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, daemonSession, daemonWorkspace } from '../test/daemonFixtures';
import { renderApp } from '../test/renderApp';

const SOURCE = 'ws-source';
const TARGET = 'ws-target';

async function renderTwoWorkspaces() {
  return renderApp({
    initialState: {
      sessions: [
        daemonSession('s1', { workspace_id: SOURCE }),
        daemonSession('s2', { workspace_id: SOURCE }),
        daemonSession('s3', { workspace_id: TARGET }),
      ],
      workspaces: [
        daemonWorkspace(SOURCE, {
          root: {
            type: 'split',
            split_id: 'source-split',
            direction: 'horizontal',
            ratio: 0.5,
            children: [{ type: 'pane', pane_id: 'pane-s1' }, { type: 'pane', pane_id: 'pane-s2' }],
          },
          panes: [agentPane('s1', SOURCE), agentPane('s2', SOURCE)],
        }),
        daemonWorkspace(TARGET, {
          root: { type: 'pane', pane_id: 'pane-s3' },
          panes: [agentPane('s3', TARGET)],
        }),
      ],
    },
  });
}

function pickUp(label: string) {
  fireEvent.pointerDown(screen.getByRole('button', { name: `Open ${label}` }), { button: 0, pointerId: 1, clientX: 0, clientY: 0 });
  fireEvent.pointerMove(window, { pointerId: 1, clientX: 40, clientY: 40 });
}

describe('workspace drag lifecycle', () => {
  it('keeps a new gesture when the preceding drag has a deferred end', async () => {
    const { daemon } = await renderTwoWorkspaces();
    await daemon.idle();

    pickUp('s1');
    fireEvent.pointerUp(window, { pointerId: 1 });
    pickUp('s2');
    await act(() => vi.advanceTimersByTimeAsync(0));

    const target = screen.getByTestId(`sidebar-workspace-${TARGET}`);
    fireEvent.pointerOver(target, { pointerId: 1 });
    fireEvent.pointerUp(target, { pointerId: 1 });
    await daemon.idle();

    expect(daemon.sentOf('workspace_layout_move_leaf_to_workspace')).toEqual([
      expect.objectContaining({ source_workspace_id: SOURCE, target_workspace_id: TARGET, leaf_id: 'pane-s2' }),
    ]);
  });
});
