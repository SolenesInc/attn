import { useCallback, useEffect, useMemo, useState } from 'react';
import { useProfilesStore } from '../../store/profiles';
import type { MigrationState } from '../../types/generated';
import { resolveDropTarget } from './dropTarget';
import { MergeDialog } from './MergeDialog';
import { Intro, StepNav, type Step } from './MigrationIntro';
import { MoveDialog } from './MoveDialog';
import {
  allDraftDesktops,
  draftView,
  findDraftDesktop,
  planWithout,
  type DraftDesktopView,
  type DraftView,
  type GroupView,
} from './migrationDraft';
import { BottomBar, DestinationPanel, DragOverlay, plural, SourcePanel, type BoardState } from './PickerPanels';
import { useGroupDrag, type GroupDropTarget } from './useGroupDrag';
import { useLatest } from './useLatest';
import { currentRevision, useDraftReader, useMigrationActions, type Notice } from './useMigrationActions';
import { usePickerKeyboard } from './usePickerKeyboard';
import './MigrationPicker.css';

export { INTRO_SENTENCE } from './MigrationIntro';

type Dialog =
  | { kind: 'merge'; groupId: string; desktopKey: string; revision: number }
  | { kind: 'move'; groupId: string };

function firstUnconfirmedExcept(groupId: string): string | null {
  const migration = useProfilesStore.getState().migration;
  return migration?.groups.find((group) => !group.confirmed && group.group_id !== groupId)?.group_id ?? null;
}

function hasStarted(migration: MigrationState): boolean {
  return migration.can_undo || migration.groups.some((group) => group.confirmed);
}

function defaultSelection(view: DraftView, selectedId: string | null): string | null {
  if (selectedId && view.groupById.has(selectedId)) return selectedId;
  return view.unconfirmed[0] ?? view.groups[0]?.group.group_id ?? null;
}

function dialogStillValid(view: DraftView, dialog: Dialog): boolean {
  if (!view.groupById.has(dialog.groupId)) return false;
  if (dialog.kind === 'move') return true;
  const target = findDraftDesktop(view, dialog.desktopKey);
  return Boolean(target && planWithout(target.tree, dialog.groupId));
}

function NoticeBanner({ notice }: { notice: Notice | null }) {
  if (!notice) return null;
  return (
    <div className={`mp-notice${notice.tone === 'info' ? ' info' : ''}`} role={notice.tone === 'alert' ? 'alert' : 'status'}>
      {notice.text}
    </div>
  );
}

function usePickerDialog(view: DraftView, setNotice: (notice: Notice) => void) {
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const invalid = dialog !== null && !dialogStillValid(view, dialog);
  useEffect(() => {
    if (!invalid) return;
    setDialog(null);
    setNotice({ tone: 'info', text: 'The draft changed in another window, so that move was cancelled. Nothing was applied.' });
  }, [invalid, setNotice]);
  return { dialog: invalid ? null : dialog, setDialog };
}

interface PlacementBoardProps {
  readError: Notice | null;
  migration: MigrationState;
  view: DraftView;
  profileName: string;
  onShowIntro: () => void;
}

function PlacementBoard({ readError, migration, view, profileName, onShowIntro }: PlacementBoardProps) {
  const actions = useMigrationActions();
  const { keep, move, isBusy, setStatus, setNotice } = actions;
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const { dialog, setDialog } = usePickerDialog(view, setNotice);
  const selected = defaultSelection(view, selectedId);

  const keepGroups = useCallback((groupIds: string[], message: string) => {
    keep(groupIds, message, () => setSelectedId(firstUnconfirmedExcept(groupIds[0])));
  }, [keep]);

  const moveGroup = useCallback((groupId: string, desktop: DraftDesktopView, anchorGroupId: string | null, edge: 'left' | 'right' | 'top' | 'bottom', share: number, expectedRevision: number) => {
    move(
      { groupId, desktop, choice: { anchorGroupId, edge, share }, expectedRevision, verb: planWithout(desktop.tree, groupId) ? 'merged into' : 'moved to' },
      () => setSelectedId(firstUnconfirmedExcept(groupId) ?? groupId),
    );
  }, [move]);

  const requestMove = useCallback((desktop: DraftDesktopView) => {
    if (isBusy()) return;
    if (!selected) {
      setStatus('Select a workspace first.');
      return;
    }
    if (planWithout(desktop.tree, selected)) {
      setDialog({ kind: 'merge', groupId: selected, desktopKey: desktop.desktop.key, revision: currentRevision() });
    } else if (desktop.groupIds.includes(selected)) {
      keepGroups([selected], `${view.groupById.get(selected)?.group.title ?? selected} stays on ${desktop.label}.`);
    } else {
      moveGroup(selected, desktop, null, 'right', 0.5, currentRevision());
    }
  }, [isBusy, keepGroups, moveGroup, selected, setDialog, setStatus, view]);

  const keepSelected = useCallback(() => {
    const group = selected ? view.groupById.get(selected)?.group : undefined;
    if (!group) return;
    if (group.confirmed) {
      setStatus(`${group.title} is already confirmed.`);
      return;
    }
    keepGroups([group.group_id], `${group.title} stays on ${view.desktopOfGroup.get(group.group_id)?.label ?? 'its desktop'}.`);
  }, [keepGroups, selected, setStatus, view]);

  usePickerKeyboard({
    active: dialog === null,
    view,
    selected,
    select: setSelectedId,
    requestMove,
    keepSelected,
    openMove: (groupId) => setDialog({ kind: 'move', groupId }),
    undo: actions.undo,
  });

  const viewRef = useLatest(view);
  const resolveTarget = useCallback((groupId: string, x: number, y: number) => resolveDropTarget(viewRef.current, groupId, x, y), [viewRef]);
  const onDrop = useCallback((groupId: string, target: GroupDropTarget) => {
    const desktop = findDraftDesktop(viewRef.current, target.desktopKey);
    if (!desktop) return;
    setSelectedId(groupId);
    moveGroup(groupId, desktop, target.anchorGroupId, target.edge, 0.5, currentRevision());
  }, [moveGroup, viewRef]);
  const { drag, onPointerDown, onLostPointerCapture } = useGroupDrag({ resolveTarget, onDrop });

  const board: BoardState = {
    view,
    selected,
    pulseId: view.unconfirmed[0] ?? null,
    draggingId: drag?.groupId ?? null,
    dropTargetKey: drag?.target?.desktopKey ?? null,
    busy: actions.busy,
  };
  const unconfirmedExtras = view.extras.flatMap((desktop) => desktop.groupIds).filter((id) => !view.groupById.get(id)?.group.confirmed).length;

  return (
    <main className={`mp-shell${drag ? ' dragging' : ''}`} onPointerDown={onPointerDown} onLostPointerCapture={onLostPointerCapture}>
      <StepNav step="place" onIntro={onShowIntro} />
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
      <NoticeBanner notice={actions.notice ?? readError} />
      <div className="mp-board">
        <SourcePanel
          board={board}
          onSelect={setSelectedId}
          onKeep={(row: GroupView) => {
            setSelectedId(row.group.group_id);
            keepGroups([row.group.group_id], `${row.group.title} stays on ${view.desktopOfGroup.get(row.group.group_id)?.label ?? 'its desktop'}.`);
          }}
        />
        <DestinationPanel
          board={board}
          profileName={profileName}
          suggestionAvailable={migration.suggestion_available}
          onSuggest={() => actions.suggest(`${plural(unconfirmedExtras, 'extra')} merged into slots and confirmed. Adjust any placement, or undo.`)}
          onActivate={requestMove}
          onKeep={(desktop, ids) => keepGroups(ids, desktop.desktop.shortcut_slot
            ? `${plural(ids.length, 'group')} confirmed on ${desktop.label}.`
            : `${plural(ids.length, 'group')} kept as ${desktop.label}.`)}
        />
      </div>
      <BottomBar
        board={board}
        canUndo={migration.can_undo}
        status={actions.status}
        onUndo={actions.undo}
        onKeepRemaining={() => keepGroups(view.unconfirmed, `${plural(view.unconfirmed.length, 'group')} kept where they are.`)}
        onFinish={actions.finish}
      />
      <DragOverlay drag={drag} view={view} />
      <PickerDialog
        dialog={dialog}
        view={view}
        revision={migration.revision}
        onClose={() => setDialog(null)}
        onMerge={(groupId, desktop, choice, expected) => moveGroup(groupId, desktop, choice.anchorGroupId, choice.edge, choice.share, expected)}
        onPick={requestMove}
      />
    </main>
  );
}

interface PickerDialogProps {
  dialog: Dialog | null;
  view: DraftView;
  revision: number;
  onClose: () => void;
  onMerge: (groupId: string, desktop: DraftDesktopView, choice: { anchorGroupId: string | null; edge: 'left' | 'right' | 'top' | 'bottom'; share: number }, expectedRevision: number) => void;
  onPick: (desktop: DraftDesktopView) => void;
}

function PickerDialog({ dialog, view, revision, onClose, onMerge, onPick }: PickerDialogProps) {
  const moving = dialog ? view.groupById.get(dialog.groupId) : undefined;
  if (!dialog || !moving) return null;
  if (dialog.kind === 'move') {
    return (
      <MoveDialog
        moving={moving}
        desktops={allDraftDesktops(view)}
        currentKey={view.desktopOfGroup.get(dialog.groupId)?.desktop.key}
        groupById={view.groupById}
        onCancel={onClose}
        onPick={(desktop) => {
          onClose();
          onPick(desktop);
        }}
      />
    );
  }
  const target = findDraftDesktop(view, dialog.desktopKey);
  const remaining = target ? planWithout(target.tree, dialog.groupId) : null;
  if (!target || !remaining) return null;
  return (
    <MergeDialog
      moving={moving}
      target={target}
      remaining={remaining}
      groupById={view.groupById}
      draftChanged={revision !== dialog.revision}
      onCancel={onClose}
      onConfirm={(choice) => {
        onClose();
        onMerge(dialog.groupId, target, choice, dialog.revision);
      }}
    />
  );
}

export function MigrationPicker() {
  const migration = useProfilesStore((state) => state.migration);
  const profiles = useProfilesStore((state) => state.profiles);
  const [step, setStep] = useState<Step | null>(null);
  const readError = useDraftReader();
  const view = useMemo(() => (migration ? draftView(migration) : null), [migration]);

  if (!migration || !view) {
    return (
      <main className="mp-shell">
        <div className="mp-loading" role="status">Loading your desktops…</div>
        <NoticeBanner notice={readError} />
      </main>
    );
  }
  if (step === 'intro' || (step === null && !hasStarted(migration))) {
    return (
      <main className="mp-shell">
        <StepNav step="intro" onIntro={() => undefined} />
        <Intro onStart={() => setStep('place')} />
      </main>
    );
  }
  const profileName = profiles.find((profile) => profile.id === migration.profile_id)?.name ?? 'Default';
  return (
    <PlacementBoard
      readError={readError}
      migration={migration}
      view={view}
      profileName={profileName}
      onShowIntro={() => setStep('intro')}
    />
  );
}
