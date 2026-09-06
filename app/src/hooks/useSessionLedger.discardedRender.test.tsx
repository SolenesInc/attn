import { StrictMode, Suspense, startTransition, useEffect, useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { act, render } from '@testing-library/react';
import { useSessionLedger } from './useSessionLedger';
import type { SessionLedgerView } from './useSessionLedger';
import type { SessionLedgerPage, SessionLedgerQuery } from './daemonSessionLedgerEvents';
import type { SessionLedgerEntry } from '../types/generated';
import { SessionState } from '../types/generated';

const NOW = new Date('2026-09-05T14:30:00Z');
const now = () => NOW;

function closedEntry(id: string): SessionLedgerEntry {
  return {
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${id}`,
    last_seen: '2026-09-05T10:00:00Z',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    id,
    closed_at: '2026-09-05T13:00:00Z',
    closed_by: 'user',
  };
}

const NEVER = new Promise<never>(() => {});

/** Throws a promise that never settles, so React starts this render and throws it away. */
function SuspendWhen({ when }: { when: boolean }) {
  if (when) throw NEVER;
  return null;
}

describe('useSessionLedger under a render React discards', () => {
  it('places a later close by the committed filters, not the abandoned ones', async () => {
    const list = vi.fn(async (_query: SessionLedgerQuery): Promise<SessionLedgerPage> => ({
      entries: [],
      omitted: 0,
    }));

    // Only a committed render publishes its view, so the assertions below can
    // only ever reach the surface the user is actually looking at.
    const seen: { view: SessionLedgerView | null } = { view: null };
    let scopeLive: (() => void) | null = null;

    function Harness() {
      const view = useSessionLedger({ enabled: true, list, now });
      const [suspend, setSuspend] = useState(false);
      const { setFilters } = view;

      useEffect(() => {
        seen.view = view;
        scopeLive = () => {
          startTransition(() => {
            setFilters((current) => ({ ...current, scope: 'live' }));
            setSuspend(true);
          });
        };
      });

      return <SuspendWhen when={suspend} />;
    }

    render(
      <Suspense fallback={null}>
        <Harness />
      </Suspense>,
    );
    await act(async () => {});

    // A transition that renders with scope 'live' and never commits.
    await act(async () => {
      scopeLive?.();
    });

    // The committed surface is still 'all', so a closed row belongs in it.
    expect(seen.view?.filters.scope).toBe('all');
    await act(async () => {
      seen.view?.recordClose(closedEntry('s1'));
    });
    expect(seen.view?.entries.map((row) => row.id)).toEqual(['s1']);
  });

  it('survives the double render StrictMode does', async () => {
    const list = vi.fn(async (_query: SessionLedgerQuery): Promise<SessionLedgerPage> => ({
      entries: [],
      omitted: 0,
    }));
    const seen: { view: SessionLedgerView | null } = { view: null };

    function Harness() {
      const view = useSessionLedger({ enabled: true, list, now });
      useEffect(() => {
        seen.view = view;
      });
      return null;
    }

    render(
      <StrictMode>
        <Harness />
      </StrictMode>,
    );
    await act(async () => {});

    await act(async () => {
      seen.view?.setFilters((current) => ({ ...current, scope: 'closed' }));
    });
    await act(async () => {
      seen.view?.recordClose(closedEntry('s2'));
    });
    expect(seen.view?.filters.scope).toBe('closed');
    expect(seen.view?.entries.map((row) => row.id)).toEqual(['s2']);
  });
});
