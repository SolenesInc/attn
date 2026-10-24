import { useState, type ReactNode } from 'react';

interface Toast {
  message: ReactNode;
  undo?: () => Promise<unknown>;
}

export function useUndoToast() {
  const [toast, setToast] = useState<Toast | null>(null);
  const say = (message: ReactNode, undo?: () => Promise<unknown>) => setToast({ message, undo });
  const fail = (err: unknown) => say(<span className="settings-warning">{err instanceof Error ? err.message : String(err)}</span>);
  return {
    toast,
    say,
    fail,
    run: (action: () => Promise<void>) => { void action().catch(fail); },
    undo: (): boolean => {
      const action = toast?.undo;
      if (!action) return false;
      setToast(null);
      void action().then(() => say('Undone.')).catch(fail);
      return true;
    },
  };
}

export type UndoToast = ReturnType<typeof useUndoToast>;

export function UndoToastBar({ toast }: { toast: UndoToast['toast'] }) {
  if (!toast) return null;
  return (
    <div className="analytics-toast" role="status">
      {toast.message}
      {toast.undo && <span className="analytics-toast-undo"><kbd>u</kbd> Undo</span>}
    </div>
  );
}
