import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { Setup } from '../types/generated';
import './SetupSwitcher.css';

interface SetupSwitcherProps {
  setups: Setup[];
  selectedSetupId: string | null;
  onSelect: (setupId: string) => void;
  onClose: () => void;
}

function byMostRecentUse(a: Setup, b: Setup): number {
  return (b.last_used_at ?? '').localeCompare(a.last_used_at ?? '');
}

export function SetupSwitcher({ setups, selectedSetupId, onSelect, onClose }: SetupSwitcherProps) {
  const ordered = useMemo(() => [...setups].sort(byMostRecentUse), [setups]);
  const [focusedId, setFocusedId] = useState(selectedSetupId);
  const focusedIndex = Math.max(0, ordered.findIndex((setup) => setup.id === focusedId));
  const menuRef = useRef<HTMLDivElement>(null);

  useEscapeStack(onClose, true);
  useEffect(() => {
    menuRef.current?.focus({ preventScroll: true });
  }, []);

  const choose = (setupId: string) => {
    onSelect(setupId);
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
    if (event.key === 'Enter') {
      event.preventDefault();
      choose(ordered[focusedIndex].id);
    }
  };

  return (
    <div className="setup-switcher-scrim">
      <button type="button" className="setup-switcher-dismiss" aria-label="Close the profile list" onClick={onClose} />
      <div
        ref={menuRef}
        className="setup-switcher"
        role="menu"
        aria-label="Switch profile"
        tabIndex={-1}
        onKeyDown={handleKeyDown}
      >
        {ordered.map((setup, index) => (
          <button
            key={setup.id}
            type="button"
            role="menuitem"
            className={`setup-switcher-item ${index === focusedIndex ? 'focused' : ''}`}
            onClick={() => choose(setup.id)}
          >
            <span className="setup-switcher-name">{setup.name}</span>
            {setup.id === selectedSetupId && <span className="setup-switcher-selected">selected</span>}
          </button>
        ))}
      </div>
    </div>
  );
}
