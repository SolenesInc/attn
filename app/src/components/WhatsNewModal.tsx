
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { formatShortcut, modifierTokens, shortcutTokens } from '../shortcuts/formatShortcut';
import { KeyCombos } from './Keycap';
import './WhatsNewModal.css';

interface WhatsNewModalProps {
  isOpen: boolean;
  onClose: () => void;
  onViewShortcuts: () => void;
}

interface Highlight {
  title: string;
  body: string;
  combos: string[][];
  flagged?: boolean;
}

function highlights(): Highlight[] {
  return [
  {
    flagged: true,
    title: 'Agents live on desktops',
    body: `A desktop is an arrangement of agents and tiles. ${formatShortcut('desktop.select1')} to ${formatShortcut('desktop.select9')} switch desktops; pressing the digit of the desktop you are on takes you back to the one before.`,
    combos: [[...modifierTokens('desktop.select1'), '1–9']],
  },
  {
    title: 'Send the focused pane elsewhere',
    body: 'Move the pane you are in to another desktop without leaving the keyboard.',
    combos: [[...modifierTokens('desktop.send1'), '1–9']],
  },
  {
    title: 'See every desktop at once',
    body: 'The overview lists every desktop, including extras past nine. Switch, send the focused pane, delete an empty desktop or give an extra a shortcut from there.',
    combos: [shortcutTokens('desktop.overview')],
  },
  {
    title: 'Profiles group everything',
    body: 'A profile holds its own agents, crew, automations and desktops, and remembers the desktop you were on. Switch profiles without closing anything.',
    combos: [shortcutTokens('profile.switch')],
  },
  {
    title: 'Every window agrees',
    body: 'The current desktop and the focused pane belong to the daemon, so every window on the same profile shows the same thing.',
    combos: [],
  },
  ];
}

export function WhatsNewModal({ isOpen, onClose, onViewShortcuts }: WhatsNewModalProps) {
  useEscapeStack(onClose, isOpen);

  if (!isOpen) return null;

  return (
    <div className="whats-new-overlay" onClick={onClose}>
      <FocusTrap
        focusTrapOptions={{
          allowOutsideClick: true,
          escapeDeactivates: false,
        }}
      >
        <div
          className="whats-new-modal"
          onClick={(e) => e.stopPropagation()}
          role="dialog"
          aria-modal="true"
          aria-labelledby="whats-new-title"
        >
          <div className="whats-new-header">
            <div className="whats-new-eyebrow">What's new</div>
            <h2 id="whats-new-title">attn is organized around desktops</h2>
            <button
              className="whats-new-close"
              onClick={onClose}
              aria-label="Close what's new"
              type="button"
            >
              ×
            </button>
          </div>

          <div className="whats-new-body">
            {highlights().map((highlight) => (
              <section
                className={`whats-new-item${highlight.flagged ? ' whats-new-item--key' : ''}`}
                key={highlight.title}
              >
                <div className="whats-new-item-head">
                  <h3>
                    {highlight.flagged && <span className="whats-new-tag">Changed</span>}
                    {highlight.title}
                  </h3>
                  {highlight.combos.length > 0 && (
                    <span className="whats-new-keys">
                      <KeyCombos combos={highlight.combos} />
                    </span>
                  )}
                </div>
                <p>{highlight.body}</p>
              </section>
            ))}
          </div>

          <div className="whats-new-footer">
            <button
              className="whats-new-link"
              onClick={onViewShortcuts}
              type="button"
            >
              View all shortcuts →
            </button>
            <button
              className="whats-new-primary"
              onClick={onClose}
              type="button"
            >
              Got it
            </button>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}
