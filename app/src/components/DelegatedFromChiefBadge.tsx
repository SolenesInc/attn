import { useDaemonStore } from '../store/daemonSessions';
import './DelegatedFromChiefBadge.css';

export function DelegatedFromChiefBadge() {
  const name = useDaemonStore(state => state.crew.find(member => member.chief)?.name || 'Chief');
  return (
    <span
      className="delegated-from-chief-badge"
      title={`Delegated from ${name}`}
      aria-label={`Delegated from ${name}`}
    >
      <span aria-hidden="true">↳</span>
    </span>
  );
}
