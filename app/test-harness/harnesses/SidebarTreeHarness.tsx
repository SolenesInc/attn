import { useEffect, useState } from 'react';
import { Sidebar } from '../../src/components/Sidebar';
import type { SidebarDesktop, SelectedTile } from '../../src/components/sidebarTypes';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const noop = () => {};
const density = [4, 1, 0, 2, 2, 1, 3, 0, 0, 0, 0, 1, 2];
const desktops: SidebarDesktop[] = density.map((count, index) => {
  const id = `desk-${index + 1}`;
  const sessions = Array.from({ length: count }, (_, leaf) => ({
    id: `${id}-agent-${leaf + 1}`, label: `Agent ${index + 1}.${leaf + 1}`, state: 'idle' as const,
  }));
  return {
    id, title: `Desktop ${index + 1}`, directory: '',
    desktop: { name: `Desktop ${index + 1}`, defaultLabel: `Desktop ${index + 1}`, number: index + 1 },
    sessions, children: sessions.map((session) => ({ kind: 'session' as const, id: session.id, session })),
    firstSessionId: sessions[0]?.id ?? null, focusedSessionId: sessions[0]?.id ?? null,
    hasUnresolvedAgentPanes: false,
  };
});
type Selection = { desktop: string; session: string | null; tile?: SelectedTile; home?: boolean };

export function SidebarTreeHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const collapsed = new URLSearchParams(window.location.search).has('rail');
  const [arrangement, setArrangement] = useState(desktops);
  const [selection, setSelection] = useState<Selection>({ desktop: 'desk-1', session: 'desk-1-agent-1' });
  const [pending, setPending] = useState<Selection | null>(null);
  useEffect(() => {
    onReady();
    setTriggerRerender(noop);
  }, [onReady, setTriggerRerender]);
  return (
    <div className="app" style={{ height: '100vh' }}>
      <Sidebar
        collapsed={collapsed} surface={collapsed ? "tree-collapsed" : "tree-open"} selectedId={selection.session}
        selectedDesktopId={selection.desktop} selectedTile={selection.tile} homeActive={selection.home}
        desktops={arrangement} visualIndexByDesktopId={new Map(arrangement.map((desktop, index) => [desktop.id, index]))}
        headerActions={[]}
        onDesktopReorder={noop}
        onSelectDesktop={(id) => {
          setPending(id === selection.desktop && !selection.home
            ? selection : { desktop: id, session: arrangement.find((desktop) => desktop.id === id)?.firstSessionId ?? null });
          if (selection.home) setSelection({ ...selection, home: false });
        }}
        onSelectSession={(id) => setPending({ desktop: arrangement.find((desktop) => desktop.sessions.some((session) => session.id === id))!.id, session: id })}
        onSelectTile={(desktopId, tileId) => setPending({ desktop: desktopId, session: null, tile: { desktopId, tileId } })}
        onNewSession={noop} onCloseSession={noop} onReloadSession={noop} onToggleCollapse={noop}
        onGoToDashboard={() => setSelection({ ...selection, session: null, tile: undefined, home: true })}
      />
      <div>
        <button onClick={() => setSelection({ desktop: 'desk-7', session: 'desk-7-agent-2' })}>Jump to agent</button>
        <button onClick={() => setSelection({ desktop: 'desk-9', session: null })}>Jump to empty desktop</button>
        <button onClick={() => setSelection({ desktop: 'desk-1', session: 'desk-1-agent-1' })}>Jump to first</button>
        <button onClick={() => setSelection({ desktop: 'desk-13', session: 'desk-13-agent-2' })}>Jump to last</button>
        <button onClick={() => {
          const tile = { type: 'tile' as const, tileId: 'notes', tileKind: 'markdown', tileParams: '/fixture/notes.md' };
          setArrangement(arrangement.map((desktop) => desktop.id === 'desk-7'
            ? { ...desktop, children: [...desktop.children, { kind: 'tile', id: tile.tileId, tile }] } : desktop));
        }}>Add tile</button>
        <button onClick={() => setSelection({ desktop: 'desk-7', session: null, tile: { desktopId: 'desk-7', tileId: 'notes' } })}>Jump to tile</button>
        <button disabled={!pending} onClick={() => { setSelection(pending!); setPending(null); }}>Deliver selection</button>
        <button onClick={() => setArrangement([...arrangement.filter((desktop) => desktop.id !== selection.desktop), arrangement.find((desktop) => desktop.id === selection.desktop)!])}>Reorder selected to end</button>
        <button onClick={() => setArrangement(arrangement.map((desktop, index) => {
          if (index !== 0) return desktop;
          const session = { id: 'new-agent', label: 'New agent', state: 'idle' as const };
          return { ...desktop, sessions: [...desktop.sessions, session], children: [...desktop.children, { kind: 'session', id: session.id, session }] };
        }))}>Add agent above</button>
      </div>
    </div>
  );
}
