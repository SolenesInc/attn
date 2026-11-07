import {
  useEffect,
  Fragment,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { usePortalTarget, useTileWindow } from '../tileWindow';
import { LABEL_COLOR_MAP, type QuickLabel } from './quickLabels';
import { placeQuickLabelPicker, QUICK_LABEL_PICKER_WIDTH } from './quickLabelPlacement';
import './QuickLabelPicker.css';

interface FloatingQuickLabelPickerProps {
  mode?: 'floating';
  className: string;
  groups: readonly (readonly QuickLabel[])[];
  anchorEl: HTMLElement;
  cursorHint?: { x: number; y: number } | null;
  onSelect: (label: QuickLabel) => void;
  onDismiss: () => void;
  onHint?: (hint: string | null) => void;
}

interface ChipQuickLabelPickerProps {
  mode: 'chips';
  className: string;
  groups: readonly (readonly QuickLabel[])[];
  isSelected: (label: QuickLabel) => boolean;
  onSelect: (label: QuickLabel) => void;
  onHint: (hint: string | null) => void;
  children?: ReactNode;
}

type QuickLabelPickerProps = FloatingQuickLabelPickerProps | ChipQuickLabelPickerProps;

function addDeferredPointerDownListener(target: Document, listener: (event: PointerEvent) => void): () => void {
  const timer = window.setTimeout(() => {
    target.addEventListener('pointerdown', listener, true);
  }, 0);
  return () => {
    window.clearTimeout(timer);
    target.removeEventListener('pointerdown', listener, true);
  };
}

function FloatingQuickLabelPicker({
  className,
  groups,
  anchorEl,
  cursorHint,
  onSelect,
  onDismiss,
  onHint,
}: FloatingQuickLabelPickerProps) {
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const [height, setHeight] = useState(0);
  const ref = useRef<HTMLDivElement>(null);
  const tileWindow = useTileWindow();
  const portalTarget = usePortalTarget();
  const { labels, indexedGroups } = useMemo(() => {
    const flattened = groups.flat();
    let nextIndex = 0;
    return {
      labels: flattened,
      indexedGroups: groups.map((group) =>
        group.map((label) => ({ label, index: nextIndex++ })),
      ),
    };
  }, [groups]);

  useEffect(() => {
    const update = () => setPosition(placeQuickLabelPicker(
      anchorEl.getBoundingClientRect(),
      cursorHint,
      height,
      { width: tileWindow.innerWidth, height: tileWindow.innerHeight },
    ));
    update();
    tileWindow.addEventListener('scroll', update, true);
    tileWindow.addEventListener('resize', update);
    return () => {
      tileWindow.removeEventListener('scroll', update, true);
      tileWindow.removeEventListener('resize', update);
    };
  }, [anchorEl, cursorHint, height, tileWindow]);

  useLayoutEffect(() => {
    const measured = ref.current?.offsetHeight ?? 0;
    if (measured > 0) {
      setHeight((current) => (current === measured ? current : measured));
    }
  });

  useEscapeStack(onDismiss, true);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const isDigit = (event.code >= 'Digit1' && event.code <= 'Digit9') || event.code === 'Digit0';
      if (isDigit && !event.ctrlKey && !event.metaKey) {
        event.preventDefault();
        const digit = parseInt(event.code.slice(5), 10);
        const index = digit === 0 ? 9 : digit - 1;
        if (index < labels.length) {
          onSelect(labels[index]);
        }
      }
    };
    tileWindow.addEventListener('keydown', handleKeyDown);
    return () => {
      tileWindow.removeEventListener('keydown', handleKeyDown);
    };
  }, [labels, onSelect, tileWindow]);

  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) {
        onDismiss();
      }
    };
    return addDeferredPointerDownListener(tileWindow.document, handlePointerDown);
  }, [onDismiss, tileWindow]);

  if (!position) {
    return null;
  }

  return createPortal(
    <div
      ref={ref}
      className={className}
      style={{ top: position.top, left: position.left, width: QUICK_LABEL_PICKER_WIDTH }}
      onMouseDown={(event) => event.stopPropagation()}
    >
      {indexedGroups.map((group, groupIndex) => (
        <Fragment key={group[0].label.id}>
          {groupIndex > 0 ? <hr className="md-quick-label-divider" /> : null}
          {group.map(({ label, index }) => {
            const color = LABEL_COLOR_MAP[label.color];
            return (
              <button
                key={label.id}
                type="button"
                className="md-quick-label-row"
                data-quick-label-id={label.id}
                onClick={() => onSelect(label)}
                title={label.tip}
                onMouseEnter={() => onHint?.(label.text)}
                onMouseLeave={() => onHint?.(null)}
                onFocus={() => onHint?.(label.text)}
                onBlur={() => onHint?.(null)}
              >
                <span
                  className="md-ql-chip"
                  style={color
                    ? ({
                        background: color.bg,
                        '--md-ql-text': color.text,
                        '--md-ql-text-dark': color.darkText,
                      } as CSSProperties)
                    : undefined}
                >
                  {label.emoji}
                </span>
                <span className="md-quick-label-text">{label.text}</span>
                {index < 10 && <span className="md-quick-label-num">{(index + 1) % 10}</span>}
              </button>
            );
          })}
        </Fragment>
      ))}
    </div>,
    portalTarget,
  );
}

function ChipQuickLabelPicker({
  className,
  groups,
  isSelected,
  onSelect,
  onHint,
  children,
}: ChipQuickLabelPickerProps) {
  return (
    <div className={className}>
      {groups.map((group, groupIndex) => (
        <Fragment key={group[0].id}>
          {groupIndex > 0 ? <span className="anno-popup-divider" /> : null}
          {group.map((label) => (
            <button
              key={label.id}
              type="button"
              className={`anno-popup-label${isSelected(label) ? ' anno-popup-label--on' : ''}`}
              title={label.text}
              aria-label={label.text}
              aria-pressed={isSelected(label)}
              onClick={() => onSelect(label)}
              onMouseEnter={() => onHint(label.text)}
              onMouseLeave={() => onHint(null)}
              onFocus={() => onHint(label.text)}
              onBlur={() => onHint(null)}
            >
              {label.emoji}
            </button>
          ))}
        </Fragment>
      ))}
      {children}
    </div>
  );
}

export function QuickLabelPicker(props: QuickLabelPickerProps) {
  if (props.mode === 'chips') {
    return <ChipQuickLabelPicker {...props} />;
  }
  return <FloatingQuickLabelPicker {...props} />;
}
