import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { emitTo, listen } from '@tauri-apps/api/event';
import type { DaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { captureDaemonClient } from '../quickCapture/daemonClient';
import { useSessionStore } from '../store/sessions';
import { getCurrentWebviewWindow } from '@tauri-apps/api/webviewWindow';
import { crewDisplayName } from '../utils/crewName';
import { isMacLikePlatform } from '../shortcuts/platform';
import {
  CAPTURE_READY, CAPTURE_REQUEST, CAPTURE_RESULT, CAPTURE_STATE, CAPTURE_SHORTCUT_SETTING, CAPTURE_FONT,
  DEFAULT_CAPTURE_SHORTCUT, EMPTY_HOST_STATE,
  type CaptureHostState, type CaptureRequest,
} from '../quickCapture/client';

interface NativeStatus { binding: string | null; active: string | null; error?: string }

export function useQuickCaptureHost(daemon: DaemonApi, settings: Record<string, string>, captureRevision = 0) {
  const delivery = useMemo(() => captureDaemonClient(daemon, async sessionId => {
    if (!useSessionStore.getState().selectAgent(sessionId)) throw new Error('The recipient session is unavailable.');
    await invoke('capture_hide');
    const main = getCurrentWebviewWindow(); await main.show(); await main.setFocus();
  }), [daemon.sendCaptureRequest]);
  const supported = isMacLikePlatform() && isTauri();
  const crew = useDaemonStore(store => store.crew);
  const [native, setNative] = useState<NativeStatus>({ binding: null, active: null });
  const [nativeReady, setNativeReady] = useState(false);
  const [instance, setInstance] = useState<string | null>(null);
  const queue = useRef(Promise.resolve());
  const current = useRef({ daemon, settings, delivery });
  useLayoutEffect(() => { current.current = { daemon, settings, delivery }; }, [daemon, settings, delivery]);
  const state: CaptureHostState = {
    connected: daemon.isConnected && daemon.hasReceivedInitialState,
    recipients: [EMPTY_HOST_STATE.recipients[0], ...crew.map(member => ({
      id: member.id, name: crewDisplayName(member.id), detail: member.binding_session ? 'Awake' : 'Asleep',
    }))],
    captureRevision, connectionError: daemon.connectionError ?? undefined,
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
        await current.current.daemon.sendSaveSetting(CAPTURE_SHORTCUT_SETTING, binding ?? '');
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
    const requestListener = listen<CaptureRequest>(CAPTURE_REQUEST, async ({ payload }) => {
      let value: unknown;
      let error: string | undefined;
      try {
        const api = current.current.delivery;
        if (payload.action === 'binding') await bindingRef.current(payload.binding);
        else if (payload.action === 'font') {
          window.dispatchEvent(new CustomEvent(CAPTURE_FONT, { detail: payload.change }));
        }
        else {
          if (!api) throw new Error('Capture delivery is not connected yet. Your draft is retained.');
          switch (payload.action) {
            case 'stage': await api.stage(payload.draft); break;
            case 'submit': value = await api.submit(payload.submission); break;
            case 'resolve': value = await api.resolve(payload.captureId); break;
            case 'recent': value = await api.recent(payload.cursor); break;
            case 'image': value = await api.image(payload.captureId, payload.attachmentId, payload.mediaType); break;
            case 'update': value = await api.update(payload.captureId, payload.update, payload.recipient); break;
            case 'discard': await api.discard(payload.captureId, payload.imageIds); break;
            case 'open': await api.openRecipient(payload.sessionId); break;
          }
        }
      } catch (failure) { error = String(failure); }
      await emitTo('capture', CAPTURE_RESULT, { id: payload.id, value, error });
    });
    const errorListener = listen<string>('capture-error', ({ payload }) => setNative(previous => ({ ...previous, error: payload })));
    const readyListener = listen(CAPTURE_READY, () => emitTo('capture', CAPTURE_STATE, stateRef.current));
    return () => { disposed = true; void requestListener.then(unlisten => unlisten()); void readyListener.then(unlisten => unlisten()); void errorListener.then(unlisten => unlisten()); };
  }, []);

  useEffect(() => {
    if (!supported || !nativeReady || !state.connected || instance === null) return;
    const raw = settings[CAPTURE_SHORTCUT_SETTING];
    const binding = raw === undefined ? (instance === '' ? DEFAULT_CAPTURE_SHORTCUT : null) : raw || null;
    void serial(async () => {
      try {
        await invoke('capture_bind', { binding });
        await invoke('capture_cache', { binding });
        await status();
      } catch (error) { setNative(previous => ({ ...previous, binding, error: String(error) })); }
    });
  }, [settings[CAPTURE_SHORTCUT_SETTING], nativeReady, state.connected, instance]);
  useEffect(() => {
    if (supported) void emitTo('capture', CAPTURE_STATE, state);
  }, [state.connected, state.connectionError, crew, native, captureRevision, state.fontScale, state.keybindings]);
  return { state, setBinding };
}
