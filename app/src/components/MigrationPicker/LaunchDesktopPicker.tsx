import { pendingDestinations } from '../../utils/launchDesktops';
import { desktopIdSlot, numberedDesktopId } from '../../utils/desktops';
import { useEffect, useRef, useState } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useProfilesStore } from '../../store/profiles';
import {
  LaunchDesktopKind,
  LaunchDesktopMode,
  type LaunchDesktopItem,
  type LaunchDesktopSetting,
} from '../../types/generated';
import { DesktopNameDialog, type DesktopNameRequest } from '../LaunchDesktopSelect';
import { useMigrationActions } from './useMigrationActions';
import './LaunchDesktopPicker.css';
import { Intro, StepNav } from './MigrationIntro';

const key = (item: LaunchDesktopItem) => `${item.kind}:${item.item_id}`;

function CrewDesktopHelp() {
  const [open, setOpen] = useState(false);
  return (
    <span className="mp-crew-help">
      <span>Change later in Manage crew</span>
      <button
        type="button"
        aria-label="Where to change crew desktops"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        ⓘ
      </button>
      <span role="tooltip" className={`mp-crew-preview${open ? ' open' : ''}`}>
        <span className="mp-preview-sidebar">
          <b>attn</b>
          <span>alder</span>
          <span>keel</span>
          <span>trellis</span>
          <strong>① Manage crew</strong>
          <small>⌘K › Manage crew</small>
        </span>
        <span className="mp-preview-panel">
          <b>Manage crew · keel</b>
          <span>
            Charter &nbsp; <strong>Launch</strong>
          </span>
          <span>Harness &nbsp; Model</span>
          <strong>② Desktop ▾</strong>
        </span>
      </span>
    </span>
  );
}

export function LaunchDesktopPicker() {
  const { sendMigrationGet, sendLaunchDesktopSet } = useDaemonApi();
  const migration = useProfilesStore((state) => state.migration);
  const profiles = useProfilesStore((state) => state.profiles);
  const actions = useMigrationActions();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState(0);
  const [showIntro, setShowIntro] = useState(false);
  const [naming, setNaming] = useState<{ item: LaunchDesktopItem; request: DesktopNameRequest } | null>(null);
  const rows = useRef(new Map<string, HTMLDivElement>());
  useEffect(() => {
    void sendMigrationGet().catch((reason: unknown) =>
      setError(reason instanceof Error ? reason.message : String(reason)),
    );
  }, [sendMigrationGet]);
  const items = migration?.launch_items ?? [];
  const ledgerOpen = items.length > 0 && !showIntro;
  useEffect(() => {
    if (ledgerOpen) rows.current.get(key(items[selected]))?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- focus once when the ledger opens
  }, [ledgerOpen]);
  const desktops = migration?.launch_desktops ?? [];
  const busy = saving || actions.busy;
  const choose = async (item: LaunchDesktopItem, setting: LaunchDesktopSetting) => {
    setSaving(true);
    setError('');
    try {
      await sendLaunchDesktopSet(item.kind, item.item_id, setting);
      await sendMigrationGet();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setSaving(false);
    }
  };
  const own = (item: LaunchDesktopItem, slot = 0) => setNaming({ item, request: { slot, name: item.name } });
  const chooseSlot = (item: LaunchDesktopItem, slot: number) => {
    const desktop = desktops.find(
      (candidate) => candidate.profile_id === item.profile_id && candidate.shortcut_slot === slot,
    );
    const pending = pendingDestinations(
      items,
      item.profile_id,
      item.setting.mode === LaunchDesktopMode.Own ? item.setting.destination_id : undefined,
    ).find((candidate) => desktopIdSlot(candidate.setting.desktop_id) === slot);
    if (desktop) void choose(item, { mode: LaunchDesktopMode.Desktop, desktop_id: desktop.id });
    else if (pending)
      void choose(item, { mode: LaunchDesktopMode.Desktop, destination_id: pending.setting.destination_id });
    else if (slot >= 5) own(item, slot);
  };
  const focusRow = (index: number) => {
    const next = Math.max(0, Math.min(items.length - 1, index));
    setSelected(next);
    const item = items[next];
    if (item) rows.current.get(key(item))?.focus();
  };
  const row = (item: LaunchDesktopItem) => {
    const index = items.indexOf(item);
    const available = desktops.filter((desktop) => desktop.profile_id === item.profile_id);
    const pending = pendingDestinations(
      items,
      item.profile_id,
      item.setting.mode === LaunchDesktopMode.Own ? item.setting.destination_id : undefined,
    );
    const joined = items.filter(
      (other) => key(other) !== key(item) && other.setting.destination_id === item.setting.destination_id,
    );
    const profile = profiles.find((candidate) => candidate.id === item.profile_id)?.name;
    return (
      <div
        key={key(item)}
        data-launch-item={item.item_id}
        ref={(element) => {
          if (element) rows.current.set(key(item), element);
          else rows.current.delete(key(item));
        }}
        className={`mp-launch-row${index === selected ? ' selected' : ''}`}
        tabIndex={index === selected ? 0 : -1}
        onFocus={() => setSelected(index)}
      >
        <div className="mp-launch-who">
          <span className={`mp-launch-glyph ${item.kind}`}>{item.name.slice(0, 1).toUpperCase()}</span>
          <span>
            <strong>{item.name}</strong>
            <small>
              {profile}
              {item.kind === LaunchDesktopKind.Automation ? ' · automation' : ' · crew member'}
            </small>
          </span>
        </div>
        <div className="mp-launch-choices">
          <button
            type="button"
            disabled={busy}
            className={`mp-launch-choice${item.setting.mode === LaunchDesktopMode.Own ? ' on' : ''}`}
            onClick={() => own(item)}
          >
            Its own desktop…
          </button>
          <span className="mp-launch-divider" />
          {[1, 2, 3, 4, 5, 6, 7, 8, 9].map((slot) => {
            const desktop = available.find((candidate) => candidate.shortcut_slot === slot);
            if (!desktop && slot < 5) return null;
            const shared = pending.find((candidate) => desktopIdSlot(candidate.setting.desktop_id) === slot);
            const active = item.setting.desktop_id === numberedDesktopId(item.profile_id, slot);
            return (
              <button
                key={slot}
                type="button"
                disabled={busy}
                title={
                  desktop
                    ? `${slot} · ${desktop.name || `Desktop ${slot}`}`
                    : shared
                      ? `${shared.setting.desktop_name} (new)`
                      : `Its own desktop in empty slot ${slot}`
                }
                className={`mp-launch-choice slot${!desktop && !shared ? ' empty' : ''}${active ? ' on' : ''}`}
                onClick={() => chooseSlot(item, slot)}
              >
                {slot}
              </button>
            );
          })}
          {available.some((desktop) => !desktop.shortcut_slot) && (
            <select
              aria-label={`Other desktops for ${item.name}`}
              disabled={busy}
              value=""
              onChange={(event) => {
                if (event.target.value)
                  void choose(item, { mode: LaunchDesktopMode.Desktop, desktop_id: event.target.value });
              }}
            >
              <option value="">Other desktops ▾</option>
              {available
                .filter((desktop) => !desktop.shortcut_slot)
                .map((desktop) => (
                  <option key={desktop.id} value={desktop.id}>
                    {desktop.name || 'Unnamed desktop'}
                  </option>
                ))}
            </select>
          )}
          {pending.length > 0 && (
            <select
              data-pending-desktops
              aria-label={`New desktops for ${item.name}`}
              disabled={busy}
              value={item.setting.mode === LaunchDesktopMode.Desktop ? (item.setting.destination_id ?? '') : ''}
              onChange={(event) => {
                if (event.target.value)
                  void choose(item, { mode: LaunchDesktopMode.Desktop, destination_id: event.target.value });
              }}
            >
              <option value="">New desktops ({pending.length}) ▾</option>
              {pending.map((candidate) => (
                <option key={candidate.setting.destination_id} value={candidate.setting.destination_id}>
                  {candidate.setting.desktop_name} (new)
                </option>
              ))}
            </select>
          )}
        </div>
        <div className="mp-launch-result">
          <strong>{item.setting.label || item.setting.desktop_name}</strong>
          {!item.confirmed && <span className="mp-launch-suggested">suggested</span>}
          <small>
            {joined.length
              ? `also used by ${joined.map((other) => other.name).join(', ')}`
              : item.setting.pending
                ? 'Appears when the agent starts'
                : 'Starts beside its active pane'}
          </small>
        </div>
      </div>
    );
  };
  return (
    <main
      className="mp-shell mp-launch-shell"
      onKeyDown={(event) => {
        if (busy || naming || event.target instanceof HTMLInputElement || event.target instanceof HTMLSelectElement)
          return;
        if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
          event.preventDefault();
          actions.finish();
          return;
        }
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
          event.preventDefault();
          focusRow(selected + (event.key === 'ArrowDown' ? 1 : -1));
          return;
        }
        const item = items[selected];
        if (!item || event.metaKey || event.ctrlKey || event.altKey) return;
        if (event.key.toLowerCase() === 'n') {
          event.preventDefault();
          own(item);
        }
        if (event.key.toLowerCase() === 'd') {
          event.preventDefault();
          rows.current.get(key(item))?.querySelector<HTMLSelectElement>('[data-pending-desktops]')?.focus();
        }
        if (/^[1-9]$/.test(event.key)) {
          event.preventDefault();
          chooseSlot(item, Number(event.key));
        }
      }}
    >
      <StepNav step={showIntro ? 'intro' : 'launch'} onIntro={() => setShowIntro(true)} hasLaunch={items.length > 0} />
      {showIntro ? (
        <Intro onStart={() => setShowIntro(false)} />
      ) : (
        <>
          <header className="mp-launch-heading">
            <div>
              <span className="mp-eyebrow">One-time setup</span>
              <h1>Where should your agents start?</h1>
            </div>
            <div className="mp-launch-saved">
              <b>Saved as you go</b>
              <br />
              Quit and reopen to resume here.
            </div>
          </header>
          <section className="mp-launch-ledger" aria-label="Launch desktop choices">
            {([LaunchDesktopKind.Automation, LaunchDesktopKind.Crew] as const).map((kind) => {
              const group = items.filter((item) => item.kind === kind);
              return group.length ? (
                <section key={kind}>
                  <div className="mp-launch-group">
                    <h2>{kind === LaunchDesktopKind.Crew ? 'Crew' : 'Automations'}</h2>
                    <span>
                      {group.length} ·{' '}
                      {kind === LaunchDesktopKind.Crew
                        ? 'woken by you or by other agents'
                        : 'each run starts a fresh agent'}
                    </span>
                    {kind === LaunchDesktopKind.Crew && <CrewDesktopHelp />}
                  </div>
                  {group.map(row)}
                </section>
              ) : null;
            })}
            {!migration && <div role="status">Loading launch desktops…</div>}
          </section>
          {(error || actions.notice) && (
            <div className="mp-notice" role="alert">
              {error || actions.notice?.text}
            </div>
          )}
          <footer className="mp-launch-footer">
            <button type="button" className="mp-button" onClick={() => setShowIntro(true)}>
              ← Back
            </button>
            <span className="mp-launch-ready">
              {items.length} agents ready.{' '}
              {items.some((item) => item.confirmed) ? 'Your choices are saved.' : 'All as suggested.'}
            </span>
            <button type="button" className="mp-button primary" disabled={busy || !migration} onClick={actions.finish}>
              {busy ? 'Saving…' : 'Finish setup'} <kbd>⌘↵</kbd>
            </button>
          </footer>
          <div className="mp-launch-keys">
            <kbd>↑</kbd>
            <kbd>↓</kbd> choose · <kbd>N</kbd> its own desktop… · <kbd>1</kbd>–<kbd>4</kbd> that desktop · <kbd>5</kbd>–
            <kbd>9</kbd> its own desktop in that empty slot · <kbd>D</kbd> new desktops of other agents · <kbd>⌘↵</kbd>{' '}
            finish
          </div>
          {naming && (
            <DesktopNameDialog
              request={naming.request}
              onCancel={() => setNaming(null)}
              onChoose={(name) => {
                void choose(naming.item, {
                  mode: LaunchDesktopMode.Own,
                  desktop_name: name,
                  ...(naming.request.slot
                    ? { desktop_id: numberedDesktopId(naming.item.profile_id, naming.request.slot) }
                    : {}),
                });
                setNaming(null);
              }}
            />
          )}
        </>
      )}
    </main>
  );
}
