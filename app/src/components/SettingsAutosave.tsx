import { createContext, useContext, useEffect, useMemo, useSyncExternalStore, type ReactNode } from 'react';

export type SaveSetting = (key: string, value: string) => void | Promise<void>;
type Field = {
  actual: string;
  value: string;
  saved: string;
  serialize: (value: string) => string;
  error?: string;
  pending?: Promise<boolean>;
  requested: boolean;
  attempting?: string;
  confirmed: boolean;
};

export function createSettingsAutosave(save: SaveSetting) {
  const fields = new Map<string, Field>();
  const listeners = new Set<() => void>();
  let revision = 0;
  const notify = () => { revision++; listeners.forEach((listener) => listener()); };
  const ensure = (key: string, actual: string) => {
    if (!fields.has(key)) fields.set(key, { actual, value: actual, saved: actual, serialize: (v) => v, requested: false, confirmed: false });
    return fields.get(key)!;
  };
  const commit = (key: string): Promise<boolean> => {
    const field = fields.get(key)!;
    if (field.pending) {
      if (field.value !== field.attempting) field.requested = true;
      return field.pending;
    }
    field.requested = true;
    const run = async () => {
      while (field.requested) {
        field.requested = false;
        const draft = field.value;
        if (draft === field.saved) { field.error = undefined; continue; }
        try {
          field.attempting = draft;
          const serialized = field.serialize(draft);
          await save(key, serialized);
          field.saved = draft;
          field.confirmed = true;
          field.error = undefined;
        } catch (error) {
          field.error = error instanceof Error ? error.message : String(error);
          field.confirmed = false;
          if (!field.requested) return false;
        }
      }
      return !field.error;
    };
    field.pending = run().finally(() => { field.pending = undefined; notify(); });
    notify();
    return field.pending;
  };
  return {
    fields, ensure, commit,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    snapshot: () => revision,
    sync(key: string, actual: string) {
      const field = ensure(key, actual);
      if (field.actual === actual) return;
      if (field.value === field.saved && !field.pending) field.value = field.saved = actual;
      field.actual = actual;
      notify();
    },
    set(key: string, value: string) {
      const field = fields.get(key)!;
      field.value = value;
      field.confirmed = false;
      notify();
    },
    needsFlush: () => [...fields.values()].some((field) => field.pending || field.value !== field.saved || field.error),
    async flush() {
      const results = await Promise.all([...fields.keys()].map(commit));
      return results.every(Boolean);
    },
  };
}

type Autosave = ReturnType<typeof createSettingsAutosave>;
const SettingsAutosaveContext = createContext<Autosave | null>(null);

export function SettingsAutosaveProvider({ save, children }: { save: SaveSetting; children: ReactNode }) {
  const store = useMemo(() => createSettingsAutosave(save), [save]);
  return <SettingsAutosaveContext.Provider value={store}>{children}</SettingsAutosaveContext.Provider>;
}

export function useSettingsAutosave() { return useContext(SettingsAutosaveContext); }

export function useAutosaveSetting(key: string, actual: string, save: SaveSetting, serialize: (value: string) => string = (v) => v) {
  const shared = useSettingsAutosave();
  const local = useMemo(() => createSettingsAutosave(save), [save]);
  const store = shared ?? local;
  const field = store.ensure(key, actual);
  field.serialize = serialize;
  useSyncExternalStore(store.subscribe, store.snapshot);
  useEffect(() => { store.sync(key, actual); }, [store, key, actual]);
  return {
    value: field.value,
    set: (value: string) => store.set(key, value),
    commit: () => store.commit(key),
    apply: (value: string) => { store.set(key, value); return store.commit(key); },
    onChange: (event: { target: { value: string } }) => store.set(key, event.target.value),
    onBlur: () => { void store.commit(key); },
    onKeyDown: (event: { key: string }) => { if (event.key === 'Enter') void store.commit(key); },
    dirty: field.value !== field.saved || Boolean(field.pending),
    error: field.error,
    saving: Boolean(field.pending),
    saved: field.confirmed && field.value === field.saved,
  };
}

export function SettingsAutosaveStatus() {
  const store = useSettingsAutosave()!;
  useSyncExternalStore(store.subscribe, store.snapshot);
  const failed = [...store.fields].filter(([, field]) => field.error);
  const saving = [...store.fields.values()].some((field) => field.pending);
  return <div className="settings-autosave-status" aria-live="polite">
    {saving && <span>Saving…</span>}
    {failed.map(([key, field]) => <div key={key} role="alert" className="settings-warning">
      {field.error} <button type="button" className="settings-action" onClick={() => void store.commit(key)}>Retry</button>
    </div>)}
    {!saving && failed.length === 0 && [...store.fields.values()].some((field) => field.confirmed) && [...store.fields.values()].every((field) => field.value === field.saved) && <span>Saved</span>}
  </div>;
}
