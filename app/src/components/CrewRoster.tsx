import type { KeyboardEvent, RefObject } from 'react';
import type { CrewLaunchSaveState } from '../hooks/useCrewLaunchAutosave';
import type { CrewMember } from '../types/generated';

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
    const current = Math.max(0, visibleMembers.findIndex((candidate) => candidate.key === selectedId));
    const offset = event.key === 'ArrowDown' ? 1 : -1;
    const next = Math.max(0, Math.min(visibleMembers.length - 1, current + offset));
    const nextMember = visibleMembers[next];
    if (nextMember) onSelect(nextMember.key, next);
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
          const state = saveState(candidate.key);
          return (
            <button
              type="button"
              key={candidate.key}
              data-crew-roster-member={candidate.key}
              data-testid={`crew-roster-${candidate.key}`}
              aria-current={candidate.key === selectedId ? 'true' : undefined}
              className={candidate.key === selectedId ? 'is-selected' : ''}
              onClick={() => onSelect(candidate.key)}
            >
              <span className="crew-avatar" aria-hidden="true">{candidate.name.slice(0, 1)}</span>
              <span className="crew-roster-identity">
                <strong>{candidate.name}</strong>
                <small>{candidate.launch_desktop?.label}</small>
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
