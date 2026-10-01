import { useCallback, useEffect, useRef, useState } from 'react';
import type { KeyboardEvent, ReactNode } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { SessionsTab } from './SessionsTab';
import type { SessionsTabProps } from './SessionsTab';
import { WorktreesTab } from './WorktreesTab';
import type { WorktreesTabProps } from './WorktreesTab';
import { ConversationsTab, type ConversationsTabProps } from './ConversationsTab';
import './LedgerSurface.css';

export type LedgerTab = 'sessions' | 'worktrees' | 'conversations';

type TabProps<T> = Omit<T, 'queryRef' | 'now' | 'onStatus'>;

export interface LedgerSurfaceProps {
  isOpen: boolean;
  tab: LedgerTab;
  onTabChange: (tab: LedgerTab) => void;
  onClose: () => void;
  yieldsFocus?: boolean;
  now?: () => Date;
  sessions: TabProps<Omit<SessionsTabProps, 'onShowWorktree' | 'requestedDir'>>;
  conversations: Omit<ConversationsTabProps, 'queryRef' | 'statusTarget'>;
  worktrees: TabProps<Omit<WorktreesTabProps, 'onShowSessions' | 'requestedPath'>>;
}

const systemNow = () => new Date();

const TABS: { id: LedgerTab; label: string }[] = [
  { id: 'sessions', label: 'Sessions' },
  { id: 'worktrees', label: 'Worktrees' },
  { id: 'conversations', label: 'Conversations' },
];

export function LedgerSurface(props: LedgerSurfaceProps) {
  if (!props.isOpen) return null;
  return <OpenLedgerSurface {...props} />;
}

function OpenLedgerSurface({
  tab, onTabChange, onClose, yieldsFocus = false, now = systemNow, sessions, worktrees, conversations,
}: LedgerSurfaceProps) {
  const [conversationStatusTarget, setConversationStatusTarget] = useState<HTMLDivElement | null>(null);
  const [status, setStatus] = useState<ReactNode>(null);
  const [legendOpen, setLegendOpen] = useState(false);
  const [requestedDir, setRequestedDir] = useState<{ path: string; nonce: number } | null>(null);
  const [requestedPath, setRequestedPath] = useState<{ path: string; nonce: number } | null>(null);
  const queryRef = useRef<HTMLInputElement | null>(null);
  const shellRef = useRef<HTMLDivElement | null>(null);

  useEscapeStack(onClose, true);
  useEscapeStack(() => setLegendOpen(false), legendOpen);

  const onStatus = useCallback((next: ReactNode) => setStatus(next), []);

  const showSessionsFor = useCallback((path: string) => {
    setRequestedDir({ path, nonce: Date.now() });
    onTabChange('sessions');
  }, [onTabChange]);

  const showWorktreeFor = useCallback((path: string) => {
    setRequestedPath({ path, nonce: Date.now() });
    onTabChange('worktrees');
  }, [onTabChange]);

  const focusSession = useCallback((id: string) => { sessions.onFocusSession?.(id); onClose(); }, [sessions.onFocusSession, onClose]);
  const selectSession = useCallback((id: string) => { worktrees.onSelectSession?.(id); onClose(); }, [worktrees.onSelectSession, onClose]);

  useEffect(() => {
    // Land on the list, not the query: arrows should work the moment the surface opens.
    const first = shellRef.current?.querySelector<HTMLElement>('.ledger-row');
    first?.focus({ preventScroll: true });
  }, [tab]);

  const onKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement;
    const typing = target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT';
    if (event.key === '/' && !typing) {
      event.preventDefault();
      queryRef.current?.focus();
      queryRef.current?.select();
      return;
    }
    if (event.key === '?' && !typing) {
      event.preventDefault();
      setLegendOpen((open) => !open);
      return;
    }
    if ((event.key === '[' || event.key === ']') && !typing) {
      event.preventDefault();
      const direction = event.key === ']' ? 1 : -1;
      onTabChange(TABS[(TABS.findIndex((option) => option.id === tab) + direction + TABS.length) % TABS.length].id);
    }
  }, [tab, onTabChange]);

  return (
    <div className="ledger-shell" ref={shellRef} onKeyDown={onKeyDown}>
      <FocusTrap paused={yieldsFocus} focusTrapOptions={{
        escapeDeactivates: false,
        initialFocus: false,
        onPostUnpause: () => {
          const target = shellRef.current?.querySelector<HTMLElement>('.ledger-row.is-selected')
            ?? shellRef.current?.querySelector<HTMLElement>('.ledger-row')
            ?? queryRef.current;
          target?.focus({ preventScroll: true });
        },
      }}>
        <div className="ledger-panel" role="dialog" aria-modal="true" aria-label="Sessions, worktrees and conversations">
          <header className="ledger-header">
            <nav className="ledger-tabs" aria-label="Which list">
              {TABS.map((option) => (
                <button
                  key={option.id}
                  type="button"
                  className={option.id === tab ? 'is-selected' : undefined}
                  aria-current={option.id === tab ? 'page' : undefined}
                  onClick={() => onTabChange(option.id)}
                >
                  {option.label}
                </button>
              ))}
            </nav>
            <button type="button" className="ledger-close" onClick={onClose}>
              <span>Close</span><kbd>esc</kbd>
            </button>
          </header>

          <div className="ledger-body">
            {tab === 'sessions'
              ? (
                <SessionsTab
                  {...sessions}
                  onFocusSession={focusSession}
                  onShowWorktree={showWorktreeFor}
                  requestedDir={requestedDir}
                  queryRef={queryRef}
                  now={now}
                  onStatus={onStatus}
                />
              )
              : tab === 'conversations' ? (
                <ConversationsTab {...conversations} onOpenSeed={(id) => { conversations.onOpenSeed(id); onClose(); }} queryRef={queryRef} statusTarget={conversationStatusTarget} />
              ) : (
                <WorktreesTab
                  {...worktrees}
                  onSelectSession={selectSession}
                  onShowSessions={showSessionsFor}
                  requestedPath={requestedPath}
                  queryRef={queryRef}
                  now={now}
                  onStatus={onStatus}
                />
              )}
          </div>

          <footer className="ledger-status" aria-live="polite">
            <div className="ledger-status-left" ref={setConversationStatusTarget}>{tab === 'conversations' ? null : status}</div>
            <button
              type="button"
              className="ledger-status-link"
              aria-expanded={legendOpen}
              onClick={() => setLegendOpen((open) => !open)}
            >
              <kbd>?</kbd> keys
            </button>
            {legendOpen && (
              <div className="ledger-legend" role="dialog" aria-label="Keys">
                <div><kbd>↑</kbd><kbd>↓</kbd><span>move</span></div>
                <div><kbd>⏎</kbd><span>primary verb</span></div>
                <div><kbd>.</kbd><span>more verbs</span></div>
                <div><kbd>1</kbd>–<kbd>9</kbd><span>nth verb</span></div>
                <div><kbd>/</kbd><span>filter</span></div>
                <div><kbd>[</kbd><kbd>]</kbd><span>switch list</span></div>
                <div><kbd>y</kbd><span>copy path</span></div>
                <div><kbd>esc</kbd><span>back out</span></div>
              </div>
            )}
          </footer>
        </div>
      </FocusTrap>
    </div>
  );
}
