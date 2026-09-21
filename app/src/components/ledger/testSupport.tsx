import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { render, screen, within } from '@testing-library/react';
import { vi } from 'vitest';
import type { Mock } from 'vitest';
import { SettingsProvider } from '../../contexts/SettingsContext';
import type {
  SessionLedgerConnectionEvent,
  SessionLedgerPage,
  SessionLedgerQuery,
  SessionLedgerUpdate,
  SessionReopenResolutionEvent,
} from '../../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerConnection } from '../../hooks/useSessionLedger';
import type { SessionLedgerEntry, SessionReopen, SessionReopenEntry } from '../../types/generated';
import { SessionReopenAction, SessionState } from '../../types/generated';
import { SessionsTab } from './SessionsTab';
import type { SessionsTabProps } from './SessionsTab';
import { WorktreesTab } from './WorktreesTab';
import type { WorktreesTabProps } from './WorktreesTab';

export const NOW = new Date('2026-09-05T14:30:00Z');
export const now = () => NOW;

type TabOnly<T> = Omit<T, 'queryRef' | 'now' | 'onStatus'>;

interface ResolutionNotice {
  resolutions: Record<string, SessionReopenResolutionEvent>;
  arrivalNonceBySession: Record<string, number>;
  nonce: number;
}

type SessionsTabTestProps = Partial<Omit<TabOnly<SessionsTabProps>, 'connection'>> & {
  listSessions: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  connectionGeneration?: number;
  closeNotice?: { entry: SessionLedgerEntry; nonce: number };
  resolutionNotice?: ResolutionNotice;
};

export const rows = () => within(screen.getByRole('listbox', { name: 'Rows' }));

function Host({ children }: { children: (host: { queryRef: React.RefObject<HTMLInputElement | null>; onStatus: (status: ReactNode) => void }) => ReactNode }) {
  const queryRef = useRef<HTMLInputElement | null>(null);
  const [status, setStatus] = useState<ReactNode>(null);
  return (
    <>
      {children({ queryRef, onStatus: setStatus })}
      <div data-testid="status">{status}</div>
    </>
  );
}

export function renderSessionsTab(
  props: SessionsTabTestProps,
  settings: { values?: Record<string, string>; setSetting?: Mock<(key: string, value: string) => void> } = {},
) {
  const setSetting = settings.setSetting ?? vi.fn<(key: string, value: string) => void>();
  const listeners = new Set<(event: SessionLedgerConnectionEvent) => void>();
  let listSessions = props.listSessions;
  const connection: SessionLedgerConnection = {
    list: (query) => listSessions(query),
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    connected: true,
    generation: props.connectionGeneration ?? 1,
  };
  const emit = (event: SessionLedgerUpdate) => {
    for (const listener of listeners) listener({ ...event, connectionGeneration: connection.generation } as SessionLedgerConnectionEvent);
  };
  function Notices({ closeNotice, resolutionNotice }: Pick<SessionsTabTestProps, 'closeNotice' | 'resolutionNotice'>) {
    const closeNonce = useRef<number | undefined>(undefined);
    const resolutionNonce = useRef<number | undefined>(undefined);
    useEffect(() => {
      if (!closeNotice || closeNonce.current === closeNotice.nonce) return;
      closeNonce.current = closeNotice.nonce;
      emit({ type: 'closed', entry: closeNotice.entry });
    }, [closeNotice]);
    useEffect(() => {
      if (!resolutionNotice || resolutionNonce.current === resolutionNotice.nonce) return;
      const previous = resolutionNonce.current;
      resolutionNonce.current = resolutionNotice.nonce;
      for (const [sessionId, resolution] of Object.entries(resolutionNotice.resolutions)) {
        if (previous !== undefined && resolutionNotice.arrivalNonceBySession[sessionId] <= previous) continue;
        emit({ type: 'reopen-resolved', resolution });
      }
    }, [resolutionNotice]);
    return null;
  }
  const tree = (next: Partial<SessionsTabTestProps>) => {
    const merged = { ...props, ...next };
    listSessions = merged.listSessions;
    connection.generation = merged.connectionGeneration ?? connection.generation;
    const { listSessions: _list, connectionGeneration: _generation, closeNotice, resolutionNotice, ...tab } = merged;
    return (
    <SettingsProvider settings={settings.values ?? {}} setSetting={setSetting}>
      <Host>
        {(host) => (
          <>
            <SessionsTab workspaceNames={{}} {...tab} connection={connection} queryRef={host.queryRef} now={now} onStatus={host.onStatus} />
            <Notices closeNotice={closeNotice} resolutionNotice={resolutionNotice} />
          </>
        )}
      </Host>
    </SettingsProvider>
    );
  };
  const view = render(tree({}));
  return {
    ...view,
    setSetting,
    connection,
    emit,
    rerender: (next: Partial<SessionsTabTestProps>) => view.rerender(tree(next)),
  };
}

export function renderWorktreesTab(props: Partial<TabOnly<WorktreesTabProps>> = {}) {
  const full: TabOnly<WorktreesTabProps> = {
    listWorktrees: vi.fn().mockResolvedValue({ worktrees: [], repositories: [], omitted: 0 }),
    getSweepLog: vi.fn().mockResolvedValue({ entries: [], omitted: 0 }),
    setKeep: vi.fn(),
    refreshWorktrees: vi.fn().mockResolvedValue(true),
    deleteWorktree: vi.fn().mockResolvedValue(undefined),
    sessions: [],
    gitOperations: {},
    onSelectSession: vi.fn(),
    onShowSessions: vi.fn(),
    ...props,
  };
  return render(
    <Host>
      {(host) => <WorktreesTab {...full} queryRef={host.queryRef} now={now} onStatus={host.onStatus} />}
    </Host>,
  );
}

export function entry(overrides: Partial<SessionLedgerEntry> & { id: string }): SessionLedgerEntry {
  return {
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${overrides.id}`,
    last_seen: '2026-09-05T10:00:00Z',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    ...overrides,
  };
}

export function closedEntry(id: string, overrides: Partial<SessionLedgerEntry> = {}): SessionLedgerEntry {
  return entry({
    id,
    last_seen: '2026-09-05T09:00:00Z',
    closed_at: '2026-09-05T10:00:00Z',
    closed_by: 'user',
    close_reason: 'work finished',
    ...overrides,
  });
}

export function liveEntry(id: string): SessionLedgerEntry {
  return entry({ id, last_seen: '2026-09-05T13:00:00Z' });
}

export function verdict(overrides: Partial<SessionReopen> = {}): SessionReopen {
  return {
    reopenable: true,
    actions: [SessionReopenAction.Reopen],
    checking: false,
    directory_state: 'present',
    workspace_id: 'ws-1',
    workspace_plan: 'reuse',
    pane_plan: 'add',
    ...overrides,
  };
}

export function judged(sessionId: string, overrides: Partial<SessionReopen> = {}): SessionReopenEntry {
  return { session_id: sessionId, reopen: verdict(overrides) };
}

export function listing(pages: SessionLedgerPage[]) {
  const calls: SessionLedgerQuery[] = [];
  const list = vi.fn(async (query: SessionLedgerQuery) => {
    calls.push(query);
    return pages[Math.min(calls.length - 1, pages.length - 1)];
  });
  return { list, calls };
}

export function page(overrides: Partial<SessionLedgerPage> = {}): SessionLedgerPage {
  return { entries: [], omitted: 0, ...overrides };
}
