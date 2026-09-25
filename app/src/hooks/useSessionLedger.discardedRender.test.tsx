import { StrictMode, Suspense, startTransition, useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { describe, expect, it } from 'vitest';
import { act } from '@testing-library/react';
import { useSessionLedger } from './useSessionLedger';
import type { SessionLedgerView } from './useSessionLedger';
import { page, pages, serveLedger, useLedgerConnection } from '../components/ledger/testSupport';
import { renderWithDaemon } from '../test/renderApp';
import { closedEntry, now } from '../test/sessionLedgerFixtures';

async function renderServed(ui: ReactElement) {
  const rendered = await renderWithDaemon();
  const ledger = serveLedger(rendered.daemon, pages([page()]));
  rendered.rerender(ui);
  await rendered.daemon.idle();
  return { ...ledger, settle: () => rendered.daemon.idle() };
}

const NEVER = new Promise<never>(() => {});

/** Throws a promise that never settles, so React starts this render and throws it away. */
function SuspendWhen({ when }: { when: boolean }) {
  if (when) throw NEVER;
  return null;
}

describe('useSessionLedger under a render React discards', () => {
  it('places a later close by the committed filters, not the abandoned ones', async () => {
    // Only a committed render publishes its view, so the assertions below can
    // only ever reach the surface the user is actually looking at.
    const seen: { view: SessionLedgerView | null } = { view: null };
    let scopeLive: (() => void) | null = null;

    function Harness() {
      const view = useSessionLedger({ enabled: true, connection: useLedgerConnection(), now });
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

    const ledger = await renderServed(
      <Suspense fallback={null}>
        <Harness />
      </Suspense>,
    );

    // A transition that renders with scope 'live' and never commits.
    act(() => {
      scopeLive?.();
    });
    await ledger.settle();

    // The committed surface is still 'all', so a closed row belongs in it.
    expect(seen.view?.filters.scope).toBe('all');
    await ledger.closed(closedEntry('s1'));
    expect(seen.view?.entries.map((row) => row.id)).toEqual(['s1']);
  });

  it('survives the double render StrictMode does', async () => {
    const seen: { view: SessionLedgerView | null } = { view: null };

    function Harness() {
      const view = useSessionLedger({ enabled: true, connection: useLedgerConnection(), now });
      useEffect(() => {
        seen.view = view;
      });
      return null;
    }

    const ledger = await renderServed(
      <StrictMode>
        <Harness />
      </StrictMode>,
    );

    act(() => {
      seen.view?.setFilters((current) => ({ ...current, scope: 'closed' }));
    });
    await ledger.settle();
    await ledger.closed(closedEntry('s2'));
    expect(seen.view?.filters.scope).toBe('closed');
    expect(seen.view?.entries.map((row) => row.id)).toEqual(['s2']);
  });
});
