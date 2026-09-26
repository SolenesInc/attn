import { act, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import App from './App';
import { installScriptedDaemon } from './test/scriptedDaemon';

vi.hoisted(() => {
  vi.stubEnv('VITE_ATTN_BUILD_INSTANCE', 'dev');
});

function serveHealth(answer: () => Promise<Response>) {
  const health = vi.fn(answer);
  vi.stubGlobal('fetch', (url: string) => (new URL(url).pathname === '/health' ? health() : Promise.reject(new TypeError('unexpected fetch'))));
  return health;
}

describe('App instance mismatch', () => {
  it('refuses a daemon of another instance, says which, and never opens its socket', async () => {
    const daemon = installScriptedDaemon();
    const health = serveHealth(async () => Response.json({ instance: '' }));
    vi.spyOn(console, 'warn').mockImplementation(() => {});

    render(<App />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    await act(() => vi.advanceTimersByTimeAsync(60_000));

    expect(screen.getByText(/this app was built for instance "dev" but the daemon reports instance "default"/)).toBeInTheDocument();
    expect(daemon.connections).toHaveLength(0);
    expect(health).toHaveBeenCalledTimes(1);
  });

  it('connects to a daemon of its own instance', async () => {
    const daemon = installScriptedDaemon();
    serveHealth(async () => Response.json({ instance: 'dev' }));

    render(<App />);
    await act(() => daemon.connected());

    expect(screen.queryByText(/Instance mismatch/)).toBeNull();
  });
});
