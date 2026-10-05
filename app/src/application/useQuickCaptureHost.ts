import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { emitTo, listen } from '@tauri-apps/api/event';
import type { DaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { useDaemonStore } from '../store/daemonSessions';
import { quickCaptureDaemonClient } from '../quickCapture/daemonClient';
import { crewDisplayName } from '../utils/crewName';
import { isMacLikePlatform } from '../shortcuts/platform';
import {
  QUICK_CAPTURE_READY, QUICK_CAPTURE_REQUEST, QUICK_CAPTURE_RESULT, QUICK_CAPTURE_STATE, QUICK_CAPTURE_SHORTCUT_SETTING, QUICK_CAPTURE_FONT,
  DEFAULT_QUICK_CAPTURE_SHORTCUT, EMPTY_HOST_STATE,
  type QuickCaptureHostState, type QuickCaptureRequest,
} from '../quickCapture/client';

interface NativeStatus { binding: string | null; active: string | null; error?: string }

export function useQuickCaptureHost(daemon: DaemonApi, settings: Record<string, string>) {
  const profileId = useProfilesStore(store => store.selectedProfileId) ?? '';
  const delivery = useMemo(() => quickCaptureDaemonClient(daemon, profileId), [daemon.sendQuickCaptureRequest, profileId]);
  const supported = isMacLikePlatform() && isTauri();
  const crew = useDaemonStore(store => store.crew);
  const [native, setNative] = useState<NativeStatus>({ binding: null, active: null });
  const [nativeReady, setNativeReady] = useState(false);
  const [instance, setInstance] = useState<string | null>(null);
  const queue = useRef(Promise.resolve());
  const current = useRef({ daemon, settings, delivery, profileId });
  useLayoutEffect(() => { current.current = { daemon, settings, delivery, profileId }; }, [daemon, settings, delivery, profileId]);
  const state: QuickCaptureHostState = {
    profileId, connected: !!profileId && daemon.isReady,
    mailboxes: [EMPTY_HOST_STATE.mailboxes[0], ...crew.filter(member => member.profile_id === profileId).map(member => ({
      id: member.id, name: crewDisplayName(member.id), detail: member.binding_session ? 'Awake' : 'Asleep',
    }))],
    connectionError: daemon.connectionError ?? undefined,
    binding: native.binding, activeBinding: native.active, shortcutError: native.error,
    fontScale: Number(settings.uiScale) || 1, keybindings: settings.keybindings_config,
  };
  const stateRef = useRef(state);
  useLayoutEffect(() => { stateRef.current = state; });
  function serial(action: () => Promise<void>) {
    const next = queue.current.catch(() => {}).then(action);
    queue.current = next;
    return next;
  }
  async function status() {
    const result = await invoke<NativeStatus>('capture_status');
    setNative(result); return result;
  }
  async function setBinding(binding: string | null) {
    return serial(async () => {
      const previous = await invoke<NativeStatus>('capture_status');
      await invoke('capture_bind', { binding });
      try {
        await current.current.daemon.sendSaveSetting(QUICK_CAPTURE_SHORTCUT_SETTING, binding ?? '');
      } catch (error) {
        await invoke('capture_bind', { binding: previous.active });
        throw error;
      } finally { await status(); }
      await invoke('capture_cache', { binding });
      await status();
    });
  }
  const bindingRef = useRef(setBinding);
  useLayoutEffect(() => { bindingRef.current = setBinding; });

  useEffect(() => {
    if (!supported) return;
    let disposed = false;
    void Promise.all([invoke<{ instance: string }>('get_build_instance'), invoke<NativeStatus>('capture_status')])
      .then(([name, native]) => { if (!disposed) { setInstance(name.instance); setNative(native); setNativeReady(true); } })
      .catch(error => { if (!disposed) setNative(previous => ({ ...previous, error: String(error) })); });
    const requestListener = listen<QuickCaptureRequest>(QUICK_CAPTURE_REQUEST, async ({ payload }) => {
      let value: unknown;
      let error: string | undefined;
      try {
        const api = current.current.delivery;
        if (payload.action === 'binding') await bindingRef.current(payload.binding);
        else if (payload.action === 'font') {
          window.dispatchEvent(new CustomEvent(QUICK_CAPTURE_FONT, { detail: payload.change }));
        }
        else {
          if (!api) throw new Error('Quick capture delivery is not connected yet. Your draft is retained.');
          if (payload.profileId !== current.current.profileId) throw new Error('The selected profile changed. Your draft is retained in its original profile.');
          switch (payload.action) {
            case 'stage': await api.stage(payload.draft); break;
            case 'submit': value = await api.submit(payload.submission); break;
            case 'resolve': value = await api.resolve(payload.captureId); break;
            case 'recent': value = await api.recent(payload.cursor); break;
            case 'file': value = await api.file(payload.captureId, payload.attachmentId, payload.mediaType); break;
            case 'discard': await api.discard(payload.captureId, payload.fileIds); break;
          }
        }
      } catch (failure) { error = String(failure); }
      await emitTo('capture', QUICK_CAPTURE_RESULT, { id: payload.id, value, error });
    });
    const errorListener = listen<string>('capture-error', ({ payload }) => setNative(previous => ({ ...previous, error: payload })));
    const readyListener = listen(QUICK_CAPTURE_READY, () => emitTo('capture', QUICK_CAPTURE_STATE, stateRef.current));
    void readyListener.then(() => { if (!disposed) return emitTo('capture', QUICK_CAPTURE_STATE, stateRef.current); });
    return () => { disposed = true; void requestListener.then(unlisten => unlisten()); void readyListener.then(unlisten => unlisten()); void errorListener.then(unlisten => unlisten()); };
  }, []);

  useEffect(() => {
    if (!supported || !nativeReady || !state.connected || instance === null) return;
    const raw = settings[QUICK_CAPTURE_SHORTCUT_SETTING];
    const binding = raw === undefined ? (instance === '' ? DEFAULT_QUICK_CAPTURE_SHORTCUT : null) : raw || null;
    void serial(async () => {
      try {
        await invoke('capture_bind', { binding });
        await invoke('capture_cache', { binding });
        await status();
      } catch (error) { setNative(previous => ({ ...previous, binding, error: String(error) })); }
    });
  }, [settings[QUICK_CAPTURE_SHORTCUT_SETTING], nativeReady, state.connected, instance]);
  useEffect(() => {
    if (supported) void emitTo('capture', QUICK_CAPTURE_STATE, state);
  }, [profileId, state.connected, state.connectionError, crew, native, state.fontScale, state.keybindings]);
  return { state, setBinding };
}
