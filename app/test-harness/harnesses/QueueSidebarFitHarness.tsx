import { useEffect } from 'react';
import { Sidebar } from '../../src/components/Sidebar';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop } from '../../src/types/generated';
import { buildDesktopViewModels } from '../../src/utils/desktopViewModels';
import { buildQueueBands } from '../../src/utils/queueBands';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const noop = () => {};
const sessions = [
  { id: 'chief', label: 'chief', state: 'idle' as const, chiefOfStaff: true },
  { id: 'crew-awake', label: 'awake crew', state: 'working' as const, crewMember: 'alder' },
  ...Array.from({ length: 5 }, (_, index) => ({
    id: `owed-${index + 1}`,
    label: `waiting agent ${index + 1}`,
    state: 'waiting_input' as const,
    turnOwed: true,
    turnOpenedAt: `2026-09-29T0${index}:00:00Z`,
  })),
  { id: 'working', label: 'working agent', state: 'working' as const },
  { id: 'snoozed-1', label: 'snoozed agent 1', state: 'working' as const, turnSnoozedUntil: '2099-01-01T09:00:00Z' },
  { id: 'snoozed-2', label: 'snoozed agent 2', state: 'working' as const, turnSnoozedUntil: '2099-01-02T09:00:00Z' },
];

const desktop: Desktop = {
  id: 'desk-1',
  profile_id: 'profile-harness',
  name: '',
  shortcut_slot: 1,
  order_key: 'desk-1',
  tree_json: JSON.stringify({ type: 'pane', pane_id: 'pane-chief' }),
  active_pane_id: 'pane-chief',
  revision: 1,
  panes: sessions.map((session) => ({
    pane_id: `pane-${session.id}`,
    desktop_id: 'desk-1',
    session_id: session.id,
    kind: LayoutPaneKind.Agent,
    title: session.label,
    status: LayoutPaneStatus.Ready,
  })),
};
const desktops = buildDesktopViewModels([desktop], sessions);

export function QueueSidebarFitHarness({ onReady, setTriggerRerender }: HarnessProps) {
  useEffect(() => {
    onReady();
    setTriggerRerender(() => noop);
  }, [onReady, setTriggerRerender]);
  return (
    <div className="app" style={{ height: '100vh' }}>
      <div className="app-frame">
        <Sidebar
          collapsed={false}
          surface={new URLSearchParams(window.location.search).has('desktop') ? 'tree-open' : 'queue-open'}
          selectedId={null}
          selectedDesktopId="desk-1"
          headerActions={[]}
          desktops={desktops}
          visualIndexByDesktopId={new Map([['desk-1', 0]])}
          queue={buildQueueBands(desktops)}
          crew={[{ id: 'alder', binding_session: 'crew-awake' }, { id: 'birch' }, { id: 'cedar' }]}
          profileName="Harness"
          onSelectSession={noop}
          onWakeCrewMember={(member) => window.__HARNESS__.recordCall('wake', [member])}
          onSelectDesktop={noop}
          onNewSession={noop}
          onCloseSession={noop}
          onReloadSession={noop}
          onGoToDashboard={noop}
          onToggleCollapse={noop}
        />
      </div>
    </div>
  );
}
