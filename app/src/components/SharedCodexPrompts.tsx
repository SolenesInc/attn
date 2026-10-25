import { useEffect, useMemo, useRef } from 'react';
import {
  BLOCKER_LOSS,
  blockerSessionIds,
  useNamedSessions,
  useSharedCodexActions,
  useSharedCodexConfirm,
  useSharedCodexState,
} from '../application/useSharedCodex';
import { staleSessionsOf, useSharedCodexStore } from '../store/sharedCodex';
import type { SharedCodexState } from '../types/generated';
import { ModalDialog } from './MigrationPicker/ModalDialog';
import './MigrationPicker/MigrationPicker.css';
import './SharedCodexPrompts.css';

function useInitialFocus() {
  const ref = useRef<HTMLButtonElement>(null);
  useEffect(() => ref.current?.focus(), []);
  return ref;
}

function RestartNowPrompt({ state, onConfirm, onCancel }: { state: SharedCodexState; onConfirm: () => void; onCancel: () => void }) {
  const cancelRef = useInitialFocus();
  const ids = useMemo(() => blockerSessionIds(state.blockers), [state.blockers]);
  const sessions = useNamedSessions(ids);
  const blockers = state.blockers ?? [];
  return (
    <ModalDialog className="shared-codex-prompt" labelledBy="shared-codex-restart-title" onCancel={onCancel}>
      <div className="mp-dialog-top">
        <h2 id="shared-codex-restart-title">Restart shared Codex now?</h2>
      </div>
      <div className="mp-dialog-body">
        <p>Every Codex tile in this profile goes offline until {state.installed_version} is up. What you typed stays.</p>
        {blockers.length > 0 && (
          <ul className="shared-codex-prompt-list">
            {blockers.map((blocker, index) => (
              <li key={blocker.session_id}>
                <span>{sessions[index].name}{sessions[index].hidden ? ' (hidden)' : ''}</span>
                <span className="shared-codex-prompt-detail">{BLOCKER_LOSS[blocker.reason]}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="mp-dialog-actions">
        <button ref={cancelRef} type="button" className="mp-button" onClick={onCancel}>Cancel</button>
        <button type="button" className="mp-button primary" onClick={onConfirm}>Restart now</button>
      </div>
    </ModalDialog>
  );
}

function ReloadTilesPrompt({ state, onConfirm, onCancel }: { state: SharedCodexState; onConfirm: () => void; onCancel: () => void }) {
  const cancelRef = useInitialFocus();
  const ids = useMemo(() => staleSessionsOf(state), [state]);
  const sessions = useNamedSessions(ids);
  const one = sessions.length === 1;
  return (
    <ModalDialog className="shared-codex-prompt" labelledBy="shared-codex-reload-title" onCancel={onCancel}>
      <div className="mp-dialog-top">
        <h2 id="shared-codex-reload-title">{one ? 'Reload 1 Codex tile?' : `Reload ${sessions.length} Codex tiles?`}</h2>
      </div>
      <div className="mp-dialog-body">
        <p>
          {one ? 'The tile restarts' : 'Each tile restarts'} its Codex on {state.running_version} and resumes its conversation.
          {' '}Text you typed and haven’t sent in {one ? 'it' : 'them'} is lost.
        </p>
        <ul className="shared-codex-prompt-list">
          {sessions.map((session) => <li key={session.id}>{session.name}</li>)}
        </ul>
      </div>
      <div className="mp-dialog-actions">
        <button ref={cancelRef} type="button" className="mp-button" onClick={onCancel}>Cancel</button>
        <button type="button" className="mp-button primary" onClick={onConfirm}>{one ? 'Reload tile' : 'Reload tiles'}</button>
      </div>
    </ModalDialog>
  );
}

export function SharedCodexPrompts() {
  const state = useSharedCodexState();
  const confirm = useSharedCodexConfirm();
  const closeConfirm = useSharedCodexStore((store) => store.closeConfirm);
  const { restart, reloadOldTiles } = useSharedCodexActions();
  if (!state) return null;
  if (confirm === 'restart') return <RestartNowPrompt state={state} onConfirm={() => restart(true)} onCancel={closeConfirm} />;
  if (confirm === 'reload') return <ReloadTilesPrompt state={state} onConfirm={reloadOldTiles} onCancel={closeConfirm} />;
  return null;
}
