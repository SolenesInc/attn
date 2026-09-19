import type { KeyboardEvent, RefObject } from 'react';
import type { CrewLaunchSaveState } from '../hooks/useCrewLaunchAutosave';
import type { CrewMember } from '../types/generated';
import { crewDisplayName } from '../utils/crewName';

export function CrewRoster({ members, visibleMembers, selectedId, filter, listRef, saveState, onFilterChange, onSelect }: {
  members: CrewMember[];
  visibleMembers: CrewMember[];
  selectedId?: string;
  filter: string;
  listRef: RefObject<HTMLDivElement | null>;
  saveState: (memberId: string) => CrewLaunchSaveState | undefined;
  onFilterChange: (filter: string) => void;
  onSelect: (memberId: string, rosterIndex?: number) => void;
}) {
  const moveSelection = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    const current = Math.max(0, visibleMembers.findIndex((candidate) => candidate.id === selectedId));
    const offset = event.key === 'ArrowDown' ? 1 : -1;
    const next = Math.max(0, Math.min(visibleMembers.length - 1, current + offset));
    const nextMember = visibleMembers[next];
    if (nextMember) onSelect(nextMember.id, next);
  };
  return (
    <aside className="crew-roster" aria-label="Crew roster">
      <div className="crew-roster-heading"><span>Members</span><span>{members.length}</span></div>
      {members.length > 5 && (
        <input value={filter} onChange={(event) => onFilterChange(event.target.value)} placeholder="Find a member…" aria-label="Find a member" />
      )}
      <div ref={listRef} className="crew-roster-list" onKeyDown={moveSelection}>
        {visibleMembers.map((candidate) => {
          const awake = Boolean(candidate.binding_session);
          const state = saveState(candidate.id);
          return (
            <button
              type="button"
              key={candidate.id}
              data-crew-roster-member={candidate.id}
              data-testid={`crew-roster-${candidate.id}`}
              aria-current={candidate.id === selectedId ? 'true' : undefined}
              className={candidate.id === selectedId ? 'is-selected' : ''}
              onClick={() => onSelect(candidate.id)}
            >
              <span className="crew-avatar" aria-hidden="true">{crewDisplayName(candidate.id).slice(0, 1)}</span>
              <span className="crew-roster-identity">
                <strong>{crewDisplayName(candidate.id)}</strong>
                <small><i className={awake ? 'is-awake' : ''} />{awake ? 'Awake' : 'Asleep'}</small>
              </span>
              {state !== 'saved' && <span className={`crew-roster-save is-${state}`} aria-label={state} />}
            </button>
          );
        })}
      </div>
    </aside>
  );
}
