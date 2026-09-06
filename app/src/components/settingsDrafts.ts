import { useEffect, useSyncExternalStore } from 'react';
import type { SavedFlash } from './useSavedFlash';
import type { SessionAgent } from '../types/sessionAgent';
import { useAutosaveSetting, useSettingsAutosave, type SaveSetting } from './SettingsAutosave';

interface DraftDeps {
  active: boolean;
  onSetSetting: SaveSetting;
  savedFlash: SavedFlash;
}

export interface SettingDraftOptions extends DraftDeps {
  actual: string;
  settingKey: string;
  trim?: boolean;
}

export function useSettingDraft({ actual, settingKey, trim = false, onSetSetting, savedFlash }: SettingDraftOptions) {
  const draft = useAutosaveSetting(settingKey, actual, onSetSetting, (value) => trim ? value.trim() : value);
  const commit = async () => {
    if (!draft.dirty) return;
    if (await draft.commit()) savedFlash.flash(settingKey);
  };
  return { ...draft, commit, onKeyDown: (event: { key: string }) => { if (event.key === 'Enter') void commit(); } };
}

type AgentValues = Partial<Record<SessionAgent, string>>;
export interface AgentSettingDraftOptions extends DraftDeps {
  actual: AgentValues;
  settingKey: (agent: SessionAgent) => string;
  flashKey?: (agent: SessionAgent) => string;
  trim?: boolean;
}

export function useAgentSettingDrafts({ actual, settingKey, flashKey = settingKey, trim = false, savedFlash }: AgentSettingDraftOptions) {
  const store = useSettingsAutosave()!;
  useSyncExternalStore(store.subscribe, store.snapshot);
  for (const [agent, value] of Object.entries(actual)) {
    store.ensure(settingKey(agent), value ?? '').serialize = (next) => trim ? next.trim() : next;
  }
  useEffect(() => {
    for (const [agent, value] of Object.entries(actual)) store.sync(settingKey(agent), value ?? '');
  }, [actual, settingKey, store]);
  const commit = async (agent: SessionAgent) => {
    const field = store.ensure(settingKey(agent), actual[agent] ?? '');
    if (field.value === field.saved && !field.pending) return;
    if (await store.commit(settingKey(agent))) savedFlash.flash(flashKey(agent));
  };
  return {
    value: (agent: SessionAgent) => store.ensure(settingKey(agent), actual[agent] ?? '').value,
    set: (agent: SessionAgent, next: string) => store.set(settingKey(agent), next),
    commit,
    apply: (agent: SessionAgent, next: string) => { store.set(settingKey(agent), next); void commit(agent); },
  };
}
