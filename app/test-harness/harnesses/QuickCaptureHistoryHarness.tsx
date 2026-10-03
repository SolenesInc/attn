import { useEffect, useMemo, useState } from 'react';
import { QuickCaptureHistory } from '../../src/components/QuickCaptureHistory';
import { captureDaemonClient } from '../../src/quickCapture/daemonClient';
import type { CaptureItem } from '../../src/quickCapture/client';
import type { HarnessProps } from '../types';
import '../../src/components/QuickCapture.css';

const history: CaptureItem[] = Array.from({ length: 4 }, (_, index) => ({
  id: `capture-${index}`, text: `Saved note ${index}`, recipient: 'chief', readAt: '2026-10-03T12:00:00Z', createdAt: '2026-10-03T11:00:00Z',
  images: [0, 1].map(image => ({ id: `image-${index}-${image}`, name: `Preview ${index}-${image}`, mediaType: 'image/png' })),
}));

export function QuickCaptureHistoryHarness({ onReady }: HarnessProps) {
  const [open, setOpen] = useState(true);
  const client = useMemo(() => captureDaemonClient({ sendCaptureRequest: async command => {
    const response = await fetch('/capture-image-wire', { method: 'POST', body: JSON.stringify(command) });
    if (!response.ok) throw new Error(await response.text());
    return response.json();
  } }), []);
  useEffect(() => onReady(), [onReady]);
  return <>
    <button onClick={() => setOpen(true)}>Open Recent</button>
    <div className="capture" style={{ height: 'calc(100vh - 32px)' }}>
      {open && <QuickCaptureHistory regionRef={null} client={client} history={history}
        hasDraft={false} submitting={false} loading={false} label={() => 'Chief'}
        onRefresh={async () => {}} onBack={() => setOpen(false)} onDiscard={() => {}} />}
    </div>
  </>;
}
