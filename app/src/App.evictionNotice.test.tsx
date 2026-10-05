import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
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

  it.each(['client too slow', 'command buffer overflow'])('keeps a reconnect notice quiet for %s', async (reason) => {
    const { daemon } = await renderApp();
    await daemon.emit(evicted(reason));
    await daemon.idle();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByText(/Reconnected/)).not.toBeInTheDocument();
  });
});
