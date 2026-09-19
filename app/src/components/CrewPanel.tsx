import FocusTrap from 'focus-trap-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useCrewCharterAutosave, type CrewCharterAutosave, type CrewCharterEdit } from '../hooks/useCrewCharterAutosave';
import { useCrewHandoffs, type CrewHandoffHistory } from '../hooks/useCrewHandoffs';
import { useCrewLaunchAutosave } from '../hooks/useCrewLaunchAutosave';
import { useCrewNavigation, type CrewTab } from '../hooks/useCrewNavigation';
import { useCrewRestart, type CrewRestarts } from '../hooks/useCrewRestart';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { DaemonSession, Seed } from '../hooks/useDaemonSocket';
import type { CrewMember, DelegationHarness } from '../types/generated';
import { crewDisplayName } from '../utils/crewName';
import { CrewCharterTab } from './CrewCharterTab';
import { CrewHandoffsTab } from './CrewHandoffsTab';
import { CrewLaunchTab, type CrewLaunchTabProps } from './CrewLaunchTab';
import { CrewRoster } from './CrewRoster';
import { CrewSeeds, type CrewSeedsProps } from './CrewSeeds';
import { effectiveMember, nextWakeLabel, runningSessionFor } from './crewLaunchPresentation';
import type { CrewSeedFilter } from './crewSeedOwnership';
import './CrewPanel.css';

type CrewLaunchAutosave = ReturnType<typeof useCrewLaunchAutosave>;

interface CrewPanelProps {
  isOpen: boolean;
  visit: number;
  initialMember?: string;
  members: CrewMember[];
  sessions: DaemonSession[];
  seeds: Seed[];
  seedsTotal: number;
  onClose: () => void;
  onOpenSeed: (seedId: string, placementSessionId?: string) => void;
}

const tabs: { id: CrewTab; label: string }[] = [
  { id: 'launch', label: 'Launch settings' },
  { id: 'charter', label: 'Charter' },
  { id: 'handoffs', label: 'Handoffs' },
  { id: 'seeds', label: 'Seeds' },
];

function useHarnessCatalog(isOpen: boolean, load: () => Promise<{ harnesses: DelegationHarness[] }>) {
  const [harnesses, setHarnesses] = useState<DelegationHarness[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [request, setRequest] = useState(0);
  useEffect(() => {
    if (!isOpen) return;
    let live = true;
    setLoading(true);
    setError('');
    void load().then((result) => {
      if (live) setHarnesses(result.harnesses);
    }).catch((cause) => {
      if (live) setError(cause instanceof Error ? cause.message : String(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [isOpen, load, request]);
  const retry = useCallback(() => setRequest((current) => current + 1), []);
  return { harnesses, error, loading, retry };
}

function MemberHeading({ member }: { member: CrewMember }) {
  const name = crewDisplayName(member.id);
  const awake = Boolean(member.binding_session);
  return (
    <div className="crew-member-heading">
      <div className="crew-member-identity">
        <span className="crew-avatar is-large" aria-hidden="true">{name.slice(0, 1)}</span>
        <div><h2>{name}</h2><span>{awake ? 'Awake' : 'Asleep'}</span></div>
      </div>
      <span className={`crew-presence ${awake ? 'is-awake' : ''}`}>{awake ? 'Current day active' : 'Between days'}</span>
    </div>
  );
}

function MemberTabs({ tab, onSelect }: { tab: CrewTab; onSelect: (tab: CrewTab) => void }) {
  return (
    <nav className="crew-member-tabs" aria-label="Member details">
      {tabs.map((candidate) => (
        <button
          key={candidate.id}
          type="button"
          data-testid={`crew-tab-${candidate.id}`}
          className={tab === candidate.id ? 'is-selected' : ''}
          aria-current={tab === candidate.id ? 'page' : undefined}
          onClick={() => onSelect(candidate.id)}
        >
          {candidate.label}
        </button>
      ))}
    </nav>
  );
}

function MemberTabPanel({ tab, launch, charterEdit, charterAutosave, handoffs, seeds, onOpenSeed }: {
  tab: CrewTab;
  launch: CrewLaunchTabProps;
  charterEdit?: CrewCharterEdit;
  charterAutosave: CrewCharterAutosave;
  handoffs: CrewHandoffHistory;
  seeds: Omit<CrewSeedsProps, 'member' | 'onOpenSeed'>;
  onOpenSeed: (seedId: string) => void;
}) {
  const member = launch.member;
  switch (tab) {
    case 'launch':
      return <CrewLaunchTab key={member.id} {...launch} />;
    case 'charter':
      return <CrewCharterTab member={member} edit={charterEdit} autosave={charterAutosave} />;
    case 'handoffs':
      return <CrewHandoffsTab member={member} history={handoffs} onOpenSeed={onOpenSeed} />;
    case 'seeds':
      return <CrewSeeds member={member} onOpenSeed={onOpenSeed} {...seeds} />;
  }
}

function RestartConfirm({ member, summary, onCancel, onConfirm }: {
  member: CrewMember;
  summary: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const name = crewDisplayName(member.id);
  return (
    <div className="crew-confirm-backdrop">
      <div className="crew-confirm" role="alertdialog" aria-modal="true" aria-labelledby="crew-confirm-title">
        <span className="crew-kicker">Confirm</span>
        <h2 id="crew-confirm-title">{member.binding_session ? `Restart ${name}?` : `Wake ${name}?`}</h2>
        <div className="crew-confirm-selection">{summary}</div>
        <div className="crew-confirm-actions">
          <button type="button" onClick={onCancel}>Cancel</button>
          <button type="button" className="is-primary" data-testid="crew-confirm-restart" autoFocus onClick={onConfirm}>
            {member.binding_session ? 'Request handoff and restart' : 'Wake member'}
          </button>
        </div>
      </div>
    </div>
  );
}

interface CrewPanelStores {
  isConnected: boolean;
  autosave: CrewLaunchAutosave;
  charterAutosave: CrewCharterAutosave;
  handoffs: CrewHandoffHistory;
  restarts: CrewRestarts;
  catalog: ReturnType<typeof useHarnessCatalog>;
  loadModels: ReturnType<typeof useDaemonApi>['sendDelegationModels'];
}

export function CrewPanel({ visit, ...surface }: CrewPanelProps) {
  const {
    isConnected,
    connectionGeneration,
    sendCrewSet,
    sendCrewRestart,
    sendCrewCharterGet,
    sendCrewCharterSet,
    sendCrewHandoffsGet,
    sendCrewHandoffGet,
    sendDelegationPreferencesGet,
    sendDelegationModels,
  } = useDaemonApi();
  const autosave = useCrewLaunchAutosave(surface.members, connectionGeneration, sendCrewSet);
  const charterAutosave = useCrewCharterAutosave(connectionGeneration, sendCrewCharterGet, sendCrewCharterSet);
  const handoffs = useCrewHandoffs(connectionGeneration, sendCrewHandoffsGet, sendCrewHandoffGet);
  const restarts = useCrewRestart(sendCrewRestart, autosave.observe);
  const catalog = useHarnessCatalog(surface.isOpen, sendDelegationPreferencesGet);
  return (
    <CrewPanelSurface
      key={visit}
      {...surface}
      stores={{ isConnected, autosave, charterAutosave, handoffs, restarts, catalog, loadModels: sendDelegationModels }}
    />
  );
}

function CrewPanelSurface({
  isOpen,
  initialMember,
  members,
  sessions,
  seeds,
  seedsTotal,
  onClose,
  onOpenSeed,
  stores,
}: Omit<CrewPanelProps, 'visit'> & { stores: CrewPanelStores }) {
  const { isConnected, autosave, charterAutosave, handoffs, restarts, catalog, loadModels } = stores;
  const [filter, setFilter] = useState('');
  const [seedFilter, setSeedFilter] = useState<CrewSeedFilter>('tending');
  const [seedQuery, setSeedQuery] = useState('');
  const [confirming, setConfirming] = useState(false);
  const closeRef = useRef<HTMLButtonElement>(null);
  const rosterRef = useRef<HTMLDivElement>(null);
  const { selectedMember, tab, pending: navigationPending, navigate } = useCrewNavigation({
    members, initialMember, rosterRef, charter: charterAutosave, onClose, onOpenSeed,
  });
  const selectedMemberId = selectedMember?.id;
  const charterEdit = selectedMemberId ? charterAutosave.read(selectedMemberId) : undefined;

  const requestClose = useCallback(() => navigate({ kind: 'close' }), [navigate]);
  const openMemberSeed = useCallback((seedId: string) => navigate({ kind: 'seed', seedId }), [navigate]);

  useEscapeStack(requestClose, isOpen && !confirming);
  useEscapeStack(() => setConfirming(false), isOpen && confirming);

  const loadCharter = charterAutosave.load;
  const loadHandoffs = handoffs.load;
  useEffect(() => {
    if (!isOpen || !selectedMemberId) return;
    if (tab === 'charter') void loadCharter(selectedMemberId, true);
    if (tab === 'handoffs') loadHandoffs(selectedMemberId);
  }, [isOpen, loadCharter, loadHandoffs, selectedMemberId, tab]);

  const visibleMembers = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return members;
    return members.filter((member) => crewDisplayName(member.id).toLowerCase().includes(query));
  }, [filter, members]);

  const edit = selectedMemberId ? autosave.read(selectedMemberId) : undefined;
  const member = selectedMember ? effectiveMember(selectedMember, edit?.acknowledged) : undefined;

  const confirmRestart = () => {
    if (!member || edit?.state !== 'saved') return;
    setConfirming(false);
    restarts.start(member);
  };

  return (
    <div className={`crew-panel-layer ${isOpen ? 'is-open' : ''}`} aria-hidden={!isOpen}>
      <FocusTrap
        active={isOpen}
        focusTrapOptions={{
          escapeDeactivates: false,
          initialFocus: () => closeRef.current,
          returnFocusOnDeactivate: false,
        }}
      >
        <section className="crew-panel" data-testid="crew-panel" role="dialog" aria-modal="true" aria-labelledby="crew-panel-title">
          <header className="crew-panel-bar" inert={confirming}>
            <div>
              <span className="crew-kicker">Crew</span>
              <h1 id="crew-panel-title">Manage crew</h1>
            </div>
            <button ref={closeRef} type="button" className="crew-close" data-testid="crew-panel-close" disabled={navigationPending} onClick={requestClose}>Close <kbd>Esc</kbd></button>
          </header>
          {isOpen && <div className="crew-panel-shell" inert={confirming}>
            <CrewRoster
              members={members}
              visibleMembers={visibleMembers}
              selectedId={member?.id}
              filter={filter}
              listRef={rosterRef}
              saveState={(memberId) => autosave.read(memberId)?.state}
              onFilterChange={setFilter}
              onSelect={(memberId, rosterIndex) => navigate({ kind: 'member', memberId, rosterIndex })}
            />
            <main className="crew-member-detail">
              {!member || !edit ? (
                <div className="crew-empty">No crew members.</div>
              ) : (
                <>
                  <MemberHeading member={member} />
                  <MemberTabs tab={tab} onSelect={(next) => navigate({ kind: 'tab', tab: next })} />

                  <MemberTabPanel
                    tab={tab}
                    launch={{
                      member,
                      edit,
                      running: runningSessionFor(member, sessions),
                      harnesses: catalog.harnesses,
                      catalogLoading: catalog.loading,
                      catalogError: catalog.error,
                      onRetryCatalog: catalog.retry,
                      autosave,
                      loadModels,
                      isConnected,
                      restart: restarts.read(member),
                      onRestart: () => setConfirming(true),
                      onResendRestart: () => restarts.resend(member),
                      onReviewRestart: () => {
                        restarts.discard(member.id);
                        setConfirming(true);
                      },
                    }}
                    charterEdit={charterEdit}
                    charterAutosave={charterAutosave}
                    handoffs={handoffs}
                    seeds={{ seeds, seedsTotal, filter: seedFilter, onFilterChange: setSeedFilter, query: seedQuery, onQueryChange: setSeedQuery }}
                    onOpenSeed={openMemberSeed}
                  />
                </>
              )}
            </main>
          </div>}

          {confirming && member && edit && (
            <RestartConfirm member={member} summary={nextWakeLabel(edit)} onCancel={() => setConfirming(false)} onConfirm={confirmRestart} />
          )}
        </section>
      </FocusTrap>
    </div>
  );
}
