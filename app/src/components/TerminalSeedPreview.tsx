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
import {
  TERMINAL_SEED_PREVIEW_FALLBACK_SIZE,
  terminalSeedPreviewPlacement,
  type TerminalSeedAnchor,
  type TerminalSeedPreviewPlacement,
} from '../utils/terminalSeedPreviewPlacement';
import { terminalSeedBodyExcerpt } from '../utils/terminalSeedPreviewText';
import './TerminalSeedPreview.css';

export type { TerminalSeedAnchor } from '../utils/terminalSeedPreviewPlacement';

interface TerminalSeedPreviewProps {
  seed: Seed;
  anchor: TerminalSeedAnchor;
  onOpen: (seedId: string) => void;
  onClose: () => void;
  onPointerEnter: () => void;
  onPointerLeave: () => void;
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

function stopPreviewPointerDown(event: ReactPointerEvent<HTMLDialogElement>) {
  event.stopPropagation();
}

export function TerminalSeedPreview({
  seed,
  anchor,
  onOpen,
  onClose,
  onPointerEnter,
  onPointerLeave,
}: TerminalSeedPreviewProps) {
  const cardRef = useRef<HTMLDialogElement>(null);
  const [placement, setPlacement] = useState<TerminalSeedPreviewPlacement | null>(null);
  const titleId = useId();
  const excerpt = terminalSeedBodyExcerpt(seed.body);
  const tender = crewHolderName(seed.tender_member, seed.tender_session);
  const updated = formatAge(seed.updated_at);

  useLayoutEffect(() => {
    const card = cardRef.current;
    if (!card) return;
    const place = () => {
      const rect = card.getBoundingClientRect();
      const width = card.offsetWidth || rect.width || Math.min(
        TERMINAL_SEED_PREVIEW_FALLBACK_SIZE.width,
        window.innerWidth - 16,
      );
      const height = card.offsetHeight || rect.height || TERMINAL_SEED_PREVIEW_FALLBACK_SIZE.height;
      setPlacement(terminalSeedPreviewPlacement(
        anchor,
        { width, height },
        { width: window.innerWidth, height: window.innerHeight },
      ));
    };
    place();
    window.addEventListener('resize', place);
    return () => window.removeEventListener('resize', place);
  }, [anchor, excerpt, seed.status, seed.title, tender, updated]);

  const style = placement
    ? ({
        left: placement.left,
        top: placement.top,
        '--terminal-seed-entry-x': `${placement.entryX}px`,
        '--terminal-seed-entry-y': `${placement.entryY}px`,
      } as CSSProperties)
    : ({ left: anchor.left, top: anchor.top, visibility: 'hidden' } as CSSProperties);

  return createPortal(
    <>
      {placement ? (
        <svg
          className="terminal-seed-preview-signal"
          data-placement={placement.side}
          width={window.innerWidth}
          height={window.innerHeight}
          viewBox={`0 0 ${window.innerWidth} ${window.innerHeight}`}
          aria-hidden="true"
        >
          <path className="terminal-seed-preview-signal__shadow" pathLength={1} d={placement.path} />
          <path className="terminal-seed-preview-signal__core" pathLength={1} d={placement.path} />
          <path className="terminal-seed-preview-signal__echo" pathLength={1} d={placement.path} />
          <circle
            className="terminal-seed-preview-signal__origin"
            cx={placement.source.x}
            cy={placement.source.y}
            r="2.2"
          />
          <circle
            className="terminal-seed-preview-signal__destination"
            cx={placement.destination.x}
            cy={placement.destination.y}
            r="2"
          />
        </svg>
      ) : null}
      <dialog
        ref={cardRef}
        open
        className={`terminal-seed-preview${placement ? ' is-placed' : ''}`}
        data-placement={placement?.side ?? 'right'}
        data-terminal-seed-preview={seed.id}
        aria-labelledby={titleId}
        style={style}
        onPointerDown={stopPreviewPointerDown}
        onPointerEnter={onPointerEnter}
        onPointerLeave={onPointerLeave}
        onKeyDown={(event) => {
          if (event.key === 'Escape') onClose();
        }}
      >
        <div className="terminal-seed-preview__chrome" aria-hidden="true" />
        <div className="terminal-seed-preview__content">
          <header className="terminal-seed-preview__head">
            <span className="terminal-seed-preview__status" data-status={seed.status}>
              <i aria-hidden="true" />
              {seed.status}
            </span>
            <button
              type="button"
              className="terminal-seed-preview__open"
              data-tooltip="Open as tile"
              title="Open as tile"
              aria-label="Open as tile"
              onClick={() => onOpen(seed.id)}
            >
              <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path d="M2.5 3.5h7v9h-7zM11.5 3.5h2v9h-2M5 6h2.5M5 8h2.5M5 10h1.5" />
              </svg>
            </button>
          </header>
          <div className="terminal-seed-preview__body">
            <h2 id={titleId}>{seed.title}</h2>
            {excerpt ? <p>{excerpt}</p> : null}
            <dl className="terminal-seed-preview__meta">
              {tender ? (
                <div>
                  <dt>Tender</dt>
                  <dd>{tender}</dd>
                </div>
              ) : null}
              {updated ? (
                <div>
                  <dt>Updated</dt>
                  <dd>{updated}</dd>
                </div>
              ) : null}
            </dl>
          </div>
        </div>
      </dialog>
    </>,
    document.body,
  );
}
