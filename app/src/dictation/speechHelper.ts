import { useEffect, useState } from 'react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { isMacLikePlatform } from '../shortcuts/platform';
import { DICTATION_SHORTCUT_SETTING, dictationShortcut } from './settings';

export const DICTATION_ENABLED_SETTING = 'dictation_enabled';
const STATUS_EVENT = 'speech-status';

export type SpeechStatus = { cacheBytes: number } & (
  | { phase: 'off' }
  | { phase: 'starting' }
  | { phase: 'downloading'; artifact: string; bytes: number; total: number }
  | { phase: 'verifying'; artifact: string }
  | { phase: 'loading' }
  | { phase: 'ready' }
  | { phase: 'failed'; message: string }
);

export const speechSupported = () => isMacLikePlatform() && isTauri();

let lifecycle: Promise<unknown> = Promise.resolve();

function changeHelper(command: 'speech_enable' | 'speech_disable' | 'speech_remove') {
  const change = lifecycle.then(() => invoke<SpeechStatus>(command));
  lifecycle = change.catch(() => undefined);
  return change;
}

export function useSpeechHelperLifecycle(settings: Record<string, string>) {
  const enabled = settings[DICTATION_ENABLED_SETTING] === 'true';
  useEffect(() => {
    if (!speechSupported()) return;
    void changeHelper(enabled ? 'speech_enable' : 'speech_disable');
  }, [enabled]);
}

export function useSpeechStatus() {
  const [status, setStatus] = useState<SpeechStatus | null>(null);
  useEffect(() => {
    if (!speechSupported()) return;
    let disposed = false;
    const unlisten = listen<SpeechStatus>(STATUS_EVENT, ({ payload }) => { if (!disposed) setStatus(payload); });
    void invoke<SpeechStatus>('speech_status').then(current => { if (!disposed) setStatus(previous => previous ?? current); });
    return () => { disposed = true; void unlisten.then(stop => stop()); };
  }, []);
  return {
    status,
    retry: () => changeHelper('speech_enable').then(setStatus),
    remove: () => changeHelper('speech_remove').then(setStatus),
  };
}

export function useDictationShortcut(settings: Record<string, string>, connected: boolean, save: (key: string, value: string) => Promise<void>) {
  const enabled = settings[DICTATION_ENABLED_SETTING] === 'true';
  const binding = dictationShortcut(settings);
  const [error, setError] = useState<string>();
  const [active, setActive] = useState<string | null>(null);
  useEffect(() => {
    if (!speechSupported() || !connected) return;
    let disposed = false;
    const wanted = enabled ? binding : null;
    invoke('dictation_bind', { binding: wanted }).then(
      () => { if (!disposed) { setActive(wanted); setError(undefined); } },
      failure => { if (!disposed) setError(String(failure)); },
    );
    return () => { disposed = true; };
  }, [enabled, binding, connected]);
  return {
    enabled,
    binding,
    active,
    error,
    setBinding: async (next: string | null) => {
      await invoke('dictation_bind', { binding: next });
      try {
        await save(DICTATION_SHORTCUT_SETTING, next ?? '');
      } catch (failure) {
        await invoke('dictation_bind', { binding: active });
        throw failure;
      }
      setActive(next);
      setError(undefined);
    },
  };
}
