import { useEffect, useMemo, useState } from 'react';
import { QuickCaptureHistory } from '../../src/components/QuickCaptureHistory';
import { quickCaptureDaemonClient } from '../../src/quickCapture/daemonClient';
import { useQuickCaptureHistory } from '../../src/quickCapture/useQuickCaptureHistory';
import type { HarnessProps } from '../types';
import '../../src/components/QuickCapture.css';

export function QuickCaptureHistoryHarness({ onReady }: HarnessProps) {
  const [open, setOpen] = useState(true);
  const client = useMemo(() => quickCaptureDaemonClient({ sendQuickCaptureRequest: async command => {
    const response = await fetch('/quick-capture-history-wire', { method: 'POST', body: JSON.stringify(command) });
    if (!response.ok) throw new Error(await response.text());
    return response.json();
  } }, 'profile-history-fixture'), []);
  const { history, nextCursor, loading, error, refresh } = useQuickCaptureHistory(client, open, true);
  useEffect(() => onReady(), [onReady]);
  return <>
    <button onClick={() => setOpen(true)}>Open Recent</button>
    <button onClick={() => void refresh()}>Refresh receipts</button>
    <div className="capture" style={{ height: 'calc(100vh - 32px)' }}>
      {open && <QuickCaptureHistory regionRef={null} client={client} history={history}
        hasDraft={false} submitting={false} loading={loading} nextCursor={nextCursor} label={id => id === 'chief' ? 'Chief' : id}
        onRefresh={refresh} onBack={() => setOpen(false)} onDiscard={() => {}} />}
      {error && <p role="alert">{error}</p>}
    </div>
  </>;
}
