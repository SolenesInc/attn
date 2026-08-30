import { openUrl } from '@tauri-apps/plugin-opener';
import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { SessionPullRequest } from '../types/generated';
import { writeClipboardText } from '../utils/clipboardBridge';
import {
  describeSessionPullRequest,
  describeSessionPullRequestChecks,
  describeSessionPullRequestMerge,
  describeSessionPullRequestReview,
  sessionPullRequestAwaitsStatus,
  sessionPullRequestRepositoryName,
  type SessionPullRequestDescription,
} from '../utils/sessionPullRequest';
import './SessionPullRequestPopover.css';

export interface PopoverAnchor {
  top: number;
  left: number;
}

const VIEWPORT_MARGIN = 8;

function formatAge(iso: string): string {
  const time = Date.parse(iso);
  if (!Number.isFinite(time)) return '';
  const seconds = Math.max(0, Math.round((Date.now() - time) / 1000));
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h ago`;
  return `${Math.round(seconds / 86400)}d ago`;
}

function Value({ description }: { description: SessionPullRequestDescription }) {
  return (
    <span className="session-pr-popover__value">
      <i className="session-pr-popover__dot" data-tone={description.tone} aria-hidden="true" />
      {description.label}
    </span>
  );
}

export function SessionPullRequestPopover({
  pullRequests,
  anchor,
  autoFocus,
  onClose,
  onPointerEnter,
  onPointerLeave,
}: {
  pullRequests: readonly SessionPullRequest[];
  anchor: PopoverAnchor;
  autoFocus: boolean;
  onClose: () => void;
  onPointerEnter: () => void;
  onPointerLeave: () => void;
}) {
  const cardRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState(anchor);
  const [selected, setSelected] = useState(0);

  // Escape only belongs to a popover the user clicked into. A hover popover
  // that grabbed it would eat the terminal's Escape, which agents live on.
  useEscapeStack(onClose, autoFocus);

  useLayoutEffect(() => {
    const card = cardRef.current;
    if (!card) return;
    const rect = card.getBoundingClientRect();
    setPosition({
      top: Math.max(VIEWPORT_MARGIN, Math.min(anchor.top, window.innerHeight - rect.height - VIEWPORT_MARGIN)),
      left: Math.max(VIEWPORT_MARGIN, Math.min(anchor.left, window.innerWidth - rect.width - VIEWPORT_MARGIN)),
    });
  }, [anchor, pullRequests.length]);

  // Only a click hands the popover focus. On hover the terminal keeps it, so a
  // bare `c` or `↵` still reaches the agent instead of this panel.
  useEffect(() => {
    if (autoFocus) cardRef.current?.focus();
  }, [autoFocus]);

  useEffect(() => {
    const handleMouseDown = (event: MouseEvent) => {
      if (cardRef.current && !cardRef.current.contains(event.target as Node)) onClose();
    };
    const id = window.setTimeout(() => document.addEventListener('mousedown', handleMouseDown), 0);
    return () => {
      window.clearTimeout(id);
      document.removeEventListener('mousedown', handleMouseDown);
    };
  }, [onClose]);

  const current = pullRequests[Math.min(selected, pullRequests.length - 1)];
  if (!current) return null;

  const open = (pr: SessionPullRequest) => {
    openUrl(pr.url).catch((error) => {
      console.error('[SessionPullRequestPopover] Failed to open PR URL:', error);
    });
  };

  // Only while the panel itself holds focus. Tabbing onto a list button hands
  // the keyboard back to that button, so ↵ opens the one the user is on.
  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget) return;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const step = event.key === 'ArrowDown' ? 1 : -1;
      setSelected((index) => Math.min(pullRequests.length - 1, Math.max(0, index + step)));
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      open(current);
      return;
    }
    if (event.key === 'c' && !event.metaKey && !event.ctrlKey && !event.altKey) {
      event.preventDefault();
      writeClipboardText(current.url).catch((error) => {
        console.error('[SessionPullRequestPopover] Failed to copy PR URL:', error);
      });
    }
  };

  const identity = `${sessionPullRequestRepositoryName(current.repository)}#${current.number}`;
  const summary = describeSessionPullRequest(current);
  const awaitingStatus = sessionPullRequestAwaitsStatus(current);

  return createPortal(
    <div
      ref={cardRef}
      className="session-pr-popover"
      data-testid="session-pr-popover"
      style={{ top: position.top, left: position.left }}
      role="dialog"
      aria-label={`Pull request ${identity}`}
      tabIndex={-1}
      onKeyDown={handleKeyDown}
      onPointerDown={(event) => event.stopPropagation()}
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
    >
      <header className="session-pr-popover__head">
        <i className="session-pr-popover__dot" data-tone={summary.tone} aria-hidden="true" />
        <button
          type="button"
          className="session-pr-popover__title"
          title={`Open ${identity} on GitHub`}
          onClick={() => open(current)}
        >
          {current.title || identity}
        </button>
        <span className="session-pr-popover__identity">{identity}</span>
      </header>

      <dl className="session-pr-popover__kv">
        <dt>state</dt>
        <dd><span className="session-pr-popover__value">{current.state}</span></dd>
        {awaitingStatus ? (
          <>
            <dt>status</dt>
            <dd><span className="session-pr-popover__value">waiting for GitHub</span></dd>
          </>
        ) : (
          <>
            <dt>checks</dt>
            <dd><Value description={describeSessionPullRequestChecks(current)} /></dd>
            <dt>review</dt>
            <dd><Value description={describeSessionPullRequestReview(current)} /></dd>
            <dt>merge</dt>
            <dd><Value description={describeSessionPullRequestMerge(current)} /></dd>
          </>
        )}
        <dt>opened</dt>
        <dd>
          <span className="session-pr-popover__value">
            {[formatAge(current.created_at), new Date(current.created_at).toLocaleString()]
              .filter(Boolean).join(' · ')}
          </span>
        </dd>
      </dl>

      {pullRequests.length > 1 && (
        <ul className="session-pr-popover__list" aria-label="Pull requests from this session">
          {pullRequests.map((pr, index) => {
            const entry = describeSessionPullRequest(pr);
            return (
              <li key={`${pr.repository}#${pr.number}`}>
                <button
                  type="button"
                  className="session-pr-popover__item"
                  data-selected={index === selected ? 'true' : undefined}
                  data-dim={pr.state === 'merged' || pr.state === 'closed' ? 'true' : undefined}
                  onPointerEnter={() => setSelected(index)}
                  onClick={() => open(pr)}
                >
                  <i className="session-pr-popover__dot" data-tone={entry.tone} aria-hidden="true" />
                  <span className="session-pr-popover__item-number">#{pr.number}</span>
                  <span className="session-pr-popover__item-title">{pr.title || pr.url}</span>
                  <span className="session-pr-popover__item-status">{entry.label}</span>
                </button>
              </li>
            );
          })}
        </ul>
      )}

      <footer className="session-pr-popover__foot">
        {pullRequests.length > 1 && <span><kbd>↑↓</kbd>pick</span>}
        <span><kbd>↵</kbd>open</span>
        <span><kbd>c</kbd>copy url</span>
      </footer>
    </div>,
    document.body,
  );
}
