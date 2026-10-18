import { useDaemonStore } from '../store/daemonSessions';
import './ChiefBadge.css';

interface ChiefBadgeProps {
  compact?: boolean;
}

export function ChiefBadge({ compact = false }: ChiefBadgeProps) {
  const name = useDaemonStore(state => state.crew.find(member => member.chief)?.name || 'Chief');
  return (
    <span
      className={`chief-badge ${compact ? 'compact' : ''}`}
      title={name}
      aria-label={name}
    >
      <span aria-hidden="true">⌁</span>
      {!compact && <span>chief</span>}
    </span>
  );
}
