
import { useCallback, useState } from 'react';
import type { SavedFlash } from './useSavedFlash';
import type { SessionAgent } from '../types/sessionAgent';

type SetSetting = (key: string, value: string) => void;

interface DraftDeps {
  active: boolean;
  onSetSetting: SetSetting;
  savedFlash: SavedFlash;
}

export interface SettingDraftOptions extends DraftDeps {
  actual: string;
  settingKey: string;
  trim?: boolean;
}

export interface SettingDraft {
  value: string;
  set: (next: string) => void;
  commit: () => void;
  onChange: (event: { target: { value: string } }) => void;
  onKeyDown: (event: { key: string }) => void;
}

export function useSettingDraft({
  actual,
  settingKey,
  trim = false,
  active,
  onSetSetting,
  savedFlash,
}: SettingDraftOptions): SettingDraft {
  const [value, setValue] = useState(actual);
  const [seededFrom, setSeededFrom] = useState<string | null>(active ? actual : null);

  if (active && seededFrom !== actual) {
    setSeededFrom(actual);
    setValue(actual);
  } else if (!active && seededFrom !== null) {
    setSeededFrom(null);
  }

  const commit = useCallback(() => {
    const next = trim ? value.trim() : value;
    const current = trim ? actual.trim() : actual;
    if (next === current) return;
    onSetSetting(settingKey, next);
    savedFlash.flash(settingKey);
  }, [actual, onSetSetting, savedFlash, settingKey, trim, value]);

  const onChange = useCallback(
    (event: { target: { value: string } }) => setValue(event.target.value),
    [],
  );

  const onKeyDown = useCallback((event: { key: string }) => {
    if (event.key === 'Enter') commit();
  }, [commit]);

  return { value, set: setValue, commit, onChange, onKeyDown };
}

type AgentValues = Partial<Record<SessionAgent, string>>;

export interface AgentSettingDraftOptions extends DraftDeps {
  /** Must be a stable object: the reseed guard compares it by reference, so a
   *  fresh literal per render reseeds forever. */
  actual: AgentValues;
  settingKey: (agent: SessionAgent) => string;
  flashKey?: (agent: SessionAgent) => string;
  trim?: boolean;
}

export interface AgentSettingDraft {
  value: (agent: SessionAgent) => string;
  set: (agent: SessionAgent, next: string) => void;
  commit: (agent: SessionAgent) => void;
  apply: (agent: SessionAgent, next: string) => void;
}

export function useAgentSettingDrafts({
  actual,
  settingKey,
  flashKey = settingKey,
  trim = false,
  active,
  onSetSetting,
  savedFlash,
}: AgentSettingDraftOptions): AgentSettingDraft {
  const [values, setValues] = useState<AgentValues>(actual);
  const [seededFrom, setSeededFrom] = useState<AgentValues | null>(active ? actual : null);

  if (active && seededFrom !== actual) {
    setSeededFrom(actual);
    setValues(actual);
  } else if (!active && seededFrom !== null) {
    setSeededFrom(null);
  }

  const value = useCallback((agent: SessionAgent) => values[agent] || '', [values]);

  const set = useCallback((agent: SessionAgent, next: string) => {
    setValues((prev) => ({ ...prev, [agent]: next }));
  }, []);

  const commit = useCallback((agent: SessionAgent) => {
    const raw = values[agent] || '';
    const current = actual[agent] || '';
    const next = trim ? raw.trim() : raw;
    if (next === (trim ? current.trim() : current)) return;
    onSetSetting(settingKey(agent), next);
    savedFlash.flash(flashKey(agent));
  }, [actual, flashKey, onSetSetting, savedFlash, settingKey, trim, values]);

  const apply = useCallback((agent: SessionAgent, next: string) => {
    setValues((prev) => ({ ...prev, [agent]: next }));
    onSetSetting(settingKey(agent), next);
    savedFlash.flash(flashKey(agent));
  }, [flashKey, onSetSetting, savedFlash, settingKey]);

  return { value, set, commit, apply };
}
