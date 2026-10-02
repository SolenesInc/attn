import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useShortcut } from '../shortcuts/useShortcut';
import type { ShortcutId } from '../shortcuts/registry';

type AnnotationSendResult =
  | { kind: 'sent' }
  | { kind: 'skipped' }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string };

export type AnnotationSendOutcome<T extends AnnotationSendResult> =
  | { kind: 'sending' }
  | T
  | { kind: 'error'; message: string }
  | null;

interface UseAnnotationSendOptions<T extends AnnotationSendResult> {
  send: () => T | null | Promise<T | null>;
  shortcutId: ShortcutId;
  enabled: boolean;
  sentClearMs: number;
  identity?: string;
}

export function useAnnotationSend<T extends AnnotationSendResult>({
  send,
  shortcutId,
  enabled,
  sentClearMs,
  identity,
}: UseAnnotationSendOptions<T>) {
  const sendRef = useRef(send);
  const gate = useMemo(() => ({ sending: false }), [identity]);
  const currentGate = useRef(gate);
  const [display, setDisplay] = useState<{ gate: typeof gate; outcome: AnnotationSendOutcome<T> } | null>(null);
  const outcome = display?.gate === gate ? display.outcome : null;
  const setOutcome = useCallback((next: AnnotationSendOutcome<T>) => {
    if (currentGate.current === gate) setDisplay({ gate, outcome: next });
  }, [gate]);

  useLayoutEffect(() => {
    sendRef.current = send;
    currentGate.current = gate;
  }, [send, gate]);

  const runSend = useCallback((action: () => T | null | Promise<T | null>) => {
    if (gate.sending) {
      return;
    }
    gate.sending = true;

    let result: T | null | Promise<T | null>;
    try {
      result = action();
    } catch (error) {
      gate.sending = false;
      setOutcome({
        kind: 'error',
        message: error instanceof Error ? error.message : 'Send failed',
      });
      return;
    }

    if (result === null) {
      gate.sending = false;
      return;
    }
    if (!(result instanceof Promise)) {
      gate.sending = false;
      setOutcome(result);
      return;
    }

    setOutcome({ kind: 'sending' });
    void result
      .then((next) => {
        if (next !== null) {
          setOutcome(next);
        }
      })
      .catch((error: unknown) => {
        setOutcome({
          kind: 'error',
          message: error instanceof Error ? error.message : 'Send failed',
        });
      })
      .finally(() => {
        gate.sending = false;
      });
  }, [gate, setOutcome]);

  const sendNow = useCallback(() => runSend(sendRef.current), [runSend]);
  const sendAlternative = useCallback(
    (alternative: () => T | null | Promise<T | null>) => runSend(alternative),
    [runSend],
  );

  useShortcut(shortcutId, sendNow, enabled);

  useEffect(() => {
    if (outcome?.kind !== 'sent') {
      return;
    }
    const timer = window.setTimeout(() => setOutcome(null), sentClearMs);
    return () => window.clearTimeout(timer);
  }, [outcome, sentClearMs, setOutcome]);

  const clearOutcome = useCallback(() => setOutcome(null), [setOutcome]);
  return { outcome, send: sendNow, sendAlternative, clearOutcome };
}
