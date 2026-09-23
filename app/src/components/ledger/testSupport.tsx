import { useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { render, screen, within } from '@testing-library/react';
import { vi } from 'vitest';
import type { Mock } from 'vitest';
import { SettingsProvider } from '../../contexts/SettingsContext';
import type { SessionLedgerPage, SessionLedgerQuery } from '../../hooks/daemonSessionLedgerEvents';
import { createSessionLedgerTestConnection } from '../../test/sessionLedgerTestConnection';
import { now } from '../../test/sessionLedgerFixtures';
import { SessionsTab } from './SessionsTab';
import type { SessionsTabProps } from './SessionsTab';
import { WorktreesTab } from './WorktreesTab';
import type { WorktreesTabProps } from './WorktreesTab';

type TabOnly<T> = Omit<T, 'queryRef' | 'now' | 'onStatus'>;

type SessionsTabTestProps = Partial<Omit<TabOnly<SessionsTabProps>, 'connection'>> & {
  listSessions: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
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
  { listSessions, ...props }: SessionsTabTestProps,
  settings: { values?: Record<string, string>; setSetting?: Mock<(key: string, value: string) => void> } = {},
) {
  const setSetting = settings.setSetting ?? vi.fn<(key: string, value: string) => void>();
  const { connection, emit, setConnected } = createSessionLedgerTestConnection(listSessions);
  const view = render(
    <SettingsProvider settings={settings.values ?? {}} setSetting={setSetting}>
      <Host>
        {(host) => (
          <SessionsTab workspaceNames={{}} {...props} connection={connection} queryRef={host.queryRef} now={now} onStatus={host.onStatus} />
        )}
      </Host>
    </SettingsProvider>,
  );
  return { ...view, setSetting, emit, setConnected };
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
