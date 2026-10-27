import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import type { AnalyticsStatus } from '../../hooks/daemonAnalyticsEvents';
import { formatBytes, recordCount, sharingLine } from './analyticsFormat';

export function useAnalyticsStatus() {
  const { sendGetAnalyticsStatus } = useDaemonApi();
  const [status, setStatus] = useState<AnalyticsStatus | null>(null);
  const reload = useCallback(() => {
    sendGetAnalyticsStatus().then(setStatus, () => setStatus(null));
  }, [sendGetAnalyticsStatus]);
  useEffect(reload, [reload]);
  return { status, reload };
}

type Confirm = 'clear' | 'forget' | 'deleteServerCopy' | null;

function Row({ title, copy, children }: { title: string; copy: ReactNode; children?: ReactNode }) {
  return (
    <div className="settings-row-card">
      <div>
        <p className="settings-row-title">{title}</p>
        <p className="settings-row-copy">{copy}</p>
      </div>
      {children ?? <span />}
    </div>
  );
}

function Confirmable({ asking, label, onYes, onDone, children }: {
  asking: boolean;
  label: string;
  onYes: () => void;
  onDone: () => void;
  children: ReactNode;
}) {
  if (!asking) return children;
  return (
    <span className="analytics-confirm">
      <button type="button" className="settings-action danger" onClick={() => { onDone(); onYes(); }}>{label}</button>
      <button type="button" className="settings-action" onClick={onDone}>Keep</button>
    </span>
  );
}

function shareCopy(sharing: boolean, status: AnalyticsStatus | null, offeringDelete: boolean): ReactNode {
  if (offeringDelete) return 'Sharing is off. Delete what the server already holds?';
  if (!sharing) return status && `${recordCount(status.waiting)} would be sent.`;
  if (status?.lastRefusal) return <span className="settings-warning">{status.lastRefusal}</span>;
  return sharingLine(status);
}

const toggled = (on: boolean) => (on ? 'false' : 'true');

export function AnalyticsMachine({ status, sharing, sendVents, toolVents, onSetSetting, onDisableRecording, onClear, onForget }: {
  status: AnalyticsStatus | null;
  sharing: boolean;
  sendVents: boolean;
  toolVents: boolean;
  onSetSetting: (key: string, value: string) => void;
  onDisableRecording: () => void;
  onClear: () => void;
  onForget: () => void;
}) {
  const [confirm, setConfirm] = useState<Confirm>(null);
  const done = () => setConfirm(null);
  const toggleSharing = () => {
    onSetSetting('analytics.sharing_enabled', toggled(sharing));
    setConfirm(sharing && status?.lastSentAt ? 'deleteServerCopy' : null);
  };
  return (
    <div className="analytics-machine" data-testid="analytics-machine">
      <Row title="Record on this machine" copy={<>Redacted bus facts, app actions, CLI outcomes and resource samples. New sessions get <code>attn vent</code>.</>}>
        <button type="button" className="settings-action" onClick={onDisableRecording}>Disable</button>
      </Row>
      <Row title="Share with attn's maintainers" copy={shareCopy(sharing, status, confirm === 'deleteServerCopy')}>
        <Confirmable asking={confirm === 'deleteServerCopy'} label="Delete from server" onYes={onForget} onDone={done}>
          <button type="button" className="settings-action" onClick={toggleSharing}>{sharing ? 'Disable' : 'Enable'}</button>
        </Confirmable>
      </Row>
      {(sharing || sendVents) && (
        <Row title="Send vents" copy={sendVents ? 'New attn vents are sent with their text.' : 'Vents stay here unless you send one.'}>
          <button type="button" className="settings-action" onClick={() => onSetSetting('analytics.share_vents', toggled(sendVents))}>{sendVents ? 'Disable' : 'Enable'}</button>
        </Row>
      )}
      <Row title="Tool vents" copy={toolVents ? 'New sessions also vent about their other tools: skills, other CLIs, harness permissions. These never leave this machine.' : 'Agents vent only about attn. Turn on to also hear about their other tools (skills, other CLIs, harness permissions), kept on this machine.'}>
        <button type="button" className="settings-action" onClick={() => onSetSetting('analytics.tool_vents', toggled(toolVents))}>{toolVents ? 'Disable' : 'Enable'}</button>
      </Row>
      <Row title="Kept on this machine for 7 days" copy={`${status ? `${recordCount(status.records)}, ${formatBytes(status.bytes)}. ` : ''}Dismissed and withdrawn vents are kept 30 days.`} />
      <Row title="Forget this install" copy="Deletes this install's data on the server and starts a new install id.">
        <Confirmable asking={confirm === 'forget'} label="Delete from server" onYes={onForget} onDone={done}>
          <button type="button" className="settings-action danger" onClick={() => setConfirm('forget')}>Forget…</button>
        </Confirmable>
      </Row>
      <Row title="Clear the local record" copy={status ? `Deletes ${recordCount(status.records)} (${formatBytes(status.bytes)}) and dismissed vents.` : 'Deletes every record and dismissed vents.'}>
        <Confirmable asking={confirm === 'clear'} label="Clear" onYes={onClear} onDone={done}>
          <button type="button" className="settings-action danger" onClick={() => setConfirm('clear')}>Clear…</button>
        </Confirmable>
      </Row>
    </div>
  );
}
