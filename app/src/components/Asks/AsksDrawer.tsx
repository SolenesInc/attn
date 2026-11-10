import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type RefObject } from 'react';
import type { Ask, AskAnswer, AskReply, AskSeedMention } from '../../types/generated';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { TURN_AGE_TICK_MS, useNow } from '../../hooks/useNow';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import { EMPTY_ASK_DRAFT, useAskLists, useAsksStore, useRepliedAskList } from '../../store/asks';
import type { UISessionState } from '../../types/sessionState';
import { askQuestions, draftAnswers, draftSendable, recommendedAnswers, type AskHolderSession, type AskListItem } from '../../utils/asks';
import { formatTurnAge } from '../../utils/queueBands';
import { isSnoozed } from '../../utils/snoozeDurations';
import { fileMarkdownSource } from '../MarkdownReader/documentSource';
import { MarkdownReader } from '../MarkdownReader';
import { SeedMentionText } from '../SeedMentionText';
import { useDaemonStore } from '../../store/daemonSessions';
import { createSeedMentionIndex } from '../../utils/seedMentions';
import { SessionLead } from '../SessionLead';
import { AskQuestionCard } from './AskQuestionCard';
import { AskRowSummary } from './AskRowSummary';
import './Asks.css';
import { isDomInstance } from '../../utils/realm';

export interface AskDrawerSession extends AskHolderSession {
  label: string;
  agent?: string;
  state: UISessionState;
  state_reason?: string;
  offline?: boolean;
}

interface AsksDrawerProps {
  sessions: readonly AskDrawerSession[];
  onTop: boolean;
  onOpenAgent: (sessionId: string) => void;
  onSnoozeAgent: (session: AskDrawerSession, control: HTMLElement) => void;
  onWakeAgent: (sessionId: string) => void;
  onOpenSeed: (seedId: string) => void;
  onClose: () => void;
}

const SEND_SHORTCUT = { key: 'Enter', meta: true };

function takeKeyboardFromTerminal(input: HTMLElement) {
  document.getSelection()?.removeAllRanges();
  input.focus({ preventScroll: true });
}

function isTyping(target: EventTarget): boolean {
  if (isDomInstance(target, 'HTMLInputElement')) return !['radio', 'checkbox', 'button'].includes(target.type);
  return isDomInstance(target, 'HTMLElement') && (target.isContentEditable || ['TEXTAREA', 'SELECT'].includes(target.tagName));
}

function drawerKey(event: KeyboardEvent): string | null {
  return isTyping(event.target) || event.metaKey || event.ctrlKey || event.altKey ? null : event.key;
}

export function AsksDrawer({ sessions, onTop, onOpenAgent, onSnoozeAgent, onWakeAgent, onOpenSeed, onClose }: AsksDrawerProps) {
  const { live, snoozed } = useAskLists(sessions);
  const open = useMemo(() => [...live, ...snoozed], [live, snoozed]);
  const replied = useRepliedAskList(sessions);
  const showReplied = useAsksStore((state) => state.showReplied);
  const items = useMemo(() => (showReplied ? [...open, ...replied] : open), [showReplied, open, replied]);
  const drafts = useAsksStore((state) => state.drafts);
  const selectedAskId = useAsksStore((state) => state.selectedAskId);
  const focusRequest = useAsksStore((state) => state.focusRequest);
  const { selectAsk, setDraft, toggleReplied } = useAsksStore.getState();
  const { sendAskAnswer, sendAskImage, sendAskSeedMentions } = useDaemonApi();
  const gardenSeeds = useDaemonStore((state) => state.seeds);
  const [resolved, setResolved] = useState<{ askId: string; seeds: typeof gardenSeeds; mentions: AskSeedMention[] } | null>(null);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const index = Math.max(0, items.findIndex((item) => item.ask.id === selectedAskId));
  const ask = items[index]?.ask;
  const askId = ask?.id;
  const askReplied = Boolean(ask?.reply);
  const seedMentions = useMemo(() => createSeedMentionIndex(
    resolved && resolved.askId === askId && resolved.seeds === gardenSeeds ? resolved.mentions : [],
  ), [resolved, askId, gardenSeeds]);
  useEffect(() => {
    let current = true;
    if (askId) void sendAskSeedMentions(askId).then((result) => {
      if (current) setResolved({ askId, seeds: gardenSeeds, mentions: result });
    }).catch((err: unknown) => {
      if (current) setError(err instanceof Error ? err.message : String(err));
    });
    return () => { current = false; };
  }, [askId, gardenSeeds, sendAskSeedMentions]);
  const drawerRef = useRef<HTMLDivElement>(null);
  const askRef = useRef<HTMLElement>(null);
  const snoozeRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLOListElement>(null);
  const sourcePath = ask ? `${ask.base_dir}/${ask.id}.md` : '';
  const source = useMemo(() => fileMarkdownSource(sourcePath), [sourcePath]);
  const loadImage = useCallback(
    (path: string) => sendAskImage(askId ?? '', path).then((image) => `data:${image.mime_type};base64,${image.data_base64}`),
    [askId, sendAskImage],
  );

  useEffect(() => () => selectAsk(null), [selectAsk]);
  useEffect(() => {
    if (!onTop) return;
    const frame = window.requestAnimationFrame(() => {
      const focused = document.activeElement;
      if (focused === document.body || (isDomInstance(focused, 'HTMLElement') && focused.closest('[inert]'))) drawerRef.current?.focus();
    });
    return () => window.cancelAnimationFrame(frame);
  }, [onTop]);
  useEffect(() => {
    if (!askId && onTop && drawerRef.current) takeKeyboardFromTerminal(drawerRef.current);
  }, [askId, onTop, focusRequest]);
  useEffect(() => {
    if (!askId) return;
    selectAsk(askId);
    listRef.current?.querySelector<HTMLElement>('[aria-current="true"]')?.scrollIntoView?.({ block: 'nearest' });
    const article = askRef.current;
    if (!article) return;
    article.scrollTop = 0;
    const input = article.querySelector<HTMLElement>('input:not(:disabled), textarea:not(:disabled)') ?? drawerRef.current;
    if (input) takeKeyboardFromTerminal(input);
  }, [askId, askReplied, focusRequest, selectAsk]);

  const show = (id: string) => {
    selectAsk(id);
    setError(null);
  };
  const step = (delta: number) => {
    const next = items[index + delta];
    if (next) show(next.ask.id);
  };

  const header = <AsksHeader count={live.length} replied={replied.length} showReplied={showReplied} onToggleReplied={toggleReplied} onClose={onClose} />;
  const onEmptyKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (drawerKey(event) !== 'r') return;
    event.preventDefault();
    toggleReplied();
  };

  if (!ask) {
    return (
      <div ref={drawerRef} className="asks-drawer" role="region" aria-label="Asks" tabIndex={-1} onKeyDown={onEmptyKeyDown}>
        {header}
        <AsksEmpty replied={replied.length} />
      </div>
    );
  }

  const { holder, from, snoozedUntil } = items[index];
  const snoozable = holder && !holder.offline ? holder : undefined;
  const holderSnoozed = isSnoozed(snoozable?.turnSnoozedUntil, Date.now());
  const toggleSnooze = () => {
    if (!snoozable) return;
    if (holderSnoozed) onWakeAgent(snoozable.id);
    else if (snoozeRef.current) onSnoozeAgent(snoozable, snoozeRef.current);
  };
  const seedId = ask.seed_id;
  const reply = ask.reply;
  const questions = askQuestions(ask);
  const draft = drafts[ask.id] ?? EMPTY_ASK_DRAFT;
  const shownAnswers = reply ? answersByKey(reply.answers) : draft.answers;
  const sendable = !reply && draftSendable(ask, draft) && !sending;
  const changeAnswer = (answer: AskAnswer) => setDraft(ask.id, { ...draft, answers: { ...draft.answers, [answer.key]: answer } });
  const recommended = reply ? [] : recommendedAnswers(ask);
  const canAcceptRecommended = recommended.length > 1 && !sending;
  const acceptRecommended = () => {
    if (!canAcceptRecommended) return;
    setDraft(ask.id, { ...draft, answers: { ...draft.answers, ...answersByKey(recommended) } });
  };
  const send = async () => {
    if (!sendable) return;
    const section = snoozedUntil ? snoozed : live;
    const position = section.findIndex((item) => item.ask.id === ask.id);
    const after = section[position + 1] ?? section[position - 1];
    setSending(true);
    setError(null);
    try {
      await sendAskAnswer(ask.id, draftAnswers(ask, draft), draft.note);
      selectAsk(after?.ask.id ?? ask.id);
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
    const key = drawerKey(event);
    if (key === 'j') step(1);
    else if (key === 'k') step(-1);
    else if (key === 'r') toggleReplied();
    else if (key === 'a' && canAcceptRecommended) acceptRecommended();
    else if (key === 'o' && holder) onOpenAgent(holder.id);
    else if (key === 's' && snoozable) toggleSnooze();
    else if (key === 'g' && seedId) onOpenSeed(seedId);
    else return;
    event.preventDefault();
  };

  return (
    <div ref={drawerRef} className="asks-drawer" role="region" aria-label="Asks" tabIndex={-1} onKeyDown={onKeyDown}>
      {header}
      <AsksList items={items} selectedId={ask.id} listRef={listRef} onShow={show} />
      <article ref={askRef} className="asks-ask" aria-label={ask.subject} data-testid={`ask-${ask.id}`}>
        <div className="asks-ask-from">
          {holder && <SessionLead agent={holder.agent} state={holder.state} reason={holder.state_reason} seed={holder.id} />}
          <span className="asks-ask-agent">{from}</span>
          {holder && (
            <button type="button" className="asks-ask-open" onClick={() => onOpenAgent(holder.id)} title={`Open ${from} (o)`}>
              Open agent
            </button>
          )}
          {snoozable && (
            <button
              ref={snoozeRef}
              type="button"
              className="asks-ask-open"
              onClick={toggleSnooze}
              title={holderSnoozed ? `Wake ${from} (s)` : `Snooze ${from} and its asks (s)`}
            >
              {holderSnoozed ? 'Wake' : 'Snooze'}
            </button>
          )}
        </div>
        <AskSeedLine ask={ask} onOpenSeed={onOpenSeed} />
        <h2 className="asks-ask-subject"><SeedMentionText text={ask.subject} index={seedMentions} onOpenSeed={onOpenSeed} /></h2>
        {ask.parts.map((part, partIndex) => (
          part.question ? (
            <AskQuestionCard
              key={part.question.key}
              askId={ask.id}
              index={questions.indexOf(part.question) + 1}
              question={part.question}
              answer={shownAnswers[part.question.key]}
              source={source}
              loadImage={loadImage}
              seedMentions={seedMentions}
              onOpenSeed={onOpenSeed}
              onChange={reply ? undefined : changeAnswer}
            />
          ) : (
            <div key={`markdown-${partIndex}`} className="asks-markdown">
              <MarkdownReader content={part.markdown ?? ''} source={source} loadLocalImage={loadImage} seedMentions={seedMentions} onOpenSeed={onOpenSeed} />
            </div>
          )
        ))}
        <AskNote
          reply={reply}
          value={draft.note}
          placeholder={holder || !seedId ? `Anything else for ${from}?` : 'Anything else for its next tender?'}
          onChange={(note) => setDraft(ask.id, { ...draft, note })}
        />
        {error && <div className="asks-error" role="alert">{error}</div>}
      </article>
      <div className="asks-footer">
        <AsksHints several={items.length > 1} hasHolder={Boolean(holder)} snooze={snoozable ? (holderSnoozed ? 'wake' : 'snooze') : undefined} hasSeed={Boolean(seedId)} />
        {canAcceptRecommended && (
          <button type="button" className="asks-accept-recommended" title="Select every recommended answer (a)" onClick={acceptRecommended}>
            Accept all recommended <kbd>a</kbd>
          </button>
        )}
        <AskAction reply={reply} sendable={sendable} sending={sending} onSend={() => void send()} />
      </div>
    </div>
  );
}

interface AsksListProps {
  items: readonly AskListItem<AskDrawerSession>[];
  selectedId: string;
  listRef: RefObject<HTMLOListElement | null>;
  onShow: (askId: string) => void;
}

function AsksList({ items, selectedId, listRef, onShow }: AsksListProps) {
  const now = useNow(TURN_AGE_TICK_MS);
  return (
    <ol ref={listRef} className="asks-list" aria-label={items.some((item) => item.ask.reply) ? 'Open and replied asks' : 'Open asks'}>
      {items.map((item, position) => {
        const selected = item.ask.id === selectedId;
        const firstReply = item.ask.reply && !items[position - 1]?.ask.reply;
        const firstSnoozed = item.snoozedUntil && !items[position - 1]?.snoozedUntil;
        const dimmed = item.ask.reply || item.snoozedUntil;
        return (
          <li key={item.ask.id}>
            {firstSnoozed && <div className="asks-list-divider">Snoozed</div>}
            {firstReply && <div className="asks-list-divider">Replied in the last day</div>}
            <button
              type="button"
              className={`asks-row ${selected ? 'asks-row--selected' : ''} ${dimmed ? 'asks-row--dimmed' : ''}`.trim()}
              data-testid={`asks-row-${item.ask.id}`}
              aria-current={selected ? 'true' : undefined}
              onClick={() => onShow(item.ask.id)}
            >
              <AskRowSummary item={item} now={now} />
            </button>
          </li>
        );
      })}
    </ol>
  );
}

function AskSeedLine({ ask, onOpenSeed }: { ask: Ask; onOpenSeed: (seedId: string) => void }) {
  const seedId = ask.seed_id;
  if (!seedId) return null;
  return (
    <div className="asks-ask-seed">
      <span className="asks-ask-seed-slug">{ask.seed_slug ?? seedId}</span>
      <span className="asks-ask-seed-title">{ask.seed_title}</span>
      <button type="button" className="asks-ask-open" onClick={() => onOpenSeed(seedId)} title={`Open ${ask.seed_title} in the Garden (g)`}>
        Open in Garden
      </button>
    </div>
  );
}

interface AsksHintsProps {
  several: boolean;
  hasHolder: boolean;
  snooze: 'snooze' | 'wake' | undefined;
  hasSeed: boolean;
}

function AsksHints({ several, hasHolder, snooze, hasSeed }: AsksHintsProps) {
  return (
    <span className="asks-hints">
      {several && <span className="asks-hint"><kbd>j</kbd><kbd>k</kbd> next · previous</span>}
      {hasHolder && <span className="asks-hint"><kbd>o</kbd> open agent</span>}
      {snooze && <span className="asks-hint"><kbd>s</kbd> {snooze}</span>}
      {hasSeed && <span className="asks-hint"><kbd>g</kbd> open in Garden</span>}
      <span className="asks-hint"><kbd>r</kbd> replied</span>
      <span className="asks-hint"><kbd>esc</kbd> close</span>
    </span>
  );
}

function answersByKey(answers: readonly AskAnswer[]): Record<string, AskAnswer> {
  return Object.fromEntries(answers.map((answer) => [answer.key, answer]));
}

function AsksEmpty({ replied }: { replied: number }) {
  return (
    <div className="asks-empty">
      <p>No open asks.</p>
      <p className="asks-empty-hint">When an agent needs a decision from you, its ask lands here.</p>
      {replied > 0 && (
        <p className="asks-empty-hint"><kbd>r</kbd> shows the {replied === 1 ? 'one' : replied} you replied to in the last day</p>
      )}
    </div>
  );
}

interface AskNoteProps {
  reply: AskReply | undefined;
  value: string;
  placeholder: string;
  onChange: (note: string) => void;
}

function AskNote({ reply, value, placeholder, onChange }: AskNoteProps) {
  if (reply) return reply.note ? <p className="ask-replied-text" aria-label="Note">{reply.note}</p> : null;
  return (
    <textarea
      className="asks-note"
      aria-label="Note"
      rows={3}
      value={value}
      placeholder={placeholder}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

interface AskActionProps {
  reply: AskReply | undefined;
  sendable: boolean;
  sending: boolean;
  onSend: () => void;
}

function AskAction({ reply, sendable, sending, onSend }: AskActionProps) {
  if (reply) return <RepliedWhen answeredAt={reply.answered_at} />;
  return (
    <button type="button" className="asks-send" disabled={!sendable} aria-busy={sending || undefined} onClick={onSend}>
      {sending ? 'Sending…' : 'Send'} <kbd>{formatShortcut(SEND_SHORTCUT)}</kbd>
    </button>
  );
}

function RepliedWhen({ answeredAt }: { answeredAt: string }) {
  const now = useNow(TURN_AGE_TICK_MS);
  const age = formatTurnAge(answeredAt, now);
  return <span className="asks-replied-when">{age === 'now' ? 'Replied just now' : `Replied ${age} ago`}</span>;
}

interface AsksHeaderProps {
  count: number;
  replied: number;
  showReplied: boolean;
  onToggleReplied: () => void;
  onClose: () => void;
}

function AsksHeader({ count, replied, showReplied, onToggleReplied, onClose }: AsksHeaderProps) {
  return (
    <div className="asks-header">
      <span className="asks-title">Asks</span>
      {count > 0 && <span className="asks-count">{count}</span>}
      <button
        type="button"
        className="asks-replied-toggle"
        aria-pressed={showReplied}
        title={`${showReplied ? 'Hide' : 'Show'} the asks you replied to in the last day (r)`}
        onClick={onToggleReplied}
      >
        Replied{replied > 0 && <span className="asks-replied-count">{replied}</span>}
      </button>
      <button type="button" className="asks-close" aria-label="Close asks" title="Close (esc)" onClick={onClose}>×</button>
    </div>
  );
}
