import {
  createContext, forwardRef, useCallback, useContext, useEffect, useId,
  useImperativeHandle, useLayoutEffect, useMemo, useRef, useState,
  type CSSProperties, type ReactNode,
} from 'react';
import { createPortal, flushSync } from 'react-dom';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { delegationTree, hasDelegationChain, type DelegationSession } from '../utils/delegationLinks';
import type { UISessionState } from '../types/sessionState';
import type { SessionDelegationRole } from '../types/generated';
import { DelegationRoleIcon } from './DelegationRoleIcon';
import { StateIndicator } from './StateIndicator';
import { harnessLabel } from './harnessLabel';
import './DelegationChain.css';

export interface ChainSession extends DelegationSession {
  agent?: string;
  state: UISessionState;
}

export interface DelegationChainHandle {
  open: (sessionId: string, returnFocus?: HTMLElement | null) => void;
  dismiss: () => HTMLElement | null;
}

interface OpenChain {
  sessionId: string;
  navigationKey: string;
  anchor: HTMLElement | null;
  pinned: boolean;
  returnFocus: HTMLElement | null;
}

interface ChainController {
  openSessionId: string | null;
  pinned: boolean;
  show: (sessionId: string, anchor: HTMLElement, pinned: boolean) => void;
  leave: (sessionId: string) => void;
  close: () => void;
}

const ChainContext = createContext<ChainController | null>(null);
const HOVER_CLOSE_DELAY_MS = 120;

export function useDelegationChainControl() {
  return useContext(ChainContext);
}

export function SessionRoleIcon({ role }: { role?: SessionDelegationRole }) {
  if (!role) return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true">
      <circle cx="4" cy="3" r="2" /><circle cx="4" cy="13" r="2" /><circle cx="12" cy="3" r="2" />
      <path d="M4 5v6m0-3h4a4 4 0 0 0 4-4" />
    </svg>
  );
  const builtinIcons = { pathfinder: 'search', builder: 'code', reviewer: 'list', orchestrator: 'spark' };
  return <DelegationRoleIcon icon={role.icon || (role.builtin ? builtinIcons[role.builtin] : '')} name={role.name} />;
}

export function DelegationChainTrigger({ session, hasDelegates = false, variant = 'sidebar', onOpen }: {
  session: DelegationSession;
  hasDelegates?: boolean;
  variant?: 'sidebar' | 'header';
  onOpen?: () => void;
}) {
  const controller = useContext(ChainContext);
  if (!hasDelegationChain(session, hasDelegates)) return null;
  const role = session.delegation_role;
  const label = `${role ? `${role.name} · ` : ''}Show delegation chain for ${session.label}`;
  return (
    <button
      type="button"
      className={`delegation-chain-trigger delegation-chain-trigger--${variant}`}
      data-testid={`delegation-chain-trigger-${session.id}`}
      data-delegation-session={session.id}
      data-role={role?.builtin}
      aria-label={label}
      aria-haspopup="dialog"
      aria-expanded={controller?.openSessionId === session.id}
      onPointerDown={(event) => event.stopPropagation()}
      onMouseDown={(event) => { event.preventDefault(); event.stopPropagation(); }}
      onPointerEnter={(event) => {
        if (event.pointerType !== 'touch') {
          onOpen?.();
          controller?.show(session.id, event.currentTarget, false);
        }
      }}
      onPointerLeave={() => controller?.leave(session.id)}
      onClick={(event) => {
        event.stopPropagation();
        onOpen?.();
        controller?.show(session.id, event.currentTarget, true);
      }}
    >
      <SessionRoleIcon role={role} />
      {variant === 'header' && <span>{role?.name ?? 'Delegation chain'}</span>}
    </button>
  );
}

export const DelegationChainProvider = forwardRef<DelegationChainHandle, {
  sessions: readonly ChainSession[];
  onSelectSession: (sessionId: string) => void;
  navigationKey?: string;
  blocked?: boolean;
  children: ReactNode;
}>(function DelegationChainProvider({ sessions, onSelectSession, navigationKey = '', blocked = false, children }, ref) {
  const [open, setOpen] = useState<OpenChain | null>(null);
  const focusContext = useRef({ navigationKey, blocked });
  useLayoutEffect(() => { focusContext.current = { navigationKey, blocked }; }, [navigationKey, blocked]);
  const canRestoreFocus = useCallback((key: string) => !focusContext.current.blocked && focusContext.current.navigationKey === key, []);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const hoverSuppressed = useRef(false);
  const pointerPosition = useRef<{ x: number; y: number } | null>(null);
  useEffect(() => {
    const moved = (event: PointerEvent) => {
      if (event.clientX === pointerPosition.current?.x && event.clientY === pointerPosition.current?.y) return;
      pointerPosition.current = { x: event.clientX, y: event.clientY };
      hoverSuppressed.current = false;
    };
    window.addEventListener('pointermove', moved, true);
    window.addEventListener('pointerover', moved, true);
    window.addEventListener('pointerout', moved, true);
    return () => {
      window.removeEventListener('pointermove', moved, true);
      window.removeEventListener('pointerover', moved, true);
      window.removeEventListener('pointerout', moved, true);
    };
  }, []);
  const cancelClose = useCallback(() => {
    if (closeTimer.current !== null) clearTimeout(closeTimer.current);
    closeTimer.current = null;
  }, []);
  useEffect(() => cancelClose, [cancelClose]);
  const close = useCallback(() => {
    cancelClose();
    hoverSuppressed.current = true;
    setOpen(null);
  }, [cancelClose]);
  const show = useCallback((sessionId: string, anchor: HTMLElement, pinned: boolean) => {
    if (blocked) return;
    if (!pinned && hoverSuppressed.current) return;
    cancelClose();
    setOpen((current) => {
      if (current?.pinned && !pinned) return current;
      if (current?.sessionId === sessionId && current.pinned === pinned) return current;
      const returnFocus = pinned ? anchor : current?.returnFocus
        ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
      return { sessionId, navigationKey, anchor, pinned, returnFocus };
    });
  }, [cancelClose, navigationKey, blocked]);
  const leave = useCallback((sessionId: string) => {
    cancelClose();
    closeTimer.current = setTimeout(() => setOpen((current) => current?.pinned || current?.sessionId !== sessionId ? current : null), HOVER_CLOSE_DELAY_MS);
  }, [cancelClose]);
  useImperativeHandle(ref, () => ({
    open(sessionId, returnFocus) {
      cancelClose();
      const anchor = Array.from(document.querySelectorAll<HTMLElement>('.delegation-chain-trigger--header'))
        .find((element) => element.dataset.delegationSession === sessionId && element.getClientRects().length > 0) ?? null;
      setOpen({ sessionId, navigationKey, anchor, pinned: true, returnFocus: returnFocus ?? anchor });
    },
    dismiss() {
      close();
      return open?.returnFocus ?? null;
    },
  }), [cancelClose, close, navigationKey, open?.returnFocus]);
  const visible = open && !blocked && open.navigationKey === navigationKey ? open : null;
  const controller = useMemo(() => ({ openSessionId: visible?.sessionId ?? null, pinned: visible?.pinned ?? false, show, leave, close }), [visible?.sessionId, visible?.pinned, show, leave, close]);
  const current = sessions.find((session) => session.id === open?.sessionId);
  useEffect(() => {
    if (open && (!current || !visible)) close();
  }, [open, current, visible, close]);
  return (
    <ChainContext.Provider value={controller}>
      {children}
      {visible && current && (
        <DelegationChainPopover
          key={visible.sessionId}
          open={visible}
          sessions={sessions}
          onClose={close}
          canRestoreFocus={canRestoreFocus}
          onSelectSession={(id) => { flushSync(close); onSelectSession(id); }}
          onPointerEnter={cancelClose}
          onPointerLeave={() => leave(visible.sessionId)}
        />
      )}
    </ChainContext.Provider>
  );
});

function DelegationChainPopover({ open, sessions, onClose, canRestoreFocus, onSelectSession, onPointerEnter, onPointerLeave }: {
  open: OpenChain;
  sessions: readonly ChainSession[];
  onClose: () => void;
  canRestoreFocus: (navigationKey: string) => boolean;
  onSelectSession: (id: string) => void;
  onPointerEnter: () => void;
  onPointerLeave: () => void;
}) {
  const card = useRef<HTMLDivElement>(null);
  const restoreFocus = useRef(true);
  const returnFocusTarget = useRef(open.returnFocus);
  const titleId = useId();
  const tree = useMemo(() => delegationTree(open.sessionId, sessions), [open.sessionId, sessions]);
  const initialFocus = useCallback(() => card.current?.querySelector<HTMLElement>('[aria-current="true"]') ?? card.current!, []);
  useEscapeStack(onClose, true);
  useLayoutEffect(() => { returnFocusTarget.current = open.returnFocus; }, [open.returnFocus]);
  useLayoutEffect(() => {
    const position = () => {
      const element = card.current;
      if (!element) return;
      const bounds = element.getBoundingClientRect();
      const anchor = open.anchor?.getBoundingClientRect();
      const sidebar = open.anchor?.closest('.sidebar') ?? open.anchor?.closest('.session-item');
      const left = anchor ? (sidebar ? sidebar.getBoundingClientRect().right + 4 : anchor.left) : (window.innerWidth - bounds.width) / 2;
      const top = anchor ? (sidebar ? anchor.top : anchor.bottom + 4) : (window.innerHeight - bounds.height) / 2;
      element.style.left = `${Math.max(8, Math.min(left, window.innerWidth - bounds.width - 8))}px`;
      element.style.top = `${Math.max(8, Math.min(top, window.innerHeight - bounds.height - 8))}px`;
    };
    const closeOnOutsideScroll = (event: Event) => {
      if (event.target instanceof Node && card.current?.contains(event.target)) return;
      restoreFocus.current = false;
      onClose();
    };
    position();
    window.addEventListener('resize', position);
    window.addEventListener('scroll', closeOnOutsideScroll, true);
    return () => {
      window.removeEventListener('resize', position);
      window.removeEventListener('scroll', closeOnOutsideScroll, true);
    };
  }, [open.anchor, tree.rows.length, onClose]);
  useEffect(() => {
    const outside = (event: PointerEvent) => {
      if (!card.current?.contains(event.target as Node) && !open.anchor?.contains(event.target as Node)) {
        restoreFocus.current = false;
        onClose();
      }
    };
    document.addEventListener('pointerdown', outside, true);
    return () => document.removeEventListener('pointerdown', outside, true);
  }, [onClose, open.anchor]);
  return createPortal(
    <FocusTrap focusTrapOptions={{
      initialFocus, fallbackFocus: initialFocus, escapeDeactivates: false, delayInitialFocus: false, preventScroll: true,
      allowOutsideClick: true, setReturnFocus: () => restoreFocus.current && canRestoreFocus(open.navigationKey) && returnFocusTarget.current?.isConnected ? returnFocusTarget.current : false,
    }}>
      <div
        ref={card}
        className="delegation-chain-popover"
        role="dialog"
        aria-label="Delegation chain"
        aria-describedby={titleId}
        data-testid="delegation-chain-popover"
        tabIndex={-1}
        onPointerEnter={onPointerEnter}
        onPointerLeave={onPointerLeave}
        onPointerDown={(event) => event.stopPropagation()}
        onMouseDown={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
          event.preventDefault();
          const buttons = Array.from(card.current?.querySelectorAll<HTMLButtonElement>('[data-chain-session]') ?? []);
          const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
          const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1
            : Math.max(0, Math.min(buttons.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
          buttons[next]?.focus();
        }}
      >
        <div className="delegation-chain-heading">
          <strong>{sessions.find((session) => session.id === open.sessionId)?.label}</strong>
          <button type="button" aria-label="Close delegation chain" onClick={onClose}>×</button>
        </div>
        <p id={titleId}>Delegation chain</p>
        {tree.earlierDispatcher && <div className="delegation-chain-earlier">↑ {tree.earlierDispatcher} · unavailable</div>}
        <ul aria-label="Agents in delegation chain">
          {tree.rows.map(({ session, depth }) => (
            <li key={session.id} style={{ '--chain-depth': depth } as CSSProperties}>
              <button
                type="button"
                data-chain-session={session.id}
                aria-current={session.id === open.sessionId ? 'true' : undefined}
                onClick={() => { restoreFocus.current = false; onSelectSession(session.id); }}
              >
                <span className="delegation-chain-branch" aria-hidden="true">{depth > 0 ? '↳' : ''}</span>
                <span className="delegation-chain-role-icon" data-role={session.delegation_role?.builtin}><SessionRoleIcon role={session.delegation_role} /></span>
                <span className="delegation-chain-agent"><span>{session.label}</span><small>{session.delegation_role?.name ?? harnessLabel(session.agent)}</small></span>
                {session.id === open.sessionId && <span className="delegation-chain-current">current</span>}
                <StateIndicator state={session.state} size="sm" seed={session.id} />
              </button>
            </li>
          ))}
        </ul>
        <div className="delegation-chain-footer"><span>↑ ↓ navigate</span><span>↵ open</span><span>esc close</span></div>
      </div>
    </FocusTrap>,
    document.body,
  );
}
