import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react';
import FocusTrap from 'focus-trap-react';
import { createPortal } from 'react-dom';
import type { SessionUsage } from '../../types/generated';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useAnchoredPopover } from '../useAnchoredPopover';

const VIEWPORT_MARGIN = 8;
const usdFormatter = new Intl.NumberFormat('en-US', {
  style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2,
});
const exactTokenFormatter = new Intl.NumberFormat('en-US');

function formatSessionUsageUSD(costUsd: number): string {
  if (costUsd > 0 && costUsd < 0.01) return '<$0.01';
  return usdFormatter.format(costUsd);
}

function formatCompactTokens(tokens: number): string {
  if (tokens >= 1_000_000_000) return `${compactNumber(tokens, 1_000_000_000)}b tokens`;
  if (tokens >= 1_000_000) return `${compactNumber(tokens, 1_000_000)}m tokens`;
  if (tokens >= 1_000) return `${compactNumber(tokens, 1_000)}k tokens`;
  return `${exactTokenFormatter.format(tokens)} tokens`;
}

function compactNumber(value: number, divisor: number): string {
  const scaled = value / divisor;
  const digits = scaled >= 100 || Number.isInteger(scaled) ? 0 : 1;
  return scaled.toFixed(digits).replace(/\.0$/, '');
}

function usageBadge(usage: SessionUsage): string {
  if (usage.cost_usd !== undefined) {
    return `${formatSessionUsageUSD(usage.cost_usd)}${usage.has_unpriced_usage ? '*' : ''}`;
  }
  return formatCompactTokens(usage.total_tokens);
}

export function HeaderSessionUsage({ usage, sessionId, pinned, onPopoverClosed }: {
  usage?: SessionUsage;
  sessionId: string;
  pinned: boolean;
  onPopoverClosed: () => void;
}) {
  const popover = useAnchoredPopover(pinned, onPopoverClosed);
  if (!usage || usage.measurement_incomplete || usage.total_tokens <= 0 || usage.models.length === 0) {
    return null;
  }

  const badge = usageBadge(usage);
  const detail = usage.has_unpriced_usage ? ', some usage has no price' : '';
  return (
    <>
      <button
        ref={popover.anchorRef}
        type="button"
        className="workspace-pane-usage"
        data-testid={`session-usage-${sessionId}`}
        title=""
        aria-label={`Session usage ${badge}${detail}`}
        aria-haspopup="dialog"
        aria-expanded={popover.open}
        onPointerDown={(event) => event.stopPropagation()}
        onPointerEnter={popover.scheduleOpen}
        onPointerLeave={popover.scheduleClose}
        onFocus={popover.openNow}
        onBlur={popover.scheduleClose}
        onKeyDown={(event) => {
          if (event.key === 'ArrowDown') {
            event.preventDefault();
            event.stopPropagation();
            popover.pin();
          } else if (event.key === 'Escape') {
            event.preventDefault();
            popover.close();
          }
        }}
        onClick={(event) => {
          event.stopPropagation();
          if (popover.pinned) popover.close();
          else popover.pin();
        }}
      >
        {badge}
      </button>
      {popover.open && popover.anchor ? (
        <SessionUsagePopover
          usage={usage}
          anchor={popover.anchor}
          anchorRef={popover.anchorRef}
          pinned={popover.pinned}
          onClose={popover.close}
          onPointerEnter={popover.cancelClose}
          onPointerLeave={popover.scheduleClose}
        />
      ) : null}
    </>
  );
}

function SessionUsagePopover({ usage, anchor, anchorRef, pinned, onClose, onPointerEnter, onPointerLeave }: {
  usage: SessionUsage;
  anchor: { top: number; right: number };
  anchorRef: RefObject<HTMLElement | null>;
  pinned: boolean;
  onClose: () => void;
  onPointerEnter: () => void;
  onPointerLeave: () => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  useEscapeStack(onClose, pinned);

  useLayoutEffect(() => {
    const element = containerRef.current;
    if (!element) return;
    const place = () => {
      const rect = element.getBoundingClientRect();
      const source = anchorRef.current?.getBoundingClientRect();
      const left = Math.max(VIEWPORT_MARGIN, Math.min(
        (source?.right ?? anchor.right) - rect.width,
        window.innerWidth - rect.width - VIEWPORT_MARGIN,
      ));
      const top = Math.max(VIEWPORT_MARGIN, Math.min(
        source ? source.bottom + 4 : anchor.top,
        window.innerHeight - rect.height - VIEWPORT_MARGIN,
      ));
      setPosition((previous) => previous?.top === top && previous.left === left ? previous : { top, left });
    };
    place();
    const observer = new ResizeObserver(place);
    observer.observe(element);
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [anchor.top, anchor.right, anchorRef]);

  useEffect(() => {
    if (!pinned) return;
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (containerRef.current?.contains(target) || anchorRef.current?.contains(target)) return;
      onClose();
    };
    document.addEventListener('pointerdown', handlePointerDown, true);
    return () => document.removeEventListener('pointerdown', handlePointerDown, true);
  }, [pinned, onClose, anchorRef]);

  const content = (
    <div
      ref={containerRef}
      className="session-usage-popover"
      role="dialog"
      aria-label="Session usage breakdown"
      tabIndex={-1}
      style={position ? position : { top: anchor.top, left: 0, visibility: 'hidden' }}
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
      onPointerDown={(event) => event.stopPropagation()}
      onKeyDown={(event) => event.stopPropagation()}
    >
      <div className="session-usage-summary">
        <div><strong>Session usage</strong><span>{exactTokenFormatter.format(usage.total_tokens)} tokens</span></div>
        {usage.cost_usd !== undefined ? (
          <strong>{formatSessionUsageUSD(usage.cost_usd)}{usage.has_unpriced_usage ? '*' : ''}</strong>
        ) : null}
      </div>
      <div className="session-usage-models">
        {usage.models.map((model) => (
          <section key={model.model} className="session-usage-model">
            <header>
              <strong>{model.model}</strong>
              <span>
                {exactTokenFormatter.format(model.total_tokens)} tokens
                {model.cost_usd !== undefined ? ` · ${formatSessionUsageUSD(model.cost_usd)}${model.has_unpriced_usage ? '*' : ''}` : ''}
              </span>
            </header>
            <dl>
              <UsageCount label="Input" value={model.input_tokens} />
              <UsageCount label="Output" value={model.output_tokens} />
              <UsageCount label="Cache read" value={model.cache_read_tokens} />
              <UsageCount label="Cache write, 5m" value={model.cache_write_5m_tokens} />
              <UsageCount label="Cache write, 1h" value={model.cache_write_1h_tokens} />
              <UsageCount label="Cache write, unknown" value={model.cache_write_unclassified_tokens} />
            </dl>
            {model.has_unpriced_usage ? (
              <p className="session-usage-unpriced">* {model.unpriced_reason || 'Some usage has no price.'}</p>
            ) : null}
          </section>
        ))}
      </div>
      {pinned ? <div className="session-usage-hint">esc close</div> : null}
    </div>
  );

  return createPortal(
    <FocusTrap
      active={pinned}
      focusTrapOptions={{
        allowOutsideClick: true,
        escapeDeactivates: false,
        initialFocus: () => containerRef.current ?? false,
        fallbackFocus: () => containerRef.current ?? document.body,
      }}
    >
      {content}
    </FocusTrap>,
    document.body,
  );
}

function UsageCount({ label, value }: { label: string; value: number }) {
  return <div><dt>{label}</dt><dd>{exactTokenFormatter.format(value)}</dd></div>;
}
