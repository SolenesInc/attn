import { formatTurnAge } from '../../utils/queueBands';
import type { AskHolderSession, AskListItem } from '../../utils/asks';
import './Asks.css';

export function AskRowSummary({ item, now }: { item: AskListItem<AskHolderSession>; now: number }) {
  const { reply } = item.ask;
  return (
    <>
      <span className="asks-row-from">{item.from}</span>
      <span className="asks-row-subject">{item.ask.subject}</span>
      {item.questionCount > 1 && <span className="asks-row-count">{item.questionCount} questions</span>}
      {!reply && item.holder?.turnOwed && <span className="asks-row-waiting" role="img" aria-label="Waiting on you" title="Waiting on you" />}
      {reply && <span className="asks-row-replied">replied</span>}
      <span className="asks-row-age">{formatTurnAge(reply?.answered_at ?? item.ask.created_at, now)}</span>
    </>
  );
}
