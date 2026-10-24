import { useCallback, useEffect, useState } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import type { AnalyticsStatus } from '../../hooks/daemonAnalyticsEvents';
import { formatBytes, recordCount } from './analyticsFormat';

export function useAnalyticsStatus() {
  const { sendGetAnalyticsStatus } = useDaemonApi();
  const [status, setStatus] = useState<AnalyticsStatus | null>(null);
  const reload = useCallback(() => {
    sendGetAnalyticsStatus().then(setStatus, () => setStatus(null));
  }, [sendGetAnalyticsStatus]);
  useEffect(reload, [reload]);
  return { status, reload };
}

export function AnalyticsMachine({ status, onDisableRecording, onClear }: {
  status: AnalyticsStatus | null;
  onDisableRecording: () => void;
  onClear: () => void;
}) {
  const [confirmClear, setConfirmClear] = useState(false);
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
          <p className="settings-row-title">Kept on this machine for 7 days</p>
          <p className="settings-row-copy">{status && `${recordCount(status.records)}, ${formatBytes(status.bytes)}. `}Dismissed and withdrawn vents are kept 30 days.</p>
        </div>
        <span />
      </div>
      <div className="settings-row-card">
        <div>
          <p className="settings-row-title">Clear the local record</p>
          <p className="settings-row-copy">{status ? `Deletes ${recordCount(status.records)} (${formatBytes(status.bytes)}).` : 'Deletes every record.'}</p>
        </div>
        {confirmClear ? (
          <span className="analytics-confirm">
            <button type="button" className="settings-action danger" onClick={() => { setConfirmClear(false); onClear(); }}>Clear</button>
            <button type="button" className="settings-action" onClick={() => setConfirmClear(false)}>Keep</button>
          </span>
        ) : (
          <button type="button" className="settings-action danger" onClick={() => setConfirmClear(true)}>Clear…</button>
        )}
      </div>
    </div>
  );
}
