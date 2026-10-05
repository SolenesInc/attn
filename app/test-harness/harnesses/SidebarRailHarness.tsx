import { useCallback, useEffect, useState } from 'react';
import { Sidebar } from '../../src/components/Sidebar';
import { HomeIcon } from '../../src/components/SidebarIcons';
import { buildDesktopViewModels } from '../../src/utils/desktopViewModels';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop } from '../../src/types/generated';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const noop = () => {};
const titles = ['Sidebar refinement', 'Queue polish', 'Garden', 'Notebook', 'Remote hosts', 'Terminal rendering', 'Review', 'Sketches'];
const sessions = titles.slice(0, -1).map((label, index) => ({
  id: `agent-${index}`, label, state: index % 2 === 0 ? 'waiting_input' as const : 'idle' as const,
  turnOwed: index % 2 === 0,
}));
const desktops = buildDesktopViewModels(titles.map((name, index): Desktop => ({
  id: `desk-${index + 1}`, profile_id: 'fixture', name, shortcut_slot: index + 1,
  order_key: String(index),
  tree_json: index < sessions.length ? JSON.stringify({ type: 'pane', pane_id: `pane-${index}` }) : '',
  active_pane_id: index < sessions.length ? `pane-${index}` : '', revision: 1,
  panes: index < sessions.length ? [{
    pane_id: `pane-${index}`, desktop_id: `desk-${index + 1}`, session_id: sessions[index].id,
    kind: LayoutPaneKind.Agent, status: LayoutPaneStatus.Ready, title: sessions[index].label,
  }] : [],
})), sessions);

export function SidebarRailHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [selected, setSelected] = useState('desk-1');
  const [arrangement, setArrangement] = useState(desktops);
  const reorder = new URLSearchParams(window.location.search).get('update') === 'reorder';
  const triggerRerender = useCallback(() => {
    if (reorder) setArrangement([...desktops.slice(1), desktops[0]]);
    else setSelected('desk-8');
  }, [reorder]);
  useEffect(() => {
    onReady();
    setTriggerRerender(triggerRerender);
  }, [onReady, setTriggerRerender, triggerRerender]);
  return (
    <div className="app" style={{ height: '100vh' }}>
      <Sidebar
        collapsed surface="tree-collapsed" selectedId={null} selectedDesktopId={selected}
        desktops={arrangement} visualIndexByDesktopId={new Map(arrangement.map((desktop, index) => [desktop.id, index]))}
        headerActions={['Garden', 'Notebook', 'Ledger', 'Automations', 'Notifications'].map((title) => ({
          id: title.toLowerCase(), title, icon: <HomeIcon />,
          unread: title === 'Notifications', onClick: noop,
        }))}
        onOpenCommands={() => window.__HARNESS__.recordCall('commands', [])}
        onSelectDesktop={(id) => { setSelected(id); window.__HARNESS__.recordCall('desktop', [id]); }}
        onSelectSession={noop} onNewSession={noop} onCloseSession={noop} onReloadSession={noop}
        onGoToDashboard={noop} onToggleCollapse={noop}
      />
    </div>
  );
}
