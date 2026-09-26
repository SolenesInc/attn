import { act, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderApp } from './test/renderApp';

function evicted(reason: string, evictedAt = '2026-08-09T12:00:00Z') {
  return { event: 'client_eviction_notice', evicted_at: evictedAt, reason, undelivered_messages: 3 } as const;
}

describe('App eviction notice', () => {
  it('identifies itself with a client id that survives reconnects', async () => {
    const { daemon } = await renderApp();
    const hello = await daemon.received('client_hello');
    expect(hello.client_id).toBeTruthy();

    const reconnected = await daemon.reconnect();

    expect(reconnected).not.toBe(daemon.connections[0]);
    expect(reconnected.sent.find((command) => command.cmd === 'client_hello')).toMatchObject({
      client_id: hello.client_id,
    });
  });

  it('tells the user, in their terms, that the app fell behind and reconnected, once, for eight seconds', async () => {
    const { daemon } = await renderApp();
    expect(screen.queryByRole('alert')).toBeNull();

    daemon.emit(evicted('client too slow'));

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('fell behind on updates');
    expect(alert).toHaveTextContent('Reconnected');
    expect(alert).not.toHaveTextContent('client too slow');

    await act(() => vi.advanceTimersByTimeAsync(7_900));
    expect(screen.getByRole('alert')).toHaveTextContent('fell behind on updates');

    await act(() => vi.advanceTimersByTimeAsync(300));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('passes an unfamiliar reason through rather than inventing one', async () => {
    const { daemon } = await renderApp();

    daemon.emit(evicted('command buffer overflow'));

    expect(screen.getByRole('alert')).toHaveTextContent('command buffer overflow');
  });

  it('drops the timestamp rather than printing a broken one', async () => {
    const { daemon } = await renderApp();

    daemon.emit(evicted('client too slow', 'not-a-date'));

    expect(screen.getByRole('alert')).toHaveTextContent('fell behind on updates');
    expect(screen.getByRole('alert')).not.toHaveTextContent('Invalid');
  });
});
