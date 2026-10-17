import { useAsksStore } from '../../store/asks';
import { useDaemonStore } from '../../store/daemonSessions';
import { askBelongsTo, type AskHolderSession } from '../../utils/asks';
import './Asks.css';

export function AskMarker({ session }: { session: AskHolderSession }) {
  const seeds = useDaemonStore((state) => state.seeds);
  const count = useAsksStore((state) => state.asks.filter((ask) => askBelongsTo(ask, session, seeds)).length);
  if (count === 0) return null;
  const title = `${count} open ${count === 1 ? 'ask' : 'asks'}`;
  return (
    <span className="ask-marker" role="img" title={title} aria-label={title} data-testid={`ask-marker-${session.id || session.crewMember}`}>
      ?{count > 1 ? count : ''}
    </span>
  );
}
