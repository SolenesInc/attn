import { useEffect } from 'react';
import { Sidebar } from '../../src/components/Sidebar';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop } from '../../src/types/generated';
import { buildQueueBands } from '../../src/utils/queueBands';
import { buildDesktopViewModels } from '../../src/utils/workspaceViewModels';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const noop = () => {};
const long = (words: string) => `${words}-${'and-then-some-more-'.repeat(4)}end`;

const params = new URLSearchParams(window.location.search);
const underGrid = params.has('grid');
const desktopIds = Array.from({ length: params.has('oneDesktop') ? 1 : 11 }, (_, index) => `desk-${index + 1}`);

const sessions = [
  ...[0, 1, 2, 3].map((index) => ({
    id: `owed-${index}`,
    label: long(`an-agent-with-a-long-label-${index}`),
    state: 'waiting_input' as const,
    workspaceId: desktopIds[index % desktopIds.length],
    turnOwed: true,
    turnOpenedAt: `2026-09-26T0${index}:00:00Z`,
  })),
  ...[0, 1].map((index) => ({
    id: `run-${index}`,
    label: `run #${index}`,
    state: 'waiting_input' as const,
    workspaceId: desktopIds[Math.min(5, desktopIds.length - 1)],
    turnOwed: true,
    turnOpenedAt: `2026-09-26T0${index}:30:00Z`,
    automation: {
      run_id: `run-${index}`,
      definition_id: 'long-automation',
      definition_name: long('an-automation-with-an-unbroken-name'),
      trigger_type: 'schedule',
    },
  })),
];

function desktop(id: string, slot: number): Desktop {
  const held = sessions.filter((session) => session.workspaceId === id);
  return {
    id,
    profile_id: 'profile-harness',
    name: '',
    ...(slot <= 9 ? { shortcut_slot: slot } : {}),
    order_key: id,
    tree_json: held[0] ? JSON.stringify({ type: 'pane', pane_id: `pane-${held[0].id}` }) : '',
    active_pane_id: held[0] ? `pane-${held[0].id}` : '',
    revision: 1,
    panes: held.map((session) => ({
      pane_id: `pane-${session.id}`,
      desktop_id: id,
      session_id: session.id,
      kind: LayoutPaneKind.Agent,
      title: session.label,
      status: LayoutPaneStatus.Ready,
    })),
  };
}

const workspaces = buildDesktopViewModels(desktopIds.map((id, index) => desktop(id, index + 1)), sessions);

export function QueueBarHarness({ onReady, setTriggerRerender }: HarnessProps) {
  useEffect(() => {
    onReady();
    setTriggerRerender(() => noop);
  }, [onReady, setTriggerRerender]);
  return (
    <div className={`app${underGrid ? ' is-grid' : ''}`} style={{ height: '100vh' }}>
      <div className="app-frame">
        <Sidebar
          collapsed
          selectedId={null}
          selectedWorkspaceId={desktopIds[0]}
          headerActions={[]}
          workspaces={workspaces}
          visualIndexByWorkspaceId={new Map(desktopIds.slice(0, 9).map((id, index) => [id, index]))}
          queue={buildQueueBands(workspaces)}
          instance="harness"
          profileName={long('A-very-long-profile-name-typed-without-spaces').repeat(params.has('oneDesktop') ? 6 : 1)}
          criticalNotifications={{ count: 3, title: long('a-critical-notification-title') }}
          onOpenNotifications={noop}
          onSelectSession={noop}
          onSelectWorkspace={noop}
          onNewSession={noop}
          onCloseSession={noop}
          onReloadSession={noop}
          onGoToDashboard={noop}
          onToggleCollapse={noop}
          onWalkRuns={noop}
        />
      </div>
      {underGrid && <div className="view-container visible" data-testid="grid-stand-in" />}
    </div>
  );
}
