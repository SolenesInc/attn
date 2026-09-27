import {
  forwardRef, useCallback, useEffect, useId, useImperativeHandle, useLayoutEffect,
  useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { delegationTree, hasDelegationChain, type DelegationSession } from '../utils/delegationLinks';
import type { UISessionState } from '../types/sessionState';
import type { SessionDelegationRole } from '../types/generated';
import { DelegationRoleIcon } from './DelegationRoleIcon';
import { StateIndicator } from './StateIndicator';
import { harnessLabel } from './harnessLabel';
import { DelegationChainController, type OpenChain } from './delegationChainController';
import { DelegationChainContext, useDelegationChainTrigger } from './delegationChainContext';
import { useDelegationChainPosition } from './useDelegationChainPosition';
import './DelegationChain.css';

export { useDelegationChainControl } from './delegationChainContext';

export interface ChainSession extends DelegationSession {
  agent?: string;
  state: UISessionState;
}

export type DelegationChainHandle = Pick<DelegationChainController, 'open' | 'dismiss' | 'prepareCommand'>;

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
  const trigger = useDelegationChainTrigger(session.id, 'badge', onOpen);
  if (!hasDelegationChain(session, hasDelegates)) return null;
  const role = session.delegation_role;
  const label = `${role ? `${role.name} · ` : ''}Show delegation chain for ${session.label}`;
  return (
    <button
      ref={trigger.ref}
      type="button"
      className={`delegation-chain-trigger delegation-chain-trigger--${variant}`}
      data-testid={`delegation-chain-trigger-${session.id}`}
      data-delegation-session={session.id}
      data-role={role?.builtin}
      aria-label={label}
      aria-haspopup="dialog"
      aria-expanded={trigger.expanded}
      onPointerDown={(event) => event.stopPropagation()}
      onMouseDown={(event) => { event.preventDefault(); event.stopPropagation(); }}
      onClick={(event) => { event.stopPropagation(); trigger.pin(event.currentTarget); }}
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
  onRestoreFocusFallback?: () => void;
  children: ReactNode;
}>(function DelegationChainProvider({ sessions, onSelectSession, navigationKey = '', blocked = false, onRestoreFocusFallback, children }, ref) {
  const [controller] = useState(() => new DelegationChainController());
  const open = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const sessionIds = useMemo(() => new Set(sessions.map((session) => session.id)), [sessions]);
  const visible = controller.isVisible(open, { navigationKey, blocked, sessionIds }) ? open : null;
  useLayoutEffect(() => {
    controller.configure({ navigationKey, blocked, sessionIds, onRestoreFocusFallback });
  }, [controller, navigationKey, blocked, sessionIds, onRestoreFocusFallback, open]);
  useEffect(() => controller.connect(), [controller]);
  useImperativeHandle(ref, () => controller, [controller]);
  const context = useMemo(() => ({ controller, open: visible }), [controller, visible]);
  return (
    <DelegationChainContext.Provider value={context}>
      {children}
      {visible && <DelegationChainPopover key={visible.sessionId} open={visible} sessions={sessions} controller={controller} onSelectSession={onSelectSession} />}
    </DelegationChainContext.Provider>
  );
});

function DelegationChainPopover({ open, sessions, controller, onSelectSession }: {
  open: OpenChain;
  sessions: readonly ChainSession[];
  controller: DelegationChainController;
  onSelectSession: (id: string) => void;
}) {
  const card = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const tree = useMemo(() => delegationTree(open.sessionId, sessions), [open.sessionId, sessions]);
  const initialFocus = useCallback(() => card.current?.querySelector<HTMLElement>('[aria-current="true"]') ?? card.current!, []);
  const setCard = useCallback((element: HTMLDivElement | null) => {
    card.current = element;
    controller.setPopover(element);
  }, [controller]);
  useEscapeStack(() => controller.dismiss(), true);
  useDelegationChainPosition(card, open.anchor, tree.rows.length);
  return createPortal(
    <FocusTrap focusTrapOptions={{
      initialFocus, fallbackFocus: initialFocus, escapeDeactivates: false, delayInitialFocus: false, preventScroll: true,
      allowOutsideClick: true, setReturnFocus: () => controller.returnFocus(open.focus),
      onPostDeactivate: () => controller.finishClose(open.focus),
    }}>
      <div
        ref={setCard}
        className="delegation-chain-popover"
        role="dialog"
        aria-label="Delegation chain"
        aria-describedby={titleId}
        data-testid="delegation-chain-popover"
        tabIndex={-1}
        onPointerEnter={controller.cancelClose}
        onPointerLeave={() => controller.leave(open.sessionId, open.anchor)}
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
          <button type="button" aria-label="Close delegation chain" onClick={() => controller.dismiss()}>×</button>
        </div>
        <p id={titleId}>Delegation chain</p>
        {tree.earlierDispatcher && <div className="delegation-chain-earlier">↑ {tree.earlierDispatcher} · unavailable</div>}
        <ul aria-label="Agents in delegation chain">
          {tree.rows.map(({ session, depth }) => (
            <li key={session.id} aria-level={depth + 1} style={{ '--chain-depth': depth } as CSSProperties}>
              <button
                type="button"
                data-chain-session={session.id}
                aria-current={session.id === open.sessionId ? 'true' : undefined}
                onClick={() => controller.select(session.id, onSelectSession)}
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
