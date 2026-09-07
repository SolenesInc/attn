import FocusTrap from 'focus-trap-react';
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useCrewCharterAutosave, type CrewCharterAutosave, type CrewCharterEdit } from '../hooks/useCrewCharterAutosave';
import { useCrewLaunchAutosave, type CrewLaunchSelection } from '../hooks/useCrewLaunchAutosave';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useHarnessModelCatalogs } from '../hooks/useHarnessModelCatalogs';
import type { DaemonSession } from '../hooks/useDaemonSocket';
import type { CrewHandoffDocument, CrewMember, DelegationHarness, DelegationModel } from '../types/generated';
import { crewDisplayName } from '../utils/crewName';
import { MarkdownReader } from './MarkdownReader';
import { seedMarkdownSource } from './MarkdownReader/documentSource';
import './CrewPanel.css';

interface CrewPanelProps {
  isOpen: boolean;
  initialMember?: string;
  members: CrewMember[];
  sessions: DaemonSession[];
  preserveStateOnOpen?: boolean;
  onClose: () => void;
  onOpenSeed: (seedId: string) => void;
}

type CrewTab = 'launch' | 'charter' | 'handoffs';

interface HandoffLoad {
  state: 'loading' | 'ready' | 'error';
  handoffs: CrewHandoffDocument[];
  error?: string;
  request: number;
  connectionGeneration: number;
}

interface RestartAttempt {
  requestId: string;
  priorRequestId?: string;
  expectedSessionId: string;
  expectedRevision: number;
  sending: boolean;
  transportError?: string;
  conflict?: boolean;
}

const DEFAULT_VALUE = '';

function charterStatus(edit?: CrewCharterEdit): string {
  if (!edit || edit.state === 'idle' || edit.state === 'loading') return 'Loading…';
  if (edit.state === 'dirty') return 'Waiting to save';
  if (edit.state === 'saving') return 'Saving…';
  if (edit.state === 'conflict') return 'Changed elsewhere';
  if (edit.state === 'error') return 'Not saved';
  return 'Saved';
}

function CharterTab({ member, edit, autosave }: {
  member: CrewMember;
  edit?: CrewCharterEdit;
  autosave: CrewCharterAutosave;
}) {
  if (!edit || edit.state === 'idle' || edit.state === 'loading') {
    return <div className="crew-document-state">Loading charter…</div>;
  }
  if (!edit.acknowledged) {
    return (
      <div className="crew-document-state is-error" role="alert">
        <span>{edit.error || 'The charter could not be loaded.'}</span>
        <button type="button" data-testid="crew-charter-load-retry" onClick={() => void autosave.load(member.id, true)}>Retry</button>
      </div>
    );
  }
  return (
    <section className="crew-charter" aria-label="Charter editor">
      <div className="crew-document-heading">
        <h3>Charter</h3>
        <span data-testid="crew-charter-status" className={`crew-document-save is-${edit.state}`} role="status" aria-live="polite">{charterStatus(edit)}</span>
      </div>
      <div className="crew-charter-meta"><span>CHARTER.md</span><span>Markdown</span></div>
      <label className="crew-visually-hidden" htmlFor={`crew-charter-${member.id}`}>Charter for {crewDisplayName(member.id)}</label>
      <textarea
        id={`crew-charter-${member.id}`}
        data-testid="crew-charter-editor"
        className="crew-charter-editor"
        value={edit.draft}
        onChange={(event) => autosave.update(member.id, event.target.value)}
        onBlur={() => void autosave.flush(member.id)}
        spellCheck
      />
      {edit.state === 'error' && (
        <div className="crew-document-error" role="alert">
          <span>{edit.error || 'The charter was not saved. Your edit is still here.'}</span>
          <button type="button" data-testid="crew-charter-save-retry" onClick={() => void autosave.retry(member.id)}>Retry</button>
        </div>
      )}
      {edit.state === 'conflict' && edit.external && (
        <div className="crew-charter-conflict" role="alert">
          <div>
            <strong>The file changed outside this editor.</strong>
            <span>Your edit is still here. Choose which version should become canonical.</span>
          </div>
          <button type="button" data-testid="crew-charter-use-file" onClick={() => autosave.useExternal(member.id)}>Use file version</button>
          <button type="button" data-testid="crew-charter-keep-mine" onClick={() => void autosave.keepMine(member.id)}>Keep my edit</button>
        </div>
      )}
    </section>
  );
}

function handoffDate(value: Date): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', timeZone: 'UTC', timeZoneName: 'short',
  }).format(date);
}

function HandoffsTab({ member, load, selected, onSelect, onRefresh, onOpenSeed }: {
  member: CrewMember;
  load?: HandoffLoad;
  selected?: string;
  onSelect: (filename: string) => void;
  onRefresh: () => void;
  onOpenSeed: (seedId: string) => void;
}) {
  if (!load || load.state === 'loading') return <div className="crew-document-state">Loading handoffs…</div>;
  if (load.state === 'error') {
    return <div className="crew-document-state is-error" role="alert"><span>{load.error}</span><button type="button" data-testid="crew-handoffs-retry" onClick={onRefresh}>Retry</button></div>;
  }
  if (load.handoffs.length === 0) {
    return (
      <section className="crew-handoffs">
        <div className="crew-document-heading"><h3>Handoffs</h3><button type="button" data-testid="crew-handoffs-refresh" onClick={onRefresh}>Refresh</button></div>
        <div className="crew-document-empty">No handoffs recorded.</div>
      </section>
    );
  }
  const handoff = load.handoffs.find((candidate) => candidate.filename === selected) ?? load.handoffs[0];
  return (
    <section className="crew-handoffs">
      <div className="crew-document-heading">
        <h3>Handoffs</h3>
        <div className="crew-handoff-actions"><span>Read only</span><button type="button" data-testid="crew-handoffs-refresh" onClick={onRefresh}>Refresh</button></div>
      </div>
      <div className="crew-handoff-layout">
        <nav className="crew-handoff-index" aria-label={`${crewDisplayName(member.id)} handoffs`}>
          <span>{load.handoffs.length} {load.handoffs.length === 1 ? 'letter' : 'letters'}</span>
          {load.handoffs.map((candidate, index) => (
            <button
              key={candidate.filename}
              type="button"
              data-testid={`crew-handoff-${index}`}
              aria-current={candidate.filename === handoff.filename ? 'page' : undefined}
              onClick={() => onSelect(candidate.filename)}
            >
              <strong>{new Date(candidate.occurred_at).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' })}</strong>
              <small>{index === 0 ? 'Latest · ' : ''}{new Date(candidate.occurred_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', timeZone: 'UTC' })} UTC</small>
            </button>
          ))}
        </nav>
        <article className="crew-handoff-reader" data-testid="crew-handoff-reader">
          <div className="crew-handoff-date"><span>{handoffDate(handoff.occurred_at)}</span><code>{handoff.filename}</code></div>
          <MarkdownReader
            content={handoff.content}
            source={seedMarkdownSource(`crew-handoff-${member.id}-${handoff.filename}`)}
            allowLocalTargets={false}
            onOpenSeed={onOpenSeed}
          />
        </article>
      </div>
    </section>
  );
}

function effectiveMember(roster: CrewMember, acknowledged?: CrewMember): CrewMember {
  if (!acknowledged || roster.revision > acknowledged.revision) return roster;
  return acknowledged;
}

function modelLabel(model: DelegationModel): string {
  const name = model.name || model.id;
  return model.provider ? `${model.provider} / ${name}` : name;
}

function modelIdentity(model: DelegationModel): string {
  return model.provider ? `${model.provider}/${model.id}` : model.id;
}

function currentModel(catalog: DelegationModel[] | undefined, id: string): DelegationModel | undefined {
  return catalog?.find((model) => modelIdentity(model) === id);
}

function runningSessionFor(member: CrewMember, sessions: DaemonSession[]): DaemonSession | undefined {
  if (!member.binding_session) return undefined;
  return sessions.find((session) => session.id === member.binding_session);
}

function statusCopy(state: 'saved' | 'saving' | 'error'): string {
  if (state === 'saving') return 'Saving…';
  if (state === 'error') return 'Not saved';
  return 'Saved';
}

function RestartState({ member, attempt, onRetryTransport, onRetryFailed }: {
  member: CrewMember;
  attempt?: RestartAttempt;
  onRetryTransport: () => void;
  onRetryFailed: () => void;
}) {
  const restart = !attempt || member.restart?.request_id === attempt.requestId ? member.restart : undefined;
  if (restart?.state === 'completed') {
    return <div className="crew-restart-state is-complete" role="status">New day started{restart.successor_session_id ? ` · ${restart.successor_session_id.slice(0, 8)}` : ''}</div>;
  }
  if (restart?.state === 'failed') {
    return (
      <div className="crew-restart-state is-failed" role="status">
        <span>{restart.error || 'The restart failed.'}</span>
        <button type="button" onClick={onRetryFailed}>Try again</button>
      </div>
    );
  }
  if (restart) {
    const label = restart.state === 'requested' ? 'Handoff requested' : 'Queued for delivery';
    return <div className="crew-restart-state is-pending" role="status">{label}{restart.detail ? ` · ${restart.detail}` : ''}</div>;
  }
  if (attempt?.transportError) {
    return (
      <div className="crew-restart-state is-failed" role="status">
        <span>{attempt.conflict ? 'The member changed before this request landed.' : attempt.transportError}</span>
        <button type="button" onClick={attempt.conflict ? onRetryFailed : onRetryTransport}>
          {attempt.conflict ? 'Review and try again' : 'Retry delivery'}
        </button>
      </div>
    );
  }
  return null;
}

export function CrewPanel({
  isOpen,
  initialMember,
  members,
  sessions,
  preserveStateOnOpen = false,
  onClose,
  onOpenSeed,
}: CrewPanelProps) {
  const {
    isConnected,
    connectionGeneration,
    sendCrewSet,
    sendCrewRestart,
    sendCrewCharterGet,
    sendCrewCharterSet,
    sendCrewHandoffsGet,
    sendDelegationPreferencesGet,
    sendDelegationModels,
  } = useDaemonApi();
  const [selectedId, setSelectedId] = useState(initialMember || members[0]?.id || '');
  const [filter, setFilter] = useState('');
  const [tab, setTab] = useState<CrewTab>('launch');
  const [harnesses, setHarnesses] = useState<DelegationHarness[]>([]);
  const [catalogError, setCatalogError] = useState('');
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [catalogRequest, setCatalogRequest] = useState(0);
  const [manualModel, setManualModel] = useState<Record<string, boolean>>({});
  const [confirming, setConfirming] = useState(false);
  const [attempts, setAttempts] = useState<Record<string, RestartAttempt>>({});
  const [handoffLoads, setHandoffLoads] = useState<Record<string, HandoffLoad>>({});
  const handoffLoadsRef = useRef(handoffLoads);
  const [selectedHandoffs, setSelectedHandoffs] = useState<Record<string, string>>({});
  const handoffRequest = useRef(0);
  const [navigationPending, setNavigationPending] = useState(false);
  const navigationSequence = useRef(0);
  const closeRef = useRef<HTMLButtonElement>(null);
  const rosterRef = useRef<HTMLDivElement>(null);
  const wasOpen = useRef(false);
  const lastInitialMember = useRef<string | undefined>(undefined);
  const autosave = useCrewLaunchAutosave(members, connectionGeneration, sendCrewSet);
  const charterAutosave = useCrewCharterAutosave(connectionGeneration, sendCrewCharterGet, sendCrewCharterSet);
  const models = useHarnessModelCatalogs(isOpen, sendDelegationModels);

  useEffect(() => {
    setAttempts((current) => {
      let next = current;
      for (const rosterMember of members) {
        const attempt = current[rosterMember.id];
        const authoritativeRequestId = rosterMember.restart?.request_id;
        if (!attempt || !authoritativeRequestId
          || authoritativeRequestId === attempt.requestId
          || authoritativeRequestId === attempt.priorRequestId) continue;
        if (next === current) next = { ...current };
        delete next[rosterMember.id];
      }
      return next;
    });
  }, [members]);

  const selectedRosterMember = members.find((member) => member.id === selectedId) ?? members[0];
  const charterEdit = selectedRosterMember ? charterAutosave.read(selectedRosterMember.id) : undefined;

  const navigate = useCallback((action: () => void) => {
    const sequence = ++navigationSequence.current;
    if (!selectedRosterMember || tab !== 'charter' || !charterEdit?.acknowledged || charterEdit.state === 'saved') {
      action();
      return;
    }
    setNavigationPending(true);
    void charterAutosave.flush(selectedRosterMember.id).then((saved) => {
      if (saved && navigationSequence.current === sequence) action();
    }).finally(() => {
      if (navigationSequence.current === sequence) setNavigationPending(false);
    });
  }, [charterAutosave, charterEdit?.state, selectedRosterMember, tab]);

  const requestClose = useCallback(() => navigate(onClose), [navigate, onClose]);

  useEscapeStack(requestClose, isOpen && !confirming);
  useEscapeStack(() => setConfirming(false), isOpen && confirming);

  useEffect(() => {
    if (!isOpen) {
      wasOpen.current = false;
      return;
    }
    const opening = !wasOpen.current;
    wasOpen.current = true;
    if ((opening && !preserveStateOnOpen) || initialMember !== lastInitialMember.current) {
      lastInitialMember.current = initialMember;
      setSelectedId(initialMember && members.some((candidate) => candidate.id === initialMember)
        ? initialMember
        : members[0]?.id || '');
      return;
    }
    if (!members.some((candidate) => candidate.id === selectedId)) setSelectedId(members[0]?.id || '');
  }, [initialMember, isOpen, members, preserveStateOnOpen, selectedId]);

  useEffect(() => {
    if (!isOpen) return;
    let live = true;
    setCatalogLoading(true);
    setCatalogError('');
    void sendDelegationPreferencesGet().then((result) => {
      if (live) setHarnesses(result.harnesses);
    }).catch((error) => {
      if (live) setCatalogError(error instanceof Error ? error.message : String(error));
    }).finally(() => {
      if (live) setCatalogLoading(false);
    });
    return () => { live = false; };
  }, [catalogRequest, isOpen, sendDelegationPreferencesGet]);

  const edit = selectedRosterMember ? autosave.read(selectedRosterMember.id) : undefined;
  const member = selectedRosterMember ? effectiveMember(selectedRosterMember, edit?.acknowledged) : undefined;
  const selection = edit?.draft;
  const clearingAgent = selection?.agent === '' && Boolean(edit?.acknowledged.agent);
  const effectiveAgent = clearingAgent ? '' : selection?.agent || member?.resolved_agent || '';
  const harness = harnesses.find((candidate) => candidate.id === effectiveAgent);
  const catalog = effectiveAgent ? models.catalogs[effectiveAgent] : undefined;
  const selectedModel = currentModel(catalog?.models, selection?.model || '');

  useEffect(() => {
    handoffLoadsRef.current = handoffLoads;
  }, [handoffLoads]);

  const loadHandoffs = useCallback((memberId: string, force = false) => {
    const existing = handoffLoadsRef.current[memberId];
    if (!force && existing && existing.state !== 'error'
      && existing.connectionGeneration === connectionGeneration) return;
    const request = ++handoffRequest.current;
    setHandoffLoads((current) => ({
      ...current,
      [memberId]: {
        state: 'loading', handoffs: current[memberId]?.handoffs ?? [], request, connectionGeneration,
      },
    }));
    void sendCrewHandoffsGet(memberId).then((result) => {
      if (result.member !== memberId) {
        throw new Error(`Handoff response named ${result.member}, expected ${memberId}`);
      }
      setHandoffLoads((current) => current[memberId]?.request !== request ? current : ({
        ...current,
        [memberId]: { state: 'ready', handoffs: result.handoffs, request, connectionGeneration },
      }));
      setSelectedHandoffs((current) => {
        const selected = current[memberId];
        if (selected && result.handoffs.some((handoff) => handoff.filename === selected)) return current;
        return { ...current, [memberId]: result.handoffs[0]?.filename ?? '' };
      });
    }).catch((error) => {
      setHandoffLoads((current) => current[memberId]?.request !== request ? current : ({
        ...current,
        [memberId]: {
          state: 'error',
          handoffs: current[memberId]?.handoffs ?? [],
          error: error instanceof Error ? error.message : String(error),
          request,
          connectionGeneration,
        },
      }));
    });
  }, [connectionGeneration, sendCrewHandoffsGet]);

  useEffect(() => {
    if (!isOpen || !selectedRosterMember) return;
    if (tab === 'charter') void charterAutosave.load(selectedRosterMember.id);
    if (tab === 'handoffs') loadHandoffs(selectedRosterMember.id);
  }, [charterAutosave, isOpen, loadHandoffs, selectedRosterMember, tab]);

  useEffect(() => {
    const agent = effectiveAgent;
    if (!isOpen || !agent || !harness?.discovery || catalog || models.loading[agent] || models.errors[agent]) return;
    void models.discover(agent);
  }, [catalog, effectiveAgent, harness?.discovery, isOpen, models]);

  const visibleMembers = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return members;
    return members.filter((member) => crewDisplayName(member.id).toLowerCase().includes(query));
  }, [filter, members]);

  const updateSelection = useCallback((next: Partial<CrewLaunchSelection>) => {
    if (!member) return;
    autosave.update(member.id, next);
  }, [autosave, member]);

  const moveRosterSelection = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    const current = Math.max(0, visibleMembers.findIndex((candidate) => candidate.id === member?.id));
    const offset = event.key === 'ArrowDown' ? 1 : -1;
    const next = Math.max(0, Math.min(visibleMembers.length - 1, current + offset));
    const nextMember = visibleMembers[next];
    if (!nextMember) return;
    navigate(() => {
      setSelectedId(nextMember.id);
      rosterRef.current?.querySelectorAll<HTMLButtonElement>('[data-crew-roster-member]')[next]?.focus();
    });
  };

  const sendAttempt = useCallback((memberId: string, attempt: RestartAttempt) => {
    setAttempts((current) => ({ ...current, [memberId]: { ...attempt, sending: true, transportError: undefined, conflict: false } }));
    void sendCrewRestart({
      member: memberId,
      requestId: attempt.requestId,
      expectedSessionId: attempt.expectedSessionId,
      expectedRevision: attempt.expectedRevision,
    }).then((outcome) => {
      if (outcome.member) autosave.observe(outcome.member);
      if (!outcome.success) {
        setAttempts((current) => current[memberId]?.requestId !== attempt.requestId ? current : ({
          ...current,
          [memberId]: {
            ...attempt,
            sending: false,
            transportError: outcome.error || 'The restart request failed.',
            conflict: outcome.conflict,
          },
        }));
        return;
      }
      setAttempts((current) => current[memberId]?.requestId !== attempt.requestId
        ? current
        : ({ ...current, [memberId]: { ...attempt, sending: false } }));
    }).catch((error) => {
      setAttempts((current) => current[memberId]?.requestId !== attempt.requestId ? current : ({
        ...current,
        [memberId]: {
          ...attempt,
          sending: false,
          transportError: error instanceof Error ? error.message : String(error),
        },
      }));
    });
  }, [autosave, sendCrewRestart]);

  const confirmRestart = () => {
    if (!member || !edit || edit.state !== 'saved') return;
    const attempt: RestartAttempt = {
      requestId: crypto.randomUUID(),
      priorRequestId: member.restart?.request_id,
      expectedSessionId: member.binding_session ?? '',
      expectedRevision: member.revision,
      sending: false,
    };
    setConfirming(false);
    sendAttempt(member.id, attempt);
  };

  const restartAttempt = member ? attempts[member.id] : undefined;
  const running = member ? runningSessionFor(member, sessions) : undefined;
  const restartBusy = restartAttempt?.sending
    || member?.restart?.state === 'queued'
    || member?.restart?.state === 'requested';
  const savesAcknowledged = edit?.state === 'saved';

  return (
    <div className={`crew-panel-layer ${isOpen ? 'is-open' : ''}`} aria-hidden={!isOpen}>
      <FocusTrap active={isOpen} focusTrapOptions={{ escapeDeactivates: false, initialFocus: () => closeRef.current }}>
        <section className="crew-panel" data-testid="crew-panel" role="dialog" aria-modal="true" aria-labelledby="crew-panel-title">
          <header className="crew-panel-bar">
            <div>
              <span className="crew-kicker">Crew</span>
              <h1 id="crew-panel-title">Manage crew</h1>
            </div>
            <button ref={closeRef} type="button" className="crew-close" data-testid="crew-panel-close" disabled={navigationPending} onClick={requestClose}>Close <kbd>Esc</kbd></button>
          </header>
          <div className="crew-panel-shell">
            <aside className="crew-roster" aria-label="Crew roster">
              <div className="crew-roster-heading"><span>Members</span><span>{members.length}</span></div>
              {members.length > 5 && (
                <input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="Find a member…" aria-label="Find a member" />
              )}
              <div ref={rosterRef} className="crew-roster-list" onKeyDown={moveRosterSelection}>
                {visibleMembers.map((candidate) => {
                  const awake = Boolean(candidate.binding_session);
                  const candidateEdit = autosave.read(candidate.id);
                  return (
                    <button
                      type="button"
                      key={candidate.id}
                      data-crew-roster-member={candidate.id}
                      data-testid={`crew-roster-${candidate.id}`}
                      aria-current={candidate.id === member?.id ? 'true' : undefined}
                      className={candidate.id === member?.id ? 'is-selected' : ''}
                      onClick={() => navigate(() => setSelectedId(candidate.id))}
                    >
                      <span className="crew-avatar" aria-hidden="true">{crewDisplayName(candidate.id).slice(0, 1)}</span>
                      <span className="crew-roster-identity">
                        <strong>{crewDisplayName(candidate.id)}</strong>
                        <small><i className={awake ? 'is-awake' : ''} />{awake ? 'Awake' : 'Asleep'}</small>
                      </span>
                      {candidateEdit?.state !== 'saved' && <span className={`crew-roster-save is-${candidateEdit?.state}`} aria-label={candidateEdit?.state} />}
                    </button>
                  );
                })}
              </div>
            </aside>
            <main className="crew-member-detail">
              {!member || !edit || !selection ? (
                <div className="crew-empty">No crew members.</div>
              ) : (
                <>
                  <div className="crew-member-heading">
                    <div className="crew-member-identity">
                      <span className="crew-avatar is-large" aria-hidden="true">{crewDisplayName(member.id).slice(0, 1)}</span>
                      <div><h2>{crewDisplayName(member.id)}</h2><span>{member.binding_session ? 'Awake' : 'Asleep'}</span></div>
                    </div>
                    <span className={`crew-presence ${member.binding_session ? 'is-awake' : ''}`}>{member.binding_session ? 'Current day active' : 'Between days'}</span>
                  </div>

                  <nav className="crew-member-tabs" aria-label="Member details">
                    <button type="button" data-testid="crew-tab-launch" className={tab === 'launch' ? 'is-selected' : ''} aria-current={tab === 'launch' ? 'page' : undefined} onClick={() => navigate(() => setTab('launch'))}>Launch settings</button>
                    <button type="button" data-testid="crew-tab-charter" className={tab === 'charter' ? 'is-selected' : ''} aria-current={tab === 'charter' ? 'page' : undefined} onClick={() => navigate(() => setTab('charter'))}>Charter</button>
                    <button type="button" data-testid="crew-tab-handoffs" className={tab === 'handoffs' ? 'is-selected' : ''} aria-current={tab === 'handoffs' ? 'page' : undefined} onClick={() => navigate(() => setTab('handoffs'))}>Handoffs</button>
                  </nav>

                  {tab === 'launch' && <>
                  {member.binding_session && (
                    <section className="crew-running" aria-label="Running now">
                      <span className="crew-kicker">Running now</span>
                      <div className="crew-runtime-values">
                        <span><small>Harness</small>{running?.agent || 'Not reported'}</span>
                        <span><small>Model</small>Not reported</span>
                        <span><small>Effort</small>Not reported</span>
                      </div>
                      <code>{member.binding_session.slice(0, 8)}</code>
                    </section>
                  )}

                  <section className="crew-launch-card">
                    <div className="crew-launch-title">
                      <div><span className="crew-kicker">Next wake</span><h3>Launch settings</h3></div>
                      <div className={`crew-save-state is-${edit.state}`} role="status" aria-live="polite">
                        {statusCopy(edit.state)}
                        {edit.state === 'error' && <button type="button" onClick={() => autosave.retry(member.id)}>Retry</button>}
                      </div>
                    </div>

                    <div className="crew-launch-fields">
                      <label>
                        <span>Harness</span>
                        <select
                          data-testid="crew-harness"
                          value={selection.agent}
                          disabled={catalogLoading}
                          onChange={(event) => {
                            setManualModel((current) => ({ ...current, [member.id]: false }));
                            updateSelection({ agent: event.target.value, model: '', effort: '' });
                          }}
                        >
                          <option value={DEFAULT_VALUE}>Crew default</option>
                          {harnesses.map((candidate) => (
                            <option key={candidate.id} value={candidate.id} disabled={!candidate.available && candidate.id !== selection.agent}>
                              {candidate.name}{candidate.available ? '' : ' (unavailable)'}
                            </option>
                          ))}
                          {selection.agent && !harness && <option value={selection.agent}>{selection.agent} (unavailable)</option>}
                        </select>
                      </label>

                      <label>
                        <span>Model</span>
                        <select
                          data-testid="crew-model"
                          value={manualModel[member.id] ? '__custom' : selection.model}
                          disabled={!effectiveAgent || harness?.model_pin === false}
                          onChange={(event) => {
                            if (event.target.value === '__custom') {
                              setManualModel((current) => ({ ...current, [member.id]: true }));
                              return;
                            }
                            setManualModel((current) => ({ ...current, [member.id]: false }));
                            const nextModel = currentModel(catalog?.models, event.target.value);
                            const clearsEffort = nextModel?.effort_support === 'unsupported'
                              || Boolean(nextModel?.effort_levels?.length && selection.effort && !nextModel.effort_levels.includes(selection.effort));
                            updateSelection({
                              model: event.target.value,
                              ...(clearsEffort ? { effort: '' } : {}),
                            });
                          }}
                        >
                          <option value={DEFAULT_VALUE}>Harness default</option>
                          {catalog?.models.map((candidate) => (
                            <option key={`${candidate.provider}/${candidate.id}`} value={modelIdentity(candidate)} disabled={candidate.access === 'unsupported'}>
                              {modelLabel(candidate)}{candidate.access === 'unsupported' ? ' (unsupported)' : ''}
                            </option>
                          ))}
                          {selection.model && !selectedModel && !manualModel[member.id] && <option value={selection.model}>{selection.model} (custom)</option>}
                          <option value="__custom">Enter a model ID…</option>
                        </select>
                      </label>

                      {manualModel[member.id] && (
                        <label className="crew-custom-model">
                          <span>Exact model ID</span>
                          <input
                            data-testid="crew-custom-model"
                            autoFocus
                            value={selection.model}
                            placeholder="Model ID from the harness"
                            onChange={(event) => updateSelection({ model: event.target.value, effort: '' })}
                          />
                        </label>
                      )}

                      <label>
                        <span>Reasoning effort</span>
                        <input
                          data-testid="crew-effort"
                          list={`crew-efforts-${member.id}`}
                          value={selection.effort}
                          placeholder="Harness default"
                          disabled={!effectiveAgent || harness?.effort_pin === false || selectedModel?.effort_support === 'unsupported'}
                          onChange={(event) => updateSelection({ effort: event.target.value })}
                        />
                        <datalist id={`crew-efforts-${member.id}`}>
                          {selectedModel?.effort_levels?.map((level) => <option key={level} value={level} />)}
                        </datalist>
                      </label>
                    </div>

                    {(catalogError || (effectiveAgent && !harness?.available) || models.errors[effectiveAgent]) && (
                      <div className="crew-capability-warning" role="alert">
                        <span>{catalogError || models.errors[effectiveAgent] || 'This harness is unavailable on this daemon.'}</span>
                        {catalogError && <button type="button" onClick={() => setCatalogRequest((request) => request + 1)}>Retry harness discovery</button>}
                      </div>
                    )}
                    {effectiveAgent && harness?.discovery && (
                      <button
                        type="button"
                        className="crew-discover"
                        disabled={models.loading[effectiveAgent]}
                        onClick={() => void models.discover(effectiveAgent)}
                      >
                        {models.loading[effectiveAgent] ? 'Discovering models…' : catalog ? 'Refresh models' : 'Discover models'}
                      </button>
                    )}
                    <div className="crew-acknowledged" data-testid="crew-acknowledged">
                      <span>Acknowledged next wake</span>
                      <strong>{[edit.acknowledged.resolved_agent, edit.acknowledged.resolved_model || 'default model', edit.acknowledged.resolved_effort || 'default effort'].join(' / ')}</strong>
                    </div>
                    {edit.error && <div className="crew-save-error">{edit.error}</div>}
                  </section>

                  <section className="crew-restart">
                    <div>
                      <h3>{member.binding_session ? 'Handoff and restart' : 'Wake member'}</h3>
                    </div>
                    <button
                      type="button"
                      data-testid="crew-restart"
                      disabled={!savesAcknowledged || restartBusy || !isConnected}
                      title={!savesAcknowledged ? 'Wait for launch settings to be saved' : undefined}
                      onClick={() => setConfirming(true)}
                    >
                      {restartBusy ? 'Restart in progress…' : member.binding_session ? 'Handoff and restart' : 'Wake'}
                    </button>
                  </section>
                  <RestartState
                    member={member}
                    attempt={restartAttempt}
                    onRetryTransport={() => restartAttempt && sendAttempt(member.id, restartAttempt)}
                    onRetryFailed={() => {
                      setAttempts((current) => {
                        const next = { ...current };
                        delete next[member.id];
                        return next;
                      });
                      setConfirming(true);
                    }}
                  />
                  </>}

                  {tab === 'charter' && <CharterTab member={member} edit={charterEdit} autosave={charterAutosave} />}
                  {tab === 'handoffs' && (
                    <HandoffsTab
                      member={member}
                      load={handoffLoads[member.id]}
                      selected={selectedHandoffs[member.id]}
                      onSelect={(filename) => setSelectedHandoffs((current) => ({ ...current, [member.id]: filename }))}
                      onRefresh={() => loadHandoffs(member.id, true)}
                      onOpenSeed={(seedId) => navigate(() => onOpenSeed(seedId))}
                    />
                  )}
                </>
              )}
            </main>
          </div>

          {confirming && member && edit && (
            <div className="crew-confirm-backdrop">
              <div className="crew-confirm" role="alertdialog" aria-modal="true" aria-labelledby="crew-confirm-title">
                <span className="crew-kicker">Confirm</span>
                <h2 id="crew-confirm-title">{member.binding_session ? `Restart ${crewDisplayName(member.id)}?` : `Wake ${crewDisplayName(member.id)}?`}</h2>
                <div className="crew-confirm-selection">
                  {edit.acknowledged.resolved_agent} / {edit.acknowledged.resolved_model || 'default model'} / {edit.acknowledged.resolved_effort || 'default effort'}
                </div>
                <div className="crew-confirm-actions">
                  <button type="button" onClick={() => setConfirming(false)}>Cancel</button>
                  <button type="button" className="is-primary" data-testid="crew-confirm-restart" autoFocus onClick={confirmRestart}>
                    {member.binding_session ? 'Request handoff and restart' : 'Wake member'}
                  </button>
                </div>
              </div>
            </div>
          )}
        </section>
      </FocusTrap>
    </div>
  );
}
