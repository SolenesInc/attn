import { useAsksStore } from '../../store/asks';
import { askBelongsTo, type AskHolderSession } from '../../utils/asks';
import './Asks.css';

export function AskMarker({ session }: { session: AskHolderSession }) {
  const count = useAsksStore((state) => state.asks.filter((ask) => askBelongsTo(ask, session)).length);
  if (count === 0) return null;
  const title = `${count} open ${count === 1 ? 'ask' : 'asks'}`;
  return (
    <span className="ask-marker" role="img" title={title} aria-label={title} data-testid={`ask-marker-${session.id || session.crewMember}`}>
      ?{count > 1 ? count : ''}
    </span>
  );
}
