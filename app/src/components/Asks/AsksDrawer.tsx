import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import type { AskAnswer } from '../../types/generated';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { TURN_AGE_TICK_MS, useNow } from '../../hooks/useNow';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import { EMPTY_ASK_DRAFT, useAsksStore } from '../../store/asks';
import type { UISessionState } from '../../types/sessionState';
import { askFrom, askHolder, askQuestions, draftAnswers, draftSendable, orderAsks, type AskHolderSession } from '../../utils/asks';
import { formatTurnAge } from '../../utils/queueBands';
import { fileMarkdownSource } from '../MarkdownReader/documentSource';
import { MarkdownReader } from '../MarkdownReader';
import { SessionLead } from '../SessionLead';
import { AskQuestionCard } from './AskQuestionCard';
import './Asks.css';

export interface AskDrawerSession extends AskHolderSession {
  label: string;
  agent?: string;
  state: UISessionState;
  state_reason?: string;
}

interface AsksDrawerProps {
  sessions: readonly AskDrawerSession[];
  onOpenAgent: (sessionId: string) => void;
  onClose: () => void;
}

const SEND_SHORTCUT = { key: 'Enter', meta: true };

function isTyping(target: EventTarget): boolean {
  if (target instanceof HTMLInputElement) return !['radio', 'checkbox', 'button'].includes(target.type);
  return target instanceof HTMLElement && (target.isContentEditable || ['TEXTAREA', 'SELECT'].includes(target.tagName));
}

export function AsksDrawer({ sessions, onOpenAgent, onClose }: AsksDrawerProps) {
  const asks = useAsksStore((state) => state.asks);
  const drafts = useAsksStore((state) => state.drafts);
  const selectedAskId = useAsksStore((state) => state.selectedAskId);
  const { selectAsk, setDraft } = useAsksStore.getState();
  const { sendAskAnswer } = useDaemonApi();
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ordered = useMemo(() => orderAsks(asks, sessions), [asks, sessions]);
  const index = Math.max(0, ordered.findIndex((ask) => ask.id === selectedAskId));
  const ask = ordered[index];
  const askId = ask?.id;
  const askRef = useRef<HTMLElement>(null);
  const listRef = useRef<HTMLOListElement>(null);
  const now = useNow(TURN_AGE_TICK_MS);
  const sourcePath = ask ? `${ask.base_dir}/${ask.id}.md` : '';
  const source = useMemo(() => fileMarkdownSource(sourcePath), [sourcePath]);

  useEffect(() => () => selectAsk(null), [selectAsk]);
  useEffect(() => {
    if (!askId) return;
    selectAsk(askId);
    listRef.current?.querySelector<HTMLElement>('[aria-current="true"]')?.scrollIntoView?.({ block: 'nearest' });
    const article = askRef.current;
    if (!article) return;
    article.scrollTop = 0;
    article.querySelector<HTMLElement>('input:not(:disabled), textarea:not(:disabled)')?.focus({ preventScroll: true });
  }, [askId, selectAsk]);

  const show = (id: string) => {
    selectAsk(id);
    setError(null);
  };
  const step = (delta: number) => {
    const next = ordered[index + delta];
    if (next) show(next.id);
  };

  if (!ask) {
    return (
      <div className="asks-drawer">
        <AsksHeader count={0} onClose={onClose} />
        <div className="asks-empty">
          <p>No open asks.</p>
          <p className="asks-empty-hint">When an agent needs a decision from you, its ask lands here.</p>
        </div>
      </div>
    );
  }

  const holder = askHolder(ask, sessions);
  const from = askFrom(ask, holder);
  const questions = askQuestions(ask);
  const draft = drafts[ask.id] ?? EMPTY_ASK_DRAFT;
  const sendable = draftSendable(ask, draft) && !sending;
  const changeAnswer = (answer: AskAnswer) => setDraft(ask.id, { ...draft, answers: { ...draft.answers, [answer.key]: answer } });
  const send = async () => {
    if (!sendable) return;
    const after = ordered[index + 1] ?? ordered[index - 1];
    setSending(true);
    setError(null);
    try {
      await sendAskAnswer(ask.id, draftAnswers(ask, draft), draft.note);
      selectAsk(after?.id ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSending(false);
    }
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void send();
      return;
    }
    if (isTyping(event.target) || event.metaKey || event.ctrlKey || event.altKey) return;
    if (event.key === 'j') step(1);
    else if (event.key === 'k') step(-1);
    else if (event.key === 'o' && holder) onOpenAgent(holder.id);
    else return;
    event.preventDefault();
  };

  return (
    <div className="asks-drawer" role="region" aria-label="Asks" tabIndex={-1} onKeyDown={onKeyDown}>
      <AsksHeader count={ordered.length} onClose={onClose} />
      <ol ref={listRef} className="asks-list" aria-label="Open asks">
        {ordered.map((item) => {
          const itemHolder = askHolder(item, sessions);
          const questionCount = askQuestions(item).length;
          const selected = item.id === ask.id;
          return (
            <li key={item.id}>
              <button
                type="button"
                className={`asks-row ${selected ? 'asks-row--selected' : ''}`.trim()}
                data-testid={`asks-row-${item.id}`}
                aria-current={selected ? 'true' : undefined}
                onClick={() => show(item.id)}
              >
                <span className="asks-row-from">{askFrom(item, itemHolder)}</span>
                <span className="asks-row-subject">{item.subject}</span>
                {questionCount > 1 && <span className="asks-row-count">{questionCount} questions</span>}
                {itemHolder?.turnOwed && <span className="asks-row-waiting" role="img" aria-label="Waiting on you" title="Waiting on you" />}
                <span className="asks-row-age">{formatTurnAge(item.created_at, now)}</span>
              </button>
            </li>
          );
        })}
      </ol>
      <article ref={askRef} className="asks-ask" aria-label={ask.subject} data-testid={`ask-${ask.id}`}>
        <div className="asks-ask-from">
          {holder && <SessionLead agent={holder.agent} state={holder.state} reason={holder.state_reason} seed={holder.id} />}
          <span className="asks-ask-agent">{from}</span>
          {holder && (
            <button type="button" className="asks-open-agent" onClick={() => onOpenAgent(holder.id)} title={`Open ${from} (o)`}>
              Open agent
            </button>
          )}
        </div>
        <h2 className="asks-ask-subject">{ask.subject}</h2>
        {ask.parts.map((part, partIndex) => (
          part.question ? (
            <AskQuestionCard
              key={part.question.key}
              askId={ask.id}
              index={questions.indexOf(part.question) + 1}
              question={part.question}
              answer={draft.answers[part.question.key]}
              source={source}
              onChange={changeAnswer}
            />
          ) : (
            <div key={`markdown-${partIndex}`} className="asks-markdown">
              <MarkdownReader content={part.markdown ?? ''} source={source} />
            </div>
          )
        ))}
        <textarea
          className="asks-note"
          aria-label="Note"
          rows={3}
          value={draft.note}
          placeholder={`Anything else for ${from}?`}
          onChange={(event) => setDraft(ask.id, { ...draft, note: event.target.value })}
        />
        {error && <div className="asks-error" role="alert">{error}</div>}
      </article>
      <div className="asks-footer">
        <span className="asks-hints">
          {ordered.length > 1 && <span className="asks-hint"><kbd>j</kbd><kbd>k</kbd> next · previous</span>}
          {holder && <span className="asks-hint"><kbd>o</kbd> open agent</span>}
          <span className="asks-hint"><kbd>esc</kbd> close</span>
        </span>
        <button type="button" className="asks-send" disabled={!sendable} aria-busy={sending || undefined} onClick={() => void send()}>
          {sending ? 'Sending…' : 'Send'} <kbd>{formatShortcut(SEND_SHORTCUT)}</kbd>
        </button>
      </div>
    </div>
  );
}

function AsksHeader({ count, onClose }: { count: number; onClose: () => void }) {
  return (
    <div className="asks-header">
      <span className="asks-title">Asks</span>
      {count > 0 && <span className="asks-count">{count}</span>}
      <button type="button" className="asks-close" aria-label="Close asks" title="Close (esc)" onClick={onClose}>×</button>
    </div>
  );
}
