import { Fragment, useMemo, useState } from 'react';
import { useNavigationContext } from '../application/AppContexts';
import {
  BLOCKER_REASON,
  blockerSessionIds,
  sharedCodexStage,
  useNamedSessions,
  useSharedCodexActions,
  useSharedCodexState,
} from '../application/useSharedCodex';
import { useNow } from '../hooks/useNow';
import { useProfilesStore } from '../store/profiles';
import { staleSessionsOf, useSharedCodexStore } from '../store/sharedCodex';
import { SharedCodexBlockerReason } from '../types/generated';
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

export function SharedCodexBanner() {
  const state = useSharedCodexState();
  const installing = useSharedCodexStore((store) => store.installing);
  const installError = useSharedCodexStore((store) => store.installError);
  const openConfirm = useSharedCodexStore((store) => store.openConfirm);
  const clearInstallError = useSharedCodexStore((store) => store.clearInstallError);
  const profileId = useProfilesStore((store) => store.selectedProfileId) ?? '';
  const [dismissedUpdate, setDismissedUpdate] = useState(readDismissedCodexUpdate);
  const [dismissedOldTiles, setDismissedOldTiles] = useState(readDismissedOldTiles);
  const { install, restart, relaunch } = useSharedCodexActions();
  const { handleSelectSession } = useNavigationContext();
  const blockerIds = useMemo(() => blockerSessionIds(state?.blockers), [state?.blockers]);
  const blockerSessions = useNamedSessions(blockerIds);

  if (!state) return null;
  const stage = sharedCodexStage(state, { installing, error: installError }, {
    update: dismissedUpdate,
    oldTiles: dismissedOldTiles[profileId] ?? null,
  });
  if (!stage) return null;

  const blockers = (state.blockers ?? []).map((blocker, index) => ({ ...blocker, ...blockerSessions[index] }));
  const names = blockers.map((blocker) => blocker.name).join(', ');

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

  let content;
  switch (stage) {
    case 'failed':
      content = (
        <>
          <span>
            Codex {state.installed_version} didn’t start: {withoutFinalPeriod(state.error ?? '')}. Still on {state.running_version}, nothing was interrupted.
          </span>
          <button type="button" className="banner-button" onClick={() => restart(false)}>Retry</button>
        </>
      );
      break;
    case 'draining':
      content = (
        <span>
          Restarting Codex now: waiting for {names || 'running turns'} to finish. Force stop in{' '}
          <ForceStopCountdown forceAt={state.force_at ?? ''} />.
        </span>
      );
      break;
    case 'restarting':
      content = <span>Restarting Codex on {state.installed_version}. Tiles reconnect on their own.</span>;
      break;
    case 'pending':
      content = (
        <>
          <span>
            Codex {state.installed_version} installed. Restarts when agents are idle.
            {blockers.length > 0 && (
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
            )}
          </span>
          {blockers.length > 0 && (
            <button type="button" className="banner-button" onClick={() => openConfirm('restart')}>Restart now</button>
          )}
        </>
      );
      break;
    case 'installing':
      content = <span>Installing Codex {state.latest_version} in a terminal tile…</span>;
      break;
    case 'available':
      content = (
        <>
          <span>
            Codex {state.latest_version} is available.
            {installError && <> Install failed: {withoutFinalPeriod(installError)}.</>}
          </span>
          <button type="button" className="banner-button" onClick={install}>Install</button>
          <button type="button" className="banner-dismiss" onClick={dismissUpdate} title="Dismiss" aria-label="Dismiss Codex update">
            ×
          </button>
        </>
      );
      break;
    case 'oldTiles': {
      const count = staleSessionsOf(state).length;
      content = (
        <>
          <span>
            Codex {state.running_version} is running. {count === 1 ? '1 tile still runs' : `${count} tiles still run`} an older Codex.
          </span>
          <button type="button" className="banner-button" onClick={() => openConfirm('reload')}>Reload tiles</button>
          <button type="button" className="banner-dismiss" onClick={dismissOldTiles} title="Dismiss" aria-label="Dismiss old Codex tiles notice">
            ×
          </button>
        </>
      );
      break;
    }
  }

  return (
    <div className={`update-banner codex-banner${stage === 'failed' ? ' is-failed' : ''}`} role="region" aria-label="Codex updates">
      {content}
    </div>
  );
}
