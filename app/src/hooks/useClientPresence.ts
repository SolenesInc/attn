import { useCallback, useEffect, useRef } from 'react';

// The daemon believes a report for 90s, so three heartbeats fit inside that window: a
// single dropped frame or a blocked event loop never expires a window genuinely watched.
export const PRESENCE_HEARTBEAT_MS = 30_000;

const PRESENCE_INPUT_REPORT_FLOOR_MS = 10_000;

const INPUT_EVENTS = ['pointerdown', 'keydown', 'wheel', 'touchstart'] as const;

export interface ClientPresenceReport {
  visible: boolean;
  dashboardVisible: boolean;
  idleSeconds?: number;
}

export function useClientPresence(
  sendSetClientPresence: (presence: ClientPresenceReport) => void,
  options: { dashboardVisible: boolean; connected: boolean },
) {
  const { dashboardVisible, connected } = options;
  // Synced in an effect rather than during render: a write from a render that never commits
  // would leak into a report.
  const sendRef = useRef(sendSetClientPresence);
  const dashboardRef = useRef(dashboardVisible);
  useEffect(() => {
    sendRef.current = sendSetClientPresence;
    dashboardRef.current = dashboardVisible;
  }, [sendSetClientPresence, dashboardVisible]);

  const lastInputAtRef = useRef<number | null>(null);
  const lastReportAtRef = useRef(0);

  const report = useCallback(() => {
    const now = Date.now();
    lastReportAtRef.current = now;
    const lastInputAt = lastInputAtRef.current;
    sendRef.current({
      visible: typeof document === 'undefined' ? true : document.visibilityState === 'visible',
      dashboardVisible: dashboardRef.current,
      // Omitted, not zeroed, when this window has seen no input at all: zero would claim the
      // user just typed.
      ...(lastInputAt === null ? {} : { idleSeconds: (now - lastInputAt) / 1000 }),
    });
  }, []);

  useEffect(() => {
    if (!connected) return;
    report();
    const timer = window.setInterval(report, PRESENCE_HEARTBEAT_MS);
    return () => window.clearInterval(timer);
  }, [connected, dashboardVisible, report]);

  useEffect(() => {
    if (!connected) return;
    const onVisibility = () => report();
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('focus', onVisibility);
    window.addEventListener('blur', onVisibility);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      window.removeEventListener('focus', onVisibility);
      window.removeEventListener('blur', onVisibility);
    };
  }, [connected, report]);

  useEffect(() => {
    if (!connected) return;
    const onInput = () => {
      lastInputAtRef.current = Date.now();
      if (Date.now() - lastReportAtRef.current >= PRESENCE_INPUT_REPORT_FLOOR_MS) {
        report();
      }
    };
    for (const name of INPUT_EVENTS) {
      window.addEventListener(name, onInput, { capture: true, passive: true });
    }
    return () => {
      for (const name of INPUT_EVENTS) {
        window.removeEventListener(name, onInput, { capture: true });
      }
    };
  }, [connected, report]);
}
