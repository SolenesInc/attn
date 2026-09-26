import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { ProfileCommandError } from '../../hooks/daemonProfileEvents';
import { keyCombo } from '../../shortcuts/formatShortcut';
import { isAccelKeyPressed } from '../../shortcuts/platform';
import { useProfilesStore } from '../../store/profiles';
import { ProfileErrorCode, type MigrationState } from '../../types/generated';
import { slotShortcut } from '../../utils/desktops';
import { DraftPreview } from './DraftPreview';
import { MergeDialog, type MergeChoice } from './MergeDialog';
import { MoveDialog } from './MoveDialog';
import {
  draftView,
  nearestEdge,
  planWithout,
  summarize,
  type DraftDesktopView,
  type DraftView,
  type GroupView,
} from './migrationDraft';
import { useGroupDrag, type GroupDropTarget } from './useGroupDrag';
import './MigrationPicker.css';

export const INTRO_SENTENCE = 'Your workspaces are already desktops. Confirm where each one goes before you continue.';

type Step = 'intro' | 'place';

type Dialog =
  | { kind: 'merge'; groupId: string; desktopKey: string; revision: number }
  | { kind: 'move'; groupId: string };

interface Notice {
  tone: 'alert' | 'info';
  text: string;
}

const STALE_NOTICE = 'Another window changed the draft while you were editing. Showing the current draft; nothing was applied.';

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function failureNotice(error: unknown, finishing: boolean): Notice {
  if (error instanceof ProfileCommandError && error.code === ProfileErrorCode.StaleRevision) {
    return { tone: 'alert', text: STALE_NOTICE };
  }
  if (error instanceof ProfileCommandError) {
    return { tone: 'alert', text: `${errorText(error)}. Nothing was applied${finishing ? '. Retry when you’re ready.' : '.'}` };
  }
  return {
    tone: 'alert',
    text: `attn could not reach the daemon (${errorText(error)}). Your choices so far are saved; try again once it reconnects.`,
  };
}

function plural(count: number, word: string): string {
  return `${count} ${word}${count === 1 ? '' : 's'}`;
}

function firstUnconfirmedExcept(groupId: string): string | null {
  const migration = useProfilesStore.getState().migration;
  return migration?.groups.find((group) => !group.confirmed && group.group_id !== groupId)?.group_id ?? null;
}

function hasStarted(migration: MigrationState): boolean {
  return migration.can_undo || migration.groups.some((group) => group.confirmed);
}

function StepNav({ step, onIntro }: { step: Step; onIntro: () => void }) {
  return (
    <div className="mp-topline">
      <div className="mp-brand"><span className="mp-brandmark" aria-hidden="true" />attn</div>
      <nav className="mp-steps" aria-label="Migration steps">
        <button
          type="button"
          className={`mp-step-link ${step === 'intro' ? 'active' : 'completed'}`}
          aria-current={step === 'intro' ? 'step' : undefined}
          onClick={onIntro}
        >
          <i>01</i> What’s changing
        </button>
        <span aria-hidden="true">›</span>
        <span className={step === 'place' ? 'active' : ''} aria-current={step === 'place' ? 'step' : undefined}>
          <i>02</i> Confirm desktops
        </span>
      </nav>
    </div>
  );
}

function Intro({ onStart }: { onStart: () => void }) {
  const startRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    startRef.current?.focus();
  }, []);
  return (
    <section className="mp-welcome" aria-labelledby="mp-welcome-title">
      <div className="mp-eyebrow">A new home for your sessions</div>
      <h1 id="mp-welcome-title">Workspaces are becoming desktops.</h1>
      <p className="mp-welcome-lead">{INTRO_SENTENCE}</p>
      <div className="mp-change-list">
        <div>
          <span className="mp-change-icon" aria-hidden="true">▦</span>
          <div>
            <h2>Each workspace is already a desktop</h2>
            <p>The first nine sit on <kbd>{slotShortcut(1)}</kbd>–<kbd>{slotShortcut(9)}</kbd>.</p>
          </div>
        </div>
        <div>
          <span className="mp-change-icon" aria-hidden="true">✓</span>
          <div>
            <h2>Confirm each one</h2>
            <p>Keep it where it is, or merge it into another desktop. Nothing moves until you finish.</p>
          </div>
        </div>
        <div>
          <span className="mp-change-icon" aria-hidden="true">⊞</span>
          <div>
            <h2>Your splits stay yours</h2>
            <p>Sessions move together.</p>
          </div>
        </div>
      </div>
      <div className="mp-welcome-actions">
        <button type="button" ref={startRef} className="mp-button primary" onClick={onStart}>Confirm desktops →</button>
        <span>Your choices are saved as you go, in every attn window.</span>
      </div>
    </section>
  );
}

function dropOverlayStyle(target: GroupDropTarget): CSSProperties {
  let { left, top, width, height } = target.box;
  if (!target.empty) {
    if (target.edge === 'left' || target.edge === 'right') {
      width /= 2;
      if (target.edge === 'right') left += width;
    } else {
      height /= 2;
      if (target.edge === 'bottom') top += height;
    }
  }
  return { left, top, width, height };
}

function dropLabel(target: GroupDropTarget, view: DraftView, desktop: DraftDesktopView): string {
  if (target.empty) return `Move to ${desktop.label}`;
  const side = target.edge === 'top' ? 'above' : target.edge === 'bottom' ? 'below' : `${target.edge} of`;
  const anchor = target.anchorGroupId ? view.groupById.get(target.anchorGroupId)?.group.title : 'the desktop';
  return `Merge ${side} ${anchor}`;
}

interface DesktopCardProps {
  desktop: DraftDesktopView;
  view: DraftView;
  selectedId: string | null;
  pulseId: string | null;
  draggingId: string | null;
  dropTargetKey: string | null;
  busy: boolean;
  onActivate: (desktop: DraftDesktopView) => void;
  onKeep: (desktop: DraftDesktopView, groupIds: string[]) => void;
}

function DesktopCard({ desktop, view, selectedId, pulseId, draggingId, dropTargetKey, busy, onActivate, onKeep }: DesktopCardProps) {
  const titles = desktop.groupIds.map((id) => view.groupById.get(id)?.group.title ?? id);
  const todo = desktop.groupIds.filter((id) => !view.groupById.get(id)?.group.confirmed);
  const leaves = desktop.groupIds.reduce((sum, id) => sum + (view.groupById.get(id)?.leaves.length ?? 0), 0);
  const slot = desktop.desktop.shortcut_slot;
  const holdsSelection = selectedId !== null && desktop.groupIds.includes(selectedId);
  const classes = [
    'mp-desktop',
    desktop.tree ? '' : 'empty',
    slot ? '' : 'extra',
    desktop.groupIds.length > 0 && todo.length === 0 ? 'all-confirmed' : '',
    holdsSelection ? 'targeted' : '',
    dropTargetKey === desktop.desktop.key ? 'dragover' : '',
  ].filter(Boolean).join(' ');
  let foot;
  if (todo.length > 0) {
    foot = (
      <button
        type="button"
        data-no-drag
        disabled={busy}
        className={`mp-button small${todo.includes(pulseId ?? '') ? ' pulse' : ''}`}
        aria-label={slot ? `Keep ${plural(todo.length, 'group')} on ${desktop.label}` : `Keep ${titles.join(', ')} as an extra desktop`}
        onClick={(event) => {
          event.stopPropagation();
          onKeep(desktop, todo);
        }}
      >
        {slot ? 'Keep here ✓' : 'Keep as extra desktop ✓'}
      </button>
    );
  } else if (desktop.groupIds.length > 0) {
    foot = <span className="mp-badge ok">{slot ? 'Confirmed' : 'Kept as extra'}</span>;
  }
  return (
    <div
      role="button"
      tabIndex={0}
      className={classes}
      data-migration-desktop={desktop.desktop.key}
      aria-label={`${desktop.label}${desktop.tree ? `, ${titles.join(', ')}` : ', empty'}`}
      onClick={() => onActivate(desktop)}
      onKeyDown={(event) => {
        if ((event.key === 'Enter' || event.key === ' ') && event.target === event.currentTarget) {
          event.preventDefault();
          onActivate(desktop);
        }
      }}
    >
      <div className="mp-deskhead">
        <span className="mp-desknumber">
          {slot ? <kbd>{slot}</kbd> : <span className="mp-extra-mark" aria-hidden="true">◌</span>}
          {desktop.label}
        </span>
        <span className="mp-deskmeta">{leaves ? plural(leaves, 'tile') : ''}</span>
      </div>
      <div className="mp-desk-preview">
        <DraftPreview
          tree={desktop.tree}
          groupById={view.groupById}
          selectedId={selectedId}
          draggingId={draggingId}
          draggable
        />
      </div>
      <div className="mp-deskfoot">
        <span className="mp-names">{desktop.tree ? titles.join(' · ') : 'Stays empty'}</span>
        {foot}
      </div>
    </div>
  );
}

interface SourceRowProps {
  view: GroupView;
  location: DraftDesktopView | undefined;
  selected: boolean;
  pulse: boolean;
  dragging: boolean;
  busy: boolean;
  onSelect: () => void;
  onKeep: () => void;
}

function SourceRow({ view, location, selected, pulse, dragging, busy, onSelect, onKeep }: SourceRowProps) {
  const { group } = view;
  const dot = view.failed ? 'failed' : view.launching ? 'pending' : '';
  return (
    <div
      className={`mp-source${selected ? ' selected' : ''}${group.confirmed ? ' confirmed' : ''}${dragging ? ' drag-source' : ''}`}
      data-drag-group={group.group_id}
    >
      <span className="mp-grip" aria-hidden="true">⠿</span>
      <button
        type="button"
        className="mp-source-main"
        role="option"
        aria-selected={selected}
        data-select-group={group.group_id}
        onClick={onSelect}
      >
        <div className="mp-source-title">
          <span className={`mp-dot ${dot}`} />
          <span className="mp-source-name">{group.title}</span>
        </div>
        <div className="mp-source-meta">{summarize(view)}</div>
        {group.directory && <div className="mp-source-meta mp-source-directory" title={group.directory}>{group.directory}</div>}
        <div className="mp-source-badges">
          <span className="mp-badge where">{location?.label ?? 'Unplaced'}</span>
          {group.confirmed ? <span className="mp-badge ok">✓ Confirmed</span> : <span className="mp-badge todo">Confirm</span>}
          {view.launching && <span className="mp-badge state">Launching</span>}
          {view.failed && <span className="mp-badge state">Failed</span>}
        </div>
      </button>
      {!group.confirmed && (
        <button
          type="button"
          data-no-drag
          disabled={busy}
          className={`mp-keep-inline${pulse ? ' pulse' : ''}`}
          aria-label={`Keep ${group.title} on ${location?.label ?? 'its desktop'}`}
          title="Keep here"
          onClick={onKeep}
        >
          ✓
        </button>
      )}
    </div>
  );
}

export function MigrationPicker() {
  const migration = useProfilesStore((state) => state.migration);
  const profiles = useProfilesStore((state) => state.profiles);
  const {
    connectionGeneration,
    sendMigrationGet,
    sendMigrationKeep,
    sendMigrationMove,
    sendMigrationSuggest,
    sendMigrationUndo,
    sendMigrationFinish,
  } = useDaemonApi();
  const [step, setStep] = useState<Step | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);
  const [status, setStatus] = useState('');
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);

  const view = useMemo(() => (migration ? draftView(migration) : null), [migration]);

  useEffect(() => {
    if (!connectionGeneration) return;
    let cancelled = false;
    sendMigrationGet().then(
      () => {
        if (!cancelled) setNotice(null);
      },
      (error: unknown) => {
        if (!cancelled) setNotice(failureNotice(error, false));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [connectionGeneration, sendMigrationGet]);

  const previousGroupsRef = useRef<Map<string, string> | null>(null);
  useEffect(() => {
    if (!migration) return;
    const current = new Map(migration.groups.map((group) => [group.group_id, group.title]));
    const previous = previousGroupsRef.current;
    previousGroupsRef.current = current;
    if (!previous) return;
    const gone = [...previous].filter(([id]) => !current.has(id)).map(([, title]) => title);
    if (gone.length === 0) return;
    setNotice({
      tone: 'info',
      text: `${gone.join(', ')} ${gone.length === 1 ? 'has' : 'have'} no agents left to place, so ${gone.length === 1 ? 'it no longer needs' : 'they no longer need'} confirming.`,
    });
  }, [migration]);

  const selected = view
    ? view.groupById.has(selectedId ?? '')
      ? selectedId
      : view.unconfirmed[0] ?? view.groups[0]?.group.group_id ?? null
    : null;
  const pulseId = view?.unconfirmed[0] ?? null;
  const onIntro = step === 'intro' || (step === null && migration !== null && !hasStarted(migration));

  const run = useCallback(async (send: () => Promise<unknown>, done?: () => void, finishing = false): Promise<boolean> => {
    if (busyRef.current) return false;
    busyRef.current = true;
    setBusy(true);
    setStatus('');
    try {
      await send();
      setNotice(null);
      done?.();
      return true;
    } catch (error) {
      setNotice(failureNotice(error, finishing));
      if (error instanceof ProfileCommandError && error.code === ProfileErrorCode.StaleRevision) {
        sendMigrationGet().catch(() => undefined);
      }
      return false;
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }, [sendMigrationGet]);

  const revision = () => useProfilesStore.getState().migration?.revision ?? 0;

  const keep = useCallback((groupIds: string[], done: string) => {
    if (groupIds.length === 0) return;
    void run(() => sendMigrationKeep(groupIds, revision()), () => {
      setStatus(done);
      setSelectedId(firstUnconfirmedExcept(groupIds[0]));
    });
  }, [run, sendMigrationKeep]);

  const move = useCallback((
    groupId: string,
    desktop: DraftDesktopView,
    choice: MergeChoice,
    expectedRevision: number,
    verb: 'moved to' | 'merged into',
  ) => {
    const title = view?.groupById.get(groupId)?.group.title ?? groupId;
    void run(
      () => sendMigrationMove({
        groupId,
        targetKey: desktop.desktop.key,
        anchorGroupId: choice.anchorGroupId ?? undefined,
        edge: choice.edge,
        share: choice.share,
        expectedRevision,
      }),
      () => {
        setStatus(`${title} ${verb} ${desktop.label}.`);
        setSelectedId(firstUnconfirmedExcept(groupId) ?? groupId);
      },
    );
  }, [run, sendMigrationMove, view]);

  const requestMove = useCallback((desktop: DraftDesktopView) => {
    if (!view || busyRef.current) return;
    if (!selected) {
      setStatus('Select a workspace first.');
      return;
    }
    const remaining = planWithout(desktop.tree, selected);
    const title = view.groupById.get(selected)?.group.title ?? selected;
    if (!remaining) {
      if (desktop.groupIds.includes(selected)) {
        keep([selected], `${title} stays on ${desktop.label}.`);
      } else {
        move(selected, desktop, { anchorGroupId: null, edge: 'right', share: 0.5 }, revision(), 'moved to');
      }
      return;
    }
    setDialog({ kind: 'merge', groupId: selected, desktopKey: desktop.desktop.key, revision: revision() });
  }, [keep, move, selected, view]);

  const undo = useCallback(() => {
    if (!migration?.can_undo) return;
    void run(() => sendMigrationUndo(revision()), () => setStatus('Undone.'));
  }, [migration?.can_undo, run, sendMigrationUndo]);

  const keepSelected = useCallback(() => {
    if (!view || !selected) return;
    const group = view.groupById.get(selected)?.group;
    if (!group) return;
    if (group.confirmed) {
      setStatus(`${group.title} is already confirmed.`);
      return;
    }
    keep([selected], `${group.title} stays on ${view.desktopOfGroup.get(selected)?.label ?? 'its desktop'}.`);
  }, [keep, selected, view]);

  const keyStateRef = useRef({ onIntro, dialog, view, selected, requestMove, keepSelected, undo });
  keyStateRef.current = { onIntro, dialog, view, selected, requestMove, keepSelected, undo };
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const state = keyStateRef.current;
      if (state.onIntro || state.dialog || !state.view || event.defaultPrevented) return;
      const tag = (event.target as HTMLElement | null)?.tagName ?? '';
      if (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA') return;
      const key = event.key.toLowerCase();
      if (isAccelKeyPressed(event) && !event.altKey && !event.shiftKey && key === 'z') {
        event.preventDefault();
        state.undo();
        return;
      }
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      if (/^[1-9]$/.test(event.key)) {
        const desktop = state.view.slots.find((entry) => entry.desktop.shortcut_slot === Number(event.key));
        if (desktop) {
          event.preventDefault();
          state.requestMove(desktop);
        }
      } else if (key === 'k') {
        event.preventDefault();
        state.keepSelected();
      } else if (key === 'm') {
        if (!state.selected) return;
        event.preventDefault();
        setDialog({ kind: 'move', groupId: state.selected });
      } else if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
        event.preventDefault();
        const rows = state.view.groups;
        const index = rows.findIndex((row) => row.group.group_id === state.selected);
        const nextIndex = Math.max(0, Math.min(rows.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
        const nextId = rows[nextIndex]?.group.group_id;
        if (!nextId) return;
        setSelectedId(nextId);
        document.querySelector<HTMLElement>(`[data-select-group="${CSS.escape(nextId)}"]`)?.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, []);

  const viewRef = useRef(view);
  viewRef.current = view;
  const resolveTarget = useCallback((groupId: string, x: number, y: number): GroupDropTarget | null => {
    const current = viewRef.current;
    const element = document.elementFromPoint(x, y) as HTMLElement | null;
    const card = element?.closest<HTMLElement>('[data-migration-desktop]');
    const desktop = current && card
      ? [...current.slots, ...current.extras].find((entry) => entry.desktop.key === card.dataset.migrationDesktop)
      : undefined;
    const preview = card?.querySelector<HTMLElement>('.mp-desk-preview');
    if (!current || !desktop || !preview) return null;
    const remaining = planWithout(desktop.tree, groupId);
    if (!remaining) {
      if (desktop.groupIds.includes(groupId)) return null;
      const target: GroupDropTarget = {
        desktopKey: desktop.desktop.key,
        anchorGroupId: null,
        edge: 'right',
        box: preview.getBoundingClientRect(),
        empty: true,
        label: '',
      };
      return { ...target, label: dropLabel(target, current, desktop) };
    }
    const groupElement = element?.closest<HTMLElement>('[data-migration-group]');
    const anchorGroupId = groupElement?.dataset.migrationGroup ?? null;
    if (anchorGroupId === groupId) return null;
    const box = (groupElement ?? preview).getBoundingClientRect();
    const target: GroupDropTarget = {
      desktopKey: desktop.desktop.key,
      anchorGroupId,
      edge: nearestEdge(box, x, y),
      box,
      empty: false,
      label: '',
    };
    return { ...target, label: dropLabel(target, current, desktop) };
  }, []);

  const onDrop = useCallback((groupId: string, target: GroupDropTarget) => {
    const current = viewRef.current;
    const desktop = current && [...current.slots, ...current.extras].find((entry) => entry.desktop.key === target.desktopKey);
    if (!desktop) return;
    setSelectedId(groupId);
    move(
      groupId,
      desktop,
      { anchorGroupId: target.anchorGroupId, edge: target.edge, share: 0.5 },
      revision(),
      target.empty ? 'moved to' : 'merged into',
    );
  }, [move]);

  const { drag, onPointerDown, onLostPointerCapture } = useGroupDrag({ resolveTarget, onDrop });

  const dialogTargetGone = (() => {
    if (!dialog || !view) return false;
    if (!view.groupById.has(dialog.groupId)) return true;
    if (dialog.kind === 'move') return false;
    const target = [...view.slots, ...view.extras].find((entry) => entry.desktop.key === dialog.desktopKey);
    return !target || !planWithout(target.tree, dialog.groupId);
  })();
  useEffect(() => {
    if (!dialogTargetGone) return;
    setDialog(null);
    setNotice({ tone: 'info', text: 'The draft changed in another window, so that move was cancelled. Nothing was applied.' });
  }, [dialogTargetGone]);

  const profileName = profiles.find((profile) => profile.id === migration?.profile_id)?.name ?? 'Default';

  if (!migration || !view) {
    return (
      <main className="mp-shell">
        <div className="mp-loading" role="status">Loading your desktops…</div>
        {notice && <div className="mp-notice" role="alert">{notice.text}</div>}
      </main>
    );
  }

  if (onIntro) {
    return (
      <main className="mp-shell">
        <StepNav step="intro" onIntro={() => undefined} />
        <Intro onStart={() => setStep('place')} />
      </main>
    );
  }

  const left = view.unconfirmed.length;
  const total = view.groups.length;
  const allDesktops = [...view.slots, ...view.extras];
  const mergeTarget = dialog?.kind === 'merge' ? allDesktops.find((entry) => entry.desktop.key === dialog.desktopKey) : undefined;
  const mergeRemaining = dialog?.kind === 'merge' && mergeTarget ? planWithout(mergeTarget.tree, dialog.groupId) : null;
  const dialogGroup = dialog ? view.groupById.get(dialog.groupId) : undefined;
  const dropTarget = drag?.target ?? null;

  return (
    <main className={`mp-shell${drag ? ' dragging' : ''}`} onPointerDown={onPointerDown} onLostPointerCapture={onLostPointerCapture}>
      <StepNav step="place" onIntro={() => setStep('intro')} />
      <div className="mp-intro">
        <div>
          <div className="mp-eyebrow">One-time setup</div>
          <h1>Confirm your desktops</h1>
          <p>Every workspace already has a desktop. Keep it there, or merge it into another one. Your existing splits stay together.</p>
        </div>
        <div className="mp-save">
          <em aria-hidden="true">●</em> Saved as you go<br />
          <span>Quit and reopen to resume here.</span>
        </div>
      </div>
      {notice && (
        <div className={`mp-notice${notice.tone === 'info' ? ' info' : ''}`} role={notice.tone === 'alert' ? 'alert' : 'status'}>
          {notice.text}
        </div>
      )}
      <div className="mp-board">
        <aside className="mp-source-panel" aria-label="Imported workspaces">
          <div className="mp-panel-title">
            <h2>Your workspaces</h2>
            <span className="mp-counter">{left ? `${left} to confirm` : 'All confirmed'}</span>
          </div>
          <div className="mp-source-hint">Drag onto a desktop to merge. <kbd>K</kbd> keeps it where it is.</div>
          <div className="mp-source-list" role="listbox" aria-label="Workspaces">
            {view.groups.length === 0 && <div className="mp-empty-list">No workspaces left to confirm.</div>}
            {view.groups.map((row) => (
              <SourceRow
                key={row.group.group_id}
                view={row}
                location={view.desktopOfGroup.get(row.group.group_id)}
                selected={row.group.group_id === selected}
                pulse={row.group.group_id === pulseId}
                dragging={drag?.groupId === row.group.group_id}
                busy={busy}
                onSelect={() => setSelectedId(row.group.group_id)}
                onKeep={() => {
                  setSelectedId(row.group.group_id);
                  keep(
                    [row.group.group_id],
                    `${row.group.title} stays on ${view.desktopOfGroup.get(row.group.group_id)?.label ?? 'its desktop'}.`,
                  );
                }}
              />
            ))}
          </div>
        </aside>
        <section className="mp-dest-panel" aria-label="Desktops">
          <div className="mp-dest-heading">
            <div>
              <h2>{profileName} · {plural(allDesktops.length, 'desktop')}</h2>
              <p>
                {left
                  ? <>Drag a workspace onto another desktop to merge it, or select it and press <kbd>1</kbd>–<kbd>9</kbd>. <kbd>K</kbd> keeps it where it is.</>
                  : 'Everything is confirmed. Finish when you’re ready.'}
              </p>
            </div>
            <div className="mp-suggest">
              <button
                type="button"
                className="mp-button quiet"
                disabled={busy || !migration.suggestion_available}
                onClick={() => {
                  const count = view.extras
                    .flatMap((desktop) => desktop.groupIds)
                    .filter((id) => !view.groupById.get(id)?.group.confirmed).length;
                  void run(
                    () => sendMigrationSuggest(revision()),
                    () => setStatus(`${plural(count, 'extra')} merged into slots and confirmed. Adjust any placement, or undo.`),
                  );
                }}
              >
                ✧ Suggest merging the extras
              </button>
              <span>Moves unconfirmed extras into the emptiest slots and confirms them.</span>
            </div>
          </div>
          <div className="mp-section-title"><h3>Shortcut slots</h3><span>{slotShortcut(1)}–{slotShortcut(9)}</span></div>
          <div className="mp-desktop-grid">
            {view.slots.map((desktop) => (
              <DesktopCard
                key={desktop.desktop.key}
                desktop={desktop}
                view={view}
                selectedId={selected}
                pulseId={pulseId}
                draggingId={drag?.groupId ?? null}
                dropTargetKey={dropTarget?.desktopKey ?? null}
                busy={busy}
                onActivate={requestMove}
                onKeep={(entry, ids) => keep(ids, `${plural(ids.length, 'group')} confirmed on ${entry.label}.`)}
              />
            ))}
          </div>
          {view.extras.length > 0 && (
            <>
              <div className="mp-section-title"><h3>Extra desktops</h3><span>reached from the overview · {view.extras.length}</span></div>
              <div className="mp-desktop-grid extras">
                {view.extras.map((desktop) => (
                  <DesktopCard
                    key={desktop.desktop.key}
                    desktop={desktop}
                    view={view}
                    selectedId={selected}
                    pulseId={pulseId}
                    draggingId={drag?.groupId ?? null}
                    dropTargetKey={dropTarget?.desktopKey ?? null}
                    busy={busy}
                    onActivate={requestMove}
                    onKeep={(entry, ids) => keep(ids, `${plural(ids.length, 'group')} kept as ${entry.label}.`)}
                  />
                ))}
              </div>
            </>
          )}
          <div className="mp-dest-tip">
            <strong>Drop at an edge to choose a split.</strong> Drag groups between desktops. Splits inside each group stay together.
          </div>
        </section>
      </div>
      <div className="mp-bottom">
        <div className="mp-bottom-left">
          <button type="button" className="mp-button quiet" disabled={busy || !migration.can_undo} onClick={undo}>↶ Undo</button>
          <span className="mp-status">
            <strong>{total - left} of {total}</strong> confirmed{left ? '' : '. Ready to finish.'}
          </span>
          <span className="mp-status-message" role="status">{status}</span>
        </div>
        <div className="mp-bottom-right">
          {left > 0 && (
            <button
              type="button"
              className="mp-button quiet"
              disabled={busy}
              onClick={() => keep(view.unconfirmed, `${plural(left, 'group')} kept where they are.`)}
            >
              Keep the remaining {left} where they are
            </button>
          )}
          <button
            type="button"
            className="mp-button primary"
            disabled={busy || left > 0}
            onClick={() => void run(() => sendMigrationFinish(revision()), undefined, true)}
          >
            Finish →
          </button>
        </div>
      </div>
      <div className="mp-keyboard-help">
        <span>
          <kbd>↑</kbd> <kbd>↓</kbd> choose · <kbd>K</kbd> keep here · <kbd>1</kbd>–<kbd>9</kbd> move to slot · <kbd>M</kbd> move to any desktop · <kbd>{keyCombo('accel', 'z')}</kbd> undo
        </span>
        <span>Every attn window sees this draft.</span>
      </div>
      {drag && (
        <div className="mp-drag-label" style={{ left: drag.x + 12, top: drag.y + 14 }}>
          {view.groupById.get(drag.groupId)?.group.title}
        </div>
      )}
      {dropTarget && (
        <>
          <div className="mp-drop-preview" style={dropOverlayStyle(dropTarget)} />
          <div className="mp-drop-label" style={{ left: Math.max(dropTarget.box.left, 8), top: Math.max(8, dropTarget.box.top - 30) }}>
            {dropTarget.label}
          </div>
        </>
      )}
      {dialog?.kind === 'merge' && dialogGroup && mergeTarget && mergeRemaining && (
        <MergeDialog
          moving={dialogGroup}
          target={mergeTarget}
          remaining={mergeRemaining}
          groupById={view.groupById}
          draftChanged={migration.revision !== dialog.revision}
          onCancel={() => setDialog(null)}
          onConfirm={(choice) => {
            const { groupId, revision: expected } = dialog;
            setDialog(null);
            move(groupId, mergeTarget, choice, expected, 'merged into');
          }}
        />
      )}
      {dialog?.kind === 'move' && dialogGroup && (
        <MoveDialog
          moving={dialogGroup}
          desktops={allDesktops}
          currentKey={view.desktopOfGroup.get(dialog.groupId)?.desktop.key}
          groupById={view.groupById}
          onCancel={() => setDialog(null)}
          onPick={(desktop) => {
            setDialog(null);
            requestMove(desktop);
          }}
        />
      )}
    </main>
  );
}
