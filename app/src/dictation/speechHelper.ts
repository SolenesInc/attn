import { useEffect, useState } from 'react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { isMacLikePlatform } from '../shortcuts/platform';

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

export function useSpeechHelperLifecycle(settings: Record<string, string>) {
  const enabled = settings[DICTATION_ENABLED_SETTING] === 'true';
  useEffect(() => {
    if (!speechSupported()) return;
    void invoke(enabled ? 'speech_enable' : 'speech_disable');
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
    retry: () => invoke<SpeechStatus>('speech_enable').then(setStatus),
    remove: () => invoke<SpeechStatus>('speech_remove').then(setStatus),
  };
}
