import { useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { render, screen, within } from '@testing-library/react';
import { vi } from 'vitest';
import type { Mock } from 'vitest';
import { SettingsProvider } from '../../contexts/SettingsContext';
import type { SessionLedgerPage, SessionLedgerQuery } from '../../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerEntry, SessionReopen, SessionReopenEntry } from '../../types/generated';
import { SessionReopenAction, SessionState } from '../../types/generated';
import { SessionsTab } from './SessionsTab';
import type { SessionsTabProps } from './SessionsTab';
import { WorktreesTab } from './WorktreesTab';
import type { WorktreesTabProps } from './WorktreesTab';

export const NOW = new Date('2026-09-05T14:30:00Z');
export const now = () => NOW;

type TabOnly<T> = Omit<T, 'queryRef' | 'now' | 'onStatus'>;

/** Queries scoped to the rows: a title also heads the inspector, so the page as a whole has it twice. */
export const rows = () => within(screen.getByRole('listbox', { name: 'Rows' }));

/** The shell's contribution to a tab: a query input ref and the status line it paints. */
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
  props: Partial<TabOnly<SessionsTabProps>> & Pick<SessionsTabProps, 'listSessions'>,
  settings: { values?: Record<string, string>; setSetting?: Mock<(key: string, value: string) => void> } = {},
) {
  const setSetting = settings.setSetting ?? vi.fn<(key: string, value: string) => void>();
  const tree = (next: Partial<TabOnly<SessionsTabProps>>) => (
    <SettingsProvider settings={settings.values ?? {}} setSetting={setSetting}>
      <Host>
        {(host) => <SessionsTab workspaceNames={{}} {...props} {...next} queryRef={host.queryRef} now={now} onStatus={host.onStatus} />}
      </Host>
    </SettingsProvider>
  );
  const view = render(tree({}));
  return { ...view, setSetting, rerender: (next: Partial<TabOnly<SessionsTabProps>>) => view.rerender(tree(next)) };
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

/** The tab's only door to the daemon: every assertion is about the query it puts through here. */
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
