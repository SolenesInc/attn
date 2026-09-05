import { useEffect, useRef, useState } from 'react';
import { afterFramePaints, nextAnimationFrame, settleUi } from '../../src/hooks/uiAutomationSettle';
import type { HarnessProps } from '../types';

declare global {
  interface Window {
    __SETTLE_HARNESS__: {
      resize: (width: number) => void;
      readAfter: (frames: number, tasks: number) => Promise<number>;
      readSettled: () => Promise<number>;
    };
  }
}

export function BridgeSettledReadHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const boxRef = useRef<HTMLDivElement>(null);
  const [measuredWidth, setMeasuredWidth] = useState(0);

  useEffect(() => {
    const box = boxRef.current;
    if (!box) return;
    const observer = new ResizeObserver(([entry]) => {
      setMeasuredWidth(Math.round(entry.contentRect.width));
    });
    observer.observe(box);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const readCommittedWidth = () =>
      Number(document.querySelector('[data-testid="committed-width"]')?.textContent ?? -1);
    window.__SETTLE_HARNESS__ = {
      resize: (width) => {
        if (boxRef.current) boxRef.current.style.width = `${width}px`;
      },
      readAfter: async (frames, tasks) => {
        for (let frame = 0; frame < frames; frame += 1) await nextAnimationFrame();
        for (let task = 0; task < tasks; task += 1) await afterFramePaints();
        return readCommittedWidth();
      },
      readSettled: async () => {
        await settleUi();
        return readCommittedWidth();
      },
    };
    setTriggerRerender(() => () => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div>
      <div ref={boxRef} data-testid="box" style={{ width: 200, height: 40, background: '#8884' }} />
      <output data-testid="committed-width">{measuredWidth}</output>
    </div>
  );
}
