import { useEffect, useState } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import type { OutpostInfo } from '../types/generated';
import { readClosedOutpostBanner, persistClosedOutpostBanner } from '../utils/outpostBannerDismissals';

const OUTPOST_DONE_CLOSE_SECONDS = 5;

export function outpostNeedsUpdate(row: OutpostInfo, homeBuild: string): boolean {
  return !!homeBuild && (row.build ? row.build !== homeBuild : row.state === 'refused');
}

export function outpostIsUpdating(row: OutpostInfo): boolean {
  return !!row.update && row.update.phase !== 'done' && row.update.phase !== 'failed';
}

export function outpostUpdateText(row: OutpostInfo): string {
  if (!row.update) return '';
  return row.update.outcome || {
    building: 'Building attn for Linux. The first build takes about a minute.',
    downloading: 'Downloading attn for Linux.',
    sending: 'Copying it over.',
    verifying: 'Checking it runs.',
    restarting: 'Restarting.',
    done: 'Up to date.',
    failed: 'Update failed.',
  }[row.update.phase];
}

function names(rows: OutpostInfo[]): string {
  if (rows.length > 2) return `${rows.length} outposts`;
  return rows.map(row => row.name).join(' and ');
}

function outpostBannerHeadline(different: OutpostInfo[], updated: OutpostInfo[]): string {
  if (updated.some(outpostIsUpdating)) return 'Updating outposts.';
  const failed = updated.filter(row => row.update?.phase === 'failed');
  if (failed.length) return `Couldn’t update ${names(failed)}.`;
  if (different.length) return `${names(different)} ${different.length === 1 ? 'runs' : 'run'} a different build than home.`;
  return `Updated ${names(updated)}.`;
}

function DoneCountdown({ paused, onDone }: { paused: boolean; onDone: () => void }) {
  const [left, setLeft] = useState(OUTPOST_DONE_CLOSE_SECONDS);
  useEffect(() => {
    if (paused) return;
    const timer = setInterval(() => setLeft(seconds => seconds - 1), 1000);
    return () => clearInterval(timer);
  }, [paused]);
  useEffect(() => {
    if (left <= 0) onDone();
  }, [left, onDone]);
  return <span className="banner-countdown">Closing in {Math.max(0, left)}s</span>;
}

export function OutpostUpdateBanner() {
  const api = useOptionalDaemonApi();
  const [closed, setClosed] = useState(readClosedOutpostBanner);
  const [paused, setPaused] = useState(false);
  if (!api || !api.homeBuild || api.settings.outposts_enabled !== 'true' || closed === api.homeBuild) return null;
  const different = api.outposts.filter(row => outpostNeedsUpdate(row, api.homeBuild));
  const updated = api.outposts.filter(row => row.update);
  if (!different.length && !updated.length) return null;
  const done = !different.length && updated.every(row => row.update?.phase === 'done');
  const key = done ? [api.homeBuild, ...updated.map(row => row.id)].join('\n') : api.homeBuild;
  if (closed === key) return null;
  const busy = updated.some(outpostIsUpdating);
  const close = () => { persistClosedOutpostBanner(key); setClosed(key); };
  return (
    <div
      className="update-banner"
      role="region"
      aria-label="Outpost updates"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
    >
      <span>
        {outpostBannerHeadline(different, updated)}
        {!done && updated.filter(row => row.update?.phase !== 'done').map(row => <span key={row.id} className="banner-note"> {row.name}: {outpostUpdateText(row)}</span>)}
        {api.outpostCommandError && <span className="banner-note"> {api.outpostCommandError}</span>}
      </span>
      {!!different.length && <button type="button" className="banner-button" disabled={!api.isConnected || busy} onClick={() => different.forEach(row => api.sendUpdateOutpost(row.id))}>{busy ? 'Updating…' : 'Update outposts'}</button>}
      {done && <DoneCountdown key={key} paused={paused} onDone={close} />}
      <button type="button" className="banner-dismiss" aria-label="Dismiss outpost updates" title="Dismiss" onClick={close}>×</button>
    </div>
  );
}
