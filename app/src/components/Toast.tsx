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
    // The approved grouped-toast prototype holds each new arrival for six seconds.
    const timer = setTimeout(fade, 6000);
    return () => clearTimeout(timer);
  }, [rows, hovered, focused, fading, fade]);
  useEffect(() => {
    if (!fading) return;
    // Match the opacity transition in Toast.css before releasing the rows.
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
        tone: 'error',
      });
    }
  };
  const actionable = rows.some((row) => row.sessionId || row.action);
  const error = rows.some((row) => row.tone === 'error');
  return (
    <div
      ref={toastRef}
      popover="manual"
      className={`toast toast--${error ? 'error' : 'notice'} ${fading ? '' : 'visible'}`}
      role={error ? 'alert' : 'status'}
      aria-live={error ? 'assertive' : 'polite'}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setFocused(false);
      }}
    >
      <NotificationGlyph row={rows[0]} error={error} />
      <div className="toast-content">
        {rows.length > 1 && <strong className="toast-title">{rows.length} notifications</strong>}
        <div className={`toast-list ${rows.length === 1 ? 'toast-list--single' : ''}`}>
          {rows.map((row, index) => (
            <NotificationRow
              key={row.id}
              row={row}
              single={rows.length === 1}
              newest={index === rows.length - 1}
              activate={activate}
            />
          ))}
        </div>
        <NotificationFooter hint={actionable ? (rows.length > 1 ? 'click a row to go' : 'click to go') : null} paused={hovered || focused} clear={clear} />
      </div>
    </div>
  );
}

function NotificationGlyph({ row, error }: { row: ToastRow; error: boolean }) {
  const kind = row.launchKind ?? (error ? 'error' : 'notice');
  const glyph = row.launchKind === 'crew' ? '◆' : row.launchKind === 'automation' ? '⟳' : error ? '!' : '✓';
  return (
    <span className={`toast-glyph ${kind}`} aria-hidden="true">
      {glyph}
    </span>
  );
}

function NotificationFooter({ hint, paused, clear }: { hint: string | null; paused: boolean; clear: () => void }) {
  return (
    <div className="toast-footer">
      {hint && <span>{hint}</span>}
      <span>{paused ? 'paused' : 'fades 6s after the last notification'}</span>
      <button className="toast-close" type="button" aria-label="Dismiss notifications" onClick={clear}>
        Dismiss
      </button>
    </div>
  );
}

function NotificationRow({
  row,
  single,
  newest,
  activate,
}: {
  row: ToastRow;
  single: boolean;
  newest: boolean;
  activate: (row: ToastRow) => Promise<void>;
}) {
  const label = row.completed ? '✓ Done' : row.actionLabel || 'Click to go';
  const content = (
    <>
      <strong>{row.message}</strong>
      {single && row.desktopLabel && (
        <span>
          {' '}
          {row.launchKind === 'crew' ? 'woke' : 'started'} on {row.desktopLabel}
        </span>
      )}
      <span className="toast-source">{row.source}</span>
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
    showError: useCallback((message: string) => append({ message, tone: 'error', source: 'attn' }), [append]),
    showNotice: useCallback((message: string) => append({ message, tone: 'notice', source: 'attn' }), [append]),
  };
}
