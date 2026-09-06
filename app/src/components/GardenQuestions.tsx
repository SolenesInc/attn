// PROTOTYPE (seed s-j3j5bk). Two placements for the same queue, so the choice
// can be made by looking at it: a banner inside the panel, a rail beside it.
import { useState } from 'react';
import { Markdown } from './Markdown';
import { agoWords, waitedWords, type Asking, type SeedQuestion } from './seedQuestions';
import './GardenQuestions.css';

export interface AnswerHandlers {
  onAnswer: (questionId: string, text: string) => void;
  onDismiss: (questionId: string) => void;
  onClear?: (questionId: string) => void;
  onOpenSeed?: (seedId: string) => void;
}

function AskMark() {
  return <span className="garden-ask__mark" aria-hidden="true">?</span>;
}

function AnswerForm({
  question,
  autoFocus,
  onAnswer,
  onDismiss,
}: { question: SeedQuestion; autoFocus?: boolean } & Pick<AnswerHandlers, 'onAnswer' | 'onDismiss'>) {
  const [text, setText] = useState('');
  const answer = () => {
    if (!text.trim()) return;
    onAnswer(question.id, text.trim());
    setText('');
  };
  return (
    <form
      className="garden-ask__form"
      onSubmit={(event) => {
        event.preventDefault();
        answer();
      }}
    >
      <textarea
        className="garden-ask__input"
        aria-label="Your answer"
        placeholder="Your call…"
        autoFocus={autoFocus}
        value={text}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            answer();
          }
        }}
      />
      <div className="garden-ask__actions">
        <button type="button" className="garden-ask__quiet" onClick={() => onDismiss(question.id)}>
          Not deciding this
        </button>
        <button type="submit" className="garden-ask__go" disabled={!text.trim()}>
          Answer <kbd>⌘↵</kbd>
        </button>
      </div>
    </form>
  );
}

/** The question where the seed itself is being read: reader page and tile. */
export function SeedQuestionCard({
  question,
  compact,
  onAnswer,
  onDismiss,
}: {
  question: SeedQuestion;
  compact?: boolean;
  onAnswer?: AnswerHandlers['onAnswer'];
  onDismiss?: AnswerHandlers['onDismiss'];
}) {
  if (question.status === 'open') {
    return (
      <section className={`garden-ask${compact ? ' is-compact' : ''}`} aria-label="A question for you">
        <div className="garden-ask__head">
          <AskMark />
          <strong>Needs you</strong>
          <span className="garden-ask__by">
            {question.asked_by} asked · waiting {waitedWords(question.asked_at)}
          </span>
        </div>
        <Markdown className="garden-ask__text" breaks>{question.text}</Markdown>
        {onAnswer && onDismiss && (
          <AnswerForm question={question} onAnswer={onAnswer} onDismiss={onDismiss} />
        )}
      </section>
    );
  }

  const label = question.status === 'answered'
    ? 'You answered'
    : question.status === 'dismissed'
      ? 'You passed on this'
      : `${question.asked_by} withdrew the question`;
  return (
    <section className={`garden-ask is-closed is-${question.status}${compact ? ' is-compact' : ''}`} aria-label="A settled question">
      <div className="garden-ask__head">
        <AskMark />
        <strong>{label}</strong>
        <span className="garden-ask__by">{agoWords(question.resolved_at ?? question.asked_at)}</span>
      </div>
      <Markdown className="garden-ask__text is-quoted" breaks>{question.text}</Markdown>
      {question.resolution && (
        <Markdown className="garden-ask__resolution" breaks>{question.resolution}</Markdown>
      )}
    </section>
  );
}

function QueueRow({
  row,
  open,
  onToggle,
  onAnswer,
  onDismiss,
  onClear,
  onOpenSeed,
}: { row: Asking; open: boolean; onToggle: () => void } & AnswerHandlers) {
  const { seed, question } = row;
  if (question.status === 'withdrawn') {
    return (
      <li className="garden-queue__row is-withdrawn">
        <div className="garden-queue__line">
          <span className="garden-queue__seed">{seed.title}</span>
          <span className="garden-queue__age">{waitedWords(question.resolved_at ?? question.asked_at)}</span>
        </div>
        <p className="garden-queue__gone">
          {question.asked_by} withdrew this{question.resolution ? ` — ${question.resolution}` : ''}.
        </p>
        {onClear && (
          <button type="button" className="garden-ask__quiet" onClick={() => onClear(question.id)}>
            Clear
          </button>
        )}
      </li>
    );
  }
  return (
    <li className={`garden-queue__row${open ? ' is-open' : ''}`}>
      <button type="button" className="garden-queue__head" aria-expanded={open} onClick={onToggle}>
        <span className="garden-queue__line">
          <AskMark />
          <span className="garden-queue__seed">{seed.title}</span>
          <span className="garden-queue__who">{question.asked_by}</span>
          <span className="garden-queue__age">{waitedWords(question.asked_at)}</span>
        </span>
        <span className="garden-queue__ask">{question.text}</span>
      </button>
      {open && (
        <div className="garden-queue__body">
          <Markdown className="garden-ask__text" breaks>{question.text}</Markdown>
          <AnswerForm question={question} autoFocus onAnswer={onAnswer} onDismiss={onDismiss} />
          {onOpenSeed && (
            <button type="button" className="garden-queue__jump" onClick={() => onOpenSeed(seed.id)}>
              Open {seed.id} →
            </button>
          )}
        </div>
      )}
    </li>
  );
}

function useQueueOpen(rows: Asking[]) {
  const first = rows.find((row) => row.question.status === 'open')?.question.id ?? '';
  const [open, setOpen] = useState<string>(first);
  const toggle = (id: string) => setOpen((cur) => (cur === id ? '' : id));
  return [open || first, toggle] as const;
}

/** Placement A: a band at the top of the panel, above the listing. */
export function NeedsYouBanner({ rows, ...handlers }: { rows: Asking[] } & AnswerHandlers) {
  const [expanded, setExpanded] = useState(false);
  const [openId, toggle] = useQueueOpen(rows);
  const live = rows.filter((row) => row.question.status === 'open');
  if (rows.length === 0) return null;
  const longest = live[0];
  return (
    <div className={`garden-needs-banner${expanded ? ' is-expanded' : ''}`} data-testid="needs-you-banner">
      <div className="garden-needs-banner__bar">
        <AskMark />
        <div className="garden-needs-banner__words">
          <strong>
            {live.length === 0
              ? 'A question was withdrawn'
              : `${live.length} ${live.length === 1 ? 'question needs' : 'questions need'} you`}
          </strong>
          {longest && (
            <span>
              {longest.seed.title} — {longest.question.asked_by} has been waiting{' '}
              {waitedWords(longest.question.asked_at)}
            </span>
          )}
        </div>
        <button type="button" onClick={() => setExpanded((cur) => !cur)}>
          {expanded ? 'Hide' : live.length === 1 ? 'Answer it' : 'Answer them'}
        </button>
      </div>
      {expanded && (
        <ul className="garden-queue">
          {rows.map((row) => (
            <QueueRow
              key={row.question.id}
              row={row}
              open={openId === row.question.id}
              onToggle={() => toggle(row.question.id)}
              {...handlers}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

/** Placement B: a standing column beside the list or the board. */
export function NeedsYouRail({ rows, ...handlers }: { rows: Asking[] } & AnswerHandlers) {
  const [openId, toggle] = useQueueOpen(rows);
  const live = rows.filter((row) => row.question.status === 'open');
  return (
    <aside className="garden-needs-rail" aria-label="Questions waiting on you" data-testid="needs-you-rail">
      <header className="garden-needs-rail__head">
        <AskMark />
        <h2>Needs you</h2>
        <span className={`garden-needs-rail__count${live.length === 0 ? ' is-quiet' : ''}`}>
          {live.length}
        </span>
      </header>
      {rows.length === 0 ? (
        <div className="garden-needs-rail__empty">
          <p>Nothing is waiting on you.</p>
          <p>
            An agent that hits a call it should not make writes <code>attn seed ask</code> and it lands here.
          </p>
        </div>
      ) : (
        <ul className="garden-queue">
          {rows.map((row) => (
            <QueueRow
              key={row.question.id}
              row={row}
              open={openId === row.question.id}
              onToggle={() => toggle(row.question.id)}
              {...handlers}
            />
          ))}
        </ul>
      )}
    </aside>
  );
}

/** The marker a row, a card or a tile header wears while a question is open. */
export function NeedsYouChip() {
  return (
    <span className="garden-needs-chip" title="A question on this seed is waiting on you">
      <AskMark />
      needs you
    </span>
  );
}
