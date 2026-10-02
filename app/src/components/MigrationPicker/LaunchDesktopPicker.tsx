import { desktopIdSlot, numberedDesktopId } from '../../utils/desktops';
import { useEffect, useRef, useState } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useProfilesStore } from '../../store/profiles';
import { LaunchDesktopKind, type LaunchDesktopItem, type LaunchDesktopSetting } from '../../types/generated';
import { DesktopNameDialog, launchChoiceLabel, newDesktopChoice, type DesktopNameRequest } from '../LaunchDesktopSelect';
import { useMigrationActions } from './useMigrationActions';
import './LaunchDesktopPicker.css';
import { Intro, StepNav } from './MigrationIntro';

const key = (item: LaunchDesktopItem) => `${item.kind}:${item.item_id}`;
const draftKey = (draft: LaunchDesktopSetting) => `${draft.desktop_id ?? ''}|${draft.desktop_name}`;

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
  // New desktops are only created on Finish, so until then they live here.
  const [drafts, setDrafts] = useState(new Map<string, LaunchDesktopSetting>());
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
  const draftList = (profileId: string) => {
    const seen = new Map<string, LaunchDesktopSetting>();
    for (const item of items) {
      const draft = drafts.get(key(item));
      if (draft && item.profile_id === profileId) seen.set(draftKey(draft), draft);
    }
    return [...seen.values()];
  };
  const setDraft = (item: LaunchDesktopItem, draft?: LaunchDesktopSetting) => {
    const next = new Map(drafts);
    if (draft) next.set(key(item), draft);
    else next.delete(key(item));
    setDrafts(next);
  };
  const choose = async (item: LaunchDesktopItem, desktopId: string) => {
    setDraft(item);
    setSaving(true);
    setError('');
    try {
      await sendLaunchDesktopSet(item.kind, item.item_id, { desktop_id: desktopId });
      await sendMigrationGet();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setSaving(false);
    }
  };
  const finish = async () => {
    if (busy) return;
    if (drafts.size) {
      setSaving(true);
      setError('');
      try {
        const created = new Map<string, string>();
        for (const item of items) {
          const draft = drafts.get(key(item));
          if (!draft) continue;
          const desktopId = created.get(draftKey(draft));
          const result = await sendLaunchDesktopSet(item.kind, item.item_id, desktopId ? { desktop_id: desktopId } : draft);
          if (!desktopId && result.item?.setting.desktop_id) created.set(draftKey(draft), result.item.setting.desktop_id);
        }
        setDrafts(new Map());
        await sendMigrationGet();
      } catch (reason) {
        setError(reason instanceof Error ? reason.message : String(reason));
        return;
      } finally {
        setSaving(false);
      }
    }
    actions.finish();
  };
  const own = (item: LaunchDesktopItem, slot = 0) => setNaming({ item, request: { slot, name: item.name } });
  const chooseSlot = (item: LaunchDesktopItem, slot: number) => {
    const desktop = desktops.find(
      (candidate) => candidate.profile_id === item.profile_id && candidate.shortcut_slot === slot,
    );
    const draft = draftList(item.profile_id).find((candidate) => desktopIdSlot(candidate.desktop_id) === slot);
    if (desktop) void choose(item, desktop.id);
    else if (draft) setDraft(item, draft);
    else if (slot >= 5) own(item, slot);
  };
  const focusRow = (index: number) => {
    const next = Math.max(0, Math.min(items.length - 1, index));
    setSelected(next);
    const item = items[next];
    if (item) rows.current.get(key(item))?.focus();
  };
  const choice = (item: LaunchDesktopItem) => drafts.get(key(item)) ?? item.setting;
  const sharing = (item: LaunchDesktopItem) => {
    const draft = drafts.get(key(item));
    if (draft) return `new:${draftKey(draft)}`;
    return item.setting.desktop_id ? `desktop:${item.setting.desktop_id}` : `suggested:${key(item)}`;
  };
  const row = (item: LaunchDesktopItem) => {
    const index = items.indexOf(item);
    const available = desktops.filter((desktop) => desktop.profile_id === item.profile_id);
    const draft = drafts.get(key(item));
    const others = draftList(item.profile_id).filter((candidate) => !draft || draftKey(candidate) !== draftKey(draft));
    const current = choice(item);
    const joined = items.filter((other) => key(other) !== key(item) && sharing(other) === sharing(item));
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
            className={`mp-launch-choice${!current.desktop_id || current.desktop_name ? ' on' : ''}`}
            onClick={() => own(item)}
          >
            A new desktop…
          </button>
          <span className="mp-launch-divider" />
          {[1, 2, 3, 4, 5, 6, 7, 8, 9].map((slot) => {
            const desktop = available.find((candidate) => candidate.shortcut_slot === slot);
            if (!desktop && slot < 5) return null;
            const shared = draftList(item.profile_id).find((candidate) => desktopIdSlot(candidate.desktop_id) === slot);
            const active = current.desktop_id === numberedDesktopId(item.profile_id, slot);
            return (
              <button
                key={slot}
                type="button"
                disabled={busy}
                title={
                  desktop
                    ? `${slot} · ${desktop.name || `Desktop ${slot}`}`
                    : shared
                      ? launchChoiceLabel(shared)
                      : `A new desktop in empty slot ${slot}`
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
                if (event.target.value) void choose(item, event.target.value);
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
          {others.length > 0 && (
            <select
              data-pending-desktops
              aria-label={`New desktops for ${item.name}`}
              disabled={busy}
              value=""
              onChange={(event) => {
                const picked = others.find((candidate) => draftKey(candidate) === event.target.value);
                if (picked) setDraft(item, picked);
              }}
            >
              <option value="">New desktops ({others.length}) ▾</option>
              {others.map((candidate) => (
                <option key={draftKey(candidate)} value={draftKey(candidate)}>
                  {launchChoiceLabel(candidate)}
                </option>
              ))}
            </select>
          )}
        </div>
        <div className="mp-launch-result">
          <strong>{launchChoiceLabel(current)}</strong>
          {!item.confirmed && !draft && <span className="mp-launch-suggested">suggested</span>}
          <small>
            {joined.length
              ? `also used by ${joined.map((other) => other.name).join(', ')}`
              : current.desktop_id && !current.desktop_name
                ? 'Starts beside its active pane'
                : 'Created when you finish'}
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
          void finish();
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
              <b>Desktops you pick are saved as you go</b>
              <br />
              New desktops are created when you finish.
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
              {drafts.size || items.some((item) => item.confirmed) ? 'Your choices are kept.' : 'All as suggested.'}
            </span>
            <button type="button" className="mp-button primary" disabled={busy || !migration} onClick={() => void finish()}>
              {busy ? 'Saving…' : 'Finish setup'} <kbd>⌘↵</kbd>
            </button>
          </footer>
          <div className="mp-launch-keys">
            <kbd>↑</kbd>
            <kbd>↓</kbd> choose · <kbd>N</kbd> a new desktop… · <kbd>1</kbd>–<kbd>4</kbd> that desktop · <kbd>5</kbd>–
            <kbd>9</kbd> a new desktop in that empty slot · <kbd>D</kbd> new desktops of other agents · <kbd>⌘↵</kbd>{' '}
            finish
          </div>
          {naming && (
            <DesktopNameDialog
              request={naming.request}
              onCancel={() => setNaming(null)}
              onChoose={(name) => {
                setDraft(naming.item, newDesktopChoice(naming.item.profile_id, name, naming.request.slot));
                setNaming(null);
              }}
            />
          )}
        </>
      )}
    </main>
  );
}
