import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { Profile } from '../types/generated';
import './ProfileSwitcher.css';

interface ProfileSwitcherProps {
  profiles: Profile[];
  selectedProfileId: string | null;
  onSelect: (profileId: string) => void;
  onClose: () => void;
}

function byMostRecentUse(a: Profile, b: Profile): number {
  return (b.last_used_at ?? '').localeCompare(a.last_used_at ?? '');
}

export function ProfileSwitcher({ profiles, selectedProfileId, onSelect, onClose }: ProfileSwitcherProps) {
  const ordered = useMemo(() => [...profiles].sort(byMostRecentUse), [profiles]);
  const [focusedId, setFocusedId] = useState(selectedProfileId);
  const focusedIndex = Math.max(0, ordered.findIndex((profile) => profile.id === focusedId));
  const menuRef = useRef<HTMLDivElement>(null);

  useEscapeStack(onClose, true);
  useEffect(() => {
    menuRef.current?.focus({ preventScroll: true });
  }, []);

  const choose = (profileId: string) => {
    onSelect(profileId);
    onClose();
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (ordered.length === 0) return;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const step = event.key === 'ArrowDown' ? 1 : ordered.length - 1;
      setFocusedId(ordered[(focusedIndex + step) % ordered.length].id);
      return;
    }
    if (event.key === 'Enter' && event.target === menuRef.current) {
      event.preventDefault();
      choose(ordered[focusedIndex].id);
    }
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
        {ordered.map((profile, index) => (
          <button
            key={profile.id}
            type="button"
            role="menuitem"
            className={`profile-switcher-item ${index === focusedIndex ? 'focused' : ''}`}
            onClick={() => choose(profile.id)}
          >
            <span className="profile-switcher-name">{profile.name}</span>
            {profile.id === selectedProfileId && <span className="profile-switcher-selected">selected</span>}
          </button>
        ))}
      </div>
    </div>
  );
}
