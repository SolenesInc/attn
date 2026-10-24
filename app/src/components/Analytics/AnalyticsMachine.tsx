import { useCallback, useEffect, useState } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import type { AnalyticsStatus } from '../../hooks/daemonAnalyticsEvents';
import { clockTime, formatBytes, recordCount } from './analyticsFormat';

export function useAnalyticsStatus() {
  const { sendGetAnalyticsStatus } = useDaemonApi();
  const [status, setStatus] = useState<AnalyticsStatus | null>(null);
  const reload = useCallback(() => {
    sendGetAnalyticsStatus().then(setStatus, () => setStatus(null));
  }, [sendGetAnalyticsStatus]);
  useEffect(reload, [reload]);
  return { status, reload };
}

export function sharingLine(status: AnalyticsStatus | null): string {
  if (!status) return '';
  const sent = status.lastSentAt ? `Last sent ${clockTime(status.lastSentAt)}` : 'Nothing sent yet';
  return `${sent} · ${status.waiting.toLocaleString()} waiting`;
}

type Confirm = 'clear' | 'forget' | 'deleteServerCopy' | null;

export function AnalyticsMachine({ status, sharing, sendVents, onSetSetting, onDisableRecording, onClear, onForget }: {
  status: AnalyticsStatus | null;
  sharing: boolean;
  sendVents: boolean;
  onSetSetting: (key: string, value: string) => void;
  onDisableRecording: () => void;
  onClear: () => void;
  onForget: () => void;
}) {
  const [confirm, setConfirm] = useState<Confirm>(null);
  const confirmButtons = (label: string, onYes: () => void) => (
    <span className="analytics-confirm">
      <button type="button" className="settings-action danger" onClick={() => { setConfirm(null); onYes(); }}>{label}</button>
      <button type="button" className="settings-action" onClick={() => setConfirm(null)}>Keep</button>
    </span>
  );
  const toggleSharing = () => {
    onSetSetting('analytics.sharing_enabled', sharing ? 'false' : 'true');
    setConfirm(sharing && status?.lastSentAt ? 'deleteServerCopy' : null);
  };
  return (
    <div className="analytics-machine" data-testid="analytics-machine">
      <div className="settings-row-card">
        <div>
          <p className="settings-row-title">Record on this machine</p>
          <p className="settings-row-copy">Redacted bus facts, app actions, CLI outcomes and resource samples. New sessions get <code>attn vent</code>.</p>
        </div>
        <button type="button" className="settings-action" onClick={onDisableRecording}>Disable</button>
      </div>
      <div className="settings-row-card">
        <div>
          <p className="settings-row-title">Share with attn's maintainers</p>
          <p className="settings-row-copy">
            {confirm === 'deleteServerCopy' ? 'Sharing is off. Delete what the server already holds?'
              : !sharing ? status && `${recordCount(status.waiting)} would be sent.`
              : status?.lastRefusal ? <span className="settings-warning">{status.lastRefusal}</span>
              : sharingLine(status)}
          </p>
        </div>
        {confirm === 'deleteServerCopy'
          ? confirmButtons('Delete from server', onForget)
          : <button type="button" className="settings-action" onClick={toggleSharing}>{sharing ? 'Disable' : 'Enable'}</button>}
      </div>
      {(sharing || sendVents) && (
        <div className="settings-row-card">
          <div>
            <p className="settings-row-title">Send vents</p>
            <p className="settings-row-copy">{sendVents ? 'New vents are sent with their text.' : 'Vents stay here unless you send one.'}</p>
          </div>
          <button type="button" className="settings-action" onClick={() => onSetSetting('analytics.share_vents', sendVents ? 'false' : 'true')}>{sendVents ? 'Disable' : 'Enable'}</button>
        </div>
      )}
      <div className="settings-row-card">
        <div>
          <p className="settings-row-title">Kept on this machine for 7 days</p>
          <p className="settings-row-copy">{status && `${recordCount(status.records)}, ${formatBytes(status.bytes)}. `}Dismissed and withdrawn vents are kept 30 days.</p>
        </div>
        <span />
      </div>
      <div className="settings-row-card">
        <div>
          <p className="settings-row-title">Forget this install</p>
          <p className="settings-row-copy">Deletes this install's data on the server and starts a new install id.</p>
        </div>
        {confirm === 'forget'
          ? confirmButtons('Delete from server', onForget)
          : <button type="button" className="settings-action danger" onClick={() => setConfirm('forget')}>Forget…</button>}
      </div>
      <div className="settings-row-card">
        <div>
          <p className="settings-row-title">Clear the local record</p>
          <p className="settings-row-copy">{status ? `Deletes ${recordCount(status.records)} (${formatBytes(status.bytes)}) and dismissed vents.` : 'Deletes every record and dismissed vents.'}</p>
        </div>
        {confirm === 'clear'
          ? confirmButtons('Clear', onClear)
          : <button type="button" className="settings-action danger" onClick={() => setConfirm('clear')}>Clear…</button>}
      </div>
    </div>
  );
}
