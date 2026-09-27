import { act, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import App from './App';
import { installScriptedDaemon } from './test/scriptedDaemon';

const LATEST_RELEASE = 'https://api.github.com/repos/victorarias/attn/releases/latest';

async function launch(installChannel: string | undefined) {
  if (installChannel !== undefined) vi.stubEnv('VITE_INSTALL_CHANNEL', installChannel);
  const daemon = installScriptedDaemon();
  const github = vi.fn(async () => Response.json({ tag_name: 'v9.9.9', html_url: 'https://github.com/victorarias/attn/releases/tag/v9.9.9', draft: false, prerelease: false }));
  vi.stubGlobal('fetch', (url: string) => (url === LATEST_RELEASE ? github() : Promise.reject(new TypeError('unexpected fetch'))));
  render(<App />);
  await act(() => daemon.connected());
  await daemon.idle();
  return github;
}

describe('App release updates', () => {
  beforeEach(() => {
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockResolvedValue(undefined);
  });

  it.each(['release', 'homebrew', 'cask', 'dmg', 'nightly', undefined])('offers a newer GitHub release to a %s install', async (installChannel) => {
    const github = await launch(installChannel);

    expect(github).toHaveBeenCalledTimes(1);
    expect(screen.getByText('Version 9.9.9 is available on GitHub.')).toBeInTheDocument();
  });

  it('never asks GitHub from a source install', async () => {
    const github = await launch(' Source ');

    expect(github).not.toHaveBeenCalled();
    expect(screen.queryByText(/is available on GitHub/)).toBeNull();
  });
});
