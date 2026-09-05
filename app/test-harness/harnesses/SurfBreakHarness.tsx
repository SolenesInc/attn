import { useEffect, useState } from 'react';
import { SurfBreak } from '../../src/components/SurfBreak/SurfBreak';
import type { HarnessProps } from '../types';

export function SurfBreakHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [open, setOpen] = useState(true);
  const [waiting, setWaiting] = useState(0);
  useEffect(() => {
    onReady();
    setTriggerRerender(() => setWaiting(count => count + 1));
  }, [onReady, setTriggerRerender]);
  return open ? <SurfBreak waitingCount={waiting}
    onClose={() => { window.__HARNESS__.recordCall('onClose', []); setOpen(false); }}
    onReturnToWaiting={() => { window.__HARNESS__.recordCall('onReturnToWaiting', []); setOpen(false); }}
  /> : <button onClick={() => setOpen(true)}>Go surfing</button>;
}
