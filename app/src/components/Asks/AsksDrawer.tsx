import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import type { AskAnswer } from '../../types/generated';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { EMPTY_ASK_DRAFT, useAsksStore } from '../../store/asks';
import type { UISessionState } from '../../types/sessionState';
import { askFrom, askHolder, askQuestions, draftAnswers, draftSendable, orderAsks, type AskHolderSession } from '../../utils/asks';
import { formatTurnAge } from '../../utils/queueBands';
import { keyJoiner, modifierGlyphs } from '../../shortcuts/platform';
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
  const askRef = useRef<HTMLElement>(null);
  const now = Date.now();

  useEffect(() => {
    askRef.current?.querySelector<HTMLElement>('input, textarea')?.focus({ preventScroll: true });
  }, [ask?.id]);

  const step = (delta: number) => {
    const next = ordered[index + delta];
    if (next) {
      selectAsk(next.id);
      setError(null);
    }
  };

  if (!ask) {
    return (
      <div className="asks-drawer">
        <AsksHeader count={0} onClose={onClose} />
        <div className="asks-empty">No agent is waiting on an answer.</div>
      </div>
    );
  }

  const holder = askHolder(ask, sessions);
  const draft = drafts[ask.id] ?? EMPTY_ASK_DRAFT;
  const source = fileMarkdownSource(`${ask.base_dir}/${ask.id}.md`);
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
  let questionNumber = 0;

  return (
    <div className="asks-drawer" onKeyDown={onKeyDown}>
      <AsksHeader count={ordered.length} onClose={onClose} />
      <ol className="asks-list" aria-label="Open asks">
        {ordered.map((item) => {
          const itemHolder = askHolder(item, sessions);
          const questions = askQuestions(item).length;
          return (
            <li key={item.id}>
              <button
                type="button"
                className={`asks-row ${item.id === ask.id ? 'asks-row--selected' : ''}`.trim()}
                data-testid={`asks-row-${item.id}`}
                aria-current={item.id === ask.id ? 'true' : undefined}
                onClick={() => {
                  selectAsk(item.id);
                  setError(null);
                }}
              >
                <span className="asks-row-from">{askFrom(item, itemHolder)}</span>
                <span className="asks-row-subject">{item.subject}</span>
                {questions > 1 && <span className="asks-row-count">{questions}</span>}
                {itemHolder?.turnOwed && <span className="asks-row-waiting" title="Waiting on you">●</span>}
                <span className="asks-row-age">{formatTurnAge(item.created_at, now)}</span>
              </button>
            </li>
          );
        })}
      </ol>
      <article ref={askRef} className="asks-ask" aria-label={ask.subject} data-testid={`ask-${ask.id}`}>
        <div className="asks-ask-from">
          {holder ? <SessionLead agent={holder.agent} state={holder.state} reason={holder.state_reason} seed={holder.id} /> : null}
          <span className="asks-ask-agent">{askFrom(ask, holder)}</span>
          {holder && (
            <button type="button" className="asks-open-agent" onClick={() => onOpenAgent(holder.id)} title="Open the agent (o)">
              Open agent
            </button>
          )}
        </div>
        <h2 className="asks-ask-subject">{ask.subject}</h2>
        {ask.parts.map((part, partIndex) => {
          if (part.question) {
            questionNumber += 1;
            return (
              <AskQuestionCard
                key={part.question.key}
                askId={ask.id}
                index={questionNumber}
                question={part.question}
                answer={draft.answers[part.question.key]}
                source={source}
                disabled={sending}
                onChange={changeAnswer}
              />
            );
          }
          return (
            <div key={`markdown-${partIndex}`} className="asks-markdown">
              <MarkdownReader content={part.markdown ?? ''} source={source} />
            </div>
          );
        })}
        <textarea
          className="asks-note"
          aria-label="Note"
          rows={3}
          value={draft.note}
          disabled={sending}
          placeholder={`Anything else for ${askFrom(ask, holder)}?`}
          onChange={(event) => setDraft(ask.id, { ...draft, note: event.target.value })}
        />
        {error && <div className="asks-error" role="alert">{error}</div>}
      </article>
      <div className="asks-footer">
        <span className="asks-hint"><kbd>j</kbd><kbd>k</kbd> next · <kbd>o</kbd> open agent</span>
        <button type="button" className="asks-send" disabled={!sendable} onClick={() => void send()}>
          {sending ? 'Sending…' : 'Send'} <kbd>{`${modifierGlyphs().accel}${keyJoiner()}⏎`}</kbd>
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
      <button type="button" className="asks-close" aria-label="Close asks" onClick={onClose}>×</button>
    </div>
  );
}
