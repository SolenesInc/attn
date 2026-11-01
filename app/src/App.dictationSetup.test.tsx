import { act, fireEvent, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { gesture } from './test/renderApp';
import { openSection, savedSettings } from './test/settings';
import type { SpeechStatus } from './dictation/speechHelper';

const native = vi.hoisted(() => ({
  listeners: new Map<string, Set<(event: { payload: unknown }) => void>>(),
  commands: [] as string[],
  status: { phase: 'off', cacheBytes: 0 } as SpeechStatus,
}));
vi.mock('@tauri-apps/api/event', () => ({
  emit: async () => {}, emitTo: async () => {},
  listen: async (event: string, listener: (event: { payload: unknown }) => void) => {
    const listeners = native.listeners.get(event) ?? new Set();
    native.listeners.set(event, listeners); listeners.add(listener);
    return () => listeners.delete(listener);
  },
}));

const helperReports = (status: SpeechStatus) => act(() => {
  native.status = status;
  for (const listener of native.listeners.get('speech-status') ?? []) listener({ payload: status });
});
const dictation = () => screen.getByRole('switch', { name: 'Dictation' });
const progress = () => screen.getByTestId('settings-dictation-status');
const speechCommands = () => native.commands.filter(command => command.startsWith('speech_'));

beforeEach(() => {
  native.listeners.clear(); native.commands = []; native.status = { phase: 'off', cacheBytes: 0 };
  vi.mocked(isTauri).mockReturnValue(true);
  vi.mocked(invoke).mockImplementation(async (command: string) => {
    native.commands.push(command);
    if (command === 'get_client_token') return 'test-token';
    if (command === 'speech_enable') native.status = { phase: 'starting', cacheBytes: native.status.cacheBytes };
    if (command === 'speech_disable') native.status = { phase: 'off', cacheBytes: native.status.cacheBytes };
    if (command === 'speech_remove') native.status = { phase: 'off', cacheBytes: 0 };
    if (command.startsWith('speech_')) return native.status;
    if (command.startsWith('capture_')) return { binding: null, active: null };
    return undefined;
  });
});

describe('Dictation setup', () => {
  it('prepares the speech model when an enabled app starts and shows its progress', async () => {
    await openSection('experimental', { settings: { dictation_enabled: 'true' } });
    expect(speechCommands()).toContain('speech_enable');
    expect(dictation()).toHaveAttribute('aria-checked', 'true');
    helperReports({ phase: 'downloading', artifact: 'Parakeet Unified English', bytes: 312_000_000, total: 731_357_568, cacheBytes: 312_000_000 });
    expect(progress()).toHaveTextContent('Downloading Parakeet Unified English: 312 MB of 731 MB');
    helperReports({ phase: 'ready', cacheBytes: 732_598_624 });
    expect(progress()).toHaveTextContent('Ready.');
  });

  it('starts preparation when turned on and releases the helper when turned off', async () => {
    const daemon = await openSection('experimental');
    expect(speechCommands()).not.toContain('speech_enable');
    await gesture(daemon, () => fireEvent.click(dictation()));
    expect(savedSettings(daemon)).toEqual([['dictation_enabled', 'true']]);
    expect(speechCommands().slice(-1)).toEqual(['speech_enable']);
    await gesture(daemon, () => fireEvent.click(dictation()));
    expect(speechCommands().slice(-1)).toEqual(['speech_disable']);
  });

  it('names a failed preparation and retries it', async () => {
    await openSection('experimental', { settings: { dictation_enabled: 'true' } });
    helperReports({ phase: 'failed', message: 'could not download Parakeet Unified English: connection reset. Downloaded bytes are kept; retry to resume.', cacheBytes: 90_000_000 });
    expect(progress()).toHaveTextContent('connection reset. Downloaded bytes are kept; retry to resume.');
    native.commands = [];
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })));
    expect(speechCommands()).toEqual(['speech_enable']);
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
  });

  it('offers to remove a downloaded model only while dictation is off', async () => {
    native.status = { phase: 'off', cacheBytes: 732_598_624 };
    await openSection('experimental');
    expect(progress()).toHaveTextContent('Speech model downloaded (733 MB).');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Remove model' })));
    expect(speechCommands()).toContain('speech_remove');
    expect(screen.queryByRole('button', { name: 'Remove model' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('settings-dictation-status')).not.toBeInTheDocument();
  });
});
