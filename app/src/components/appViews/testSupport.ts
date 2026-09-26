import { fireEvent, screen } from '@testing-library/react';
import { agentPane, daemonSession, daemonWorkspace } from '../../test/daemonFixtures';
import { gesture, renderApp } from '../../test/renderApp';
import type { EventMessage } from '../../test/protocol';

type AppEntry = EventMessage<'apps_updated'>['apps'][number];

export const SERVING_HASH = 'a'.repeat(64);

export function reviewerApp(overrides: Partial<AppEntry> = {}): AppEntry {
  return {
    name: 'reviewer',
    enabled: true,
    version_id: 7,
    content_hash: SERVING_HASH,
    views: [{ name: 'approvals', kind: 'tile', title: 'Pending approvals' }],
    ...overrides,
  };
}

const dockedApprovals = daemonWorkspace(
  'ws-1',
  {
    root: {
      type: 'split',
      split_id: 'split-1',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'pane', pane_id: 'pane-sess-1' },
        { type: 'tile', tile_id: 'tile-7', tile_kind: 'app:reviewer/approvals', tile_params: 't-42' },
      ],
    },
    panes: [agentPane('sess-1', 'ws-1')],
  },
  { title: 'reviewing' },
);

export async function openDockedApprovals(apps: AppEntry[]) {
  const { daemon } = await renderApp({
    initialState: {
      apps,
      sessions: [daemonSession('sess-1', { label: 'reviewing', workspace_id: 'ws-1' })],
      workspaces: [dockedApprovals],
    },
  });
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open reviewing' })));
  return daemon;
}
