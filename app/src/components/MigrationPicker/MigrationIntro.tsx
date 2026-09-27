import { useEffect, useRef } from 'react';
import { slotShortcut } from '../../utils/desktops';
import { WorkspaceExplainer } from './WorkspaceExplainer';

export const INTRO_SENTENCE = 'Each workspace you had is already its own desktop. Before you continue, confirm each one is where you want it.';

export type Step = 'intro' | 'place';

export function StepNav({ step, onIntro }: { step: Step; onIntro: () => void }) {
  return (
    <div className="mp-topline">
      <div className="mp-brand"><span className="mp-brandmark" aria-hidden="true" />attn</div>
      <nav className="mp-steps" aria-label="Migration steps">
        <button
          type="button"
          className={`mp-step-link ${step === 'intro' ? 'active' : 'completed'}`}
          aria-current={step === 'intro' ? 'step' : undefined}
          onClick={onIntro}
        >
          <i>01</i> What’s changing
        </button>
        <span aria-hidden="true">›</span>
        <span className={step === 'place' ? 'active' : ''} aria-current={step === 'place' ? 'step' : undefined}>
          <i>02</i> Confirm desktops
        </span>
      </nav>
    </div>
  );
}

export function Intro({ onStart }: { onStart: () => void }) {
  const startRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    startRef.current?.focus();
  }, []);
  return (
    <section className="mp-welcome" aria-labelledby="mp-welcome-title">
      <div className="mp-eyebrow">A new home for your sessions</div>
      <h1 id="mp-welcome-title">Your workspaces are now desktops.</h1>
      <p className="mp-welcome-lead">{INTRO_SENTENCE}</p>
      <WorkspaceExplainer />
      <div className="mp-change-list">
        <div>
          <span className="mp-change-icon" aria-hidden="true">▦</span>
          <div>
            <h2>Nothing to set up</h2>
            <p>Every workspace already has its desktop. The first nine open with <kbd>{slotShortcut(1)}</kbd>–<kbd>{slotShortcut(9)}</kbd>.</p>
          </div>
        </div>
        <div>
          <span className="mp-change-icon" aria-hidden="true">✓</span>
          <div>
            <h2>Confirm each one</h2>
            <p>Keep it where it is, move it, or merge it into another desktop. Nothing changes until you finish.</p>
          </div>
        </div>
        <div>
          <span className="mp-change-icon" aria-hidden="true">⊞</span>
          <div>
            <h2>Your splits stay yours</h2>
            <p>Sessions that were side by side stay side by side.</p>
          </div>
        </div>
      </div>
      <div className="mp-welcome-actions">
        <button type="button" ref={startRef} className="mp-button primary" onClick={onStart}>Continue →</button>
        <span>Your choices are saved as you go, in every attn window.</span>
      </div>
    </section>
  );
}
