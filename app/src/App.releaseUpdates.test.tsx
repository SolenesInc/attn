import { act, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import App from './App';
import { installScriptedDaemon } from './test/scriptedDaemon';
import { answerQuickCaptureStatus } from './test/renderApp';

describe('App release updates', () => {
  beforeEach(() => {
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockResolvedValue(undefined);
  });

  it('shows the release the daemon found and never asks GitHub itself', async () => {
    const fetchCalls = vi.fn(() => Promise.reject(new TypeError('unexpected fetch')));
    vi.stubGlobal('fetch', fetchCalls);
    const daemon = installScriptedDaemon({ initialState: { settings: { update_available_version: '9.9.9', update_available_url: 'https://github.com/SolenesInc/attn/releases/tag/v9.9.9' } } });
    answerQuickCaptureStatus();
    render(<App />);
    await act(() => daemon.connected());
    await daemon.idle();

    expect(screen.getByText('Version 9.9.9 is available on GitHub.')).toBeInTheDocument();
    expect(fetchCalls).not.toHaveBeenCalled();
  });
});
