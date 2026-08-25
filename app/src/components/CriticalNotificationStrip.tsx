import './CriticalNotificationStrip.css';

interface CriticalNotificationStripProps {
  count: number;
  title: string;
  onOpen: () => void;
}

export function CriticalNotificationStrip({ count, title, onOpen }: CriticalNotificationStripProps) {
  if (count <= 0) return null;

  const label = title || 'Critical notification';
  const ariaLabel =
    count === 1
      ? `1 unread critical notification: ${label}. Open notifications.`
      : `${count} unread critical notifications, newest: ${label}. Open notifications.`;

  return (
    <button type="button" className="critical-strip" onClick={onOpen} aria-label={ariaLabel}>
      <span className="critical-strip-mark" aria-hidden="true" />
      <span className="critical-strip-text">{label}</span>
      {count > 1 && (
        <span className="critical-strip-count" aria-hidden="true">
          {count}
        </span>
      )}
    </button>
  );
}
