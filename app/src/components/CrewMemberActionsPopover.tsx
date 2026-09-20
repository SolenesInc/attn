import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './SessionActionsPopover.css';

interface CrewMemberActionsPopoverProps {
  memberName: string;
  anchor: { top: number; left: number };
  onOpenDetails: () => void;
  onClose: () => void;
}

const VIEWPORT_MARGIN = 8;

export function CrewMemberActionsPopover({
  memberName,
  anchor,
  onOpenDetails,
  onClose,
}: CrewMemberActionsPopoverProps) {
  const menuRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState(anchor);

  useEscapeStack(onClose, true);

  useLayoutEffect(() => {
    const menu = menuRef.current;
    if (!menu) return;
    const rect = menu.getBoundingClientRect();
    setPosition({
      top: Math.max(VIEWPORT_MARGIN, Math.min(anchor.top, window.innerHeight - rect.height - VIEWPORT_MARGIN)),
      left: Math.max(VIEWPORT_MARGIN, Math.min(anchor.left, window.innerWidth - rect.width - VIEWPORT_MARGIN)),
    });
    menu.querySelector<HTMLButtonElement>('button')?.focus();
  }, [anchor]);

  useEffect(() => {
    const dismiss = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) onClose();
    };
    const id = window.setTimeout(() => document.addEventListener('mousedown', dismiss), 0);
    return () => {
      window.clearTimeout(id);
      document.removeEventListener('mousedown', dismiss);
    };
  }, [onClose]);

  return (
    <div
      ref={menuRef}
      className="session-actions-popover"
      style={{ top: position.top, left: position.left }}
      role="menu"
      aria-label={`Actions for ${memberName}`}
    >
      <button type="button" role="menuitem" data-testid="crew-member-details-action" onClick={onOpenDetails}>
        <span aria-hidden="true">⌁</span>
        Member details
      </button>
    </div>
  );
}
