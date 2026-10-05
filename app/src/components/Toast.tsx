import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react';
import { createPortal } from 'react-dom';
import { toastHost, subscribeToastHost } from '../utils/toastHost';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useToastStore, type ToastRow } from '../store/toasts';
import './Toast.css';

export function Toast() {
  const visible = useToastStore((state) => state.rows.length > 0);
  return visible ? <ToastLayer /> : null;
}

function ToastLayer() {
  const host = useSyncExternalStore(subscribeToastHost, toastHost, () => null);
  return host ? createPortal(<ToastContent />, host) : null;
}

function ToastContent() {
  const { rows, fading, fade, clear, complete, append } = useToastStore();
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const { sendDesktopShowSession } = useDaemonApi();
  const toastRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const element = toastRef.current;
    element?.showPopover?.();
    return () => element?.hidePopover?.();
  }, []);
  useEffect(() => {
    if (!rows.length || hovered || focused || fading) return;
    const timer = setTimeout(fade, 6000);
    return () => clearTimeout(timer);
  }, [rows, hovered, focused, fading, fade]);
  useEffect(() => {
    if (!fading) return;
    const timer = setTimeout(clear, 150);
    return () => clearTimeout(timer);
  }, [fading, clear]);
  if (!rows.length) return null;
  const activate = async (row: ToastRow) => {
    try {
      if (row.sessionId) await sendDesktopShowSession(row.sessionId);
      else await row.action?.();
      complete(row.id);
    } catch (reason) {
      append({
        message: reason instanceof Error ? reason.message : String(reason),
        source: row.message,
      });
    }
  };
  const actionable = rows.some((row) => row.sessionId || row.action);
  return (
    <div
      ref={toastRef}
      popover="manual"
      className={`toast toast--error ${fading ? '' : 'visible'}`}
      role="alert"
      tabIndex={0}
      aria-live="assertive"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setFocused(false);
      }}
    >
      <span className="toast-glyph error" aria-hidden="true">!</span>
      <div className="toast-content">
        {rows.length > 1 && <strong className="toast-title">{rows.length} notifications</strong>}
        <div className={`toast-list ${rows.length === 1 ? 'toast-list--single' : ''}`}>
          {rows.map((row, index) => (
            <NotificationRow
              key={row.id}
              row={row}
              newest={index === rows.length - 1}
              activate={activate}
            />
          ))}
        </div>
        {actionable && <div className="toast-footer">{rows.length > 1 ? 'click a row to go' : 'click to go'}</div>}
      </div>
    </div>
  );
}

function NotificationRow({
  row,
  newest,
  activate,
}: {
  row: ToastRow;
  newest: boolean;
  activate: (row: ToastRow) => Promise<void>;
}) {
  const label = row.completed ? '✓ Done' : row.actionLabel || 'Click to go';
  const content = (
    <>
      <strong>{row.message}</strong>
      {row.source && <span className="toast-source">{row.source}</span>}
    </>
  );
  return (
    <div className={`toast-row ${newest ? 'toast-row--new' : ''} ${row.completed ? 'toast-row--seen' : ''}`}>
      {row.sessionId || row.action ? (
        <button
          type="button"
          disabled={Boolean(row.action && row.completed)}
          onClick={() => {
            void activate(row);
          }}
        >
          {content}
          <small>{label}</small>
        </button>
      ) : (
        content
      )}
    </div>
  );
}

export function useToast() {
  const append = useToastStore((state) => state.append);
  return {
    showError: useCallback((message: string) => append({ message }), [append]),
  };
}
