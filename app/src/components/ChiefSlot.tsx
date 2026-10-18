import { useLayoutEffect, useState } from 'react';
import { useSidebarContext } from './SidebarContext';
import { ChiefSetup } from './ChiefSetup';
import { CrewRowView } from './QueueRows';
import { SessionLead } from './SessionLead';
import './ChiefSlot.css';

export function ChiefSlot() {
  const { chief, chiefActions, chiefSetupOpen, chiefSetupOpenedByUser, onChiefSettings, desktops, selectedId, onSelectSession, delegates, visualIndexOfDesktop, openSessionActions, openCrewMemberActions } = useSidebarContext();
  const [slot, setSlot] = useState<HTMLDivElement | null>(null);
  const [automaticChief, setAutomaticChief] = useState<string | null>(null);
  const hasSessions = desktops.some(desktop => desktop.sessions.length);
  const setupVisible = chiefSetupOpen && (chiefSetupOpenedByUser || automaticChief === chief?.key);
  useLayoutEffect(() => {
    if (!slot || !chief || !chiefSetupOpen || chiefSetupOpenedByUser || automaticChief === chief.key) return;
    const active = document.activeElement;
    if (!hasSessions && (active === document.body || active === slot?.closest('.app'))) setAutomaticChief(chief.key);
    else chiefActions?.dismiss();
  }, [slot, chief?.key, chiefSetupOpen, chiefSetupOpenedByUser, hasSessions, automaticChief]);
  const [focusRequested, setFocusRequested] = useState<string | null>(null);
  const returnFocus = () => setFocusRequested(chief?.key ?? null);
  useLayoutEffect(() => {
    if (!focusRequested) return;
    const element = slot;
    const active = document.activeElement;
    if (!element || chief?.key !== focusRequested || (active !== document.body && active !== element.closest('.app') && !element.contains(active))) {
      setFocusRequested(null);
      return;
    }
    if (setupVisible) return;
    const target = element.querySelector<HTMLButtonElement>('.queue-row-select:not(:disabled)');
    if (target) { setFocusRequested(null); target.focus(); }
  });
  useLayoutEffect(() => {
    if (!focusRequested) return;
    const cancel = () => setFocusRequested(null);
    document.addEventListener('focusin', cancel, true);
    document.addEventListener('pointerdown', cancel, true);
    return () => {
      document.removeEventListener('focusin', cancel, true);
      document.removeEventListener('pointerdown', cancel, true);
    };
  }, [focusRequested]);
  if (!chief || !chiefActions) return null;
  const desktop = desktops.find(candidate => candidate.sessions.some(session => session.id === chief.sessionId));
  const session = desktop?.sessions.find(candidate => candidate.id === chief.sessionId);
  const row = desktop && session ? { session: { ...session, label: chief.name, crewMember: chief.key }, desktopId: desktop.id, desktopTitle: desktop.title } : undefined;
  const busy = chief.requestPending;
  return <div ref={setSlot} className="chief-slot" data-testid="chief-slot" data-chief-phase={chief.phase} data-chief-member={chief.key} data-chief-session={chief.sessionId}>
    {chief.phase === 'unconfigured' || chief.phase === 'configuring' || chief.phase === 'waking' ? <div className="session-item sidebar-leaf-row queue-row chief-pending-row">
      <button type="button" className="queue-row-select" aria-label={`Set up ${chief.name}`} aria-haspopup="dialog" aria-expanded={Boolean(setupVisible)} onClick={chiefActions.edit} disabled={busy} />
      <SessionLead agent={chief.agent} state={busy ? 'launching' : 'idle'} seed={chief.key} />
      <span className="sidebar-session-identity">{chief.name}</span>
      <span className="session-trailing"><span className="chief-row-pill">{chief.phase === 'unconfigured' ? <>Set up <kbd>⏎</kbd></> : 'waking…'}</span></span>
    </div> : <CrewRowView chief sleeping={chief.phase === 'sleeping'} member={chief.key} name={chief.name} agent={chief.agent} row={row} selected={selectedId === chief.sessionId} where={desktop ? { slot: visualIndexOfDesktop(desktop.id) >= 0 ? String(visualIndexOfDesktop(desktop.id) + 1) : '·', title: desktop.title } : undefined} onSelect={chief.sessionId ? () => onSelectSession(chief.sessionId!) : undefined} onWake={!busy ? chiefActions.wake : undefined} onSleep={!busy ? chiefActions.sleep : undefined} onOpenActions={row ? event => openSessionActions(row.session, event) : undefined} onOpenMemberActions={event => openCrewMemberActions(chief.key, event)} delegates={session ? delegates.get(session.id) || [] : []} />}
    {setupVisible && <ChiefSetup key={chief.key} chief={chief} anchor={slot} autoFocus onSave={chiefActions.setUp} onClose={(restoreFocus = true) => { chiefActions.dismiss(); if (restoreFocus) returnFocus(); }} onReturnFocus={returnFocus} onOpenSettings={onChiefSettings} />}
  </div>;
}
