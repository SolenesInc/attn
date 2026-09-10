import { useEffect, useLayoutEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { UISessionState } from '../types/sessionState';
import { StateIndicator } from './StateIndicator';
import type { PopoverAnchor } from './SessionPullRequestPopover';
import './SessionDelegatesPopover.css';

export interface SessionDelegateLink {
  id: string;
  label: string;
  agent: string;
  state: UISessionState;
}

const VIEWPORT_MARGIN = 8;

export function SessionDelegatesPopover({
  delegates,
  anchor,
  onSelectSession,
  onClose,
}: {
  delegates: readonly SessionDelegateLink[];
  anchor: PopoverAnchor;
  onSelectSession: (sessionId: string) => void;
  onClose: () => void;
}) {
  const cardRef = useRef<HTMLDialogElement>(null);

  useEscapeStack(onClose, true);

  useLayoutEffect(() => {
    const card = cardRef.current;
    if (!card) return;
    if (!card.open) card.show();
    const rect = card.getBoundingClientRect();
    card.style.top = `${Math.max(VIEWPORT_MARGIN, Math.min(anchor.top, window.innerHeight - rect.height - VIEWPORT_MARGIN))}px`;
    card.style.left = `${Math.max(VIEWPORT_MARGIN, Math.min(anchor.left, window.innerWidth - rect.width - VIEWPORT_MARGIN))}px`;
  }, [anchor, delegates.length]);

  useEffect(() => {
    cardRef.current?.focus();
    const handleMouseDown = (event: MouseEvent) => {
      if (cardRef.current && !cardRef.current.contains(event.target as Node)) onClose();
    };
    const id = window.setTimeout(() => document.addEventListener('mousedown', handleMouseDown), 0);
    return () => {
      window.clearTimeout(id);
      document.removeEventListener('mousedown', handleMouseDown);
    };
  }, [onClose]);

  return createPortal(
    <dialog
      ref={cardRef}
      className="session-delegates-popover"
      data-testid="session-delegates-popover"
      style={{ top: anchor.top, left: anchor.left }}
      aria-label="Delegates"
      tabIndex={-1}
      onPointerDown={(event) => event.stopPropagation()}
    >
      <ul aria-label="Sessions delegated from this session">
        {delegates.map((delegate) => (
          <li key={delegate.id}>
            <button
              type="button"
              onClick={() => {
                onSelectSession(delegate.id);
                onClose();
              }}
            >
              <StateIndicator state={delegate.state} size="sm" seed={delegate.id} />
              <span>{delegate.label}</span>
              <span className="session-delegates-popover__agent">{delegate.agent}</span>
            </button>
          </li>
        ))}
      </ul>
    </dialog>,
    document.body,
  );
}
