import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useNavigationContext, useAppPanelsContext } from '../application/AppContexts';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { CalloutAnchor, type CalloutStep } from '../types/generated';
import { calloutElements, calloutTarget } from './calloutAnchors';
import { useCalloutStore } from './useCalloutStore';
import './CalloutOverlay.css';

const CARD_WIDTH = 300;
const GAP = 14;
const MARGIN = 8;

type Placement = {
  left: number;
  top: number;
  side: 'right' | 'left' | 'below' | 'none';
  arrow: number;
  rect: DOMRect | null;
};

function placeCard(step: CalloutStep, card: HTMLElement): Placement {
  const target = calloutTarget(step);
  const width = Math.min(CARD_WIDTH, window.innerWidth - MARGIN * 2);
  const height = card.getBoundingClientRect().height;
  const clampX = (x: number) => Math.max(MARGIN, Math.min(x, window.innerWidth - width - MARGIN));
  const clampY = (y: number) => Math.max(MARGIN, Math.min(y, window.innerHeight - height - MARGIN));
  if (!target) return { left: clampX((window.innerWidth - width) / 2), top: clampY((window.innerHeight - height) / 2), side: 'none', arrow: 0, rect: null };
  const rect = target.rect;
  const bar = target.elements[0].closest('.queue-bar');
  const side = !bar && rect.right + GAP + width <= window.innerWidth - MARGIN ? 'right'
    : bar || rect.bottom + GAP + height <= window.innerHeight - MARGIN ? 'below' : 'left';
  const left = clampX(side === 'right' ? rect.right + GAP : side === 'left' ? rect.left - width - GAP : rect.left + Math.min(rect.width / 2, 120) - 40);
  const top = clampY(side === 'below' ? rect.bottom + GAP : rect.height > height ? rect.top + 48 : rect.top + rect.height / 2 - 28);
  const arrow = side === 'below'
    ? Math.max(16, Math.min(width - 16, rect.left + rect.width / 2 - left))
    : Math.max(16, Math.min(height - 16, rect.height > height ? 24 : rect.top + rect.height / 2 - top));
  return { left, top, side, arrow, rect };
}

export function CalloutOverlay() {
  const callout = useCalloutStore((state) => state.callout);
  const { selectAgent, handleSelectDesktop, requestTerminalFocus } = useNavigationContext();
  const { agentListOpen, toggleAgentList } = useAppPanelsContext();
  const cardRef = useRef<HTMLDivElement>(null);
  const navigating = useRef(false);
  const previous = useRef(callout);
  const [placement, setPlacement] = useState<Placement | null>(null);
  const step = callout?.steps[callout.index];
  const end = useCalloutStore.getState().end;

  useLayoutEffect(() => {
    if (previous.current && !callout && !navigating.current) requestTerminalFocus();
    previous.current = callout;
    navigating.current = false;
  }, [callout, requestTerminalFocus]);

  const advance = (goThere: boolean) => {
    if (!step) return;
    if (goThere && (step.session_id || step.desktop_id)) {
      navigating.current = true;
      end();
      if (step.session_id) selectAgent(step.session_id);
      else if (step.desktop_id) handleSelectDesktop(step.desktop_id);
    } else useCalloutStore.getState().next();
  };

  useEscapeStack(end, Boolean(callout));

  useEffect(() => {
    if (!callout) return;
    const keydown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
      if (event.key !== 'Enter' && event.key !== 'ArrowRight') return;
      event.preventDefault();
      event.stopPropagation();
      advance(event.key === 'Enter');
    };
    const outside = (event: PointerEvent) => {
      if (!cardRef.current?.contains(event.target as Node)) end();
    };
    window.addEventListener('keydown', keydown, true);
    window.addEventListener('pointerdown', outside, true);
    return () => {
      window.removeEventListener('keydown', keydown, true);
      window.removeEventListener('pointerdown', outside, true);
    };
  });

  useLayoutEffect(() => {
    const card = cardRef.current;
    if (!step || !card) return;
    const place = () => {
      const next = placeCard(step, card);
      setPlacement((current) => current
        && current.left === next.left && current.top === next.top
        && current.side === next.side && current.arrow === next.arrow
        && current.rect?.x === next.rect?.x && current.rect?.y === next.rect?.y
        && current.rect?.width === next.rect?.width && current.rect?.height === next.rect?.height
        ? current : next);
    };
    const observer = new ResizeObserver(place);
    observer.observe(card);
    for (const element of calloutElements(step)) observer.observe(element);
    let frame: number | undefined;
    if (step.anchor === CalloutAnchor.Agent || step.anchor === CalloutAnchor.Desktop) {
      const reveal = () => {
        const target = calloutElements(step)[0];
        target?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
        for (const element of calloutElements(step)) observer.observe(element);
        place();
      };
      if (!calloutElements(step).length && step.anchor === CalloutAnchor.Agent && !agentListOpen
        && document.querySelector('[data-testid="queue-agents-toggle"]')) {
        frame = requestAnimationFrame(() => {
          toggleAgentList();
          reveal();
        });
      } else reveal();
    }
    place();
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      if (frame !== undefined) cancelAnimationFrame(frame);
      observer.disconnect();
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [callout?.requestId, callout?.index, step, agentListOpen, toggleAgentList]);

  if (!callout || !step) return null;
  const dynamic = Boolean(step.session_id || step.desktop_id);
  const last = callout.index === callout.steps.length - 1;
  const rect = placement?.rect;
  return createPortal(
    <div className="callout-overlay" data-testid="callout-overlay">
      {rect && <div key={`${callout.requestId}:${callout.index}`} className="callout-ring" data-testid="callout-ring" style={{ left: Math.max(2, rect.left - 3), top: Math.max(2, rect.top - 3), width: Math.min(window.innerWidth - 2, rect.right + 3) - Math.max(2, rect.left - 3), height: Math.min(window.innerHeight - 2, rect.bottom + 3) - Math.max(2, rect.top - 3) }} />}
      <div key={`${callout.requestId}:${callout.index}:card`} ref={cardRef} className="callout-card" role="dialog" aria-label={`Callout from ${callout.from}`} data-side={placement?.side ?? 'none'} data-anchor={step.anchor}
        style={placement ? { left: placement.left, top: placement.top, '--callout-arrow': `${placement.arrow}px` } as React.CSSProperties : { visibility: 'hidden' }}>
        <div className="callout-heading"><span>{callout.from}</span><span>{callout.index + 1} of {callout.steps.length}</span><button type="button" aria-label="End callout" onClick={end}>×</button></div>
        {!rect && <div className="callout-unanchored">{step.label ?? step.anchor} isn't on screen</div>}
        <p>{step.text}</p>
        <div className="callout-actions">
          <button type="button" onClick={() => advance(true)}><kbd>↵</kbd> {dynamic ? 'Go there' : last ? 'Done' : 'Next'}</button>
          {(dynamic || !last) && <button type="button" onClick={() => advance(false)}><kbd>→</kbd> {last ? 'Done' : 'Next'}</button>}
          <span><kbd>esc</kbd> end</span>
        </div>
      </div>
    </div>, document.body,
  );
}
