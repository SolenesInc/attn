import { act, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { renderApp } from './test/renderApp';

describe('App client token', () => {
  beforeEach(() => {
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockImplementation(async (cmd: string) =>
      cmd === 'get_client_token' ? 'instance-token' : undefined,
    );
  });

  it('presents the instance token in client_hello', async () => {
    const { daemon } = await renderApp();

    expect(await daemon.received('client_hello')).toMatchObject({ client_token: 'instance-token' });
  });

  it('stops reconnecting and shows the daemon message when the token is refused', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { daemon } = await renderApp({ initialState: false });

    daemon.emit({
      event: 'command_error',
      cmd: 'client_hello',
      success: false,
      error_code: 'unauthorized_client',
      error: 'client_hello refused: read /tmp/.attn-dev/client-token',
    });
    daemon.disconnect(1008);
    await act(() => vi.advanceTimersByTimeAsync(60_000));

    expect(screen.getByText(/client_hello refused: read \/tmp\/\.attn-dev\/client-token/)).toBeInTheDocument();
    expect(daemon.connections).toHaveLength(1);
  });
});
