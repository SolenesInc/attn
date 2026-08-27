import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import { createPortal } from 'react-dom';
import type { Seed } from '../types/generated';
import { crewHolderName } from '../utils/crewName';
import './TerminalSeedPreview.css';

export interface TerminalSeedAnchor {
  left: number;
  right: number;
  top: number;
  bottom: number;
  bounds: { left: number; right: number; top: number; bottom: number };
}

interface TerminalSeedPreviewProps {
  seed: Seed;
  anchor: TerminalSeedAnchor;
  onOpen: (seedId: string) => void;
  onClose: () => void;
  onPointerEnter: () => void;
  onPointerLeave: () => void;
}

interface PreviewPlacement {
  left: number;
  top: number;
  arrowLeft: number;
  side: 'above' | 'below';
}

const VIEWPORT_MARGIN = 8;
const ANCHOR_GAP = 9;
const ARROW_MARGIN = 22;

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

function placementFor(
  anchor: TerminalSeedAnchor,
  size: { width: number; height: number },
): PreviewPlacement {
  const viewport = { left: 0, top: 0, right: window.innerWidth, bottom: window.innerHeight };
  const boundsWidth = anchor.bounds.right - anchor.bounds.left;
  const boundsHeight = anchor.bounds.bottom - anchor.bounds.top;
  const bounds = boundsWidth >= size.width + VIEWPORT_MARGIN * 2
    && boundsHeight >= size.height + VIEWPORT_MARGIN * 2
    ? anchor.bounds
    : viewport;
  const center = (anchor.left + anchor.right) / 2;
  const left = clamp(
    center - size.width / 2,
    bounds.left + VIEWPORT_MARGIN,
    bounds.right - size.width - VIEWPORT_MARGIN,
  );
  const above = anchor.top - size.height - ANCHOR_GAP;
  const side = above >= bounds.top + VIEWPORT_MARGIN ? 'above' : 'below';
  const proposedTop = side === 'above' ? above : anchor.bottom + ANCHOR_GAP;
  const top = clamp(
    proposedTop,
    bounds.top + VIEWPORT_MARGIN,
    bounds.bottom - size.height - VIEWPORT_MARGIN,
  );
  return {
    left,
    top,
    side,
    arrowLeft: clamp(center - left, ARROW_MARGIN, size.width - ARROW_MARGIN),
  };
}

export function terminalSeedBodyExcerpt(body: string, maxLength = 260): string {
  const plain = body
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/^\s{0,3}#{1,6}\s+/gm, '')
    .replace(/^\s*(?:[-*+] |\d+\. )/gm, '')
    .replace(/[*_~`>|]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  if (plain.length <= maxLength) return plain;
  const clipped = plain.slice(0, maxLength + 1);
  const boundary = clipped.lastIndexOf(' ');
  return `${clipped.slice(0, boundary > maxLength * 0.65 ? boundary : maxLength).trimEnd()}…`;
}

function formatAge(iso: string): string {
  const time = Date.parse(iso);
  if (!Number.isFinite(time)) return '';
  const seconds = Math.max(0, Math.round((Date.now() - time) / 1000));
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h ago`;
  return `${Math.round(seconds / 86400)}d ago`;
}

export function TerminalSeedPreview({
  seed,
  anchor,
  onOpen,
  onClose,
  onPointerEnter,
  onPointerLeave,
}: TerminalSeedPreviewProps) {
  const cardRef = useRef<HTMLElement>(null);
  const [placement, setPlacement] = useState<PreviewPlacement | null>(null);
  const titleId = useId();
  const excerpt = terminalSeedBodyExcerpt(seed.body);
  const tender = crewHolderName(seed.tender_member, seed.tender_session);
  const updated = formatAge(seed.updated_at);

  useLayoutEffect(() => {
    const card = cardRef.current;
    if (!card) return;
    const place = () => {
      const rect = card.getBoundingClientRect();
      setPlacement(placementFor(anchor, { width: rect.width, height: rect.height }));
    };
    place();
    window.addEventListener('resize', place);
    return () => window.removeEventListener('resize', place);
  }, [anchor]);

  const handlePointerDown = (event: ReactPointerEvent<HTMLElement>) => {
    event.stopPropagation();
  };
  const style = placement
    ? ({
        left: placement.left,
        top: placement.top,
        '--terminal-seed-arrow-left': `${placement.arrowLeft}px`,
      } as CSSProperties)
    : ({ left: anchor.left, top: anchor.top, visibility: 'hidden' } as CSSProperties);

  return createPortal(
    <aside
      ref={cardRef}
      className="terminal-seed-preview"
      data-placement={placement?.side ?? 'above'}
      data-terminal-seed-preview={seed.id}
      role="dialog"
      aria-labelledby={titleId}
      style={style}
      onPointerDown={handlePointerDown}
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
      onKeyDown={(event) => {
        if (event.key === 'Escape') onClose();
      }}
    >
      <div className="terminal-seed-preview__rail" aria-hidden="true" />
      <div className="terminal-seed-preview__head">
        <div className="terminal-seed-preview__identity">
          <h2 id={titleId}>{seed.title}</h2>
          <span className="terminal-seed-preview__id">{seed.id}</span>
        </div>
        <button
          type="button"
          className="terminal-seed-preview__open"
          data-tooltip="Open as tile"
          title="Open as tile"
          aria-label="Open as tile"
          onClick={() => onOpen(seed.id)}
        >
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
            <rect x="1.75" y="2.25" width="12.5" height="11.5" rx="1.5" />
            <path d="M9.25 2.5v11" />
            <path d="M4.25 8h3M6 6.25 7.75 8 6 9.75" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
      </div>
      {excerpt ? <p className="terminal-seed-preview__body">{excerpt}</p> : null}
      <div className="terminal-seed-preview__meta">
        <span className="terminal-seed-preview__status" data-status={seed.status}>
          {seed.status}
        </span>
        {tender ? <span>tended by {tender}</span> : null}
        {updated ? <span>updated {updated}</span> : null}
      </div>
    </aside>,
    document.body,
  );
}
