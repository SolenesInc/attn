import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { SNOOZE_CHOICES, snoozeInstant, type SnoozeChoiceId } from '../utils/snoozeDurations';
import { clampIntoViewport } from '../utils/viewportClamp';
import './SnoozeMenu.css';

export type SnoozePlacement =
  | { kind: 'anchor'; top: number; left: number }
  | { kind: 'center'; pane: HTMLElement | null };

interface SnoozeMenuProps {
  sessionLabel: string;
  placement: SnoozePlacement;
  onSnooze: (until: Date) => void;
  onClose: () => void;
  onRestoreFocus: (reason: 'cancel' | 'choose' | 'removed') => void;
}

const VIEWPORT_MARGIN = 8;

// Freeze the clock on opening so the displayed wake time is the instant sent.
export function SnoozeMenu({ sessionLabel, placement, onSnooze, onClose, onRestoreFocus }: SnoozeMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState({ top: 0, left: 0 });
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [openedAt] = useState(() => new Date());
  const closeReason = useRef<'cancel' | 'choose' | 'outside' | null>(null);
  const restoreFocusRef = useRef(onRestoreFocus);
  useLayoutEffect(() => { restoreFocusRef.current = onRestoreFocus; }, [onRestoreFocus]);

  const close = useCallback((reason: 'cancel' | 'choose' | 'outside') => {
    if (closeReason.current) return;
    closeReason.current = reason;
    onClose();
  }, [onClose]);
  useEscapeStack(() => close('cancel'), true);

  useLayoutEffect(() => {
    const menu = menuRef.current;
    if (!menu) return;
    const positionMenu = () => {
      const rect = menu.getBoundingClientRect();
      const pane = placement.kind === 'center' ? placement.pane?.getBoundingClientRect() : null;
      const top = placement.kind === 'anchor' ? placement.top
        : (pane?.top ?? 0) + ((pane?.height ?? window.innerHeight) - rect.height) / 2;
      const left = placement.kind === 'anchor' ? placement.left
        : (pane?.left ?? 0) + ((pane?.width ?? window.innerWidth) - rect.width) / 2;
      setPosition({
        top: Math.max(VIEWPORT_MARGIN, Math.min(top, window.innerHeight - rect.height - VIEWPORT_MARGIN)),
        left: Math.max(VIEWPORT_MARGIN, Math.min(left, window.innerWidth - rect.width - VIEWPORT_MARGIN)),
      });
    };
    positionMenu();
    const observer = new ResizeObserver(positionMenu);
    observer.observe(menu);
    if (placement.kind === 'center' && placement.pane) observer.observe(placement.pane);
    window.addEventListener('resize', positionMenu);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', positionMenu);
    };
  }, [placement]);

  useEffect(() => {
    const handleMouseDown = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) close('outside');
    };
    document.addEventListener('mousedown', handleMouseDown);
    return () => document.removeEventListener('mousedown', handleMouseDown);
  }, [close]);

  const choose = (id: SnoozeChoiceId) => {
    if (closeReason.current) return;
    close('choose');
    onSnooze(snoozeInstant(id, openedAt));
  };
  const focusChoice = (index: number) => {
    menuRef.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')[index]?.focus();
  };

  return (
    <FocusTrap focusTrapOptions={{
      initialFocus: () => menuRef.current?.querySelector('[role="menuitem"]') as HTMLElement,
      delayInitialFocus: false,
      escapeDeactivates: false,
      allowOutsideClick: true,
      returnFocusOnDeactivate: false,
      // Restore only after the trap releases focus, before queue handover can run.
      onDeactivate: () => {
        if (closeReason.current !== 'outside') restoreFocusRef.current(closeReason.current ?? 'removed');
      },
    }}>
      <div
        ref={menuRef}
        className="snooze-menu"
        style={{ top: position.top, left: position.left }}
        role="menu"
        aria-label={`Snooze ${sessionLabel}`}
        data-testid="snooze-menu"
        onKeyDown={(event) => {
          if (event.key === 'ArrowDown') {
            event.preventDefault();
            focusChoice((selectedIndex + 1) % SNOOZE_CHOICES.length);
          } else if (event.key === 'ArrowUp') {
            event.preventDefault();
            focusChoice((selectedIndex + SNOOZE_CHOICES.length - 1) % SNOOZE_CHOICES.length);
          } else if (event.key === 'Home' || event.key === 'End') {
            event.preventDefault();
            focusChoice(event.key === 'Home' ? 0 : SNOOZE_CHOICES.length - 1);
          } else if (event.key === 'Tab') {
            event.preventDefault();
            focusChoice(selectedIndex);
          }
        }}
      >
        <div className="snooze-menu-header">Snooze {sessionLabel}</div>
        {SNOOZE_CHOICES.map((choice, index) => (
          <button
            key={choice.id}
            type="button"
            role="menuitem"
            className={index === selectedIndex ? 'is-selected' : undefined}
            tabIndex={index === selectedIndex ? 0 : -1}
            data-testid={`snooze-choice-${choice.id}`}
            onFocus={() => setSelectedIndex(index)}
            onPointerMove={(event) => event.currentTarget.focus()}
            onClick={() => choose(choice.id)}
          >
            <span className="snooze-menu-label">{choice.label}</span>
            <span className="snooze-menu-detail">{choice.detail(openedAt)}</span>
          </button>
        ))}
        <div className="snooze-menu-footer" aria-hidden="true">↑ ↓ choose · ↵ snooze · esc cancel</div>
      </div>
    </FocusTrap>
  );
}
