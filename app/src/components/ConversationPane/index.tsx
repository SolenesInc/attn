import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  useConversationsStore,
  selectConversation,
  conversationItemKey,
  type AgentPromptMode,
  type ConversationItem,
} from '../../store/conversations';
import { useSessionStore } from '../../store/sessions';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import type { ResolvedTheme } from '../../hooks/useTheme';
import type { UISessionState } from '../../types/sessionState';
import { ToolCard } from './ToolCard';
import { Markdown, ReaderPresentation } from '../Markdown';
import { MarkdownBoundary } from '../Markdown/MarkdownBoundary';
import './ConversationPane.css';

interface ConversationPaneProps {
  sessionId: string;
  paneActive: boolean;
  sessionState?: UISessionState;
  resolvedTheme?: ResolvedTheme;
}

function isAtBottom(list: HTMLElement): boolean {
  return list.scrollHeight - list.scrollTop - list.clientHeight < 80;
}

function itemLength(item: ConversationItem): number {
  if (item.kind === 'message') return item.text.length;
  if (item.kind === 'tool') return item.summary.length;
  return item.text.length;
}

export function ConversationPane({ sessionId, paneActive, sessionState, resolvedTheme }: ConversationPaneProps) {
  const conversation = useConversationsStore(selectConversation(sessionId));
  const promptSent = useConversationsStore((state) => state.promptSent);
  const historyRequested = useConversationsStore((state) => state.historyRequested);
  const reloadSession = useSessionStore((state) => state.reloadSession);
  const { sendAgentPrompt, sendAgentClearQueue, sendAgentAttach, sendAgentHistory, sendAgentSetModel } = useDaemonApi();
  const [draft, setDraft] = useState('');
  const [reloading, setReloading] = useState(false);
  const [reloadError, setReloadError] = useState<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);
  const attachedRef = useRef<string | null>(null);

  const { running, awaitingRun, ready, items, queue, lastSeq, hasMoreBefore, loadingHistory, droppedBefore, model, models } = conversation;
  const recoverable = sessionState === 'recoverable';

  // Only a session the daemon says is up may be asked: a launching one volunteers
  // its own snapshot, a recoverable one has no host, and asking either is a socket error.
  const hostShouldAnswer = sessionState !== undefined
    && sessionState !== 'launching'
    && sessionState !== 'recoverable'
    && sessionState !== 'unknown';
  useEffect(() => {
    if (!hostShouldAnswer || lastSeq > 0) return;
    if (attachedRef.current === sessionId) return;
    attachedRef.current = sessionId;
    sendAgentAttach(sessionId);
  }, [hostShouldAnswer, lastSeq, sendAgentAttach, sessionId]);
  const canSend = ready && !awaitingRun;
  const pending = [...queue.steering, ...queue.followUp];

  // Decided by the reader's own scrolling, never by measuring after a delta landed:
  // an opening code fence measured 133px in one paint against a tolerance of 80.
  const followingRef = useRef(true);
  const openedRef = useRef(false);
  const lastLength = items.reduce((total, item) => total + itemLength(item), 0);
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list) return;
    if (!openedRef.current && items.length > 0) {
      openedRef.current = true;
      if (list.scrollTop === 0) list.scrollTop = list.scrollHeight;
      followingRef.current = isAtBottom(list);
      return;
    }
    if (followingRef.current) list.scrollTop = list.scrollHeight;
  }, [lastLength, items.length]);

  // A mermaid diagram draws one frame AFTER the text that carried it, growing
  // the document with no delta to notice.
  const followDiagramGrowth = useCallback(() => {
    const list = listRef.current;
    if (list && followingRef.current) list.scrollTop = list.scrollHeight;
  }, []);

  const oldestKey = items.length > 0 ? conversationItemKey(items[0]) : '';
  const anchorRef = useRef<{ key: string; fromBottom: number } | null>(null);
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const anchor = anchorRef.current;
    if (anchor && anchor.key !== '' && anchor.key !== oldestKey) {
      list.scrollTop = list.scrollHeight - anchor.fromBottom;
    }
    anchorRef.current = { key: oldestKey, fromBottom: list.scrollHeight - list.scrollTop };
  }, [oldestKey]);

  const loadEarlier = useCallback(() => {
    if (!hasMoreBefore || loadingHistory || items.length === 0) return;
    historyRequested(sessionId);
    sendAgentHistory(sessionId, conversationItemKey(items[0]));
  }, [hasMoreBefore, historyRequested, items, loadingHistory, sendAgentHistory, sessionId]);

  const handleScroll = useCallback(() => {
    const list = listRef.current;
    if (!list) return;
    anchorRef.current = { key: oldestKey, fromBottom: list.scrollHeight - list.scrollTop };
    followingRef.current = isAtBottom(list);
    if (list.scrollTop < list.clientHeight) loadEarlier();
  }, [loadEarlier, oldestKey]);

  useEffect(() => {
    if (paneActive && canSend) inputRef.current?.focus();
  }, [paneActive, canSend]);

  const send = useCallback((mode: AgentPromptMode) => {
    const text = draft.trim();
    if (!text || !canSend) return;
    sendAgentPrompt(sessionId, text, mode);
    if (mode === 'prompt') {
      promptSent(sessionId);
    }
    setDraft('');
  }, [canSend, draft, promptSent, sendAgentPrompt, sessionId]);

  const reload = useCallback(() => {
    setReloading(true);
    setReloadError(null);
    void reloadSession(sessionId)
      .catch((error: unknown) => {
        setReloadError(error instanceof Error ? error.message : String(error));
      })
      .finally(() => setReloading(false));
  }, [reloadSession, sessionId]);

  const primary: AgentPromptMode = running ? 'steer' : 'prompt';

  const handleKeyDown = useCallback((event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      send(primary);
    }
  }, [primary, send]);

  return (
    <div className="conversation-pane" data-testid={`conversation-pane-${sessionId}`}>
      {models.length > 0 && (
        <div className="conversation-pane-header" data-testid="conversation-header">
          <label className="conversation-model-label" htmlFor={`conversation-model-${sessionId}`}>Model</label>
          {/* The value is the host's word, not this select's: a refused switch
              comes back as the model still in force. */}
          <select
            id={`conversation-model-${sessionId}`}
            className="conversation-model"
            data-testid="conversation-model"
            value={models.includes(model) ? model : ''}
            disabled={!ready}
            onChange={(event) => {
              if (event.target.value !== '') sendAgentSetModel(sessionId, event.target.value);
            }}
          >
            {!models.includes(model) && <option value="">{model || 'Unknown'}</option>}
            {models.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </div>
      )}
      <div
        className="conversation-pane-messages"
        ref={listRef}
        onScroll={handleScroll}
        data-testid="conversation-messages"
      >
        {droppedBefore > 0 && !hasMoreBefore && (
          <div
            className="conversation-notice conversation-pane-dropped"
            data-testid="conversation-history-dropped"
            data-dropped={droppedBefore}
          >
            {`${droppedBefore.toLocaleString()} earlier ${droppedBefore === 1 ? 'item' : 'items'} are no longer kept for this conversation.`}
          </div>
        )}
        {hasMoreBefore && (
          <button
            type="button"
            className="conversation-pane-earlier"
            data-testid="conversation-load-earlier"
            disabled={loadingHistory}
            onClick={loadEarlier}
          >
            {loadingHistory ? 'Loading earlier messages...' : 'Load earlier messages'}
          </button>
        )}
        {items.length === 0 ? (
          <div className="conversation-pane-empty">
            {ready ? 'Ask this agent something.' : recoverable ? '' : 'Starting the agent...'}
          </div>
        ) : (
          items.map((item) => {
            if (item.kind === 'tool') {
              return (
                <ToolCard
                  key={`tool:${item.callId}`}
                  sessionId={sessionId}
                  tool={item}
                  resolvedTheme={resolvedTheme}
                />
              );
            }
            if (item.kind === 'notice') {
              return (
                <div
                  key={`notice:${item.id}`}
                  className={`conversation-notice conversation-notice--${item.level}`}
                  data-testid={`conversation-notice-${item.id}`}
                  data-level={item.level}
                  data-done={item.done ? 'true' : 'false'}
                >
                  {item.text}
                </div>
              );
            }
            return (
              <div
                key={`message:${item.id}`}
                className={`conversation-message conversation-message--${item.role}`}
                data-testid={`conversation-message-${item.id}`}
                data-role={item.role}
                data-streaming={item.streaming ? 'true' : 'false'}
              >
                <div className="conversation-message-role">{item.role}</div>
                {/* Enter is a line break in the composer's textarea, so `breaks`
                    is set on the user's side only. */}
                <MarkdownBoundary
                  key={`md:${item.id}`}
                  fallback={<div className="conversation-message-text conversation-message-text--raw">{item.text}</div>}
                >
                  {/* A diagram too wide for the column gets size detection, focus
                      view and zoom rather than a silent squeeze. */}
                  <ReaderPresentation>
                  <Markdown
                    className="conversation-message-text"
                    breaks={item.role === 'user'}
                    streaming={item.streaming}
                    onDiagramLayoutChange={followDiagramGrowth}
                  >
                    {item.text}
                  </Markdown>
                  </ReaderPresentation>
                </MarkdownBoundary>
              </div>
            );
          })
        )}
      </div>
      {recoverable && (
        <div className="conversation-pane-recoverable" data-testid="conversation-recoverable">
          <span className="conversation-pane-recoverable-text">
            {reloadError ?? 'This agent stopped. Reload to pick the conversation back up.'}
          </span>
          <button
            type="button"
            className="conversation-pane-reload"
            data-testid="conversation-reload"
            disabled={reloading}
            onClick={reload}
          >
            {reloading ? 'Reloading...' : 'Reload'}
          </button>
        </div>
      )}
      {pending.length > 0 && (
        <div className="conversation-pane-queue" data-testid="conversation-queue">
          <div className="conversation-pane-queue-header">
            <span className="conversation-queued-label">Not read yet</span>
            {/* pi clears both queues or neither, and the strip empties on pi's own
                queue_update, not on this click. */}
            <button
              type="button"
              className="conversation-queue-clear"
              data-testid="conversation-queue-clear"
              title="Drop everything the agent has not read yet"
              onClick={() => sendAgentClearQueue(sessionId)}
            >
              Cancel all
            </button>
          </div>
          {pending.map((entry, index) => (
            <div className="conversation-queued" key={`${index}-${entry}`} data-testid="conversation-queued">
              <span className="conversation-queued-label">
                {index < queue.steering.length ? 'Steering' : 'Follow-up'}
              </span>
              <span className="conversation-queued-text">{entry}</span>
            </div>
          ))}
        </div>
      )}
      <div className="conversation-pane-composer">
        <textarea
          ref={inputRef}
          className="conversation-pane-input"
          data-testid="conversation-input"
          value={draft}
          disabled={!canSend}
          placeholder={awaitingRun ? 'Sending...' : running ? 'Steer the agent' : ready ? 'Message the agent' : recoverable ? 'Reload to continue' : 'Waiting for the agent'}
          rows={2}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={handleKeyDown}
        />
        {running && (
          <button
            type="button"
            className="conversation-pane-followup"
            data-testid="conversation-follow-up"
            title="Queue this until the agent finishes what it is doing"
            disabled={!canSend || draft.trim() === ''}
            onClick={() => send('follow_up')}
          >
            Follow up
          </button>
        )}
        <button
          type="button"
          className="conversation-pane-send"
          data-testid="conversation-send"
          disabled={!canSend || draft.trim() === ''}
          onClick={() => send(primary)}
        >
          {running ? 'Steer' : 'Send'}
        </button>
      </div>
    </div>
  );
}
