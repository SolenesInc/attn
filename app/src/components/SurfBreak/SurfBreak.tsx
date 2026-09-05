import { useCallback, useEffect, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { Ocean, OCEAN_HEIGHT, OCEAN_WIDTH } from './ocean';
import { SurfControls, surfButtons } from './controls';
import { SurfSound } from './sound';
import { beaches, defaultConditions, type BeachId, type SurfConditions } from './beaches';
import './SurfBreak.css';

interface SurfBreakProps {
  waitingCount: number;
  connected?: boolean;
  onClose: () => void;
  onReturnToWaiting: () => void;
}

export function SurfIcon() {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
    <path d="M3 16c4 0 5-10 11-10 5 0 6 6 2 7 2-4-3-4-3-1 0 3 4 5 8 5M3 20c3 0 3-2 6-2s3 2 6 2 3-2 6-2" />
  </svg>;
}

export function SurfBreak({ waitingCount, connected = true, onClose, onReturnToWaiting }: SurfBreakProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [initialOcean] = useState(() => new Ocean({ beach: 'cove', start: 'beach' }));
  const ocean = useRef(initialOcean);
  const controls = useRef(new SurfControls());
  const sound = useRef<SurfSound | null>(null);
  const [started, setStarted] = useState(false);
  const [paused, setPaused] = useState(false);
  const [music, setMusic] = useState(true);
  const [audioError, setAudioError] = useState(false);
  const [armsCrossed, setArmsCrossed] = useState(false);
  const [showBoard, setShowBoard] = useState(false);
  const [posture, setPosture] = useState<'prone' | 'standing'>('prone');
  const [canStand, setCanStand] = useState(false);
  const [sceneState, setSceneState] = useState<Ocean['state']>('walking');
  const [wipeoutCause, setWipeoutCause] = useState<Ocean['wipeoutCause']>(null);
  const [beach, setBeach] = useState<BeachId>('cove');
  const [chosenBeach, setChosenBeach] = useState<BeachId>('cove');
  const [conditions, setConditions] = useState(defaultConditions);
  const [chosenConditions, setChosenConditions] = useState(defaultConditions);
  const [choosingBeach, setChoosingBeach] = useState(false);
  const [sceneVersion, setSceneVersion] = useState(0);
  const beachSelect = useRef<HTMLSelectElement>(null);
  const [foreground, setForeground] = useState(() => !document.hidden && document.hasFocus());
  const [reducedMotion, setReducedMotion] = useState(() => window.matchMedia('(prefers-reduced-motion: reduce)').matches);
  const active = started && !paused && foreground && !choosingBeach;
  useEscapeStack(() => {
    if (choosingBeach) { setChoosingBeach(false); canvasRef.current?.focus(); }
    else onClose();
  }, true);

  const clearInput = useCallback(() => { controls.current.clear(); }, []);
  const toggleArms = useCallback(() => {
    ocean.current.armsCrossed = !ocean.current.armsCrossed;
    setArmsCrossed(ocean.current.armsCrossed);
  }, []);
  const enableSound = useCallback(() => {
    try {
      sound.current ??= new SurfSound();
      void sound.current.play().catch(() => setAudioError(true));
      setAudioError(false);
    } catch { setAudioError(true); }
  }, []);
  const start = useCallback(() => {
    setStarted(true);
    setPaused(false);
    if (music) enableSound();
    canvasRef.current?.focus();
  }, [music, enableSound]);
  const toggleMusic = useCallback(() => {
    if (!music && active) enableSound();
    setMusic(value => !value);
  }, [music, active, enableSound]);

  useEffect(() => { if (choosingBeach) beachSelect.current?.focus(); }, [choosingBeach]);
  const applyBeach = () => {
    clearInput();
    ocean.current = new Ocean({ beach: chosenBeach, conditions: chosenConditions, start: 'beach' });
    setBeach(chosenBeach); setConditions({ ...chosenConditions });
    setPosture('prone'); setCanStand(false); setSceneState('walking'); setArmsCrossed(false); setWipeoutCause(null);
    setSceneVersion(value => value + 1); setChoosingBeach(false);
    canvasRef.current?.focus();
  };

  useEffect(() => {
    const syncForeground = () => {
      clearInput();
      setForeground(!document.hidden && document.hasFocus());
    };
    const blur = () => { clearInput(); setForeground(false); };
    const media = window.matchMedia('(prefers-reduced-motion: reduce)');
    const syncMotion = () => setReducedMotion(media.matches);
    const release = (event: KeyboardEvent) => controls.current.release(event.code);
    window.addEventListener('blur', blur);
    window.addEventListener('focus', syncForeground);
    window.addEventListener('keyup', release);
    document.addEventListener('visibilitychange', syncForeground);
    media.addEventListener('change', syncMotion);
    return () => {
      window.removeEventListener('blur', blur);
      window.removeEventListener('focus', syncForeground);
      window.removeEventListener('keyup', release);
      document.removeEventListener('visibilitychange', syncForeground);
      media.removeEventListener('change', syncMotion);
      void sound.current?.dispose().catch(() => {});
      sound.current = null;
    };
  }, [clearInput]);

  useEffect(() => {
    if (!active) clearInput();
    const audio = sound.current;
    if (!audio) return;
    void (active && music ? audio.play() : audio.pause()).catch(() => setAudioError(true));
  }, [active, music, clearInput]);

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext('2d', { alpha: false });
    if (!canvas || !ctx) return;
    ocean.current.draw(ctx, reducedMotion);
    if (!active) return;
    let frame = 0;
    let previous = performance.now();
    const draw = (now: number) => {
      if (now - previous >= 1000 / 30) {
        ocean.current.step((now - previous) / 1000, controls.current.input);
        controls.current.input.jump = false;
        controls.current.input.posture = false;
        setPosture(ocean.current.posture);
        setCanStand(ocean.current.canStand && !controls.current.input.dive);
        setSceneState(ocean.current.state);
        setWipeoutCause(ocean.current.wipeoutCause);
        ocean.current.draw(ctx, reducedMotion);
        canvas.dataset.surfState = ocean.current.state;
        canvas.dataset.waveCount = String(ocean.current.waves.length);
        canvas.dataset.depth = ocean.current.depth.toFixed(1);
        canvas.dataset.line = ocean.current.z.toFixed(1);
        canvas.dataset.heading = ocean.current.heading.toFixed(2);
        canvas.dataset.stance = ocean.current.stance.toFixed(2);
        canvas.dataset.barrel = String(ocean.current.cover > 0);
        canvas.dataset.posture = ocean.current.posture;
        canvas.dataset.worldX = ocean.current.x.toFixed(1);
        canvas.dataset.beach = ocean.current.beach.id;
        if (music) sound.current?.setSurf(ocean.current.speed, ocean.current.time);
        previous = now;
      }
      frame = requestAnimationFrame(draw);
    };
    frame = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frame);
  }, [active, music, reducedMotion, sceneVersion]);

  return (
    <FocusTrap focusTrapOptions={{ initialFocus: () => canvasRef.current!, escapeDeactivates: false, allowOutsideClick: false }}>
      <section className="surf-break" role="dialog" aria-modal="true" aria-label="Swell surfing break"
        data-playing={active} data-audio={audioError ? 'unavailable' : active && music ? 'playing' : 'silent'}
        onKeyDown={event => {
          if (event.metaKey || event.ctrlKey || event.altKey) return;
          if (event.target !== canvasRef.current) return;
          const key = event.key.toLowerCase();
          if (key === 'm' && !event.repeat) { event.preventDefault(); toggleMusic(); }
          if (key === 'p' && started && !event.repeat) { event.preventDefault(); setPaused(value => !value); }
          if (key === 'enter' && event.target === canvasRef.current) {
            event.preventDefault();
            if (!started) start(); else if (connected && waitingCount > 0) onReturnToWaiting();
          }
          if (!active) return;
          if (controls.current.press(event.code)) event.preventDefault();
          if (event.code === 'KeyC' && !event.repeat) { event.preventDefault(); toggleArms(); }
          if (event.code === 'KeyF' && !event.repeat) {
            event.preventDefault();
            controls.current.input.posture = true;
          }
          if (event.code === 'Space' && event.target === canvasRef.current) {
            event.preventDefault();
            if (!event.repeat) controls.current.input.jump = true;
          }
        }}>
        <canvas ref={canvasRef} width={OCEAN_WIDTH} height={OCEAN_HEIGHT} tabIndex={0}
          aria-label="Surf at your own pace. Walk right from the beach into the water. Arrows or WASD walk, paddle while lying down, and carve while standing. F stands up when moving fast enough or lies down. Slowing down returns you to paddling. Up moves away from the screen, down moves toward it. Hold Shift to dive, release to float up. Space jumps while standing. Q steps to the tail, E to the nose, C crosses your arms. P pauses, M toggles sound, Escape returns to attn."
          aria-describedby="surf-instructions" />
        <header className="surf-header">
          <div className="surf-name"><SurfIcon /><span>Swell</span></div>
          <button type="button" className="surf-beach-button" aria-expanded={choosingBeach} onClick={() => {
            clearInput(); setChosenBeach(beach); setChosenConditions({ ...conditions }); setChoosingBeach(value => !value);
          }}>{beaches[beach].name} <span aria-hidden="true">⌄</span></button>
          <button type="button" onClick={onClose}>Back to attn <kbd>esc</kbd></button>
        </header>
        {choosingBeach && <div className="surf-beach-picker" role="group" aria-label="Choose your beach">
          <label>Beach<select ref={beachSelect} value={chosenBeach} onChange={event => setChosenBeach(event.target.value as BeachId)}>
            {Object.values(beaches).map(place => <option key={place.id} value={place.id}>{place.name}</option>)}
          </select></label>
          <p>{beaches[chosenBeach].description}</p>
          <div className="surf-condition-fields">
            <label>Wave size<select value={chosenConditions.size} onChange={event => setChosenConditions(value => ({ ...value, size: event.target.value as SurfConditions['size'] }))}>
              <option value="small">Smaller</option><option value="usual">Usual</option><option value="large">Larger</option>
            </select></label>
            <label>Time between sets<select value={chosenConditions.rhythm} onChange={event => setChosenConditions(value => ({ ...value, rhythm: event.target.value as SurfConditions['rhythm'] }))}>
              <option value="quiet">Long rests</option><option value="steady">Steady</option><option value="frequent">Frequent sets</option>
            </select></label>
          </div>
          <div className="surf-picker-actions">
            <button type="button" onClick={() => { setChoosingBeach(false); canvasRef.current?.focus(); }}>Stay here</button>
            <button type="button" className="surf-start" onClick={applyBeach}>Start on this beach</button>
          </div>
        </div>}
        {!started && !choosingBeach && <div className="surf-invitation">
          <h1>Take your time.</h1>
          <p>Walk down to the water. There’s always another wave.</p>
          <button type="button" className="surf-start" onClick={start}>Into the water <span aria-hidden="true">↵</span></button>
        </div>}
        {started && !active && !choosingBeach && <div className="surf-pause">
          <p>The ocean can wait.</p>
          <button type="button" onClick={start}>Keep surfing</button>
        </div>}
        <footer className="surf-footer">
          <div className="surf-controls" id="surf-instructions">
            {surfButtons.filter(button => showBoard || (button.action !== 'tail' && button.action !== 'nose')).map(button => <button key={button.action} type="button"
              aria-label={button.name}
              onPointerDown={event => {
                event.currentTarget.setPointerCapture(event.pointerId);
                canvasRef.current?.focus();
                event.preventDefault();
                if (active) controls.current.holdPointer(event.pointerId, button.action);
              }}
              onPointerUp={event => controls.current.releasePointer(event.pointerId)}
              onPointerCancel={event => controls.current.releasePointer(event.pointerId)}
              onLostPointerCapture={event => controls.current.releasePointer(event.pointerId)}>
              <kbd>{button.key}</kbd> {button.label}
            </button>)}
            <button type="button" aria-pressed={posture === 'standing'} disabled={!active || (posture === 'prone' && !canStand)}
              title={posture === 'prone' && !canStand ? 'Paddle to build enough speed to stand' : undefined}
              onClick={() => {
                if (active) controls.current.input.posture = true;
                canvasRef.current?.focus();
              }}><kbd>f</kbd> {posture === 'standing' ? 'lie down' : 'stand up'}</button>
            <button type="button" className="surf-jump" disabled={!active || posture !== 'standing'} onClick={() => {
              if (active) controls.current.input.jump = true;
              canvasRef.current?.focus();
            }}><kbd>space</kbd> jump</button>
            {showBoard && <button type="button" aria-pressed={armsCrossed} onClick={() => {
              if (active) toggleArms();
              canvasRef.current?.focus();
            }}><kbd>c</kbd> {armsCrossed ? 'unfold arms' : 'cross arms'}</button>}
            <button type="button" aria-expanded={showBoard} onClick={() => setShowBoard(value => !value)}>Board moves {showBoard ? '−' : '+'}</button>
          </div>
          <p className="surf-rest">{sceneState === 'walking' || sceneState === 'wading' ? 'Walk right into the water. You’ll start paddling when it gets deep enough.'
            : sceneState === 'recovering' ? `${wipeoutCause === 'stall' ? 'Lost speed in the breaking face.' : wipeoutCause === 'closeout' ? 'The wave closed over you.' : 'Caught by the lip.'} Keep steering; you’ll be paddling again shortly.`
              : showBoard ? 'While standing: Q / E walk the board. C holds your pose.'
            : posture === 'standing' ? 'Carve with arrows or WASD. F lies down. Shift dives.'
              : canStand ? 'Paddle left with the wave. When it carries you, F stands up.' : 'Paddle right to head out, left to catch a wave. F stands up. Shift dives.'}</p>
          <div className="surf-footer-bottom">
            <div className="surf-audio-controls">
              <button type="button" onClick={toggleMusic} aria-pressed={music}>
                {audioError ? 'Sound unavailable' : music ? 'Sound on' : 'Sound off'} <kbd>m</kbd>
              </button>
              {started && <button type="button" onClick={() => setPaused(value => !value)}>
                {paused ? 'Resume' : 'Pause'} <kbd>p</kbd>
              </button>}
            </div>
            {connected && waitingCount > 0 ? <button type="button" className="surf-waiting" onClick={onReturnToWaiting}>
              <span className="surf-waiting-dot" />{waitingCount} {waitingCount === 1 ? 'agent' : 'agents'} waiting <kbd>↵</kbd>
            </button> : <span className="surf-status">{connected ? 'No agents waiting.' : 'Reconnecting to your agents…'}</span>}
          </div>
        </footer>
      </section>
    </FocusTrap>
  );
}
