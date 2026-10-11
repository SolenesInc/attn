import { useAsksStore } from '../../store/asks';
import { askBelongsTo, type AskHolderSession } from '../../utils/asks';
import './Asks.css';

export function AskMarker({ session }: { session: AskHolderSession }) {
  const count = useAsksStore((state) => state.asks.filter((ask) => askBelongsTo(ask, session)).length);
  if (count === 0) return null;
  const title = count === 1 ? 'Asked you a question' : `Asked you ${count} questions`;
  return (
    <span className="ask-marker" title={title} aria-label={title} data-testid={`ask-marker-${session.id || session.crewMember}`}>
      ?{count > 1 ? count : ''}
    </span>
  );
}
