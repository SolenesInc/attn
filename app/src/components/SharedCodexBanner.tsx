import { Fragment, useMemo, useState } from 'react';
import { useNavigationContext } from '../application/AppContexts';
import {
  BLOCKER_REASON,
  blockerSessionIds,
  sharedCodexStage,
  useNamedSessions,
  useSharedCodexActions,
  useSharedCodexState,
  type SharedCodexStage,
} from '../application/useSharedCodex';
import { useNow } from '../hooks/useNow';
import { useProfilesStore } from '../store/profiles';
import { staleSessionsOf, useSharedCodexStore } from '../store/sharedCodex';
import { SharedCodexBlockerReason, type SharedCodexBlocker, type SharedCodexState } from '../types/generated';
import {
  persistDismissedCodexUpdate,
  persistDismissedOldTiles,
  readDismissedCodexUpdate,
  readDismissedOldTiles,
} from '../utils/codexBannerDismissals';

const COUNTDOWN_TICK_MS = 1000;

function withoutFinalPeriod(text: string): string {
  return text.replace(/[.\s]+$/, '');
}

function countdown(forceAt: string, now: number): string {
  const left = Math.max(0, Math.ceil((Date.parse(forceAt) - now) / 1000));
  return `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`;
}

function ForceStopCountdown({ forceAt }: { forceAt: string }) {
  const now = useNow(COUNTDOWN_TICK_MS);
  return <span className="banner-countdown">{countdown(forceAt, now)}</span>;
}

type NamedBlocker = SharedCodexBlocker & { name: string };

function WaitingOn({ blockers }: { blockers: NamedBlocker[] }) {
  const { relaunch } = useSharedCodexActions();
  const { handleSelectSession } = useNavigationContext();
  return (
    <span className="banner-note">
      {' '}Waiting on{' '}
      {blockers.map((blocker, index) => (
        <Fragment key={blocker.session_id}>
          <button
            type="button"
            className="banner-link"
            aria-label={`Go to ${blocker.name}`}
            onClick={() => handleSelectSession(blocker.session_id)}
          >
            {blocker.name}
          </button>{' '}
          ({BLOCKER_REASON[blocker.reason]})
          {blocker.reason === SharedCodexBlockerReason.Starting && (
            <>
              {' '}
              <button
                type="button"
                className="banner-button small"
                aria-label={`Relaunch ${blocker.name}`}
                onClick={() => relaunch(blocker.session_id)}
              >
                Relaunch
              </button>
            </>
          )}
          {index < blockers.length - 1 ? ', ' : '.'}
        </Fragment>
      ))}
    </span>
  );
}

interface StageProps {
  stage: SharedCodexStage;
  state: SharedCodexState;
  blockers: NamedBlocker[];
  installError: string | null;
  onDismissUpdate: () => void;
  onDismissOldTiles: () => void;
}

function StageContent({ stage, state, blockers, installError, onDismissUpdate, onDismissOldTiles }: StageProps) {
  const { install, restart } = useSharedCodexActions();
  const openConfirm = useSharedCodexStore((store) => store.openConfirm);
  switch (stage) {
    case 'failed':
      return (
        <>
          <span>
            Codex {state.installed_version} didn’t start: {withoutFinalPeriod(state.error ?? '')}. Still on {state.running_version}, nothing was interrupted.
          </span>
          <button type="button" className="banner-button" onClick={() => restart(false)}>Retry</button>
        </>
      );
    case 'draining':
      return (
        <span>
          Restarting Codex now: waiting for {blockers.map((blocker) => blocker.name).join(', ') || 'running turns'} to finish. Force stop in{' '}
          <ForceStopCountdown forceAt={state.force_at ?? ''} />.
        </span>
      );
    case 'restarting':
      return <span>Restarting Codex on {state.installed_version}. Tiles reconnect on their own.</span>;
    case 'pending':
      return (
        <>
          <span>
            Codex {state.installed_version} installed. Restarts when agents are idle.
            {blockers.length > 0 && <WaitingOn blockers={blockers} />}
          </span>
          {blockers.length > 0 && (
            <button type="button" className="banner-button" onClick={() => openConfirm('restart')}>Restart now</button>
          )}
        </>
      );
    case 'installing':
      return <span>Installing Codex {state.latest_version} in a terminal tile…</span>;
    case 'available':
      return (
        <>
          <span>
            Codex {state.latest_version} is available.
            {installError && <> Install failed: {withoutFinalPeriod(installError)}.</>}
          </span>
          <button type="button" className="banner-button" onClick={install}>Install</button>
          <button type="button" className="banner-dismiss" onClick={onDismissUpdate} title="Dismiss" aria-label="Dismiss Codex update">
            ×
          </button>
        </>
      );
    case 'oldTiles': {
      const count = staleSessionsOf(state).length;
      return (
        <>
          <span>
            Codex {state.running_version} is running. {count === 1 ? '1 tile still runs' : `${count} tiles still run`} an older Codex.
          </span>
          <button type="button" className="banner-button" onClick={() => openConfirm('reload')}>Reload tiles</button>
          <button type="button" className="banner-dismiss" onClick={onDismissOldTiles} title="Dismiss" aria-label="Dismiss old Codex tiles notice">
            ×
          </button>
        </>
      );
    }
  }
}

export function SharedCodexBanner() {
  const state = useSharedCodexState();
  const installing = useSharedCodexStore((store) => store.installing);
  const installError = useSharedCodexStore((store) => store.installError);
  const clearInstallError = useSharedCodexStore((store) => store.clearInstallError);
  const profileId = useProfilesStore((store) => store.selectedProfileId) ?? '';
  const [dismissedUpdate, setDismissedUpdate] = useState(readDismissedCodexUpdate);
  const [dismissedOldTiles, setDismissedOldTiles] = useState(readDismissedOldTiles);
  const blockerIds = useMemo(() => blockerSessionIds(state?.blockers), [state?.blockers]);
  const blockerSessions = useNamedSessions(blockerIds);

  if (!state) return null;
  const stage = sharedCodexStage(state, { installing, error: installError }, {
    update: dismissedUpdate,
    oldTiles: dismissedOldTiles[profileId] ?? null,
  });
  if (!stage) return null;

  const dismissUpdate = () => {
    if (!state.latest_version) return;
    persistDismissedCodexUpdate(state.latest_version);
    setDismissedUpdate(state.latest_version);
    clearInstallError();
  };
  const dismissOldTiles = () => {
    const next = {
      ...dismissedOldTiles,
      [profileId]: { running: state.running_version ?? '', sessions: staleSessionsOf(state) },
    };
    persistDismissedOldTiles(next);
    setDismissedOldTiles(next);
  };

  return (
    <div className={`update-banner codex-banner${stage === 'failed' ? ' is-failed' : ''}`} role="region" aria-label="Codex updates">
      <StageContent
        stage={stage}
        state={state}
        blockers={(state.blockers ?? []).map((blocker, index) => ({ ...blocker, name: blockerSessions[index].name }))}
        installError={installError}
        onDismissUpdate={dismissUpdate}
        onDismissOldTiles={dismissOldTiles}
      />
    </div>
  );
}
