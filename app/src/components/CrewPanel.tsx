import FocusTrap from './AppFocusTrap';
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useCrewCharterAutosave, type CrewCharterAutosave, type CrewCharterEdit } from '../hooks/useCrewCharterAutosave';
import { useCrewHandoffs, type CrewHandoffHistory } from '../hooks/useCrewHandoffs';
import { useCrewLaunchAutosave } from '../hooks/useCrewLaunchAutosave';
import { useCrewNavigation, type CrewTab } from '../hooks/useCrewNavigation';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { DaemonSession, Seed } from '../hooks/useDaemonSocket';
import { useHarnesses } from '../hooks/useHarnesses';
import type { CrewMember } from '../types/generated';
import { CrewCharterTab } from './CrewCharterTab';
import { CrewHandoffsTab } from './CrewHandoffsTab';
import { CrewLaunchTab, type CrewLaunchTabProps } from './CrewLaunchTab';
import { CrewRoster } from './CrewRoster';
import { CrewSeeds, type CrewSeedsProps } from './CrewSeeds';
import { effectiveMember, runningSessionFor } from './crewLaunchPresentation';
import type { CrewSeedFilter } from './crewSeedOwnership';
import './CrewPanel.css';

type CrewLaunchAutosave = ReturnType<typeof useCrewLaunchAutosave>;

interface CrewPanelProps {
  isOpen: boolean;
  canRename: boolean;
  visit: number;
  initialMember?: string;
  members: CrewMember[];
  sessions: DaemonSession[];
  seeds: Seed[];
  seedsTotal: number;
  onClose: () => void;
  onWakeMember: (member: string) => void;
  onSleepMember: (member: string) => void;
  onOpenSeed: (seedId: string, placementSessionId?: string) => void;
}

const tabs: { id: CrewTab; label: string }[] = [
  { id: 'launch', label: 'Launch settings' },
  { id: 'charter', label: 'Charter' },
  { id: 'handoffs', label: 'Handoffs' },
  { id: 'seeds', label: 'Seeds' },
];


function MemberHeading({ member, canRename }: { member: CrewMember; canRename: boolean }) {
  const [renaming, setRenaming] = useState(false);
  const awake = Boolean(member.binding_session);
  return (
    <div className="crew-member-heading">
      <div className="crew-member-identity">
        <span className="crew-avatar is-large" aria-hidden="true">{member.name.slice(0, 1)}</span>
        <div>
          {renaming ? (
            <MemberRename key={member.id} member={member} onDone={() => setRenaming(false)} />
          ) : (
            <h2>
              {member.name}
              {canRename && (
                <button type="button" className="crew-rename" data-testid="crew-rename" onClick={() => setRenaming(true)}>Rename</button>
              )}
            </h2>
          )}
          <span>{awake ? 'Awake' : 'Asleep'}</span>
        </div>
      </div>
      <span className={`crew-presence ${awake ? 'is-awake' : ''}`}>{awake ? 'Current day active' : 'Between days'}</span>
    </div>
  );
}

function MemberRename({ member, onDone }: { member: CrewMember; onDone: () => void }) {
  const { sendCrewRename } = useDaemonApi();
  const [draft, setDraft] = useState(member.name);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  useEscapeStack(onDone, !saving);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const name = draft.trim();
    if (name === member.name) {
      onDone();
      return;
    }
    setSaving(true);
    try {
      await sendCrewRename(member.id, name);
      onDone();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
      setSaving(false);
    }
  };
  return (
    <form className="crew-rename-form" onSubmit={(event) => void submit(event)}>
      <input
        autoFocus
        aria-label={`New name for ${member.name}`}
        data-testid="crew-rename-input"
        value={draft}
        disabled={saving}
        onChange={(event) => { setDraft(event.target.value); setError(undefined); }}
      />
      {error && <span role="alert" className="crew-rename-error">{error}</span>}
    </form>
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

interface CrewPanelStores {
  isConnected: boolean;
  autosave: CrewLaunchAutosave;
  charterAutosave: CrewCharterAutosave;
  handoffs: CrewHandoffHistory;
  catalog: ReturnType<typeof useHarnesses>;
  loadModels: ReturnType<typeof useDaemonApi>['sendHarnessModels'];
}

export function CrewPanel({ visit, ...surface }: CrewPanelProps) {
  const {
    isConnected,
    connectionGeneration,
    sendCrewSet,
    sendCrewCharterGet,
    sendCrewCharterSet,
    sendCrewHandoffsGet,
    sendCrewHandoffGet,
    sendHarnessModels,
  } = useDaemonApi();
  const autosave = useCrewLaunchAutosave(surface.members, connectionGeneration, sendCrewSet);
  const charterAutosave = useCrewCharterAutosave(connectionGeneration, sendCrewCharterGet, sendCrewCharterSet);
  const handoffs = useCrewHandoffs(connectionGeneration, sendCrewHandoffsGet, sendCrewHandoffGet);
  const catalog = useHarnesses(surface.isOpen);
  return (
    <CrewPanelSurface
      key={visit}
      {...surface}
      stores={{ isConnected, autosave, charterAutosave, handoffs, catalog, loadModels: sendHarnessModels }}
    />
  );
}

function CrewPanelSurface({
  isOpen,
  canRename,
  initialMember,
  members,
  sessions,
  seeds,
  seedsTotal,
  onClose,
  onWakeMember,
  onSleepMember,
  onOpenSeed,
  stores,
}: Omit<CrewPanelProps, 'visit'> & { stores: CrewPanelStores }) {
  const { isConnected, autosave, charterAutosave, handoffs, catalog, loadModels } = stores;
  const [filter, setFilter] = useState('');
  const [seedFilter, setSeedFilter] = useState<CrewSeedFilter>('tending');
  const [seedQuery, setSeedQuery] = useState('');
  const closeRef = useRef<HTMLButtonElement>(null);
  const rosterRef = useRef<HTMLDivElement>(null);
  const { selectedMember, tab, pending: navigationPending, navigate } = useCrewNavigation({
    members, initialMember, rosterRef, charter: charterAutosave, onClose, onOpenSeed,
  });
  const selectedMemberId = selectedMember?.id;
  const charterEdit = selectedMemberId ? charterAutosave.read(selectedMemberId) : undefined;

  const requestClose = useCallback(() => navigate({ kind: 'close' }), [navigate]);
  const openMemberSeed = useCallback((seedId: string) => navigate({ kind: 'seed', seedId }), [navigate]);

  useEscapeStack(requestClose, isOpen);

  const loadCharter = charterAutosave.load;
  const loadHandoffs = handoffs.load;
  useEffect(() => {
    if (isOpen && selectedMemberId && tab === 'charter') void loadCharter(selectedMemberId);
  }, [isOpen, loadCharter, selectedMemberId, tab]);
  useEffect(() => {
    if (isOpen && selectedMemberId && tab === 'handoffs') loadHandoffs(selectedMemberId);
  }, [isOpen, loadHandoffs, selectedMemberId, tab]);

  const visibleMembers = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return members;
    return members.filter((member) => member.name.toLowerCase().includes(query));
  }, [filter, members]);

  const edit = selectedMemberId ? autosave.read(selectedMemberId) : undefined;
  const member = selectedMember ? effectiveMember(selectedMember, edit?.acknowledged) : undefined;

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
        <dialog open={isOpen} className="crew-panel" data-testid="crew-panel" aria-modal="true" aria-labelledby="crew-panel-title">
          <header className="crew-panel-bar">
            <div>
              <span className="crew-kicker">Crew</span>
              <h1 id="crew-panel-title">Manage crew</h1>
            </div>
            <button ref={closeRef} type="button" className="crew-close" data-testid="crew-panel-close" disabled={navigationPending} onClick={requestClose}>Close <kbd>Esc</kbd></button>
          </header>
          {isOpen && <div className="crew-panel-shell">
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
                  <MemberHeading key={member.id} member={member} canRename={canRename} />
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
                      onSleep: () => onSleepMember(member.id),
                      onWake: () => onWakeMember(member.id),
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
        </dialog>
      </FocusTrap>
    </div>
  );
}
