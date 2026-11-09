import { useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useUpdateActions } from '../hooks/useUpdateActions';
import type { UpdateOutpostProgress, UpdateRun } from '../types/generated';
import { DoneCountdown } from './OutpostUpdateBanner';

const CLOSED_RUN_KEY = 'attn-closed-update-run';

const PHASE_TEXT: Record<string, string> = {
  preparing: 'Building the update.',
  staging: 'Getting the update ready.',
  applying: 'Installing the update. attn will restart; your agents keep running.',
  home_restarted: 'attn restarted.',
  outposts: 'Finishing up.',
};

const OUTPOST_STEP_TEXT: Record<string, string> = {
  connecting: 'Connecting.',
  building: 'Building attn for Linux.',
  downloading: 'Downloading attn for Linux.',
  sending: 'Copying it over.',
  verifying: 'Checking it runs.',
};

export function updateOutpostText(outpost: UpdateOutpostProgress): string {
  switch (outpost.state) {
    case 'staging': return OUTPOST_STEP_TEXT[outpost.detail ?? ''] ?? 'Getting ready.';
    case 'staged': return 'Ready.';
    case 'switching': return 'Switching to the new build.';
    case 'done': return 'Up to date.';
    default: return outpost.detail ?? outpost.state;
  }
}

export function failedUpdateOutposts(run: UpdateRun): UpdateOutpostProgress[] {
  return run.phase === 'failed' ? (run.outposts ?? []).filter(outpost => outpost.state === 'failed') : [];
}

export function updateRunIsOpen(run: UpdateRun): boolean {
  return run.phase !== 'done' && run.phase !== 'failed';
}

function readClosedRun(): string {
  try {
    return localStorage.getItem(CLOSED_RUN_KEY) ?? '';
  } catch {
    return '';
  }
}

function persistClosedRun(id: string) {
  try {
    localStorage.setItem(CLOSED_RUN_KEY, id);
  } catch {
    /* the banner just reappears next launch */
  }
}

export function UpdateRunBanner() {
  const api = useDaemonApi();
  const { startUpdate } = useUpdateActions();
  const [closed, setClosed] = useState(readClosedRun);
  const [paused, setPaused] = useState(false);
  const run = api?.updateRun;
  if (!run || run.id === closed) return null;
  const close = () => {
    persistClosedRun(run.id);
    setClosed(run.id);
    setPaused(false);
  };
  const text = updateRunIsOpen(run) ? PHASE_TEXT[run.phase] : run.outcome ?? '';
  const failed = failedUpdateOutposts(run);
  const needsAttention = (run.outposts ?? []).some(outpost => outpost.state === 'failed' || outpost.state === 'skipped');
  return (
    <div
      className="update-banner"
      role="region"
      aria-label="attn update"
      data-testid="update-run-banner"
      data-phase={run.phase}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
    >
      <span>
        {text}
        {updateRunIsOpen(run) && (run.outposts ?? []).map(outpost => <span key={outpost.id} className="banner-note"> {outpost.name}: {updateOutpostText(outpost)}</span>)}
      </span>
      {run.phase === 'failed' && (
        <button
          type="button"
          className="banner-button"
          disabled={!api.isConnected}
          onClick={() => startUpdate()}
        >
          Retry
        </button>
      )}
      {failed.length > 0 && (
        <button
          type="button"
          className="banner-button"
          disabled={!api.isConnected}
          onClick={() => startUpdate(undefined, failed.map(outpost => outpost.id))}
        >
          Update without {failed.length === 1 ? failed[0].name : `${failed.length} outposts`}
        </button>
      )}
      {run.phase === 'done' && !needsAttention && <DoneCountdown key={run.id} paused={paused} onDone={close} />}
      {!updateRunIsOpen(run) && (
        <button type="button" className="banner-dismiss" aria-label="Dismiss update" title="Dismiss" onClick={close}>×</button>
      )}
    </div>
  );
}
