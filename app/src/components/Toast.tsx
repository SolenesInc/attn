import { useState, useEffect, useCallback } from 'react';
import './Toast.css';

export interface ToastMessage {
  message: string;
  tone: 'error' | 'notice';
  durationMs: number;
}

interface ToastProps {
  toast: ToastMessage | null;
  onDone: () => void;
}

export function Toast({ toast, onDone }: ToastProps) {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    if (toast) {
      setVisible(true);
      const timer = setTimeout(() => {
        setVisible(false);
        setTimeout(onDone, 200);
      }, toast.durationMs);
      return () => clearTimeout(timer);
    }
  }, [toast, onDone]);

  if (!toast) return null;

  const error = toast.tone === 'error';
  return (
    <div
      className={`toast toast--${toast.tone} ${visible ? 'visible' : ''}`}
      role={error ? 'alert' : 'status'}
      aria-live={error ? 'assertive' : 'polite'}
    >
      {error && (
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
          <circle cx="12" cy="12" r="10" />
          <line x1="15" y1="9" x2="9" y2="15" />
          <line x1="9" y1="9" x2="15" y2="15" />
        </svg>
      )}
      <span>{toast.message}</span>
    </div>
  );
}

export function useToast() {
  const [toast, setToast] = useState<ToastMessage | null>(null);

  const showError = useCallback((message: string, options?: { durationMs?: number }) => {
    setToast({ message, tone: 'error', durationMs: options?.durationMs ?? 3000 });
  }, []);

  const showNotice = useCallback((message: string) => {
    setToast({ message, tone: 'notice', durationMs: 3000 });
  }, []);

  const clearToast = useCallback(() => {
    setToast(null);
  }, []);

  return { toast, showError, showNotice, clearToast };
}
