import { useCallback, useRef } from 'react';
import { useSidebarContext } from './SidebarContext';
import { ChiefSetup } from './ChiefSetup';
import { CrewRowView } from './QueueRows';
import { SessionLead } from './SessionLead';
import './ChiefSlot.css';

export function ChiefSlot() {
  const { chief, chiefActions, chiefSetupOpen, chiefSetupOpenedByUser, onChiefSettings, desktops, selectedId, onSelectSession, delegates, visualIndexOfDesktop, openSessionActions, openCrewMemberActions } = useSidebarContext();
  const slot = useRef<HTMLDivElement>(null);
  const returnFocus = useCallback(() => {
    const element = slot.current;
    const member = element?.dataset.chiefMember;
    window.requestAnimationFrame(() => {
      if (!element?.isConnected || element.dataset.chiefMember !== member) return;
      const active = document.activeElement;
      if (active !== document.body && active !== element.closest('.app') && !element.contains(active)) return;
      element.querySelector<HTMLButtonElement>('.queue-row-select:not(:disabled)')?.focus();
    });
  }, []);
  if (!chief || !chiefActions) return null;
  const desktop = desktops.find(candidate => candidate.sessions.some(session => session.id === chief.sessionId));
  const session = desktop?.sessions.find(candidate => candidate.id === chief.sessionId);
  const row = desktop && session ? { session: { ...session, label: chief.name, crewMember: chief.key }, desktopId: desktop.id, desktopTitle: desktop.title } : undefined;
  const busy = chief.requestPending;
  return <div ref={slot} className="chief-slot" data-testid="chief-slot" data-chief-phase={chief.phase} data-chief-member={chief.key} data-chief-session={chief.sessionId}>
    {chief.phase === 'unconfigured' || chief.phase === 'configuring' || chief.phase === 'waking' ? <div className="session-item sidebar-leaf-row queue-row chief-pending-row">
      <button type="button" className="queue-row-select" aria-label={`Set up ${chief.name}`} onClick={chiefActions.edit} disabled={busy} />
      <SessionLead agent={chief.agent} state={busy ? 'launching' : 'idle'} seed={chief.key} />
      <span className="sidebar-session-identity">{chief.name}</span>
      <span className="session-trailing"><span className="queue-chief-glyph" aria-label={chief.name}>⌁</span>{chief.phase === 'unconfigured' ? 'set up' : chief.phase === 'configuring' ? 'saving…' : 'waking…'}</span>
    </div> : <CrewRowView chief sleeping={chief.phase === 'sleeping'} member={chief.key} name={chief.name} agent={chief.agent} row={row} selected={selectedId === chief.sessionId} where={desktop ? { slot: visualIndexOfDesktop(desktop.id) >= 0 ? String(visualIndexOfDesktop(desktop.id) + 1) : '·', title: desktop.title } : undefined} onSelect={chief.sessionId ? () => onSelectSession(chief.sessionId!) : undefined} onWake={!busy ? chiefActions.wake : undefined} onSleep={!busy ? chiefActions.sleep : undefined} onOpenActions={row ? event => openSessionActions(row.session, event) : undefined} onOpenMemberActions={event => openCrewMemberActions(chief.key, event)} delegates={session ? delegates.get(session.id) || [] : []} />}
    {!chiefSetupOpen && chief.restartError && <p className="chief-setup-error" role="alert">{chief.restartError}</p>}
    {chiefSetupOpen && <ChiefSetup key={chief.key} chief={chief} autoFocus={Boolean(chiefSetupOpenedByUser || !desktops.some(desktop => desktop.sessions.length))} onSave={chiefActions.setUp} onClose={() => { chiefActions.dismiss(); returnFocus(); }} onReturnFocus={returnFocus} onOpenSettings={onChiefSettings} />}
  </div>;
}
