import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { Profile } from '../types/generated';
import './ProfileSwitcher.css';

interface ProfileSwitcherProps {
  profiles: Profile[];
  selectedProfileId: string | null;
  onSelect: (profileId: string) => void;
  onCreate: (name: string) => Promise<void>;
  onRename: (profileId: string, name: string) => Promise<void>;
  onDelete: (profileId: string, destinationProfileId: string) => Promise<void>;
  onClose: () => void;
}

type Mode =
  | { kind: 'list' }
  | { kind: 'name'; profileId: string | null; draft: string }
  | { kind: 'delete'; profileId: string; destinationId: string };

function byMostRecentUse(a: Profile, b: Profile): number {
  return (b.last_used_at ?? '').localeCompare(a.last_used_at ?? '');
}

function failureText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function stepThrough<T extends { id: string }>(items: T[], currentId: string | null, key: string): string | null {
  if (items.length === 0) return null;
  const index = Math.max(0, items.findIndex((item) => item.id === currentId));
  const step = key === 'ArrowDown' ? 1 : items.length - 1;
  return items[(index + step) % items.length].id;
}

export function ProfileSwitcher({ profiles, selectedProfileId, onSelect, onCreate, onRename, onDelete, onClose }: ProfileSwitcherProps) {
  const ordered = useMemo(() => [...profiles].sort(byMostRecentUse), [profiles]);
  const [focusedId, setFocusedId] = useState(selectedProfileId);
  const focusedIndex = Math.max(0, ordered.findIndex((profile) => profile.id === focusedId));
  const focused = ordered[focusedIndex];
  const [mode, setMode] = useState<Mode>({ kind: 'list' });
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  const backToList = () => {
    setMode({ kind: 'list' });
    setError(null);
    menuRef.current?.focus({ preventScroll: true });
  };

  useEscapeStack(() => (mode.kind === 'list' ? onClose() : backToList()), true);
  useEffect(() => {
    menuRef.current?.focus({ preventScroll: true });
  }, []);

  const choose = (profileId: string) => {
    onSelect(profileId);
    onClose();
  };

  const run = (action: () => Promise<void>, after: () => void) => {
    setBusy(true);
    setError(null);
    action()
      .then(after)
      .catch((err) => setError(failureText(err)))
      .finally(() => setBusy(false));
  };

  const startNew = () => setMode({ kind: 'name', profileId: null, draft: '' });
  const startRename = () => focused && setMode({ kind: 'name', profileId: focused.id, draft: focused.name });
  const startDelete = () => {
    if (!focused) return;
    const destination = ordered.find((profile) => profile.id !== focused.id);
    if (!destination) {
      setError(`${focused.name} is the last profile, and attn always keeps one.`);
      return;
    }
    setError(null);
    setMode({ kind: 'delete', profileId: focused.id, destinationId: destination.id });
  };

  const submitName = (profileId: string | null, draft: string) => {
    if (profileId === null) run(() => onCreate(draft), onClose);
    else run(() => onRename(profileId, draft), backToList);
  };

  const handleListKey = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      setFocusedId(stepThrough(ordered, focused?.id ?? null, event.key));
      return;
    }
    if (event.key === 'Enter' && event.target === menuRef.current && focused) {
      event.preventDefault();
      choose(focused.id);
      return;
    }
    const letter = event.key.toLowerCase();
    if (letter === 'n') { event.preventDefault(); startNew(); }
    if (letter === 'r') { event.preventDefault(); startRename(); }
    if (event.key === 'Backspace' || event.key === 'Delete') { event.preventDefault(); startDelete(); }
  };

  const handleDeleteKey = (event: KeyboardEvent<HTMLDivElement>, deleting: Extract<Mode, { kind: 'delete' }>) => {
    const destinations = ordered.filter((profile) => profile.id !== deleting.profileId);
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const next = stepThrough(destinations, deleting.destinationId, event.key);
      if (next) setMode({ ...deleting, destinationId: next });
      return;
    }
    if (event.key === 'Enter' && event.target === menuRef.current && !busy) {
      event.preventDefault();
      run(() => onDelete(deleting.profileId, deleting.destinationId), backToList);
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (mode.kind === 'list') handleListKey(event);
    if (mode.kind === 'delete') handleDeleteKey(event, mode);
  };

  return (
    <div className="profile-switcher-scrim">
      <button type="button" className="profile-switcher-dismiss" aria-label="Close the profile list" onClick={onClose} />
      <div
        ref={menuRef}
        className="profile-switcher"
        role="menu"
        aria-label="Switch profile"
        tabIndex={-1}
        onKeyDown={handleKeyDown}
      >
        {mode.kind === 'list' && (
          <ProfileList ordered={ordered} focusedIndex={focusedIndex} selectedProfileId={selectedProfileId} onChoose={choose} />
        )}
        {mode.kind === 'name' && (
          <ProfileNameForm
            renaming={ordered.find((profile) => profile.id === mode.profileId)?.name ?? null}
            draft={mode.draft}
            busy={busy}
            onDraft={(draft) => setMode({ ...mode, draft })}
            onSubmit={() => submitName(mode.profileId, mode.draft)}
          />
        )}
        {mode.kind === 'delete' && (
          <ProfileDeleteForm
            deleting={ordered.find((profile) => profile.id === mode.profileId)?.name ?? ''}
            destinations={ordered.filter((profile) => profile.id !== mode.profileId)}
            destinationId={mode.destinationId}
            busy={busy}
            onPick={(destinationId) => setMode({ ...mode, destinationId })}
            onConfirm={() => run(() => onDelete(mode.profileId, mode.destinationId), backToList)}
          />
        )}
        {error && <div className="profile-switcher-error" role="alert">{error}</div>}
        {mode.kind === 'list' && (
          <div className="profile-switcher-actions">
            <button type="button" onClick={startNew}><kbd>N</kbd>New</button>
            <button type="button" onClick={startRename} disabled={!focused}><kbd>R</kbd>Rename</button>
            <button type="button" onClick={startDelete} disabled={!focused}><kbd>⌫</kbd>Delete</button>
          </div>
        )}
      </div>
    </div>
  );
}

function ProfileList({ ordered, focusedIndex, selectedProfileId, onChoose }: {
  ordered: Profile[];
  focusedIndex: number;
  selectedProfileId: string | null;
  onChoose: (profileId: string) => void;
}) {
  return (
    <>
      {ordered.map((profile, index) => (
        <button
          key={profile.id}
          type="button"
          role="menuitem"
          className={`profile-switcher-item ${index === focusedIndex ? 'focused' : ''}`}
          onClick={() => onChoose(profile.id)}
        >
          <span className="profile-switcher-name">{profile.name}</span>
          {profile.id === selectedProfileId && <span className="profile-switcher-selected">selected</span>}
        </button>
      ))}
    </>
  );
}

function ProfileNameForm({ renaming, draft, busy, onDraft, onSubmit }: {
  renaming: string | null;
  draft: string;
  busy: boolean;
  onDraft: (draft: string) => void;
  onSubmit: () => void;
}) {
  const label = renaming === null ? 'New profile name' : `Rename ${renaming}`;
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    inputRef.current?.focus();
  }, []);
  return (
    <form
      className="profile-switcher-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (!busy && draft.trim()) onSubmit();
      }}
    >
      <label className="profile-switcher-label" htmlFor="profile-switcher-name">{label}</label>
      <input
        id="profile-switcher-name"
        className="profile-switcher-input"
        ref={inputRef}
        value={draft}
        disabled={busy}
        onChange={(event) => onDraft(event.target.value)}
      />
      <div className="profile-switcher-actions">
        <button type="submit" disabled={busy || !draft.trim()}>
          <kbd>⏎</kbd>{renaming === null ? 'Create and switch' : 'Rename'}
        </button>
      </div>
    </form>
  );
}

function ProfileDeleteForm({ deleting, destinations, destinationId, busy, onPick, onConfirm }: {
  deleting: string;
  destinations: Profile[];
  destinationId: string;
  busy: boolean;
  onPick: (destinationId: string) => void;
  onConfirm: () => void;
}) {
  return (
    <>
      <div className="profile-switcher-label">Delete {deleting}. Its agents, crew and automations move to:</div>
      {destinations.map((profile) => (
        <button
          key={profile.id}
          type="button"
          role="menuitemradio"
          aria-checked={profile.id === destinationId}
          className={`profile-switcher-item ${profile.id === destinationId ? 'focused' : ''}`}
          onClick={() => onPick(profile.id)}
        >
          <span className="profile-switcher-name">{profile.name}</span>
        </button>
      ))}
      <div className="profile-switcher-actions">
        <button type="button" className="is-danger" disabled={busy} onClick={onConfirm}><kbd>⏎</kbd>Delete {deleting}</button>
      </div>
    </>
  );
}
