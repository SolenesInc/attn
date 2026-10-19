import { formatTurnAge } from '../../utils/queueBands';
import type { AskHolderSession, AskListItem } from '../../utils/asks';
import './Asks.css';

export function AskRowSummary({ item, now }: { item: AskListItem<AskHolderSession>; now: number }) {
  return (
    <>
      <span className="asks-row-from">{item.from}</span>
      <span className="asks-row-subject">{item.ask.subject}</span>
      {item.questionCount > 1 && <span className="asks-row-count">{item.questionCount} questions</span>}
      {item.holder?.turnOwed && <span className="asks-row-waiting" role="img" aria-label="Waiting on you" title="Waiting on you" />}
      <span className="asks-row-age">{formatTurnAge(item.ask.created_at, now)}</span>
    </>
  );
}
