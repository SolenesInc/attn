import { useState } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import type { OutpostInfo } from '../types/generated';
import { readDismissedOutpostBuild, persistDismissedOutpostBuild } from '../utils/outpostBannerDismissals';

export function outpostNeedsUpdate(row: OutpostInfo, homeBuild: string): boolean {
  return !!homeBuild && (row.build ? row.build !== homeBuild : row.state === 'refused');
}

export function outpostIsUpdating(row: OutpostInfo): boolean {
  return !!row.update && row.update.phase !== 'done' && row.update.phase !== 'failed';
}

export function outpostUpdateText(row: OutpostInfo): string {
  if (!row.update) return '';
  return row.update.outcome || {
    building: 'Building the Linux daemon…',
    downloading: 'Downloading the Linux daemon…',
    sending: 'Sending the update…',
    verifying: 'Verifying the update…',
    restarting: 'Restarting and reconnecting…',
    done: "Updated to the home's build.",
    failed: 'Update failed.',
  }[row.update.phase];
}

export function OutpostUpdateBanner() {
  const api = useOptionalDaemonApi();
  const [dismissed, setDismissed] = useState(readDismissedOutpostBuild);
  if (!api || !api.homeBuild || api.settings.outposts_enabled !== 'true' || dismissed === api.homeBuild) return null;
  const different = api.outposts.filter(row => outpostNeedsUpdate(row, api.homeBuild));
  const updated = api.outposts.filter(row => row.update);
  if (!different.length && !updated.length) return null;
  const busy = updated.some(outpostIsUpdating);
  return (
    <div className="update-banner" role="region" aria-label="Outpost updates">
      <span>
        {different.length ? "Outposts need your home's build." : 'Outposts are on your home’s build.'}
        {updated.map(row => <span key={row.id} className="banner-note"> {row.name}: {outpostUpdateText(row)}</span>)}
        {api.outpostCommandError && <span className="banner-note"> {api.outpostCommandError}</span>}
      </span>
      {!!different.length && <button type="button" className="banner-button" disabled={!api.isConnected || busy} onClick={() => different.forEach(row => api.sendUpdateOutpost(row.id))}>{busy ? 'Updating…' : 'Update outposts'}</button>}
      <button type="button" className="banner-dismiss" aria-label="Dismiss outpost updates" title="Dismiss" onClick={() => { persistDismissedOutpostBuild(api.homeBuild); setDismissed(api.homeBuild); }}>×</button>
    </div>
  );
}
