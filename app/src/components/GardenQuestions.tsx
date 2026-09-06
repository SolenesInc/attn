import { useState } from 'react';
import type { Seed } from '../hooks/useDaemonSocket';
import { Markdown } from './Markdown';
import { asking, questionAuthor, questionOf, waitedWords, type Asking } from './seedQuestions';
import './GardenQuestions.css';

export interface QuestionActionHandlers {
  onAnswer: (seedId: string, answer: string) => Promise<unknown> | void;
  onDismiss: (seedId: string, reason: string) => Promise<unknown> | void;
  onClear?: (seedId: string) => Promise<unknown> | void;
  onOpenSeed?: (seedId: string) => void;
}

function WaitingMark() {
  return <span className="garden-waiting-mark">waiting on you</span>;
}

function errorWords(error: unknown): string {
  return error instanceof Error ? error.message : 'The question could not be updated.';
}

function AnswerForm({
  seedId,
  autoFocus,
  onAnswer,
  onDismiss,
}: {
  seedId: string;
  autoFocus?: boolean;
} & Pick<QuestionActionHandlers, 'onAnswer' | 'onDismiss'>) {
  const [text, setText] = useState('');
  const [mode, setMode] = useState<'answer' | 'dismiss'>('answer');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const submit = async () => {
    const message = text.trim();
    if (!message || busy) return;
    setBusy(true);
    setError('');
    try {
      if (mode === 'answer') await onAnswer(seedId, message);
      else await onDismiss(seedId, message);
      setText('');
    } catch (actionError) {
      setError(errorWords(actionError));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      className="garden-question__form"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      {mode === 'dismiss' && (
        <p className="garden-question__dismiss-copy">Tell the agent why this is the wrong decision to hand you.</p>
      )}
      <textarea
        className="garden-question__input"
        data-testid={`garden-question-${mode}-input-${seedId}`}
        aria-label={mode === 'answer' ? 'Your answer' : 'Reason for dismissing'}
        placeholder={mode === 'answer' ? 'Your call…' : 'Why is this the wrong ask?'}
        autoFocus={autoFocus}
        value={text}
        disabled={busy}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            void submit();
          }
        }}
      />
      {error && <p className="garden-question__error" role="alert">{error}</p>}
      <div className="garden-question__actions">
        {mode === 'answer' ? (
          <button
            type="button"
            className="garden-question__quiet"
            data-testid={`garden-question-dismiss-mode-${seedId}`}
            onClick={() => { setMode('dismiss'); setText(''); setError(''); }}
          >
            Dismiss…
          </button>
        ) : (
          <button type="button" className="garden-question__quiet" onClick={() => { setMode('answer'); setText(''); setError(''); }}>
            Back to answer
          </button>
        )}
        <button
          type="submit"
          className="garden-question__submit"
          data-testid={`garden-question-${mode}-${seedId}`}
          disabled={!text.trim() || busy}
        >
          {busy ? 'Saving…' : mode === 'answer' ? 'Answer' : 'Dismiss'}
          {!busy && <kbd>⌘↵</kbd>}
        </button>
      </div>
    </form>
  );
}

function ClearButton({ seedId, onClear }: { seedId: string; onClear?: QuestionActionHandlers['onClear'] }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  if (!onClear) return null;
  return (
    <>
      <button
        type="button"
        className="garden-question__quiet"
        data-testid={`garden-question-clear-${seedId}`}
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError('');
          try {
            await onClear(seedId);
          } catch (clearError) {
            setError(errorWords(clearError));
            setBusy(false);
          }
        }}
      >
        {busy ? 'Clearing…' : 'Clear'}
      </button>
      {error && <p className="garden-question__error" role="alert">{error}</p>}
    </>
  );
}

export function SeedQuestionCard({
  seed,
  compact,
  onAnswer,
  onDismiss,
  onClear,
}: {
  seed: Seed;
  compact?: boolean;
} & Partial<Pick<QuestionActionHandlers, 'onAnswer' | 'onDismiss' | 'onClear'>>) {
  const question = questionOf(seed);
  if (!question) return null;
  if (question.status === 'withdrawn') {
    return (
      <section className={`garden-question is-withdrawn${compact ? ' is-compact' : ''}`} aria-label="Withdrawn question">
        <div className="garden-question__head">
          <strong>Question withdrawn</strong>
          <span>{questionAuthor(question)} withdrew this</span>
        </div>
        <Markdown className="garden-question__text is-quoted" breaks>{question.text}</Markdown>
        <ClearButton seedId={seed.id} onClear={onClear} />
      </section>
    );
  }
  if (question.status !== 'open') return null;
  return (
    <section className={`garden-question${compact ? ' is-compact' : ''}`} aria-label="Question waiting on you">
      <div className="garden-question__head">
        <WaitingMark />
        <span>{questionAuthor(question)} asked · waiting {waitedWords(question.asked_at)}</span>
      </div>
      <Markdown className="garden-question__text" breaks>{question.text}</Markdown>
      {onAnswer && onDismiss && (
        <AnswerForm seedId={seed.id} onAnswer={onAnswer} onDismiss={onDismiss} />
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
}: {
  row: Asking;
  open: boolean;
  onToggle: () => void;
} & QuestionActionHandlers) {
  const { seed, question } = row;
  if (question.status === 'withdrawn') {
    return (
      <li className="garden-question-queue__row is-withdrawn" data-seed-id={seed.id}>
        <div className="garden-question-queue__line">
          <span className="garden-question-queue__seed">{seed.title}</span>
          <span className="garden-question-queue__age">withdrawn</span>
        </div>
        <p>{questionAuthor(question)} withdrew this question.</p>
        <Markdown className="garden-question__text is-quoted" breaks>{question.text}</Markdown>
        <ClearButton seedId={seed.id} onClear={onClear} />
      </li>
    );
  }
  return (
    <li className={`garden-question-queue__row${open ? ' is-open' : ''}`} data-seed-id={seed.id}>
      <button
        type="button"
        className="garden-question-queue__head"
        data-testid={`garden-question-open-${seed.id}`}
        aria-expanded={open}
        onClick={onToggle}
      >
        <span className="garden-question-queue__line">
          <WaitingMark />
          <span className="garden-question-queue__seed">{seed.title}</span>
          <span className="garden-question-queue__who">{questionAuthor(question)}</span>
          <span className="garden-question-queue__age">{waitedWords(question.asked_at)}</span>
        </span>
        <span className="garden-question-queue__ask">{question.text}</span>
      </button>
      {open && (
        <div className="garden-question-queue__body">
          <Markdown className="garden-question__text" breaks>{question.text}</Markdown>
          <AnswerForm seedId={seed.id} autoFocus onAnswer={onAnswer} onDismiss={onDismiss} />
          {onOpenSeed && (
            <button type="button" className="garden-question-queue__jump" onClick={() => onOpenSeed(seed.id)}>
              Open {seed.id} →
            </button>
          )}
        </div>
      )}
    </li>
  );
}

function firstOpenQuestion(rows: Asking[]): string {
  return rows.find((row) => row.question.status === 'open')?.seed.id ?? '';
}

export function NeedsHumanBand({
  seeds,
  canOpenSeed,
  ...handlers
}: { seeds: Seed[]; canOpenSeed?: (seedId: string) => boolean } & QuestionActionHandlers) {
  const rows = asking(seeds);
  const [expanded, setExpanded] = useState(false);
  const [openedSeedId, setOpenedSeedId] = useState('');
  if (rows.length === 0) return null;
  const live = rows.filter((row) => row.question.status === 'open');
  const longest = live[0];
  const openId = live.some((row) => row.seed.id === openedSeedId)
    ? openedSeedId
    : firstOpenQuestion(rows);
  return (
    <div
      className={`garden-needs-human${expanded ? ' is-expanded' : ''}`}
      data-testid="garden-needs-human"
      data-live-count={live.length}
    >
      <div className="garden-needs-human__bar">
        <WaitingMark />
        <div className="garden-needs-human__words">
          <strong>
            {live.length === 0
              ? 'A question was withdrawn'
              : `${live.length} ${live.length === 1 ? 'question needs' : 'questions need'} you`}
          </strong>
          {longest && (
            <span>{longest.seed.title} · {questionAuthor(longest.question)} has waited {waitedWords(longest.question.asked_at)}</span>
          )}
        </div>
        <button type="button" data-testid="garden-needs-human-toggle" onClick={() => setExpanded((current) => !current)}>
          {expanded ? 'Hide' : live.length === 0 ? 'Show' : live.length === 1 ? 'Answer it' : 'Answer them'}
        </button>
      </div>
      {expanded && (
        <ul className="garden-question-queue">
          {rows.map((row) => (
            <QueueRow
              key={row.seed.id}
              row={row}
              open={openId === row.seed.id}
              onToggle={() => setOpenedSeedId((current) => current === row.seed.id ? '' : row.seed.id)}
              {...handlers}
              onOpenSeed={!canOpenSeed || canOpenSeed(row.seed.id) ? handlers.onOpenSeed : undefined}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

export function WaitingOnYouMark() {
  return <WaitingMark />;
}
