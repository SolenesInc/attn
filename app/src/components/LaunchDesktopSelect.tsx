import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { useLaunchDesktopStore } from '../store/launchDesktops';
import { LaunchDesktopMode, type Desktop, type LaunchDesktopKind, type LaunchDesktopSetting } from '../types/generated';
import { ModalDialog } from './MigrationPicker/ModalDialog';
import { pendingDestinations } from '../utils/launchDesktops';
import './LaunchDesktopSelect.css';

export interface DesktopNameRequest {
  slot: number;
  name: string;
}

export function DesktopNameDialog({
  request,
  onCancel,
  onChoose,
}: {
  request: DesktopNameRequest;
  onCancel: () => void;
  onChoose: (name: string) => void;
}) {
  const id = useId();
  const [name, setName] = useState(request.name);
  return (
    <ModalDialog labelledBy={id} onCancel={onCancel}>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          event.stopPropagation();
          if (name.trim()) onChoose(name.trim());
        }}
      >
        <h2 id={id}>Its own desktop</h2>
        <label className="launch-name-field">
          Name
          <input
            value={name}
            onFocus={(event) => event.target.select()}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <p className="launch-name-hint">
          {request.slot
            ? `Use ⌘${request.slot} when that slot is free.`
            : 'No ⌘ number. It appears when the agent starts.'}
        </p>
        <div className="mp-dialog-actions">
          <button type="button" className="mp-button" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="mp-button primary" disabled={!name.trim()}>
            Use this name
          </button>
        </div>
      </form>
    </ModalDialog>
  );
}

export function LaunchDesktopSelect({
  kind,
  itemId,
  profileId,
  defaultName,
  value,
  onChange,
  disabled,
}: {
  kind: LaunchDesktopKind;
  itemId?: string | null;
  profileId: string;
  defaultName: string;
  value?: LaunchDesktopSetting;
  onChange: (setting: LaunchDesktopSetting) => void;
  disabled?: boolean;
}) {
  const api = useOptionalDaemonApi();
  const desktops = useProfilesStore((state) => state.desktops);
  const items = useLaunchDesktopStore((state) => state.items);
  const [choices, setChoices] = useState<Desktop[] | null>(null);
  const [error, setError] = useState('');
  const [naming, setNaming] = useState<DesktopNameRequest | null>(null);
  const get = api?.sendLaunchDesktopGet;
  const refresh = useCallback(() => {
    if (!get) return;
    void get(kind, itemId || '')
      .then((result) => {
        setChoices(result.desktops ?? []);
        setError('');
      })
      .catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
  }, [get, kind, itemId]);
  useEffect(refresh, [refresh]);
  const available = useMemo(
    () => (choices ?? desktops).filter((desktop) => desktop.profile_id === profileId),
    [choices, desktops, profileId],
  );
  const pending = pendingDestinations(
    items,
    profileId,
    value?.mode === LaunchDesktopMode.Own ? value.destination_id : undefined,
  );
  const occupied = new Set(available.map((desktop) => desktop.shortcut_slot));
  const selected = value?.destination_id
    ? `destination:${value.destination_id}`
    : value?.desktop_id
      ? `desktop:${value.desktop_id}`
      : '__saved';
  return (
    <>
      <label className="launch-desktop-field">
        <span>Desktop</span>
        <select
          value={selected}
          disabled={disabled}
          onFocus={refresh}
          onChange={(event) => {
            const next = event.target.value;
            if (next === '__own' || next.startsWith('slot:')) {
              setNaming({ slot: next === '__own' ? 0 : Number(next.slice(5)), name: defaultName || 'Agent' });
            } else if (next.startsWith('desktop:')) {
              onChange({ mode: LaunchDesktopMode.Desktop, desktop_id: next.slice(8) });
            } else if (next.startsWith('destination:')) {
              onChange({ mode: LaunchDesktopMode.Desktop, destination_id: next.slice(12) });
            }
          }}
        >
          <option value={selected}>{value?.label || value?.desktop_name || 'Its own desktop'}</option>
          <option value="__own">Its own desktop…</option>
          {available.map((desktop) => (
            <option key={desktop.id} value={`desktop:${desktop.id}`}>
              {desktop.shortcut_slot ? `${desktop.shortcut_slot} · ` : ''}
              {desktop.name || `Desktop ${desktop.shortcut_slot ?? ''}`}
            </option>
          ))}
          {[5, 6, 7, 8, 9]
            .filter((slot) => !occupied.has(slot))
            .map((slot) => (
              <option key={slot} value={`slot:${slot}`}>
                Empty slot {slot}…
              </option>
            ))}
          {pending.map((item) => (
            <option key={item.setting.destination_id} value={`destination:${item.setting.destination_id}`}>
              {item.setting.desktop_name} (new)
            </option>
          ))}
        </select>
        {error && (
          <span className="launch-desktop-error" role="alert">
            {error}
          </span>
        )}
      </label>
      {naming && (
        <DesktopNameDialog
          request={naming}
          onCancel={() => setNaming(null)}
          onChoose={(name) => {
            onChange({ mode: LaunchDesktopMode.Own, desktop_name: name, shortcut_slot: naming.slot });
            setNaming(null);
          }}
        />
      )}
    </>
  );
}
