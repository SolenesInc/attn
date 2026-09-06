import type { MouseEvent, PointerEvent } from 'react';
import type { DelegationSession, DispatcherLink } from '../utils/delegationLinks';

export function SidebarDispatcherLine<TSession extends DelegationSession>({
  dispatcher,
  onSelectSession,
}: {
  dispatcher: DispatcherLink<TSession> | null;
  onSelectSession: (id: string) => void;
}) {
  if (!dispatcher) return null;

  const stopPointer = (event: PointerEvent) => event.stopPropagation();
  const openDispatcher = (event: MouseEvent) => {
    event.stopPropagation();
    if (dispatcher.session) onSelectSession(dispatcher.session.id);
  };

  return (
    <span className="sidebar-dispatcher" data-testid="sidebar-dispatcher">
      <span aria-hidden="true">↳</span>
      {dispatcher.session ? (
        <button
          type="button"
          className="sidebar-dispatcher__link"
          title={`Open ${dispatcher.name}`}
          aria-label={`Open dispatcher ${dispatcher.name}`}
          onPointerDown={stopPointer}
          onClick={openDispatcher}
        >
          {dispatcher.name}
        </button>
      ) : (
        <span className="sidebar-dispatcher__name">{dispatcher.name}</span>
      )}
    </span>
  );
}

export function SidebarDelegateCount<TSession extends DelegationSession>({
  delegates,
}: {
  delegates: readonly TSession[];
}) {
  if (delegates.length === 0) return null;
  const names = delegates.map((delegate) => delegate.label).join(', ');
  const label = `${delegates.length} live ${delegates.length === 1 ? 'delegate' : 'delegates'}: ${names}`;
  return (
    <span className="sidebar-delegate-count" title={label} aria-label={label}>
      {delegates.length}
    </span>
  );
}
