import { useLayoutEffect, useRef, useState, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import type { Ask } from '../../types/generated';
import { useAsksOf } from '../../store/asks';
import { askQuestions, type AskHolderSession } from '../../utils/asks';
import { clampIntoViewport, type ViewportPoint } from '../../utils/viewportClamp';
import { useAnchoredPopover } from '../useAnchoredPopover';
import './Asks.css';

const NOTHING_TO_RELEASE = () => {};

export function PaneAskChip({ session, onOpenAsk }: { session: AskHolderSession; onOpenAsk: (askId: string) => void }) {
  const asks = useAsksOf(session);
  if (asks.length === 0) return null;
  const oldestFirst = [...asks].sort((a, b) => a.created_at.localeCompare(b.created_at));
  return <AskChip asks={oldestFirst} sessionId={session.id} onOpenAsk={onOpenAsk} />;
}

function AskChip({ asks, sessionId, onOpenAsk }: { asks: readonly Ask[]; sessionId: string; onOpenAsk: (askId: string) => void }) {
  const preview = useAnchoredPopover(false, NOTHING_TO_RELEASE);
  const [first] = asks;
  const more = asks.length - 1;
  const open = (askId: string) => {
    preview.close();
    onOpenAsk(askId);
  };
  return (
    <>
      <button
        ref={preview.anchorRef}
        type="button"
        className="pane-ask-chip"
        data-testid={`ask-chip-${sessionId}`}
        title=""
        aria-label={`Open ask: ${first.subject}${more ? `, and ${more} more` : ''}`}
        onPointerDown={(event) => event.stopPropagation()}
        onPointerEnter={preview.scheduleOpen}
        onPointerLeave={preview.scheduleClose}
        onClick={(event) => {
          event.stopPropagation();
          open(first.id);
        }}
      >
        <span className="ask-marker" aria-hidden="true">?</span>
        <span className="pane-ask-chip-subject">{first.subject}</span>
        {more > 0 && <span className="pane-ask-chip-more">+{more}</span>}
      </button>
      {preview.open && preview.anchor && (
        <AskPreview
          asks={asks}
          anchorRef={preview.anchorRef}
          onOpen={open}
          onPointerEnter={preview.cancelClose}
          onPointerLeave={preview.scheduleClose}
        />
      )}
    </>
  );
}

interface AskPreviewProps {
  asks: readonly Ask[];
  anchorRef: RefObject<HTMLButtonElement | null>;
  onOpen: (askId: string) => void;
  onPointerEnter: () => void;
  onPointerLeave: () => void;
}

function AskPreview({ asks, anchorRef, onOpen, onPointerEnter, onPointerLeave }: AskPreviewProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<ViewportPoint | null>(null);
  useLayoutEffect(() => {
    const panel = panelRef.current;
    const anchor = anchorRef.current;
    if (!panel || !anchor) return;
    const below = anchor.getBoundingClientRect();
    const size = panel.getBoundingClientRect();
    setPosition(clampIntoViewport({ top: below.bottom + 4, left: below.right - size.width }, size));
  }, [anchorRef, asks]);
  return createPortal(
    <div
      ref={panelRef}
      className="ask-preview"
      role="dialog"
      aria-label="Open asks from this agent"
      style={position ?? { top: 0, left: 0, visibility: 'hidden' }}
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
      onPointerDown={(event) => event.stopPropagation()}
    >
      {asks.map((ask) => (
        <section key={ask.id} className="ask-preview-ask" aria-label={ask.subject}>
          <div className="ask-preview-subject">{ask.subject}</div>
          {askQuestions(ask).map((question) => (
            <div key={question.key} className="ask-preview-question">
              <div className="ask-preview-prompt">{question.prompt}</div>
              {question.kind === 'text' ? (
                <div className="ask-preview-choice ask-preview-written">Written answer</div>
              ) : (
                question.choices.map((choice) => (
                  <div key={choice.label} className="ask-preview-choice">
                    <span className="ask-preview-choice-label">{choice.label}</span>
                    {choice.description && <span className="ask-preview-choice-description">{choice.description}</span>}
                    {question.recommended === choice.label && <span className="ask-recommended">recommended</span>}
                  </div>
                ))
              )}
            </div>
          ))}
          <button type="button" className="ask-preview-answer" onClick={() => onOpen(ask.id)}>Answer</button>
        </section>
      ))}
    </div>,
    document.body,
  );
}
