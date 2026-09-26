import { act, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import App from './App';
import { initialState, installScriptedDaemon } from './test/scriptedDaemon';

const ensureCalls = () => vi.mocked(invoke).mock.calls.filter(([command]) => command === 'ensure_daemon').length;

describe('App daemon ensure', () => {
  beforeEach(() => {
    vi.mocked(isTauri).mockReturnValue(true);
  });

  it('shows why the daemon would not start, skips the socket, and retries until it does', async () => {
    const startupError = 'start background jobs: acquire runner lock /tmp/attn/.runner.lock: already held';
    let daemonStarts = false;
    vi.mocked(invoke).mockImplementation(async (command: string) => {
      if (command === 'ensure_daemon' && !daemonStarts) throw new Error(startupError);
      return undefined;
    });
    vi.spyOn(console, 'error').mockImplementation(() => {});

    const daemon = installScriptedDaemon();
    render(<App />);
    await act(() => vi.advanceTimersByTimeAsync(0));

    expect(screen.getByText(startupError)).toBeInTheDocument();
    expect(daemon.connections).toHaveLength(0);

    daemonStarts = true;
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    await act(() => daemon.connected());

    expect(screen.queryByText(startupError)).toBeNull();
  });

  it('runs daemon ensure again when the daemon speaks another protocol version', async () => {
    vi.mocked(invoke).mockResolvedValue(undefined);
    let ensuresBeforeHandshake = 0;
    const daemon = installScriptedDaemon({ initialState: false });
    daemon.on('client_hello', () => {
      ensuresBeforeHandshake = ensureCalls();
      return initialState({ protocol_version: '41' });
    });
    render(<App />);
    await act(() => daemon.connected());
    await daemon.idle();

    expect(ensureCalls()).toBeGreaterThan(ensuresBeforeHandshake);
    expect(daemon.connections[0].readyState).toBe(WebSocket.CLOSED);
  });
});
