import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, screen } from '@testing-library/react';
import { useQuery } from '@victorarias/attn-app';
import { openDockedApprovals, reviewerApp } from './testSupport';
import type { ScriptedDaemon } from '../../test/scriptedDaemon';

const loadAppView = vi.hoisted(() => vi.fn());
vi.mock('./loadAppView', async () => {
  const actual = await vi.importActual<typeof import('./loadAppView')>('./loadAppView');
  return { ...actual, loadAppView };
});

function PendingRequests() {
  const { docs, live, error } = useQuery<{ title: string }>('requests', {
    filters: [{ field: 'status', op: 'eq', value: 'pending' }],
    sort: { field: 'updated_at', desc: true },
    limit: 20,
  });
  return (
    <>
      <ul>{docs.map((doc) => <li key={doc.id}>{doc.body.title}</li>)}</ul>
      <div data-testid="query-live">{live ? 'served' : 'not served'}</div>
      {error && <div data-testid="query-error">{error.code}: {error.message}</div>}
    </>
  );
}

beforeEach(() => {
  loadAppView.mockReset();
  loadAppView.mockResolvedValue(PendingRequests);
});

const stored = (id: string, title: string, rev: number) => ({
  id, body: JSON.stringify({ title }), rev, created_at: '2026-08-13T10:00:00Z', updated_at: '2026-08-13T10:00:00Z',
});

async function deliver(daemon: ScriptedDaemon, subscriptionId: string, delivery: number, upsert: ReturnType<typeof stored>[], order = upsert.map((doc) => doc.id)) {
  daemon.emit({ event: 'doc_subscription_delivery', subscription_id: subscriptionId, delivery, as_of_seq: 40 + delivery, order, upsert });
  await daemon.idle();
}

async function dropAndReturn(daemon: ScriptedDaemon) {
  const count = daemon.connections.length;
  daemon.disconnect();
  await act(async () => {});
  const whileAway = screen.getByTestId('query-live').textContent;
  while (daemon.connections.length === count) {
    await act(() => vi.advanceTimersToNextTimerAsync());
  }
  await act(() => daemon.connected());
  await daemon.idle();
  return whileAway;
}

const subscribesOn = (daemon: ScriptedDaemon, connection: number) =>
  daemon.connections[connection].sent.filter((command) => command.cmd === 'doc_subscribe');

describe('a view reading a live query', () => {
  it('asks for its window with each filter bound as JSON text, and claims to hold nothing yet', async () => {
    const daemon = await openDockedApprovals([reviewerApp()]);

    const [subscribe] = daemon.sentOf('doc_subscribe');
    expect(subscribe.query).toEqual({
      namespace: 'app/reviewer',
      collection: 'requests',
      filters: [{ field: 'status', op: 'eq', value_json: '"pending"' }],
      sort: { field: 'updated_at', desc: true },
      limit: 20,
    });
    expect(subscribe).not.toHaveProperty('have');
  });

  it('renders what the daemon delivers to its own subscription, and nothing sent for another', async () => {
    const daemon = await openDockedApprovals([reviewerApp()]);
    const [{ subscription_id: id }] = daemon.sentOf('doc_subscribe');

    await deliver(daemon, id!, 1, [stored('a', 'approve the deploy', 1)]);
    await deliver(daemon, `${id}-other`, 1, [stored('b', 'not mine', 1)]);

    expect(screen.getByText('approve the deploy')).toBeInTheDocument();
    expect(screen.queryByText('not mine')).toBeNull();
    expect(screen.getByTestId('query-live')).toHaveTextContent('served');
  });

  it('says it is not served while the daemon is away, and asks again on return with the revisions it holds', async () => {
    const daemon = await openDockedApprovals([reviewerApp()]);
    const [{ subscription_id: id }] = daemon.sentOf('doc_subscribe');
    await deliver(daemon, id!, 1, [stored('a', 'approve the deploy', 1)]);
    await deliver(daemon, id!, 2, [stored('a', 'approve the deploy today', 7)]);

    expect(await dropAndReturn(daemon)).toBe('not served');

    const [again] = subscribesOn(daemon, daemon.connections.length - 1);
    expect(again.subscription_id).toBe(id);
    expect(again.have).toEqual([{ id: 'a', rev: 7 }]);
    expect(screen.getByTestId('query-live')).toHaveTextContent('served');
    expect(screen.getByText('approve the deploy today')).toBeInTheDocument();
  });

  it('shows the ending the daemon names, and never asks for that window again', async () => {
    const daemon = await openDockedApprovals([reviewerApp()]);
    const [{ subscription_id: id }] = daemon.sentOf('doc_subscribe');

    daemon.emit({ event: 'doc_subscription_ended', subscription_id: id!, code: 'collection_undefined', error: 'reviewer no longer declares requests' });
    await daemon.idle();
    expect(screen.getByTestId('query-error')).toHaveTextContent('collection_undefined: reviewer no longer declares requests');

    await dropAndReturn(daemon);

    expect(subscribesOn(daemon, daemon.connections.length - 1)).toEqual([]);
  });
});
