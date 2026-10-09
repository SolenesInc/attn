import { useCallback, useEffect, useMemo, useSyncExternalStore } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { agentLabel, getAgentAvailability, isBuiltinAgent } from '../utils/agentAvailability';
import type { RouteHarness } from './useHarnessRoute';
import type { Harness } from '../types/generated';
import type { DelegationSettingsState } from './daemonDelegationEvents';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';

let harnesses: Harness[] = [];
let initial: DelegationSettingsState | undefined;
let initialVersion = -1;
let request: Promise<DelegationSettingsState> | undefined;
let error = '';
let version = 0;
let generation = 0;
const listeners = new Set<() => void>();
const emit = () => { version++; for (const listener of listeners) listener(); };
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };

export function clearHarnesses() { generation++; harnesses = []; initial = undefined; initialVersion = -1; request = undefined; error = ''; emit(); }
export function rememberHarnesses(state: DelegationSettingsState) {
  harnesses = state.harnesses;
  initial = state;
  initialVersion = useDelegationPreferencesPush.getState().version;
  error = '';
  emit();
}
export function readHarnessPreferences(load: () => Promise<DelegationSettingsState>, fresh = false): Promise<DelegationSettingsState> {
  if (request) return request;
  if (!fresh && initial && initialVersion === useDelegationPreferencesPush.getState().version) return Promise.resolve(initial);
  const current = generation;
  const running = load().then(state => { if (current !== generation) return readHarnessPreferences(load, true); rememberHarnesses(state); return state; })
    .catch((cause: unknown) => { if (current === generation) error = cause instanceof Error ? cause.message : String(cause); throw cause; })
    .finally(() => { if (request === running) request = undefined; emit(); });
  request = running;
  emit();
  return running;
}
export function useHarnesses(active = true) {
  const api = useOptionalDaemonApi();
  const sendDelegationPreferencesGet = api?.sendDelegationPreferencesGet;
  const isConnected = Boolean(api?.isConnected);
  useSyncExternalStore(subscribe, () => version);
  const epoch = generation;
  const retry = useCallback(() => { if (sendDelegationPreferencesGet) void readHarnessPreferences(sendDelegationPreferencesGet, true).catch(() => {}); }, [sendDelegationPreferencesGet]);
  useEffect(() => { if (active && isConnected && sendDelegationPreferencesGet && !harnesses.length) void readHarnessPreferences(sendDelegationPreferencesGet).catch(() => {}); }, [active, isConnected, sendDelegationPreferencesGet, epoch]);
  const currentHarnesses = useMemo(() => harnesses.map(harness => {
    const available = isBuiltinAgent(harness.id) ? api?.settings[`${harness.id}_available`] : undefined;
    return (available === 'true' || available === 'false') && harness.available !== (available === 'true') ? { ...harness, available: available === 'true' } : harness;
  }), [harnesses, api?.settings]);
  return { harnesses: currentHarnesses, hasCatalog: Boolean(initial), error, loading: Boolean(request) || Boolean(active && isConnected && !initial && !error), retry };
}

export function useHarnessChoices(agents: string[], settings: Record<string, string>) {
  const catalog = useHarnesses();
  const harnesses = useMemo<RouteHarness[]>(() => {
    const availability = getAgentAvailability(settings);
    const reported = catalog.harnesses.filter(harness => agents.includes(harness.id));
    const ids = [...reported.map(harness => harness.id), ...agents.filter(agent => !reported.some(harness => harness.id === agent))];
    return ids.map(id => {
      const metadata = reported.find(harness => harness.id === id);
      return {
        ...metadata, id, name: metadata?.name || agentLabel(id),
        available: Boolean(availability[id]) && metadata?.available !== false,
        model_pin: metadata?.model_pin, effort_pin: metadata?.effort_pin, discovery: metadata?.discovery,
      };
    });
  }, [catalog.harnesses, agents, settings]);
  return { ...catalog, harnesses };
}

export function useHeadlessHarnesses(agents: string[], settings: Record<string, string>) {
  const catalog = useHarnessChoices(agents, settings);
  const harnesses = useMemo(() => catalog.harnesses.map(harness => ({
    ...harness,
    available: harness.available && isBuiltinAgent(harness.id) && settings[`${harness.id}_cap_headless_task`] !== 'false',
    model_pin: true, effort_pin: true,
  })), [catalog.harnesses, settings]);
  return { ...catalog, harnesses };
}
