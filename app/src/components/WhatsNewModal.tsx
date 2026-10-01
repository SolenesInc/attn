import { useRef, useState, type KeyboardEvent } from 'react';
import FocusTrap from 'focus-trap-react';
import { useKeybindings } from '../contexts/KeybindingsContext';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { KeyCombos } from './Keycap';
import { whatsNewSteps } from './whatsNewSteps';
import './WhatsNewModal.css';

interface WhatsNewModalProps {
  isOpen: boolean;
  onClose: () => void;
  onViewShortcuts: () => void;
}

export function WhatsNewModal({ isOpen, onClose, onViewShortcuts }: WhatsNewModalProps) {
  useEscapeStack(onClose, isOpen);
  if (!isOpen) return null;
  return <WhatsNewTour onClose={onClose} onViewShortcuts={onViewShortcuts} />;
}

function WhatsNewTour({ onClose, onViewShortcuts }: Omit<WhatsNewModalProps, 'isOpen'>) {
  const { resolve } = useKeybindings();
  const steps = whatsNewSteps(resolve);
  const [index, setIndex] = useState(0);
  const primaryRef = useRef<HTMLButtonElement>(null);
  const step = steps[index];
  const last = index === steps.length - 1;

  const goTo = (next: number) => {
    const clamped = Math.min(steps.length - 1, Math.max(0, next));
    // Back disappears on the first step; keep the keyboard on the dialog.
    if (clamped === 0) primaryRef.current?.focus();
    setIndex(clamped);
  };

  const advance = () => (last ? onClose() : goTo(index + 1));

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
      event.preventDefault();
      event.stopPropagation();
      goTo(index + (event.key === 'ArrowRight' ? 1 : -1));
      return;
    }
    // Enter advances from the primary button or the dialog itself (a click on the scene
    // focuses it); on Back, a dot or the shortcuts link it keeps the button's own action.
    if (event.key === 'Enter' && (event.target === event.currentTarget || event.target === primaryRef.current)) {
      event.preventDefault();
      advance();
    }
  };

  return (
    <div className="whats-new-overlay" onClick={onClose}>
      <FocusTrap
        focusTrapOptions={{
          allowOutsideClick: true,
          escapeDeactivates: false,
          delayInitialFocus: false,
          initialFocus: () => primaryRef.current ?? false,
        }}
      >
        <div
          className="whats-new-modal"
          onClick={(e) => e.stopPropagation()}
          onKeyDown={handleKeyDown}
          tabIndex={-1}
          role="dialog"
          aria-modal="true"
          aria-label="What's new"
        >
          <div className="whats-new-header">
            <span className="whats-new-eyebrow">What's new</span>
            <span className="whats-new-count">{index + 1} of {steps.length}</span>
            <button className="whats-new-close" onClick={onClose} aria-label="Close what's new" type="button">
              ×
            </button>
          </div>

          <div className="whats-new-live" aria-live="polite">
            <section className="whats-new-step" key={step.id} data-testid={`whats-new-step-${step.id}`}>
              {step.scene}
              <h2>{step.title}</h2>
              <p>{step.body}</p>
              {step.keys.length > 0 && (
                <ul className="whats-new-keys">
                  {step.keys.map((entry) => (
                    <li key={entry.label}>
                      <KeyCombos combos={entry.combos} />
                      <span className="whats-new-key-label">{entry.label}</span>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>

          <div className="whats-new-footer">
            <button className="whats-new-link" onClick={onViewShortcuts} type="button">
              View all shortcuts →
            </button>
            <div className="whats-new-dots">
              {steps.map((entry, i) => (
                <button
                  key={entry.id}
                  type="button"
                  tabIndex={-1}
                  className={`whats-new-dot${i === index ? ' is-current' : ''}`}
                  aria-label={`Step ${i + 1}: ${entry.title}`}
                  aria-current={i === index ? 'step' : undefined}
                  onClick={() => {
                    goTo(i);
                    primaryRef.current?.focus();
                  }}
                />
              ))}
            </div>
            <div className="whats-new-nav">
              {index > 0 && (
                <button className="whats-new-back" onClick={() => goTo(index - 1)} type="button">
                  Back
                </button>
              )}
              <button
                ref={primaryRef}
                className="whats-new-primary"
                onClick={advance}
                type="button"
              >
                {last ? 'Got it' : 'Next'}
              </button>
            </div>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}
