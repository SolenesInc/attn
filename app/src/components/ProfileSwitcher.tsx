import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { Profile } from '../types/generated';
import { PlusIcon } from './SidebarIcons';
import './ProfileSwitcher.css';

interface ProfileSwitcherProps {
  profiles: Profile[];
  selectedProfileId: string | null;
  onSelect: (profileId: string) => void;
  onCreate: (name: string) => Promise<void>;
  onRename: (profileId: string, name: string) => Promise<void>;
  onDelete: (profileId: string) => Promise<void>;
  onClose: () => void;
}

type Mode =
  | { kind: 'list' }
  | { kind: 'name'; profileId: string | null; draft: string }
  | { kind: 'delete'; profileId: string };

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

  const enterMode = (next: Mode) => {
    setMode(next);
    setError(null);
    menuRef.current?.focus({ preventScroll: true });
  };
  const startNew = () => enterMode({ kind: 'name', profileId: null, draft: '' });
  const startRename = (profile = focused) => profile && enterMode({ kind: 'name', profileId: profile.id, draft: profile.name });
  const startDelete = (profile = focused) => {
    if (!profile) return;
    if (ordered.length === 1) {
      setError(`${profile.name} is the last profile, and attn always keeps one.`);
      return;
    }
    setError(null);
    enterMode({ kind: 'delete', profileId: profile.id });
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
    if (event.key === 'Enter' && event.target === menuRef.current && !busy) {
      event.preventDefault();
      run(() => onDelete(deleting.profileId), backToList);
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
          <ProfileList ordered={ordered} focusedIndex={focusedIndex} selectedProfileId={selectedProfileId} onChoose={choose} onFocus={setFocusedId} onRename={startRename} onDelete={startDelete} />
        )}
        {mode.kind === 'name' && (
          <ProfileNameForm
            renaming={ordered.find((profile) => profile.id === mode.profileId)?.name ?? null}
            draft={mode.draft}
            busy={busy}
            onDraft={(draft) => setMode({ ...mode, draft })}
            onSubmit={() => submitName(mode.profileId, mode.draft)}
            onBack={backToList}
          />
        )}
        {mode.kind === 'delete' && (
          <ProfileDeleteForm
            deleting={ordered.find((profile) => profile.id === mode.profileId)?.name ?? ''}
            busy={busy}
            onConfirm={() => run(() => onDelete(mode.profileId), backToList)}
            onBack={backToList}
          />
        )}
        {error && <div className="profile-switcher-error" role="alert">{error}</div>}
        {mode.kind === 'list' && (
          <div className="profile-switcher-actions">
            <button type="button" onClick={startNew}><PlusIcon />New profile<kbd>N</kbd></button>
          </div>
        )}
      </div>
    </div>
  );
}

function ProfileList({ ordered, focusedIndex, selectedProfileId, onChoose, onFocus, onRename, onDelete }: {
  ordered: Profile[];
  focusedIndex: number;
  selectedProfileId: string | null;
  onChoose: (profileId: string) => void;
  onFocus: (profileId: string) => void;
  onRename: (profile: Profile) => void;
  onDelete: (profile: Profile) => void;
}) {
  return (
    <>
      {ordered.map((profile, index) => (
        <div key={profile.id} className={`profile-switcher-item ${index === focusedIndex ? 'focused' : ''}`}>
          <button
            type="button"
            role="menuitem"
            className="profile-switcher-choose"
            onFocus={() => onFocus(profile.id)}
            onClick={() => onChoose(profile.id)}
          >
            <span className="profile-switcher-selected" aria-label={profile.id === selectedProfileId ? 'Current profile' : undefined}>
              {profile.id === selectedProfileId ? '✓' : ''}
            </span>
            <span className="profile-switcher-name">{profile.name}</span>
          </button>
          <span className="profile-switcher-row-actions">
            <button type="button" aria-label={`Rename ${profile.name}`} onClick={() => { onFocus(profile.id); onRename(profile); }}>
              <PencilIcon /><kbd>R</kbd>
            </button>
            <button type="button" aria-label={`Delete ${profile.name}`} className="is-danger" onClick={() => { onFocus(profile.id); onDelete(profile); }}>
              <TrashIcon /><kbd className="symbol-key">⌫</kbd>
            </button>
          </span>
        </div>
      ))}
    </>
  );
}

function PencilIcon() {
  return <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" aria-hidden="true"><path d="m10.5 2.5 3 3-7.5 7.5-4 1 1-4zM9 4l3 3" /></svg>;
}

function TrashIcon() {
  return <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M2 4h12M6 4V2h4v2M4 4l.5 10h7L12 4M6.5 7v4M9.5 7v4" /></svg>;
}

function BackHint({ onBack }: { onBack: () => void }) {
  return <button type="button" className="profile-switcher-back" onClick={onBack}><kbd>esc</kbd>back</button>;
}

function ProfileNameForm({ renaming, draft, busy, onDraft, onSubmit, onBack }: {
  renaming: string | null;
  draft: string;
  busy: boolean;
  onDraft: (draft: string) => void;
  onSubmit: () => void;
  onBack: () => void;
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
          <kbd className="symbol-key">⏎</kbd>{renaming === null ? 'Create and switch' : 'Rename'}
        </button>
      </div>
      <BackHint onBack={onBack} />
    </form>
  );
}

function ProfileDeleteForm({ deleting, busy, onConfirm, onBack }: {
  deleting: string;
  busy: boolean;
  onConfirm: () => void;
  onBack: () => void;
}) {
  return (
    <>
      <div className="profile-switcher-label">Delete {deleting}? Clean up its agents, crew, automations and tiles first.</div>
      <div className="profile-switcher-actions">
        <button type="button" className="is-danger" disabled={busy} onClick={onConfirm}><kbd className="symbol-key">⏎</kbd>Delete {deleting}</button>
      </div>
      <BackHint onBack={onBack} />
    </>
  );
}
